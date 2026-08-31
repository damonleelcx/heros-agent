package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/task"
)

func fixture(t *testing.T, ids ...string) (*Memory, *goal.Goal) {
	t.Helper()
	now := time.Now()
	g := &goal.Goal{
		ID: "g1", Tenant: "t1", Intent: intent.Assess, State: goal.Draft,
		Subject: goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc"},
		Ceilings: bounds.Ceilings{MaxIterations: 10, MaxTasks: 50, MaxAttemptsPerTask: 3, MaxToolCalls: 100,
			MaxTokens: 1e6, MaxCostCents: 500, MaxWallClock: time.Hour, MaxSpawnDepth: 3},
		Criteria: []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 9}},
	}
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	m := NewMemory()
	if err := m.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	var tasks []*task.Task
	for _, id := range ids {
		tasks = append(tasks, &task.Task{ID: task.ID(id), GoalID: "g1", Kind: "analyse", State: task.Pending})
	}
	d, err := task.NewDAG("g1", tasks)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	if err := m.SaveDAG(d); err != nil {
		t.Fatalf("saveDAG: %v", err)
	}
	return m, g
}

// TestExactlyOneWorkerWinsATask is the property the whole lease mechanism exists for. Run under -race.
func TestExactlyOneWorkerWinsATask(t *testing.T) {
	m, _ := fixture(t, "only")
	now := time.Now()

	const workers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := m.Claim("g1", fmt.Sprintf("w%d", i), time.Minute, now)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				won++
			} else if !errors.Is(err, ErrNoWork) {
				t.Errorf("unexpected claim error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d workers claimed the same task; exactly 1 must win", won)
	}
}

// TestAnExpiredLeaseIsReclaimable — the crashed-worker recovery path. If this fails, a worker that dies
// takes its task to the grave and the goal stalls forever.
func TestAnExpiredLeaseIsReclaimable(t *testing.T) {
	m, _ := fixture(t, "t1")
	now := time.Now()
	first, err := m.Claim("g1", "worker-a", time.Minute, now)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := m.Claim("g1", "worker-b", time.Minute, now); !errors.Is(err, ErrNoWork) {
		t.Fatal("a live lease was ignored")
	}
	// worker-a dies. Nothing sweeps. Time simply passes.
	later := now.Add(2 * time.Minute)
	second, err := m.Claim("g1", "worker-b", time.Minute, later)
	if err != nil {
		t.Fatalf("expired lease was not reclaimable, so a dead worker stalls the goal forever: %v", err)
	}
	if second.ID != first.ID {
		t.Fatal("reclaimed a different task")
	}
	if second.Attempt != 2 {
		t.Errorf("attempt = %d, want 2: a reclaim must count against the retry ladder, or a task that "+
			"crashes a worker every time retries forever", second.Attempt)
	}
}

// TestAZombieWorkerCannotWriteItsResult. Its task was handed to somebody else; letting it write would
// overwrite live work with stale work.
func TestAZombieWorkerCannotWriteItsResult(t *testing.T) {
	m, _ := fixture(t, "t1")
	now := time.Now()
	if _, err := m.Claim("g1", "worker-a", time.Minute, now); err != nil {
		t.Fatalf("claim: %v", err)
	}
	later := now.Add(2 * time.Minute)
	if _, err := m.Claim("g1", "worker-b", time.Minute, later); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	err := m.Complete("g1", "t1", "worker-a", task.Succeeded, []byte("stale"), "", later)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a zombie worker wrote over live work: %v", err)
	}
	if err := m.Complete("g1", "t1", "worker-b", task.Succeeded, []byte("fresh"), "", later); err != nil {
		t.Fatalf("the live holder was refused: %v", err)
	}
}

