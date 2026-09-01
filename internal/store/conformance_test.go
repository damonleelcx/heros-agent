package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
)

// # Why this suite runs against BOTH implementations
//
// Memory is what tests and local runs use; Postgres is what production uses. Two implementations of one
// interface diverge silently unless something asserts they behave the same — and the divergence always
// surfaces in production, because that is the implementation nobody exercised. So every behavioural
// guarantee is written ONCE here and run twice.
//
// 🔴 The Postgres leg SKIPS when HEROS_TEST_DATABASE_URL is unset, and a skip is NOT a pass. The
// `TestPostgresLegActuallyRan` fence at the bottom fails when the variable is set but no Postgres test
// executed, so a broken connection cannot masquerade as a green suite.

func newMemory(t *testing.T) store.Store { t.Helper(); return store.NewMemory() }

func newPostgres(t *testing.T) store.Store {
	t.Helper()
	dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HEROS_TEST_DATABASE_URL is unset; the Postgres leg did not run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := migrations.Apply(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Each test gets its own goal id, so a shared database does not make tests interfere. Truncating
	// would be worse: it would make two packages running concurrently delete each other's rows.
	return store.NewPostgres(db)
}

type impl struct {
	name string
	open func(*testing.T) store.Store
}

func implementations() []impl {
	return []impl{{"memory", newMemory}, {"postgres", newPostgres}}
}

var goalSeq struct {
	sync.Mutex
	n int
}

func uniqueGoalID(prefix string) goal.ID {
	goalSeq.Lock()
	defer goalSeq.Unlock()
	goalSeq.n++
	return goal.ID(fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), goalSeq.n))
}

func ceilings() bounds.Ceilings {
	return bounds.Ceilings{MaxIterations: 10, MaxTasks: 50, MaxAttemptsPerTask: 3, MaxToolCalls: 100,
		MaxTokens: 1e6, MaxCostCents: 500, MaxWallClock: time.Hour, MaxSpawnDepth: 3}
}

// seed creates an admitted goal with the named tasks, and returns its id.
func seed(t *testing.T, s store.Store, prefix string, tasks ...*task.Task) (goal.ID, *goal.Goal) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := uniqueGoalID(prefix)
	g := &goal.Goal{
		ID: id, Tenant: "t1", Intent: intent.Assess, State: goal.Draft,
		Objective: "assess",
		Subject:   goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc123"},
		Ceilings:  ceilings(),
		Criteria:  []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 9}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	for _, tk := range tasks {
		tk.GoalID, tk.CreatedAt, tk.UpdatedAt = string(id), now, now
	}
	if len(tasks) > 0 {
		d, err := task.NewDAG(string(id), tasks)
		if err != nil {
			t.Fatalf("dag: %v", err)
		}
		if err := s.SaveDAG(d); err != nil {
			t.Fatalf("save dag: %v", err)
		}
	}
	return id, g
}

func pending(id, kind string, deps ...task.ID) *task.Task {
	return &task.Task{ID: task.ID(id), Kind: kind, State: task.Pending, DependsOn: deps}
}

// ── the shared suite ─────────────────────────────────────────────────────────────────────────────

// TestGoalRoundTrips — the most basic durability claim. If the ceilings do not survive the round trip,
// a resumed goal runs unbounded.
func TestGoalRoundTrips(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, orig := seed(t, s, "roundtrip")
			got, err := s.LoadGoal(id)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.Ceilings != orig.Ceilings {
				t.Fatalf("ceilings did not survive the round trip:\n got %+v\nwant %+v",
					got.Ceilings, orig.Ceilings)
			}
			if got.State != goal.Running || got.Intent != intent.Assess {
				t.Fatalf("state/intent lost: %s %s", got.State, got.Intent)
			}
			if len(got.Criteria) != 1 || got.Criteria[0].Kind != goal.AxesAssessed {
				t.Fatal("completion criteria lost; nothing could decide when this goal is done")
			}
			if _, err := s.LoadGoal("nope-does-not-exist"); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("missing goal: %v", err)
			}
		})
	}
}

