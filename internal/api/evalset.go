package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/evalboard"
)

// evalset.go serves the eval-set surface behind `/app/workflows/{id}/evalset` (P30 task 1.12).
//
// # Why a route and not a modal on the board
//
// "Which cases is this number computed over?" is a question somebody asks in a review, links to, and
// comes back to. A modal has no URL, so it cannot be sent to the person who needs to see it, and it
// disappears the moment the reader navigates — which for a table they are meant to read against the
// board is exactly wrong. Deep-linkable, per NFR17's standing preference for a route over an overlay.

// EvalSetSource answers "what is in this workflow's eval set?" for one tenant.
//
// tenantID is a parameter for the reason BoardSource's is: case ids are customer-authored strings and
// workflow ids collide across tenants, so a read scoped by workflow alone serves one customer's eval
// set to another with no line of code looking wrong.
type EvalSetSource interface {
	EvalSet(tenantID, workflowID string) (evalboard.EvalSetView, bool)
}

// MountEvalSet registers the eval-set read. Optional, like every mount: unmounted it answers 503,
// which says this deployment does not serve the surface rather than that the workflow does not exist.
func (s *Server) MountEvalSet(src EvalSetSource) {
	s.evalSet = src
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/eval-set", s.handleEvalSet)
}

func (s *Server) handleEvalSet(w http.ResponseWriter, r *http.Request) {
	if s.evalSet == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "the eval-set surface is not mounted on this deployment"})
		return
	}
	principal, authed := auth.PrincipalFrom(r.Context())
	if !authed || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{
			Error: "the eval set requires an authenticated tenant"})
		return
	}
	view, ok := s.evalSet.EvalSet(principal.TenantID, r.PathValue("workflow_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, specError{
			Error: "no eval set for workflow " + r.PathValue("workflow_id")})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
