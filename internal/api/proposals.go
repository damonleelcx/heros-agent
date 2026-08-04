package api

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/verification"
)

// p55.go mounts the P5.5 ranked-recommendation + verification surface, following the p45.go / p5.go
// pattern: one Mount method, a nil-able source interface, 503 when unmounted, the demo page embedded.
//
// The surface's load-bearing rule is structural at the HTTP boundary: the recommendation list carries
// ONLY gate-passing verdicts (nothing-unverified, §4.5), and the one-click open-PR action is a POST
// that the handler refuses unless the proposal's verdict passed AND the workflow is Assisted (§6.4) —
// so "unverified never ships" cannot be bypassed by calling the endpoint directly.

//go:embed static/p55.html
var p55HTML []byte

// ProposalsSource is what the API asks of the engine: the assembled recommendation surface for a workflow,
// and the Assisted apply action (open a PR carrying a verified proposal's diff). OpenPR NEVER merges
// and never mutates the working tree — it opens a reviewable PR for the human (design Decision 9).
type ProposalsSource interface {
	// Surface returns one TENANT's recommendation surface for a workflow.
	//
	// 🔴 tenantID is the FIFTH interface in this package to need it, after PatternSource,
	// GraphEditorSource, BoardSource and ScorecardSource. Every one was written against a demo stub that
	// returned a single fixture to every caller, so the missing scope was invisible until something
	// durable sat behind it. Workflow ids are chosen by customers and collide; the caller reads one
	// straight out of a URL.
	//
	// It matters more here than on a read-only board: OpenPR acts on what Surface returned. An
	// unscoped Surface would let one tenant enumerate another's proposals AND open a pull request
	// carrying their diff into a repository — a write, into someone else's code.
	Surface(tenantID, workflowID string) (Surface, bool)
	OpenPR(tenantID, workflowID, proposalID string) (PRResult, error)
}

// Surface is the whole P5.5 read model for one workflow.
type Surface struct {
	WorkflowID      string `json:"workflow_id"`
	AutomationLevel string `json:"automation_level"` // "advisory" | "assisted"
	// State is the first-class surface state (§6.5): "ready" | "empty" | "verifying" | "error".
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Recommendations are gate-passing proposals, in ranked (verified-delta) order.
	Recommendations []Card `json:"recommendations"`
	// Withheld are gate-failed / build-failed proposals, clearly separated (§6.2) — never mixed in.
	Withheld []Card `json:"withheld"`
	// Trend is the across-variants-over-time view (§6.3).
	Trend verification.TrendView `json:"trend"`
}

// Card is one proposal rendered for the surface: the diagnosis + failing-case evidence + reviewable
// source diff (source + Variant-Spec diff) + the verified verdict (delta ± CI, cost/latency, cases
// fixed/broken), plus the derived UI state and the Assisted PR-open gate.
type Card struct {
	ProposalID      string               `json:"proposal_id"`
	Operator        string               `json:"operator"`
	NodeID          string               `json:"node_id"`
	Pattern         string               `json:"pattern"`
	DiagID          string               `json:"diag_id"`
	EvidenceCaseIDs []string             `json:"evidence_case_ids"`
	Rationale       string               `json:"rationale"`
	SourceDiff      string               `json:"source_diff"`
	SpecDiff        []proposal.DimChange `json:"spec_diff"`
	BuildStatus     string               `json:"build_status"`
	// verdict (present for verified / gate-failed; zero for build-failed).
	State            string   `json:"state"`
	GateResult       string   `json:"gate_result"`
	HeldOut          bool     `json:"held_out"`
	HeldOutLabel     string   `json:"held_out_label"`
	Delta            float64  `json:"delta"`
	CILow            float64  `json:"ci_low"`
	CIHigh           float64  `json:"ci_high"`
	Significant      bool     `json:"significant"`
	CostDelta        float64  `json:"cost_delta"`
	LatencyDelta     float64  `json:"latency_delta"`
	CasesFixed       []string `json:"cases_fixed"`
	CasesBroken      []string `json:"cases_broken"`
	Narration        string   `json:"narration"`
	CanOpenPR        bool     `json:"can_open_pr"`
	PRDisabledReason string   `json:"pr_disabled_reason,omitempty"`
	// RefusedNodeID / RefusedDimension / RefusedReason name a change the TRANSFORM declined to make
	// (P14 task 8.2). They are three flat fields rather than a nested object because the console's
	// generated view contract is flat, and — more to the point — because a refusal must be impossible to
	// miss: "node X, dimension skills: <reason>" is the whole of what a user needs, and it renders
	// without the reader having to open anything.
	//
	// 🔴 Empty on every card that was not refused. A refusal that merely LOOKED like a missing field
	// would read as a change that happened, which is precisely the outcome decisions.md D-14.3 forbids.
	RefusedNodeID    string `json:"refused_node_id,omitempty"`
	RefusedDimension string `json:"refused_dimension,omitempty"`
	RefusedReason    string `json:"refused_reason,omitempty"`
}

