// Package task is the durable task DAG: the decomposition of one long-running goal into small,
// independently executable units with declared dependencies.
//
// # Why a DAG and not a list
//
// A list encodes "next"; a DAG encodes "why not yet". When a task is waiting, the person watching wants
// to know what it is waiting FOR, and a list can only say "it has not been reached". A blocked task
// whose prerequisite failed must also stop being reachable, and that is a graph property.
//
// # 🔴 State lives here, not in the worker
//
// A worker holds a task for the duration of one lease and then forgets it. Every fact needed to resume —
// which attempt this is, what it depends on, what it produced, why it failed — is on the Task. This is
// what makes "never rely on model context as memory" enforceable rather than aspirational: there is
// nowhere else for the state to live.
package task

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// ID identifies a task within a goal.
type ID string

// State is where a task is in its life. A CLOSED set with explicit transitions (see CanTransitionTo),
// because the failure mode of an implicit state machine is a task that is simultaneously running and
// done, which resolves differently depending on which worker asks.
type State string

const (
	// Pending — created, dependencies not yet satisfied.
	Pending State = "pending"
	// Ready — every dependency succeeded; may be claimed.
	Ready State = "ready"
	// Running — claimed under a live lease.
	Running State = "running"
	// AwaitingApproval — the work is done and its effect is gated on a human. 🔴 A distinct state rather
	// than a flag on Running, because a task waiting on a person must not hold a lease: the person may
	// take a week, and a week-long lease is indistinguishable from a hung worker.
	AwaitingApproval State = "awaiting_approval"
	// Succeeded — done and VERIFIED. 🔴 A task is not successful because its tool returned cleanly; it is
	// successful because the intended real-world result was independently confirmed.
	Succeeded State = "succeeded"
	// Failed — terminal after the retry ladder was exhausted.
	Failed State = "failed"
	// Blocked — a dependency failed, so this task can never become ready. Distinct from Failed because
	// nothing was attempted here and nothing about THIS task needs fixing.
	Blocked State = "blocked"
	// Cancelled — stopped deliberately. Terminal.
	Cancelled State = "cancelled"
)

// Terminal reports whether a state can still change.
func (s State) Terminal() bool {
	switch s {
	case Succeeded, Failed, Blocked, Cancelled:
		return true
	}
	return false
}

// transitions is the closed set of legal moves. Absent means forbidden.
//
// 🔴 A table rather than scattered `if` statements, so a reviewer can CHECK the machine rather than
// reconstruct it from every call site and trust there is no other one.
var transitions = map[State][]State{
	Pending:          {Ready, Blocked, Cancelled},
	Ready:            {Running, Blocked, Cancelled},
	Running:          {Succeeded, Failed, AwaitingApproval, Ready, Cancelled},
	AwaitingApproval: {Running, Cancelled, Failed},
	Succeeded:        {},
	Failed:           {},
	Blocked:          {},
	Cancelled:        {},
}

// CanTransitionTo reports whether this move is legal.
//
// 🔴 Running→Ready is legal and is not a mistake: it is what a LEASE EXPIRY does. A worker that dies
// mid-task must leave the task claimable again, and that return trip is the whole recovery story.
func (s State) CanTransitionTo(next State) bool {
	for _, ok := range transitions[s] {
		if ok == next {
			return true
		}
	}
	return false
}

