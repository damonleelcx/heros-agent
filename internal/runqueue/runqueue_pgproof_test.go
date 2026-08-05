//go:build pgproof

// Live-Postgres proof for the run queue (task 6.3).
//
// There is no unit-test half of this package worth writing. Everything it does that could be wrong —
// SKIP LOCKED handing two workers disjoint items, a lease expiring into a redelivery, an atomic claim
// under contention — is a property of Postgres's concurrency semantics. A mocked queue would only
// prove the mock agrees with itself.
package runqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/lib/pq"
)

var testDB *sql.DB

const cfgHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_runqueue")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	// 🔴 The FULL embedded set, exactly as a booting deployment applies it.
	//
	// This used to hand-list the handful of migrations this package's own tables need. That is the
	// pattern `internal/pgmigrate`'s header names as the reason nothing in CI applied anything past
	// ~0009: a proof against its own subset is a proof against a schema no deployment has, and it goes
	// red the first time another phase adds a column to a table this package writes. P27 adding
	// `run.tenant_id` was that first time.
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		fmt.Fprintf(os.Stderr, "apply migrations: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func seedLineage(t *testing.T) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf','local://t','abc','go','1.0.0') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v','wf','base') ON CONFLICT DO NOTHING`,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ('` + cfgHash + `','v','wf','1.0.0','{}') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant_spec (config_hash, source_revision, spec_json)
		 VALUES ('` + cfgHash + `','rev1','{}') ON CONFLICT DO NOTHING`,
		`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		 VALUES ('` + cfgHash + `','rev1','built','type-checked') ON CONFLICT DO NOTHING`,
	} {
		if _, err := testDB.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// clean gives each test an empty queue: these assert on depth and on who gets what, and a neighbour's
// leftovers would make them pass or fail on execution order.
func clean(t *testing.T) {
	t.Helper()
	if _, err := testDB.Exec(`DELETE FROM run_queue`); err != nil {
		t.Fatalf("clean: %v", err)
	}
}

func TestPG_EnqueueDequeueAck(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB)

	if err := q.Enqueue(ctx, "r1", cfgHash, "rev1", 42); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	it, err := q.Dequeue(ctx, "w1")
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if it.RunID != "r1" || it.Seed != 42 || it.Attempts != 1 {
		t.Errorf("item = %+v", it)
	}
	// Leased: nobody else may take it.
	if _, err := q.Dequeue(ctx, "w2"); !errors.Is(err, ErrEmpty) {
		t.Errorf("a leased item was dispatched to a second worker: %v", err)
	}
	if err := q.Ack(ctx, "r1", "w1"); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if s, _ := q.Stats(ctx); s.Done != 1 || s.Ready != 0 || s.Leased != 0 {
		t.Errorf("stats = %+v, want 1 done", s)
	}
}

// Enqueue is idempotent on run_id: a submitter retrying its own submit must not queue the work twice.
func TestPG_EnqueueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB)
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, "r_idem", cfgHash, "rev1", 1); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	if s, _ := q.Stats(ctx); s.Ready != 1 {
		t.Errorf("stats = %+v, want exactly 1 ready", s)
	}
}

// SKIP LOCKED is what lets N workers pull DISJOINT items with no coordination. Without it they
// serialize on the head row and the fan-out is a queue of one; worse, a read-then-update would let
// two workers claim the same item and run one variant twice.
//
// Driven with real concurrent workers, because that is the only way the race can appear.
func TestPG_ConcurrentWorkersGetDisjointItems(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB)

	const n = 20
	for i := 0; i < n; i++ {
		if err := q.Enqueue(ctx, fmt.Sprintf("r_c%02d", i), cfgHash, "rev1", int64(i)); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	var mu sync.Mutex
	claimed := map[string]string{} // run_id -> worker
	var wg sync.WaitGroup
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			worker := fmt.Sprintf("w%d", w)
			for {
				it, err := q.Dequeue(ctx, worker)
				if errors.Is(err, ErrEmpty) {
					return
				}
				if err != nil {
					t.Errorf("Dequeue: %v", err)
					return
				}
				mu.Lock()
				if prev, dup := claimed[it.RunID]; dup {
					t.Errorf("%s was dispatched to BOTH %s and %s; one variant would run twice",
						it.RunID, prev, worker)
				}
				claimed[it.RunID] = worker
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if len(claimed) != n {
		t.Errorf("%d of %d items were dispatched; the rest were lost", len(claimed), n)
	}
}

// At-least-once: a worker that dies holds a lease that expires, and the item comes back. This is the
// property the whole design rests on — without it a dead worker's run is silently lost, and P4 scores
// a variant that never ran.
func TestPG_ExpiredLeaseIsRedelivered(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB, WithLease(100*time.Millisecond))

	if err := q.Enqueue(ctx, "r_lease", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	first, err := q.Dequeue(ctx, "w_dead")
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	// w_dead now "dies": it never acks.
	time.Sleep(200 * time.Millisecond)

	second, err := q.Dequeue(ctx, "w_live")
	if err != nil {
		t.Fatalf("the item was not redelivered after its lease expired: %v", err)
	}
	if second.RunID != first.RunID {
		t.Fatalf("redelivered %s, want %s", second.RunID, first.RunID)
	}
	// The SAME run_id — which is what makes every idempotency guarantee downstream apply. A run_id
	// minted per delivery would make each redelivery a new run, and every one would be billed.
	if second.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 — a redelivery must be visible, not silent", second.Attempts)
	}
	// The dead worker's ack must not close an item another worker now holds.
	if err := q.Ack(ctx, "r_lease", "w_dead"); !errors.Is(err, ErrNotLeased) {
		t.Errorf("the dead worker acked an item it had lost: %v", err)
	}
}

// Renew is why the lease can stay short enough to detect a dead worker quickly without redelivering
// a long run out from under a live one.
func TestPG_RenewKeepsALongRunFromBeingRedelivered(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB, WithLease(300*time.Millisecond))

	if err := q.Enqueue(ctx, "r_renew", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Dequeue(ctx, "w1"); err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	// A worker on a long run, renewing as it goes.
	for i := 0; i < 3; i++ {
		time.Sleep(150 * time.Millisecond)
		if err := q.Renew(ctx, "r_renew", "w1"); err != nil {
			t.Fatalf("Renew %d: %v", i, err)
		}
		if _, err := q.Dequeue(ctx, "w2"); !errors.Is(err, ErrEmpty) {
			t.Fatalf("a renewed lease was redelivered underneath its worker: %v", err)
		}
	}
	if err := q.Ack(ctx, "r_renew", "w1"); err != nil {
		t.Fatalf("Ack: %v", err)
	}
}

// Unbounded redelivery of a deterministically-failing run is an infinite loop that spends money on
// every lap. It is parked for a human — never silently dropped, because a run that vanished is
// indistinguishable from one never submitted.
func TestPG_ExhaustedItemIsDeadLetteredNotDroppedOrLooped(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB, WithMaxAttempts(2))

	if err := q.Enqueue(ctx, "r_dead", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	for i := 1; i <= 2; i++ {
		it, err := q.Dequeue(ctx, "w1")
		if err != nil {
			t.Fatalf("Dequeue %d: %v", i, err)
		}
		if it.Attempts != i {
			t.Errorf("Attempts = %d, want %d", it.Attempts, i)
		}
		if err := q.Nack(ctx, "r_dead", "w1", fmt.Errorf("boom %d", i), 0); err != nil {
			t.Fatalf("Nack %d: %v", i, err)
		}
	}
	// The third dispatch is over budget: parked, not handed out.
	if _, err := q.Dequeue(ctx, "w1"); !errors.Is(err, ErrEmpty) {
		t.Errorf("an exhausted item was dispatched again: %v", err)
	}
	s, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.Dead != 1 {
		t.Errorf("stats = %+v, want 1 dead", s)
	}
	// And it says why — a parked item nobody can diagnose is one nobody will revive.
	var last string
	if err := testDB.QueryRowContext(ctx, `SELECT last_error FROM run_queue WHERE run_id='r_dead'`).Scan(&last); err != nil {
		t.Fatalf("read: %v", err)
	}
	if last == "" {
		t.Error("the dead letter has no reason recorded")
	}
}

// THE test for this package. At-least-once dispatch is only safe because execution is idempotent —
// so redelivering a run and executing it twice must leave exactly one set of records and one charge.
//
// If this fails, the queue is a machine for double-billing and at-least-once was the wrong choice.
func TestPG_RedeliveryDoesNotDoubleWriteOrDoubleCharge(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB, WithLease(100*time.Millisecond))
	es := executor.NewStore(testDB)

	if err := q.Enqueue(ctx, "r_once", cfgHash, "rev1", 7); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// runOnce is what a worker does: claim, start, record a node, finish.
	runOnce := func(worker string) *Item {
		it, err := q.Dequeue(ctx, worker)
		if err != nil {
			t.Fatalf("Dequeue(%s): %v", worker, err)
		}
		// Start is idempotent-by-conflict: the redelivery's Start hits the existing run row.
		_ = es.Start(ctx, it.RunID, it.ConfigHash, it.SourceRevision, it.Seed, "acme")
		if err := es.RecordNode(ctx, it.RunID, executor.NodeResult{
			NodeID: "n_a", AttemptGroup: 0, Status: executor.StatusSucceeded,
			IdempotencyKey: executor.IdempotencyKey(it.RunID, "n_a", 0),
		}); err != nil {
			t.Fatalf("RecordNode(%s): %v", worker, err)
		}
		if err := es.Finish(ctx, &executor.Run{RunID: it.RunID, Status: executor.StatusSucceeded}); err != nil {
			t.Fatalf("Finish(%s): %v", worker, err)
		}
		return it
	}

	first := runOnce("w_slow") // executes, then "dies" before acking
	time.Sleep(200 * time.Millisecond)
	second := runOnce("w_retry") // the redelivery executes the same run again

	if first.RunID != second.RunID {
		t.Fatalf("the redelivery ran a different run_id (%s vs %s); every idempotency guarantee "+
			"downstream keys on it being the same", second.RunID, first.RunID)
	}

	// One node execution, not two: node_execution's PRIMARY KEY (task 5.2).
	var nodes int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM node_execution WHERE run_id='r_once'`).Scan(&nodes); err != nil {
		t.Fatalf("count: %v", err)
	}
	if nodes != 1 {
		t.Errorf("a redelivered run wrote %d node_execution rows, want 1 — at-least-once dispatch "+
			"just became a duplicate result", nodes)
	}
	// One idempotency key, so the provider would have billed once (task 5.1).
	var keys int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(DISTINCT idempotency_key) FROM node_execution WHERE run_id='r_once'`).Scan(&keys); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if keys != 1 {
		t.Errorf("the redelivery produced %d distinct idempotency keys, want 1 — the provider would "+
			"bill the same call twice", keys)
	}
	// One run, still terminal with its original verdict.
	got, err := es.Get(ctx, "r_once")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != executor.StatusSucceeded {
		t.Errorf("run status = %q after redelivery", got.Status)
	}

	if err := q.Ack(ctx, "r_once", "w_retry"); err != nil {
		t.Fatalf("Ack: %v", err)
	}
}

// A queue item for a configuration that was never transformed would be dispatched and fail on every
// attempt until it dead-lettered — a slow, expensive way to learn the spec was never applied.
func TestPG_CannotEnqueueARunOfAnAbsentTransform(t *testing.T) {
	seedLineage(t)
	err := New(testDB).Enqueue(context.Background(), "r_ghost", cfgHash, "no-such-rev", 1)
	if err == nil {
		t.Fatal("a run of a non-existent transform was enqueued")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || string(pqErr.Code) != "23503" {
		t.Errorf("rejected, but not by the FK (23503): %v", err)
	}
}

// A lease with no expiry never expires, so its run would never be redelivered — the one failure
// at-least-once exists to prevent.
func TestPG_LeaseWithoutAnExpiryIsRejectedByTheDatabase(t *testing.T) {
	seedLineage(t)
	clean(t)
	_, err := testDB.Exec(
		`INSERT INTO run_queue (run_id, config_hash, source_revision, seed, state)
		 VALUES ('r_noexp','` + cfgHash + `','rev1',1,'leased')`)
	if err == nil {
		t.Fatal("a leased item with no expiry was accepted; it would never be redelivered")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || string(pqErr.Code) != "23514" {
		t.Errorf("rejected, but not by the CHECK (23514): %v", err)
	}
}

func TestPG_AckOfAnUnknownItemIsErrNotLeased(t *testing.T) {
	clean(t)
	err := New(testDB).Ack(context.Background(), "nope", "w1")
	if !errors.Is(err, ErrNotLeased) {
		t.Fatalf("want ErrNotLeased, got %v", err)
	}
}

// Nack's backoff must actually hold the item back, or a failing run spins as fast as the loop.
func TestPG_NackBackoffDelaysRedelivery(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB)
	if err := q.Enqueue(ctx, "r_backoff", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Dequeue(ctx, "w1"); err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if err := q.Nack(ctx, "r_backoff", "w1", errors.New("transient"), 30*time.Second); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if _, err := q.Dequeue(ctx, "w1"); !errors.Is(err, ErrEmpty) {
		t.Errorf("a backed-off item was dispatched immediately: %v", err)
	}
}

// The P8 operator surface, proved against the live table (P8 task 6.1/6.2): an operator can SEE the
// queue, CANCEL a running item with a reason that survives on the row, and RETRY a parked one — and
// the state machine refuses the two operations that would corrupt a run (retrying something a worker
// still holds, cancelling something already finished).
func TestPG_OperatorListCancelRetry(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	clean(t)
	q := New(testDB)

	for _, id := range []string{"op1", "op2"} {
		if err := q.Enqueue(ctx, id, cfgHash, "rev1", 1); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}
	it, err := q.Dequeue(ctx, "w1")
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	// ── List shows both, with the lease holder visible ──
	jobs, err := q.List(ctx, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("List returned %d jobs, want 2", len(jobs))
	}
	var leased *Job
	for i := range jobs {
		if jobs[i].RunID == it.RunID {
			leased = &jobs[i]
		}
	}
	if leased == nil || leased.State != "leased" || leased.LeasedBy != "w1" || leased.LeaseExpiresAt == nil {
		t.Fatalf("the leased item does not report its holder: %+v", leased)
	}

	// ── Cancel the running item: the reason survives on the row ──
	const why = "cancelled by operator: incident INC-77"
	if err := q.Cancel(ctx, it.RunID, why); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	jobs, _ = q.List(ctx, 100)
	for _, j := range jobs {
		if j.RunID == it.RunID {
			if j.State != "dead" || j.DeadLetterReason != why {
				t.Errorf("cancelled item = %+v, want dead with the operator's reason", j)
			}
			if j.LeasedBy != "" || j.LeaseExpiresAt != nil {
				t.Errorf("a cancelled item still holds a lease: %+v", j)
			}
		}
	}
	// A cancelled item is not dispatchable.
	next, err := q.Dequeue(ctx, "w2")
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if next.RunID == it.RunID {
		t.Error("a cancelled item was dispatched again")
	}

	// ── Retry the parked item: clean ready, attempts reset ──
	if err := q.Requeue(ctx, it.RunID); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	jobs, _ = q.List(ctx, 100)
	for _, j := range jobs {
		if j.RunID == it.RunID {
			if j.State != "ready" || j.Attempts != 0 || j.DeadLetterReason != "" {
				t.Errorf("retried item = %+v, want a clean ready item", j)
			}
		}
	}

	// ── The two refusals ──
	if err := q.Requeue(ctx, next.RunID); !errors.Is(err, ErrNotRetryable) {
		t.Errorf("retrying a leased item: err = %v, want ErrNotRetryable", err)
	}
	if err := q.Ack(ctx, next.RunID, "w2"); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := q.Cancel(ctx, next.RunID, "too late"); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling a finished item: err = %v, want ErrNotCancellable", err)
	}
	if err := q.Cancel(ctx, "op-unknown", ""); err == nil {
		t.Error("a cancel with no reason was accepted")
	}
}

func TestPG_ZZ_DownMigrationRemovesTheQueueOnly(t *testing.T) {
	ctx := context.Background()
	down, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", "0006_p2_run_queue.down.sql"))
	if err != nil {
		t.Fatalf("read down: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	var exists bool
	if err := testDB.QueryRowContext(ctx, `SELECT to_regclass('run_queue') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Error("run_queue survived the down migration")
	}
	// Expand-only: the durable records the queue merely pointed at must remain.
	for _, obj := range []string{"run", "node_execution", "transform"} {
		var ok bool
		if err := testDB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, obj).Scan(&ok); err != nil {
			t.Fatalf("check %s: %v", obj, err)
		}
		if !ok {
			t.Errorf("the 0006 rollback removed %s, which it does not own", obj)
		}
	}
	_ = strings.TrimSpace
}
