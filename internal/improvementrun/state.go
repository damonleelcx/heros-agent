package improvementrun

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/proposalgen"
)

// state.go is FR7, design D5, and P30's inherited defect: **"there is nothing to propose" is not one
// sentence.**
//
// # What P30 found, and why it is a requirement rather than a polish item
//
// `proposalgen` returns a closed `State` with a distinct answer for each way a pass can find nothing,
// and the surface discarded it. The consequence is not cosmetic: a customer with no linked runs and a
// customer whose repository is genuinely healthy saw the SAME empty screen and took the SAME (wrong)
// action. One of them needed to run `heros link`; the other needed to be told they were done.
//
// # ⚠️ The count in the requirement is wrong, and this is the record of it
//
// PRD §2.2, PRD §6.2 FR7 and `tasks.md` 3.5/7.12 all say **five** states. `proposalgen` has **eight** —
// `generated` plus **seven** ways to find nothing. The two the prose does not know about are
// `no_model_menu` and `revision_mismatch`, both of which arrived after that sentence was written.
//
// 🔴 This implements **all seven**, not five, and the reason is that implementing five is the P30 defect
// with a smaller blast radius: two states would fall through to a default, and a default is exactly the
// "generic empty result" the requirement forbids. `TestEveryNothingToProposeStateHasItsOwnSentence`
// reads `proposalgen.States()` rather than a number, so the eighth, ninth and tenth are caught the day
// they are added rather than the day a customer meets one.
//
// The discrepancy is recorded in `openspec/changes/p35-autonomous-improvement-run/decisions.md` and in
// the PRD, so nobody reconciles it in the wrong direction by deleting two sentences to match a count.

// EmptyState is one "nothing to propose" answer, ready to render.
type EmptyState struct {
	// State is `proposalgen`'s own value, carried by NAME. 🚫 Never flattened to a boolean.
	State proposalgen.State `json:"state"`
	// Headline is what the surface leads with. One per state, and no two are the same — the fence
	// asserts it, because two states rendering one sentence is one state with two names.
	Headline string `json:"headline"`
	// Detail is `proposalgen`'s own sentence, carried VERBATIM from the pass. 🔴 Not re-worded here:
	// the pass knows things this table does not — which two revisions disagreed, which half of the model
	// join was missing — and a re-wording would drop them.
	Detail string `json:"detail"`
	// NextAction is what to do. Empty ONLY for the two states where there is genuinely nothing to do,
	// and that emptiness is meaningful rather than a gap — see `nextActions`.
	NextAction string `json:"next_action,omitempty"`
	// Healthy says this is a good answer rather than a missing input. 🔴 `no_bottleneck` and
	// `no_admissible_candidate` are SUCCESSES: the platform looked and there was nothing to improve.
	// Rendering them in the same tone as "you have linked no runs" tells a customer their setup is
	// broken when it is finished.
	Healthy bool `json:"healthy"`
}

// headlines is one sentence per state. Sentences, not labels: a label ("no_linked_runs") is the state's
// name and the surface already has that.
var headlines = map[proposalgen.State]string{
	proposalgen.StateGenerated:        "Candidates were generated.",
	proposalgen.StateNoRuns:           "No runs have been linked for this workflow yet.",
	proposalgen.StateNoPerNode:        "The linked runs carry no per-node metrics.",
	proposalgen.StateNoGraph:          "No source snapshot has been pushed for this workflow.",
	proposalgen.StateNoMenu:           "This deployment publishes no model tiers.",
	proposalgen.StateNoBottleneck:     "Nothing on this workflow dominates its cost or latency.",
	proposalgen.StateNoCandidates:     "A bottleneck was found and every operator declined it.",
	proposalgen.StateRevisionMismatch: "The linked run and the discovered graph are at different revisions.",
}

// nextActions is what to do about each state.
//
// 🔴 Two are deliberately EMPTY, and the emptiness is the answer rather than a hole:
//
//	no_bottleneck            There is nothing to do. The workflow is not dominated by any node, so
//	                         there is no cost to remove. Inventing a step here would send somebody to
//	                         change something that is working.
//	no_admissible_candidate  Also nothing — most often the node already runs the cheapest published
//	                         model. The refusals are attached and are the honest detail.
//
// This is the same shape `forgedelivery.Withheld.NextAction` uses, and it is stated again because the
// tempting fix is to fill every row.
var nextActions = map[proposalgen.State]string{
	proposalgen.StateGenerated:        "",
	proposalgen.StateNoRuns:           "Run an eval and link it with `heros link`, then ask again.",
	proposalgen.StateNoPerNode:        "Re-run an eval on a current CLI; per-node metrics are recorded by the eval, and a run linked by an older CLI has none.",
	proposalgen.StateNoGraph:          "Push a source snapshot with `heros push-source`, or connect the repository.",
	proposalgen.StateNoMenu:           "Ask your operator to publish model tiers; without them `cheaper` is not expressible on this deployment.",
	proposalgen.StateNoBottleneck:     "",
	proposalgen.StateNoCandidates:     "",
	proposalgen.StateRevisionMismatch: "Push source at the revision your linked run measured, or link a run measured at the revision we hold.",
}

// healthyStates are the answers that mean the platform looked and found nothing WRONG.
var healthyStates = map[proposalgen.State]bool{
	proposalgen.StateNoBottleneck: true,
	proposalgen.StateNoCandidates: true,
}

// EmptyStateFor renders one `proposalgen.Result` as the surface receives it.
//
// 🔴 It takes the RESULT, not the state, so the pass's own `Detail` travels. A function taking a bare
// state would force the surface to fetch the detail separately or drop it, and the version that drops
// it is the one that renders "The linked run and the discovered graph are at different revisions."
// without naming which two.
func EmptyStateFor(res proposalgen.Result) (EmptyState, error) {
	if !res.State.Valid() {
		return EmptyState{}, fmt.Errorf("improvementrun: %q is not a generation state; a pass that "+
			"reported no state is a pass whose reason was discarded", res.State)
	}
	headline, ok := headlines[res.State]
	if !ok {
		// Unreachable while the fence holds, and returned as an error rather than defaulted so that if
		// the fence is ever removed the failure is loud instead of a blank card.
		return EmptyState{}, fmt.Errorf("improvementrun: the generation state %q has no sentence; a "+
			"state with no sentence renders as nothing, which is indistinguishable from a surface that "+
			"was never asked", res.State)
	}
	return EmptyState{
		State:      res.State,
		Headline:   headline,
		Detail:     res.Detail,
		NextAction: nextActions[res.State],
		Healthy:    healthyStates[res.State],
	}, nil
}

// EmptyStates renders every "nothing to propose" state, for the fence and for a documentation page
// that has to show a customer what the seven answers are.
func EmptyStates() []EmptyState {
	var out []EmptyState
	for _, s := range proposalgen.States() {
		if !s.FoundNothing() {
			continue
		}
		out = append(out, EmptyState{
			State:      s,
			Headline:   headlines[s],
			NextAction: nextActions[s],
			Healthy:    healthyStates[s],
		})
	}
	return out
}
