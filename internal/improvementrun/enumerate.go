package improvementrun

import (
	"sync"

	"github.com/heros-foreal/agentd/internal/optimizer"
)

// enumerate.go is task 2.4: a bounded `optimizer.Enumerator` built from the plan, driving the EXISTING
// `optimizer.Controller`. No fork of the loop.
//
// # What "no fork" means in practice, and why it is the risk
//
// The tempting shape is a P35 loop that calls proposal, then verification, then delivery, in the order
// the PRD's diagram draws them. It would be readable and it would be a second implementation of
// `optimizer.Controller` — and the second implementation is the one that does not call the gate,
// because the gate is a line in the first one that nobody copied. `TestLoop_GateFailingHighScorerNotMerged`
// would stay green the whole time, because it tests the loop that is no longer on the path.
//
// So P35 supplies an `Enumerator` and a `Constraints`, and the shipped loop does the rest. Everything
// this file adds is SUBTRACTIVE: it emits fewer candidates than the delegate would.
//
// # The two subtractions, and why each is here rather than in the delegate
//
//	scope   The plan's axes. A candidate on an axis the person did not ask about is one they did not
//	        agree to spend on. Filtering here rather than asking the delegate for a subset keeps every
//	        existing enumerator usable unchanged — the delegate is `internal/proposal`, and P35 adds no
//	        operator (FR4).
//	cap     The plan's candidate cap. 🔴 It is enforced HERE AS WELL as through
//	        `Constraints.MaxIterations`, and the redundancy is deliberate: MaxIterations bounds how many
//	        candidates are VERIFIED, and this bounds how many are ADMITTED. A delegate that returned
//	        ten thousand candidates would cost nothing to verify beyond the cap and would still have to
//	        be built, sorted and held in memory first.
//
// # 🔴 The cap counts DISTINCT candidates, and re-emits the ones it already admitted
//
// This was got wrong first, and the way it failed is worth recording because it is invisible from
// inside this file. `optimizer.Controller` RE-ENUMERATES on every iteration and filters out the config
// hashes it has already consumed itself. A cap counting total EMISSIONS therefore returns the full set
// on iteration 0 and nothing on iteration 1 — so the loop reads "the search space is exhausted" after a
// single candidate and reports `stalled`. The run ends early, under a cap of twenty, having tried one
// thing, and every bound reports normally.
//
// Found by running the fence against the real `optimizer.Controller`. A helper that simulated the loop
// would have agreed with the wrong implementation, which is the second half of why task 2.4's fence
// drives the shipped loop.

// BoundedEnumerator wraps a delegate enumerator and emits only candidates the plan admits, up to the
// plan's candidate cap.
//
// Safe for concurrent use: `optimizer.Search.NextCandidates` is called from one loop today, but the cap
// is a counter and a counter that is only correct single-threaded is a cap that silently stops being
// one the first time somebody parallelises the search.
type BoundedEnumerator struct {
	// Plan is the scope and the cap.
	Plan Plan
	// Delegate is the real enumerator — in production an adapter over `proposal.Engine.Propose`. P35
	// adds no operator; this is the same catalog every other caller uses.
	//
	// 🔴 It MUST be deterministic per target: the same target must offer the same candidates with the
	// same config hashes on every call, because the loop re-enumerates each iteration and matches
	// against its own consumed set by hash. A delegate that minted a fresh hash each call would make
	// every candidate look new, the cap would fill on the first iteration, and the run would report the
	// search space as exhausted. `proposal.Engine.Propose` is a pure function of its targets and menu,
	// so this holds for the production enumerator; a future delegate that does not is a defect here.
	Delegate optimizer.Enumerator

	mu sync.Mutex
	// admitted is the set of candidate config hashes this enumerator has let through, in admission
	// order. It is the cap's denominator, and it is a SET rather than a counter because the loop
	// re-enumerates every iteration — see the file header.
	admitted map[string]bool
	order    []string
	// axisOf remembers which axis each admitted candidate was on, so the ledger can record it per
	// candidate rather than as a per-axis total. A total cannot be replayed into a breakdown when a run
	// is reconstructed from the record.
	axisOf map[string]string
	// perAxis is §9.5's obligation applied at the earliest possible point: how many candidates each
	// axis produced. 🔴 Recorded here rather than derived later from the loop's iterations, because the
	// loop only iterates over candidates it CONSUMED — an axis that generated forty candidates the cap
	// truncated would show as zero, and "this operator produces nothing" and "this operator was
	// truncated" are opposite findings.
	perAxis map[string]int
	// outOfScope counts candidates the delegate offered that the plan's scope excluded. Published so
	// "the scope excluded everything" is distinguishable from "the operators produced nothing" — the
	// same two-opposites problem, one level up.
	outOfScope int
}

