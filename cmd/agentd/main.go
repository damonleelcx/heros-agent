package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
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

func main() {
	cfgPath := flag.String("config", "", "path to config.json (optional)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.AuthMode == "required" {
		reg := auth.NewRegistry(cfg)
		if !reg.HasKeys() {
			log.Fatal("auth_mode=required but tenant_credentials is empty")
		}
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	sqlitePath := filepath.Join(cfg.DataDir, "agent.db")
	database, err := db.Open(sqlitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if err := promptlayer.SeedIfEmpty(database, cfg.DataDir); err != nil {
		log.Fatal(err)
	}
	if err := harness.SeedHarness(database); err != nil {
		log.Fatal(err)
	}

	bctx, bcancel := context.WithTimeout(context.Background(), 60*time.Second)
	rt, err := platform.Bootstrap(bctx, cfg, database)
	bcancel()
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close(context.Background())

	if rt.Neo != nil {
		sctx, scancel := context.WithTimeout(context.Background(), 60*time.Second)
		if err := indexsync.SyncSkillsTools(sctx, rt.Neo, database); err != nil {
			log.Printf("neo4j index sync: %v", err)
		}
		scancel()
	}

	srv := api.New(database, cfg, rt)
	schCtx, schCancel := context.WithCancel(context.Background())
	defer schCancel()
	go scheduler.Run(schCtx, database, rt.Bus, 30*time.Second)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("agentd listening on http://%s (approval UI: /)", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}
