package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/proposalgen"
)

// proposalgen.go exposes the platform-side proposal generator as an explicit action.
//
// # Why a POST somebody makes, and not a background pass
//
// A generation pass reads a customer's discovered graph and writes proposal rows that a delivery
// pipeline can later act on. A loop doing that on its own schedule is harder to reason about than a
// call somebody made — and it would generate against every workflow on every tick, including ones whose
// owner has not looked at the surface in months.
//
// # Why the response is a STATE and not a list
//
// `proposalgen.Result` carries a closed state and a sentence, always. "You have linked no runs", "you
// have pushed no source", "this deployment publishes no model catalog" and "nothing here is a cost
// bottleneck" are four different answers with four different next actions, and an empty array is the
// one shape that can tell none of them apart. The console renders the sentence; the array is secondary.

// ProposalGenerator runs one generation pass for a tenant's workflow.
type ProposalGenerator interface {
	Generate(ctx context.Context, tenantID, workflowID string) (proposalgen.Result, error)
}

// MountProposalGeneration registers the generate action. Call after New. Optional, like every mount:
// unmounted it answers 503, which says this deployment does not generate proposals rather than that the
// workflow does not exist.
//
// # Two paths, one handler, for one release (P30 task 1.8)
//
// 🔴 The flat path is the one that can be PUBLISHED. `/api/v1/workflows/{id}/proposals/generate` cannot
// be matched by an `Exact` ingress rule — the id is in the middle of it — and the only rule that could
// reach it is a `Prefix` under `/api/v1/workflows/`, which would simultaneously publish `commit`,
// `orderings`, `orderings/stream`, `validate`, `proposals/{id}/open-pr`, `pattern-graph`, `eval-board`
// and `nodes`. See publicroutes.go and TestAPrefixRuleWouldPublishItsSiblings: this is the exact shape
// that already 404'd three CLI commands at the edge behind a green build.
//
// Both shapes are served for one release (expand-contract). The parameterised one keeps working for a
// console built before this change; it is never published, which is the status quo stated rather than
// inherited, and it is removed when the console floor moves.
func (s *Server) MountProposalGeneration(g ProposalGenerator) {
	s.proposalGen = g
	s.Mux.HandleFunc("POST /api/v1/proposal-generations", s.handleGenerateProposalsFlat)
	s.Mux.HandleFunc("POST /api/v1/workflows/{workflow_id}/proposals/generate", s.handleGenerateProposals)
}

// generateRequest is the flat path's body. The identifier moves from the path into it — that is the
// whole change, and it is what makes the route publishable as `Exact`.
type generateRequest struct {
	WorkflowID string `json:"workflow_id"`
}

func (s *Server) handleGenerateProposalsFlat(w http.ResponseWriter, r *http.Request) {
	var req generateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "a generation pass is addressed by a JSON body naming the workflow: {\"workflow_id\": \"...\"}"})
		return
	}
	s.generateProposals(w, r, req.WorkflowID)
}

func (s *Server) handleGenerateProposals(w http.ResponseWriter, r *http.Request) {
	s.generateProposals(w, r, r.PathValue("workflow_id"))
}

func (s *Server) generateProposals(w http.ResponseWriter, r *http.Request, workflowID string) {
	if s.proposalGen == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment does not generate proposals"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "generating proposals requires an authenticated tenant"})
		return
	}
	if workflowID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a generation pass must name a workflow"})
		return
	}

	// Scope is the AUTHENTICATED tenant, never anything in the request. A pass reads one tenant's graph
	// and writes proposals attributed to them; a tenant taken from the body would generate against
	// somebody else's workflow and file the result under them.
	res, err := s.proposalGen.Generate(r.Context(), principal.TenantID, workflowID)
	if err != nil {
		// 🔴 A read failure is NOT a state. proposalgen returns its "nothing was produced" answers as
		// states with a 200, and reserves the error for a store it could not reach — reporting that as
		// `no_linked_runs` would tell a customer to link a run they have already linked.
		writeJSON(w, http.StatusBadGateway, specError{Error: "the generation pass failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
