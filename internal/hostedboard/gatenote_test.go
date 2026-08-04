package hostedboard

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// gatenote_test.go fences the board's summary against the row it summarises.
//
// A run is disqualified when GatePass is false, and that is false for two different verdicts: `fail`
// (the customer set a threshold and this run missed it) and `not-configured` (nobody ever set one).
// Collapsing them in the RANKING is correct — a run nobody held to a threshold must not be ranked as
// though it cleared one. Collapsing them in the PROSE was not: the note asserted "Every measured
// configuration failed its configured gate" over rows whose own chip read `no gate configured`.
//
// One page, two contradictory statements, and the false one was the summary — which is the half a
// reader believes, because it is the half that reads like a conclusion.

func linked(config string, outcome runlink.GateOutcome) linkingest.LinkedRun {
	return linkingest.LinkedRun{
		WorkflowID: "wf",
		ConfigHash: config,
		Scores:     []runlink.Score{{Metric: "quality", Value: 0.8, CILow: 0.7, CIHigh: 0.9}},
		Eval: runlink.EvalSummary{
			CaseCount:   8,
			SeedCount:   5,
			GateOutcome: outcome,
		},
	}
}

// notesOf returns the board notes for a set of linked runs.
func notesOf(t *testing.T, runs ...linkingest.LinkedRun) string {
	t.Helper()
	v := Build("wf", runs)
	if len(v.Ranked) != 0 {
		t.Fatalf("this fence is about the no-ranked-winner note; %d row(s) ranked", len(v.Ranked))
	}
	return strings.Join(v.Notes, "\n")
}

// TestTheNoWinnerNoteDoesNotInventAGateNobodyConfigured is the defect, exactly.
func TestTheNoWinnerNoteDoesNotInventAGateNobodyConfigured(t *testing.T) {
	notes := notesOf(t, linked("aaa", runlink.GateNotConfigured))

	if strings.Contains(notes, "failed its configured gate") {
		t.Errorf("the board says a configuration failed a gate that was never configured.\n"+
			"The row's own chip reads `no gate configured`, so the page states both — and a reader "+
			"believes the summary. To someone who set no gates it reads as `your quality gates are "+
			"failing`, which sends them looking for a regression that does not exist.\nnotes:\n%s", notes)
	}
	if !strings.Contains(strings.ToLower(notes), "no configuration here has a gate") {
		t.Errorf("the board does not say what is actually true — that nothing failed because nothing "+
			"was gated, and the next action is to configure gates.\nnotes:\n%s", notes)
	}
}

// TestARealGateFailureIsStillReportedAsOne is the other direction. Softening the sentence for everybody
// would trade a false alarm for a missed one, and a missed gate failure is the worse of the two.
func TestARealGateFailureIsStillReportedAsOne(t *testing.T) {
	notes := notesOf(t, linked("bbb", runlink.GateFail))

	if !strings.Contains(notes, "failed its configured gate") {
		t.Errorf("a run that genuinely failed the customer's own threshold is no longer reported as a "+
			"gate failure. That is the alarm this board exists to raise.\nnotes:\n%s", notes)
	}
}

// TestAMixedBoardCountsBothRatherThanPickingOne. With one of each, either sentence alone is false for
// half the rows — so the note has to carry both numbers.
func TestAMixedBoardCountsBothRatherThanPickingOne(t *testing.T) {
	notes := notesOf(t, linked("aaa", runlink.GateNotConfigured), linked("bbb", runlink.GateFail))

	for _, want := range []string{"1 failed a gate you set", "1 were never held to one"} {
		if !strings.Contains(notes, want) {
			t.Errorf("a board with one gate failure and one ungated run must account for both; missing "+
				"%q.\nnotes:\n%s", want, notes)
		}
	}
}
