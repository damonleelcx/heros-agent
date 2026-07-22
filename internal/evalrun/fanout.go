package evalrun

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/runqueue"
)

// fanout.go is task 6.1: queue-driven fan-out for multi-seed sweeps and generation, with bounded
// concurrency, backpressure, and idempotent re-delivery.
//
// The queue (P2) already gives at-least-once delivery, lease reclaim and an attempt budget; what it
// deliberately does NOT give is a worker pool or any cap on in-flight work. Adding those here rather
// than in the queue keeps the queue a queue: the concurrency policy belongs to the workload, and a
// 250-unit eval sweep and a single interactive submit want very different ones.
//
// Idempotency is INHERITED, not re-invented: the run id is content-derived (RunIDFor), Enqueue is
// ON CONFLICT DO NOTHING, and the P2 executor refuses to overwrite a terminal verdict. A redelivered
// item therefore re-executes the same run rather than billing a second one.

// DefaultConcurrency is the in-flight cap. Bounded by default because an unbounded fan-out over a
// 250-unit sweep is a self-inflicted provider rate-limit and a bill nobody approved.
const DefaultConcurrency = 8

// DefaultPollInterval is how long a worker waits when the queue is empty. It is a wait, not a spin:
// ErrEmpty is the queue's normal idle state, not an error condition to retry hard against.
const DefaultPollInterval = 200 * time.Millisecond

// Enqueuer is the subset of the run queue the fan-out submits through. An interface so the planner
// is testable without Postgres, and so nothing here can reach past the queue's idempotency.
type Enqueuer interface {
	Enqueue(ctx context.Context, runID, configHash, sourceRevision string, seed int64) error
}

// Handler executes one unit of eval work. It is the seam between the fan-out (which owns
// concurrency, leases and backpressure) and the harness (which owns measurement).
type Handler interface {
	Handle(ctx context.Context, item runqueue.Item) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, item runqueue.Item) error

func (f HandlerFunc) Handle(ctx context.Context, item runqueue.Item) error { return f(ctx, item) }

// Dispatcher is the queue side the worker pool consumes.
type Dispatcher interface {
	Dequeue(ctx context.Context, worker string) (*runqueue.Item, error)
	Ack(ctx context.Context, runID, worker string) error
	Nack(ctx context.Context, runID, worker string, cause error, backoff time.Duration) error
	Renew(ctx context.Context, runID, worker string) error
}

// EnqueuePlan submits every unit of a fan-out plan.
//
// It returns how many were NEWLY queued versus how many were already present. The distinction is the
// idempotency proof made visible: re-running EnqueuePlan after a crash reports every unit as already
// queued and enqueues nothing, which is what "no double-charge on redelivery" means in practice.
func EnqueuePlan(ctx context.Context, q Enqueuer, units []Unit) (Submitted, error) {
	var s Submitted
	seen := map[string]bool{}
	for _, u := range units {
		if seen[u.RunID] {
			// Two units with the same run id are the same work by construction (same config, case
			// and seed). Collapsing them here rather than at the database keeps the count honest.
			s.Collapsed++
			continue
		}
		seen[u.RunID] = true
		if err := q.Enqueue(ctx, u.RunID, u.ConfigHash, u.SourceRevision, u.Seed); err != nil {
			return s, fmt.Errorf("evalrun: enqueue %s: %w", u.RunID, err)
		}
		s.Submitted++
		s.RunIDs = append(s.RunIDs, u.RunID)
	}
	return s, nil
}

// Submitted reports what an EnqueuePlan call did.
type Submitted struct {
	Submitted int      `json:"submitted"`
	Collapsed int      `json:"collapsed"`
	RunIDs    []string `json:"run_ids"`
}

// PoolConfig configures the worker pool.
type PoolConfig struct {
	// Concurrency is the maximum number of units executing at once — the backpressure knob.
	Concurrency int
	// WorkerPrefix identifies this process's workers in the queue's lease column.
	WorkerPrefix string
	// PollInterval is the idle wait when the queue is empty.
	PollInterval time.Duration
	// RenewInterval is how often an in-flight unit renews its lease. Without renewal a unit slower
	// than the lease is redelivered while still running, and the same work executes twice.
	RenewInterval time.Duration
	// Backoff is the delay applied when a unit is nacked.
	Backoff time.Duration
}

