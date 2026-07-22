package api

import (
	_ "embed"
	"net/http"

	"github.com/heros-foreal/agentd/internal/evalboard"
)

//go:embed static/p4board.html
var p4BoardHTML []byte

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
	Board(workflowID, profile string) (evalboard.View, bool)
}

// MountP4 registers the P4 board UI and its JSON endpoint.
func (s *Server) MountP4(src BoardSource) {
	s.p4 = src
	s.Mux.HandleFunc("GET /p4/board", s.handleP4UI)
	s.Mux.HandleFunc("GET /api/p4/workflows/{workflow_id}/board", s.handleP4Board)
}

func (s *Server) handleP4UI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p4BoardHTML)
}

func (s *Server) handleP4Board(w http.ResponseWriter, r *http.Request) {
	if s.p4 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "p4 board is not mounted on this server",
		})
		return
	}
	workflowID := r.PathValue("workflow_id")
	profile := r.URL.Query().Get("profile")
	view, ok := s.p4.Board(workflowID, profile)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no board for workflow " + workflowID,
		})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
