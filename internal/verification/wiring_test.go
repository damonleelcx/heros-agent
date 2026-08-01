package verification

import (
	"context"
	"testing"
)

// P15 §6.3 — a produced wiring candidate is EXPLORATORY until verification confirms it.
//
// The failure this guards against is specific and likely. A redundancy signal says "these two calls
// look like one"; a merge fuses them; the graph gets simpler and the cost goes down — and every one of
// those facts can be true while the answer gets worse, because the second call was CORRECTING the
// first. Nothing in the wiring space can tell the two apart. Only held-out measurement can.
//
// So the surfacing rule is not a wiring rule at all: the merge is subject to the same gate every other
// proposal passes through, and `Recommendations` — the ONE function the surface reads — filters on the
// verdict, not on the operator. This test proves the wiring axis has no bypass.

// mergeProposal is a merge candidate's verification input: a fused graph scored against the parent.
func mergeProposal() Proposal {
	p := baseProposal()
	p.ProposalID = "merge-B-absorbs-C"
	return p
}

// TestUnverifiedMergeNotSurfaced: a merge that reads redundant but scores WORSE on held-out data is
// withheld — never presented as a recommended change.
func TestUnverifiedMergeNotSurfaced(t *testing.T) {
	// The candidate is cheaper and faster (one fewer call — exactly the shape that looks like a win)
	// and worse on held-out quality, because the absorbed node was doing real work.
	r := fakeRunner{
		success: map[string]map[string]float64{
			baseCfg: succMap(evalSet, 0.80),
			candCfg: succMap(evalSet, 0.55),
		},
		cost:    map[string]float64{baseCfg: 0.02, candCfg: 0.01},
		latency: map[string]float64{baseCfg: 900, candCfg: 500},
	}
	v, err := Verify(context.Background(), r, mergeProposal(), evalSet, DefaultConfig())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Passed() {
		t.Fatalf("a merge that is cheaper but WORSE on held-out data must not pass the gate: %+v", v)
	}
	if !v.HeldOut {
		t.Error("the verdict must record that the delta was measured on held-out cases")
	}
	if v.Reason == "" {
		t.Error("a withheld proposal with no reason reads as 'the tool did not work'")
	}

	// The load-bearing assertion: the recommendation surface reads Recommendations(), and this verdict
	// is not in it. A cheaper graph does not buy its way onto the surface.
	if got := Recommendations([]Verdict{v}); len(got) != 0 {
		t.Fatalf("an unverified merge must not be recommended, got %+v", got)
	}
	withheld := Withheld([]Verdict{v})
	if len(withheld) != 1 || withheld[0].ProposalID != v.ProposalID {
		t.Fatalf("it must still be SHOWN as withheld, with its reason: %+v", withheld)
	}
}

// TestVerifiedMergeIsSurfaced is the positive control. A rule that withheld everything would pass the
// test above and be useless: a merge that IS better on held-out data must reach the surface, through
// the same gate and with no wiring-specific handling.
func TestVerifiedMergeIsSurfaced(t *testing.T) {
	r := fakeRunner{
		success: map[string]map[string]float64{
			baseCfg: succMap(evalSet, 0.50),
			candCfg: succMap(evalSet, 0.90),
		},
		cost:    map[string]float64{baseCfg: 0.02, candCfg: 0.01},
		latency: map[string]float64{baseCfg: 900, candCfg: 500},
	}
	v, err := Verify(context.Background(), r, mergeProposal(), evalSet, DefaultConfig())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Passed() {
		t.Fatalf("a merge that is better AND cheaper on held-out data must pass: %+v", v)
	}
	if got := Recommendations([]Verdict{v}); len(got) != 1 {
		t.Fatalf("a verified merge must be recommendable, got %+v", got)
	}
	if v.CostDelta >= 0 {
		t.Errorf("the fused graph is cheaper; the verdict must record it (%v)", v.CostDelta)
	}
}

// TestUnrunWiringCandidateNotSurfaced: a wiring candidate that was never verified is withheld too.
// "Produced" is not "recommended" — the exploratory state has to be reachable and it has to be
// unsurfaceable, or the whole verification-gated posture is one forgotten call away from collapsing.
func TestUnrunWiringCandidateNotSurfaced(t *testing.T) {
	produced := Verdict{ProposalID: "merge-produced-never-verified", ConfigHash: candCfg, GateResult: GateUnrun}
	if produced.Passed() {
		t.Fatal("an unrun verdict must not pass")
	}
	if got := Recommendations([]Verdict{produced}); len(got) != 0 {
		t.Fatalf("a produced-but-unverified wiring candidate must not surface, got %+v", got)
	}
}
