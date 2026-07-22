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
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Server is the agentd HTTP API.
type Server struct {
	// DB is the SQLite dev ledger (auth keys, memory). NOT P2's store — see p2.go.
	DB      *sql.DB
	Cfg     config.Config
	Mux     *http.ServeMux
	Handler http.Handler

	// p2 is the Postgres-backed P2 read surface, mounted by MountP2 when available.
	p2 P2Stores

	// monitor is the P2.5 live run-monitoring read model, mounted by MountMonitor when available.
	monitor MonitorSource

	// p35 is the P3.5 pattern-classifier read model, mounted by MountP35 when available.
	p35 PatternSource

	// p4 is the P4 eval-board read model, mounted by MountP4 when available.
	p4 BoardSource

	// secrets is the live provider-credential source, reported by /readyz.
	//
	// The SOURCE, never a credential: this holds the thing that can produce secrets precisely so the
	// endpoint can name it without anyone re-deriving it from configuration and getting a different
	// answer than the gateway did.
	secrets providergateway.Secrets
}

// SetSecretsSource records which secrets source is live so /readyz can report it.
//
// This exists because health-signal-surface is not satisfied by a log line at boot: "the deployment
// is on AWS Secrets Manager" is only useful if it can be checked NOW, by a monitor, on the box that
// is actually misbehaving — and a log line scrolled past three restarts ago cannot be checked at all.
func (s *Server) SetSecretsSource(src providergateway.Secrets) { s.secrets = src }

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

// handleReadyz reports readiness — dependencies (the SQLite ledger) are reachable — and which
// secrets source is live.
//
// The secrets source is reported HERE rather than from a new endpoint on purpose (careful-api-
// creation: a new endpoint is new surface area, and this is one field on a document that already
// answers "what state is this process in"). It reports the source's IDENTITY, not its health: probing
// the secrets manager on every readiness check would make an AWS latency spike look like an agentd
// outage, and would fetch a credential to prove we can fetch a credential.
//
// It is absent rather than "unknown" when unset — a deployment that never wired a source has no
// secrets source, and saying so by omission beats inventing a status for it.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.DB != nil {
		if err := s.DB.PingContext(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "db_unavailable"})
			return
		}
	}
	body := map[string]any{"status": "ready"}
	if s.secrets != nil {
		body["secrets_source"] = s.secrets.Describe()
	}
	writeJSON(w, http.StatusOK, body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
