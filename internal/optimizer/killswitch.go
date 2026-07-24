package optimizer

import (
	"fmt"
	"sync"
)

// KillSwitch is the always-available stop control (spec Requirement "The kill switch SHALL stop the
// loop immediately with no further merge"). It is checked before every iteration and, critically,
// immediately before every merge — so firing it mid-iteration discards the in-flight verification
// rather than merging it, leaving the last-good spec live. It is concurrency-safe: the UI fires it from
// another goroutine while the loop runs.
type KillSwitch struct {
	mu    sync.Mutex
	fired bool
	actor string
}

// NewKillSwitch builds an un-fired switch.
func NewKillSwitch() *KillSwitch { return &KillSwitch{} }

// Fire trips the switch. After it returns, Fired() is true for every subsequent check, so no candidate
// merges. Idempotent — the first actor to fire is recorded.
func (k *KillSwitch) Fire(actor string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.fired {
		k.fired = true
		k.actor = actor
	}
}

// Fired reports whether the switch has been tripped.
func (k *KillSwitch) Fired() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.fired
}

// Actor returns who fired the switch (empty if un-fired).
func (k *KillSwitch) Actor() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.actor
}

// ── Concurrency lock: one active Autonomous run per workflow (design Decision 7 / task 6.4) ─────────

// ErrWorkflowLocked means a second Autonomous run tried to start against a workflow that already has an
// active run. Two concurrent runs merging against the same branch are a write-write hazard on the repo
// and the live spec; the lock removes it by admitting only one.
type ErrWorkflowLocked struct{ WorkflowID string }

func (e ErrWorkflowLocked) Error() string {
	return fmt.Sprintf("optimizer: workflow %q already has an active optimization run", e.WorkflowID)
}

// LockSet enforces at most one active run per workflow, keyed on the workflow id (spec Requirement
// "one active Autonomous run per workflow, enforced by a lock keyed on the workflow"). It is the
// concurrency-safety mechanism, not an ambient global — a caller acquires and releases explicitly.
type LockSet struct {
	mu     sync.Mutex
	active map[string]string // workflow_id → run_id holding the lock
}

// NewLockSet builds an empty lock set.
func NewLockSet() *LockSet { return &LockSet{active: map[string]string{}} }

// Acquire takes the lock for workflowID on behalf of runID, or returns ErrWorkflowLocked if another run
// holds it. Re-acquiring for the same run is a no-op success (the run may restart its own loop).
func (l *LockSet) Acquire(workflowID, runID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if holder, ok := l.active[workflowID]; ok && holder != runID {
		return ErrWorkflowLocked{WorkflowID: workflowID}
	}
	l.active[workflowID] = runID
	return nil
}

// Release frees the lock for workflowID if runID holds it.
func (l *LockSet) Release(workflowID, runID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if holder, ok := l.active[workflowID]; ok && holder == runID {
		delete(l.active, workflowID)
	}
}
