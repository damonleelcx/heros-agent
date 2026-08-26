package improvementrun

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/proposalgen"
	"github.com/heros-foreal/agentd/internal/verification"
)

// proposal_test.go proves §3: only a verified candidate is surfaced, the delta travels with it, a
// contract violation is rejected before verification, and every "nothing to propose" state says which.

func candidate(axis assessment.Axis) optimizer.SearchCandidate {
	return optimizer.SearchCandidate{
		DiagnosisID: "diag_1", Node: "n1", Dimension: string(axis),
		ConfigHash: "cfg_" + string(axis), Operator: "model_downgrade",
		Rationale: "this node dominates cost and a cheaper published tier exists",
		SpecBytes: []byte(`{}`),
	}
}

func passingVerdict(delta float64) optimizer.VerifyResult {
	r := verifiedResult(0.7, delta)
	// 🔴 A real per-candidate spend, small enough that it never reaches `okBounds`' $4.00 budget. A
	// zero would make every spend assertion in this package vacuously pass — including the per-tenant
	// attribution, which cannot be told apart from "no spend happened" when the number is zero.
	r.SpendUSD = 0.02
	r.Verdict.Delta = evalstats.Interval{
		Mean: delta, Low: delta - 0.02, High: delta + 0.02,
		NSeeds: 5, NCases: 40, Method: "bootstrap", Confidence: 0.95,
	}
	return r
}

func newProposal(t *testing.T, vr optimizer.VerifyResult) (VerifiedProposal, error) {
	t.Helper()
	p, _ := Translate("fix it", okBounds())
	return NewVerifiedProposal("run_1", p, candidate(assessment.AxisModel), vr,
		"prop_1", "diff_1", "2 files, +12 −4", "claude-opus-5-2026-05", nil)
}

// ── 3.4 only verified candidates surface, and the delta travels with them ────────────────────────

func TestAnUnverifiedCandidateIsNotSurfaced(t *testing.T) {
	unverified := passingVerdict(0.08)
	unverified.Verdict.GateResult = verification.GateFailSig
	unverified.Verdict.Significant = false
	if _, err := newProposal(t, unverified); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("an unverified candidate produced %v, not ErrNotVerified. Surfacing it puts a card in "+
			"front of somebody asking them to approve a change nothing measured", err)
	}
}

// TestAGateFailingHighScorerIsNotSurfaced is FR9 stated as the constraint it is: the composite is the
// objective, the gate is the constraint, and a constraint that yields to a high objective is not one.
func TestAGateFailingHighScorerIsNotSurfaced(t *testing.T) {
	high := passingVerdict(0.95) // an enormous gain …
	high.Verdict.GateResult = verification.GateFailRegress
	high.Verdict.RegressionPass = false
	if _, err := newProposal(t, high); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("a candidate with a +0.95 delta that FAILED a gate was surfaced (%v). However high the "+
			"composite is the exact phrasing of FR9", err)
	}
}

func TestTheVerifiedDeltaAndItsIntervalTravelWithTheProposal(t *testing.T) {
	p, err := newProposal(t, passingVerdict(0.08))
	if err != nil {
		t.Fatal(err)
	}
	if p.Delta.NSeeds != 5 || p.Delta.NCases != 40 {
		t.Fatalf("the proposal lost the size of the set behind its delta: %+v", p.Delta)
	}
	label := p.DeltaLabel()
	for _, want := range []string{"+0.080", "CI", "40 cases", "5 seeds"} {
		if !strings.Contains(label, want) {
			t.Fatalf("the rendered delta %q does not carry %q; a number without its interval and its "+
				"denominator is not evidence", label, want)
		}
	}
}

// TestACandidateThatDidNotBuildIsNotSurfaced isolates the `MergeReady()` guard.
//
// 🔴 It exists because the fence drill found `TestAGateFailingHighScorerIsNotSurfaced` passing with that
// guard removed — `Validate()`'s own gate check caught the case too. That is defence in depth working
// and it made the drill unable to prove either guard. This case is caught by `MergeReady()` and by
// NOTHING ELSE: the gate PASSED and the candidate did not compile, so `Validate()` sees `GatePass` and
// lets it through.
func TestACandidateThatDidNotBuildIsNotSurfaced(t *testing.T) {
	unbuilt := passingVerdict(0.08)
	unbuilt.Builds = false // the gate passed; the diff does not compile
	if _, err := newProposal(t, unbuilt); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("a candidate that does not build was surfaced (%v). A pull request whose diff does not "+
			"compile is a change a reviewer cannot merge and we said was verified", err)
	}
}

