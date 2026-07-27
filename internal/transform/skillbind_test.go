package transform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P14 Wave 14a — skill materialization (tasks 2.1–2.5).
//
// The property under test throughout this file is not "a diff was produced". It is "the diff's SHAPE
// came from the sealed contract, and everything the engine could not derive from a contract was
// refused by name". A test that only asserted an edit exists would pass for a materializer that
// invented a schema, which is the exact failure decisions.md D-14.3 says must never look like success.

// skillEntry builds a resolved skill entry the way the registry would: the schemas go through the same
// hermetic compile, so a test can never assert against a contract the registry would have rejected.
func skillEntry(t *testing.T, name, inputSchema string) *registry.SkillEntry {
	t.Helper()
	return skillEntryWithOutput(t, name, inputSchema, `{"type":"object"}`)
}

func skillEntryWithOutput(t *testing.T, name, inputSchema, outputSchema string) *registry.SkillEntry {
	t.Helper()
	spec := registry.SkillSpec{
		ImplHandle:   "builtin:" + name,
		InputSchema:  json.RawMessage(inputSchema),
		OutputSchema: json.RawMessage(outputSchema),
	}
	e, err := registry.NewSkillEntry(strings.Repeat("s", 64), name, spec)
	if err != nil {
		t.Fatalf("build skill entry %q: %v", name, err)
	}
	return e
}

// ── 2.1 the materialized shape comes from the SEALED schema ──────────────────────────────────────

func TestSkillMaterializedMatchesSealedSchema(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	entry := skillEntry(t, "search_kb", `{
		"type": "object",
		"properties": {"query": {"type": "string"}, "top_k": {"type": "integer", "default": 5}},
		"required": ["query"]
	}`)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["agent"]: {Skills: []*registry.SkillEntry{entry}},
	}), root)
	if err != nil {
		t.Fatalf("a bound skill must materialize on a supported (language, provider) pair, got: %v", err)
	}
	got := string(p.Files["pipeline.go"])

	// The constructed value is the SDK's tool shape, carrying the skill's registered name...
	if !strings.Contains(got, `anthropic.ToolParam{Name: "search_kb"`) {
		t.Fatalf("the materialized tool value does not name the bound skill:\n%s", p.Diff)
	}
	// ...and the property bag is the SEALED schema's, key for key. This is the assertion that separates
	// "constructed from the contract" from "constructed from something plausible".
	for _, want := range []string{
		`"query": map[string]any{"type": "string"}`,
		`"top_k": map[string]any{"default": 5, "type": "integer"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("materialized properties are missing %s; the shape must come from the sealed schema, not be inferred:\n%s", want, p.Diff)
		}
	}
	// A schema key the sealed contract does NOT declare must not appear: an invented property is a tool
	// the model can call wrongly, and it would compile.
	if strings.Contains(got, `"limit"`) {
		t.Errorf("the materialized value carries a property the sealed schema never declared:\n%s", p.Diff)
	}
	if len(p.Touched) != 1 || p.Touched[0].Dim != "skills" {
		t.Errorf("the patch must record the skills dimension as touched, got %+v", p.Touched)
	}
}

// Two skills that share a NAME but pin different sealed schemas must materialize differently. If the
// shape were inferred from the call site (which is identical in both runs), they would not.
func TestSkillMaterializedShapeComesFromContractNotValue(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	materialize := func(schema string) string {
		t.Helper()
		p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
			ids["agent"]: {Skills: []*registry.SkillEntry{skillEntry(t, "lookup", schema)}},
		}), root)
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
		return string(p.Files["pipeline.go"])
	}

	a := materialize(`{"type":"object","properties":{"id":{"type":"string"}}}`)
	b := materialize(`{"type":"object","properties":{"id":{"type":"integer"}}}`)
	if a == b {
		t.Fatal("two skills sharing a name but pinning different sealed schemas materialized identically; " +
			"the shape is being inferred from the call site rather than read from the contract")
	}
}

// A bound skill REPLACES a tool list the call site already wrote — a deletion-and-construction the
// engine can stand behind, because it can see exactly what it is dropping.
func TestSkillMaterializedReplacesAStaticToolList(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["toolbound"]: {Skills: []*registry.SkillEntry{
			skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)}},
	}), root)
	if err != nil {
		t.Fatalf("replacing a static tool list must be applicable, got: %v", err)
	}
	if !strings.Contains(string(p.Files["pipeline.go"]), `Name: "search_kb"`) {
		t.Errorf("the written tool list was not replaced by the bound skill:\n%s", p.Diff)
	}
}

// Determinism: same resolved spec, same tree -> byte-identical materialized diff (task 7.7's Go half,
// asserted here too because a map-iterating schema renderer would only fail intermittently elsewhere).
func TestSkillToolTransformDeterministic(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	schema := `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"},"m":{"enum":["x","y"]}}}`

	var first string
	for i := 0; i < 8; i++ {
		p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
			ids["agent"]: {Skills: []*registry.SkillEntry{skillEntry(t, "wide", schema)}},
		}), root)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = p.DiffHash
			continue
		}
		if p.DiffHash != first {
			t.Fatalf("run %d produced diff hash %s, run 0 produced %s; a schema renderer that iterates a "+
				"map makes the codemod's output depend on hash seeding", i, p.DiffHash, first)
		}
	}
}

