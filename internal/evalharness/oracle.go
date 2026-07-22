package evalharness

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// oracle.go answers a question the eval set's own report card was getting wrong: not "does this case
// HAVE an oracle?" but "can that oracle ever say NO?".
//
// The distinction was invisible until a real repository supplied a degenerate one. P1 discovery over
// nousresearch/hermes-agent emits `{"type":"object"}` as the I/O contract for all 40 of its nodes —
// its syntactic frontend does not resolve types. That schema ACCEPTS EVERY OBJECT, so the
// schema-validity evaluator returned 1 for every output of every variant, and a variant answering
// wrong 70% of the time scored task_success 1.000 and passed the min-quality gate. Oracle coverage
// reported 4% (2 of 53) when the truthful figure was 0.
//
// This is the same defect class as a metric whose variants are statistically indistinguishable
// deciding the ranking (scoring.separates). An oracle that cannot reject anything discriminates
// nothing, and counting it as evidence is how a board becomes confident about nothing.
//
// So the oracle is PROBED rather than assumed: run it against structurally diverse documents and see
// whether it ever says no. Measure, do not trust the declaration — the same rule the rest of the
// phase applies to everything else.

// Discriminating power is measured WITHIN THE TYPE THE CONTRACT DECLARES, not across all of JSON.
//
// This distinction is the whole of the fix, and the first attempt got it wrong. `{"type":"object"}`
// does reject `null`, `0` and `[]` — so a probe set spanning all of JSON scores it "decisive". But a
// workflow that emits objects never emits `null`; the only question that matters is whether the
// contract can tell one OBJECT from another, and that one it cannot answer. Rejecting inputs the
// workflow could never produce is not evidence.
//
// So the probes are drawn from the declared type. A contract that accepts every plausible document
// of its own declared shape decides nothing about that workflow.
var probesByType = map[string][]string{
	"object": {
		`{}`,
		`{"a":"correct"}`,
		`{"a":"wrong"}`,
		`{"unexpected":123,"nested":{"x":[1,2]}}`,
	},
	"array":   {`[]`, `["correct"]`, `["wrong"]`, `[1,2,3]`},
	"string":  {`""`, `"correct"`, `"wrong"`},
	"number":  {`0`, `1`, `-1.5`},
	"integer": {`0`, `1`, `-1`},
	"boolean": {`true`, `false`},
}

// untypedProbes span JSON's shape space, for a contract that declares no top-level type at all —
// there, anything is plausible, so anything is fair to probe with.
var untypedProbes = []string{
	`{}`, `{"a":"correct"}`, `{"a":"wrong"}`, `[]`, `"a bare string"`, `0`, `null`,
}

// oracleProbes is the regex probe corpus: a pattern is matched against raw output bytes, so the
// plausible-output question is "can it reject some output this workflow might emit?".
var oracleProbes = []string{
	`{}`, `{"a":"correct"}`, `{"a":"wrong"}`, `{"unexpected":123}`,
}

