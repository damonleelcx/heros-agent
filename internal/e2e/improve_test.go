// Package e2e drives the whole system the way a deployment does: a real Postgres, the real planner, the
// real worker, the real store. Only the far side of the tool boundary is substituted.
//
// # Why this exists in addition to the unit tests
//
// Every component below is already tested in isolation and every one of those tests passes. That proves
// each part works; it does not prove they COMPOSE. The specific thing asserted here is an ordering that
// no single package can see: an `improve` goal's first plan is an assessment, so at the moment the
// assessment finishes every task is terminal and "all tasks succeeded" is true. A system that checks
// completion before replanning declares victory exactly then, and ships an empty improvement run
// reported as a success.
package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/worker"
)

// ── the substituted outside world ────────────────────────────────────────────────────────────────

// world stands in for the model and the repository host. It records what was actually done to it, which
// is what the assertions read — an assertion against the worker's own report would be the worker
// grading itself.
type world struct {
	assessed map[string]int
	proposed int
	verified int
	pullReqs map[string]int // keyed by idempotency key, so a duplicate is visible as a count of 2
}

func newWorld() *world {
	return &world{assessed: map[string]int{}, pullReqs: map[string]int{}}
}

type toolFn struct {
	spec toolcontract.Spec
	fn   func(toolcontract.Call) (toolcontract.Result, error)
}

func (t toolFn) Spec() toolcontract.Spec { return t.spec }
func (t toolFn) Execute(_ context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	return t.fn(c)
}

type verifyFn func(toolcontract.Call, toolcontract.Result) (bool, string, error)

func (v verifyFn) Verify(_ context.Context, c toolcontract.Call, r toolcontract.Result) (bool, string, error) {
	return v(c, r)
}

func spend() toolcontract.Result {
	return toolcontract.Result{Tokens: 100, CostMicroCents: 12_700, ToolCalls: 1}
}

// registry wires one tool per kind the improve plan emits.
func registry(t *testing.T, w *world) *toolcontract.Registry {
	t.Helper()
	r := toolcontract.NewRegistry()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	// Assessing an axis: memory and tools each report one actionable finding; the rest report none.
	must(r.Register(toolFn{
		spec: toolcontract.Spec{Kind: planner.KindAssessAxis, Timeout: time.Second, RetrySafe: true},
		fn: func(c toolcontract.Call) (toolcontract.Result, error) {
			axis := c.TaskID[len("assess-"):]
			w.assessed[axis]++
			res := spend()
			var out []planner.Finding
			if axis == "memory" || axis == "tools" {
				out = []planner.Finding{{Axis: axis, Weakness: "weak " + axis, Actionable: true}}
			}
			res.Output, _ = json.Marshal(out)
			return res, nil
		}}, nil))

	must(r.Register(toolFn{
		spec: toolcontract.Spec{Kind: planner.KindSynthesise, Timeout: time.Second, RetrySafe: true},
		fn: func(toolcontract.Call) (toolcontract.Result, error) {
			res := spend()
			res.Output = []byte(`{"summary":"two weak axes"}`)
			return res, nil
		}}, nil))

	must(r.Register(toolFn{
		spec: toolcontract.Spec{Kind: planner.KindProposeChange, Timeout: time.Second, RetrySafe: true},
		fn: func(toolcontract.Call) (toolcontract.Result, error) {
			w.proposed++
			res := spend()
			res.Output = []byte(`{"diff":"--- a\n+++ b\n"}`)
			return res, nil
		}}, nil))

	must(r.Register(toolFn{
		spec: toolcontract.Spec{Kind: planner.KindVerifyProposal, Timeout: time.Second, RetrySafe: true},
		fn: func(toolcontract.Call) (toolcontract.Result, error) {
			w.verified++
			res := spend()
			res.Output = []byte(`{"passes":true}`)
			return res, nil
		}}, nil))

	// The only effect-bearing tool. Keyed by idempotency key so a duplicate is COUNTABLE rather than
	// merely improbable.
	must(r.Register(toolFn{
		spec: toolcontract.Spec{Kind: planner.KindOpenPR, Timeout: time.Second, EffectBearing: true,
			Permissions: []toolcontract.Permission{toolcontract.WriteRemoteRepo}},
		fn: func(c toolcontract.Call) (toolcontract.Result, error) {
			w.pullReqs[c.IdempotencyKey]++
			res := spend()
			res.Output = []byte(`{"url":"https://example.invalid/pr/1"}`)
			return res, nil
		}},
		verifyFn(func(c toolcontract.Call, _ toolcontract.Result) (bool, string, error) {
			if w.pullReqs[c.IdempotencyKey] > 0 {
				return true, "", nil
			}
			return false, "no pull request exists for this key", nil
		})))

	return r
}

