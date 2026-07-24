// Package runqueue dispatches runs to workers (task 6.3).
//
// # At-least-once, and why that is the honest choice
//
// PRD OQ4 asks: at-least-once with idempotent execution, or exactly-once? Exactly-once delivery is
// not available. A worker that died mid-run and a worker that is merely slow look identical from
// here — the queue cannot tell them apart, so it must choose which way to be wrong:
//
//	at-most-once   drop the run when a worker goes quiet. For an eval platform this is the worse
//	               failure by far: P4 scores a variant that silently never ran.
//	at-least-once  redeliver on lease expiry, and make re-execution free.
//
// This package takes at-least-once, and that is only defensible because the idempotency it depends on
// already exists and is proved:
//
//   - the run_id is allocated at ENQUEUE, so a redelivery re-executes the SAME run;
//   - node_execution's PRIMARY KEY (run_id, node_id, attempt_group) makes a double-write a caught
//     conflict rather than a duplicate row (task 5.2);
//   - the provider-facing idempotency key makes a re-executed node one charge (task 5.1);
//   - run.Finish refuses to overwrite a terminal verdict.
//
// Take those away and this package becomes a machine for double-billing. That is the whole design,
// and TestPG_RedeliveryDoesNotDoubleWriteOrDoubleCharge is where it is checked rather than asserted.
package runqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DefaultLease is how long a worker holds an item before it is redeliverable.
//
// It bounds how long a dead worker's run sits undetected, so it wants to be short; but an item
// redelivered while its first worker is still running gets executed twice, which is safe but wastes a
// build and a fan-out slot — so it wants to be long. Five minutes sits above P2's transform+build
// time and well under a human's patience. Workers renew it (see Renew), so a long run is not a
// reason to raise it.
const DefaultLease = 5 * time.Minute

// DefaultMaxAttempts bounds redelivery before an item is parked for a human.
//
// Unbounded redelivery of a run that fails deterministically is an infinite loop that spends money on
// every lap. Three is the same bound the gateway uses for provider retries.
const DefaultMaxAttempts = 3

var (
	// ErrEmpty means nothing is dispatchable right now. Not an error condition — the normal state of
	// an idle queue — so it is a sentinel rather than a nil item a caller might dereference.
	ErrEmpty = errors.New("runqueue: nothing to dispatch")
	// ErrNotLeased means an Ack/Nack/Renew names an item this worker does not hold. Returned rather
	// than ignored: a worker acking an item it lost to a lease expiry is a worker whose results may
	// already have been superseded, and it should learn that.
	ErrNotLeased = errors.New("runqueue: this item is not leased by this worker")
	// ErrNotRetryable means an operator retry named an item that is not dead-lettered. Retrying a
	// leased item would put two workers on one run; retrying a done one would re-execute finished work.
	ErrNotRetryable = errors.New("runqueue: only a dead-lettered item can be retried")
	// ErrNotCancellable means an operator cancel named an item that is not ready or leased.
	ErrNotCancellable = errors.New("runqueue: only a queued or running item can be cancelled")
)

// Item is one dispatched run.
type Item struct {
	RunID          string
	ConfigHash     string
	SourceRevision string
	Seed           int64
	// Attempts is how many times this item has been dispatched, including this one. Surfaced so a
	// worker can tell a first attempt from a redelivery — and so a run that is being retried is
	// visible rather than silently repeating.
	Attempts int
}

// Queue is a Postgres-backed run queue.
type Queue struct {
	db          *sql.DB
	lease       time.Duration
	maxAttempts int
}

type Option func(*Queue)

// WithLease sets how long a dispatched item is held before redelivery.
func WithLease(d time.Duration) Option { return func(q *Queue) { q.lease = d } }

// WithMaxAttempts bounds redelivery before dead-lettering.
func WithMaxAttempts(n int) Option { return func(q *Queue) { q.maxAttempts = n } }

