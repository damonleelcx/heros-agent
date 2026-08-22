package transform

import (
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Harness materialization for the SYNTACTIC (tree-sitter) engines (P18 §5, extended by §11).
//
// The dispatch order below is the one P16 had to fix once already and is worth restating, because
// getting it wrong produces refusals that are true and useless. The questions are asked
// SPECIFIC-FIRST:
//
//	1. the STRATEGY's own requirements  — a `react-loop` needs a tool executor in EVERY language, so
//	                                      telling its author "your language's rewriter is pending" would
//	                                      send them to wait for something that would refuse them too.
//	2. the LANGUAGE                     — does an emitted harness module and a rewriter exist here.
//	3. the CALL SITE                    — can this particular call be re-invoked and its answer read.
//
// Only the second is our backlog, and only the second may carry CauseNoMaterializer.

// spanMaterializeHarness is the span engines' entry for the harness dimension.
func spanMaterializeHarness(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimHarness)

	if o.Harness == nil || o.Harness.IsSingleShot() {
		return nil, nil // the identity; see materializeHarness
	}
	strategy := o.Harness.Spec.Strategy

	// 🔴 P34: an EXECUTION ENVELOPE reaches no rewriter, in any language. It is not a loop, so there is
	// nothing to wrap; its ceilings and host-service provision are checked at resolve and by the sandbox.
	// The no-op rather than a refusal, for the reason materializeHarness gives.
	if strategy == registry.StrategyEnvelope {
		return nil, nil
	}

	if svc := harnessHostService(strategy); svc != "" {
		return nil, refuseHostService(site.NodeID, dim, strategy, svc)
	}
	// The LANGUAGE. Asked before the call site, because without an emitted module there is nothing for a
	// rewritten call to drive — so no fact about the user's code could change the outcome, and telling
	// them about their call shape would point them at something they cannot fix.
	if !HasHarnessMaterializer(site.Language) {
		return nil, refuseHarness(site.NodeID, site.Language, o)
	}
	// 4. the CALL SITE. Both halves — drive and decide — resolved before any edit is emitted.
	return spanHarnessLoop(site, src, o)
}

// harnessHostService names the host service a strategy needs and a CALL SITE cannot supply, or "" when
// the strategy needs none (decisions.md D-10).
//
// 🔴 It is language-INDEPENDENT and permanent, which is why it is asked before the language question. The
// generated module makes no provider call and dispatches no tool — a generated file that reached a
// provider would put a credential in the customer's process and spend it on turns the author never wrote
// — so three of the five strategies can never be materialized AT A CALL SITE in any language. Telling
// their author to wait for a rewriter would be sending them somewhere that will refuse them too.
//
// It is keyed by strategy NAME rather than declared as a method on registry.HarnessStrategy, for the
// reason the memory runtime's dispatch is: putting it on the sealed type would drag a transform-side
// concern into every consumer of a vocabulary. A conformance test asserts the two name the same set.
func harnessHostService(strategy string) string {
	switch strategy {
	case "react-loop":
		return "a tool executor — the loop continues by RUNNING the tool the model asked for, and the " +
			"generated module has no way to dispatch your tools"
	case "plan-execute":
		return "a planner and a step executor — the loop's first turn produces a plan and the rest EXECUTE " +
			"its steps, neither of which the generated module can perform"
	case "critic-loop":
		return "a separate critic model — the loop continues by CALLING another model to judge the answer, " +
			"and a generated file may not reach a provider"
	}
	return ""
}

// refuseHostService is the refusal for a strategy whose host service a call site cannot supply.
//
// 🔴 CauseNotAtCallSite, and it carries NO missing artifact. That asymmetry is the point (P13 FR45): there
// is nothing for the platform to build here, so naming an artifact would promise work that would not help.
// The reason is the same in every language, before and after every rewriter lands.
func refuseHostService(nodeID, dim, strategy, service string) error {
	return refuseNotAtCallSite(nodeID, dim,
		"harness strategy %q needs %s. A call site offers no place to inject one, and the generated module "+
			"makes no provider call and dispatches no tool by design — a generated file that reached a "+
			"provider would put your credential in your own process and spend it on turns you did not "+
			"write. 🚫 It is REFUSED rather than degraded to a strategy that needs no such service: running "+
			"a lighter loop under %q's config_hash would report one scaffold as another",
		strategy, service, strategy)
}
