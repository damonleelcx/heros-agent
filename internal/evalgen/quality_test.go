package evalgen

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// Task 4.5 — an oracle-derived reference is gold; an unreviewed LLM reference is weak.
func TestGoldVersusWeakLabeling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		c        evalharness.Case
		reviewed bool
		want     evalharness.ReferenceLabel
	}{
		{
			name: "deterministic schema oracle is gold",
			c: evalharness.Case{
				Input:        json.RawMessage(`{"q":"x"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				Origin:       evalharness.OriginSchema,
			},
			want: evalharness.LabelGold,
		},
		{
			name: "deterministic regex oracle is gold",
			c:    evalharness.Case{Input: json.RawMessage(`{}`), Pattern: `^ok$`, Origin: evalharness.OriginSchema},
			want: evalharness.LabelGold,
		},
		{
			name: "unreviewed LLM reference is weak",
			c: evalharness.Case{
				Input:     json.RawMessage(`{"q":"x"}`),
				Reference: json.RawMessage(`{"a":"guess"}`),
				Origin:    evalharness.OriginLLM,
			},
			want: evalharness.LabelWeak,
		},
		{
			name: "human-reviewed LLM reference is gold",
			c: evalharness.Case{
				Input:     json.RawMessage(`{"q":"x"}`),
				Reference: json.RawMessage(`{"a":"checked"}`),
				Origin:    evalharness.OriginLLM,
			},
			reviewed: true,
			want:     evalharness.LabelGold,
		},
		{
			name: "hand-authored reference is gold",
			c: evalharness.Case{
				Input:     json.RawMessage(`{"q":"x"}`),
				Reference: json.RawMessage(`{"a":"written by a person"}`),
				Origin:    evalharness.OriginHandAuthored,
			},
			want: evalharness.LabelGold,
		},
		{
			name: "no reference at all is none",
			c:    evalharness.Case{Input: json.RawMessage(`{"q":"x"}`), Origin: evalharness.OriginAdversarial},
			want: evalharness.LabelNone,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LabelReference(tc.c, tc.reviewed)
			if got.Label != tc.want {
				t.Fatalf("want %q got %q", tc.want, got.Label)
			}
		})
	}
}

// The LLM generator labels every reference it invents WEAK — the single line that stops a synthetic
// guess from driving a gate.
func TestLLMGeneratorAlwaysLabelsItsReferencesWeak(t *testing.T) {
	model := &referenceWritingModel{}
	g := &LLMGenerator{Model: model}
	produced, err := g.Generate(context.Background(), fxRouterIR(),
		Gap{Paths: []string{EdgeID("router", "branch_b")}}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(produced) == 0 {
		t.Fatal("fixture produced nothing")
	}
	for _, c := range produced {
		if len(c.Reference) > 0 && c.Label != evalharness.LabelWeak {
			t.Fatalf("an LLM-written reference must be weak, got %q for %s", c.Label, c.CaseID)
		}
	}
}

type referenceWritingModel struct{}

func (referenceWritingModel) GenerateCases(_ context.Context, req CaseRequest) ([]GeneratedCase, error) {
	return []GeneratedCase{{
		Input:     json.RawMessage(`{"route":"branch_b","q":"synthesized"}`),
		Reference: json.RawMessage(`{"a":"the model's guess at the right answer"}`),
	}}, nil
}

// Task 4.5 / 8.5 — a weak-labeled reference cannot silently gate: GatingSet SPLITS it out, so a
// caller cannot use it by accident.
func TestWeakLabeledReferenceCannotSilentlyGate(t *testing.T) {
	cases := []evalharness.Case{
		{CaseID: "gold-1", Label: evalharness.LabelGold, Input: json.RawMessage(`{"q":"a"}`)},
		{CaseID: "weak-1", Label: evalharness.LabelWeak, Input: json.RawMessage(`{"q":"b"}`)},
		{CaseID: "none-1", Label: evalharness.LabelNone, Input: json.RawMessage(`{"q":"c"}`)},
	}
	gating, weak := GatingSet(cases)
	for _, c := range gating {
		if c.Label == evalharness.LabelWeak {
			t.Fatalf("a weak case reached the gating set: %s", c.CaseID)
		}
	}
	if len(weak) != 1 || weak[0].CaseID != "weak-1" {
		t.Fatalf("the weak case must be surfaced separately, got %v", weak)
	}
	if ids := WeakCaseIDs(cases); len(ids) != 1 || ids[0] != "weak-1" {
		t.Fatalf("WeakCaseIDs: got %v", ids)
	}
}

// Task 4.6 — near-identical cases are deduped, deterministically.
func TestDedupeRemovesNearIdenticalCases(t *testing.T) {
	mk := func(id, q string) evalharness.Case {
		in, _ := json.Marshal(map[string]any{"route": "branch_a", "q": q})
		return evalharness.Case{CaseID: id, Input: in}
	}
	cases := []evalharness.Case{
		mk("c1", "what is the capital of france"),
		mk("c2", "what is the capital of france"), // exact duplicate
		mk("c3", "what is the capital of france today"),
		mk("c4", "explain quantum tunnelling to a child using only kitchen metaphors"),
	}
	kept, removed := Dedupe(cases, DefaultDedupeThreshold)
	if removed == 0 {
		t.Fatal("the exact duplicate must be removed")
	}
	seen := map[string]bool{}
	for _, c := range kept {
		seen[c.CaseID] = true
	}
	if !seen["c1"] || !seen["c4"] {
		t.Fatalf("dedupe must keep the distinct cases, kept %v", seen)
	}
	if seen["c2"] {
		t.Fatal("the exact duplicate survived")
	}

	// Deterministic: the same input yields the same survivors regardless of the order it arrives in.
	shuffled := []evalharness.Case{cases[3], cases[1], cases[2], cases[0]}
	kept2, removed2 := Dedupe(shuffled, DefaultDedupeThreshold)
	if removed2 != removed || len(kept2) != len(kept) {
		t.Fatalf("dedupe must be order-independent: %d/%d vs %d/%d", removed, len(kept), removed2, len(kept2))
	}
	for i := range kept {
		if kept[i].CaseID != kept2[i].CaseID {
			t.Fatalf("dedupe survivors differ by input order: %v vs %v", kept[i].CaseID, kept2[i].CaseID)
		}
	}
}

// Task 4.6 — a trivially-easy, low-diversity set is surfaced as LOW CONFIDENCE.
func TestTriviallyEasyLowDiversitySetIsLowConfidence(t *testing.T) {
	mk := func(id string, difficulty float64) evalharness.Case {
		in, _ := json.Marshal(map[string]any{"route": "branch_a", "q": "hello"})
		return evalharness.Case{CaseID: id, Input: in, Difficulty: difficulty, Label: evalharness.LabelNone}
	}
	// Six near-identical cases the baseline passes every time.
	var cases []evalharness.Case
	for i := 0; i < 6; i++ {
		cases = append(cases, mk("c"+itoa(i), 0.01))
	}
	q := MeasureQuality(cases, LoopConfig{})
	t.Logf("quality: %+v", q)

	if !q.LowConfidence {
		t.Fatal("a near-duplicate, trivially-easy set must be surfaced as low-confidence")
	}
	if q.Diversity >= DefaultDiversityFloor {
		t.Fatalf("six identical inputs must score below the diversity floor, got %v", q.Diversity)
	}
	if len(q.Reasons) == 0 {
		t.Fatal("low confidence must state its reasons")
	}
}

// An unmeasured difficulty is NOT reported as zero difficulty: "nobody ran a baseline" and "the set
// is trivial" are different facts.
func TestUnmeasuredDifficultyIsSurfacedAsUnmeasured(t *testing.T) {
	mk := func(id, q string) evalharness.Case {
		in, _ := json.Marshal(map[string]any{"q": q})
		return evalharness.Case{CaseID: id, Input: in}
	}
	cases := []evalharness.Case{
		mk("c1", "entirely different first question about geology"),
		mk("c2", "a completely unrelated second prompt concerning maritime law"),
	}
	q := MeasureQuality(cases, LoopConfig{})
	if q.DifficultyMeasured {
		t.Fatal("difficulty must not read as measured when no baseline ran")
	}
	if !q.LowConfidence {
		t.Fatal("an unmeasured set's discriminating power is unknown, so it is low-confidence")
	}
}

// Difficulty is MEASURED by running a real baseline, repeatedly.
func TestMeasureDifficultyRunsTheBaseline(t *testing.T) {
	mk := func(id string) evalharness.Case {
		in, _ := json.Marshal(map[string]any{"q": id})
		return evalharness.Case{CaseID: id, Input: in}
	}
	cases := []evalharness.Case{mk("easy"), mk("hard")}
	r := &scriptedBaseline{fails: map[string]int{"hard": 3}}

	out, err := MeasureDifficulty(context.Background(), r, cases, 3)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	byID := map[string]float64{}
	for _, c := range out {
		byID[c.CaseID] = c.Difficulty
	}
	if byID["easy"] != 0 {
		t.Fatalf("a case the baseline always passes has difficulty 0, got %v", byID["easy"])
	}
	if byID["hard"] != 1 {
		t.Fatalf("a case the baseline always fails has difficulty 1, got %v", byID["hard"])
	}
	if r.calls != 6 {
		t.Fatalf("want 2 cases x 3 repeats = 6 baseline runs, got %d", r.calls)
	}
}

type scriptedBaseline struct {
	fails map[string]int
	calls int
}

func (b *scriptedBaseline) Pass(_ context.Context, c evalharness.Case) (bool, error) {
	b.calls++
	if n, ok := b.fails[c.CaseID]; ok && n > 0 {
		b.fails[c.CaseID] = n - 1
		return false, nil
	}
	return true, nil
}

// Task 4.2 — the schema layer derives valid, boundary and invalid inputs from the typed contract.
func TestSchemaGeneratorProducesBoundaryAndInvalidInputs(t *testing.T) {
	g := &SchemaGenerator{}
	produced, err := g.Generate(context.Background(), fxRouterIR(), Gap{Nodes: []string{"router"}}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	kinds := map[evalharness.EdgeCaseKind]bool{}
	for _, c := range produced {
		kinds[c.EdgeCase] = true
		if err := c.Validate(); err != nil {
			t.Fatalf("generated case is invalid: %v", err)
		}
	}
	if !kinds[evalharness.EdgeCaseBoundary] {
		t.Fatal("the schema layer must produce a boundary input")
	}
	if !kinds[evalharness.EdgeCaseMalformedInput] {
		t.Fatal("the schema layer must produce an invalid input")
	}
	if !kinds[evalharness.EdgeCaseNone] {
		t.Fatal("the schema layer must produce a valid input")
	}
	// A schema-derived oracle is decidable, so its cases are gold.
	for _, c := range produced {
		if len(c.OutputSchema) > 0 && c.Label != evalharness.LabelGold {
			t.Fatalf("a decidable schema oracle is gold, got %q", c.Label)
		}
	}
}

// Every adversarial probe is tagged so it routes into the P3 sandbox.
func TestAdversarialProbesAreTaggedForTheSandbox(t *testing.T) {
	g := &AdversarialGenerator{}
	seed := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	produced, err := g.Generate(context.Background(), fxRouterIR(), Gap{}, seed)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(produced) == 0 {
		t.Fatal("the adversarial layer produced nothing to perturb")
	}
	for _, c := range produced {
		if c.EdgeCase != evalharness.EdgeCaseAdversarial && c.EdgeCase != evalharness.EdgeCaseEmptyInput {
			t.Fatalf("every adversarial probe must carry a taxonomy tag, got %q on %s", c.EdgeCase, c.CaseID)
		}
		if c.Origin != evalharness.OriginAdversarial {
			t.Fatalf("provenance must be stamped, got %q", c.Origin)
		}
	}
}
