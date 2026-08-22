package variantspec

import (
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/registry"
)

// THE ENVELOPE GATES — the ceiling and the host services, checked at RESOLVE (P34 tasks 4.2, 4.3)
// ───────────────────────────────────────────────────────────────────────────────────────────────
//
// Both checks existed before P34; neither ran here. `boundedCeiling` checked the turn count when a RUN
// reached the node, and `Hosts.require` refused a missing tool executor at the same moment. Moving them
// left is FR6/FR7, and the reason is not tidiness: at run time the codemod has already been generated
// and applied, so "this configuration was never runnable" arrives after the change is in the tree.
//
// 🔴 Neither check REPLACES its run-time counterpart. internal/harnessruntime keeps refusing, because a
// host can be assembled by a path that never resolved a spec, and a gate that only exists in front of
// one entrance is not a gate. This is a second one, earlier.

// checkEnvelopeAdmits refuses a loop the node's envelope will not admit.
//
// `env` is nil when the node bound no harness at all, and that is not treated as a failure: the platform
// ceiling (registry.MaxTurnsCeiling) is enforced by every loop strategy's own schema at seal, so no loop
// entry that exists can exceed it, and a node with no envelope is bounded by that alone. Refusing every
// loop that had no envelope would make the loop axis unusable without also authoring a harness entry,
// which is a coupling the split exists to remove.
func checkEnvelopeAdmits(nodeID string, loop *registry.LoopEntry, env *registry.Envelope) error {
	if loop == nil {
		return nil
	}
	if err := checkTurnCeiling(nodeID, loop, env); err != nil {
		return err
	}
	return checkHostServices(nodeID, loop, env)
}

// checkTurnCeiling is FR6 / task 4.2: a `max_turns` above the envelope's ceiling is refused, naming
// BOTH values.
//
// 🔴 Naming both is the requirement, not a nicety. "Too many turns" leaves an author unable to tell
// whether to lower their value or to ask for a higher policy — and those are requests to two different
// people. The ceiling is imposed by whoever owns the envelope; the value is chosen by whoever authors
// the loop, and the whole point of the split is that those are different acts under different review.
func checkTurnCeiling(nodeID string, loop *registry.LoopEntry, env *registry.Envelope) error {
	if env == nil || env.TurnCeiling == nil {
		return nil
	}
	// 🔴 The bool, not a fabricated 1. A loop that CHOSE no turn count (`single-shot`, whose schema
	// forbids the key) has nothing to compare, and comparing a made-up value would make this check pass
	// on a shape it never examined.
	chosen, didChoose := loop.MaxTurns()
	if !didChoose {
		return nil
	}
	ceiling := *env.TurnCeiling
	if chosen <= ceiling {
		return nil
	}
	return &SpecError{NodeID: nodeID, Dim: DimLoop, Ref: loop.VersionID, Err: ErrCeilingExceeded,
		Detail: fmt.Sprintf("the loop asks for max_turns=%d and the envelope's turn_ceiling is %d. "+
			"The ceiling is a policy about how much autonomous work one node may do — it is imposed, not "+
			"chosen — so honouring the larger value would make this a second and looser gate. Either lower "+
			"max_turns to %d or less, or ask whoever owns the envelope to raise turn_ceiling.",
			chosen, ceiling, ceiling)}
}

// checkHostServices is FR7 / task 4.3: a loop needing a second actor the envelope does not provide is
// refused at resolve, naming the loop AND the missing service.
//
// 🔴 A node with NO envelope is refused too, and that asymmetry with the ceiling check is deliberate.
// A missing ceiling still leaves the platform ceiling standing, so absence is safe. A missing host
// service has no fallback at all: `react-loop` with nothing to run its tools is not a slower react-loop,
// it is a different strategy — and running it would report one scaffold under another's config_hash,
// which is the drive-and-decide failure the runtime already refuses at execution.
//
// 🚫 It is never degraded to a strategy that needs no service. That substitution is the single most
// tempting thing in this file and the single most damaging: `critic-loop` without a critic IS
// `reflexion`, and running it under critic-loop's hash would report one strategy as another.
func checkHostServices(nodeID string, loop *registry.LoopEntry, env *registry.Envelope) error {
	need := registry.HostServicesForLoop(loop.Spec.Strategy)
	if len(need) == 0 {
		return nil
	}
	var missing []string
	if env == nil {
		missing = need
	} else {
		missing = env.MissingServices(need...)
	}
	if len(missing) == 0 {
		return nil
	}
	where := "this node binds no execution envelope, so nothing provides it"
	if env != nil {
		provided := "nothing"
		if len(env.HostServices) > 0 {
			provided = registry.HostServiceDisplay(env.HostServices)
		}
		where = fmt.Sprintf("the envelope provides %s", provided)
	}
	return &SpecError{NodeID: nodeID, Dim: DimLoop, Ref: loop.VersionID, Err: ErrMissingHostService,
		Detail: fmt.Sprintf("the %q loop needs %s and %s. 🚫 It is NOT degraded to a strategy that needs "+
			"no second actor — that would report one strategy's result under another's config_hash. Add %s "+
			"to the envelope's host_services, or choose a loop that needs none.",
			loop.Spec.Strategy, registry.HostServiceDisplay(need), where, strings.Join(missing, " and "))}
}

// checkConcurrencyLimit is FR5 / task 4.4's resolve-time half: a concurrent group wider than the
// envelope's limit is refused here.
//
// 🔴 It is HALF of the answer, and saying so is the point. The sandbox enforces the same limit at
// execution, independently of what the spec declared (task 9.12 asserts both). This one is the early,
// legible refusal; that one is what holds when this one is bypassed — by a Resolved assembled by hand,
// by a spec resolved under an older binary, by any path that reaches the executor without coming
// through here. A single check in either place alone would be a limit with a way around it.
func checkConcurrencyLimit(groupIndex, width int, env *registry.Envelope) error {
	if env == nil || env.ConcurrencyLimit == nil {
		return nil
	}
	limit := *env.ConcurrencyLimit
	if width <= limit {
		return nil
	}
	return specErr("", DimHarness, ErrCeilingExceeded,
		"graph_groups[%d] declares %d concurrent members and the envelope's concurrency_limit is %d. "+
			"Concurrency multiplies a run's PEAK resource use by the group's width, which is why the limit "+
			"is a policy rather than a hint — the sandbox enforces the same number at execution regardless "+
			"of what this spec declares", groupIndex, width, limit)
}