// ── 2.2 the interim refusal, per language and per provider ───────────────────────────────────────

// A language with no landed materializer refuses — and refuses BEFORE producing any part of a diff.
//
// 🔴 It runs against a REAL Python fixture, not a Go tree relabelled "python". Under a relabelled tree
// Generate fails earlier, at "no Python call site with this node_id", and the test would go green
// without the skills dimension ever being dispatched — passing for a reason that has nothing to do with
// what it claims to prove.
func TestUnappliedSkillRefusesAndEmitsNoDiff(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")
	entry := skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)

	r := resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Skills: []*registry.SkillEntry{entry}},
	})
	p, err := Generate(r, root)
	if err == nil {
		t.Fatal("a skill binding in a language with no materializer must refuse, not silently succeed")
	}
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite naming the refusal, got %v", err)
	}
	if p != nil {
		t.Fatalf("a refusal must emit NO diff, got a patch with %d file(s)", len(p.Files))
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.NodeID != id || re.Dim != "skills" {
		t.Fatalf("the refusal must name the node and the skills dimension, got %v", err)
	}
	mustContain(t, re.Detail, "search_kb", "the skill the user asked for")
	mustContain(t, re.Detail, "no materializer for this language has landed yet",
		"that the language, not the request, is the limit")
	mustContain(t, re.Detail, "REFUSED rather than dropped", "that this is not a silent drop")
}

// The same refusal, exercised through the span engine's own dispatch rather than through a language
// mismatch, so the refusal names the node and the `skills` dimension the way D-14.3 requires.
func TestGenerate_SpanSkillRefusalNamesNodeAndDimension(t *testing.T) {
	entry := skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)
	err := refuseSkills("node-1", variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{entry}})
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite, got %v", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("want a *RewriteError, got %T", err)
	}
	if re.NodeID != "node-1" || re.Dim != "skills" {
		t.Errorf("the refusal must name node and dimension, got node=%q dim=%q", re.NodeID, re.Dim)
	}
	if !strings.Contains(re.Detail, "search_kb") {
		t.Errorf("the refusal must name the skill the user asked for, got: %s", re.Detail)
	}
}

