package proposal

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// P13 §5 — QA acceptance gate. Independent, red-able assertions of the phase's load-bearing properties.

// 5.1: on an UNGROUNDED request, every new prompt operator yields zero candidates (and no error).
func TestUngroundedYieldsZeroCandidates(t *testing.T) {
	e := p13Engine()
	em := e.Propose([]Target{p13Target("Answer the question.\nExample: a->a\nExample: a->a")}) // no cases
	for _, op := range []OperatorKind{OpInstructionHarden, OpFewShotCurate, OpPromptCompress, OpRedundancyRemove, OpPromptRewrite} {
		if hasCandidate(em.Candidates, op, "answer") {
			t.Errorf("%s emitted a candidate on an ungrounded request", op)
		}
	}
	for _, r := range em.Refusals {
		switch r.Operator {
		case OpInstructionHarden, OpFewShotCurate, OpPromptCompress, OpRedundancyRemove, OpPromptRewrite:
			t.Errorf("declining ungrounded was reported as an error for %s: %s", r.Operator, r.Reason)
		}
	}
}

// 5.2: a rewrite creates a NEW version id, and the parent stays resolvable (unchanged).
func TestRewriteCreatesNewVersionParentIntact(t *testing.T) {
	e := p13Engine()
	baseRefBefore := e.Base.Nodes["answer"].PromptRef // "" — the discovered/inline parent
	em := e.Propose([]Target{p13Target("Answer the question.", "c1")})
	c := findCandidate(t, em.Candidates, OpInstructionHarden, "answer")

	newRef := c.Spec.Nodes["answer"].PromptRef
	if newRef == "" || newRef == baseRefBefore {
		t.Errorf("rewrite did not mint a new version id (got %q, base %q)", newRef, baseRefBefore)
	}
	if len(newRef) != 64 {
		t.Errorf("new version id is not a content address: %q", newRef)
	}
	// Parent intact: the baseline is not mutated by the rewrite.
	if e.Base.Nodes["answer"].PromptRef != baseRefBefore {
		t.Error("the parent version was mutated by the rewrite")
	}
}

// 5.4: a non-overlapping downgrade is inadmissible even when its eval_cost_usd is strictly lower.
func TestNonOverlappingDowngradeInadmissible(t *testing.T) {
	cases := caseIDs(30)
	incumbent := successSeries("incumbent", cases, fiveSeeds, 0.95)
	cheaper := successSeries("cheaper", cases, fiveSeeds, 0.40) // clearly worse → non-overlapping

	res := EvaluateDowngradeGuardrail("cfg-qa-54", incumbent, cheaper, evalstats.DefaultConfig())
	if res.Verdict.Admissible() {
		t.Fatalf("a non-overlapping downgrade must be inadmissible, got %q", res.Verdict)
	}
	// Even a large cost saving does not rescue it.
	if ClassifyDowngrade(res, 0.05 /*incumbent eval_cost_usd*/, 0.001 /*much cheaper*/).Admissible {
		t.Error("lower eval_cost_usd must not admit a quality-regressing downgrade")
	}
}

// 5.5: every verdict carries a CI, CI-overlapping candidates report tied, and a single-seed run cannot
// decide (the harness refuses to aggregate under the seed floor).
func TestP13VerdictsAreMultiSeedWithTies(t *testing.T) {
	cases := caseIDs(20)
	a := successSeries("a", cases, fiveSeeds, 0.80)
	b := successSeries("b", cases, fiveSeeds, 0.80) // identical → overlapping CIs → tie

	cmp, err := evalstats.Compare(a, b, evalstats.HigherIsBetter, evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if cmp.Verdict != evalstats.VerdictTie {
		t.Errorf("overlapping intervals must report a tie, got %q", cmp.Verdict)
	}
	if cmp.A.Width() < 0 || cmp.A.NSeeds != len(fiveSeeds) {
		t.Errorf("verdict must carry a CI with its seed count, got %+v", cmp.A)
	}

	// A single-seed series cannot decide: aggregation refuses below the seed floor.
	single := successSeries("single", cases, []int64{1}, 0.80)
	if _, err := evalstats.Aggregate(single, evalstats.DefaultConfig()); err == nil {
		t.Error("a single-seed series must be refused, not silently aggregated into a decision")
	}
}

// 5.6: a shorter-but-worse prompt is NOT a win (FR8). Token reduction does not override verified quality.
func TestShorterWorsePromptIsNotAWin(t *testing.T) {
	cases := caseIDs(20)
	incumbent := successSeries("incumbent", cases, fiveSeeds, 0.90)
	shorterWorse := successSeries("compressed", cases, fiveSeeds, 0.55) // fewer tokens, worse quality

	// Compare the shorter candidate (a) against the incumbent (b) on task_success.
	cmp, err := evalstats.Compare(shorterWorse, incumbent, evalstats.HigherIsBetter, evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if cmp.Verdict == evalstats.VerdictAWins {
		t.Fatal("a shorter-but-worse prompt must not win on quality")
	}
	if cmp.Verdict != evalstats.VerdictBWins {
		t.Errorf("the incumbent should win over a shorter-but-worse prompt, got %q", cmp.Verdict)
	}
}
