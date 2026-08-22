package assessment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// schema_test.go keeps `schemas/assessment.schema.json` from becoming a second, drifting copy of the
// vocabularies, and keeps the checked-in sample an actual product of the Go types.
//
// # Why the schema exists at all when the type is already opaque
//
// They guard different entrances. The type guards Go callers; the schema guards a stored row and a
// posted payload, and it is what `make schema` can check without linking the binary. Task 1.2 asks for
// both, and both is what "enforced in the type and in the schema, not in review" means.
//
// # Why THIS test rather than a generator
//
// A generator would be a fourth artifact to keep working, and the schema carries prose — the `$comment`
// on each conditional says WHY the negative half is load-bearing — that a generator would either drop
// or force into Go struct tags. So the schema is written by hand and this test asserts the only part
// that can silently drift: the closed sets.

func schemaPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "schemas", "assessment.schema.json")
}

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(schemaPath(t))
	if err != nil {
		t.Fatalf("reading the assessment schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parsing the assessment schema: %v", err)
	}
	return doc
}

// enumAt walks a dotted path into the schema and returns the `enum` there, sorted.
func enumAt(t *testing.T, doc map[string]any, path ...string) []string {
	t.Helper()
	cur := any(doc)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v: %q is not an object", path, p)
		}
		cur, ok = m[p]
		if !ok {
			t.Fatalf("schema path %v: %q is absent", path, p)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("schema path %v does not name an object", path)
	}
	raw, ok := m["enum"].([]any)
	if !ok {
		t.Fatalf("schema path %v carries no enum, so the closed set is not closed there", path)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	sort.Strings(out)
	return out
}

// TestTheSchemasClosedSetsEqualTheGoOnes is the anti-drift fence.
//
// 🔴 Equality in BOTH directions, per set. A schema that permits a value Go refuses lets an invalid
// row through `make schema`; a schema that refuses a value Go emits makes a correct assessment fail
// validation on the day the new member first occurs, which is the worst possible day.
func TestTheSchemasClosedSetsEqualTheGoOnes(t *testing.T) {
	doc := loadSchema(t)
	props := []string{"$defs", "Finding", "properties"}

	for _, tc := range []struct {
		name string
		path []string
		want []string
	}{
		{"axis", append(append([]string{}, props...), "axis"), AxisNames()},
		{"state", append(append([]string{}, props...), "state"), sortedStrings(States())},
		{"origin", append(append([]string{}, props...), "origin"), sortedStrings(Origins())},
		{"missing_input", append(append([]string{}, props...), "missing_input"), MissingInputNames()},
		{"refusal_cause", append(append([]string{}, props...), "refusal_cause"), sortedStrings(RefusalCauses())},
		{"surface", []string{"$defs", "EvidenceRef", "properties", "surface"}, SurfaceNames()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := enumAt(t, doc, tc.path...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("the schema's %s enum has drifted from the Go closed set:\n"+
					"  schema: %v\n  go:     %v\n"+
					"A member added in Go and missing here fails validation the first time it occurs; "+
					"a member here and missing in Go lets an invalid row past `make schema`.",
					tc.name, got, tc.want)
			}
		})
	}
}

// TestTheFindingsArrayIsBoundedAtNine is FR1 in the schema half.
func TestTheFindingsArrayIsBoundedAtNine(t *testing.T) {
	doc := loadSchema(t)
	findings := doc["properties"].(map[string]any)["findings"].(map[string]any)
	for _, key := range []string{"minItems", "maxItems"} {
		got, ok := findings[key].(float64)
		if !ok {
			t.Fatalf("the findings array declares no %s, so an eight-axis report validates", key)
		}
		if int(got) != len(Axes()) {
			t.Fatalf("the findings array's %s is %d, want %d", key, int(got), len(Axes()))
		}
	}
}

