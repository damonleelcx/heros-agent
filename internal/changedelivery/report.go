package changedelivery

import "fmt"

// report.go — delivery as a REPORTED STATE (P13 §23.1/23.3, FR57/FR58/FR60).
//
// # The bug this file exists to make impossible
//
// Before it, a change whose rewriter refused produced no diff, therefore no pull request, therefore
// nothing — and the product said nothing about it. On every surface that state was indistinguishable
// from "queued behind other work". A customer looking at a verified memory proposal saw something that
// looked like a promise and was actually a dead end.
//
// So there is no path here that returns silence. Every change resolves to a State, both routes always
// report an outcome, and the one state that means "this is finished" is reachable only through a merged
// pull request.

// State is a change's delivery state. The set is closed and small on purpose: a surface's switch must
// be exhaustive, and a sixth state added casually is how "undeliverable" would eventually acquire a
// hopeful synonym.
type State string

const (
	// StateNothingToDeliver — the change resolves to the identity configuration (clearing a memory
	// strategy back to `none`, clearing an override). 🔴 A third value, deliberately: reporting it as
	// `delivered` would count a no-op as a delivery, and reporting it as a refusal would alarm someone
	// whose change is simply already true.
	StateNothingToDeliver State = "nothing-to-deliver"
	// StateUndeliverable — every route refused. Both causes are carried, and this is a TERMINAL,
	// HONEST state rather than a queue position.
	StateUndeliverable State = "undeliverable"
	// StateSourcePending — the source route can carry it; a pull request is the next step and a human
	// merge is the one after.
	StateSourcePending State = "source-pending"
	// StateRolloutActive — a rollout is running. 🚫 NOT a delivery: it is evidence under real load, and
	// RemainingStep says what permanence still costs.
	StateRolloutActive State = "rollout-active"
	// StateDelivered — a pull request carrying this change was merged. The ONLY terminal success, and
	// the only state a rollout can never reach on its own (FR60).
	StateDelivered State = "delivered"
)

// States lists them in narrative order for a surface that renders a legend.
func States() []State {
	return []State{StateNothingToDeliver, StateUndeliverable, StateSourcePending, StateRolloutActive, StateDelivered}
}