// TestRenewingBeatsALongLease. A long lease is indistinguishable from a hung worker.
func TestRenewingBeatsALongLease(t *testing.T) {
	m, _ := fixture(t, "t1")
	now := time.Now()
	if _, err := m.Claim("g1", "worker-a", time.Minute, now); err != nil {
		t.Fatalf("claim: %v", err)
	}
	mid := now.Add(30 * time.Second)
	if err := m.Renew("g1", "t1", "worker-a", time.Minute, mid); err != nil {
		t.Fatalf("renew: %v", err)
	}
	// Past the ORIGINAL expiry but inside the renewed one.
	if _, err := m.Claim("g1", "worker-b", time.Minute, now.Add(70*time.Second)); !errors.Is(err, ErrNoWork) {
		t.Fatal("a renewed lease was stolen")
	}
	if err := m.Renew("g1", "t1", "worker-b", time.Minute, mid); !errors.Is(err, ErrLeaseLost) {
		t.Error("a worker renewed a lease it does not hold")
	}
}

// TestAPausedGoalHandsOutNoWork. A worker that claims from a paused goal is a pause that did not happen.
func TestAPausedGoalHandsOutNoWork(t *testing.T) {
	m, g := fixture(t, "t1", "t2")
	now := time.Now()
	if err := g.Pause(now); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := m.SaveGoal(g); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := m.Claim("g1", "worker-a", time.Minute, now); !errors.Is(err, ErrNoWork) {
		t.Fatalf("a paused goal handed out work: %v", err)
	}
	if err := g.Resume(now); err != nil {
		t.Fatalf("resume: %v", err)
	}
	_ = m.SaveGoal(g)
	if _, err := m.Claim("g1", "worker-a", time.Minute, now); err != nil {
		t.Fatalf("a resumed goal handed out nothing: %v", err)
	}
}

// TestReleaseYieldsWithoutBurningAnOutcome — graceful shutdown leaves the task runnable.
func TestReleaseYieldsWithoutBurningAnOutcome(t *testing.T) {
	m, _ := fixture(t, "t1")
	now := time.Now()
	if _, err := m.Claim("g1", "worker-a", time.Minute, now); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := m.Release("g1", "t1", "worker-a", now); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err := m.Claim("g1", "worker-b", time.Minute, now)
	if err != nil {
		t.Fatalf("a released task was not immediately claimable: %v", err)
	}
	if got.LeasedBy != "worker-b" {
		t.Errorf("lease not transferred: %q", got.LeasedBy)
	}
}

// TestCheckpointsRoundTrip — resume starts from the latest, not the first.
func TestCheckpointsRoundTrip(t *testing.T) {
	m, _ := fixture(t, "t1")
	if _, ok, _ := m.LatestCheckpoint("g1"); ok {
		t.Fatal("a fresh goal reported a checkpoint")
	}
	now := time.Now()
	for i := 1; i <= 3; i++ {
		if err := m.Checkpoint(Checkpoint{GoalID: "g1", Iteration: i, At: now}); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
	}
	cp, ok, err := m.LatestCheckpoint("g1")
	if err != nil || !ok {
		t.Fatalf("latest: %v ok=%v", err, ok)
	}
	if cp.Iteration != 3 {
		t.Errorf("resume would restart at iteration %d instead of 3", cp.Iteration)
	}
}

// TestFailureBlocksDownstreamAtCompletion — the store propagates, so a worker cannot forget to.
func TestFailureBlocksDownstreamAtCompletion(t *testing.T) {
	m, _ := fixture(t)
	now := time.Now()
	d, _ := task.NewDAG("g1", []*task.Task{
		{ID: "a", GoalID: "g1", Kind: "analyse", State: task.Pending},
		{ID: "b", GoalID: "g1", Kind: "analyse", State: task.Pending, DependsOn: []task.ID{"a"}},
	})
	_ = m.SaveDAG(d)
	if _, err := m.Claim("g1", "w", time.Minute, now); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := m.Complete("g1", "a", "w", task.Failed, nil, "boom", now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if d.Tasks["b"].State != task.Blocked {
		t.Fatalf("downstream task is %s; the store must propagate so a worker cannot forget to",
			d.Tasks["b"].State)
	}
}
