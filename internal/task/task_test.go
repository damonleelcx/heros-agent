package task

import (
	"errors"
	"testing"
	"time"
)

func tk(id string, deps ...ID) *Task {
	return &Task{ID: ID(id), GoalID: "g1", Kind: "analyse", State: Pending, DependsOn: deps}
}

func mustDAG(t *testing.T, tasks ...*Task) *DAG {
	t.Helper()
	d, err := NewDAG("g1", tasks)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	return d
}

// TestAnInvalidGraphIsRejectedBeforeItCostsAnything. A cycle or a dangling dependency discovered at
// execution time has already spent budget getting there.
func TestAnInvalidGraphIsRejectedBeforeItCostsAnything(t *testing.T) {
	if _, err := NewDAG("g1", []*Task{tk("a", "nope")}); !errors.Is(err, ErrUnknownTask) {
		t.Errorf("dangling dependency accepted: %v", err)
	}
	if _, err := NewDAG("g1", []*Task{tk("a", "b"), tk("b", "a")}); !errors.Is(err, ErrCycle) {
		t.Errorf("two-node cycle accepted: %v", err)
	}
	if _, err := NewDAG("g1", []*Task{tk("a", "c"), tk("b", "a"), tk("c", "b")}); !errors.Is(err, ErrCycle) {
		t.Errorf("three-node cycle accepted: %v", err)
	}
	if _, err := NewDAG("g1", []*Task{tk("a"), tk("a")}); err == nil {
		t.Error("duplicate task id accepted")
	}
	// A diamond is not a cycle, and rejecting it would forbid the most common useful shape.
	if _, err := NewDAG("g1", []*Task{tk("a"), tk("b", "a"), tk("c", "a"), tk("d", "b", "c")}); err != nil {
		t.Errorf("diamond rejected: %v", err)
	}
}

// TestReadySetWaitsForPrerequisites — the whole reason this is a graph.
func TestReadySetWaitsForPrerequisites(t *testing.T) {
	d := mustDAG(t, tk("a"), tk("b", "a"), tk("c", "a"), tk("d", "b", "c"))
	if got := len(d.ReadySet()); got != 1 || d.ReadySet()[0].ID != "a" {
		t.Fatalf("only the root should be ready; got %d", got)
	}
	d.Tasks["a"].State = Succeeded
	if got := len(d.ReadySet()); got != 2 {
		t.Fatalf("both children should be ready after the root succeeds; got %d", got)
	}
	d.Tasks["b"].State = Succeeded
	for _, r := range d.ReadySet() {
		if r.ID == "d" {
			t.Fatal("d became ready with only one of its two prerequisites done")
		}
	}
	d.Tasks["c"].State = Succeeded
	if got := len(d.ReadySet()); got != 1 || d.ReadySet()[0].ID != "d" {
		t.Fatalf("d should be ready once both prerequisites succeed; got %d", got)
	}
}

// TestAFailureBlocksOnlyWhatDependsOnIt. "Handle partial failures": one task failing must not corrupt
// the workflow or discard work that can still proceed.
func TestAFailureBlocksOnlyWhatDependsOnIt(t *testing.T) {
	d := mustDAG(t, tk("a"), tk("b", "a"), tk("c", "b"), tk("independent"))
	d.Tasks["a"].State = Failed
	blocked := d.PropagateFailure()
	if blocked != 2 {
		t.Fatalf("expected b and c to block transitively, got %d", blocked)
	}
	for _, id := range []ID{"b", "c"} {
		if d.Tasks[id].State != Blocked {
			t.Errorf("%s is %s, want blocked", id, d.Tasks[id].State)
		}
		if d.Tasks[id].Failure == "" {
			t.Errorf("%s is blocked but does not say what blocked it", id)
		}
	}
	// 🔴 The point of the test: the unrelated branch is untouched and still runnable.
	if d.Tasks["independent"].State != Pending {
		t.Errorf("an unrelated task was collateral damage: %s", d.Tasks["independent"].State)
	}
	if len(d.ReadySet()) != 1 {
		t.Error("the independent task should still be ready to run")
	}
}

// TestBlockedIsNotFailed. Reporting untried tasks as failures sends an operator to look at healthy code.
func TestBlockedIsNotFailed(t *testing.T) {
	d := mustDAG(t, tk("a"), tk("b", "a"))
	d.Tasks["a"].State = Failed
	d.PropagateFailure()
	if d.Tasks["b"].State == Failed {
		t.Fatal("a task that was never attempted must not be reported as failed")
	}
	if d.Tasks["b"].Attempt != 0 {
		t.Fatal("a blocked task should have no attempts")
	}
}

