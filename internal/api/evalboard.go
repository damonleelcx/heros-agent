package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/evalboard"
)

// p4.go mounts the P4 eval board, following the p35.go pattern exactly: one Mount method, a
// nil-able source interface, 503 when unmounted (deliberately distinct from 404), and the demo page
// embedded into the binary so deploying the UI is deploying agentd.
//
// The read model is an INTERFACE so the API depends on no concrete store and the tests stub it. The
// board itself is computed in internal/evalboard — the browser renders what it is handed and derives
// nothing, because "are these two tied" has exactly one correct answer and a second implementation
// of it in JavaScript would drift.

// BoardSource is the one question the API asks: give me the board for this workflow, under this
// weight profile.
//
// The profile is a PARAMETER of the read, not a mutation: switching profiles is a re-read that
// re-ranks from the cached normalized values and enqueues zero runs. Modelling it as a GET with a
// query parameter is what makes that structural rather than a promise — a GET cannot enqueue work.
type BoardSource interface {
	// Board returns one TENANT's board for a workflow.
	//
	// 🔴 tenantID is not decoration. This is the third interface in this package to learn it — after
	// PatternSource and GraphEditorSource — and all three learned it the same way: the signature was
	// written against a demo stub that returned one fixture to every caller, so the missing scope was
	// invisible until something durable was mounted behind it. Workflow ids are chosen by customers,
	// they collide, and the caller reads one straight out of a URL. A board carries scores, spend and
	// gate verdicts; serving them across tenants is a data breach with no line of code looking wrong.
	Board(tenantID, workflowID, profile string) (evalboard.View, bool)
}

// MountEvalBoard registers the P4 board UI and its JSON endpoint.
func (s *Server) MountEvalBoard(src BoardSource) {
	s.evalBoard = src
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/eval-board", s.handleEvalBoard)
}

func (s *Server) handleEvalBoard(w http.ResponseWriter, r *http.Request) {
	if s.evalBoard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "p4 board is not mounted on this server",
		})
		return
	}
	principal, authed := auth.PrincipalFrom(r.Context())
	if !authed || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "the eval board requires an authenticated tenant",
		})
		return
	}
	workflowID := r.PathValue("workflow_id")
	profile := r.URL.Query().Get("profile")
	view, ok := s.evalBoard.Board(principal.TenantID, workflowID, profile)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no board for workflow " + workflowID,
		})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
