// Package store is the durable state: goals, task DAGs, checkpoints, and the lease-based claim that
// stops two workers executing one task.
//
// # Why leases live HERE and not in a separate broker
//
// A lease is a fact about a row — "this task is claimed by worker W until time T" — and the only place
// that fact can be evaluated atomically is beside the row it describes. A separate broker holding leases
// in its own memory reintroduces the exact failure it was meant to prevent: the broker restarts, its
// leases vanish, and two workers hold the same task while both believe they are the only one.
//
// # 🔴 Expiry is evaluated on READ, never by a sweeper
//
// There is no background job that "expires" leases. `Claim` treats any lease whose expiry has passed as
// absent. This matters because a sweeper is itself a process that can be down, and while it is down
// every crashed worker's task stays claimed forever — the failure mode is a silent, total stall that
// looks exactly like "there is no work to do".
//
// # What this implementation is and is not
//
// `Memory` is a complete, concurrency-correct implementation used by tests and local runs. It is NOT
// durable across a process restart. The Postgres implementation is the highest-priority gap in
// docs/implementation-plan.md, and until it lands, principle 5 is designed for but not delivered.
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/task"
)

var (
	ErrGoalNotFound = errors.New("store: goal not found")
	ErrTaskNotFound = errors.New("store: task not found")
	ErrNotClaimable = errors.New("store: task is not claimable")
	ErrLeaseLost    = errors.New("store: lease is no longer held by this worker")
	ErrNoWork       = errors.New("store: no claimable task")
)

// Checkpoint is a resumable point in a goal's execution.
//
// 🔴 It records the ITERATION and the SPEND, not the model's context. Reconstructing context from
// persisted state on every cycle is the whole design; a checkpoint that stored a prompt would make the
// resumed run depend on a serialized context window, which is the thing being avoided.
type Checkpoint struct {
	GoalID    goal.ID
	Iteration int
	Spend     interface{ String() string } // opaque here; the goal owns its meaning
	Note      string
	At        time.Time
}

// Store is the durable interface. Every method is safe for concurrent use by many workers.
type Store interface {
	CreateGoal(g *goal.Goal) error
	LoadGoal(id goal.ID) (*goal.Goal, error)
	SaveGoal(g *goal.Goal) error
	ListGoals(state goal.State) ([]*goal.Goal, error)
	// LatestGoal returns the most recently created goal for a tenant.
	//
	// 🔴 Its own method rather than "the last element of ListGoals". ListGoals is ordered by ID for
	// stable rendering, and IDs are prefixed by whatever created them — `g-`, `live-`, `e2e-` — so the
	// lexically-last goal is whichever prefix sorts highest, not the newest. That bug shipped: run
	// history answered about a leftover test goal while the real run sat one row above it.
	LatestGoal(tenant string) (*goal.Goal, bool, error)

	SaveDAG(d *task.DAG) error
	LoadDAG(goalID goal.ID) (*task.DAG, error)

	// Claim leases one ready task for a worker. Returns ErrNoWork when there is nothing claimable.
	Claim(goalID goal.ID, worker string, lease time.Duration, now time.Time) (*task.Task, error)
	// Renew extends a live lease. A worker doing long work renews rather than taking a long lease,
	// because a long lease is indistinguishable from a hung worker.
	Renew(goalID goal.ID, id task.ID, worker string, lease time.Duration, now time.Time) error
	// Complete records a terminal outcome and releases the lease atomically.
	Complete(goalID goal.ID, id task.ID, worker string, next task.State, result []byte, failure string, now time.Time) error
	// Release returns a task to the ready set without consuming an attempt — a graceful yield.
	Release(goalID goal.ID, id task.ID, worker string, now time.Time) error

	Checkpoint(cp Checkpoint) error
	LatestCheckpoint(goalID goal.ID) (Checkpoint, bool, error)
}

// Memory is an in-process Store. Correct under concurrency; not durable across a restart.
type Memory struct {
	mu          sync.Mutex
	goals       map[goal.ID]*goal.Goal
	dags        map[goal.ID]*task.DAG
	checkpoints map[goal.ID][]Checkpoint
}

// NewMemory builds an empty store.
func NewMemory() *Memory {
	return &Memory{
		goals:       map[goal.ID]*goal.Goal{},
		dags:        map[goal.ID]*task.DAG{},
		checkpoints: map[goal.ID][]Checkpoint{},
	}
}

func (m *Memory) CreateGoal(g *goal.Goal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.goals[g.ID]; dup {
		return fmt.Errorf("store: goal %q already exists", g.ID)
	}
	m.goals[g.ID] = g
	return nil
}

func (m *Memory) LoadGoal(id goal.ID) (*goal.Goal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return g, nil
}

func (m *Memory) SaveGoal(g *goal.Goal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.goals[g.ID]; !ok {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, g.ID)
	}
	m.goals[g.ID] = g
	return nil
}