// Task is one durable unit of work.
type Task struct {
	ID     ID
	GoalID string
	// Kind names what this task does. It selects the tool contract, so it is data rather than a closure:
	// a function pointer cannot be written to a database and read back after a restart.
	Kind string
	// DependsOn are prerequisite tasks. All must SUCCEED for this task to become Ready.
	DependsOn []ID
	State     State
	// Attempt counts executions. Compared against Ceilings.MaxAttemptsPerTask.
	Attempt int
	// SpawnDepth is how many tasks deep this was created. Bounds recursive task creation.
	SpawnDepth int
	// IdempotencyKey makes a retried side effect safe. 🔴 Required for effect-bearing kinds and asserted
	// by the fence: without it a retried "open a pull request" opens two, and the second one is
	// discovered by the customer.
	IdempotencyKey string
	// Result is what the task produced. Persisted so a resumed goal does not redo finished work.
	Result []byte
	// Failure is why it failed, kept on the task rather than only in a log so that "why is this red"
	// is answerable from the record the DAG is reconstructed from.
	Failure string
	// LeasedBy and LeaseExpiry are the claim. Empty when unclaimed.
	LeasedBy    string
	LeaseExpiry time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DAG is one goal's whole task graph.
type DAG struct {
	GoalID string
	Tasks  map[ID]*Task
}

// Sentinel errors, typed so a caller can distinguish an author's mistake from a limit of the system.
var (
	ErrUnknownTask   = errors.New("task: dependency names a task that is not in the DAG")
	ErrCycle         = errors.New("task: dependencies form a cycle")
	ErrIllegalMove   = errors.New("task: illegal state transition")
	ErrNoIdempotency = errors.New("task: effect-bearing task has no idempotency key")
)

// NewDAG builds a graph and validates it. A DAG that does not validate is never stored: an invalid
// graph discovered at execution time has already consumed budget.
func NewDAG(goalID string, tasks []*Task) (*DAG, error) {
	d := &DAG{GoalID: goalID, Tasks: make(map[ID]*Task, len(tasks))}
	for _, t := range tasks {
		if _, dup := d.Tasks[t.ID]; dup {
			return nil, fmt.Errorf("task: %q appears twice", t.ID)
		}
		d.Tasks[t.ID] = t
	}
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if _, ok := d.Tasks[dep]; !ok {
				return nil, fmt.Errorf("%w: %q depends on %q", ErrUnknownTask, t.ID, dep)
			}
		}
	}
	if err := d.detectCycle(); err != nil {
		return nil, err
	}
	return d, nil
}

// detectCycle is an iterative three-colour DFS. Iterative rather than recursive because the depth is
// attacker-influenced in the limit — a replanner emits tasks — and a stack overflow is a crash rather
// than a refusal.
func (d *DAG) detectCycle() error {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // finished
	)
	colour := make(map[ID]int, len(d.Tasks))
	for _, id := range d.ids() {
		if colour[id] != white {
			continue
		}
		stack := []ID{id}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			switch colour[cur] {
			case white:
				colour[cur] = grey
				for _, dep := range d.Tasks[cur].DependsOn {
					switch colour[dep] {
					case grey:
						return fmt.Errorf("%w: %q ↔ %q", ErrCycle, cur, dep)
					case white:
						stack = append(stack, dep)
					}
				}
			case grey:
				colour[cur] = black
				stack = stack[:len(stack)-1]
			default:
				stack = stack[:len(stack)-1]
			}
		}
	}
	return nil
}