// probesFor returns the probe corpus for a schema: the plausible documents of its declared type,
// PLUS one probe per declared constraint that deliberately violates it.
//
// The constraint-derived half is what the first version missed. A contract declaring
// `properties: {a: {type: string}}` accepts every generic object probe — `{}` omits `a`,
// `{"a":"correct"}` satisfies it — and would have been scored indecisive. It is not: it rejects
// `{"a": 42}`. Probing the SHAPE alone tests whether the contract narrows the type; probing its
// declared constraints tests whether it narrows anything at all. Both are needed.
func probesFor(raw json.RawMessage) []string {
	var decl struct {
		Type       any                        `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
		Enum       []any                      `json:"enum"`
	}
	if err := json.Unmarshal(raw, &decl); err != nil {
		return untypedProbes
	}

	var probes []string
	switch t := decl.Type.(type) {
	case string:
		probes = append(probes, probesByType[t]...)
	case []any:
		// A union of types: probe every arm, since the contract admits all of them.
		for _, arm := range t {
			if name, ok := arm.(string); ok {
				probes = append(probes, probesByType[name]...)
			}
		}
	}
	if len(probes) == 0 {
		probes = append(probes, untypedProbes...)
	}

	// One violating probe per declared property: the wrong type for its declared type.
	for name, prop := range decl.Properties {
		var p struct {
			Type any `json:"type"`
		}
		if json.Unmarshal(prop, &p) != nil {
			continue
		}
		wrong := `42`
		if t, _ := p.Type.(string); t == "integer" || t == "number" {
			wrong = `"not a number"`
		}
		if b, err := json.Marshal(name); err == nil {
			probes = append(probes, `{`+string(b)+`:`+wrong+`}`)
		}
	}
	// An object omitting every required property.
	if len(decl.Required) > 0 {
		probes = append(probes, `{}`)
	}
	// A value outside a declared enum.
	if len(decl.Enum) > 0 {
		probes = append(probes, `"__not_in_enum__"`)
	}
	return probes
}

// HasOracle reports whether the case carries anything that COULD decide success: a reference output,
// an output schema, or a regex. It is a presence check, used by Validate for the gold/weak label rule
// — a case that carries a schema is carrying an oracle, however weak, and must be labeled as such.
func (c Case) HasOracle() bool {
	return len(c.Reference) > 0 || len(c.OutputSchema) > 0 || c.Pattern != ""
}

// OracleVerdict is why an oracle is or is not decisive, in words a coverage report can print.
type OracleVerdict struct {
	Decisive bool   `json:"decisive"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason,omitempty"`
}

// DecisiveOracle probes the case's oracle and reports whether it can ever return "no".
//
// Precedence matches SuccessOracleFor: a reference is checked first, because an exact-match oracle is
// decisive by construction — some output differs from it.
func (c Case) DecisiveOracle() OracleVerdict {
	if len(c.Reference) > 0 {
		// Exact match against a fixed reference can always fail: any other output does.
		return OracleVerdict{Decisive: true, Kind: "reference"}
	}
	if len(c.OutputSchema) > 0 {
		return probeSchema(c.OutputSchema)
	}
	if c.Pattern != "" {
		return probeRegex(c.Pattern)
	}
	return OracleVerdict{Kind: "none", Reason: "the case carries no reference, schema or pattern"}
}

// HasDecisiveOracle is the predicate the eval set's report card counts.
func (c Case) HasDecisiveOracle() bool { return c.DecisiveOracle().Decisive }

// probeSchema reports whether an output schema rejects anything at all.
func probeSchema(raw json.RawMessage) OracleVerdict {
	sch, err := CompileSchema(raw)
	if err != nil {
		// An uncompilable schema decides nothing — it makes every case it is attached to a skip.
		return OracleVerdict{Kind: "schema", Reason: "the output schema does not compile: " + err.Error()}
	}
	for _, probe := range probesFor(raw) {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(probe)))
		if err != nil {
			continue
		}
		if sch.Validate(doc) != nil {
			return OracleVerdict{Decisive: true, Kind: "schema"}
		}
	}
	return OracleVerdict{
		Kind: "schema",
		Reason: "the output schema accepts every plausible document of its own declared type, so it " +
			"can never fail — an unconstrained contract (typically `{\"type\":\"object\"}` from a " +
			"frontend that does not resolve types) is not a decision procedure",
	}
}

// probeRegex reports whether a pattern rejects anything at all.
func probeRegex(pattern string) OracleVerdict {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return OracleVerdict{Kind: "regex", Reason: "the pattern does not compile: " + err.Error()}
	}
	for _, probe := range oracleProbes {
		if !re.MatchString(probe) {
			return OracleVerdict{Decisive: true, Kind: "regex"}
		}
	}
	return OracleVerdict{
		Kind:   "regex",
		Reason: "the pattern matches every probe document, so it can never fail",
	}
}
