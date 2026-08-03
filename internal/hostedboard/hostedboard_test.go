package hostedboard

import (
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// hostedboard_test.go pins the things this board must NOT say.
//
// The ordinary assertions (rows appear, ranking is by quality) matter less than the refusals: this
// assembler exists in a codebase that left P4 unmounted for phases rather than render an invented
// `gate_pass`, and the way that discipline gets lost is one plausible-looking default at a time.

func run(configHash string, quality float64, gate runlink.GateOutcome, opts ...func(*linkingest.LinkedRun)) linkingest.LinkedRun {
	lr := linkingest.LinkedRun{
		RunID: "run-" + configHash, TenantID: "t1", WorkflowID: "wf", ConfigHash: configHash,
		LinkedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Scores: []runlink.Score{
			{Metric: "quality", Value: quality, CILow: quality - 0.05, CIHigh: quality + 0.05},
			{Metric: "cost_usd", Value: 0.01},
			{Metric: "latency_ms", Value: 500},
		},
		Eval: runlink.EvalSummary{CaseCount: 8, SeedCount: 5, GateOutcome: gate},
	}
	for _, o := range opts {
		o(&lr)
	}
	return lr
}

func at(ts time.Time) func(*linkingest.LinkedRun) {
	return func(lr *linkingest.LinkedRun) { lr.LinkedAt = ts }
}

// TestTieAnalysisIsReportedUnavailable is the central refusal. `AllTie: false` is a CLAIM that the
// variants are distinguishable, and nothing here tested that — the bootstrap replicates the overlap test
// needs never cross the boundary. Without this field the board would make that claim by default on every
// render.
func TestTieAnalysisIsReportedUnavailable(t *testing.T) {
	v := Build("wf", []linkingest.LinkedRun{
		run("aaaa000000001111", 0.9, runlink.GatePass),
		run("bbbb000000002222", 0.89, runlink.GatePass),
	})
	if v.TieAnalysis != evalboard.TieUnavailable {
		t.Fatalf("tie_analysis = %q, want %q — this board cannot test for ties and must not imply it did",
			v.TieAnalysis, evalboard.TieUnavailable)
	}
	for _, r := range v.Ranked {
		if len(r.TiedWith) != 0 {
			t.Errorf("row %s carries TiedWith %v from a board that ran no overlap test", r.VariantID, r.TiedWith)
		}
	}
}

// TestNotConfiguredIsNotAPass is the distinction that decides where a variant ranks. A run with no gate
// has not cleared a threshold, and ranking it beside runs that did would be the board asserting a
// verdict nobody reached.
func TestNotConfiguredIsNotAPass(t *testing.T) {
	v := Build("wf", []linkingest.LinkedRun{run("cccc000000003333", 0.95, runlink.GateNotConfigured)})

	for _, r := range v.Ranked {
		if r.GatePass {
			t.Fatalf("a run with gate_outcome=not-configured was ranked as gate_pass — an unmeasured run "+
				"now sits on the board exactly like one that cleared a threshold (row %+v)", r)
		}
	}
	if len(v.Disqualified) != 1 {
		t.Fatalf("want the not-configured run in Disqualified, got ranked=%d disqualified=%d",
			len(v.Ranked), len(v.Disqualified))
	}
	var flagged bool
	for _, f := range v.Disqualified[0].Flags {
		if f == "no gate configured" {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("the row does not say WHY it is not ranked; flags = %v", v.Disqualified[0].Flags)
	}
}

func TestGateFailureDisqualifiesEvenAtTopQuality(t *testing.T) {
	v := Build("wf", []linkingest.LinkedRun{
		run("dddd000000004444", 0.99, runlink.GateFail, func(lr *linkingest.LinkedRun) {
			lr.Eval.GateFailures = []string{"cost_usd"}
		}),
		run("eeee000000005555", 0.70, runlink.GatePass),
	})
	if len(v.Ranked) != 1 || v.Ranked[0].ConfigHash != "eeee000000005555" {
		t.Fatalf("the highest-quality variant failed its gate and must not rank; ranked = %+v", v.Ranked)
	}
	if len(v.Disqualified) != 1 || len(v.Disqualified[0].FailedGates) != 1 {
		t.Fatalf("the disqualified row must name the failing metric; got %+v", v.Disqualified)
	}
}

// TestRunWithoutEvidenceIsListedNotRanked: a run linked before migration 0023 has no case count and no
// verdict. Ranking it would need a quality score it never sent; dropping it would show a tenant two of
// their five configurations with nothing saying so.
func TestRunWithoutEvidenceIsListedNotRanked(t *testing.T) {
	old := linkingest.LinkedRun{
		RunID: "run-old", TenantID: "t1", WorkflowID: "wf", ConfigHash: "ffff000000006666",
		LinkedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Scores:   []runlink.Score{{Metric: "quality", Value: 0.8}},
		// No Eval: GateOutcome is "", so EvalEvidencePresent is false.
	}
	v := Build("wf", []linkingest.LinkedRun{old})

	if len(v.Ranked) != 0 || len(v.Disqualified) != 0 {
		t.Fatalf("a run with no eval evidence was ranked; ranked=%+v disqualified=%+v", v.Ranked, v.Disqualified)
	}
	if len(v.Unmeasured) != 1 {
		t.Fatalf("the run was DROPPED rather than listed as unmeasured — a board silently missing a "+
			"tenant's configuration is the quietest kind of wrong; view = %+v", v)
	}
	if v.State != evalboard.StatePartial {
		t.Errorf("state = %q, want partial when a configuration could not be ranked", v.State)
	}
}

// TestOlderRunsAreSupersededNotAveraged: combining two reported intervals is not a valid interval over
// their union, and the observations needed to do it properly are exactly what does not cross.
func TestOlderRunsAreSupersededNotAveraged(t *testing.T) {
	newer := run("aaaa000000001111", 0.90, runlink.GatePass, at(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)))
	older := run("aaaa000000001111", 0.50, runlink.GatePass, at(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)))

	// Newest-first, as the store documents.
	v := Build("wf", []linkingest.LinkedRun{newer, older})

	if len(v.Ranked) != 1 {
		t.Fatalf("two runs of ONE configuration produced %d rows; a variant is a config hash", len(v.Ranked))
	}
	if got := v.Ranked[0].Score; got != 0.90 {
		t.Fatalf("score = %v, want the newest run's 0.90 (an average would be 0.70) — reported intervals "+
			"cannot be validly combined", got)
	}
	var said bool
	for _, n := range v.Notes {
		if len(n) > 0 && (contains(n, "superseded")) {
			said = true
		}
	}
	if !said {
		t.Errorf("the board discarded a run without saying so; notes = %v", v.Notes)
	}
}

