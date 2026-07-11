// Package api serves the agentd HTTP surface.
//
// This is the minimal foundation left after the pivot to the LLM Agentic
// Workflow Evaluation & Configuration System: a health/readiness surface
// behind the existing auth policy. The retired agent's large route set
// (memory, collective, harness, vaults, catalog sync, …) has been removed.
// Discovery / config / runtime / eval endpoints are added per phase — see
// openspec/changes/* and docs/prd/.
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
)

// Server is the agentd HTTP API.
type Server struct {
	DB      *sql.DB
	Cfg     config.Config
	Mux     *http.ServeMux
	Handler http.Handler
}

// New builds the minimal agentd HTTP surface. Health/readiness are public;
// future /api/* routes are gated by API-key auth when auth_mode=required.
func New(db *sql.DB, cfg config.Config) *Server {
	s := &Server{DB: db, Cfg: cfg, Mux: http.NewServeMux()}
	s.Mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.Mux.HandleFunc("GET /readyz", s.handleReadyz)

	var h http.Handler = s.Mux
	if cfg.AuthMode == "required" {
		reg := auth.NewRegistry(cfg)
		h = auth.Compose(reg, h) // gates /api/*; health paths stay open
	}
	s.Handler = h
	return s
}

// handleHealthz reports liveness — the process is up and serving.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleReadyz reports readiness — dependencies (the SQLite ledger) are reachable.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.DB != nil {
		if err := s.DB.PingContext(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "db_unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