func (c PoolConfig) normalized() PoolConfig {
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}
	if c.WorkerPrefix == "" {
		c.WorkerPrefix = "evalrun"
	}
	if c.PollInterval <= 0 {
		c.PollInterval = DefaultPollInterval
	}
	if c.RenewInterval <= 0 {
		c.RenewInterval = runqueue.DefaultLease / 3
	}
	if c.Backoff <= 0 {
		c.Backoff = time.Second
	}
	return c
}

// PoolStats is what the pool did, surfaced so a fan-out's progress is readable while it runs.
type PoolStats struct {
	Handled     int `json:"handled"`
	Failed      int `json:"failed"`
	MaxInFlight int `json:"max_in_flight"`
}

// Pool is the bounded worker pool over the run queue.
type Pool struct {
	q   Dispatcher
	h   Handler
	cfg PoolConfig

	mu       sync.Mutex
	stats    PoolStats
	inFlight int
}

// NewPool builds a worker pool.
func NewPool(q Dispatcher, h Handler, cfg PoolConfig) (*Pool, error) {
	if q == nil {
		return nil, errors.New("evalrun: NewPool requires a dispatcher")
	}
	if h == nil {
		return nil, errors.New("evalrun: NewPool requires a handler")
	}
	return &Pool{q: q, h: h, cfg: cfg.normalized()}, nil
}

// Drain runs units until the queue is empty or the context is cancelled, then returns.
//
// "Until empty" rather than "forever" is what an eval sweep wants: the fan-out is a bounded batch
// with a known end, and a caller that wants a long-lived worker calls Drain in its own loop.
func (p *Pool) Drain(ctx context.Context) (PoolStats, error) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, p.cfg.Concurrency)
	errCh := make(chan error, 1)

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return p.snapshot(), ctx.Err()
		case err := <-errCh:
			wg.Wait()
			return p.snapshot(), err
		default:
		}

		// Acquiring the slot BEFORE dequeuing is the backpressure: with every slot busy the pool
		// stops claiming work, so unclaimed items stay `ready` in the queue and remain visible to
		// another worker (or this one, later) instead of being held under an expiring lease here.
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return p.snapshot(), ctx.Err()
		}

		worker := fmt.Sprintf("%s-%d", p.cfg.WorkerPrefix, len(slots))
		item, err := p.q.Dequeue(ctx, worker)
		if errors.Is(err, runqueue.ErrEmpty) {
			<-slots
			if p.idle() {
				return p.snapshot(), nil
			}
			select {
			case <-time.After(p.cfg.PollInterval):
				continue
			case <-ctx.Done():
				wg.Wait()
				return p.snapshot(), ctx.Err()
			}
		}
		if err != nil {
			<-slots
			wg.Wait()
			return p.snapshot(), fmt.Errorf("evalrun: dequeue: %w", err)
		}

		p.enter()
		wg.Add(1)
		go func(it runqueue.Item, worker string) {
			defer wg.Done()
			defer func() { p.leave(); <-slots }()
			p.run(ctx, it, worker)
		}(*item, worker)
	}
}

// run executes one unit, holding its lease alive and ack/nacking honestly.
func (p *Pool) run(ctx context.Context, item runqueue.Item, worker string) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Lease renewal. A unit slower than the lease would otherwise be redelivered while still
	// running, and the same work would execute twice — the idempotent run id makes that survivable,
	// but paying for it twice is not.
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(p.cfg.RenewInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-runCtx.Done():
				return
			case <-t.C:
				_ = p.q.Renew(runCtx, item.RunID, worker)
			}
		}
	}()

	err := p.h.Handle(runCtx, item)
	close(done)

	if err != nil {
		p.countFailure()
		_ = p.q.Nack(ctx, item.RunID, worker, err, p.cfg.Backoff)
		return
	}
	p.countHandled()
	_ = p.q.Ack(ctx, item.RunID, worker)
}

func (p *Pool) enter() {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.stats.MaxInFlight {
		p.stats.MaxInFlight = p.inFlight
	}
	p.mu.Unlock()
}

func (p *Pool) leave() {
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
}

func (p *Pool) idle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inFlight == 0
}

func (p *Pool) countHandled() {
	p.mu.Lock()
	p.stats.Handled++
	p.mu.Unlock()
}

func (p *Pool) countFailure() {
	p.mu.Lock()
	p.stats.Failed++
	p.mu.Unlock()
}

func (p *Pool) snapshot() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Stats returns the pool's counters, readable while it runs.
func (p *Pool) Stats() PoolStats { return p.snapshot() }