// NewBoundedEnumerator builds one from a plan and a delegate.
func NewBoundedEnumerator(p Plan, delegate optimizer.Enumerator) *BoundedEnumerator {
	return &BoundedEnumerator{
		Plan: p, Delegate: delegate,
		admitted: map[string]bool{}, perAxis: map[string]int{}, axisOf: map[string]string{},
	}
}

// Enumerate implements optimizer.Enumerator.
//
// 🔴 A nil delegate returns nothing rather than panicking, and the distinction is reported by
// `Emitted() == 0` with `OutOfScope() == 0` — which the caller renders as `proposalgen`'s
// `no_admissible_candidate` state rather than as a crash. A conversational surface that panicked on a
// misconfiguration would take down a turn that could have said what was wrong.
func (e *BoundedEnumerator) Enumerate(t optimizer.Target) []optimizer.SearchCandidate {
	if e.Delegate == nil {
		return nil
	}
	// 🔴 The target's own dimension is checked FIRST, so an out-of-scope target never reaches the
	// delegate at all. Filtering only the OUTPUT would let a scope-excluded target run whatever the
	// delegate does to produce candidates — which for a production enumerator means work, and possibly
	// a provider call, on an axis the person did not ask about.
	if t.Dimension != "" && !e.Plan.InScope(t.Dimension) {
		e.mu.Lock()
		e.outOfScope++
		e.mu.Unlock()
		return nil
	}

	offered := e.Delegate.Enumerate(t)

	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]optimizer.SearchCandidate, 0, len(offered))
	for _, c := range offered {
		axis := c.Dimension
		if axis == "" {
			// The search fills an empty dimension from the target before the loop sees it, so an empty
			// one here means the target had none either. Attribute it to the target's dimension so it is
			// not counted under "".
			axis = t.Dimension
		}
		if !e.Plan.InScope(axis) {
			e.outOfScope++
			continue
		}
		if e.admitted[c.ConfigHash] {
			// Already admitted on an earlier iteration. Re-emitted so the loop can keep filtering its
			// own consumed set — withholding it here is what starved the loop. See the file header.
			out = append(out, c)
			continue
		}
		if len(e.admitted) >= e.Plan.CandidateCap {
			// 🚫 The remainder is DROPPED, not queued. A queue would make the cap a delay rather than a
			// bound, and a person who agreed to twenty candidates would get twenty now and the rest on
			// the next run they did not ask for.
			continue
		}
		e.admitted[c.ConfigHash] = true
		e.order = append(e.order, c.ConfigHash)
		e.axisOf[c.ConfigHash] = axis
		e.perAxis[axis]++
		out = append(out, c)
	}
	return out
}

// Admitted is how many DISTINCT candidates this enumerator let through. It is what the cap bounds and
// what the ledger records as "candidates generated".
func (e *BoundedEnumerator) Admitted() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.admitted)
}

// AdmittedHashes returns the admitted candidates' config hashes in admission order. Used by the ledger
// so "which candidates did this plan generate" is answerable from the record rather than re-derived.
func (e *BoundedEnumerator) AdmittedHashes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.order...)
}

// AxisOf returns the axis an admitted candidate was on, or "" if it was never admitted.
func (e *BoundedEnumerator) AxisOf(configHash string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.axisOf[configHash]
}

// CapReached reports whether the candidate cap was the reason enumeration stopped.
//
// It is what lets `OutcomeOf` distinguish `candidate_cap` from `stopping_condition` when the loop ends
// having exhausted the candidates it was given: the loop cannot tell "there were no more" from "we
// stopped handing them over", because from inside the loop those are the same event.
func (e *BoundedEnumerator) CapReached() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.admitted) >= e.Plan.CandidateCap
}

// OutOfScope is how many candidates (and targets) the plan's scope excluded.
func (e *BoundedEnumerator) OutOfScope() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.outOfScope
}

// PerAxis is the generated-per-axis breakdown (§9.5, task 7.15). A copy.
//
// 🚫 There is deliberately no `Total()` and no mean. A caller that wants an aggregate writes the loop,
// and writing it is a decision somebody makes rather than a default they inherit — the same absence
// `proposal.AxisPassRate` maintains, for the same reason.
func (e *BoundedEnumerator) PerAxis() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int, len(e.perAxis))
	for k, v := range e.perAxis {
		out[k] = v
	}
	return out
}

// compile-time assertion: this is the loop's own seam, not a parallel one.
var _ optimizer.Enumerator = (*BoundedEnumerator)(nil)
