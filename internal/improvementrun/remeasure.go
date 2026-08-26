package improvementrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// remeasure.go is FR15–FR17 and design D2: **the second observation is allowed to disagree, and
// disagreement withdraws the change before delivery.**
//
// # Why this is not a confirmation ritual
//
// The P5.5 gate ran on held-out data BEFORE the change was applied. Re-measurement observes the APPLIED
// result. If the second observation can only confirm, it is theatre — and a ritual that cannot fail
// teaches everyone downstream to ignore it. So this file is written so that a withdrawal is a normal,
// expected outcome rather than an error path.
//
// # 🔴 D2's trap, stated where the code is
//
// A delta can fail to reproduce for four reasons, and only ONE of them is "the change is bad":
//
//	the change is bad     → withdraw it. This is the outcome FR16 exists for.
//	the eval set is noisy → the intervals are wide and they OVERLAP, so `Reproduced` says yes. Held by
//	                        multi-seed intervals, which is why a single-seed measurement is refused.
//	the provider moved    → the two measurements ran against different models. Held by recording the
//	                        provider model version and comparing it FIRST.
//	the source moved      → the resolved config hash does not match what was requested. Held by FR17's
//	                        pinning, which FAILS the run rather than scoring it.
//
// **Three mechanisms must all be in place or none of them works.** Without them FR16 withdraws good
// changes for reasons nobody can see, and the feature gets blamed on model variance. That is stated in
// design D2 as the kind of dependency discovered after a feature ships; it is stated here so it is
// discovered in review instead.

// Measurement is one observation of a change's effect, with everything needed to compare it to another.
type Measurement struct {
	// Delta is the quality delta with its confidence interval, seed count and case count.
	Delta       evalstats.Interval `json:"delta"`
	Significant bool               `json:"significant"`
	// ProviderModelVersion is D2's third control variable. 🔴 REQUIRED: two measurements taken against
	// two different provider model versions are not comparable, and comparing them anyway attributes the
	// provider's change to the customer's.
	ProviderModelVersion string `json:"provider_model_version"`
	// ResolvedConfigHash is what the measurement run ACTUALLY resolved to, which is not necessarily what
	// was requested — that gap is FR17's whole subject.
	ResolvedConfigHash string `json:"resolved_config_hash"`
	SourceRevision     string `json:"source_revision"`
	// SpendUSD is what taking this observation cost. 🔴 Reported by the runner rather than estimated,
	// because decisions.md D-35.4 makes a withdrawn change's compute CHARGEABLE against the run's
	// budget while not billable — and "charged" is only meaningful if the number is measured.
	SpendUSD float64 `json:"spend_usd"`
	AtMS     int64   `json:"at_ms"`
}

// Label renders a measurement as one string, through the same formatter a proposal's delta uses, so the
// two numbers a withdrawal reports are formatted identically. Two formatters for one comparison is how
// a reader concludes the difference is bigger than it is.
func (m Measurement) Label() string {
	return VerifiedProposal{Delta: m.Delta, Significant: m.Significant}.DeltaLabel()
}

// Validate refuses a measurement that cannot be compared.
func (m Measurement) Validate() error {
	if m.ProviderModelVersion == "" {
		return errors.New("improvementrun: a measurement that records no provider model version cannot " +
			"be compared to another: a disagreement between them could not be told apart from the " +
			"provider moving underneath both")
	}
	if m.Delta.NSeeds <= 1 {
		// 🔴 More than ONE, not more than zero. A single-seed measurement produces an interval of width
		// zero, which never overlaps anything — so every re-measurement would "disagree" and every change
		// would be withdrawn. The mechanism that holds noise is the one being asserted here.
		return fmt.Errorf("improvementrun: this measurement ran on %d seed(s). A single-seed measurement "+
			"has an interval of width zero, which overlaps nothing — under it every re-measurement "+
			"disagrees and every change is withdrawn", m.Delta.NSeeds)
	}
	if m.Delta.NCases <= 0 {
		return errors.New("improvementrun: this measurement names no case count, so the size of the set " +
			"behind its interval is unknown")
	}
	return nil
}

// WithdrawalReason names WHY a change was withdrawn. A closed set, because the four answers send a
// reader to four different places and "it did not reproduce" is only one of them.
type WithdrawalReason string