// A Go call site whose PROVIDER has no declared tool-value form refuses by name. "Go is supported" is
// not "every Go call site is supported", and the message has to say which half is missing (D-14.4).
func TestSkillRefusesProviderWithNoDeclaredToolForm(t *testing.T) {
	for _, c := range MaterializerCoverage() {
		if c.Provider == "cohere" {
			t.Skip("cohere gained a declared form; pick another uncovered provider for this test")
		}
	}
	form, covered := toolValueForms["anthropic"]
	if !covered || form.sdkNote == "" {
		t.Fatal("the anthropic row must declare the SDK generation its spelling targets")
	}

	root := newTarget(t)
	ids := nodeIDs(t, root)
	// The fixture's call sites are anthropic; drop the form for the duration of this test to prove the
	// refusal is driven by the TABLE rather than by an accident of the fixture.
	delete(toolValueForms, "anthropic")
	t.Cleanup(func() { toolValueForms["anthropic"] = form })

	_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["agent"]: {Skills: []*registry.SkillEntry{
			skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)}},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("an uncovered provider must refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the refusal must name the provider it has no form for, got: %v", err)
	}
}

// A tool set assembled at runtime is refused, never overwritten: the engine cannot see what it would
// be discarding, and a binding that silently dropped a runtime-built tool set would compile.
func TestSkillBindingRefusesDynamicToolSet(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["dynamictools"]: {Skills: []*registry.SkillEntry{
			skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)}},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("binding over a runtime-assembled tool set must refuse, got %v", err)
	}
	if p != nil {
		t.Fatal("a refusal must emit no diff")
	}
}

// A sealed schema this engine cannot render into a call-site literal is refused rather than rendered
// as an empty tool — an empty bag is a valid tool that accepts nothing, which compiles and then fails
// every call the model makes.
func TestSkillWithNoPropertiesRefusesRatherThanEmptyTool(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["agent"]: {Skills: []*registry.SkillEntry{skillEntry(t, "opaque", `{"type":"object"}`)}},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("a contract with no property bag must refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "properties") {
		t.Errorf("the refusal must say what the contract was missing, got: %v", err)
	}
}

// ── 2.3 🚫 a silent drop is a FAILING test (the go-red gate) ──────────────────────────────────────

// TestSilentSkillDropIsAFailingTest is the go-red gate D-14.3 asks for.
//
// It writes down, as an executable assertion, the behavior that must NEVER be accepted: an
// un-applicable SkillRef that "succeeds" by being dropped from the override while the rest of the diff
// proceeds. The node would then run without its binding while `config_hash` still claimed it, and the
// eval would score a configuration that never existed — an L1 correctness defect wearing the costume
// of a completed diff.
//
// The test asserts BOTH halves, because either alone can be satisfied by the wrong implementation:
// the un-applicable binding produces an error (not a success), AND the rest of the spec's diff does
// not appear (not a partial diff).
func TestSilentSkillDropIsAFailingTest(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	// Two overrides in ONE spec: a model swap that WOULD apply cleanly, and a skill binding over a
	// runtime-assembled tool set that cannot. A "drop the un-applicable one" implementation returns the
	// model diff and no error; that is the failure this test exists to catch.
	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
		ids["dynamictools"]: {Skills: []*registry.SkillEntry{
			skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)}},
	}), root)

	if err == nil {
		t.Fatal("an un-applicable skill binding was DROPPED and the spec reported success; the node would " +
			"run without the binding its config_hash claims, and the eval would score a configuration that " +
			"never existed")
	}
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite naming the refusal, got %v", err)
	}
	if p != nil {
		t.Fatal("a PARTIAL diff was emitted alongside the refusal; the applicable half of a rejected spec " +
			"must not ship on its own")
	}
}

// ── 2.4 a materialized skill's arguments are validated BEFORE execution ──────────────────────────

