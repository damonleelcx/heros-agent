// Command livecheck runs one real durable goal end to end against DeepSeek and a real Postgres, and
// prints what it actually cost. It is the answer to "no number in this repo came from a real call".
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/goal"
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

// excerpts are a real-shaped agent: a support bot with the weaknesses the prototype describes.
// 🔴 Fixtures. Subject-repository discovery is not built; see tools.FixtureSource.
var excerpts = map[string]string{
	"model":   "Every node calls model='deepseek-v4-pro', temperature=0.7, including a classifier whose\noutput is one of three fixed labels.",
	"prompt":  "SYSTEM_PROMPT is a 1,400-word string literal in agents/triage.py, edited in place, no versioning.",
	"skills":  "No skills are bound at any call site. Capabilities are inlined into the system prompt as prose.",
	"context": "def build_messages(self, history):\n    return [SYSTEM] + history   # no truncation, no summarisation",
	"tools":   "triage offers 4 tools, calls 4.\nlookup_order offers 6 tools, calls 5 — refund_tool has never been called in 30 days of traces.\nescalate offers 2, calls 2.",
	"memory":  "Nothing persists between sessions. Each conversation starts empty; returning customers re-explain\ntheir order number every time.",
	"harness": "No timeout, no retry, no token ceiling on any call. A hung provider call hangs the request thread.",
	"loop":    "while True:\n    resp = call_model(...)\n    if 'DONE' in resp: break   # no max_turns, no other stop condition",
	// 🔴 `graph` is deliberately absent: the topology is built at runtime, so it cannot be read without
	// executing the customer's code. The axis fails, and saying so is the point.
}

func main() {
	if err := config.LoadDotEnv(".env.local"); err != nil {
		die("dotenv", err)
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
		Subject:   goal.Subject{RepoURL: "git@github.com:acme/support-bot.git", Revision: "rev-a41c9"},
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
		Source: tools.FixtureSource{Excerpts: excerpts}, MaxTokens: 700,
	}, nil); err != nil {
		die("register", err)
	}
	// The synthesis has no tool yet, so it will fail when reached. Stated rather than hidden.
	w := worker.New("livecheck", s, reg)
	w.Lease = 2 * time.Minute

	fmt.Printf("goal   %s\n", g.ID)
	fmt.Printf("model  %s   ceiling $%.2f / %d tokens\n\n",
		deepseek.ModelFlash, float64(g.Ceilings.MaxCostCents)/100, g.Ceilings.MaxTokens)

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
