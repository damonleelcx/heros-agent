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
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/db"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Server is a running agentd instance: the HTTP API over the local ledger and, when the deployment
// declares one, the platform database.
type Server struct {
	Config config.Config
	// DB is the local SQLite ledger (auth, registries, memory, tools, inbox).
	DB *sql.DB
	// PlatformDB is the Postgres the platform schema lives in, or nil when the deployment declares none.
	PlatformDB *sql.DB
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

	// The PLATFORM database (P19 Decision 9).
	//
	// This is the store db/migrations/postgres models — lineage, registries, variant specs, transforms,
	// runs, eval results, billing, delivery, consent. Until now nothing in the deployed path opened it:
	// the manifests started Postgres, set HEROS_DATASTORE_NAME=postgres, and the process pinged the local
	// SQLite ledger under that name. The migrations were applied only by a demo binary reading files from
	// a relative path, so "upgrade preserves user state" had no mechanism behind it at all.
	//
	// A deployment with no DSN declares no platform database: the DB-backed capabilities register
	// unsourced and /readyz reports no `postgres` component, which is true. A deployment WITH a DSN that
	// cannot be reached fails the boot, for the reason the secrets source does: a process that starts,
	// looks healthy, and fails the first real request is a worse signal than one that does not start.
	platformDB, err := openPlatformDB(ctx)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	if platformDB != nil {
		res, mErr := pgmigrate.Apply(ctx, platformDB)
		if mErr != nil {
			_ = platformDB.Close()
			_ = database.Close()
			return nil, fmt.Errorf("platform schema: %w", mErr)
		}
		switch {
		case res.FreshInstall():
			log.Printf("platform schema: applied %d migrations to an empty database", len(res.Applied))
		case len(res.Applied) > 0:
			log.Printf("platform schema: applied %d new migrations (%d already present)", len(res.Applied), len(res.Already))
		default:
			log.Printf("platform schema: already current (%d migrations)", len(res.Already))
		}
		// Probed, not described: the name `postgres` on /readyz now means this pool.
		handler.AddComponentProbe(api.NewDBComponentProbe("postgres", platformDB))
	}

	// Every capability surface the platform ships is registered here — sourced where a durable store
	// exists, nil where none does so the answer is 503 not-mounted rather than 404 (Decision 10).
	caps, err := mountCapabilities(handler, platformDB, cfg.DataDir, strings.TrimSpace(os.Getenv("CONSOLE_HEALTH_URL")))
	if err != nil {
		if platformDB != nil {
			_ = platformDB.Close()
		}
		_ = database.Close()
		return nil, fmt.Errorf("capabilities: %w", err)
	}
	logCapabilities(caps)

	// The P24 error-reporting boundary (task 2.10).
	//
	// 🔴 With no DSN this returns the ABSENT reporter: no transmit, no warning, and `/readyz` reports
	// `absent`. That is the expected and correct state on a customer's Compose or Kubernetes install and
	// on an air-gapped network, and it is deliberately SILENT — a deployment that was never meant to
	// report is not degraded, and a warning that fires on every correct install teaches an operator to
	// ignore warnings.
	//
	// A DSN that is SET and unusable is a hard boot failure, for the opposite reason: that is a
	// deployment somebody meant to configure and got wrong, and the alternative is a process that starts,
	// looks healthy, and silently reports nothing for a month.
	reporter, err := erroreport.FromEnv(log.Printf)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("error reporting: %w", err)
	}
	handler.SetErrorReporter(reporter)
	if state, _ := reporter.State(); state != erroreport.StateAbsent {
		log.Printf("error reporting: %s", state)
	}

	// The P9 customer console, aggregated into /readyz when this deployment ships one (FR25).
	//
	// Wired from the environment rather than from config.json for the same reason the secrets source
	// is: it is a property of the DEPLOYMENT — which containers are in this unit — not of the
	// platform's configuration. A deployment with no console leaves it unset and /readyz reports on
	// the Go service alone, which is true. A deployment that HAS one and leaves it unset gets a
	// readiness signal that says "ready" while the surface its users reach is dead, which is the
	// lying health signal `health-signal-surface` exists to forbid.
	if consoleHealth := strings.TrimSpace(os.Getenv("CONSOLE_HEALTH_URL")); consoleHealth != "" {
		handler.SetConsoleProbe(api.NewHTTPComponentProbe("customer_console", consoleHealth))
		log.Printf("readiness aggregates the customer_console component at %s", consoleHealth)

		// The customer IDENTITY PROVIDER, read from the same endpoint and reported as its own named
		// component (P22 task 7.1). Two components rather than one, because they fail for different
		// reasons and an operator needs to know which: `customer_console` says the surface is down,
		// `identity_provider` says the surface is up and nobody can sign in. Collapsing them would
		// produce a signal that is true and useless.
		handler.SetIdentityProbe(api.NewHTTPIdentityProbe(consoleHealth))
		log.Printf("readiness aggregates the identity_provider component from %s", consoleHealth)
	}

	// The rest of the platform deployment (P19). Each component is aggregated into /readyz when — and
	// only when — this deployment ships it, wired by a health URL in the environment. Same posture as
	// the console: a component with no URL is honestly absent, not "unknown"; a component WITH a URL
	// that is unreachable turns /readyz not-ready and NAMES itself. The name is the one /readyz
	// reports, so a monitor alerts on "admin_console" or "object_store", not on "not ready".
	//
	//   ADMIN_CONSOLE_HEALTH_URL  the P8 operator console (its own origin/unit) — admin-console-deploy FR
	//   OBJECT_STORE_HEALTH_URL   the object store (MinIO / content store)
	//   QUEUE_HEALTH_URL          the queue (NATS/JetStream)
	//   VECTOR_STORE_HEALTH_URL   the vector store (Qdrant)
	//   GRAPH_STORE_HEALTH_URL    the graph store (Neo4j)
	for _, c := range []struct{ env, name string }{
		{"ADMIN_CONSOLE_HEALTH_URL", "admin_console"},
		{"OBJECT_STORE_HEALTH_URL", "object_store"},
		{"QUEUE_HEALTH_URL", "queue"},
		{"VECTOR_STORE_HEALTH_URL", "vector_store"},
		{"GRAPH_STORE_HEALTH_URL", "graph_store"},
	} {
		if url := strings.TrimSpace(os.Getenv(c.env)); url != "" {
			handler.AddComponentProbe(api.NewHTTPComponentProbe(c.name, url))
			log.Printf("readiness aggregates the %s component at %s", c.name, url)
		}
	}

	// The secret source's REACHABILITY, aggregated fail-closed (platform-llm-access FR, air-gapped
	// FR). Describe() already names the live source on /readyz; this adds the degraded verdict. It is
	// wired only for an out-of-process store that can actually be unreachable — the air-gapped on-prem
	// gateway (HEROS_SECRETS_HEALTH_URL). For AWS Secrets Manager the store is reached with an ambient
	// identity and there is no health URL to sh/probe; for the env source there is nothing off-box to
	// be unreachable. When the configured gateway is down, /readyz reports secrets_source degraded and
	// the model-dependent stages read that as degraded-not-available — fail static, never fail open,
	// and never a startup dependency (the probe runs at readiness time, not at boot).
	if secretsHealth := strings.TrimSpace(os.Getenv("HEROS_SECRETS_HEALTH_URL")); secretsHealth != "" {
		handler.AddComponentProbe(api.NewHTTPComponentProbe("secrets_source", secretsHealth))
		log.Printf("readiness aggregates the secrets_source reachability at %s", secretsHealth)
	}
	httpServer := &http.Server{
		Handler:           handler.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		if platformDB != nil {
			_ = platformDB.Close()
		}
		_ = database.Close()
		return nil, fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	go func() {
		log.Printf("agentd listening on http://%s (health: /healthz)", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("agentd HTTP: %v", err)
		}
	}()

	return &Server{Config: cfg, DB: database, PlatformDB: platformDB, HTTPServer: httpServer}, nil
}

// Shutdown stops the HTTP server and closes both stores.
//
// Both are closed even if the first fails, and the first error is the one returned: a shutdown that
// abandoned the platform pool because the ledger's Close errored would leak connections against the
// store every other component shares, on every restart.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.HTTPServer != nil {
		if err := s.HTTPServer.Shutdown(ctx); err != nil {
			return err
		}
	}
	var firstErr error
	if s.DB != nil {
		if err := s.DB.Close(); err != nil {
			firstErr = err
		}
	}
	if s.PlatformDB != nil {
		if err := s.PlatformDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
