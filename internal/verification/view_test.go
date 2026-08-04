package verification

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// §6.4 / §7.5: one-click PR-open is offered ONLY for a gate-passing proposal in Assisted mode.
func TestPROpenAvailable_GatedOnVerification(t *testing.T) {
	pass := Verdict{GateResult: GatePass}
	fail := Verdict{GateResult: GateFailRegress}

	if ok, _ := PROpenAvailable(Assisted, pass); !ok {
		t.Error("Assisted + pass must offer one-click PR-open")
	}
	if ok, reason := PROpenAvailable(Assisted, fail); ok || reason == "" {
		t.Error("a gate-failed proposal must not offer PR-open, and must give a reason")
	}
	if ok, _ := PROpenAvailable(Advisory, pass); ok {
		t.Error("Advisory mode must not offer one-click PR-open even for a passing proposal")
	}
}

// §6.5: UI state is derived only from the verdict — pass→verified, other-ran→gate_failed, unrun→verifying.
func TestState_FromVerdict(t *testing.T) {
	cases := map[GateResult]ProposalState{
		GatePass:          StateVerified,
		GateFailSig:       StateGateFailed,
		GateFailRegress:   StateGateFailed,
		GateFailConstrain: StateGateFailed,
		GateUnrun:         StateVerifying,
	}
	for gr, want := range cases {
		if got := State(Verdict{GateResult: gr}); got != want {
			t.Errorf("State(%s) = %s, want %s", gr, got, want)
		}
	}
}

// §6.7: the synthesis is narration over the structured verdict — it reflects the verdict's numbers and
// never contradicts a withheld result.
func TestNarrate_IsOverTheVerdict(t *testing.T) {
	pass := Verdict{GateResult: GatePass, Metric: "task_success", HeldOut: true,
		Delta:     evalstats.Interval{Mean: 0.22, Low: 0.10, High: 0.34},
		CostDelta: 0.002, LatencyDelta: 20}
	pass.SetCases([]string{"h1", "h2"}, nil)
	s := Narrate(pass)
	if !strings.Contains(s, "0.22") || !strings.Contains(s, "2 case(s) fixed") {
		t.Errorf("narration must reflect the verdict's delta and cases fixed: %q", s)
	}
	withheld := Verdict{GateResult: GateFailSig, Reason: "CI overlaps"}
	if !strings.Contains(Narrate(withheld), "Withheld") {
		t.Errorf("a withheld verdict must narrate as withheld, got %q", Narrate(withheld))
	}
}

// §6.3 / §7.6: across three iterations where cluster A falls and cluster B rises by a comparable
// amount, the trend shows the workflow did NOT globally improve — the failure mass moved.
func TestBuildTrend_ProblemsMovedNotImproved(t *testing.T) {
	points := []TrendPoint{
		{VariantID: "v1", Iteration: 1, OverallSuccess: 0.60, ClusterSizes: map[string]int{"A": 5, "B": 1}},
		{VariantID: "v2", Iteration: 2, OverallSuccess: 0.61, ClusterSizes: map[string]int{"A": 3, "B": 3}},
		{VariantID: "v3", Iteration: 3, OverallSuccess: 0.60, ClusterSizes: map[string]int{"A": 1, "B": 5}},
	}
	tv := BuildTrend(points)
	if tv.GloballyImproved {
		t.Error("the workflow did not globally improve — success is flat")
	}
	if !tv.ProblemsMoved {
		t.Error("the trend must show the failure mass moved from cluster A to cluster B")
	}
	if !strings.Contains(tv.Narrative, "moved") {
		t.Errorf("narrative must say problems moved: %q", tv.Narrative)
	}
}

// A genuine global improvement is reported as improvement, not movement.
func TestBuildTrend_RealImprovement(t *testing.T) {
	points := []TrendPoint{
		{VariantID: "v1", Iteration: 1, OverallSuccess: 0.50, ClusterSizes: map[string]int{"A": 6, "B": 4}},
		{VariantID: "v2", Iteration: 2, OverallSuccess: 0.80, ClusterSizes: map[string]int{"A": 2, "B": 1}},
	}
	tv := BuildTrend(points)
	if !tv.GloballyImproved || tv.ProblemsMoved {
		t.Errorf("a real success gain must read as improved, not moved: %+v", tv)
	}
}
