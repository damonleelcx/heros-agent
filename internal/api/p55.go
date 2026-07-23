package api

import (
	_ "embed"
	"net/http"

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

// P55Source is what the API asks of the engine: the assembled recommendation surface for a workflow,
// and the Assisted apply action (open a PR carrying a verified proposal's diff). OpenPR NEVER merges
// and never mutates the working tree — it opens a reviewable PR for the human (design Decision 9).
type P55Source interface {
	Surface(workflowID string) (Surface, bool)
	OpenPR(workflowID, proposalID string) (PRResult, error)
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

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MountP55 registers the P5.5 UI, its surface JSON endpoint, and the Assisted open-PR action.
func (s *Server) MountP55(src P55Source) {
	s.p55 = src
	s.Mux.HandleFunc("GET /p55/recommendations", s.handleP55UI)
	s.Mux.HandleFunc("GET /api/p55/workflows/{workflow_id}/surface", s.handleP55Surface)
	s.Mux.HandleFunc("POST /api/p55/workflows/{workflow_id}/proposals/{proposal_id}/open-pr", s.handleP55OpenPR)
}

func (s *Server) handleP55UI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p55HTML)
}

func (s *Server) handleP55Surface(w http.ResponseWriter, r *http.Request) {
	if s.p55 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p5.5 surface is not mounted on this server"})
		return
	}
	view, ok := s.p55.Surface(r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no surface for workflow " + r.PathValue("workflow_id")})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleP55OpenPR is the Assisted apply action. It re-checks the gate at the HTTP boundary: the surface
// is asked for the workflow, and a proposal that is not in the gate-passing Recommendations list (or is
// not one-click-openable) is REFUSED with 409 — the "unverified never ships" guarantee cannot be
// bypassed by POSTing directly.
func (s *Server) handleP55OpenPR(w http.ResponseWriter, r *http.Request) {
	if s.p55 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p5.5 surface is not mounted on this server"})
		return
	}
	wf := r.PathValue("workflow_id")
	pid := r.PathValue("proposal_id")

	view, ok := s.p55.Surface(wf)
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
	res, err := s.p55.OpenPR(wf, pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
