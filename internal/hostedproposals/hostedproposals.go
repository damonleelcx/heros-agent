// Package hostedproposals renders the P5.5 recommendation surface from the platform's durable
// proposals and the verdicts its customers' CI reported.
//
// # What this deployment can and cannot show
//
// It shows every proposal the platform generated, sorted into the two lists the surface is built
// around: gate-passing recommendations, and withheld ones with the reason. It shows the measured delta
// with its interval, the cost and latency impact, and how many cases moved.
//
// It does NOT show a source diff, and OpenPR refuses. Both follow from the same fact rather than from
// an omission: this platform generates a candidate Variant Spec and never compiles it. The codemod
// needs the customer's source at a revision AND a build check, and the row says `unbuilt` because that
// is true. A surface that rendered an empty diff — or an "Open PR" button that produced a pull request
// with no changes in it — would be the "looks complete" failure decisions.md D-14.3 is written against,
// arriving through the surface instead of through the codemod.
//
// # Why an unverified proposal is WITHHELD rather than hidden
//
// Most proposals on a hosted deployment have no verdict at any given moment: the platform proposes, and
// the customer's CI measures on its own schedule. Hiding those would make a workflow with six pending
// proposals look identical to one with none — and the console's whole job here is to say what to do
// next, which for an unmeasured proposal is "run your verification job".
package hostedproposals

import (
	"context"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/verification"
)

// ProposalReader is the read this package needs.
type ProposalReader interface {
	ForWorkflow(ctx context.Context, tenantID, workflowID string) ([]proposalstore.Scored, error)
}

// Source implements api.ProposalsSource over the durable store.
type Source struct{ store ProposalReader }

// NewSource returns the hosted recommendation surface.
func NewSource(store ProposalReader) *Source { return &Source{store: store} }

// Surface returns one tenant's recommendation surface for a workflow.
//
// 🔴 The bool is "this workflow resolves", not "this workflow has proposals". A workflow with no
// proposals returns a surface in the `empty` state — which the console renders as a sentence — because
// returning false would make the endpoint answer 404, and "no such workflow" and "nothing proposed
// yet" send a reader to two completely different places.
func (s *Source) Surface(tenantID, workflowID string) (api.Surface, bool) {
	if s == nil || s.store == nil {
		return api.Surface{}, false
	}
	scored, err := s.store.ForWorkflow(context.Background(), tenantID, workflowID)
	if err != nil {
		// A read failure is its own STATE, not an empty surface. `error` is one of §6.5's four states
		// precisely so a database outage cannot render as "we looked and found nothing".
		return api.Surface{
			WorkflowID: workflowID,
			State:      "error",
			Error:      "The recommendation surface could not be read. This is not a statement about your workflow.",
			// Both lists are non-nil so the console renders two empty sections rather than crashing on a
			// null — and so an error surface is visibly an error rather than a quiet zero.
			Recommendations: []api.Card{},
			Withheld:        []api.Card{},
		}, true
	}

	out := api.Surface{
		WorkflowID: workflowID,
		// ADVISORY, and it is not a placeholder. Assisted is the level at which the platform opens a pull
		// request on the customer's behalf, and this deployment cannot: it compiles no diff. Declaring
		// `assisted` would light the Open-PR affordance on every card and every click would fail.
		AutomationLevel: string(verification.Advisory),
		State:           "ready",
		Recommendations: []api.Card{},
		Withheld:        []api.Card{},
	}

	for _, sc := range scored {
		card := cardFor(sc)
		if sc.Verdict != nil && sc.Verdict.Passed() && sc.BuildStatus == proposalstore.BuildBuilt {
			out.Recommendations = append(out.Recommendations, card)
			continue
		}
		out.Withheld = append(out.Withheld, card)
	}

	// Ranked by verified delta, best first — the order §6.2 specifies. A withheld card has no delta to
	// rank on and keeps the store's newest-first order.
	sort.SliceStable(out.Recommendations, func(i, j int) bool {
		return out.Recommendations[i].Delta > out.Recommendations[j].Delta
	})

	if len(scored) == 0 {
		out.State = "empty"
	} else if len(out.Recommendations) == 0 && anyAwaitingVerdict(scored) {
		// `verifying` rather than `empty`: proposals exist and are waiting on a measurement the customer's
		// CI performs. Reporting that as empty would tell a customer the product found nothing on a day it
		// found several things and is waiting for them.
		out.State = "verifying"
	}
	out.Trend = trendFor(scored)
	return out, true
}

