package improvementrun

import (
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/verification"
)

// proposal.go is FR8/FR9/FR10: only a P5.5-verified candidate is surfaced, and the verified delta
// travels WITH the proposal everywhere it renders.
//
// # Why the delta is a field on the proposal rather than a lookup
//
// FR10 says "wherever it is rendered". A renderer that had to fetch the verdict separately is a
// renderer that can skip the fetch — and the version that skips it looks identical: a card with a
// title, a diff and an operator name, asking somebody to open a pull request on faith. So the delta,
// its interval, the size of the set behind it and whether that set could have failed are all carried on
// the value the surface receives, and `VerifiedProposal` cannot be CONSTRUCTED without them.
//
// 🔴 That is the load-bearing part: `NewVerifiedProposal` is the only constructor and it refuses a
// candidate whose verdict did not pass. There is no exported struct literal path that produces a
// surfaceable proposal from an unverified candidate — the fields are exported for JSON, and the
// validity is asserted at the one place the value is made.

// ErrNotVerified is the refusal a candidate gets when the P5.5 gate did not pass.
//
// 🔴 Deliberately NOT `forgedelivery.ErrNotVerified`. They mean the same thing at two different
// distances from the forge, and collapsing them would let a caller satisfy one by catching the other —
// which is how a "verified" check becomes a check that some other layer already did.
var ErrNotVerified = errors.New(
	"improvementrun: this candidate did not pass the held-out verification gate, so it is not surfaced")

// ErrContractViolation is a candidate rejected by the typed I/O contract. It is returned BEFORE any
// verification is requested (FR6) — see `RejectBeforeVerification`.
var ErrContractViolation = errors.New(
	"improvementrun: this candidate violates a typed I/O contract and is rejected before verification")