// PRResult is what OpenPR returns: a reviewable PR the human merges (never auto-merged).
type PRResult struct {
	ProposalID string `json:"proposal_id"`
	Branch     string `json:"branch"`
	URL        string `json:"url"`
	// Draft marks an Advisory draft PR vs an Assisted ready-for-review PR.
	Draft bool `json:"draft"`
	// Reverted names the rollback command, because a reversible change must say how to reverse it.
	Rollback string `json:"rollback"`
}

// BuildCard assembles a Card from a compiled candidate's presentation, its build status, its verdict,
// and the workflow's automation level. It is the single place the source-of-truth verdict is turned
// into UI fields, so state / narration / PR-gate can never drift from the verdict.
func BuildCard(pres proposal.Presentation, buildStatus string, v verification.Verdict, level verification.AutomationLevel) Card {
	canPR, reason := verification.PROpenAvailable(level, v)
	return Card{
		ProposalID:       v.ProposalID,
		Operator:         string(pres.Operator),
		NodeID:           pres.NodeID,
		Pattern:          pres.Pattern,
		DiagID:           pres.DiagID,
		EvidenceCaseIDs:  pres.EvidenceCaseIDs,
		Rationale:        pres.Rationale,
		SourceDiff:       pres.SourceDiff,
		SpecDiff:         pres.SpecDiff,
		BuildStatus:      buildStatus,
		State:            string(verification.State(v)),
		GateResult:       string(v.GateResult),
		HeldOut:          v.HeldOut,
		HeldOutLabel:     verification.HeldOutLabel(v),
		Delta:            v.Delta.Mean,
		CILow:            v.Delta.Low,
		CIHigh:           v.Delta.High,
		Significant:      v.Significant,
		CostDelta:        v.CostDelta,
		LatencyDelta:     v.LatencyDelta,
		CasesFixed:       nonNil(v.CasesFixed),
		CasesBroken:      nonNil(v.CasesBroken),
		Narration:        verification.Narrate(v),
		CanOpenPR:        canPR,
		PRDisabledReason: reason,
	}
}

// CardFor is the ONE place a compiled candidate's build status decides which card it becomes.
//
// # Why this exists rather than three call sites choosing for themselves
//
// Before P14 there were two statuses (built / build_failed) and each producer wrote its own two-branch
// `if`. Adding a THIRD (refused, P14 task 8.2) is exactly the change that breaks that arrangement:
// every producer that was not updated keeps its two branches, sends a refused candidate down the
// `built` path, and renders a zero delta and an empty diff for a change that was never made. That is
// the "looks complete" failure decisions.md D-14.3 is written against, arriving through the surface
// rather than through the codemod.
//
// So the switch lives here, it is exhaustive, and an unrecognised status is treated as REFUSED rather
// than as built — the fail-closed direction, because "we could not tell you what happened" is honest
// and "verified, delta 0.00" is not.
func CardFor(pres proposal.Presentation, status proposal.BuildStatus, buildLog string,
	v verification.Verdict, level verification.AutomationLevel) Card {
	switch status {
	case proposal.BuildBuilt:
		return BuildCard(pres, string(status), v, level)
	case proposal.BuildFailed:
		return BuildFailedCard(pres, buildLog)
	case proposal.BuildUnbuilt:
		return UnbuiltCard(pres, v, buildLog)
	default:
		// BuildRefused, and anything a later phase adds.
		return RefusedCard(pres)
	}
}

