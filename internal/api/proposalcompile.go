package api

import (
	"context"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/hostedcompile"
)

// proposalcompile.go exposes the codemod as an explicit action, beside the generator.
//
// Two actions rather than one, and the split is the same one the domain makes: GENERATING a proposal
// reads metrics and a graph and is cheap; COMPILING one extracts a source snapshot, re-parses the
// repository and runs the codemod over it, which is seconds of CPU per call. Folding them together
// would also make the expensive half unavoidable for a caller who only wants to see what was found —
// and would hide which of the two failed when nothing appears.

// ProposalCompiler compiles a workflow's uncompiled proposals into reviewable diffs.
type ProposalCompiler interface {
	Compile(ctx context.Context, tenantID, workflowID string) (hostedcompile.Result, error)
}

// MountProposalCompile registers the compile action. Call after New. Optional, like every mount.
func (s *Server) MountProposalCompile(c ProposalCompiler) {
	s.proposalCompile = c
	s.Mux.HandleFunc("POST /api/v1/workflows/{workflow_id}/proposals/compile", s.handleCompileProposals)
}

func (s *Server) handleCompileProposals(w http.ResponseWriter, r *http.Request) {
	if s.proposalCompile == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment does not compile proposals into diffs"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "compiling proposals requires an authenticated tenant"})
		return
	}
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a compile pass must name a workflow"})
		return
	}

	// Scope is the AUTHENTICATED tenant. A pass extracts one tenant's source snapshot and rewrites it;
	// a tenant taken from the request would run the codemod over somebody else's repository.
	res, err := s.proposalCompile.Compile(r.Context(), principal.TenantID, workflowID)
	if err != nil {
		// A read or materialization failure is NOT a state. hostedcompile returns its "nothing was
		// compiled" answers as states with a 200 and reserves the error for a dependency it could not
		// reach — reporting that as `no_source` would tell a customer to push source they have pushed.
		writeJSON(w, http.StatusBadGateway, specError{Error: "the compile pass failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