// RouteOutcome is one route's answer for one concrete change.
type RouteOutcome struct {
	Route  Route  `json:"route"`
	Status Status `json:"status"`
	// Cause is the refusal's stable identifier. For the runtime route it is one of this package's three
	// causes; for the source route it is the transform layer's own cause class or a gate rejection —
	// deliberately NOT translated into this package's vocabulary, because the two taxonomies answer
	// different questions and collapsing them would lose which one refused.
	Cause string `json:"cause,omitempty"`
	// Owner is who can close it: "nobody", "you", or "the platform".
	Owner string `json:"owner,omitempty"`
	// Permanent marks a boundary. 🔴 A permanent outcome must carry NO MissingArtifact — see
	// Report.Validate.
	Permanent       bool   `json:"permanent,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
}

// Refused reports whether this route refused.
func (o RouteOutcome) Refused() bool { return o.Status == StatusRefuses }

// SourceOutcome is what the source route reports for a concrete change, supplied by the caller from
// `transform.AxisCoverage()` (see source.go) or from a gate rejection.
//
// It is an INPUT rather than something this file computes, so the state machine stays a pure function
// and can be tested against every combination without standing up a transform engine.
type SourceOutcome struct {
	// Materializes is true when the rewriter emits a change for this (axis, language, form).
	Materializes bool
	// Varies is true when SOME form in this language materializes and some does not, and no form was
	// supplied to decide between them.
	//
	// 🔴 It is deliberately distinct from a plain refusal. For "will MY call site get a diff?" the
	// conservative answer is no, and that is what Materializes reports. But for "is this change
	// undeliverable anywhere?" the same value would be a lie — some call site does get a diff. A
	// consumer that collapsed the two would count a partially-covered axis as a dead end.
	Varies bool
	// Cause is the transform cause class, or a gate rejection identifier, when it does not.
	Cause string
	// Permanent marks a source refusal that no materializer would fix (a run-time-produced value, a
	// call site that cannot carry the change).
	Permanent       bool
	MissingArtifact string
	Note            string
	// GateRejected marks a change a safety gate refused outright. 🔴 When true the change is
	// undeliverable by EVERY route — see Report(): a gate that produces no runnable spec must not be
	// routed around by the runtime route.
	GateRejected bool
	// CallSiteCannotCarry marks a refusal that is a fact about THIS CALL SITE'S OWN SOURCE — arguments
	// unpacked from a mapping, a tool list assembled at run time, a model bound on a builder two
	// statements earlier.
	//
	// 🔴 When true, the runtime route reports the same thing. A call site that constructs no tool list
	// has nothing to select from whether the selection arrives from a codemod or from a binding
	// document, and telling that author "the document has no field yet" would send them to wait for a
	// field that will not help them the day it lands. Both routes name the call site (P14 FR56).
	CallSiteCannotCarry bool
	// Merged is true once a pull request carrying this change was merged by a human. The only input
	// that can produce StateDelivered.
	Merged bool
}

// RolloutStatus is the runtime route's live state for a concrete change.
type RolloutStatus struct {
	// Active is true while a rollout is running for this change.
	Active bool
	// Completed is true once a rollout finished without tripping a guard. 🔴 This does NOT make the
	// change delivered (FR60) — it makes it evidence.
	Completed bool
	// Reverted is true once a guard tripped.
	Reverted     bool
	RevertDetail string
}

// ExecutionCondition is a fact about RUNNING a change, not about DELIVERING one (P18 §15.2, FR50).
//
// 🔴 It is a separate field for one reason: `hostAbsent` and `notRuntimeResolvable` both mention the
// runtime and mean opposite things. `hostAbsent` says the strategy is deliverable and its host service
// is simply not running here and now — it refuses rather than substituting, and starting a service
// fixes it. `notRuntimeResolvable` says the change cannot be delivered as data at all, host or no host,
// and nothing fixes it.
//
// Folding one into the other sends an operator to restart something that was never the problem. So an
// execution condition travels beside the routes rather than inside them, and it does NOT alter delivery
// eligibility.
type ExecutionCondition struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// ConditionHostAbsent is the P18 harness-runtime refusal, carried here so a surface can render it
// without borrowing a delivery cause's treatment.
const ConditionHostAbsent = "host-absent"

// Report is the total answer for one concrete change: both routes, a state, and — when the state is not
// terminal — what remains.
type Report struct {
	Change ChangeKind `json:"change"`
	Axis   string     `json:"axis"`
	// Language is the source route's cell key. Empty when the change is not language-scoped.
	Language string         `json:"language,omitempty"`
	Routes   []RouteOutcome `json:"routes"`
	State    State          `json:"state"`
	// RemainingStep is the sentence that keeps a rollout honest: it says permanence still costs a
	// codemod, a pull request and a merge. Empty for terminal states.
	RemainingStep string `json:"remaining_step,omitempty"`
	// IdentityChange marks a change that resolves to the identity configuration.
	IdentityChange bool `json:"identity_change,omitempty"`
	// Conditions are facts about EXECUTING the change. 🚫 They never change the routes' answers.
	Conditions []ExecutionCondition `json:"conditions,omitempty"`
}

// WithCondition attaches an execution condition without touching delivery eligibility.
//
// 🔴 It deliberately cannot reach the routes. A caller that wanted an absent host to make a change
// undeliverable would have to edit the routes themselves, which is exactly the confusion this shape
// prevents: a strategy whose host is down is still perfectly deliverable, and reporting otherwise would
// send an operator to the wrong problem.
func (r Report) WithCondition(c ExecutionCondition) Report {
	r.Conditions = append(append([]ExecutionCondition(nil), r.Conditions...), c)
	return r
}

// Outcome returns the outcome for one route, and whether it was present. A Report is always total over
// both routes, so `false` here is a defect rather than a normal absence.
func (r Report) Outcome(route Route) (RouteOutcome, bool) {
	for _, o := range r.Routes {
		if o.Route == route {
			return o, true
		}
	}
	return RouteOutcome{}, false
}

// Undeliverable reports whether every route refused.
func (r Report) Undeliverable() bool { return r.State == StateUndeliverable }

// Validate asserts the invariants a Report must satisfy before any surface renders it.
//
// This is the machine-enforced half of FR57/FR66: totality over both routes, a closed cause set, and
// the boundary/backlog asymmetry that keeps "we will never do this" from wearing "we have not done this
// yet"'s clothes.
func (r Report) Validate() error {
	for _, route := range Routes() {
		o, ok := r.Outcome(route)
		if !ok {
			return fmt.Errorf("delivery report for %q is not total: route %q has no outcome, and an absent route renders as 'not applicable', which is a claim nobody made", r.Change, route)
		}
		if o.Refused() && o.Cause == "" {
			return fmt.Errorf("delivery report for %q: route %q refuses with no cause — a refusal without a cause is the silence this contract exists to remove", r.Change, route)
		}
		if o.Permanent && o.MissingArtifact != "" {
			return fmt.Errorf("delivery report for %q: route %q is a permanent boundary but names missing artifact %q — a boundary has no artifact to build, and attaching one turns it into a promise", r.Change, route, o.MissingArtifact)
		}
	}
	if r.State == StateDelivered && r.RemainingStep != "" {
		return fmt.Errorf("delivery report for %q is delivered but still names a remaining step", r.Change)
	}
	return nil
}

// BuildReport joins the two routes into one honest answer (FR57/FR58/FR60).
//
// nodeIsBound is the node's ADR-004 apply mode. identityChange marks a change that resolves to the
// identity configuration.
func BuildReport(kind ChangeKind, language string, nodeIsBound bool, src SourceOutcome, rollout RolloutStatus, identityChange bool) (Report, error) {
	axis, known := AxisFor(kind)
	if !known {
		return Report{}, &ErrUnknownChangeKind{Kind: kind}
	}
	elig, err := RuntimeEligibility(kind, nodeIsBound)
	if err != nil {
		return Report{}, err
	}

	out := Report{Change: kind, Axis: axis, Language: language, IdentityChange: identityChange}

	// ── the source route
	srcOut := RouteOutcome{Route: RouteSource, Status: StatusDelivers, Note: src.Note}
	if !src.Materializes {
		srcOut.Status = StatusRefuses
		srcOut.Cause = src.Cause
		srcOut.Permanent = src.Permanent
		srcOut.MissingArtifact = src.MissingArtifact
		// 🔴 Permanence and ownership are two different questions, and collapsing them is the mistake
		// this block exists to avoid. A call-site-shape refusal is BOTH permanent (no materializer would
		// fix it) and the reader's own (they can edit their code). Reporting it as "nobody's move"
		// would tell an author who can act that there is nothing to be done.
		switch {
		case src.CallSiteCannotCarry:
			srcOut.Owner = "you"
			srcOut.MissingArtifact = ""
		case src.Permanent:
			srcOut.Owner = "nobody"
			// A permanent source refusal names no artifact, for the same reason a permanent runtime one
			// does not. Clearing it here rather than trusting the caller keeps Validate's invariant true
			// even when an upstream table is sloppy.
			srcOut.MissingArtifact = ""
		case src.MissingArtifact != "":
			srcOut.Owner = "the platform"
		default:
			srcOut.Owner = "you"
		}
	}

	// ── the runtime route
	rtOut := RouteOutcome{Route: RouteRuntime, Status: StatusDelivers, Note: elig.Note}
	if !elig.Eligible {
		rtOut.Status = StatusRefuses
		rtOut.Cause = string(elig.Cause)
		rtOut.Owner = elig.Cause.Owner()
		rtOut.Permanent = elig.Cause.Permanent()
		rtOut.MissingArtifact = elig.MissingArtifact
	}

	// 🔴 A gate-rejected change is undeliverable by EVERY route (P15 FR54). The runtime route arriving
	// beside a gate whose purpose is to produce nothing is exactly where someone reasons "the rewriter
	// refused, so roll it out instead" — so the gate's answer overrides eligibility rather than sitting
	// beside it.
	if src.GateRejected {
		rtOut = RouteOutcome{Route: RouteRuntime, Status: StatusRefuses, Cause: src.Cause, Owner: "you",
			Note: "The change was rejected by a safety gate, so it has no runnable spec. A rollout is not an alternative route around a gate."}
	}

	// 🔴 …and a call site that cannot carry the change gets the SAME answer from both routes (P14 FR56).
	// This one is easy to get wrong in the reader's favour and wrong in fact: the runtime route's own
	// table would say `noRolloutBinding` here, which is true about the schema and useless to this
	// author — the day the field lands, their call site still has no tool list to select from. So the
	// call-site fact wins, and it wins on both routes.
	if src.CallSiteCannotCarry && !src.GateRejected {
		rtOut = RouteOutcome{Route: RouteRuntime, Status: StatusRefuses, Cause: src.Cause, Owner: "you",
			Permanent: true,
			Note:      "This call site's own source cannot carry the change. A binding document field would not change that — there is nothing at this call site for either route to act on."}
	}

	out.Routes = []RouteOutcome{srcOut, rtOut}
	out.State, out.RemainingStep = deriveState(srcOut, rtOut, src, rollout, identityChange)
	if err := out.Validate(); err != nil {
		return Report{}, err
	}
	return out, nil
}

// deriveState is the state machine, and its whole job is to refuse to say `delivered` too early.
func deriveState(srcOut, rtOut RouteOutcome, src SourceOutcome, rollout RolloutStatus, identityChange bool) (State, string) {
	if identityChange {
		return StateNothingToDeliver, ""
	}
	if src.Merged {
		return StateDelivered, ""
	}
	if rollout.Active {
		// 🔴 FR60. A running rollout is evidence, and the sentence below is the product's honesty about
		// that. It names the remaining cost EVEN WHEN the source route also refuses — because a rollout
		// on an axis with no materializer genuinely cannot become permanent, and saying so is the point.
		if srcOut.Refused() {
			return StateRolloutActive, "This rollout cannot be made permanent: the source route refuses this change, so there is no diff to merge. It will expire to the parent arm."
		}
		return StateRolloutActive, "Permanence still requires a pull request carrying the codemod, merged by a human. A completed rollout is evidence, not delivery."
	}
	if rollout.Reverted {
		if srcOut.Refused() && rtOut.Refused() {
			return StateUndeliverable, ""
		}
		return StateSourcePending, "The rollout reverted to the parent arm. Resuming requires an edited document delivered as a pull request and merged by a human."
	}
	if rollout.Completed {
		// A completed rollout is NOT delivered. This is the single most load-bearing line in the file.
		if srcOut.Refused() {
			return StateUndeliverable, ""
		}
		return StateSourcePending, "The rollout completed without tripping a guard. That is evidence; permanence still requires a merged pull request."
	}
	if srcOut.Refused() && rtOut.Refused() {
		return StateUndeliverable, ""
	}
	if !srcOut.Refused() {
		return StateSourcePending, "A pull request carrying this change is the next step; a human merges it."
	}
	// The source route refuses but the runtime route is eligible: the change can be TRIED, and saying so
	// is the whole reason the second route was added.
	return StateSourcePending, "The source route refuses this change, so it cannot become permanent. It remains eligible for a bounded rollout, which produces evidence rather than delivery."
}
