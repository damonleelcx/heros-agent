// Command herosd serves the conversational console and drives durable goals.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/api"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/intake"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider/deepseek"
	"github.com/heros-foreal/heros/internal/router"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/tools"
	"github.com/heros-foreal/heros/internal/worker"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	web := flag.String("web", "web", "directory holding the console")
	dsn := flag.String("dsn", "", "postgres DSN (default $HEROS_DATABASE_URL)")
	flag.Parse()

	if err := config.LoadDotEnv(".env.local"); err != nil {
		log.Fatalf("config: %v", err)
	}
	key, err := config.DeepSeekKey()
	if err != nil {
		log.Fatalf("%v", err)
	}
	url := *dsn
	if url == "" {
		url = os.Getenv("HEROS_DATABASE_URL")
	}
	if url == "" {
		url = "postgres://heros:heros@localhost:55700/heros?sslmode=disable"
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("postgres: %v — is it running? try `make pg-up`", err)
	}
	if err := migrations.Apply(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	st := store.NewPostgres(db)
	plans, err := planner.Default()
	if err != nil {
		log.Fatalf("planners: %v", err)
	}

	// 🔴 The tool registry is built ONCE at startup, and Register refuses a tool that cannot be operated
	// safely. That is why this is a fatal error rather than a per-request one: a deployment missing a
	// verifier for an effect-bearing tool should fail to start, not fail on the first pull request.
	reg := toolcontract.NewRegistry()
	client := deepseek.New(key)
	// The assessment tool is registered with no source: it is REBOUND when a repository is loaded, by
	// Registry.Replace, which re-runs the same refusals. Registering here proves at startup that the
	// tool satisfies them, rather than discovering it on a customer's first question.
	if err := reg.Register(tools.AssessAxis{
		Provider: client, Model: deepseek.ModelFlash, MaxTokens: 1200,
		Source: tools.FixtureSource{},
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.SynthesiseAssessment{
		Provider: client, Model: deepseek.ModelFlash,
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	// Eval-set generation and comparison. The source-bound ones are REBOUND when a repository loads;
	// registering here proves at startup that they satisfy the contract's refusals rather than
	// discovering it on a customer's first question.
	if err := reg.Register(tools.GenerateCases{
		Provider: client, Model: deepseek.ModelFlash, Source: tools.FixtureSource{},
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.QualityGate{}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.PublishEvalSet{Root: "."}, tools.NewPublishVerifier(".")); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.CompareAssessments{Store: st, Tenant: "local"}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}

	mem := memory.NewPG(db)

	w := worker.New("herosd", st, reg)
	w.Lease = 2 * time.Minute
	w.Episodes = mem

	cache, _ := os.UserCacheDir()
	srv := &api.Server{
		Store:    st,
		Planners: plans,
		Sup:      api.NewSupervisor(st, w),
		Resolver: intake.NewResolver(filepath.Join(cache, "heros", "repos")),
		Router:   router.New(),
		Tenant:   "local",
		Ceilings: bounds.Ceilings{
			MaxIterations: 200, MaxTasks: 60, MaxAttemptsPerTask: 2, MaxToolCalls: 200,
			MaxTokens: 400_000, MaxCostCents: 100, MaxWallClock: 20 * time.Minute, MaxSpawnDepth: 3,
		},
	}
	srv.ToolRegistry = reg
	srv.Provider = client
	srv.Model = deepseek.ModelFlash
	srv.Approvals = api.NewApprovals()
	srv.Episodes = mem

	mux := srv.Routes()
	mux.Handle("/", http.FileServer(http.Dir(*web)))

	httpSrv := &http.Server{
		Addr: *addr, Handler: mux,
		ReadHeaderTimeout: 10 * time.Second,
		// 🚫 No WriteTimeout. The events endpoint is a long-lived stream, and a write deadline would cut
		// every run off mid-flight at exactly the moment it became interesting.
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	fmt.Printf("heros console  http://%s\n", *addr)
	fmt.Printf("model          %s\n", deepseek.ModelFlash)
	fmt.Printf("database       %s\n", redact(url))
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// redact hides a password in a DSN before it reaches a terminal or a log.
func redact(dsn string) string {
	at := -1
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		return dsn
	}
	scheme := 0
	for i := 0; i+2 < len(dsn); i++ {
		if dsn[i] == ':' && dsn[i+1] == '/' && dsn[i+2] == '/' {
			scheme = i + 3
			break
		}
	}
	return dsn[:scheme] + "***" + dsn[at:]
}