func TestMaterializedSkillArgsValidatedBeforeExecution(t *testing.T) {
	entry := skillEntry(t, "search_kb", `{
		"type": "object",
		"properties": {"query": {"type": "string"}},
		"required": ["query"],
		"additionalProperties": false
	}`)
	bound := BoundSkills("node-1", variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{entry}})
	if len(bound) != 1 {
		t.Fatalf("want one bound skill, got %d", len(bound))
	}

	invoked := false
	impl := func(map[string]any) (map[string]any, error) {
		invoked = true
		return map[string]any{"ok": true}, nil
	}

	// An out-of-contract argument object: `query` is required and this omits it.
	res := bound[0].Invoke("search", map[string]any{"q": "hello"}, impl)
	if invoked {
		t.Fatal("the implementation was invoked for arguments that violate the skill's sealed input " +
			"contract; validation must happen BEFORE the implementation runs")
	}
	if res["status"] != "error" {
		t.Fatalf("an out-of-contract argument must be reported as an error, got %v", res["status"])
	}
	if res["error_code"] != string(toolcontract.ErrorCodeValidationError) {
		t.Errorf("want a VALIDATION_ERROR for a contract violation, got %v", res["error_code"])
	}

	// The same skill with conforming arguments runs.
	res = bound[0].Invoke("search", map[string]any{"query": "hello"}, impl)
	if !invoked {
		t.Fatal("conforming arguments must reach the implementation")
	}
	if res["status"] != "ok" {
		t.Fatalf("conforming arguments must succeed, got %v", res)
	}
}

// The bind site validates against the SEALED contract, not against a permissive default: a skill whose
// version pinned a tighter schema rejects an argument the looser one would have admitted.
func TestBoundSkillValidatesAgainstItsOwnSealedContract(t *testing.T) {
	tight := skillEntry(t, "lookup", `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`)
	loose := skillEntry(t, "lookup", `{"type":"object","properties":{"id":{}},"required":["id"]}`)

	args := map[string]any{"id": "not-an-integer"}
	if err := (BoundSkills("n", variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{tight}})[0]).ValidateArgs(args); err == nil {
		t.Error("the tighter sealed contract must reject a string where it pinned an integer")
	}
	if err := (BoundSkills("n", variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{loose}})[0]).ValidateArgs(args); err != nil {
		t.Errorf("the looser sealed contract admits this argument; got %v", err)
	}
}

// ── 2.5 failures stay inside the toolcontract whitelist ──────────────────────────────────────────

// TestBoundSkillErrorsStayInWhitelist walks every failure a bound skill can surface and asserts the
// reported code is allowlisted.
//
// `tool_error_rate` is one of the two metrics P14 promises will score a skill/tool change with NO eval
// change (design Decision 6). A metric over error codes is well-defined only if the code set is closed,
// so a bound skill that invented a code would make a shipped metric's denominator depend on which skill
// happened to fail.
func TestBoundSkillErrorsStayInWhitelist(t *testing.T) {
	entry := skillEntryWithOutput(t, "search_kb",
		`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`,
		`{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"]}`)
	bound := BoundSkills("node-1", variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{entry}})[0]

	cases := []struct {
		name string
		res  map[string]any
	}{
		{"input violates the sealed contract",
			bound.Invoke("search", map[string]any{"nope": 1}, func(map[string]any) (map[string]any, error) {
				return nil, nil
			})},
		{"the implementation handle is not bound",
			bound.Invoke("search", map[string]any{"query": "x"}, nil)},
		{"the implementation failed",
			bound.Invoke("search", map[string]any{"query": "x"}, func(map[string]any) (map[string]any, error) {
				return nil, errors.New("upstream refused")
			})},
		{"output violates the sealed contract",
			bound.Invoke("search", map[string]any{"query": "x"}, func(map[string]any) (map[string]any, error) {
				return map[string]any{"wrong": true}, nil
			})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.res["status"] != "error" {
				t.Fatalf("want an error envelope, got %v", tc.res)
			}
			code, ok := tc.res["error_code"].(string)
			if !ok {
				t.Fatalf("the envelope carries no error_code: %v", tc.res)
			}
			if !toolcontract.IsAllowedErrorCode(toolcontract.ParseErrorCode(code)) {
				t.Fatalf("error code %q is outside ErrorCodeWhitelist; tool_error_rate stops being "+
					"well-defined the moment a bound skill can invent a code", code)
			}
		})
	}

	// The success path carries no error code at all — an "ok" that also reported a code would inflate
	// tool_error_rate from the numerator side.
	ok := bound.Invoke("search", map[string]any{"query": "x"}, func(map[string]any) (map[string]any, error) {
		return map[string]any{"hits": []any{}}, nil
	})
	if ok["status"] != "ok" {
		t.Fatalf("a conforming call must succeed, got %v", ok)
	}
	if _, has := ok["error_code"]; has {
		t.Errorf("a successful bound-skill call must carry no error_code, got %v", ok["error_code"])
	}
}

