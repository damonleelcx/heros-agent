package assessment

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// finding_test.go is task 1.2's fence: the conditional requirements are STRUCTURAL, and a mutation
// that removes one has to make a test red.
//
// 🔴 Every test here that asserts a refusal asserts the REASON as well as the failure. A test that
// only checks `err != nil` passes when a constructor starts failing for an unrelated reason, which is
// how a fence stops guarding the thing it was written for while staying green.

func ref(t *testing.T) EvidenceRef {
	t.Helper()
	return EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"}
}

func report(t *testing.T) EvalSetReport {
	t.Helper()
	return EvalSetReport{
		EvalSetHash:      "set-abc",
		Score:            Interval{Mean: 0.81, Low: 0.76, High: 0.86, NSeeds: 5},
		NCases:           1,
		OracleCoverage:   1,
		CoverageMeasured: true,
		Cases: []CaseView{{
			CaseID: "case-1",
			Oracle: evalharness.OracleVerdict{Decisive: true, Kind: "reference"},
		}},
	}
}

// TestANotMeasuredFindingWithNoMissingInputCannotBeConstructed is task 7.9, and it is the reason the
// fields are unexported.
//
// There is no second half to this test — no "and here is the invalid literal that also fails" —
// because there IS no invalid literal. `Finding{state: StateNotMeasured}` does not compile outside
// this package, and that is the guarantee. What is tested here is the one entrance that remains.
func TestANotMeasuredFindingWithNoMissingInputCannotBeConstructed(t *testing.T) {
	_, err := NotMeasured(AxisMemory, "", "the memory strategy could not be read", ref(t))
	if err == nil {
		t.Fatal("NotMeasured accepted an empty missing input: a report that says \"we could not\" " +
			"without saying what it lacked is a shrug, and task 1.2 makes it unconstructable")
	}
	if !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("refusal does not wrap ErrInvalidFinding: %v", err)
	}
	if !strings.Contains(err.Error(), "names no missing input") {
		t.Fatalf("refusal does not name the cause: %v", err)
	}
}

func TestAMissingInputOutsideTheClosedSetIsRefused(t *testing.T) {
	_, err := NotMeasured(AxisMemory, MissingInput("the sandbox was sad"), "claim", ref(t))
	if err == nil || !strings.Contains(err.Error(), "outside the closed set") {
		t.Fatalf("a free-text missing input was accepted; health is grouped by this field and a free "+
			"string makes that a group-by over a set nobody can enumerate: %v", err)
	}
}

// TestOnlyNotMeasuredCarriesAMissingInput is the negative half. It matters as much as the positive
// half: a `refused` finding carrying a missing input renders in two different message shapes
// depending on which field the console checks first.
func TestOnlyNotMeasuredCarriesAMissingInput(t *testing.T) {
	f, err := Observed(AxisModel, "every node names gpt-4o-mini", ref(t))
	if err != nil {
		t.Fatalf("Observed: %v", err)
	}
	if f.MissingInput() != "" {
		t.Fatalf("an observed finding carries missing input %q", f.MissingInput())
	}
	// The only way to reach the negative branch is through the wire, which is exactly the entrance
	// it was written to guard.
	var got Finding
	err = json.Unmarshal([]byte(`{"axis":"model","state":"observed","origin":"structural",
	  "claim":"c","evidence_ref":{"surface":"graph","locator":"wf-1"},"missing_input":"budget_exhausted"}`), &got)
	if err == nil || !strings.Contains(err.Error(), "only not_measured may") {
		t.Fatalf("an observed finding with a missing input decoded: %v", err)
	}
}

func TestARefusedFindingMustNameOneOfThree(t *testing.T) {
	_, err := Refused(AxisGraph, "", "the topology could not be assessed", ref(t))
	if err == nil || !strings.Contains(err.Error(), "names none of the frontend") {
		t.Fatalf("a refusal with no cause was accepted; \"unsupported\" tells three different readers "+
			"to do nothing: %v", err)
	}
	_, err = Refused(AxisGraph, RefusalCause("vibes"), "c", ref(t))
	if err == nil || !strings.Contains(err.Error(), "not one of the three") {
		t.Fatalf("an unknown refusal cause was accepted: %v", err)
	}
}