// TestNoComponentsAreInvented: a Normalized of 0 beside a real Raw reads as "this metric contributed
// nothing", which is a measurement claim. Absent is the honest rendering.
func TestNoComponentsAreInvented(t *testing.T) {
	v := Build("wf", []linkingest.LinkedRun{run("aaaa000000001111", 0.9, runlink.GatePass)})
	for _, r := range v.Ranked {
		if len(r.Components) != 0 {
			t.Errorf("row %s carries %d component(s) this assembler cannot compute: %+v",
				r.VariantID, len(r.Components), r.Components)
		}
	}
}

func TestEmptyBoardIsEmptyNotComplete(t *testing.T) {
	v := Build("wf", nil)
	if v.State != evalboard.StateEmpty {
		t.Fatalf("state = %q, want empty for a tenant that has linked nothing", v.State)
	}
}

// TestRequestedProfileIsReportedAsNotApplied: silently ignoring it would render the profile's name over
// an unchanged ranking, which is the most convincing possible lie about this board.
func TestRequestedProfileIsReportedAsNotApplied(t *testing.T) {
	src := NewSource(stubRuns{runs: []linkingest.LinkedRun{run("aaaa000000001111", 0.9, runlink.GatePass)}})
	v, ok := src.Board("t1", "wf", "cost-first")
	if !ok {
		t.Fatal("no board")
	}
	var said bool
	for _, n := range v.Notes {
		if contains(n, "cost-first") && contains(n, "not applied") {
			said = true
		}
	}
	if !said {
		t.Fatalf("a requested profile was accepted silently; notes = %v", v.Notes)
	}
}

func TestUnknownWorkflowIsNotFound(t *testing.T) {
	src := NewSource(stubRuns{})
	if _, ok := src.Board("t1", "wf", ""); ok {
		t.Fatal("a workflow with no linked runs reported a board")
	}
}

type stubRuns struct {
	runs []linkingest.LinkedRun
	err  error
}

func (s stubRuns) ForWorkflow(_, _ string) ([]linkingest.LinkedRun, error) { return s.runs, s.err }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