// VerifiedProposal is a candidate that passed, in the shape every surface renders.
//
// Every field a reviewer needs to decide is here, and the ones that make the delta INTERPRETABLE are
// not optional: an interval without the number of seeds behind it, or a score from an eval set that
// cannot fail, are both "a number" and neither is evidence.
type VerifiedProposal struct {
	// ProposalID is the ledger artifact. 🚫 A model cannot mint one.
	ProposalID string `json:"proposal_id"`
	RunID      string `json:"run_id"`
	TenantID   string `json:"tenant_id"`

	// ConfigHash and SourceRevision are what an approval BINDS to (FR13). Carried on the proposal so
	// the binding is visible at the point of consent rather than resolved somewhere else.
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`

	// Axis and Node locate the change. Axis is the nine-axis vocabulary, so the scope a person asked
	// for, the report they read and this card all use one dictionary.
	Axis assessment.Axis `json:"axis"`
	Node string          `json:"node"`
	// Operator names WHICH change this is, from `internal/proposal`'s catalog. P35 adds none (FR4).
	Operator string `json:"operator"`
	// Rationale is the operator's own words about why it fired.
	Rationale string `json:"rationale"`

	// Delta is the VERIFIED delta with its confidence interval, seed count and case count. 🔴 Carried
	// whole rather than as a formatted string: the console renders it through the platform's pinned
	// formatter, and a pre-formatted number is a second place the same value is formatted.
	Delta evalstats.Interval `json:"delta"`
	// Significant is the gate's own significance decision. Rendered beside the delta, because a delta
	// whose interval straddles zero is a tie, and a tie presented as a gain is the single most
	// misleading thing this surface could show.
	Significant bool `json:"significant"`
	// HeldOut is false when no held-out split could be formed. Carried so "verified" never silently
	// means "verified on the cases that produced it".
	HeldOut bool `json:"held_out"`
	// GateResult is the verdict's own terminal value, carried verbatim.
	GateResult verification.GateResult `json:"gate_result"`

	// EvalSet is the decisiveness report (§7.2): how many cases, how many of them carry an oracle that
	// could return NO. Optional only because a verdict reported by a customer's CI arrives without one.
	EvalSet *assessment.EvalSetReport `json:"eval_set,omitempty"`
	// EvalSetCannotFail is computed server-side from the CASE LIST, exactly as `assessments.go` does it,
	// so the claim and the enumeration behind it cannot disagree. When true, the surface states that the
	// set cannot fail and does not present the score as evidence of quality.
	EvalSetCannotFail bool `json:"eval_set_cannot_fail,omitempty"`

	// DiffRef points at the compiled diff. A reference, so the diff is read where diffs are read.
	DiffRef string `json:"diff_ref"`
	// DiffStat is the short human summary for the pull-request body.
	DiffStat string `json:"diff_stat"`

	// ProviderModelVersion is design D2's third control variable, recorded at MEASUREMENT time. Without
	// it a re-measurement that disagrees cannot be told apart from a provider that moved underneath us,
	// and FR16 would withdraw good changes for a reason nobody can see.
	ProviderModelVersion string `json:"provider_model_version"`

	// CostDelta and LatencyDelta are the other two axes a reviewer weighs. A quality gain bought with a
	// latency regression is a trade, and a card that showed only the gain would be asking for a decision
	// with half the inputs.
	CostDelta    float64 `json:"cost_delta"`
	LatencyDelta float64 `json:"latency_delta"`
}

// DeltaLabel renders the delta and its interval as one string, for the surfaces that need text — the
// pull-request body and the conversational `proposal` payload.
//
// 🔴 Computed HERE, once, rather than in each renderer. `conversation.ProposalPayload.DeltaLabel`'s own
// comment states the rule this obeys: the moment a second place formats the number, the two disagree.
func (p VerifiedProposal) DeltaLabel() string {
	if !p.Significant {
		return fmt.Sprintf("no significant change (%+.3f, 95%% CI %+.3f to %+.3f over %d cases × %d seeds)",
			p.Delta.Mean, p.Delta.Low, p.Delta.High, p.Delta.NCases, p.Delta.NSeeds)
	}
	return fmt.Sprintf("%+.3f (95%% CI %+.3f to %+.3f over %d cases × %d seeds)",
		p.Delta.Mean, p.Delta.Low, p.Delta.High, p.Delta.NCases, p.Delta.NSeeds)
}

// Validate refuses a proposal that cannot be rendered honestly.
//
// It is called by the constructor and again by anything that receives one over a boundary, because a
// proposal that arrives from a store is a proposal somebody could have written by hand.
func (p VerifiedProposal) Validate() error {
	if p.ProposalID == "" {
		return errors.New("improvementrun: a proposal with no id cannot be approved or delivered")
	}
	if p.ConfigHash == "" || p.SourceRevision == "" {
		return errors.New("improvementrun: a proposal must carry the (config_hash, source_revision) an " +
			"approval binds to; without both, an approval is an approval for a diff nobody saw")
	}
	if !p.Axis.Valid() {
		return fmt.Errorf("improvementrun: %q is not one of the nine axes", p.Axis)
	}
	if p.GateResult != verification.GatePass {
		return fmt.Errorf("%w (gate result %q)", ErrNotVerified, p.GateResult)
	}
	if p.Delta.NSeeds <= 0 || p.Delta.NCases <= 0 {
		// 🔴 An interval with no seeds and no cases behind it is a number wearing a statistic's clothes.
		// Refusing it here is what makes FR10's "with its confidence interval and the size of the set
		// behind it" a property of the value rather than a hope about the renderer.
		return errors.New("improvementrun: this proposal's delta names no seed count or case count, so " +
			"its interval cannot be read; a delta with no evidence behind it is not a verified delta")
	}
	if p.ProviderModelVersion == "" {
		// design D2's trap, made structural: withdrawal on re-measurement is only interpretable when the
		// provider is held fixed, and it is only held fixed if the version was recorded.
		return errors.New("improvementrun: this proposal records no provider model version, so a " +
			"re-measurement that disagrees could not be told apart from the provider moving")
	}
	return nil
}

// NewVerifiedProposal is the ONLY constructor. It refuses anything the gate did not pass.
//
// 🔴 A candidate that failed a gate is refused with `ErrNotVerified` even when its composite is the
// highest in the run (FR9). The composite is the objective; the gate is the constraint, and a
// constraint that yields to a high objective is not one.
func NewVerifiedProposal(
	runID string, p Plan, cand optimizer.SearchCandidate, vr optimizer.VerifyResult,
	proposalID, diffRef, diffStat, providerModelVersion string, evalSet *assessment.EvalSetReport,
) (VerifiedProposal, error) {
	if !vr.ContractOK {
		return VerifiedProposal{}, fmt.Errorf("%w: %s", ErrContractViolation, vr.ContractReason)
	}
	if !vr.MergeReady() {
		return VerifiedProposal{}, fmt.Errorf("%w (contract=%v builds=%v gate=%q)",
			ErrNotVerified, vr.ContractOK, vr.Builds, vr.Verdict.GateResult)
	}
	out := VerifiedProposal{
		ProposalID: proposalID, RunID: runID, TenantID: p.TenantID,
		ConfigHash: cand.ConfigHash, SourceRevision: p.SourceRevision,
		Axis: assessment.Axis(cand.Dimension), Node: cand.Node,
		Operator: cand.Operator, Rationale: cand.Rationale,
		Delta: vr.Verdict.Delta, Significant: vr.Verdict.Significant, HeldOut: vr.Verdict.HeldOut,
		GateResult:           vr.Verdict.GateResult,
		EvalSet:              evalSet,
		DiffRef:              diffRef,
		DiffStat:             diffStat,
		ProviderModelVersion: providerModelVersion,
		CostDelta:            vr.Verdict.CostDelta,
		LatencyDelta:         vr.Verdict.LatencyDelta,
	}
	if evalSet != nil {
		out.EvalSetCannotFail = evalSet.CannotFail()
	}
	if err := out.Validate(); err != nil {
		return VerifiedProposal{}, err
	}
	return out, nil
}

// RejectBeforeVerification is FR6/task 3.3, expressed as a function so the ORDER is a call site
// somebody can point at rather than a property of how a switch happens to be written.
//
// # Why the order matters more than it looks
//
// A contract-violating candidate that reaches verification costs a provider call and produces a verdict
// — and a verdict is a row. Once the row exists, "was this ever verified" has an answer that reads yes
// for a candidate that should never have been measured, and the delivery oracle reads that row.
//
// The optimizer's `ComposedVerifier` already short-circuits contract → build → gate in this order, so
// this function is not a second check. It is the assertion that P35's path REACHES that ordering, which
// is exactly the class of thing a new caller silently drops.
func RejectBeforeVerification(check optimizer.ContractChecker, cand optimizer.SearchCandidate) error {
	if check == nil {
		// 🚫 Not "allow". A missing contract checker is a misconfiguration, and admitting the candidate
		// would make the gate's absence indistinguishable from its success.
		return fmt.Errorf("%w: no typed-contract checker is configured, so no candidate can be admitted",
			ErrContractViolation)
	}
	if ok, reason := check.Check(cand); !ok {
		return fmt.Errorf("%w: %s", ErrContractViolation, reason)
	}
	return nil
}