// TestAStoredProposalWithAFailedGateIsRefusedOnRead isolates `Validate()`'s guard.
//
// 🔴 A `VerifiedProposal` that arrives from a STORE is one somebody could have written by hand, and
// `NewVerifiedProposal`'s checks never ran on it. This is the guard that stands between a row and a
// card, and the drill needs a case only it can catch.
func TestAStoredProposalWithAFailedGateIsRefusedOnRead(t *testing.T) {
	fromStore := VerifiedProposal{
		ProposalID: "prop_1", ConfigHash: "cfg", SourceRevision: "rev",
		Axis: assessment.AxisModel, ProviderModelVersion: "claude-opus-5-2026-05",
		Delta:      evalstats.Interval{Mean: 0.9, Low: 0.8, High: 1.0, NSeeds: 5, NCases: 40},
		GateResult: verification.GateFailRegress,
	}
	if err := fromStore.Validate(); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("a stored proposal whose gate FAILED validated on read (%v). Nothing re-ran the "+
			"constructor's checks on it, so this is the only guard between that row and a card", err)
	}
}

func TestADeltaWithNoEvidenceBehindItIsRefused(t *testing.T) {
	naked := passingVerdict(0.08)
	naked.Verdict.Delta = evalstats.Interval{Mean: 0.08, Low: 0.06, High: 0.10} // no seeds, no cases
	_, err := newProposal(t, naked)
	if err == nil {
		t.Fatal("a delta with no seed count and no case count was accepted. An interval with nothing " +
			"behind it is a number wearing a statistic's clothes, and FR10 requires the size of the set")
	}
}

func TestAProposalWithNoProviderModelVersionIsRefused(t *testing.T) {
	p, _ := Translate("fix it", okBounds())
	_, err := NewVerifiedProposal("run_1", p, candidate(assessment.AxisModel), passingVerdict(0.08),
		"prop_1", "diff_1", "stat", "", nil)
	if err == nil {
		t.Fatal("a proposal recording no provider model version was accepted. Re-measurement can then " +
			"withdraw a good change because the provider moved, and nobody can tell which happened " +
			"(design D2's trap)")
	}
}

func TestATieIsRenderedAsATie(t *testing.T) {
	tie := passingVerdict(0.002)
	tie.Verdict.Significant = false
	p, err := newProposal(t, tie)
	if err != nil {
		t.Fatalf("a gate-passing but non-significant result was refused construction: %v", err)
	}
	if !strings.Contains(p.DeltaLabel(), "no significant change") {
		t.Fatalf("a non-significant delta renders as %q. A tie presented as a gain is the single most "+
			"misleading thing this surface can show", p.DeltaLabel())
	}
}

// ── 3.3 a contract violation is rejected BEFORE verification ─────────────────────────────────────

type recordingContract struct {
	ok      bool
	reason  string
	callsAt *[]string
}

func (c recordingContract) Check(optimizer.SearchCandidate) (bool, string) {
	*c.callsAt = append(*c.callsAt, "contract")
	return c.ok, c.reason
}

func TestAContractViolationIsRejectedBeforeVerificationIsEvenRequested(t *testing.T) {
	var calls []string
	err := RejectBeforeVerification(recordingContract{ok: false, reason: "node n1 emits a type the next node cannot read", callsAt: &calls}, candidate(assessment.AxisModel))
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("a contract-violating candidate was admitted: %v", err)
	}
	if len(calls) != 1 || calls[0] != "contract" {
		t.Fatalf("call order was %v; the contract check must be the first thing that runs", calls)
	}
	if !strings.Contains(err.Error(), "node n1 emits a type") {
		t.Fatalf("the refusal dropped the contract's own reason: %v", err)
	}
}

