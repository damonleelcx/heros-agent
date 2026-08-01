package api

import (
	_ "embed"
	"net/http"

	"github.com/heros-foreal/agentd/internal/scorecard"
)

// p45.go mounts the P4.5 read-only per-run scorecard, following the p4.go / p35.go pattern exactly:
// one Mount method, a nil-able source interface, 503 when unmounted (distinct from 404), the demo
// page embedded into the binary.
//
// The scorecard EXPLAINS; it does not FIX. The route is a GET — a GET cannot enqueue work or apply a
// change — and the view carries no apply affordance. That the phase is read-only is therefore
// structural at the HTTP boundary too, not only in the data model.

//go:embed static/p45scorecard.html
var p45ScorecardHTML []byte

// ScorecardSource is the one question the API asks: give me the read-only scorecard for this
// variant's run. Keyed by variant id, because a scorecard is per-run — one variant × one eval set.
type ScorecardSource interface {
	Scorecard(variantID string) (scorecard.View, bool)
}

// MountScorecard registers the scorecard UI and its JSON endpoint.
func (s *Server) MountScorecard(src ScorecardSource) {
	s.scorecard = src
	s.Mux.HandleFunc("GET /scorecard", s.handleScorecardUI)
	s.Mux.HandleFunc("GET /api/v1/variants/{variant_id}/scorecard", s.handleScorecard)
}

func (s *Server) handleScorecardUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p45ScorecardHTML)
}

func (s *Server) handleScorecard(w http.ResponseWriter, r *http.Request) {
	if s.scorecard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the p4.5 scorecard is not mounted on this server",
		})
		return
	}
	view, ok := s.scorecard.Scorecard(r.PathValue("variant_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no scorecard for variant " + r.PathValue("variant_id"),
		})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