func New(db *sql.DB, opts ...Option) *Queue {
	q := &Queue{db: db, lease: DefaultLease, maxAttempts: DefaultMaxAttempts}
	for _, o := range opts {
		o(q)
	}
	return q
}

// interval renders a Go duration as a Postgres interval, in MILLISECONDS.
//
// Milliseconds, not seconds, and this is not a style choice. The obvious
// `fmt.Sprintf("%d seconds", int(d.Seconds()))` truncates: any duration under a second becomes
// "0 seconds" — a lease that has already expired the instant it is taken. Every item would then be
// immediately redeliverable, so every run would be executed by as many workers as happened to poll,
// and at-least-once would quietly become at-least-N-times. Production never sees it (DefaultLease is
// 5 minutes), which is exactly what makes it the kind of bug that ships: only a test with a short
// lease hits it, and a test with a short lease that ALSO expects expiry passes for the wrong reason.
func interval(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%d milliseconds", ms)
}

// Enqueue submits a run of an already-built transform.
//
// runID is the caller's, and it is the identity everything downstream keys on. Idempotent on it:
// enqueueing the same run_id twice is a no-op, so a submitter that retries its own submit does not
// queue the work twice.
func (q *Queue) Enqueue(ctx context.Context, runID, configHash, sourceRevision string, seed int64) error {
	if runID == "" {
		return fmt.Errorf("runqueue: Enqueue requires a run_id")
	}
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO run_queue (run_id, config_hash, source_revision, seed)
		 VALUES ($1, $2, $3, $4) ON CONFLICT (run_id) DO NOTHING`,
		runID, configHash, sourceRevision, seed)
	if err != nil {
		return fmt.Errorf("runqueue: enqueue %s: %w", runID, err)
	}
	return nil
}

// Dequeue claims one dispatchable item for worker.
//
// The claim is a single UPDATE over a `FOR UPDATE SKIP LOCKED` subselect. Both halves matter:
//
//   - SKIP LOCKED is what lets N workers pull disjoint items with no coordination. Without it they
//     serialize on the same head row and the fan-out is a queue of one.
//   - Doing it in ONE statement makes the claim atomic. A read-then-update would let two workers read
//     the same row and both claim it — the classic queue race, and here it would mean two executors
//     running one variant.
//
// Dispatchable is `ready and visible` OR `leased and expired`. Reclaiming expired leases in the same
// statement is what makes redelivery happen at all, with no sweeper process to run or forget to run.
func (q *Queue) Dequeue(ctx context.Context, worker string) (*Item, error) {
	if worker == "" {
		return nil, fmt.Errorf("runqueue: Dequeue requires a worker id")
	}
	var it Item
	err := q.db.QueryRowContext(ctx,
		`UPDATE run_queue
		    SET state = 'leased',
		        attempts = attempts + 1,
		        leased_by = $1,
		        lease_expires_at = now() + $2::interval
		  WHERE run_id = (
		        SELECT run_id FROM run_queue
		         WHERE (state = 'ready'  AND visible_at <= now())
		            OR (state = 'leased' AND lease_expires_at < now())
		         ORDER BY enqueued_at
		         FOR UPDATE SKIP LOCKED
		         LIMIT 1
		  )
		 RETURNING run_id, config_hash, source_revision, seed, attempts`,
		worker, interval(q.lease)).
		Scan(&it.RunID, &it.ConfigHash, &it.SourceRevision, &it.Seed, &it.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEmpty
	}
	if err != nil {
		return nil, fmt.Errorf("runqueue: dequeue: %w", err)
	}

	// Over its attempt budget: park it rather than dispatch it again. Checked AFTER the claim so the
	// decision is made under the same lock that handed it out — a pre-check would race another worker
	// into dispatching the very attempt this one is refusing.
	if it.Attempts > q.maxAttempts {
		if err := q.kill(ctx, it.RunID, fmt.Sprintf(
			"exhausted %d attempts without completing", q.maxAttempts)); err != nil {
			return nil, err
		}
		return nil, ErrEmpty
	}
	return &it, nil
}

// Ack marks a dispatched item complete.
//
// Conditional on this worker still holding the lease. A worker whose lease expired mid-run has had
// its item redelivered, and letting it ack anyway would mark done a run another worker is still
// executing — closing the item while work is in flight.
func (q *Queue) Ack(ctx context.Context, runID, worker string) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE run_queue SET state='done', leased_by=NULL, lease_expires_at=NULL
		  WHERE run_id=$1 AND state='leased' AND leased_by=$2`, runID, worker)
	if err != nil {
		return fmt.Errorf("runqueue: ack %s: %w", runID, err)
	}
	return q.affectedOne(ctx, res, runID, worker, "ack")
}