// UnbuiltCard renders a candidate that has NOT been through a build gate.
//
// 🔴 BuildUnbuilt used to fall through to RefusedCard, and that was right while `unbuilt` could only
// mean "never compiled" — a candidate with nothing to show is indistinguishable from one the transform
// declined. It stopped being right when a deployment started COMPILING proposals without being able to
// build them: the platform now produces a real reviewable diff and cannot establish that it compiles,
// and RefusedCard drops the diff on purpose ("a refusal that shipped a partial diff is exactly the
// 'looks complete' failure D-14.3 refuses"). So the diff the codemod generated was thrown away by the
// surface, under a narration saying the transform refused a change it had in fact made.
//
// The card SHOWS the diff and is never recommendable. `buildLog` is the gate's own account of what it
// did and did not prove, rendered rather than summarised — a reviewer deciding whether to trust an
// unbuilt change needs to know a parser ran and a compiler did not.
func UnbuiltCard(pres proposal.Presentation, v verification.Verdict, buildLog string) Card {
	c := BuildCard(pres, string(proposal.BuildUnbuilt), v, verification.Advisory)
	// Never one-click-openable, whatever the verdict says: ADR-001's rule is that nothing reaches a
	// repository except as a diff that BUILDS, and this one has not been built.
	//
	// The two reasons are distinct because the next actions are: an uncompiled proposal needs a compile
	// pass, and a compiled one needs a build gate. Decided HERE, from whether the presentation carries a
	// diff, rather than by each caller — this function is the one place a build status becomes a card,
	// and a caller computing its own reason is a second answer to the question it just asked.
	c.CanOpenPR = false
	if pres.SourceDiff == "" {
		c.PRDisabledReason = "this change has not been compiled into a diff yet"
	} else {
		c.PRDisabledReason = "this change has a reviewable diff and has not been proved to build"
	}
	if buildLog != "" {
		c.Narration = strings.TrimRight(c.Narration, " ") + " " + buildLog
	}
	return c
}

// Recommendable reports whether a compiled candidate may appear in the RECOMMENDATIONS list rather
// than the withheld one. Both halves are required: it must have built, and its verdict must have
// passed. A refused candidate fails the first half without ever reaching the second.
func Recommendable(status proposal.BuildStatus, v verification.Verdict) bool {
	return status == proposal.BuildBuilt && v.Passed()
}

// BuildFailedCard renders a build-failed candidate for the withheld section — it has no verdict.
func BuildFailedCard(pres proposal.Presentation, log string) Card {
	return Card{
		ProposalID: pres.ConfigHash, Operator: string(pres.Operator), NodeID: pres.NodeID, Pattern: pres.Pattern,
		DiagID: pres.DiagID, EvidenceCaseIDs: pres.EvidenceCaseIDs, Rationale: pres.Rationale,
		SourceDiff: pres.SourceDiff, SpecDiff: pres.SpecDiff, BuildStatus: string(proposal.BuildFailed),
		State: "gate_failed", GateResult: "build_failed",
		Narration:        "Withheld: the candidate's diff failed to build and was rejected before verification.",
		PRDisabledReason: "build failed",
	}
}

