// Command livecheck runs one real durable goal end to end against DeepSeek and a real Postgres, and
// prints what it actually cost. It is the answer to "no number in this repo came from a real call".
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/discovery"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intake"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/provider/deepseek"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/tools"
	"github.com/heros-foreal/heros/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: livecheck <path-or-github-ref>")
		os.Exit(2)
	}
	if err := config.LoadDotEnv(".env.local"); err != nil {
		die("dotenv", err)
	}

	// ── intake: resolve what was typed to a pinned revision ──────────────────────────────────────
	cache, _ := os.UserCacheDir()
	src, err := intake.NewResolver(filepath.Join(cache, "heros", "repos")).Resolve(os.Args[1])
	if err != nil {
		die("intake", err)
	}
	fmt.Printf("source   %s\n", src.Describe())

	// ── discovery: read the repository, bounded ──────────────────────────────────────────────────
	corpus, err := discovery.Walk(src.Root, discovery.Limits{})
	if err != nil {
		die("discovery", err)
	}
	isAgent, why := corpus.LooksLikeAnAgent()
	fmt.Printf("read     %d files, %d test files excluded\n", len(corpus.Files), corpus.Skipped["test-file"])
	fmt.Printf("agent    %v — %s\n", isAgent, why)
	if !isAgent {
		fmt.Println("\nRefusing to assess: there is no agent here to assess.")
		os.Exit(1)
	}
	key, err := config.DeepSeekKey()
	if err != nil {
		die("credential", err)
	}
	dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://heros:heros@localhost:55700/heros?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		die("postgres", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := migrations.Apply(ctx, db); err != nil {
		die("migrate", err)
	}
	s := store.NewPostgres(db)
	now := time.Now().UTC()

	g := &goal.Goal{
		ID: goal.ID(fmt.Sprintf("live-%d", now.UnixNano())), Tenant: "acme",
		Intent: intent.Assess, State: goal.Draft,
		Objective: "look at my repository and tell me what is weak",
		Subject:   goal.Subject{RepoURL: src.RemoteURL, Revision: src.Revision},
		Ceilings: bounds.Ceilings{MaxIterations: 40, MaxTasks: 20, MaxAttemptsPerTask: 2,
			MaxToolCalls: 40, MaxTokens: 200_000, MaxCostCents: 500,
			MaxWallClock: 10 * time.Minute, MaxSpawnDepth: 2},
		Criteria:  []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 8}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := g.Admit(now); err != nil {
		die("admit", err)
	}
	if err := s.CreateGoal(g); err != nil {
		die("create", err)
	}
	plans, err := planner.Default()
	if err != nil {
		die("planners", err)
	}
	d, err := plans.Build(g, now)
	if err != nil {
		die("plan", err)
	}
	if err := s.SaveDAG(d); err != nil {
		die("save dag", err)
	}

	client := deepseek.New(key)
	reg := toolcontract.NewRegistry()
	if err := reg.Register(tools.AssessAxis{
		Provider: client, Model: deepseek.ModelFlash,
		Source: discovery.NewIndex(corpus),
	}, nil); err != nil {
		die("register", err)
	}
	// The synthesis has no tool yet, so it will fail when reached. Stated rather than hidden.
	w := worker.New("livecheck", s, reg)
	w.Lease = 2 * time.Minute

	fmt.Printf("goal     %s\nmodel    %s   ceiling $%.2f / %d tokens\n\n",
		g.ID, deepseek.ModelFlash, float64(g.Ceilings.MaxCostCents)/100, g.Ceilings.MaxTokens)

	start := time.Now()
	for i := 0; i < 40; i++ {
		out, err := w.RunOnce(ctx, g.ID)
		if err != nil {
			fmt.Printf("  cycle error: %v\n", err)
			break
		}
		// 🔴 DidWork means "the task reached a terminal state", which includes FAILING. A mark chosen
		// from the outcome alone prints a tick beside an exhausted retry ladder — which is exactly the
		// "nothing left to do read as success" confusion this system keeps having to unlearn.
		mark := "·"
		switch {
		case out.Did == worker.DidRetry:
			mark = "↻"
		case out.Did == worker.DidWork && out.Detail != "":
			mark = "✕"
		case out.Did == worker.DidWork:
			mark = "✓"
		}
		if out.TaskID != "" {
			fmt.Printf("  %s %-22s %s\n", mark, out.TaskID, out.Detail)
		} else if out.Did != worker.DidNothing {
			fmt.Printf("  %s %-22s %s\n", mark, out.Did, out.Detail)
		}
		if !out.More {
			fmt.Printf("\nended: %s — %s\n", out.Did, out.Detail)
			break
		}
	}
	elapsed := time.Since(start)

	final, err := s.LoadGoal(g.ID)
	if err != nil {
		die("reload", err)
	}
	fresh, err := s.LoadDAG(g.ID)
	if err != nil {
		die("reload dag", err)
	}

	fmt.Printf("\n─── what it actually cost ───────────────────────────────\n")
	fmt.Printf("wall clock    %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("model calls   %d\n", final.Spend.ToolCalls)
	fmt.Printf("tokens        %d  (ceiling %d)\n", final.Spend.Tokens, final.Ceilings.MaxTokens)
	fmt.Printf("cost          %d micro-cents = %s  (ceiling $%.2f)\n",
		final.Spend.CostMicroCents, provider.FormatCents(final.Spend.CostMicroCents),
		float64(final.Ceilings.MaxCostCents)/100)
	if final.Spend.ToolCalls > 0 {
		fmt.Printf("per call      %d tokens, %s\n",
			final.Spend.Tokens/int64(final.Spend.ToolCalls),
			provider.FormatCents(final.Spend.CostMicroCents/int64(final.Spend.ToolCalls)))
	}

	fmt.Printf("\n─── findings ────────────────────────────────────────────\n")
	for _, id := range order(fresh) {
		t := fresh.Tasks[id]
		switch t.State {
		case task.Succeeded:
			fmt.Printf("  ✓ %-24s %s\n", t.ID, firstWeakness(t.Result))
		case task.Failed:
			fmt.Printf("  ✕ %-24s %s\n", t.ID, t.Failure)
		case task.Blocked:
			fmt.Printf("  ⊘ %-24s %s\n", t.ID, t.Failure)
		}
	}
}

func order(d *task.DAG) []task.ID {
	out := make([]task.ID, 0, len(d.Tasks))
	for id := range d.Tasks {
		out = append(out, id)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func firstWeakness(b []byte) string {
	var fs []struct {
		Weakness   string `json:"weakness"`
		Actionable bool   `json:"actionable"`
	}
	if len(b) == 0 {
		return "(no finding)"
	}
	if err := json.Unmarshal(b, &fs); err != nil || len(fs) == 0 {
		return "(unparsed)"
	}
	tag := " "
	if fs[0].Actionable {
		tag = "→"
	}
	w := fs[0].Weakness
	if len(w) > 88 {
		w = w[:88] + "…"
	}
	return tag + " " + w
}

func die(what string, err error) { fmt.Printf("%s: %v\n", what, err); os.Exit(1) }