// TestAnInferredFindingCarriesBothAttributionFields is design D7.
func TestAnInferredFindingCarriesBothAttributionFields(t *testing.T) {
	for _, tc := range []struct {
		name, model, address, want string
	}{
		{"no address", "claude-opus-5-20260501", "", "carries no pinned inference address"},
		{"no model version", "", "sha256:abc", "records no provider model version"},
		{"neither", "", "", "carries no pinned inference address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Inferred(AxisMemory, "a per-session store, never pruned", ref(t), tc.model, tc.address)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal naming %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAStructuralFindingCannotCarryInferenceAttribution(t *testing.T) {
	var got Finding
	err := json.Unmarshal([]byte(`{"axis":"model","state":"observed","origin":"structural","claim":"c",
	  "evidence_ref":{"surface":"graph","locator":"wf-1"},"provider_model_version":"claude-opus-5"}`), &got)
	if err == nil || !strings.Contains(err.Error(), "structural and carries inference attribution") {
		t.Fatalf("a structural finding decoded with a model version: %v", err)
	}
}

// TestTheTwoIllegalCellsHaveNoConstructor documents the matrix by asserting the ONLY way to express
// either cell — the wire — is closed.
func TestTheTwoIllegalCellsHaveNoConstructor(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{
			"measured and inferred",
			`{"axis":"model","state":"measured","origin":"inferred","claim":"c",
			  "evidence_ref":{"surface":"board","locator":"wf-1"},
			  "provider_model_version":"m","inference_address":"a",
			  "eval_set":{"eval_set_hash":"h","score":{"mean":1,"ci_low":1,"ci_high":1,"n_seeds":1},
			              "n_cases":0,"coverage_measured":false,"cases":[]}}`,
			"measured and inferred",
		},
		{
			"refused and inferred",
			`{"axis":"graph","state":"refused","origin":"inferred","claim":"c",
			  "evidence_ref":{"surface":"graph","locator":"wf-1"},"refusal_cause":"frontend",
			  "provider_model_version":"m","inference_address":"a"}`,
			"refused and not structural",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Finding
			err := json.Unmarshal([]byte(tc.body), &got)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal naming %q, got %v", tc.want, err)
			}
		})
	}
}

// TestAMeasuredFindingCarriesItsEvalSetReport is `eval-set-decisiveness`'s first requirement stated
// where it cannot be forgotten: the score and the decisiveness are one object.
func TestAMeasuredFindingCarriesItsEvalSetReport(t *testing.T) {
	var got Finding
	err := json.Unmarshal([]byte(`{"axis":"prompt","state":"measured","origin":"structural","claim":"c",
	  "evidence_ref":{"surface":"board","locator":"wf-1"}}`), &got)
	if err == nil || !strings.Contains(err.Error(), "carries no eval-set report") {
		t.Fatalf("a measured finding decoded without decisiveness: %v", err)
	}
	f, err := Measured(AxisPrompt, "the prompt suite scores 0.81", ref(t), report(t))
	if err != nil {
		t.Fatalf("Measured: %v", err)
	}
	if f.Eval() == nil {
		t.Fatal("the report did not survive construction")
	}
}

func TestOnlyAMeasuredFindingCarriesAnEvalSetReport(t *testing.T) {
	var got Finding
	err := json.Unmarshal([]byte(`{"axis":"prompt","state":"observed","origin":"structural","claim":"c",
	  "evidence_ref":{"surface":"board","locator":"wf-1"},
	  "eval_set":{"eval_set_hash":"h","score":{"mean":1,"ci_low":1,"ci_high":1,"n_seeds":1},
	              "n_cases":0,"coverage_measured":false,"cases":[]}}`), &got)
	if err == nil || !strings.Contains(err.Error(), "only measured may") {
		t.Fatalf("an observed finding decoded with an eval-set report: %v", err)
	}
}

func TestEveryFindingStatesSomething(t *testing.T) {
	_, err := Observed(AxisTools, "   ", ref(t))
	if err == nil || !strings.Contains(err.Error(), "states nothing") {
		t.Fatalf("a finding with a blank claim was accepted: %v", err)
	}
}

func TestEveryFindingCarriesAnEvidenceReference(t *testing.T) {
	_, err := Observed(AxisTools, "three tools are bound", EvidenceRef{})
	if err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("a finding with no evidence surface was accepted: %v", err)
	}
	_, err = Observed(AxisTools, "three tools are bound", EvidenceRef{Surface: SurfaceGraph})
	if err == nil || !strings.Contains(err.Error(), "carries no locator") {
		t.Fatalf("a finding with no evidence locator was accepted: %v", err)
	}
}

// TestTheZeroFindingIsNotAReport guards the map-lookup hazard: `m[axis]` on a missing key yields a
// zero Finding, and a renderer that trusted it would print an empty row as though it were an answer.
func TestTheZeroFindingIsNotAReport(t *testing.T) {
	if err := (Finding{}).Validate(); err == nil {
		t.Fatal("the zero Finding validates; a missing map entry would render as a finding")
	}
}

// TestUnknownFieldsAreRefused is careful-api-creation's point at the wire: the field a future client
// would add here is a score.
func TestUnknownFieldsAreRefused(t *testing.T) {
	var got Finding
	err := json.Unmarshal([]byte(`{"axis":"model","state":"observed","origin":"structural","claim":"c",
	  "evidence_ref":{"surface":"graph","locator":"wf-1"},"score":62}`), &got)
	if err == nil || !strings.Contains(err.Error(), "score") {
		t.Fatalf("a finding carrying a score decoded: %v", err)
	}
}

