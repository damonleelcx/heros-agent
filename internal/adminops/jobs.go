package adminops

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/runqueue"
)

// jobs.go is the job/queue operations surface and worker-fleet health (FR11).
//
// # One queue, not a second one
//
// Everything here reads and commands the EXISTING P4/P6 run queue through a narrow interface that
// `runqueue.Queue` satisfies. There is no console-side job table, no shadow state and no second
// dispatcher — because two queues means two answers to "is this job running", and the console would
// be authoritative about neither.
//
// # Why fleet health is derived rather than collected
//
// The queue already knows its own depth by state, which worker holds which lease, and when those
// leases expire. That IS fleet health. Collecting it a second way would produce a number that
// disagrees with the queue during exactly the incident it was built for.

// JobQueue is P8's view of the existing P4/P6 queue.
type JobQueue interface {
	// List returns up to limit queue items, newest first.
	List(ctx context.Context, limit int) ([]runqueue.Job, error)
	// Requeue returns a dead-lettered item to the ready state (the operator retry).
	Requeue(ctx context.Context, runID string) error
	// Cancel parks a queued or running item with the operator's reason.
	Cancel(ctx context.Context, runID, reason string) error
	// Stats reports depth by state.
	Stats(ctx context.Context) (runqueue.Stats, error)
	// Describe names the queue for the readiness surface, so an operator can confirm the console is
	// pointed at the real one.
	Describe() string
}

// FleetHealth is the worker-fleet read model, derived from the queue.
type FleetHealth struct {
	// Ready / Leased / Done / Dead are the queue's depth by state.
	Ready  int `json:"ready"`
	Leased int `json:"leased"`
	Done   int `json:"done"`
	Dead   int `json:"dead"`
	// Workers is the set of workers currently holding a lease, with how many items each holds.
	Workers map[string]int `json:"workers"`
	// ExpiredLeases counts leases already past their expiry — items a worker is holding that the queue
	// will redeliver. A non-zero value is the shape a dead worker takes.
	ExpiredLeases int `json:"expired_leases"`
	// OldestLeaseAge is how long the longest-held lease has been out.
	OldestLeaseAgeSeconds int `json:"oldest_lease_age_seconds"`
	// Source names the queue this was derived from — evidence that the console is reading the existing
	// pipeline rather than one of its own.
	Source string `json:"source"`
	// Degraded and Detail report that the queue could not be read. Distinct from "everything is zero",
	// which is what an empty healthy queue looks like (FR26).
	Degraded bool   `json:"degraded"`
	Detail   string `json:"detail,omitempty"`
}

// JobService is the operator's job and fleet surface.
type JobService struct {
	exec  *Executor
	queue JobQueue
	now   func() time.Time
}

// NewJobService wires the service.
func NewJobService(exec *Executor, queue JobQueue) (*JobService, error) {
	if exec == nil || queue == nil {
		return nil, errors.New("adminops: the job service needs the command path and the existing P4/P6 queue")
	}
	return &JobService{exec: exec, queue: queue, now: exec.Now}, nil
}

// List returns queue items. Read-only, permission-gated on the read capability every role holds.
func (s *JobService) List(ctx context.Context, limit int) ([]runqueue.Job, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapJobRead, TargetGlobal); err != nil {
		return nil, err
	}
	return s.queue.List(ctx, limit)
}

// Health returns worker-fleet health derived from the same queue.
//
// A read failure returns a DEGRADED health rather than an error, because the console must render
// "we cannot see the fleet" distinguishably from "the fleet is idle" — and an error would collapse
// both into an empty page.
func (s *JobService) Health(ctx context.Context) (FleetHealth, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapJobRead, TargetGlobal); err != nil {
		return FleetHealth{}, err
	}
	h := FleetHealth{Workers: map[string]int{}, Source: s.queue.Describe()}
	stats, err := s.queue.Stats(ctx)
	if err != nil {
		h.Degraded, h.Detail = true, err.Error()
		return h, nil
	}
	h.Ready, h.Leased, h.Done, h.Dead = stats.Ready, stats.Leased, stats.Done, stats.Dead

	jobs, err := s.queue.List(ctx, 500)
	if err != nil {
		h.Degraded, h.Detail = true, err.Error()
		return h, nil
	}
	now := s.now()
	for _, j := range jobs {
		if j.State != "leased" || j.LeasedBy == "" {
			continue
		}
		h.Workers[j.LeasedBy]++
		if j.LeaseExpiresAt != nil && now.After(*j.LeaseExpiresAt) {
			h.ExpiredLeases++
		}
		if age := int(now.Sub(j.EnqueuedAt).Seconds()); age > h.OldestLeaseAgeSeconds {
			h.OldestLeaseAgeSeconds = age
		}
	}
	return h, nil
}

