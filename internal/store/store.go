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
	// ErrGoalTerminal is a write that would change the state of a run that has already ended. It is a
	// STALE WRITER, not a broken one — see refuseTerminalOverwrite — so callers stop quietly rather than
	// reporting a failure the customer did not have.
	ErrGoalTerminal = errors.New("store: this run has already ended")
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

// Root hands out tenant-scoped stores. It is the ONLY way to obtain a Store.
//
// # 🔴 Why scoping is a type rather than a parameter
//
// Twelve of the fourteen methods below take a goal id and nothing else. With an unscoped store, a goal
// id is therefore sufficient to read any customer's data, and isolation depends on every call site
// remembering to check — which is a property no codebase keeps forever.
//
// `For` closes over the tenant, and every query it produces carries it. A request handler is given a
// scoped Store and never holds the root, so it cannot construct a query for another tenant: not because
// it is careful, but because it has nothing to be careless with.
//
// `TestATenantCannotReachAnotherTenantsData` proves it on a real database, method by method.
type Root interface {
	// For returns a store bound to one tenant. An empty tenant is refused rather than treated as "all",
	// because "all" is the value an unset variable has.
	For(tenant string) Store
}

// Store is the durable interface, ALWAYS scoped to one tenant. Every method is safe for concurrent use.
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
	// Decide answers a task parked awaiting approval: back to Ready, or Cancelled.
	//
	// 🔴 No worker id, because a parked task holds NO LEASE — a person may take a week, and a week-long
	// lease is indistinguishable from a hung worker. The state itself is the guard: only a task actually
	// in AwaitingApproval can be decided, so a second click cannot approve it twice.
	Decide(goalID goal.ID, id task.ID, approve bool, now time.Time) error

	// Cancel stops a run at a person's request: the goal moves to Cancelled and every task that has not
	// finished and is NOT currently leased moves to Cancelled with it. Returns how many tasks it took.
	//
	// # 🔴 Why this is a store method and not load / mutate / SaveDAG in the handler
	//
	// SaveDAG writes the WHOLE graph. A handler that loads the DAG, cancels some tasks and saves it back
	// has a window in which a worker completes a task, and the save then writes the stale copy over the
	// top of it — a succeeded task silently reverts to running and its result is lost. Cancelling has to
	// happen under the same lock (or transaction) that Claim and Complete take, which is here.
	//
	// # 🔴 Why a LEASED running task is deliberately left alone
	//
	// `task.Cancelled` has no outgoing transitions, and `Complete` transitions the task the worker holds.
	// So cancelling underneath a live worker does not stop it — it makes its next `Complete` fail, and
	// `api.Supervisor.drive` turns any error out of `RunOnce` into a terminal `{"goal","error"}` event.
	// The person who pressed Cancel would be told the run ERRORED, which is both untrue and the one
	// reading that sends somebody looking for a bug.
	//
	// Leaving it means the task finishes normally and the next cycle stops at `goal.Claimable()`, which
	// already refuses a non-running goal (see worker.RunOnce). One stopping mechanism, not two racing.
	// The cost is bounded by the lease: at worst the run stops one task later than the click.
	Cancel(goalID goal.ID, now time.Time) (int, error)

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

// LoadGoal returns a COPY of the goal.
//
// # 🔴 It used to return the live pointer, and that made this leg disagree with Postgres
//
// Handing out `m.goals[id]` means a caller who mutates what it loaded has already written to the store,
// with no SaveGoal and no lock. Postgres cannot behave that way — it rebuilds a struct from rows — so
// the two implementations had different semantics for the single most common sequence in the codebase:
// load, mutate, save.
//
// It stayed invisible until something needed to compare the stored state against an incoming write.
// `refuseTerminalOverwrite` does exactly that, and on the aliased version it was comparing the stored
// goal with ITSELF — the caller's mutation was already applied — so the guard passed every time and the
// memory leg silently kept the bug the Postgres leg was fixed for.
//
// The same reasoning applies more sharply to `Cancel`: with an alias, any worker holding a loaded goal
// could undo a cancellation by writing to its own copy, without ever calling the store.
//
// The slices and the Refusal pointer are cloned too, not just the struct: a shallow copy still shares
// them, so appending a milestone or rewriting a refusal would reach into the store through the back
// door the struct copy just closed.
//
// Fenced by TestAFinishedRunCannotBeRelabelledByAStaleWriter, which fails on this leg without it.
func (m *Memory) LoadGoal(id goal.ID) (*goal.Goal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return copyGoal(g), nil
}

