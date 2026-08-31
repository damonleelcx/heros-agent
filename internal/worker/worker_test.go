package worker_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/worker"
)

// ── doubles ──────────────────────────────────────────────────────────────────────────────────────
//
// 🔴 These are FAKES of the boundary, not of the code under test. The worker, the store, the DAG and
// the tool contract are all real here; only the thing on the far side of the tool boundary — the model,
// the repository — is substituted, because that is the actual external dependency.

type fakeTool struct {
	spec  toolcontract.Spec
	calls int
	fn    func(c toolcontract.Call) (toolcontract.Result, error)
}

func (f *fakeTool) Spec() toolcontract.Spec { return f.spec }
func (f *fakeTool) Execute(_ context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	f.calls++
	if f.fn != nil {
		return f.fn(c)
	}
	return toolcontract.Result{Output: []byte("ok"), Tokens: 10, CostMicroCents: 12_700, ToolCalls: 1}, nil
}

type fakeVerifier struct {
	fn func(c toolcontract.Call, r toolcontract.Result) (bool, string, error)
}

func (f *fakeVerifier) Verify(_ context.Context, c toolcontract.Call, r toolcontract.Result) (bool, string, error) {
	if f.fn != nil {
		return f.fn(c, r)
	}
	return true, "", nil
}

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// ── harness ──────────────────────────────────────────────────────────────────────────────────────

func ceilings() bounds.Ceilings {
	return bounds.Ceilings{MaxIterations: 20, MaxTasks: 50, MaxAttemptsPerTask: 3, MaxToolCalls: 100,
		MaxTokens: 1e6, MaxCostCents: 500, MaxWallClock: time.Hour, MaxSpawnDepth: 3}
}

type harness struct {
	s     store.Store
	w     *worker.Worker
	clock *fixedClock
	g     *goal.Goal
	id    goal.ID
}

func setup(t *testing.T, s store.Store, tool *fakeTool, v toolcontract.Verifier, tasks ...*task.Task) *harness {
	t.Helper()
	clock := &fixedClock{t: time.Now().UTC().Truncate(time.Millisecond)}
	id := goal.ID(fmt.Sprintf("wg-%d", time.Now().UnixNano()))
	g := &goal.Goal{
		ID: id, Tenant: "t1", Intent: intent.Assess, State: goal.Draft,
		Subject:   goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc"},
		Ceilings:  ceilings(),
		Criteria:  []goal.Criterion{{Kind: goal.AllTasksSucceeded, Threshold: 1}},
		CreatedAt: clock.t, UpdatedAt: clock.t,
	}
	if err := g.Admit(clock.t); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, tk := range tasks {
		tk.GoalID, tk.CreatedAt, tk.UpdatedAt = string(id), clock.t, clock.t
	}
	d, err := task.NewDAG(string(id), tasks)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	if err := s.SaveDAG(d); err != nil {
		t.Fatalf("save dag: %v", err)
	}
	reg := toolcontract.NewRegistry()
	if err := reg.Register(tool, v); err != nil {
		t.Fatalf("register: %v", err)
	}
	w := worker.New("worker-1", s, reg)
	w.Clock = clock
	w.Lease = time.Minute
	return &harness{s: s, w: w, clock: clock, g: g, id: id}
}

func analyse(id string, deps ...task.ID) *task.Task {
	return &task.Task{ID: task.ID(id), Kind: "analyse", State: task.Pending, DependsOn: deps}
}

func readOnlyTool() *fakeTool {
	return &fakeTool{spec: toolcontract.Spec{Kind: "analyse", Timeout: time.Second, RetrySafe: true}}
}

// ── the registry refuses what cannot be operated safely ──────────────────────────────────────────

func TestTheRegistryRefusesUnsafeTools(t *testing.T) {
	reg := toolcontract.NewRegistry()
	noTimeout := &fakeTool{spec: toolcontract.Spec{Kind: "x"}}
	if err := reg.Register(noTimeout, nil); !errors.Is(err, toolcontract.ErrNoTimeout) {
		t.Errorf("a tool with no timeout was admitted; it would hang a worker until its lease expires "+
			"and look exactly like a crash: %v", err)
	}
	effectNoVerifier := &fakeTool{spec: toolcontract.Spec{Kind: "open_pull_request",
		Timeout: time.Second, EffectBearing: true,
		Permissions: []toolcontract.Permission{toolcontract.WriteRemoteRepo}}}
	if err := reg.Register(effectNoVerifier, nil); !errors.Is(err, toolcontract.ErrNoVerifier) {
		t.Errorf("an effect-bearing tool with no verifier was admitted; it would be trusted on its own "+
			"word about whether it changed the world: %v", err)
	}
	effectNoPerms := &fakeTool{spec: toolcontract.Spec{Kind: "write_source",
		Timeout: time.Second, EffectBearing: true}}
	if err := reg.Register(effectNoPerms, &fakeVerifier{}); !errors.Is(err, toolcontract.ErrNoPermissions) {
		t.Errorf("an effect-bearing tool with no declared permissions was admitted; its blast radius "+
			"is unreadable: %v", err)
	}
}

