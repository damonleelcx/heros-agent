// Package launch starts the agentd HTTP server (SQLite ledger + minimal API) in-process.
//
// Reduced to the minimal boot path after the pivot: open the SQLite ledger,
// build the API handler, and serve. The retired agent's bootstrap (platform
// runtime wiring for Qdrant/Neo4j/NATS, scheduler, collective poller, vault
// indexer, memory sweeper, prompt/harness seeding) has been removed;
// subsystems are reintroduced per phase (see openspec/changes/*).
package launch

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/db"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Server is a running agentd instance (HTTP API over the SQLite ledger).
type Server struct {
	Config     config.Config
	DB         *sql.DB
	HTTPServer *http.Server
}

// StartAgentd opens the ledger and serves the HTTP API in a background
// goroutine. Call Shutdown when finished.
func StartAgentd(ctx context.Context, cfg config.Config) (*Server, error) {
	if cfg.AuthMode == "required" {
		reg := auth.NewRegistry(cfg)
		if !reg.HasKeys() {
			return nil, fmt.Errorf("auth_mode=required but tenant_credentials is empty")
		}
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}

	database, err := db.Open(filepath.Join(cfg.DataDir, "agent.db"))
	if err != nil {
		return nil, err
	}

	// Resolve the secrets source at BOOT, not at the first provider call.
	//
	// Two reasons, and neither is tidiness. First, failing closed here turns a misconfigured
	// deployment into a process that does not start — the loudest signal there is — instead of one
	// that starts, looks healthy, serves for an hour, and then fails the first real model call with a
	// credential error that reads like an IAM problem. Second, /readyz can only report the live source
	// if the live source is decided once, here, rather than re-derived by each caller.
	secrets, err := providergateway.NewSecretsFromEnv(ctx)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("secrets source: %w", err)
	}
	log.Printf("secrets source: %s (%s)", secrets.Describe().Kind, secrets.Describe().Detail)

	handler := api.New(database, cfg)
	handler.SetSecretsSource(secrets)
	httpServer := &http.Server{
		Handler:           handler.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	go func() {
		log.Printf("agentd listening on http://%s (health: /healthz)", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("agentd HTTP: %v", err)
		}
	}()

	return &Server{Config: cfg, DB: database, HTTPServer: httpServer}, nil
}

// Shutdown stops the HTTP server and closes the ledger.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.HTTPServer != nil {
		if err := s.HTTPServer.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.DB != nil {
		return s.DB.Close()
	}
	return nil
}