// TestLeaseExpiryReturnsATaskToTheQueue. Running→Ready is the recovery path; forbidding it would mean
// a worker that dies takes its task to the grave.
func TestLeaseExpiryReturnsATaskToTheQueue(t *testing.T) {
	if !Running.CanTransitionTo(Ready) {
		t.Fatal("running→ready must be legal: it is what a lease expiry does when a worker dies")
	}
	for _, s := range []State{Succeeded, Failed, Blocked, Cancelled} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
		if s.CanTransitionTo(Running) {
			t.Errorf("%s is terminal but can move back to running", s)
		}
	}
}

// TestApprovalDoesNotHoldALease. A person may take a week; a week-long lease is indistinguishable from
// a hung worker, and the reclaim logic cannot tell them apart.
func TestApprovalDoesNotHoldALease(t *testing.T) {
	if !Running.CanTransitionTo(AwaitingApproval) {
		t.Fatal("a task must be able to park itself awaiting a human")
	}
	if !AwaitingApproval.CanTransitionTo(Running) {
		t.Fatal("an approved task must be able to resume")
	}
	if AwaitingApproval.Terminal() {
		t.Fatal("awaiting approval is not terminal — the human has not answered yet")
	}
}

// TestIllegalTransitionsAreRefused. An implicit state machine produces tasks that are simultaneously
// running and done, resolving differently depending on which worker asks.
func TestIllegalTransitionsAreRefused(t *testing.T) {
	now := time.Now()
	tsk := tk("a")
	if err := tsk.Transition(Succeeded, now); !errors.Is(err, ErrIllegalMove) {
		t.Errorf("pending→succeeded must be refused; a task cannot finish without running: %v", err)
	}
	if err := tsk.Transition(Ready, now); err != nil {
		t.Fatalf("pending→ready: %v", err)
	}
	if err := tsk.Transition(Running, now); err != nil {
		t.Fatalf("ready→running: %v", err)
	}
	if err := tsk.Transition(Succeeded, now); err != nil {
		t.Fatalf("running→succeeded: %v", err)
	}
	if err := tsk.Transition(Running, now); !errors.Is(err, ErrIllegalMove) {
		t.Error("a succeeded task was resurrected")
	}
}

// TestAStallIsDetectedRatherThanWaitedOut. Otherwise a goal burns its wall-clock ceiling to reach a
// conclusion that was available immediately, and looks busy the whole time.
func TestAStallIsDetectedRatherThanWaitedOut(t *testing.T) {
	d := mustDAG(t, tk("a"), tk("b", "a"))
	if d.Stalled() {
		t.Fatal("a fresh graph with a ready root is not stalled")
	}
	d.Tasks["a"].State = Failed
	d.PropagateFailure()
	if d.Stalled() {
		t.Fatal("everything is terminal here; that is finished, not stalled")
	}
	d2 := mustDAG(t, tk("a"), tk("b", "a"))
	d2.Tasks["a"].State = Cancelled
	if !d2.Stalled() {
		t.Fatal("a cancelled prerequisite leaves b unreachable and nothing running: that is a stall")
	}
	d3 := mustDAG(t, tk("a"))
	d3.Tasks["a"].State = Running
	if d3.Stalled() {
		t.Fatal("work in flight is not a stall")
	}
}

// TestEffectBearingTasksMustCarryAnIdempotencyKey. Without one a retried "open a pull request" opens
// two, and the customer finds the duplicate.
func TestEffectBearingTasksMustCarryAnIdempotencyKey(t *testing.T) {
	for kind := range EffectBearingKinds {
		bare := &Task{ID: "x", Kind: kind}
		if err := bare.RequireIdempotency(); !errors.Is(err, ErrNoIdempotency) {
			t.Errorf("%s without a key was accepted: a retry would duplicate the effect", kind)
		}
		keyed := &Task{ID: "x", Kind: kind, IdempotencyKey: "k1"}
		if err := keyed.RequireIdempotency(); err != nil {
			t.Errorf("%s with a key was rejected: %v", kind, err)
		}
	}
	if err := (&Task{ID: "x", Kind: "analyse"}).RequireIdempotency(); err != nil {
		t.Errorf("a read-only kind must not need a key: %v", err)
	}
}

// TestProgressCountsTerminalWork.
func TestProgressCountsTerminalWork(t *testing.T) {
	d := mustDAG(t, tk("a"), tk("b", "a"), tk("c", "b"))
	if done, total := d.Progress(); done != 0 || total != 3 {
		t.Fatalf("got %d/%d, want 0/3", done, total)
	}
	d.Tasks["a"].State = Succeeded
	d.Tasks["b"].State = Failed
	if done, total := d.Progress(); done != 2 || total != 3 {
		t.Fatalf("got %d/%d, want 2/3", done, total)
	}
}