// ── NFR7 / task 9.4: coverage is stated in ONE place ─────────────────────────────────────────────

// The capability a doc claims and the refusal a user reads must come from the same table. This asserts
// they do — MaterializerCoverage is derived from toolValueForms, not written alongside it.
func TestMaterializerCoverageIsDerivedFromTheFormTable(t *testing.T) {
	cov := MaterializerCoverage()
	if len(cov) != len(toolValueForms) {
		t.Fatalf("coverage lists %d pair(s) but the form table has %d row(s); the two have drifted",
			len(cov), len(toolValueForms))
	}
	for _, c := range cov {
		if c.Language != "go" {
			t.Errorf("only Go materializes today (D-14.4), coverage claims %q", c.Language)
		}
		form, ok := toolValueForms[c.Provider]
		if !ok {
			t.Fatalf("coverage claims provider %q, which the form table does not carry", c.Provider)
		}
		if c.SDK == "" || c.SDK != form.sdkNote {
			t.Errorf("provider %q's coverage must state the SDK generation its spelling targets", c.Provider)
		}
	}
}

// ── task 9.4 / NFR7: the coverage DOC cannot drift from the coverage TABLE ────────────────────────

// TestCoverageDocMatchesTheFormTable is the gate that makes docs/decisions/p14-materializer-coverage.md
// a checked copy rather than a second source of truth.
//
// A capability doc and an engine refusal drift in one direction: the document stays optimistic and the
// engine does not. A user is then told a change is supported, asks for it, and gets a refusal that
// contradicts the page they read it on. Copying the table is fine; copying it WITHOUT a gate is what
// creates that failure, so this test reads the checked-in markdown and asserts it says exactly what
// toolValueForms says.
func TestCoverageDocMatchesTheFormTable(t *testing.T) {
	const doc = "../../docs/decisions/p14-materializer-coverage.md"
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("read the coverage doc: %v", err)
	}
	body := string(raw)

	const begin, end = "<!-- BEGIN COVERAGE", "<!-- END COVERAGE"
	i, j := strings.Index(body, begin), strings.Index(body, end)
	if i < 0 || j < 0 || j < i {
		t.Fatalf("%s has no delimited coverage block; the gate cannot check an undelimited table", doc)
	}
	block := body[i:j]

	// Every covered pair must be stated, with the SDK generation it targets — the part a reader acts on.
	for _, c := range MaterializerCoverage() {
		row := fmt.Sprintf("| %s | %s | %s |", c.Language, c.Provider, c.SDK)
		if !strings.Contains(block, row) {
			t.Errorf("the coverage doc does not carry the row for %s/%s.\nexpected line: %s\ngot block:\n%s",
				c.Language, c.Provider, row, block)
		}
	}

	// 🔴 And the other direction, which is the one that actually misleads: the doc must not claim a
	// provider the table does not carry. A row removed from the engine and left in the doc reads as a
	// capability that still exists.
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| go |") {
			continue
		}
		cols := strings.Split(line, "|")
		if len(cols) < 4 {
			continue
		}
		provider := strings.TrimSpace(cols[2])
		if _, ok := toolValueForms[provider]; !ok {
			t.Errorf("the coverage doc claims provider %q, which the form table does not carry; the doc "+
				"promises a capability the engine refuses", provider)
		}
	}
}