// ── the loop ─────────────────────────────────────────────────────────────────────────────────────

// TestOneCycleDoesOneBoundedUnitOfWork.
func TestOneCycleDoesOneBoundedUnitOfWork(t *testing.T) {
	tool := readOnlyTool()
	h := setup(t, store.NewMemory(), tool, nil, analyse("a"), analyse("b", "a"))

	out, err := h.w.RunOnce(context.Background(), h.id)
	if err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if out.Did != worker.DidWork || out.TaskID != "a" {
		t.Fatalf("cycle 1 = %+v, want work on a", out)
	}
	if tool.calls != 1 {
		t.Fatalf("one cycle invoked the tool %d times; a cycle is ONE unit of work", tool.calls)
	}
	out, _ = h.w.RunOnce(context.Background(), h.id)
	if out.Did != worker.DidWork || out.TaskID != "b" {
		t.Fatalf("cycle 2 = %+v, want work on b", out)
	}
	out, _ = h.w.RunOnce(context.Background(), h.id)
	if out.Did != worker.DidComplete {
		t.Fatalf("cycle 3 = %+v, want complete", out)
	}
	if out.More {
		t.Error("a completed goal reported there is more to do")
	}
}

// TestATtoolThatSucceedsButCannotBeConfirmedIsNotASuccess.
//
// 🔴 The single most important test in this package. "Opened a pull request" returning cleanly while
// the pull request does not exist is what a timeout on the response leg of a successful write looks
// like from the caller's side — every time.
func TestAToolThatSucceedsButCannotBeConfirmedIsNotASuccess(t *testing.T) {
	tool := readOnlyTool()
	denier := &fakeVerifier{fn: func(toolcontract.Call, toolcontract.Result) (bool, string, error) {
		return false, "the pull request does not exist", nil
	}}
	h := setup(t, store.NewMemory(), tool, denier, analyse("a"))

	// Attempts 1..3 all "succeed" at the tool and all fail verification.
	var last worker.Outcome
	for i := 0; i < 4; i++ {
		last, _ = h.w.RunOnce(context.Background(), h.id)
	}
	d, err := h.s.LoadDAG(h.id)
	if err != nil {
		t.Fatalf("load dag: %v", err)
	}
	if got := d.Tasks["a"].State; got != task.Failed {
		t.Fatalf("task is %s; a tool returning nil error is NOT evidence the world changed", got)
	}
	if last.Did == worker.DidComplete {
		t.Fatal("the goal completed on unverified work")
	}
}

// TestAnUnconfirmableEffectIsNotRetried. The effect may have landed; a blind retry duplicates it.
func TestAnUnconfirmableEffectIsNotRetried(t *testing.T) {
	tool := readOnlyTool()
	inconclusive := &fakeVerifier{fn: func(toolcontract.Call, toolcontract.Result) (bool, string, error) {
		return false, "", errors.New("the repository host is unreachable")
	}}
	h := setup(t, store.NewMemory(), tool, inconclusive, analyse("a"))

	out, _ := h.w.RunOnce(context.Background(), h.id)
	if out.Did != worker.DidWork {
		t.Fatalf("got %+v; an unconfirmable effect must fail terminally, not retry", out)
	}
	d, _ := h.s.LoadDAG(h.id)
	if d.Tasks["a"].State != task.Failed {
		t.Fatalf("task is %s, want failed on the first inconclusive verification", d.Tasks["a"].State)
	}
	if tool.calls != 1 {
		t.Fatalf("the tool ran %d times; an effect that may have landed must not be repeated", tool.calls)
	}
}

// TestTheRetryLadderIsBoundedAndThenTerminal.
func TestTheRetryLadderIsBoundedAndThenTerminal(t *testing.T) {
	tool := readOnlyTool()
	tool.fn = func(toolcontract.Call) (toolcontract.Result, error) {
		return toolcontract.Result{Tokens: 5, CostMicroCents: 5_000, ToolCalls: 1}, errors.New("transient")
	}
	h := setup(t, store.NewMemory(), tool, nil, analyse("a"))

	for i := 1; i <= 2; i++ {
		out, _ := h.w.RunOnce(context.Background(), h.id)
		if out.Did != worker.DidRetry {
			t.Fatalf("attempt %d = %+v, want retry", i, out)
		}
	}
	out, _ := h.w.RunOnce(context.Background(), h.id)
	if out.Did != worker.DidWork {
		t.Fatalf("attempt 3 = %+v, want terminal failure", out)
	}
	d, _ := h.s.LoadDAG(h.id)
	if d.Tasks["a"].State != task.Failed {
		t.Fatalf("task is %s after exhausting the ladder", d.Tasks["a"].State)
	}
	if tool.calls != 3 {
		t.Fatalf("the tool ran %d times; MaxAttemptsPerTask is 3", tool.calls)
	}
}