func (m *Memory) ListGoals(state goal.State) ([]*goal.Goal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*goal.Goal
	for _, g := range m.goals {
		if state == "" || g.State == state {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LatestGoal returns the newest goal by creation time.
func (m *Memory) LatestGoal(tenant string) (*goal.Goal, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *goal.Goal
	for _, g := range m.goals {
		if tenant != "" && g.Tenant != tenant {
			continue
		}
		if best == nil || g.CreatedAt.After(best.CreatedAt) {
			best = g
		}
	}
	return best, best != nil, nil
}

func (m *Memory) SaveDAG(d *task.DAG) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dags[goal.ID(d.GoalID)] = d
	return nil
}

func (m *Memory) LoadDAG(goalID goal.ID) (*task.DAG, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dags[goalID]
	if !ok {
		return nil, fmt.Errorf("%w: no DAG for goal %q", ErrGoalNotFound, goalID)
	}
	return d, nil
}

// Claim atomically leases one ready task.
//
// 🔴 The goal's own state is checked FIRST. Pause has to take effect without every worker knowing the
// goal state list, and a worker that claims from a paused goal is a pause that did not happen.
func (m *Memory) Claim(goalID goal.ID, worker string, lease time.Duration, now time.Time) (*task.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[goalID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	if !g.Claimable() {
		return nil, fmt.Errorf("%w: goal is %s", ErrNoWork, g.State)
	}
	d, ok := m.dags[goalID]
	if !ok {
		return nil, fmt.Errorf("%w: no DAG for goal %q", ErrGoalNotFound, goalID)
	}
	// ClaimableSet — not ReadySet. A task whose worker died is still Running and must be reclaimable;
	// see the comment on ClaimableSet for why conflating the two stalls a goal permanently.
	for _, t := range d.ClaimableSet(now) {
		// An EXPIRED lease is treated as absent — evaluated on read, never by a sweeper.
		if t.State == task.Running {
			if err := t.Transition(task.Ready, now); err != nil {
				return nil, err
			}
		}
		if t.State == task.Pending {
			if err := t.Transition(task.Ready, now); err != nil {
				return nil, err
			}
		}
		if err := t.Transition(task.Running, now); err != nil {
			return nil, err
		}
		t.LeasedBy, t.LeaseExpiry = worker, now.Add(lease)
		t.Attempt++
		return t, nil
	}
	return nil, ErrNoWork
}

// held returns the task if this worker still holds a live lease on it.
func (m *Memory) held(goalID goal.ID, id task.ID, worker string, now time.Time) (*task.Task, error) {
	d, ok := m.dags[goalID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	t, ok := d.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	}
	if t.LeasedBy != worker {
		return nil, fmt.Errorf("%w: %q is held by %q", ErrLeaseLost, id, t.LeasedBy)
	}
	if !t.LeaseExpiry.After(now) {
		return nil, fmt.Errorf("%w: %q expired at %s", ErrLeaseLost, id, t.LeaseExpiry.Format(time.RFC3339))
	}
	return t, nil
}

func (m *Memory) Renew(goalID goal.ID, id task.ID, worker string, lease time.Duration, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.held(goalID, id, worker, now)
	if err != nil {
		return err
	}
	t.LeaseExpiry = now.Add(lease)
	t.UpdatedAt = now
	return nil
}

// Complete records a terminal outcome and releases the lease in one step.
//
// 🔴 A worker whose lease already expired is REFUSED here. Its task has been handed to somebody else,
// and letting a zombie write its result would overwrite live work with stale work — the classic
// duplicate-execution corruption that leases exist to prevent.
func (m *Memory) Complete(goalID goal.ID, id task.ID, worker string, next task.State, result []byte, failure string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.held(goalID, id, worker, now)
	if err != nil {
		return err
	}
	if err := t.Transition(next, now); err != nil {
		return err
	}
	t.Result, t.Failure = result, failure
	t.LeasedBy, t.LeaseExpiry = "", time.Time{}
	if next == task.Failed {
		m.dags[goalID].PropagateFailure()
	}
	return nil
}

// Release yields a task without consuming its outcome. The attempt already counted at Claim.
func (m *Memory) Release(goalID goal.ID, id task.ID, worker string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.held(goalID, id, worker, now)
	if err != nil {
		return err
	}
	if err := t.Transition(task.Ready, now); err != nil {
		return err
	}
	t.LeasedBy, t.LeaseExpiry = "", time.Time{}
	return nil
}

func (m *Memory) Checkpoint(cp Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints[cp.GoalID] = append(m.checkpoints[cp.GoalID], cp)
	return nil
}

func (m *Memory) LatestCheckpoint(goalID goal.ID) (Checkpoint, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cps := m.checkpoints[goalID]
	if len(cps) == 0 {
		return Checkpoint{}, false, nil
	}
	return cps[len(cps)-1], true, nil
}

// compile-time proof that Memory satisfies the interface.
var _ Store = (*Memory)(nil)