// ── harness ──────────────────────────────────────────────────────────────────────────────────────

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HEROS_TEST_DATABASE_URL unset; the end-to-end run did not execute")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrations.Apply(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func improveGoal(now time.Time) *goal.Goal {
	g := &goal.Goal{
		ID: goal.ID(fmt.Sprintf("e2e-%d", time.Now().UnixNano())), Tenant: "acme",
		Intent: intent.Improve, State: goal.Draft,
		Objective: "fix what is weak and open a pull request",
		Subject:   goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "rev-abc"},
		Ceilings: bounds.Ceilings{MaxIterations: 200, MaxTasks: 60, MaxAttemptsPerTask: 3,
			MaxToolCalls: 500, MaxTokens: 1e7, MaxCostCents: 10_000,
			MaxWallClock: time.Hour, MaxSpawnDepth: 3},
		Criteria:  []goal.Criterion{{Kind: goal.AllTasksSucceeded, Threshold: 1}},
		CreatedAt: now, UpdatedAt: now,
	}
	return g
}

// drive runs cycles until the outcome stops saying there is more to do, or the cap is hit.
func drive(t *testing.T, w *worker.Worker, id goal.ID, cap int) []worker.Outcome {
	t.Helper()
	var seen []worker.Outcome
	for i := 0; i < cap; i++ {
		out, err := w.RunOnce(context.Background(), id)
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		seen = append(seen, out)
		if !out.More {
			return seen
		}
	}
	t.Fatalf("the run did not terminate within %d cycles; the last outcome was %+v", cap, seen[len(seen)-1])
	return seen
}

// ── the run ──────────────────────────────────────────────────────────────────────────────────────