// ids returns task ids in a stable order, so every traversal and every rendering is reproducible.
func (d *DAG) ids() []ID {
	out := make([]ID, 0, len(d.Tasks))
	for id := range d.Tasks {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ReadySet returns the tasks whose dependencies have all succeeded and which are claimable now.
//
// It is computed from stored state on every call rather than cached, because the cache is exactly what
// would go stale across a restart — and a stale ready-set either stalls a goal forever or runs a task
// whose prerequisite failed.
func (d *DAG) ReadySet() []*Task {
	var out []*Task
	for _, id := range d.ids() {
		t := d.Tasks[id]
		if t.State != Pending && t.State != Ready {
			continue
		}
		if d.dependenciesSucceeded(t) {
			out = append(out, t)
		}
	}
	return out
}

// ClaimableSet returns the tasks a worker may take right now: dependencies satisfied, and either
// unclaimed or holding a lease that has already expired.
//
// # 🔴 Why this is separate from ReadySet, and why the separation is load-bearing
//
// ReadySet answers "what could run" and deliberately excludes Running tasks, because a running task is
// not waiting for anything. Claiming asks a DIFFERENT question — "what may I take" — and a task whose
// worker died is claimable while still being Running.
//
// Collapsing the two costs exactly one bug, and it is a total one: with only ReadySet, an expired lease
// is invisible to the claimer, so a crashed worker's task stays Running forever, nothing is ever
// reclaimed, and the goal stalls permanently while reporting that it has work in flight. That is the
// precise failure leases exist to prevent, reintroduced by the read path.
//
// Expiry is evaluated HERE, on read, rather than by a sweeper — a sweeper is a process that can itself
// be down, and while it is down every crashed worker's task is stuck.
func (d *DAG) ClaimableSet(now time.Time) []*Task {
	var out []*Task
	for _, id := range d.ids() {
		t := d.Tasks[id]
		switch t.State {
		case Pending, Ready:
		case Running:
			if t.LeaseExpiry.After(now) {
				continue // a live lease: somebody else holds this
			}
		default:
			continue
		}
		if d.dependenciesSucceeded(t) {
			out = append(out, t)
		}
	}
	return out
}

func (d *DAG) dependenciesSucceeded(t *Task) bool {
	for _, dep := range t.DependsOn {
		if d.Tasks[dep].State != Succeeded {
			return false
		}
	}
	return true
}

// PropagateFailure marks every task transitively downstream of a failed task as Blocked.
//
// 🔴 Blocked rather than Failed, because nothing was attempted at those tasks and nothing about them
// needs fixing. Reporting them as failures would tell an operator to go and look at nine healthy tasks.
// This is "handle partial failures": one task failing must not corrupt the rest of the graph, and the
// tasks that CAN still run are untouched here.
func (d *DAG) PropagateFailure() int {
	blocked := 0
	for changed := true; changed; {
		changed = false
		for _, id := range d.ids() {
			t := d.Tasks[id]
			if t.State.Terminal() {
				continue
			}
			for _, dep := range t.DependsOn {
				ds := d.Tasks[dep].State
				if ds == Failed || ds == Blocked || ds == Cancelled {
					t.State = Blocked
					t.Failure = fmt.Sprintf("dependency %q is %s", dep, ds)
					blocked++
					changed = true
					break
				}
			}
		}
	}
	return blocked
}

// Progress counts terminal versus total. Used by the goal to decide completion and by the timeline to
// answer "how far along is this".
func (d *DAG) Progress() (done, total int) {
	for _, t := range d.Tasks {
		total++
		if t.State.Terminal() {
			done++
		}
	}
	return done, total
}

// Stalled reports a goal that can make no further progress but is not finished: nothing is ready,
// nothing is running, nothing awaits approval, and work remains.
//
// 🔴 This state must be DETECTED rather than waited out. Without it a goal with an unreachable task
// sits at "in progress" forever, consuming its wall-clock ceiling to reach a conclusion that was
// available immediately — and the operator sees a run that looks busy.
func (d *DAG) Stalled() bool {
	if len(d.ReadySet()) > 0 {
		return false
	}
	remaining := false
	for _, t := range d.Tasks {
		switch t.State {
		case Running, AwaitingApproval:
			return false
		}
		if !t.State.Terminal() {
			remaining = true
		}
	}
	return remaining
}

// Transition moves a task, refusing illegal moves.
func (t *Task) Transition(next State, now time.Time) error {
	if !t.State.CanTransitionTo(next) {
		return fmt.Errorf("%w: %s → %s (task %q)", ErrIllegalMove, t.State, next, t.ID)
	}
	t.State = next
	t.UpdatedAt = now
	return nil
}

// EffectBearingKinds is the closed set of task kinds that can change something outside the platform.
//
// 🔴 A TABLE rather than a check inside each executor, for the reason the previous system recorded: a
// reviewer can check a list, but confirming a property spread across N constructors means reading all N
// and trusting there is no N+1.
var EffectBearingKinds = map[string]bool{
	"open_pull_request": true,
	"write_source":      true,
	"deliver_change":    true,
	"publish_eval_set":  true,
}

// RequireIdempotency fails when an effect-bearing task has no idempotency key.
//
// Without one a retried "open a pull request" opens two, and the duplicate is discovered by the
// customer rather than by us.
func (t *Task) RequireIdempotency() error {
	if EffectBearingKinds[t.Kind] && t.IdempotencyKey == "" {
		return fmt.Errorf("%w: kind %q, task %q", ErrNoIdempotency, t.Kind, t.ID)
	}
	return nil
}