// TestExactlyOneWorkerWinsATask — the property the lease exists for, asserted on both implementations.
func TestExactlyOneWorkerWinsATask(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "onewinner", pending("only", "analyse"))
			now := time.Now().UTC()

			const workers = 24
			var wg sync.WaitGroup
			var mu sync.Mutex
			won := 0
			wg.Add(workers)
			for i := 0; i < workers; i++ {
				go func(i int) {
					defer wg.Done()
					_, err := s.Claim(id, fmt.Sprintf("w%d", i), time.Minute, now)
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == nil:
						won++
					case errors.Is(err, store.ErrNoWork):
					default:
						t.Errorf("claim: %v", err)
					}
				}(i)
			}
			wg.Wait()
			if won != 1 {
				t.Fatalf("%d workers claimed one task; exactly 1 must win", won)
			}
		})
	}
}

// TestAnExpiredLeaseIsReclaimable — the crashed-worker recovery path. Without it a dead worker takes
// its task to the grave and the goal stalls forever while reporting work in flight.
func TestAnExpiredLeaseIsReclaimable(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "reclaim", pending("t1", "analyse"))
			now := time.Now().UTC()

			first, err := s.Claim(id, "worker-a", time.Minute, now)
			if err != nil {
				t.Fatalf("first claim: %v", err)
			}
			if _, err := s.Claim(id, "worker-b", time.Minute, now); !errors.Is(err, store.ErrNoWork) {
				t.Fatal("a live lease was ignored")
			}
			// worker-a dies. Nothing sweeps. Time simply passes.
			later := now.Add(2 * time.Minute)
			second, err := s.Claim(id, "worker-b", time.Minute, later)
			if err != nil {
				t.Fatalf("expired lease not reclaimable — a dead worker stalls the goal forever: %v", err)
			}
			if second.ID != first.ID {
				t.Fatalf("reclaimed %q, want %q", second.ID, first.ID)
			}
			if second.Attempt != 2 {
				t.Errorf("attempt=%d want 2: a reclaim must count against the retry ladder, or a task "+
					"that kills its worker every time retries forever", second.Attempt)
			}
		})
	}
}

// TestAZombieWorkerCannotWriteItsResult — its task was handed to somebody else; letting it write would
// overwrite live work with stale work.
func TestAZombieWorkerCannotWriteItsResult(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "zombie", pending("t1", "analyse"))
			now := time.Now().UTC()

			if _, err := s.Claim(id, "worker-a", time.Minute, now); err != nil {
				t.Fatalf("claim: %v", err)
			}
			later := now.Add(2 * time.Minute)
			if _, err := s.Claim(id, "worker-b", time.Minute, later); err != nil {
				t.Fatalf("reclaim: %v", err)
			}
			if err := s.Complete(id, "t1", "worker-a", task.Succeeded, []byte("stale"), "", later); !errors.Is(err, store.ErrLeaseLost) {
				t.Fatalf("a zombie wrote over live work: %v", err)
			}
			if err := s.Complete(id, "t1", "worker-b", task.Succeeded, []byte("fresh"), "", later); err != nil {
				t.Fatalf("the live holder was refused: %v", err)
			}
		})
	}
}

// TestRenewingBeatsALongLease — a long lease is indistinguishable from a hung worker.
func TestRenewingBeatsALongLease(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "renew", pending("t1", "analyse"))
			now := time.Now().UTC()

			if _, err := s.Claim(id, "worker-a", time.Minute, now); err != nil {
				t.Fatalf("claim: %v", err)
			}
			mid := now.Add(30 * time.Second)
			if err := s.Renew(id, "t1", "worker-a", time.Minute, mid); err != nil {
				t.Fatalf("renew: %v", err)
			}
			if _, err := s.Claim(id, "worker-b", time.Minute, now.Add(70*time.Second)); !errors.Is(err, store.ErrNoWork) {
				t.Fatal("a renewed lease was stolen")
			}
			if err := s.Renew(id, "t1", "worker-b", time.Minute, mid); !errors.Is(err, store.ErrLeaseLost) {
				t.Error("a worker renewed a lease it does not hold")
			}
		})
	}
}

// TestAPausedGoalHandsOutNoWork — a worker claiming from a paused goal is a pause that did not happen.
func TestAPausedGoalHandsOutNoWork(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, g := seed(t, s, "paused", pending("t1", "analyse"), pending("t2", "analyse"))
			now := time.Now().UTC()

			if err := g.Pause(now); err != nil {
				t.Fatalf("pause: %v", err)
			}
			if err := s.SaveGoal(g); err != nil {
				t.Fatalf("save: %v", err)
			}
			if _, err := s.Claim(id, "worker-a", time.Minute, now); !errors.Is(err, store.ErrNoWork) {
				t.Fatalf("a paused goal handed out work: %v", err)
			}
			if err := g.Resume(now); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if err := s.SaveGoal(g); err != nil {
				t.Fatalf("save: %v", err)
			}
			if _, err := s.Claim(id, "worker-a", time.Minute, now); err != nil {
				t.Fatalf("a resumed goal handed out nothing: %v", err)
			}
		})
	}
}

