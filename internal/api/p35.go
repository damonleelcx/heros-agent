package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// The P3.5 pattern-classifier read surface. It annotates the workflow graph with each subgraph's
// pattern label(s) and confidence, distinguishing a rule-sourced label from an llm-sourced one, and
// showing an unclassified region as "not yet classified" rather than as a blank.
//
// Two routes, one read model:
//   GET /p35/graph?workflow_id=…                the view (self-contained HTML, embedded)
//   GET /api/v1/workflows/{workflow_id}/pattern-graph  the JSON read model (404 for no such workflow)
//
// READ-ONLY. The classifier writes labels into the IR; this surface only shows what is there.

// PatternSource is the read model the routes serve: a classified workflow graph. An interface so the
// API depends on no concrete store and a test can stub it.
type PatternSource interface {
	GraphView(workflowID string) (patternclassifier.GraphView, bool)
}

// MountP35 registers the pattern-classifier routes. Call after New.
func (s *Server) MountP35(src PatternSource) {
	s.p35 = src
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/pattern-graph", s.handleP35Graph)
}

func (s *Server) handleP35Graph(w http.ResponseWriter, r *http.Request) {
	if s.p35 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the pattern classifier is not mounted"})
		return
	}
	view, ok := s.p35.GraphView(r.PathValue("workflow_id"))
	if !ok {
		// 404 is DISTINCT from a workflow that exists but carries no labels. The view's empty state
		// depends on telling "no such workflow" apart from "workflow exists, nothing classified yet" —
		// collapsing the two is exactly how "not yet classified" turns into a misleading blank.
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
