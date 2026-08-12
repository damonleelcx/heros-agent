package evalboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// P30 tasks 1.12–1.15 — the eval set behind the board's denominator.

func caseWithReference(id string) evalharness.Case {
	return evalharness.Case{
		CaseID: id, WorkflowID: "wf", Suite: "s",
		Input:     json.RawMessage(`{"q":1}`),
		Reference: json.RawMessage(`{"a":1}`),
		Label:     evalharness.LabelGold,
		EdgeCase:  evalharness.EdgeCaseKind("malformed_input"),
	}
}

// An oracle that accepts every possible output. It looks measured and decides nothing, which is why
// evalgen counts it separately from "no oracle at all".
func caseWithIndecisiveOracle(id string) evalharness.Case {
	return evalharness.Case{
		CaseID: id, WorkflowID: "wf", Suite: "s",
		Input:        json.RawMessage(`{"q":1}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Label:        evalharness.LabelNone,
	}
}

// 🔴 TASK 1.15 — the list and the denominator must agree, and a mismatch is an ERROR, not a render.
//
// This is the fence. Proved red by changing the state resolution to return EvalSetListed regardless of
// the comparison: this test then reports a `listed` state over a 2-of-3 table, which is exactly the
// silent disagreement it exists to catch.
func TestAListShorterThanTheDenominatorIsAnErrorNotATable(t *testing.T) {
	v := BuildEvalSet(EvalSetInput{
		WorkflowID:     "wf",
		NCases:         3,
		Linked:         true,
		CasesAvailable: true,
		Cases:          []evalharness.Case{caseWithReference("a"), caseWithReference("b")},
	})
	if v.State != EvalSetInconsistent {
		t.Fatalf("state = %q, want %q — a 2-case list under a 3-case denominator is a disagreement "+
			"about which eval set every score on the board describes, and rendering the shorter table "+
			"under the larger number is how that becomes invisible", v.State, EvalSetInconsistent)
	}
	for _, want := range []string{"2 case", "3", "will not pick"} {
		if !strings.Contains(v.Sentence, want) {
			t.Errorf("the sentence does not contain %q:\n  %s", want, v.Sentence)
		}
	}
}

// A list LONGER than the denominator is the same defect in the other direction, and it must not be
// treated as "extra cases we can show".
func TestAListLongerThanTheDenominatorIsAlsoAnError(t *testing.T) {
	v := BuildEvalSet(EvalSetInput{
		WorkflowID: "wf", NCases: 1, Linked: true, CasesAvailable: true,
		Cases: []evalharness.Case{caseWithReference("a"), caseWithReference("b")},
	})
	if v.State != EvalSetInconsistent {
		t.Errorf("state = %q, want %q", v.State, EvalSetInconsistent)
	}
}

// The agreeing case: `listed`, with the four columns task 1.12 names.
func TestAnAgreeingListIsListedWithItsFourColumns(t *testing.T) {
	v := BuildEvalSet(EvalSetInput{
		WorkflowID: "wf", NCases: 2, Linked: true, CasesAvailable: true,
		Cases: []evalharness.Case{caseWithReference("b"), caseWithIndecisiveOracle("a")},
	})
	if v.State != EvalSetListed {
		t.Fatalf("state = %q, want %q (sentence: %s)", v.State, EvalSetListed, v.Sentence)
	}
	if len(v.Cases) != 2 {
		t.Fatalf("cases = %+v", v.Cases)
	}
	// Sorted by case id: the same input in a different order must draw the same table.
	if v.Cases[0].CaseID != "a" || v.Cases[1].CaseID != "b" {
		t.Errorf("the list is not in a deterministic order: %+v", v.Cases)
	}
	if !v.Cases[0].Indecisive {
		t.Error("a case whose schema accepts every output is not marked indecisive — it looks measured " +
			"and decides nothing, which is the most misleading state a case can be in")
	}
	if v.Cases[1].Indecisive {
		t.Error("a case with a fixed reference is marked indecisive; exact match can always fail")
	}
	if v.Cases[1].Family != "malformed_input" {
		t.Errorf("family = %q, want malformed_input", v.Cases[1].Family)
	}
	if v.Cases[1].Oracle == "" {
		t.Error("the oracle kind is empty on a case that carries a reference")
	}
}

// 🔴 The hosted reality: the platform holds the count and not the cases. It must say so by name, and it
// must NOT report `listed` with an empty table — which would read as an eval set with no cases in it.
func TestCountsOnlyNamesTheLimitInsteadOfRenderingAnEmptySet(t *testing.T) {
	v := BuildEvalSet(EvalSetInput{
		WorkflowID: "wf", NCases: 8, Linked: true, CasesAvailable: false,
		Quality: &evalgen.SetQuality{NCases: 8, NGold: 5, NWeak: 3, NOracle: 6, NIndecisive: 2},
	})
	if v.State != EvalSetCountsOnly {
		t.Fatalf("state = %q, want %q", v.State, EvalSetCountsOnly)
	}
	if len(v.Cases) != 0 {
		t.Errorf("cases were invented: %+v", v.Cases)
	}
	if !strings.Contains(v.Sentence, "case_count") || !strings.Contains(v.Sentence, "stay on your machine") {
		t.Errorf("the sentence does not name the wire rule that causes this state:\n  %s", v.Sentence)
	}
	// The columns it cannot fill are named, so a console marks the cells rather than drawing an em-dash
	// that a reader takes for legitimately absent data.
	for _, col := range []string{"case_id", "family", "oracle", "indecisive"} {
		var found bool
		for _, u := range v.Unattributed {
			if u == col {
				found = true
			}
		}
		if !found {
			t.Errorf("column %q is not named as unattributed: %v", col, v.Unattributed)
		}
	}
	// The split it DOES hold still answers "what is in this set" at the resolution available.
	if len(v.Families) != 2 || v.NOracle != 6 || v.NIndecisive != 2 {
		t.Errorf("the counts that DO cross were dropped: families=%+v n_oracle=%d n_indecisive=%d",
			v.Families, v.NOracle, v.NIndecisive)
	}
}

// No linked run at all is its own state. "You have linked nothing" and "your eval set is empty" send a
// reader to two different places.
func TestNeverLinkedIsItsOwnState(t *testing.T) {
	v := BuildEvalSet(EvalSetInput{WorkflowID: "wf"})
	if v.State != EvalSetNeverLinked {
		t.Errorf("state = %q, want %q", v.State, EvalSetNeverLinked)
	}
	if !strings.Contains(v.Sentence, "not an empty eval set") {
		t.Errorf("the sentence does not distinguish absence of measurement from an empty set:\n  %s", v.Sentence)
	}
}

// 🔴 TASK 1.13 — the vacuous axes are named, not counted.
func TestVacuousCoverageAxesAreNamedNotCounted(t *testing.T) {
	rep := &evalgen.CoverageReport{
		Path: evalgen.Dimension{Name: "path", Target: 1, Vacuous: true},
		Node: evalgen.Dimension{Name: "node", Target: 1},
	}
	v := BuildEvalSet(EvalSetInput{WorkflowID: "wf", Linked: true, Coverage: rep})
	if len(v.VacuousDimensions) != 1 || v.VacuousDimensions[0] != "path" {
		t.Fatalf("vacuous_dimensions = %v, want [path]. A count tells a reader nothing they can act on; "+
			"the NAME tells them their workflow's inter-node flow has not been observed.", v.VacuousDimensions)
	}
}

// Every collection is empty rather than nil, so a consumer never has to treat `null` as a state.
func TestEveryCollectionIsEmptyNeverNil(t *testing.T) {
	v := BuildEvalSet(EvalSetInput{WorkflowID: "wf"})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), ":null") {
		t.Errorf("a nil collection serialised as null: %s", b)
	}
}