// TestDependenciesAreEnforcedByTheStore. 🔴 Not only by the Go DAG: the claim query must refuse a task
// whose prerequisites are unmet, or a worker with a stale in-memory DAG runs work out of order.
func TestDependenciesAreEnforcedByTheStore(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "deps",
				pending("a", "analyse"),
				pending("b", "analyse", "a"),
				pending("c", "analyse", "a", "b"))
			now := time.Now().UTC()

			first, err := s.Claim(id, "w", time.Minute, now)
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			if first.ID != "a" {
				t.Fatalf("claimed %q before its prerequisite; only the root is runnable", first.ID)
			}
			if _, err := s.Claim(id, "w2", time.Minute, now); !errors.Is(err, store.ErrNoWork) {
				t.Fatal("a task with an unmet dependency was handed out")
			}
			if err := s.Complete(id, "a", "w", task.Succeeded, []byte("ok"), "", now); err != nil {
				t.Fatalf("complete a: %v", err)
			}
			second, err := s.Claim(id, "w2", time.Minute, now)
			if err != nil || second.ID != "b" {
				t.Fatalf("after a succeeded, b must be claimable; got %v %v", second, err)
			}
		})
	}
}

// TestFailurePropagatesInTheStore. The store propagates so a worker cannot forget to, and the
// distinction between Blocked and Failed survives: untried tasks must not be reported as failures.
func TestFailurePropagatesInTheStore(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "propagate",
				pending("a", "analyse"),
				pending("b", "analyse", "a"),
				pending("c", "analyse", "b"),
				pending("independent", "analyse"))
			now := time.Now().UTC()

			if _, err := s.Claim(id, "w", time.Minute, now); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if err := s.Complete(id, "a", "w", task.Failed, nil, "boom", now); err != nil {
				t.Fatalf("complete: %v", err)
			}
			d, err := s.LoadDAG(id)
			if err != nil {
				t.Fatalf("load dag: %v", err)
			}
			for _, want := range []task.ID{"b", "c"} {
				if got := d.Tasks[want].State; got != task.Blocked {
					t.Errorf("%s is %s, want blocked (transitively downstream of a failure)", want, got)
				}
			}
			if got := d.Tasks["independent"].State; got != task.Pending {
				t.Errorf("an unrelated task was collateral damage: %s", got)
			}
			// 🔴 The unrelated branch must still be runnable: one failure must not corrupt the workflow.
			got, err := s.Claim(id, "w2", time.Minute, now)
			if err != nil {
				t.Fatalf("the independent branch became unrunnable after an unrelated failure: %v", err)
			}
			if got.ID != "independent" {
				t.Fatalf("claimed %q, want independent", got.ID)
			}
		})
	}
}

// TestCheckpointsResumeFromTheLatest.
func TestCheckpointsResumeFromTheLatest(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "checkpoint")
			if _, ok, err := s.LatestCheckpoint(id); err != nil || ok {
				t.Fatalf("a fresh goal reported a checkpoint: ok=%v err=%v", ok, err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			for i := 1; i <= 3; i++ {
				if err := s.Checkpoint(store.Checkpoint{GoalID: id, Iteration: i,
					Note: fmt.Sprintf("step %d", i), At: now}); err != nil {
					t.Fatalf("checkpoint %d: %v", i, err)
				}
			}
			cp, ok, err := s.LatestCheckpoint(id)
			if err != nil || !ok {
				t.Fatalf("latest: ok=%v err=%v", ok, err)
			}
			if cp.Iteration != 3 {
				t.Errorf("resume would restart at iteration %d instead of 3", cp.Iteration)
			}
			// Re-writing the same iteration must be idempotent: a worker that crashes after writing and
			// before acknowledging re-writes rather than duplicating.
			if err := s.Checkpoint(store.Checkpoint{GoalID: id, Iteration: 3, Note: "rewritten", At: now}); err != nil {
				t.Fatalf("re-checkpoint must be idempotent: %v", err)
			}
			cp, _, _ = s.LatestCheckpoint(id)
			if cp.Note != "rewritten" {
				t.Errorf("note=%q, want the rewrite to win", cp.Note)
			}
		})
	}
}