// TestAMissingContractCheckerRefusesRatherThanAdmits is the fail-closed direction. A nil checker that
// returned "allowed" would make the gate's ABSENCE indistinguishable from its success — which is
// design.md's whole worry about a new caller in one line of code.
func TestAMissingContractCheckerRefusesRatherThanAdmits(t *testing.T) {
	if err := RejectBeforeVerification(nil, candidate(assessment.AxisModel)); !errors.Is(err, ErrContractViolation) {
		t.Fatalf("a nil contract checker admitted the candidate (%v). A gate that is not called and a "+
			"gate that passed must not produce the same outcome", err)
	}
}

// TestConstructionRejectsAContractViolationWithoutReadingTheVerdict asserts the ORDER once more at the
// place a caller is most likely to get it wrong: `NewVerifiedProposal` must report the contract
// violation, not "not verified", when both are true.
func TestConstructionRejectsAContractViolationWithoutReadingTheVerdict(t *testing.T) {
	bad := passingVerdict(0.08)
	bad.ContractOK, bad.ContractReason = false, "incoherent re-arrangement"
	_, err := newProposal(t, bad)
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("got %v, want a contract violation. Reporting it as \"not verified\" would file a "+
			"structural defect beside a measurement result, and only one of them is about the change", err)
	}
}

// ── 3.5 every "nothing to propose" state says which one ──────────────────────────────────────────

// TestEveryNothingToProposeStateHasItsOwnSentence reads `proposalgen.States()` rather than a count.
//
// ⚠️ The requirement says FIVE and there are SEVEN. Implementing five would leave two states falling
// through to a default, and a default IS the generic empty result the requirement forbids. See
// state.go's header.
func TestEveryNothingToProposeStateHasItsOwnSentence(t *testing.T) {
	seenHeadline := map[string]proposalgen.State{}
	n := 0
	for _, s := range proposalgen.States() {
		if !s.FoundNothing() {
			continue
		}
		n++
		es, err := EmptyStateFor(proposalgen.Result{State: s, Detail: "the pass's own words"})
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if es.Headline == "" {
			t.Fatalf("%s renders no headline, so it is an empty screen with a different name", s)
		}
		if prior, dup := seenHeadline[es.Headline]; dup {
			t.Fatalf("%s and %s render the SAME headline. That is the P30 defect exactly: two customers "+
				"in opposite situations read one sentence and take the same wrong action", s, prior)
		}
		seenHeadline[es.Headline] = s
		if es.Detail != "the pass's own words" {
			t.Fatalf("%s dropped the pass's own detail; the pass knows which revisions disagreed and "+
				"this table does not", s)
		}
	}
	if n < 7 {
		t.Fatalf("only %d nothing-to-propose states were rendered. The PRD says five and `proposalgen` "+
			"has seven; rendering five leaves two falling through to a default", n)
	}
}

func TestAHealthyEmptyStateIsNotRenderedAsAMissingInput(t *testing.T) {
	healthy, err := EmptyStateFor(proposalgen.Result{State: proposalgen.StateNoBottleneck, Detail: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if !healthy.Healthy {
		t.Fatal("`no_bottleneck` is not marked healthy. The platform looked and there was nothing to " +
			"improve; rendering that in the same tone as \"you have linked no runs\" tells a customer " +
			"their setup is broken when it is finished")
	}
	if healthy.NextAction != "" {
		t.Fatalf("`no_bottleneck` names the next action %q. There is nothing to do, and inventing a step "+
			"sends somebody to change something that is working", healthy.NextAction)
	}
	broken, _ := EmptyStateFor(proposalgen.Result{State: proposalgen.StateNoRuns, Detail: "d"})
	if broken.Healthy || broken.NextAction == "" {
		t.Fatal("`no_linked_runs` must be a missing input with a next action")
	}
}

func TestAnUnknownStateIsAnErrorRatherThanABlankCard(t *testing.T) {
	if _, err := EmptyStateFor(proposalgen.Result{State: "invented"}); err == nil {
		t.Fatal("an unknown state rendered without complaint. A state with no sentence renders as " +
			"nothing, which a reader cannot tell from a surface that was never asked")
	}
}
