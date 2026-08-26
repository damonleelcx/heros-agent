package improvementrun

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/optimizer"
)

// bound.go is FR26 and task 2.5: a run reports WHICH bound stopped it.
//
// # Why "which" and not "it stopped"
//
// The four bounds have four different next actions and one of them is "nothing — this worked":
//
//	spend_budget        the run was truncated by money. Raise the budget or narrow the scope.
//	candidate_cap       the run was truncated by count. It may have had more to give.
//	stopping_condition  the run found the best change reachable and stopped GAINING. Nothing to do.
//	kill_switch         somebody stopped it. Nothing about the run says anything about the repository.
//
// A surface that reported all four as "finished" would make the third indistinguishable from the first,
// so a person whose run converged would keep raising a budget that was never the constraint.
//
// # 🔴 A FAULT IS NOT A BOUND, and this is the distinction the type exists to keep
//
// `optimizer.StateStopped` covers the kill switch AND "verification unavailable" AND "the change ledger
// is down". Mapping the state to a bound would report an outage as a bound the customer reached —
// telling somebody their budget stopped their run on a day the verification service was merely
// unreachable. So `Outcome` carries a bound and a fault as SEPARATE fields, exactly one of which is
// populated, and `BoundNone` with a fault is a well-formed, honest answer.

// Bound is the closed set of things that stop a run. Four, plus the empty value meaning "no bound
// stopped it".
type Bound string

const (
	// BoundNone: no bound stopped this run. Either it is still running, or it ended on a fault — read
	// Outcome.Fault.
	BoundNone Bound = ""
	// BoundBudget: cumulative provider spend reached the plan's spend budget.
	BoundBudget Bound = "spend_budget"
	// BoundCandidateCap: the plan's candidate cap was reached.
	BoundCandidateCap Bound = "candidate_cap"
	// BoundStoppingCondition: the run stopped gaining — converged below the min-improvement floor, or
	// stalled, or exhausted the in-scope search space. 🔴 The three collapse into ONE bound on purpose:
	// they differ in mechanism and are identical in what the person should do about them, and a
	// distinction with no different action is a distinction that makes a screen harder to read.
	BoundStoppingCondition Bound = "stopping_condition"
	// BoundKillSwitch: the kill switch was armed, or the run was cancelled.
	BoundKillSwitch Bound = "kill_switch"
)

var bounds = []Bound{BoundBudget, BoundCandidateCap, BoundStoppingCondition, BoundKillSwitch}

