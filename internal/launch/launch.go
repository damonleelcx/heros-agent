// Package launch starts the agentd HTTP stack (SQLite, platform runtime, scheduler) in-process.
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
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/db"
	"github.com/heros-foreal/agentd/internal/harness"
	"github.com/heros-foreal/agentd/internal/indexsync"
	"github.com/heros-foreal/agentd/internal/platform"
	"github.com/heros-foreal/agentd/internal/promptlayer"
	"github.com/heros-foreal/agentd/internal/scheduler"
)

// Server is a running agentd instance (HTTP API + background scheduler).
type Server struct {
	Config     config.Config
	DB         *sql.DB
	RT         *platform.Runtime
	HTTPServer *http.Server
	schCancel  context.CancelFunc
}

// AgentdBaseURL returns the HTTP origin for in-process clients (e.g. heros-cli).
func (s *Server) AgentdBaseURL() string {
	addr := strings.TrimSpace(s.Config.ListenAddr)
	if addr == "" {
		return "http://127.0.0.1:8787"
	}
	if addr[0] == ':' {
		return "http://127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

// StartAgentd boots SQLite, seeds, platform runtime, HTTP server, and scheduler.
// The HTTP server listens in a background goroutine; call Shutdown when finished.
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

	sqlitePath := filepath.Join(cfg.DataDir, "agent.db")
	database, err := db.Open(sqlitePath)
	if err != nil {
		return nil, err
	}

	if err := promptlayer.SeedIfEmpty(database, cfg.DataDir); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := harness.SeedHarness(database); err != nil {
		_ = database.Close()
		return nil, err
	}

	bctx, bcancel := context.WithTimeout(ctx, 60*time.Second)
	rt, err := platform.Bootstrap(bctx, cfg, database)
	bcancel()
	if err != nil {
		_ = database.Close()
		return nil, err
	}

	if rt.Neo != nil {
		sctx, scancel := context.WithTimeout(context.Background(), 60*time.Second)
		if err := indexsync.SyncSkillsTools(sctx, rt.Neo, database); err != nil {
			log.Printf("neo4j index sync: %v", err)
		}
		scancel()
	}

	srvHandler := api.New(database, cfg, rt)
	schCtx, schCancel := context.WithCancel(context.Background())
	go scheduler.Run(schCtx, database, rt.Bus, 30*time.Second)

	httpServer := &http.Server{
		Handler:           srvHandler.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		schCancel()
		rt.Close(context.Background())
		_ = database.Close()
		return nil, fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	go func() {
		log.Printf("agentd listening on http://%s (approval UI: /)", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("agentd HTTP: %v", err)
		}
	}()

	return &Server{
		Config:     cfg,
		DB:         database,
		RT:         rt,
		HTTPServer: httpServer,
		schCancel:  schCancel,
	}, nil
}

// WaitReady polls GET /health until success or ctx is done.
func WaitReady(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	healthURL := strings.TrimRight(baseURL, "/") + "/health"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return nil
				}
			}
		}
	}
}

// Shutdown stops the scheduler, HTTP server, runtime hooks, and closes SQLite.
func (s *Server) Shutdown(parent context.Context) error {
	if s == nil {
		return nil
	}
	s.schCancel()
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	_ = s.HTTPServer.Shutdown(ctx)
	s.RT.Close(ctx)
	return s.DB.Close()
}