// TestAnEvidencePathEscapesItsLocator is the defect the live run against `nousresearch/hermes-agent`
// surfaced.
//
// 🔴 A workflow id is CUSTOMER-CHOSEN TEXT. That repository's is `github.com/nousresearch/hermes-agent`
// — three slashes — and unescaped it produced a path with four extra segments that matches no route
// and that anything parsing it reads as a workflow called `github.com`. Nothing errored. The link
// simply went somewhere else, which is the quietest kind of wrong.
func TestAnEvidencePathEscapesItsLocator(t *testing.T) {
	// The SAME reference with a slash-free locator is the baseline. Comparing against it rather than
	// against a hard-coded number means the fence keeps working if the route shape ever changes.
	plain := EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"}.Path()
	ref := EvidenceRef{Surface: SurfaceGraph, Locator: "github.com/nousresearch/hermes-agent"}
	got := ref.Path()
	if strings.Count(got, "/") != strings.Count(plain, "/") {
		t.Fatalf("the path has %d separators and a slash-free locator gives %d: %q\nA locator with "+
			"slashes in it must not add route segments — the result matches no handler and reads as a "+
			"different subject", strings.Count(got, "/"), strings.Count(plain, "/"), got)
	}
	if !strings.HasSuffix(got, "/pattern-graph") {
		t.Fatalf("the path does not end at the surface: %q", got)
	}
	if !strings.Contains(got, "github.com%2Fnousresearch%2Fhermes-agent") {
		t.Fatalf("the locator is not escaped: %q", got)
	}
}

func TestFindingRoundTrips(t *testing.T) {
	want, err := Inferred(AxisMemory, "a per-session store that is never pruned", ref(t),
		"claude-opus-5-20260501", "sha256:9f2c")
	if err != nil {
		t.Fatalf("Inferred: %v", err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Finding
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Fatalf("round trip changed the finding:\n got %+v\nwant %+v", got, want)
	}
}

// ── The vocabulary fence (doc.go's claim, asserted) ──────────────────────────────────────────────

// TestTheSevenSharedAxesAreExactlyTheSevenDimensions keeps the noun dictionary single-source
// (task 8.4). An eighth `variantspec.Dimension` added by a later phase must not give the platform a
// configuration axis nothing ever assesses.
func TestTheSevenSharedAxesAreExactlyTheSevenDimensions(t *testing.T) {
	dims := variantspec.Dimensions()
	axes := Axes()
	if len(axes) != len(dims)+2 {
		t.Fatalf("there are %d axes and %d dimensions; the assessment reports the seven dimensions "+
			"plus loop and graph, so a new dimension needs an axis and a decision", len(axes), len(dims))
	}
	for i, d := range dims {
		if string(axes[i]) != string(d) {
			t.Fatalf("axis %d is %q, dimension %d is %q — the console, the CLI and the report must "+
				"name a surface identically", i, axes[i], i, d)
		}
	}
	if axes[len(dims)] != AxisLoop || axes[len(dims)+1] != AxisGraph {
		t.Fatalf("the two P34 axes are not the last two: %v", axes)
	}
}

// TestTheThreeSharedStateSpellingsAreIdentical is doc.go's claim about P31.
//
// 🔴 Both directions. A spelling that drifts in EITHER package gives the console two copy strings for
// what a reader learns as one word.
func TestTheThreeSharedStateSpellingsAreIdentical(t *testing.T) {
	shared := map[string]struct {
		mine   State
		theirs conversation.FindingState
	}{
		"measured":     {StateMeasured, conversation.FindingMeasured},
		"not_measured": {StateNotMeasured, conversation.FindingNotMeasured},
		"refused":      {StateRefused, conversation.FindingRefused},
	}
	for want, pair := range shared {
		if string(pair.mine) != want {
			t.Fatalf("assessment spells it %q, not %q", pair.mine, want)
		}
		if string(pair.theirs) != want {
			t.Fatalf("conversation spells it %q, not %q", pair.theirs, want)
		}
	}
	// And the two enums are deliberately NOT the same set. If they ever became equal, doc.go's
	// argument for a second enum would have quietly stopped being true.
	if len(States()) == len(conversation.FindingStates()) && !statesDiffer(t) {
		t.Fatal("assessment.State and conversation.FindingState now have identical members; " +
			"doc.go argues they are different questions, so one of the two has changed and the " +
			"argument needs rewriting or the enums need merging")
	}
}

func statesDiffer(t *testing.T) bool {
	t.Helper()
	mine := map[string]bool{}
	for _, s := range States() {
		mine[string(s)] = true
	}
	for _, s := range conversation.FindingStates() {
		if !mine[string(s)] {
			return true
		}
	}
	return false
}
