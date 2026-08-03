package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
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
	// GraphView returns a workflow's classified graph for ONE tenant.
	//
	// 🔴 tenantID is not decoration. This interface took only a workflow id, which is safe for a
	// single-tenant demo and a cross-tenant read the moment a real store sits behind it: workflow ids
	// are chosen by customers, they collide, and the caller supplies one from a URL. Mounting a
	// database-backed source behind the old signature would have served tenant A's graph to tenant B
	// with no code looking wrong anywhere. The scope now travels with the question.
	GraphView(tenantID, workflowID string) (patternclassifier.GraphView, bool)
}

// MountPatternGraph registers the pattern-classifier routes. Call after New.
func (s *Server) MountPatternGraph(src PatternSource) {
	s.patternGraph = src
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/pattern-graph", s.handlePatternGraph)
}

func (s *Server) handlePatternGraph(w http.ResponseWriter, r *http.Request) {
	if s.patternGraph == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the pattern classifier is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "reading a workflow graph requires an authenticated tenant"})
		return
	}
	view, ok := s.patternGraph.GraphView(principal.TenantID, r.PathValue("workflow_id"))
	if !ok {
		// 404 is DISTINCT from a workflow that exists but carries no labels. The view's empty state
		// depends on telling "no such workflow" apart from "workflow exists, nothing classified yet" —
		// collapsing the two is exactly how "not yet classified" turns into a misleading blank.
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such workflow"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