// Nack returns an item for redelivery after a failure, with a backoff.
//
// The error is recorded on the item, not just logged: an item that has failed twice with the same
// message is the signal that redelivery is pointless, and it belongs where whoever looks at the queue
// will see it.
func (q *Queue) Nack(ctx context.Context, runID, worker string, cause error, backoff time.Duration) error {
	msg := "(no reason given)"
	if cause != nil {
		msg = cause.Error()
	}
	res, err := q.db.ExecContext(ctx,
		`UPDATE run_queue
		    SET state='ready', leased_by=NULL, lease_expires_at=NULL,
		        visible_at = now() + $3::interval, last_error=$4
		  WHERE run_id=$1 AND state='leased' AND leased_by=$2`,
		runID, worker, interval(backoff), msg)
	if err != nil {
		return fmt.Errorf("runqueue: nack %s: %w", runID, err)
	}
	return q.affectedOne(ctx, res, runID, worker, "nack")
}

// Renew extends a lease. A worker on a long run calls this so its item is not redelivered underneath
// it — which is why DefaultLease can stay short enough to detect a dead worker quickly.
func (q *Queue) Renew(ctx context.Context, runID, worker string) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE run_queue SET lease_expires_at = now() + $3::interval
		  WHERE run_id=$1 AND state='leased' AND leased_by=$2`,
		runID, worker, interval(q.lease))
	if err != nil {
		return fmt.Errorf("runqueue: renew %s: %w", runID, err)
	}
	return q.affectedOne(ctx, res, runID, worker, "renew")
}

// kill parks an item for a human. Never a silent drop: a run that vanished is indistinguishable from
// one that was never submitted, and PRD §9 requires a stuck run be legible.
func (q *Queue) kill(ctx context.Context, runID, reason string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE run_queue SET state='dead', leased_by=NULL, lease_expires_at=NULL, last_error=$2
		  WHERE run_id=$1`, runID, reason)
	if err != nil {
		return fmt.Errorf("runqueue: dead-letter %s: %w", runID, err)
	}
	return nil
}

// affectedOne turns "0 rows updated" into a diagnosis rather than a shrug.
func (q *Queue) affectedOne(ctx context.Context, res sql.Result, runID, worker, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("runqueue: %s %s: rows affected: %w", op, runID, err)
	}
	if n == 1 {
		return nil
	}
	var state, holder sql.NullString
	err = q.db.QueryRowContext(ctx,
		`SELECT state, leased_by FROM run_queue WHERE run_id=$1`, runID).Scan(&state, &holder)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s does not exist", ErrNotLeased, runID)
	}
	if err != nil {
		return fmt.Errorf("runqueue: %s: read-back: %w", op, err)
	}
	return fmt.Errorf("%w: %s to %s is %s (held by %q, not %q) — the lease was probably lost to an "+
		"expiry and the item redelivered", ErrNotLeased, op, runID, state.String, holder.String, worker)
}

// Stats is the queue's depth by state — the operability read (task 6.4). Externally readable, because
// a health signal that only exists in a log or in memory is not one.
type Stats struct{ Ready, Leased, Done, Dead int }