// TestImproveRunsEndToEndAndStopsAtTheApprovalGate.
func TestImproveRunsEndToEndAndStopsAtTheApprovalGate(t *testing.T) {
	db := openDB(t)
	s := store.NewPostgres(db)
	now := time.Now().UTC().Truncate(time.Millisecond)

	g := improveGoal(now)
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	plans, err := planner.Default()
	if err != nil {
		t.Fatalf("planners: %v", err)
	}
	d, err := plans.Build(g, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := s.SaveDAG(d); err != nil {
		t.Fatalf("save dag: %v", err)
	}

	wrld := newWorld()
	w := worker.New("e2e-worker", s, registry(t, wrld))
	w.Clock = &clock{t: now}
	w.Reviser = plans
	w.Lease = time.Minute

	outcomes := drive(t, w, g.ID, 100)
	if last := outcomes[len(outcomes)-1]; last.Did != worker.DidBlockedOnApproval {
		t.Fatalf("the run ended on %+v, want blocked_on_approval", last)
	}

	// ── what the OUTSIDE WORLD saw ───────────────────────────────────────────────────────────────
	if len(wrld.assessed) != len(intent.Axes()) {
		t.Errorf("assessed %d axes, want all %d", len(wrld.assessed), len(intent.Axes()))
	}
	for axis, n := range wrld.assessed {
		if n != 1 {
			t.Errorf("axis %q was assessed %d times; each axis is one unit of work", axis, n)
		}
	}
	if wrld.proposed != 2 {
		t.Errorf("proposed %d changes, want 2 — one per ACTIONABLE finding, not one per axis", wrld.proposed)
	}
	if wrld.verified != 2 {
		t.Errorf("verified %d proposals, want 2", wrld.verified)
	}

	// 🔴 The central assertion: the pull requests did NOT happen. They are gated on a person, and this
	// run had nobody to ask.
	if len(wrld.pullReqs) != 0 {
		t.Fatalf("%d pull request(s) were opened without approval: %v", len(wrld.pullReqs), wrld.pullReqs)
	}

	// ── that replanning actually occurred ────────────────────────────────────────────────────────
	var replans, approvals int
	for _, o := range outcomes {
		switch o.Did {
		case worker.DidReplan:
			replans++
		case worker.DidAwaitApproval:
			approvals++
		}
	}
	if replans == 0 {
		t.Fatal("no replanning happened: the improve goal would have completed on its assessment alone, " +
			"shipping an empty improvement run reported as a success")
	}
	if approvals != 2 {
		t.Errorf("%d tasks parked for approval, want 2", approvals)
	}

	// ── what is actually IN the database ─────────────────────────────────────────────────────────
	//
	// Read back rather than trusting the in-process objects: HTTP 200 is not evidence of a write, and
	// neither is a struct field.
	fresh, err := s.LoadDAG(g.ID)
	if err != nil {
		t.Fatalf("reload dag: %v", err)
	}
	if len(fresh.Tasks) != len(intent.Axes())+1+6 {
		t.Errorf("%d tasks persisted; want %d axes + synthesis + two 3-task chains",
			len(fresh.Tasks), len(intent.Axes()))
	}
	var awaiting int
	for _, tk := range fresh.Tasks {
		if tk.State == task.AwaitingApproval {
			awaiting++
			if tk.LeasedBy != "" {
				t.Errorf("%s waits on a human and still holds a lease", tk.ID)
			}
			if tk.IdempotencyKey == "" {
				t.Errorf("%s is effect-bearing and carries no idempotency key", tk.ID)
			}
		}
	}
	if awaiting != 2 {
		t.Errorf("%d tasks await approval in the database, want 2", awaiting)
	}

	// Spend was recorded and is under the ceiling.
	after, err := s.LoadGoal(g.ID)
	if err != nil {
		t.Fatalf("reload goal: %v", err)
	}
	if after.Spend.CostMicroCents == 0 || after.Spend.Tokens == 0 {
		t.Error("the run cost nothing, which means spend was never recorded")
	}
	if which, hit := after.Spend.Exceeded(after.Ceilings); hit {
		t.Errorf("the run exceeded %s", which)
	}
}

// TestTheRunResumesAfterTheProcessDies.
//
// 🔴 The crash is modelled by DISCARDING the worker and everything it held, then building a new one from
// nothing but the database. That is what a restart is. If any state the run needs lived in the first
// worker, the second cannot finish — and that is precisely the failure "never rely on model context as
// memory" exists to prevent.
func TestTheRunResumesAfterTheProcessDies(t *testing.T) {
	db := openDB(t)
	s := store.NewPostgres(db)
	now := time.Now().UTC().Truncate(time.Millisecond)

	g := improveGoal(now)
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	plans, _ := planner.Default()
	d, _ := plans.Build(g, now)
	if err := s.SaveDAG(d); err != nil {
		t.Fatalf("save dag: %v", err)
	}

	// ── process 1: run three cycles, then die ────────────────────────────────────────────────────
	w1 := newWorld()
	p1 := worker.New("worker-1", s, registry(t, w1))
	p1.Clock = &clock{t: now}
	p1.Reviser = plans
	p1.Lease = time.Minute
	for i := 0; i < 3; i++ {
		if _, err := p1.RunOnce(context.Background(), g.ID); err != nil {
			t.Fatalf("process 1 cycle %d: %v", i, err)
		}
	}
	mid, err := s.LoadDAG(g.ID)
	if err != nil {
		t.Fatalf("mid load: %v", err)
	}
	doneMid, _ := mid.Progress()
	if doneMid == 0 {
		t.Fatal("process 1 made no progress, so the resume proves nothing")
	}

	// ── process 1 is gone. Nothing survives except the database. ─────────────────────────────────
	w2 := newWorld()
	p2 := worker.New("worker-2", s, registry(t, w2))
	// A later clock, so any lease process 1 held has expired exactly as it would during a restart.
	p2.Clock = &clock{t: now.Add(5 * time.Minute)}
	p2.Reviser = plans
	p2.Lease = time.Minute

	outcomes := drive(t, p2, g.ID, 100)
	// The correct ending for this run is "waiting for a person": the two pull requests are gated, and no
	// amount of further polling changes that. Completing or stalling here would both be wrong.
	last := outcomes[len(outcomes)-1]
	if last.Did != worker.DidBlockedOnApproval {
		t.Fatalf("the resumed run ended on %+v, want blocked_on_approval", last)
	}
	if last.More {
		t.Error("a run waiting on a human reported there is more for the WORKER to do; it will be " +
			"polled forever and read as a busy, healthy goal")
	}

	// 🔴 The second process must not REDO the assessment work the first one finished. If it does, the
	// resume is a restart, and every long run costs its full price again on every deployment.
	total := 0
	for _, n := range w2.assessed {
		total += n
	}
	if total >= len(intent.Axes()) {
		t.Errorf("process 2 assessed %d axes; process 1 had already finished %d tasks, so a resume "+
			"that redoes them is a restart wearing a resume's name", total, doneMid)
	}

	// The run still reached the approval gate, and still opened nothing.
	if len(w1.pullReqs)+len(w2.pullReqs) != 0 {
		t.Error("a pull request was opened across the restart without approval")
	}
	fresh, _ := s.LoadDAG(g.ID)
	awaiting := 0
	for _, tk := range fresh.Tasks {
		if tk.State == task.AwaitingApproval {
			awaiting++
		}
	}
	if awaiting != 2 {
		t.Errorf("%d tasks await approval after the resume, want 2", awaiting)
	}
}