// TestReleaseYieldsWithoutBurningAnOutcome — graceful shutdown leaves the task immediately runnable.
func TestReleaseYieldsWithoutBurningAnOutcome(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			id, _ := seed(t, s, "release", pending("t1", "analyse"))
			now := time.Now().UTC()
			if _, err := s.Claim(id, "worker-a", time.Minute, now); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if err := s.Release(id, "t1", "worker-a", now); err != nil {
				t.Fatalf("release: %v", err)
			}
			got, err := s.Claim(id, "worker-b", time.Minute, now)
			if err != nil {
				t.Fatalf("a released task was not immediately claimable: %v", err)
			}
			if got.LeasedBy != "worker-b" {
				t.Errorf("lease not transferred: %q", got.LeasedBy)
			}
		})
	}
}

// ── the fence on the fence ───────────────────────────────────────────────────────────────────────

var postgresLegRan struct {
	sync.Mutex
	yes bool
}

// postgresRan records that a Postgres subtest got past its skip. Called at the TOP of each subtest,
// before open() — no: it is called with the name, and only marks when the leg is postgres AND the DSN
// is set, which is exactly the condition under which open() will not skip.
func postgresRan(name string) {
	if name != "postgres" || os.Getenv("HEROS_TEST_DATABASE_URL") == "" {
		return
	}
	postgresLegRan.Lock()
	defer postgresLegRan.Unlock()
	postgresLegRan.yes = true
}

// TestZZPostgresLegActuallyRan is the fence that keeps a skip from passing as a pass.
//
// 🔴 Without it, a wrong DSN, a stopped container or a typo'd variable name turns the entire Postgres
// half of this suite into silent skips, and the suite still reports ok. The most dangerous green build
// is the one where the thing you care about did not execute. Named ZZ so it sorts last.
func TestZZPostgresLegActuallyRan(t *testing.T) {
	if os.Getenv("HEROS_TEST_DATABASE_URL") == "" {
		t.Log("HEROS_TEST_DATABASE_URL unset: the Postgres leg was SKIPPED, and a skip is not a pass")
		return
	}
	postgresLegRan.Lock()
	defer postgresLegRan.Unlock()
	if !postgresLegRan.yes {
		t.Fatal("HEROS_TEST_DATABASE_URL is set but no Postgres subtest ran: the suite would have " +
			"reported green while testing only the in-memory implementation")
	}
}

// TestLatestGoalIsTheNewestNotTheLexicallyLast.
//
// 🔴 Regression fence. Run history used the last element of an id-ordered ListGoals, on the assumption
// that ids sort by time. They do not: ids carry the prefix of whatever created them — `g-`, `live-`,
// `e2e-` — so the lexically-last goal is whichever prefix sorts highest. In practice run history
// answered about a leftover test goal while the real run sat one row above it, and reported "recorded
// no episodes" for a run that had nine.
func TestLatestGoalIsTheNewestNotTheLexicallyLast(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			base := time.Now().UTC().Truncate(time.Millisecond)

			// `zzz-` sorts after `aaa-` lexically, but is OLDER.
			older, _ := seed(t, s, "zzz")
			g1, err := s.LoadGoal(older)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			g1.CreatedAt = base.Add(-time.Hour)
			g1.Tenant = "latest-test"
			if err := s.SaveGoal(g1); err != nil {
				t.Fatalf("save: %v", err)
			}

			newer, _ := seed(t, s, "aaa")
			g2, err := s.LoadGoal(newer)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			g2.CreatedAt = base
			g2.Tenant = "latest-test"
			if err := s.SaveGoal(g2); err != nil {
				t.Fatalf("save: %v", err)
			}

			got, ok, err := s.LatestGoal("latest-test")
			if err != nil || !ok {
				t.Fatalf("latest: ok=%v err=%v", ok, err)
			}
			if got.ID != newer {
				t.Fatalf("latest goal is %q, want %q — the lexically-last id was returned instead of "+
					"the most recent goal", got.ID, newer)
			}
		})
	}
}

// TestLatestGoalOnAnEmptyTenantIsNotAnError. "Nothing has run yet" is a real state, not a failure.
func TestLatestGoalOnAnEmptyTenantIsNotAnError(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			postgresRan(im.name)
			s := im.open(t)
			_, ok, err := s.LatestGoal("tenant-that-has-never-run-anything")
			if err != nil {
				t.Fatalf("an empty tenant produced an error: %v", err)
			}
			if ok {
				t.Fatal("an empty tenant returned a goal")
			}
		})
	}
}