// TestTheSampleIsAProductOfTheGoTypes writes the checked-in sample under P33_WRITE_SAMPLE=1 and
// otherwise asserts the file on disk is exactly what these types marshal to.
//
// That closes the loop `make schema` needs: the sample validates against the schema (validate.py),
// and the sample is what Go emits (here). Without the second half the sample is a hand-written
// document that agrees with the schema and with nothing that runs.
func TestTheSampleIsAProductOfTheGoTypes(t *testing.T) {
	got, err := json.MarshalIndent(sampleAssessment(t), "", "  ")
	if err != nil {
		t.Fatalf("marshalling the sample: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("..", "..", "schemas", "samples", "assessment.valid.json")
	if os.Getenv("P33_WRITE_SAMPLE") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing the sample: %v", err)
		}
		t.Log("wrote", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the sample (regenerate with P33_WRITE_SAMPLE=1 go test ./internal/assessment): %v", err)
	}
	if string(want) != string(got) {
		t.Fatalf("schemas/samples/assessment.valid.json is not what these types marshal to.\n" +
			"Regenerate with: P33_WRITE_SAMPLE=1 go test ./internal/assessment -run TestTheSampleIsAProductOfTheGoTypes")
	}
}

// sampleAssessment is a report that exercises every legal cell of the matrix, so the sample is a
// worked example rather than a happy path: one measured, one structural observation, one inference,
// one abstention, one budget refusal, one frontend refusal, and the two axes P34 owns.
func sampleAssessment(t *testing.T) Assessment {
	t.Helper()
	graph := EvidenceRef{Surface: SurfaceGraph, Locator: "wf-checkout-agent"}
	board := EvidenceRef{Surface: SurfaceBoard, Locator: "wf-checkout-agent", Fragment: "set-4f1c"}

	mk := func(f Finding, err error) Finding {
		t.Helper()
		if err != nil {
			t.Fatalf("building the sample: %v", err)
		}
		return f
	}

	evalReport := EvalSetReport{
		EvalSetHash:       "set-4f1c",
		Score:             Interval{Mean: 0.81, Low: 0.74, High: 0.88, NSeeds: 5},
		NCases:            3,
		OracleCoverage:    float64(2) / float64(3),
		NIndecisive:       1,
		CoverageMeasured:  true,
		VacuousDimensions: []string{"path"},
		Cases: []CaseView{
			{CaseID: "case-01", Suite: "extraction", Oracle: evalharness.OracleVerdict{Decisive: true, Kind: "reference"}},
			{CaseID: "case-02", Suite: "extraction", Oracle: evalharness.OracleVerdict{Decisive: true, Kind: "schema"}},
			{CaseID: "case-03", Suite: "extraction", Oracle: evalharness.OracleVerdict{
				Kind:   "schema",
				Reason: "the output schema constrains nothing: it accepts every JSON value",
			}},
		},
	}

	return Assessment{
		AssessmentID:    "as-2026-08-21-0001",
		TenantID:        "tn-nousresearch",
		WorkflowID:      "wf-checkout-agent",
		SourceRevision:  "9f2c1ab4c6d1f0e3a5b7c9d1e3f5a7b9c1d3e5f7",
		AgentConfigHash: "cfg-3d9a7f21",
		StartedAtMS:     1755734400000,
		CompletedAtMS:   1755734462000,
		SpendUSD:        0.42,
		SpendCapUSD:     1.00,
		Findings: []Finding{
			mk(Observed(AxisModel, "all four call sites name gpt-4o-mini with temperature 0.2", graph)),
			mk(Measured(AxisPrompt, "the extraction suite scores 0.81 across five seeds", board, evalReport)),
			mk(Observed(AxisSkills, "no skills are bound at any call site", graph)),
			mk(Observed(AxisContext, "each call assembles system + last-three-turns", graph)),
			mk(NotMeasured(AxisTools, MissingUnresolvedField,
				"the tool list is built at runtime from a value discovery could not resolve; the Python frontend is syntactic and cannot follow it across a statement", graph)),
			mk(Inferred(AxisMemory, "a per-session dictionary written on every turn and never pruned", graph,
				"claude-opus-5-20260501", "sha256:b1946ac92492d2347c6235b4d2611184")),
			mk(NotMeasured(AxisHarness, MissingBudgetExhausted,
				"the assessment reached its $1.00 cap before this axis was inferred; re-run with a higher cap to get an answer", graph)),
			mk(Refused(AxisLoop, RefusalAnalysis,
				"this build does not assess control loops as a configuration axis yet; the analysis lands with P34", graph)),
			// 🔴 `analysis`, not `frontend`, and the sample is the place that distinction is easiest to
			// get wrong. The topology extractor DOES attribute an empty graph to the frontend — that is
			// design D6 and it is tested — but the shipped report never reaches it: `graph` is refused
			// behind P34 (task 9.2), because reporting on it before then names an axis the configuration
			// layer does not have. A sample showing the frontend refusal would document a state no
			// deployment produces.
			mk(Refused(AxisGraph, RefusalAnalysis,
				"this build does not report topology as an assessment axis yet; the analysis exists and `graph` becomes a surface you can configure with P34", graph)),
		},
	}
}

func sortedStrings[T ~string](in []T) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, string(v))
	}
	sort.Strings(out)
	return out
}