// cardFor renders one stored proposal.
//
// It routes through api.CardFor, which is the ONE place a build status decides which card a candidate
// becomes — including the fail-closed default that treats an unrecognised status as refused rather than
// as built. Every proposal this platform generates is `unbuilt`, which lands in that default.
func cardFor(sc proposalstore.Scored) api.Card {
	pres := proposal.Presentation{
		Operator:   proposal.OperatorKind(sc.Operator),
		NodeID:     sc.NodeID,
		Pattern:    sc.Pattern,
		ConfigHash: sc.CandidateConfigHash,
		Rationale:  sc.Rationale,
		DiagID:     sc.DiagnosisID,
		// No SourceDiff and no SpecDiff: this platform compiled neither. Left empty rather than
		// reconstructed — a diff assembled for display is a diff nobody built or verified.
		EvidenceCaseIDs: caseIDs(sc.Evidence),
	}

	v := verification.Verdict{}
	if sc.Verdict != nil {
		v = *sc.Verdict
	}
	card := api.CardFor(pres, proposal.BuildStatus(sc.BuildStatus), "", v, verification.Advisory)

	// 🔴 The proposal id is the STORE's, not the config hash. api.BuildCard takes it from the verdict
	// (which carries one) and the refused/build-failed cards fall back to the config hash — neither is
	// the id this platform issued, and the id is what `heros report-verdict --proposal` and the
	// open-PR route are addressed by. A card whose id does not resolve is a button that 404s.
	card.ProposalID = sc.ProposalID

	if sc.Verdict == nil {
		// An unmeasured proposal is NOT "gate failed". CardFor has no state for "nobody has measured this
		// yet", so it is set here rather than left reading as a rejection.
		card.State = "unverified"
		card.GateResult = string(verification.GateUnrun)
		card.Narration = "Awaiting verification. This change has been proposed and not yet measured — " +
			"the gate runs your eval harness, on your machine, so the verdict arrives when your " +
			"verification job reports it."
	}
	if card.PRDisabledReason == "" {
		card.CanOpenPR = false
		card.PRDisabledReason = noDiffReason
	}
	return card
}

// noDiffReason is the one sentence this deployment repeats wherever a diff would be. Stated once so the
// card, the PR gate and OpenPR cannot describe the same limit three different ways.
const noDiffReason = "This deployment generates the change but does not compile it: the codemod needs " +
	"your source at a revision and a build check, so there is no reviewable diff to open a pull request with."

// OpenPR refuses, by name.
//
// 🔴 It refuses for EVERY proposal, including a gate-passing one, and that is the honest answer rather
// than a missing feature: OpenPR's contract is to open a reviewable pull request carrying the
// proposal's diff, and this platform holds no diff. The alternatives are worse in the direction that
// matters — opening an empty pull request in a customer's repository, or returning a success the
// console renders as "opened" with nothing behind it.
func (s *Source) OpenPR(tenantID, workflowID, proposalID string) (api.PRResult, error) {
	return api.PRResult{}, fmt.Errorf("%s Proposal %s is recorded and can be verified by your CI; "+
		"delivery of a compiled diff is not available on this deployment", noDiffReason, proposalID)
}

func caseIDs(ev []proposalstore.Evidence) []string {
	if len(ev) == 0 {
		return nil
	}
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		out = append(out, e.CaseID)
	}
	return out
}

func anyAwaitingVerdict(scored []proposalstore.Scored) bool {
	for _, sc := range scored {
		if sc.Verdict == nil {
			return true
		}
	}
	return false
}

// trendFor builds the across-iterations view from the verdicts that exist.
//
// 🔴 It uses only MEASURED proposals, and reports honestly when there are too few. verification.BuildTrend
// already says "Not enough iterations to establish a trend" below two points — this feeds it only real
// measurements rather than padding the series with unverified proposals at delta zero, which would draw
// a flat line through changes nobody measured and read as "your workflow is not improving".
func trendFor(scored []proposalstore.Scored) verification.TrendView {
	var points []verification.TrendPoint
	// Oldest first: a trend is read left to right, and the store returns newest first.
	for i := len(scored) - 1; i >= 0; i-- {
		sc := scored[i]
		if sc.Verdict == nil {
			continue
		}
		points = append(points, verification.TrendPoint{
			VariantID:      sc.CandidateConfigHash,
			Iteration:      len(points) + 1,
			OverallSuccess: sc.Verdict.Delta.Mean,
		})
	}
	return verification.BuildTrend(points)
}