// RefusedCard renders a change the TRANSFORM DECLINED, for the withheld section (P14 task 8.2).
//
// # Why a refusal gets its own card rather than being folded into build_failed
//
// They are different events with different owners. "Build failed" means the engine wrote code and the
// compiler rejected it — a bug on our side, and the log is the next thing to read. "Refused" means the
// engine declined to write code it could not stand behind — a limit, named, and the reason is the next
// thing to read. Rendering the second as the first would send a user hunting a compiler error that does
// not exist, and would quietly claim we tried.
//
// The card carries NO source diff, deliberately: a refusal that shipped a partial diff is exactly the
// "looks complete" failure D-14.3 refuses, and the surface must not reconstruct it.
func RefusedCard(pres proposal.Presentation) Card {
	r := pres.Refusal
	if r == nil {
		r = &proposal.ChangeRefusal{NodeID: pres.NodeID, Reason: "the transform declined this change"}
	}
	where := r.NodeID
	if r.Dimension != "" {
		where = fmt.Sprintf("node %s, dimension %s", r.NodeID, r.Dimension)
	}
	return Card{
		ProposalID: pres.ConfigHash, Operator: string(pres.Operator), NodeID: pres.NodeID, Pattern: pres.Pattern,
		DiagID: pres.DiagID, EvidenceCaseIDs: pres.EvidenceCaseIDs, Rationale: pres.Rationale,
		SpecDiff: pres.SpecDiff, BuildStatus: string(proposal.BuildRefused),
		State: "gate_failed", GateResult: string(proposal.BuildRefused),
		Narration: fmt.Sprintf("Withheld: the transform refused this change at %s. %s. Nothing was "+
			"applied for that dimension — no partial diff was generated.", where, strings.TrimRight(r.Reason, ".")),
		PRDisabledReason: "the transform refused this change",
		RefusedNodeID:    r.NodeID,
		RefusedDimension: r.Dimension,
		RefusedReason:    r.Reason,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MountProposals registers the P5.5 UI, its surface JSON endpoint, and the Assisted open-PR action.
func (s *Server) MountProposals(src ProposalsSource) {
	s.proposals = src
	s.Mux.HandleFunc("GET /recommendations", s.handleProposalsUI)
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/proposals", s.handleProposals)
	s.Mux.HandleFunc("POST /api/v1/workflows/{workflow_id}/proposals/{proposal_id}/open-pr", s.handleOpenPR)
}

func (s *Server) handleProposalsUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p55HTML)
}

func (s *Server) handleProposals(w http.ResponseWriter, r *http.Request) {
	if s.proposals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p5.5 surface is not mounted on this server"})
		return
	}
	principal, authed := auth.PrincipalFrom(r.Context())
	if !authed || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "the recommendation surface requires an authenticated tenant"})
		return
	}
	view, ok := s.proposals.Surface(principal.TenantID, r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no surface for workflow " + r.PathValue("workflow_id")})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleOpenPR is the Assisted apply action. It re-checks the gate at the HTTP boundary: the surface
// is asked for the workflow, and a proposal that is not in the gate-passing Recommendations list (or is
// not one-click-openable) is REFUSED with 409 — the "unverified never ships" guarantee cannot be
// bypassed by POSTing directly.
func (s *Server) handleOpenPR(w http.ResponseWriter, r *http.Request) {
	if s.proposals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p5.5 surface is not mounted on this server"})
		return
	}
	principal, authed := auth.PrincipalFrom(r.Context())
	if !authed || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "opening a pull request requires an authenticated tenant"})
		return
	}
	wf := r.PathValue("workflow_id")
	pid := r.PathValue("proposal_id")

	view, ok := s.proposals.Surface(principal.TenantID, wf)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no surface for workflow " + wf})
		return
	}
	var card *Card
	for i := range view.Recommendations {
		if view.Recommendations[i].ProposalID == pid {
			card = &view.Recommendations[i]
			break
		}
	}
	if card == nil || !card.CanOpenPR {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "proposal " + pid + " is not a gate-passing, one-click-openable recommendation"})
		return
	}
	res, err := s.proposals.OpenPR(principal.TenantID, wf, pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