func (q *Queue) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	rows, err := q.db.QueryContext(ctx, `SELECT state, count(*) FROM run_queue GROUP BY state`)
	if err != nil {
		return s, fmt.Errorf("runqueue: stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return s, fmt.Errorf("runqueue: stats: scan: %w", err)
		}
		switch state {
		case "ready":
			s.Ready = n
		case "leased":
			s.Leased = n
		case "done":
			s.Done = n
		case "dead":
			s.Dead = n
		}
	}
	return s, rows.Err()
}

// ── Operator surface (P8 task 6.1) ──────────────────────────────────────────────────────────────
//
// These three exist so the P8 operator console can view, retry and cancel jobs on THIS queue rather
// than standing up a second one. They live here, in the package that owns the table, for the same
// reason the console does not own tenant status: one fact, one place that can change it.

// Job is one queue item as an operator sees it.
type Job struct {
	RunID          string     `json:"run_id"`
	ConfigHash     string     `json:"config_hash"`
	SourceRevision string     `json:"source_revision"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	LeasedBy       string     `json:"leased_by,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	EnqueuedAt     time.Time  `json:"enqueued_at"`
	// DeadLetterReason is why a parked item was parked. A dead letter with no reason is one nobody
	// will revive, which is why the table requires it.
	DeadLetterReason string `json:"dead_letter_reason,omitempty"`
}

// List returns up to limit queue items, newest first. Read-only.
func (q *Queue) List(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT run_id, config_hash, source_revision, state, attempts,
		        COALESCE(leased_by,''), lease_expires_at, enqueued_at, COALESCE(dead_letter_reason,'')
		   FROM run_queue ORDER BY enqueued_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("runqueue: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		var j Job
		var expires sql.NullTime
		if err := rows.Scan(&j.RunID, &j.ConfigHash, &j.SourceRevision, &j.State, &j.Attempts,
			&j.LeasedBy, &expires, &j.EnqueuedAt, &j.DeadLetterReason); err != nil {
			return nil, fmt.Errorf("runqueue: list: scan: %w", err)
		}
		if expires.Valid {
			t := expires.Time
			j.LeaseExpiresAt = &t
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Requeue returns a dead-lettered item to the ready state so it is dispatched again — the operator
// "retry".
//
// It resets attempts, because the attempt budget exists to stop an automatic redelivery loop, and a
// human deciding to retry after diagnosing the failure is not that loop. It refuses anything that is
// not dead: retrying a leased item would produce two workers on one run, and retrying a done item
// would re-execute completed work.
func (q *Queue) Requeue(ctx context.Context, runID string) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE run_queue
		    SET state='ready', attempts=0, visible_at=now(),
		        leased_by=NULL, lease_expires_at=NULL, dead_letter_reason=NULL
		  WHERE run_id=$1 AND state='dead'`, runID)
	if err != nil {
		return fmt.Errorf("runqueue: requeue %s: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("runqueue: requeue %s: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s is not a dead-lettered item — only a parked job can be retried", ErrNotRetryable, runID)
	}
	return nil
}

// Cancel parks a ready or leased item with the operator's reason — the operator "cancel".
//
// It records the reason in the same dead_letter_reason column an exhausted item uses, so a cancelled
// job and an exhausted one are both diagnosable from one field, and neither can be parked silently.
func (q *Queue) Cancel(ctx context.Context, runID, reason string) error {
	if reason == "" {
		return fmt.Errorf("runqueue: cancelling %s requires a reason", runID)
	}
	res, err := q.db.ExecContext(ctx,
		`UPDATE run_queue
		    SET state='dead', leased_by=NULL, lease_expires_at=NULL, dead_letter_reason=$2
		  WHERE run_id=$1 AND state IN ('ready','leased')`, runID, reason)
	if err != nil {
		return fmt.Errorf("runqueue: cancel %s: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("runqueue: cancel %s: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s is not running or queued — a finished job cannot be cancelled", ErrNotCancellable, runID)
	}
	return nil
}