// Retry returns a parked job to the queue. Permission-gated to Platform-SRE (and Superadmin),
// reason-required, confirmed, audited — a retry re-spends money, so it is not a free click.
func (s *JobService) Retry(ctx context.Context, runID, reason string, confirm Confirmation) (Receipt, error) {
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapJobRetry,
		Action:     adminaudit.ActionJobRetry,
		Target:     JobTarget(runID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{runID},
		Evidence:   map[string]string{"run_id": runID, "queue": s.queue.Describe()},
	}, func(ctx context.Context) (map[string]string, error) {
		if err := s.queue.Requeue(ctx, runID); err != nil {
			return nil, err
		}
		return map[string]string{"run_id": runID, "state": "ready"}, nil
	})
}

// Cancel parks a running or queued job. Destructive: confirm + reason + audit (FR11).
func (s *JobService) Cancel(ctx context.Context, runID, reason string, confirm Confirmation) (Receipt, error) {
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapJobCancel,
		Action:     adminaudit.ActionJobCancel,
		Target:     JobTarget(runID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{runID},
		Evidence:   map[string]string{"run_id": runID, "queue": s.queue.Describe()},
	}, func(ctx context.Context) (map[string]string, error) {
		// The operator's reason travels all the way into the queue's dead-letter reason, so the job's
		// own record says why it stopped without a cross-reference to the audit chain.
		if err := s.queue.Cancel(ctx, runID, fmt.Sprintf("cancelled by operator: %s", reason)); err != nil {
			return nil, err
		}
		return map[string]string{"run_id": runID, "state": "dead"}, nil
	})
}

// PostgresQueue adapts the live Postgres run queue to JobQueue. It is a type assertion in function
// form: the adapter exists so the interface stays this package's, while the implementation stays the
// queue's.
type PostgresQueue struct{ Q *runqueue.Queue }

// List implements JobQueue.
func (p PostgresQueue) List(ctx context.Context, limit int) ([]runqueue.Job, error) {
	return p.Q.List(ctx, limit)
}

// Requeue implements JobQueue.
func (p PostgresQueue) Requeue(ctx context.Context, runID string) error {
	return p.Q.Requeue(ctx, runID)
}

// Cancel implements JobQueue.
func (p PostgresQueue) Cancel(ctx context.Context, runID, reason string) error {
	return p.Q.Cancel(ctx, runID, reason)
}

// Stats implements JobQueue.
func (p PostgresQueue) Stats(ctx context.Context) (runqueue.Stats, error) { return p.Q.Stats(ctx) }

// Describe implements JobQueue.
func (p PostgresQueue) Describe() string { return "postgres:run_queue(p4/p6)" }

// MemJobQueue is an in-process JobQueue with the run_queue state machine, for the demo and any
// deployment that runs the queue in memory. The live Postgres path (PostgresQueue) is what production
// uses; this exists so the console can be driven without a database, not as a second production queue.
type MemJobQueue struct {
	mu   sync.Mutex
	jobs map[string]*runqueue.Job
	now  func() time.Time
}

// NewMemJobQueue builds an empty in-memory queue.
func NewMemJobQueue(now func() time.Time) *MemJobQueue {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemJobQueue{jobs: map[string]*runqueue.Job{}, now: now}
}

// Seed adds a job in a given state — the demo populates the fleet with it.
func (q *MemJobQueue) Seed(j runqueue.Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cp := j
	q.jobs[j.RunID] = &cp
}

// List implements JobQueue.
func (q *MemJobQueue) List(_ context.Context, limit int) ([]runqueue.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]runqueue.Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EnqueuedAt.After(out[j].EnqueuedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Requeue implements JobQueue: only a dead-lettered item can be retried.
func (q *MemJobQueue) Requeue(_ context.Context, runID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[runID]
	if !ok || j.State != "dead" {
		return runqueue.ErrNotRetryable
	}
	j.State, j.Attempts, j.LeasedBy, j.LeaseExpiresAt, j.DeadLetterReason = "ready", 0, "", nil, ""
	return nil
}

// Cancel implements JobQueue: only a queued or running item can be cancelled.
func (q *MemJobQueue) Cancel(_ context.Context, runID, reason string) error {
	if reason == "" {
		return fmt.Errorf("adminops: cancelling %s requires a reason", runID)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[runID]
	if !ok || (j.State != "ready" && j.State != "leased") {
		return runqueue.ErrNotCancellable
	}
	j.State, j.LeasedBy, j.LeaseExpiresAt, j.DeadLetterReason = "dead", "", nil, reason
	return nil
}

// Stats implements JobQueue.
func (q *MemJobQueue) Stats(_ context.Context) (runqueue.Stats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var s runqueue.Stats
	for _, j := range q.jobs {
		switch j.State {
		case "ready":
			s.Ready++
		case "leased":
			s.Leased++
		case "done":
			s.Done++
		case "dead":
			s.Dead++
		}
	}
	return s, nil
}

// Describe implements JobQueue.
func (q *MemJobQueue) Describe() string { return "memory:run_queue(p4/p6)" }