// TestAFailedAttemptStillCostsMoney. A ceiling that only counts successes is one a failing loop walks
// straight through.
func TestAFailedAttemptStillCostsMoney(t *testing.T) {
	tool := readOnlyTool()
	tool.fn = func(toolcontract.Call) (toolcontract.Result, error) {
		return toolcontract.Result{Tokens: 100, CostMicroCents: 7_000, ToolCalls: 1}, errors.New("nope")
	}
	h := setup(t, store.NewMemory(), tool, nil, analyse("a"))
	if _, err := h.w.RunOnce(context.Background(), h.id); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	g, _ := h.s.LoadGoal(h.id)
	if g.Spend.CostMicroCents != 7_000 || g.Spend.Tokens != 100 {
		t.Fatalf("spend after a FAILED attempt = %+v; a failed call still cost money", g.Spend)
	}
}

// TestACeilingStopsTheWorkerBeforeItClaims. Starting work that cannot be paid for spends the budget
// discovering that it cannot be paid for.
func TestACeilingStopsTheWorkerBeforeItClaims(t *testing.T) {
	tool := readOnlyTool()
	h := setup(t, store.NewMemory(), tool, nil, analyse("a"))
	g, _ := h.s.LoadGoal(h.id)
	g.Spend.CostMicroCents = g.Ceilings.MaxCostCents * bounds.MicroCentsPerCent
	if err := h.s.SaveGoal(g); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, _ := h.w.RunOnce(context.Background(), h.id)
	if out.Did != worker.DidStop {
		t.Fatalf("got %+v, want stop", out)
	}
	if tool.calls != 0 {
		t.Fatal("the worker spent money past its ceiling")
	}
	after, _ := h.s.LoadGoal(h.id)
	if after.State != goal.Failed || after.Refusal == nil {
		t.Fatal("the goal did not record why it stopped")
	}
	if after.Refusal.NextAction() == "" {
		t.Error("a stopped goal must tell the operator what to do next")
	}
}

// TestAnApprovalGateParksTheTaskWithoutHoldingALease. A person may take a week.
func TestAnApprovalGateParksTheTaskWithoutHoldingALease(t *testing.T) {
	tool := &fakeTool{spec: toolcontract.Spec{Kind: "open_pull_request", Timeout: time.Second,
		EffectBearing: true, Permissions: []toolcontract.Permission{toolcontract.WriteRemoteRepo}}}
	gated := &task.Task{ID: "pr", Kind: "open_pull_request", State: task.Pending, IdempotencyKey: "k1"}
	h := setup(t, store.NewMemory(), tool, &fakeVerifier{}, gated)

	out, err := h.w.RunOnce(context.Background(), h.id)
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if out.Did != worker.DidAwaitApproval {
		t.Fatalf("got %+v; an effect outside the platform must wait for a person", out)
	}
	if tool.calls != 0 {
		t.Fatal("the effect happened BEFORE the approval, which is the whole failure the gate prevents")
	}
	d, _ := h.s.LoadDAG(h.id)
	tk := d.Tasks["pr"]
	if tk.State != task.AwaitingApproval {
		t.Fatalf("task is %s, want awaiting_approval", tk.State)
	}
	if tk.LeasedBy != "" || !tk.LeaseExpiry.IsZero() {
		t.Fatalf("a task waiting on a human still holds a lease (%q until %s); a week-long lease is "+
			"indistinguishable from a hung worker", tk.LeasedBy, tk.LeaseExpiry)
	}
}

// TestAnEffectBearingTaskWithoutAnIdempotencyKeyNeverReachesATool.
func TestAnEffectBearingTaskWithoutAnIdempotencyKeyNeverReachesATool(t *testing.T) {
	tool := &fakeTool{spec: toolcontract.Spec{Kind: "open_pull_request", Timeout: time.Second,
		EffectBearing: true, Permissions: []toolcontract.Permission{toolcontract.WriteRemoteRepo}}}
	// Bypass SaveDAG's check by writing straight to a memory store's DAG.
	unkeyed := &task.Task{ID: "pr", Kind: "open_pull_request", State: task.Pending}
	h := setup(t, store.NewMemory(), tool, &fakeVerifier{}, unkeyed)
	h.w.Policy = allowAll{}

	out, _ := h.w.RunOnce(context.Background(), h.id)
	if tool.calls != 0 {
		t.Fatal("a retryable effect with no idempotency key reached a tool; a retry would duplicate it")
	}
	d, _ := h.s.LoadDAG(h.id)
	if d.Tasks["pr"].State != task.Failed {
		t.Fatalf("task is %s, want failed; got outcome %+v", d.Tasks["pr"].State, out)
	}
}