func copyGoal(g *goal.Goal) *goal.Goal {
	c := *g
	c.Axes = append([]string(nil), g.Axes...)
	c.Criteria = append([]goal.Criterion(nil), g.Criteria...)
	c.Milestones = append([]goal.Milestone(nil), g.Milestones...)
	for i := range c.Milestones {
		c.Milestones[i].TaskIDs = append([]string(nil), g.Milestones[i].TaskIDs...)
	}
	if g.Refusal != nil {
		r := *g.Refusal
		c.Refusal = &r
	}
	return &c
}

func (m *Memory) SaveGoal(g *goal.Goal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.goals[g.ID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, g.ID)
	}
	if err := refuseTerminalOverwrite(cur.State, g); err != nil {
		return err
	}
	m.goals[g.ID] = g
	return nil
}

// refuseTerminalOverwrite stops a stale writer resurrecting or relabelling a finished run.
//
// # 🔴 The bug this closes, which was found by cancelling a real run and reading the result
//
// `Supervisor.drive` is a loop around `worker.RunOnce`, and RunOnce reads the goal ONCE at the top of a
// cycle. Cancelling from the console writes `cancelled` while a cycle is already in flight, holding a
// copy that still says `running`. That cycle then finishes its task, finds every remaining task
// cancelled — so the DAG is stalled — and writes `failed` from its stale copy. The console said the run
// was cancelled and the record said it FAILED, complete with a refusal explaining a stall that was
// really a person pressing stop.
//
// The DAG was already protected from exactly this by `Cancel` being a store method rather than a
// load / mutate / SaveDAG round trip. The goal ROW had the same hole through SaveGoal, and this is the
// same answer in the same place: last-writer-wins is wrong when one of the writers is working from a
// state that has since changed underneath it.
//
// # 🔴 Why it forbids a state CHANGE and not every write
//
// A terminal goal still legitimately receives writes — final spend, a checkpoint timestamp, milestone
// annotations — and refusing those would lose the accounting for the last task of every run. What must
// not happen is the STATE moving: terminal means terminal, so `succeeded → failed`, `cancelled → failed`
// and any resurrection are refused, while a save that leaves the state alone passes through.
//
// Fenced by TestAFinishedRunCannotBeRelabelledByAStaleWriter.
func refuseTerminalOverwrite(current goal.State, incoming *goal.Goal) error {
	if !current.Terminal() || incoming.State == current {
		return nil
	}
	return fmt.Errorf("%w: %q is %s and cannot become %s", ErrGoalTerminal,
		incoming.ID, current, incoming.State)
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

// Decide answers a parked task.
func (m *Memory) Decide(goalID goal.ID, id task.ID, approve bool, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dags[goalID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	t, ok := d.Tasks[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	}
	if t.State != task.AwaitingApproval {
		return fmt.Errorf("%w: %q is %s, not awaiting approval", ErrNotClaimable, id, t.State)
	}
	next := task.Cancelled
	if approve {
		next = task.Ready
	}
	if err := t.Transition(next, now); err != nil {
		return err
	}
	t.Approved = approve
	if !approve {
		t.Failure = "declined"
		d.PropagateFailure()
	}
	return nil
}

// Cancel stops a run. See the interface for why a leased task is left running.
func (m *Memory) Cancel(goalID goal.ID, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[goalID]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	// The goal moves FIRST. It is the gate workers read (goal.Claimable), so setting it before touching
	// any task means there is no instant in which tasks are being cancelled while the goal still invites
	// a worker to claim more of them.
	if err := g.Cancel(now); err != nil {
		return 0, err
	}
	d, ok := m.dags[goalID]
	if !ok {
		// A goal with no DAG is legitimate — Draft, refused at admission, or cancelled before planning.
		// The goal is still cancelled; there was simply nothing under it.
		return 0, nil
	}
	took := 0
	for _, t := range d.Tasks {
		if !cancellable(t, now) {
			continue
		}
		if err := t.Transition(task.Cancelled, now); err != nil {
			// Not fatal, and not silent. The task states are a closed machine and every source we select
			// for has a legal edge to Cancelled, so this is unreachable unless that machine changes
			// underneath us — which is exactly when a silent skip would be worst.
			return took, fmt.Errorf("store: cancelling %q: %w", t.ID, err)
		}
		took++
	}
	return took, nil
}

// cancellable decides whether Cancel may take this task.
//
// 🔴 Shared by both implementations so the answer cannot differ between them. Two conditions, and the
// second is the subtle one: a task is exempt only while a lease is genuinely LIVE. An expired lease
// belongs to a worker that is gone — the same reading `held` takes — and leaving those behind would let
// one dead worker keep a cancelled run's tasks pending forever.
func cancellable(t *task.Task, now time.Time) bool {
	if t.State.Terminal() {
		return false
	}
	return !(t.LeasedBy != "" && t.LeaseExpiry.After(now))
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