const (
	// WithdrawnDidNotReproduce — the two measurements disagree and every control variable held. This is
	// the outcome FR16 exists for, and it is a statement about the CHANGE.
	WithdrawnDidNotReproduce WithdrawalReason = "did_not_reproduce"
	// WithdrawnProviderMoved — the two measurements ran against different provider model versions, so
	// they are not comparable. 🔴 NOT a statement about the change. Reporting this as
	// `did_not_reproduce` would blame a customer's change for a vendor's release.
	WithdrawnProviderMoved WithdrawalReason = "provider_moved"
	// WithdrawnPinBroken — the re-measurement run's resolved configuration is not the one that was
	// requested (FR17). The run FAILS rather than being scored, and this is what that failure is called.
	WithdrawnPinBroken WithdrawalReason = "pin_broken"
	// WithdrawnUnmeasurable — the re-measurement could not be taken at all. Also not a statement about
	// the change.
	WithdrawnUnmeasurable WithdrawalReason = "unmeasurable"
)

var withdrawalReasons = []WithdrawalReason{
	WithdrawnDidNotReproduce, WithdrawnProviderMoved, WithdrawnPinBroken, WithdrawnUnmeasurable,
}

// WithdrawalReasons returns the closed set. A copy.
func WithdrawalReasons() []WithdrawalReason {
	return append([]WithdrawalReason(nil), withdrawalReasons...)
}

// Valid reports membership.
func (r WithdrawalReason) Valid() bool {
	for _, v := range withdrawalReasons {
		if v == r {
			return true
		}
	}
	return false
}

// String makes WithdrawalReason printable.
func (r WithdrawalReason) String() string { return string(r) }

// AboutTheChange reports whether this reason says something about the customer's change.
//
// 🔴 Two of the four do not, and a surface that rendered all four alike would tell somebody their change
// was bad on a day a vendor shipped a model. This predicate is what the console switches on.
func (r WithdrawalReason) AboutTheChange() bool { return r == WithdrawnDidNotReproduce }

// Withdrawal is a change that was approved, applied, and then withdrawn before delivery.
//
// 🔴 BOTH measurements travel on it (FR16). A withdrawal with one number looks like a bug; with two it
// is a finding — *this looked like +8% and measured +1% ± 4%* — which is information about the eval set
// as much as about the change.
type Withdrawal struct {
	ProposalID string           `json:"proposal_id"`
	Axis       assessment.Axis  `json:"axis"`
	Reason     WithdrawalReason `json:"reason"`
	// Verified is the measurement the P5.5 gate took before the change was applied.
	Verified Measurement `json:"verified"`
	// Remeasured is the observation of the applied result. Zero-valued only when the reason is
	// `unmeasurable` or `pin_broken`, where no comparable number exists.
	Remeasured Measurement `json:"remeasured"`
	// Detail is a named condition, in the product's own nouns.
	Detail string `json:"detail"`
	// SpendUSD is what this withdrawn change cost. Recorded because decisions.md D-35.4 makes it
	// chargeable against the run's budget and NOT billable, and reporting it separately is what makes
	// "40% of this run's budget went to withdrawals" visible.
	SpendUSD float64 `json:"spend_usd"`
	AtMS     int64   `json:"at_ms"`
}

// Sentence is what a surface says. It reports both numbers when both exist, and says plainly when the
// withdrawal is not about the change.
func (w Withdrawal) Sentence() string {
	switch w.Reason {
	case WithdrawnDidNotReproduce:
		return fmt.Sprintf("This change was withdrawn after it was applied: it verified at %s and "+
			"re-measured at %s. Those two do not agree, so it was not delivered.",
			w.Verified.Label(), w.Remeasured.Label())
	case WithdrawnProviderMoved:
		return fmt.Sprintf("This change was withdrawn, and it is NOT a result about the change. It was "+
			"verified against provider model %s and re-measured against %s, so the two measurements are "+
			"not comparable.", w.Verified.ProviderModelVersion, w.Remeasured.ProviderModelVersion)
	case WithdrawnPinBroken:
		return "This change was withdrawn because the re-measurement resolved a different configuration " +
			"from the one requested. The run failed rather than being scored, which is deliberate: a " +
			"score from the wrong configuration is worse than no score."
	default:
		return "This change was withdrawn because it could not be re-measured: " + w.Detail +
			". That is not a result about the change."
	}
}