type allowAll struct{}

func (allowAll) NeedsApproval(*goal.Goal, *task.Task) (bool, string) { return false, "" }

// TestCancellationReleasesTheLease. Abandoning it makes a clean shutdown look exactly like a crash and
// delays every task the shutdown meant to hand over.
func TestCancellationReleasesTheLease(t *testing.T) {
	tool := readOnlyTool()
	ctx, cancel := context.WithCancel(context.Background())
	tool.fn = func(toolcontract.Call) (toolcontract.Result, error) {
		cancel() // the shutdown arrives mid-execution
		return toolcontract.Result{Output: []byte("ok"), ToolCalls: 1}, nil
	}
	h := setup(t, store.NewMemory(), tool, nil, analyse("a"))

	out, err := h.w.RunOnce(ctx, h.id)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if out.Did != worker.DidRetry {
		t.Fatalf("got %+v, want retry", out)
	}
	d, _ := h.s.LoadDAG(h.id)
	tk := d.Tasks["a"]
	if tk.LeasedBy != "" {
		t.Fatalf("the lease was abandoned rather than released (held by %q)", tk.LeasedBy)
	}
	if tk.State != task.Ready {
		t.Fatalf("task is %s; a cleanly cancelled task must be immediately runnable", tk.State)
	}
}

// TestAStallIsDetectedRatherThanWaitedOut.
func TestAStallIsDetectedRatherThanWaitedOut(t *testing.T) {
	tool := readOnlyTool()
	tool.fn = func(toolcontract.Call) (toolcontract.Result, error) {
		return toolcontract.Result{ToolCalls: 1}, errors.New("always fails")
	}
	h := setup(t, store.NewMemory(), tool, nil, analyse("a"), analyse("b", "a"))
	h.g.Criteria = []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 2}}
	_ = h.s.SaveGoal(h.g)

	var out worker.Outcome
	for i := 0; i < 6 && out.Did != worker.DidStall; i++ {
		out, _ = h.w.RunOnce(context.Background(), h.id)
	}
	if out.Did != worker.DidStall {
		t.Fatalf("final outcome %+v; a goal whose remaining work can never run must be detected, not "+
			"left to burn its wall-clock ceiling looking busy", out)
	}
	if out.More {
		t.Error("a stalled goal reported there is more to do")
	}
}

// ── crash recovery, against a real Postgres ──────────────────────────────────────────────────────

// TestACrashedWorkerIsRecoveredByAnother is the recovery drill: kill a worker mid-task by simply never
// calling it again, and assert a second worker picks the task up once the lease expires.
//
// 🔴 Postgres-only, because the property being tested is that recovery works from PERSISTED state. An
// in-memory store proves nothing here: the "crashed" worker's state never left the process.
func TestACrashedWorkerIsRecoveredByAnother(t *testing.T) {
	dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HEROS_TEST_DATABASE_URL unset; the recovery drill did not run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := migrations.Apply(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.NewPostgres(db)

	// Worker A claims, then "crashes": its tool blocks, and we never complete the task.
	crashed := readOnlyTool()
	crashed.fn = func(toolcontract.Call) (toolcontract.Result, error) {
		return toolcontract.Result{}, errors.New("process died")
	}
	h := setup(t, s, crashed, nil, analyse("a"))

	if _, err := s.Claim(h.id, "worker-A", time.Minute, h.clock.Now()); err != nil {
		t.Fatalf("worker A claim: %v", err)
	}
	// Nothing sweeps. Time simply passes, exactly as it would while a machine is being replaced.
	h.clock.advance(2 * time.Minute)

	recovered := readOnlyTool()
	regB := toolcontract.NewRegistry()
	if err := regB.Register(recovered, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	wB := worker.New("worker-B", s, regB)
	wB.Clock = h.clock
	wB.Lease = time.Minute

	out, err := wB.RunOnce(context.Background(), h.id)
	if err != nil {
		t.Fatalf("worker B: %v", err)
	}
	if out.Did != worker.DidWork || out.TaskID != "a" {
		t.Fatalf("worker B got %+v; a crashed worker's task must be recovered from persisted state", out)
	}
	d, err := s.LoadDAG(h.id)
	if err != nil {
		t.Fatalf("load dag: %v", err)
	}
	tk := d.Tasks["a"]
	if tk.State != task.Succeeded {
		t.Fatalf("task is %s after recovery", tk.State)
	}
	if tk.Attempt != 2 {
		t.Errorf("attempt = %d, want 2: the crashed attempt must count, or a task that kills every "+
			"worker it touches retries forever", tk.Attempt)
	}
}
