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

// PassReader answers "what did the last generation pass find for this workflow?".
//
// ok=false means NO PASS HAS EVER RUN, which is a different answer from "a pass ran and found nothing"
// and is the whole reason this read exists. The error is a third answer again.
type PassReader interface {
	LastPass(ctx context.Context, tenantID, workflowID string) (proposalstore.Pass, bool, error)
}

// Source implements api.ProposalsSource over the durable store.
type Source struct {
	store  ProposalReader
	diffs  DiffReader
	passes PassReader
}

// NewSource returns the hosted recommendation surface. diffs and passes may be nil.
//
// 🔴 What a nil `passes` costs, stated rather than defaulted away: with no pass reader, a workflow with
// no proposals reports `never_analysed`, because that is the honest reading of what this surface can
// see. It does NOT report `empty` — `empty` asserts that a pass ran, and a deployment that cannot read
// passes has no grounds for that assertion. The safe direction is the one that tells a reader to press
// the button, not the one that tells them they are finished.
func NewSource(store ProposalReader, diffs DiffReader, passes PassReader) *Source {
	return &Source{store: store, diffs: diffs, passes: passes}
}

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
			State:      api.SurfaceError,
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
		State:           api.SurfaceReady,
		Recommendations: []api.Card{},
		Withheld:        []api.Card{},
	}
	out.Pass = s.lastPass(tenantID, workflowID)

	for _, sc := range scored {
		card := s.cardFor(sc)
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

	switch {
	case len(scored) == 0 && out.Pass == nil:
		// 🔴 The state that did not exist. No proposals AND no recorded pass means nobody has ever asked
		// this platform to look — the opposite of `empty`, which asserts that a pass ran and found
		// nothing. Both used to render "Nothing is pending."
		out.State = api.SurfaceNeverAnalysed
	case len(scored) == 0:
		// A pass ran and recorded no proposals. `empty` now means exactly that, and the generator's own
		// sentence on out.Pass says WHICH of the eight ways it came to be empty.
		out.State = api.SurfaceEmpty
	case len(out.Recommendations) == 0 && anyAwaitingVerdict(scored):
		// `verifying` rather than `empty`: proposals exist and are waiting on a measurement the customer's
		// CI performs. Reporting that as empty would tell a customer the product found nothing on a day it
		// found several things and is waiting for them.
		out.State = api.SurfaceVerifying
	}
	out.Trend = trendFor(scored)
	return out, true
}

// lastPass reads the recorded pass, or nil when there is none to read.
//
// A read FAILURE also returns nil, and the consequence is deliberate: the surface reports
// `never_analysed` rather than `empty`. Both are wrong when the store is down, and only one of them
// tells the reader to do something that will make the truth visible.
func (s *Source) lastPass(tenantID, workflowID string) *api.PassView {
	if s.passes == nil {
		return nil
	}
	p, ok, err := s.passes.LastPass(context.Background(), tenantID, workflowID)
	if err != nil || !ok {
		return nil
	}
	return &api.PassView{State: p.State, Detail: p.Detail, Proposals: p.Proposals, RanAtMS: p.RanAtMS}
}

// cardFor renders one stored proposal.
//
// It routes through api.CardFor, which is the ONE place a build status decides which card a candidate
// becomes — including the fail-closed default that treats an unrecognised status as refused rather than
// as built. Every proposal this platform generates is `unbuilt`, which lands in that default.
func (s *Source) cardFor(sc proposalstore.Scored) api.Card {
	pres := proposal.Presentation{
		Operator:   proposal.OperatorKind(sc.Operator),
		NodeID:     sc.NodeID,
		Pattern:    sc.Pattern,
		ConfigHash: sc.CandidateConfigHash,
		Rationale:  sc.Rationale,
		DiagID:     sc.DiagnosisID,
		// The compiled diff, when one exists. A proposal that has not been compiled has none, and the
		// field stays empty rather than being reconstructed — a diff assembled for display is a diff
		// nobody generated. No SpecDiff: the platform does not store the per-dimension summary.
		SourceDiff:      s.diffFor(sc),
		EvidenceCaseIDs: caseIDs(sc.Evidence),
	}

	// 🔴 A REFUSAL is marked by its reason, not by build_status: 0012's column has no `refused` value,
	// so a refused proposal and one nobody has compiled both read `unbuilt`. Reading the reason is what
	// tells them apart — and getting it wrong renders "the transform declined this change" over a
	// proposal whose diff was generated, or hides a named refusal behind "not compiled yet".
	status := proposal.BuildStatus(sc.BuildStatus)
	if sc.RefusalReason != "" {
		status = proposal.BuildRefused
		pres.Refusal = &proposal.ChangeRefusal{
			NodeID: sc.NodeID, Dimension: sc.RefusalDimension, Reason: sc.RefusalReason,
		}
	}

	v := verification.Verdict{}
	if sc.Verdict != nil {
		v = *sc.Verdict
	}
	card := api.CardFor(pres, status, "", v, verification.Advisory)

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
	// The PR gate is decided in api.CardFor — the one place a build status becomes a card — including
	// the two distinct unbuilt reasons (not compiled yet / compiled but not built). Computing a reason
	// here as well would be a second answer to the question that function just answered. This only
	// catches a card that arrived without one, which would otherwise disable the action in silence.
	if card.PRDisabledReason == "" {
		card.CanOpenPR = false
		card.PRDisabledReason = notCompiledReason
	}
	return card
}

// diffFor fetches the compiled diff, or returns "" when there is none to fetch.
//
// A read FAILURE also returns "": the card then renders as though the proposal is not compiled, which
// understates what exists. That is the safe direction — the alternative is rendering a partial or
// error string where a reviewer expects a diff — and the surface never claims a diff it did not read.
func (s *Source) diffFor(sc proposalstore.Scored) string {
	if s.diffs == nil || sc.SourceDiffBlobHash == "" {
		return ""
	}
	b, err := s.diffs.Get(context.Background(), sc.SourceDiffBlobHash)
	if err != nil {
		return ""
	}
	return string(b)
}

// The two reasons a pull request is unavailable, stated once each so the card, the PR gate and OpenPR
// cannot describe the same limit three different ways.
const (
	notCompiledReason = "This proposal has not been compiled yet, so there is no diff to open a pull " +
		"request with. Compile it to generate the reviewable diff."
	notBuiltReason = "This proposal has a reviewable diff, and it has not been proved to BUILD: this " +
		"deployment carries no toolchain, so the diff was parsed rather than compiled. Delivery requires " +
		"a build, so the change is reviewable here and not deliverable from here."
)

// OpenPR refuses, by name.
//
// 🔴 It refuses for EVERY proposal, including a gate-passing one with a compiled diff, and that is the
// honest answer rather than a missing feature. OpenPR's contract is to open a reviewable pull request
// carrying the proposal's diff — and ADR-001's rule is that nothing reaches a repository except as a
// diff that BUILDS. This deployment can produce the diff and cannot establish that it builds. The
// alternatives are worse in the direction that matters: opening a pull request whose contents nobody
// compiled, or returning a success the console renders as "opened" with nothing behind it.
func (s *Source) OpenPR(tenantID, workflowID, proposalID string) (api.PRResult, error) {
	return api.PRResult{}, fmt.Errorf("%s Proposal %s can be reviewed here and verified by your CI; "+
		"opening the pull request is the CI-mediated delivery step", notBuiltReason, proposalID)
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