// ErrPinBroken is FR17: a re-measurement run whose resolved `config_hash` does not match what was
// requested FAILS rather than being scored.
//
// 🔴 An error rather than a low score, and the direction matters. A score from the wrong configuration
// is not a worse score — it is a number about something else, and it is indistinguishable from a real
// one once it is written down.
var ErrPinBroken = errors.New(
	"improvementrun: this measurement run resolved a different configuration from the one requested, " +
		"so it fails rather than being scored")

// Remeasurer takes the second observation.
//
// It is a seam because running the eval harness against an applied change is the harness's job, and P35
// supplies no scoring, no metric and no oracle.
type Remeasurer interface {
	// Remeasure runs the pinned measurement for one approved proposal. It MUST return `ErrPinBroken`
	// when the run resolves a configuration other than `want.ConfigHash` — never a scored result.
	Remeasure(ctx context.Context, p VerifiedProposal, want Binding) (Measurement, error)
}

// Reproduced reports whether a re-measurement is consistent with the verified delta.
//
// # Why interval OVERLAP and not "the mean is close enough"
//
// A threshold on the means is a number somebody picks, and every value of it is wrong for some eval set:
// too tight and a noisy set withdraws everything, too loose and a real regression passes. Overlap asks
// the question the intervals were computed to answer — *are these two observations consistent?* — and it
// scales with the evidence automatically: a set with more cases and more seeds produces narrower
// intervals, which makes the test stricter exactly where it can afford to be.
//
// It is `evalstats.Interval.Overlaps`, which is the platform's own tie predicate. 🚫 Not a second
// comparison written here: two definitions of "these agree" is two answers to the only question that
// decides whether a change ships.
func Reproduced(verified, remeasured Measurement) bool {
	return verified.Delta.Overlaps(remeasured.Delta)
}

// Reconcile decides what happens to an applied change, and returns a withdrawal when it must not ship.
//
// The order of the checks is the order of the four causes in the file header, and it is not arbitrary:
// the two that are NOT about the change are tested FIRST, so a run where the provider moved is never
// reported as a change that failed.
func Reconcile(p VerifiedProposal, verified Measurement, remeasured Measurement, remeasureErr error, atMS int64) (*Withdrawal, error) {
	base := Withdrawal{
		ProposalID: p.ProposalID, Axis: p.Axis, Verified: verified, Remeasured: remeasured, AtMS: atMS,
	}

	// 1 · FR17. The pin broke — the run resolved something other than what was requested.
	if errors.Is(remeasureErr, ErrPinBroken) {
		base.Reason, base.Detail = WithdrawnPinBroken, remeasureErr.Error()
		base.Remeasured = Measurement{}
		return &base, nil
	}
	// 2 · The measurement could not be taken.
	if remeasureErr != nil {
		base.Reason, base.Detail = WithdrawnUnmeasurable, remeasureErr.Error()
		base.Remeasured = Measurement{}
		return &base, nil
	}
	// 3 · Both measurements must be comparable at all.
	if err := verified.Validate(); err != nil {
		base.Reason, base.Detail = WithdrawnUnmeasurable, "the verified measurement is not comparable: "+err.Error()
		return &base, nil
	}
	if err := remeasured.Validate(); err != nil {
		base.Reason, base.Detail = WithdrawnUnmeasurable, "the re-measurement is not comparable: "+err.Error()
		return &base, nil
	}
	// 4 · D2's third control variable. Checked BEFORE the deltas are compared, because a provider that
	// moved makes the comparison meaningless rather than negative.
	if verified.ProviderModelVersion != remeasured.ProviderModelVersion {
		base.Reason = WithdrawnProviderMoved
		base.Detail = fmt.Sprintf("verified against %s, re-measured against %s",
			verified.ProviderModelVersion, remeasured.ProviderModelVersion)
		return &base, nil
	}
	// 5 · The question FR16 actually asks.
	if !Reproduced(verified, remeasured) {
		base.Reason = WithdrawnDidNotReproduce
		base.Detail = fmt.Sprintf("verified %s, re-measured %s", verified.Label(), remeasured.Label())
		return &base, nil
	}
	// It reproduced. 🔴 nil, nil — no withdrawal and no error. The caller proceeds to delivery.
	return nil, nil
}