// Bounds returns the closed set, sorted, for the fence that asserts each renders its own sentence.
func BoundsSet() []Bound {
	out := append([]Bound(nil), bounds...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports membership. The empty bound is valid — it is the "no bound" answer.
func (b Bound) Valid() bool {
	if b == BoundNone {
		return true
	}
	for _, v := range bounds {
		if v == b {
			return true
		}
	}
	return false
}

// String makes Bound printable in an error.
func (b Bound) String() string { return string(b) }

// Sentence is what a surface says when this bound stopped a run. One sentence per bound, and the
// mapping lives here rather than in a renderer for `assessment`'s reason: a second copy in TypeScript
// drifts, and the copy that drifts is the one a customer reads.
func (b Bound) Sentence() string {
	switch b {
	case BoundBudget:
		return "This run stopped because it reached its spend budget. What it found up to that point " +
			"stands; there may be more it did not reach."
	case BoundCandidateCap:
		return "This run stopped because it reached its candidate cap. What it found stands; the cap " +
			"was the limit, not the search."
	case BoundStoppingCondition:
		return "This run stopped because it stopped finding gains above the threshold. That is the " +
			"search finishing, not being cut short."
	case BoundKillSwitch:
		return "This run was stopped. Nothing it had not already delivered was delivered, and nothing " +
			"partial was pushed."
	default:
		return "This run did not stop on a bound."
	}
}

// Outcome is how one run ended: which bound stopped it, or which fault ended it, plus the numbers a
// person needs to decide whether to run it again.
//
// 🔴 `Bound` and `Fault` are mutually exclusive and both may be empty (a run that completed its whole
// scope without reaching a bound). `Validate` enforces the exclusivity, because an Outcome carrying
// both would be rendered by whichever field the surface happened to check first.
type Outcome struct {
	// Bound is which bound stopped it, or BoundNone.
	Bound Bound `json:"bound"`
	// Fault is a named internal failure that ended the run — verification unreachable, the change
	// ledger down. 🚫 NEVER a bound: reporting an outage as a bound tells a customer their budget
	// stopped a run that our own dependency stopped.
	Fault string `json:"fault,omitempty"`
	// State is the optimizer's own terminal state, carried through unchanged so an operator reading a
	// ledger entry and an operator reading the loop's audit trail are reading the same word.
	State optimizer.RunState `json:"state"`
	// Detail is the loop's own stop reason, verbatim. Not re-worded: a softened reason is a second,
	// weaker statement of it.
	Detail string `json:"detail"`

	CandidatesConsidered int     `json:"candidates_considered"`
	SpendUSD             float64 `json:"spend_usd"`
}

// Stopped reports whether a bound stopped this run.
func (o Outcome) Stopped() bool { return o.Bound != BoundNone }

// Faulted reports whether the run ended on an internal failure rather than on a bound.
func (o Outcome) Faulted() bool { return o.Fault != "" }

// Validate enforces the exclusivity of Bound and Fault.
func (o Outcome) Validate() error {
	if !o.Bound.Valid() {
		return fmt.Errorf("improvementrun: %q is not a bound", o.Bound)
	}
	if o.Stopped() && o.Faulted() {
		return fmt.Errorf("improvementrun: this outcome reports both the bound %q and the fault %q; a "+
			"run ends on one or the other, and a surface would render whichever it checked first",
			o.Bound, o.Fault)
	}
	return nil
}

// Sentence is the one line a surface renders. A fault says so explicitly, so nobody reads an outage as
// a verdict about their repository.
func (o Outcome) Sentence() string {
	if o.Faulted() {
		return "This run could not finish: " + o.Fault + ". This is not a result about your repository."
	}
	return o.Bound.Sentence()
}

// OutcomeOf reads a run's terminal state and says which bound stopped it.
//
// `killed` is passed in rather than inferred from the state, and that is the whole correctness of this
// function. `StateStopped` is the loop's answer for the kill switch AND for a dependency being
// unreachable; only the caller — which holds the kill switch — can tell them apart. Inferring it from
// the stop reason's TEXT was the alternative, and a bound decided by string-matching an error message
// is a bound that changes meaning when somebody improves the wording.
func OutcomeOf(res optimizer.RunResult, killed bool) Outcome {
	o := Outcome{
		State:                res.State,
		Detail:               res.StopReason,
		CandidatesConsidered: len(res.Iterations),
		SpendUSD:             res.CumulativeSpend,
	}
	switch {
	case killed:
		o.Bound = BoundKillSwitch
	case res.State == optimizer.StateHaltedBudget:
		o.Bound = BoundBudget
	case res.State == optimizer.StateMaxIter:
		o.Bound = BoundCandidateCap
	case res.State == optimizer.StateConverged, res.State == optimizer.StateStalled:
		o.Bound = BoundStoppingCondition
	case res.State == optimizer.StateStopped:
		// 🔴 Not a bound. The loop stopped for a reason that is OURS — verification unreachable, the
		// ledger down, the operator console's brake, a lock conflict — and every one of those is a fault
		// from the customer's point of view. The reason travels verbatim.
		o.Fault = res.StopReason
		if o.Fault == "" {
			o.Fault = "the run stopped without naming a reason, which is itself a defect"
		}
	case res.State == optimizer.StateHaltedRegression:
		// A regression halt is a bound in every sense that matters to a person: the run stopped itself,
		// deliberately, having measured something. It reports as the stopping condition because the
		// action is the same — read what it found, nothing to raise.
		o.Bound = BoundStoppingCondition
	}
	return o
}
