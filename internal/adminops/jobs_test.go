package adminops_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/runqueue"
)

// jobs_test.go covers task 6.2 — view/retry/cancel a job (cancel gated + audited) and fleet health
// derived from the EXISTING queue (FR11).
//
// The queue under test is a seam implementation with the same state machine as the Postgres one:
// ready/leased/done/dead, retry only from dead, cancel only from ready or leased. The live SQL is
// proved in internal/runqueue's pgproof suite against a real Postgres, where those semantics
// actually live; what belongs HERE is the operator discipline layered on top — the gate, the reason,
// the audit entry, and the fact that no second queue exists.

// seamQueue is an in-process queue with the run_queue state machine.
type seamQueue struct {
	mu   sync.Mutex
	jobs map[string]*runqueue.Job
	// failStats and failList make the queue unreadable, so the degraded rendering can be exercised.
	failStats bool
	failList  bool
	// cancelReasons records the reason each cancel carried into the queue, proving the operator's
	// reason reaches the job's own record and not only the audit chain.
	cancelReasons map[string]string
}

func newSeamQueue() *seamQueue {
	return &seamQueue{jobs: map[string]*runqueue.Job{}, cancelReasons: map[string]string{}}
}

func (q *seamQueue) put(j runqueue.Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cp := j
	q.jobs[j.RunID] = &cp
}

func (q *seamQueue) get(runID string) (runqueue.Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[runID]
	if !ok {
		return runqueue.Job{}, false
	}
	return *j, true
}

func (q *seamQueue) List(_ context.Context, limit int) ([]runqueue.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failList {
		return nil, errors.New("queue unreachable")
	}
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

func (q *seamQueue) Requeue(_ context.Context, runID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[runID]
	if !ok || j.State != "dead" {
		return runqueue.ErrNotRetryable
	}
	j.State, j.Attempts, j.LeasedBy, j.LeaseExpiresAt, j.DeadLetterReason = "ready", 0, "", nil, ""
	return nil
}

func (q *seamQueue) Cancel(_ context.Context, runID, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[runID]
	if !ok || (j.State != "ready" && j.State != "leased") {
		return runqueue.ErrNotCancellable
	}
	j.State, j.LeasedBy, j.LeaseExpiresAt, j.DeadLetterReason = "dead", "", nil, reason
	q.cancelReasons[runID] = reason
	return nil
}

func (q *seamQueue) Stats(_ context.Context) (runqueue.Stats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failStats {
		return runqueue.Stats{}, errors.New("queue unreachable")
	}
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

func (q *seamQueue) Describe() string { return "seam:run_queue(p4/p6)" }

// seedJobs puts a running, a queued, a finished and a parked job on the queue.
func (h *harness) seedJobs() *seamQueue {
	q := h.jobQueue
	base := h.clk.now()
	expired := base.Add(-1 * time.Minute)
	future := base.Add(4 * time.Minute)
	q.put(runqueue.Job{RunID: "run-running", ConfigHash: "cfg1", SourceRevision: "rev1", State: "leased",
		Attempts: 1, LeasedBy: "worker-a", LeaseExpiresAt: &future, EnqueuedAt: base.Add(-2 * time.Minute)})
	q.put(runqueue.Job{RunID: "run-stuck", ConfigHash: "cfg2", SourceRevision: "rev1", State: "leased",
		Attempts: 2, LeasedBy: "worker-b", LeaseExpiresAt: &expired, EnqueuedAt: base.Add(-20 * time.Minute)})
	q.put(runqueue.Job{RunID: "run-queued", ConfigHash: "cfg3", SourceRevision: "rev1", State: "ready",
		EnqueuedAt: base.Add(-1 * time.Minute)})
	q.put(runqueue.Job{RunID: "run-done", ConfigHash: "cfg4", SourceRevision: "rev1", State: "done",
		EnqueuedAt: base.Add(-30 * time.Minute)})
	q.put(runqueue.Job{RunID: "run-parked", ConfigHash: "cfg5", SourceRevision: "rev1", State: "dead",
		Attempts: 3, DeadLetterReason: "exhausted 3 attempts without completing",
		EnqueuedAt: base.Add(-40 * time.Minute)})
	return q
}

// TestViewRetryAndCancelAJob is FR11's first scenario.
func TestViewRetryAndCancelAJob(t *testing.T) {
	h := newHarness(t)
	q := h.seedJobs()
	sre := h.ctx(adminrbac.RolePlatformSRE)

	// ── View: every role can read jobs ──
	for _, role := range adminrbac.Roles {
		jobs, err := h.jobs.List(h.ctx(role), 100)
		if err != nil {
			t.Fatalf("%s listing jobs: %v", role, err)
		}
		if len(jobs) != 5 {
			t.Fatalf("%s sees %d jobs, want 5", role, len(jobs))
		}
	}

	// ── Retry a parked job ──
	if _, err := h.jobs.Retry(sre, "run-parked", "root cause fixed in the transform; re-running", adminops.Confirm()); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if j, _ := q.get("run-parked"); j.State != "ready" || j.Attempts != 0 || j.DeadLetterReason != "" {
		t.Errorf("after a retry the job is %+v, want a clean ready item", j)
	}
	if n := len(h.entriesFor(adminaudit.ActionJobRetry)); n != 2 {
		t.Errorf("retry wrote %d audit entries, want 2", n)
	}

	// ── Cancel a running job: confirm + reason + audit ──
	const cancelReason = "incident INC-77: this run is thrashing the provider"
	if _, err := h.jobs.Cancel(sre, "run-running", cancelReason, adminops.Confirm()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	j, _ := q.get("run-running")
	if j.State != "dead" {
		t.Errorf("a cancelled job is in state %q, want dead", j.State)
	}
	if q.cancelReasons["run-running"] == "" {
		t.Error("the operator's reason did not reach the job's own record")
	}
	entries := h.entriesFor(adminaudit.ActionJobCancel)
	if len(entries) != 2 {
		t.Fatalf("cancel wrote %d audit entries, want 2", len(entries))
	}
	outcome := entries[1]
	if outcome.ActorAdminID != h.adminIDs[adminrbac.RolePlatformSRE] || outcome.Target != adminops.JobTarget("run-running") ||
		outcome.Reason != cancelReason || outcome.CreatedAt.IsZero() {
		t.Errorf("the cancel entry does not record actor/job/reason/timestamp: %+v", outcome)
	}
	h.assertChainIntact()
}

// TestCancelRequiresReasonAndIsGated: the destructive discipline and least privilege at the job path.
func TestCancelRequiresReasonAndIsGated(t *testing.T) {
	h := newHarness(t)
	q := h.seedJobs()
	sre := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.jobs.Cancel(sre, "run-running", "", adminops.Confirm()); !errors.Is(err, adminops.ErrNoReason) {
		t.Fatalf("cancel with no reason: err = %v, want ErrNoReason", err)
	}
	if _, err := h.jobs.Cancel(sre, "run-running", "because", adminops.Confirmation{}); !errors.Is(err, adminops.ErrNotConfirmed) {
		t.Fatalf("unconfirmed cancel: err = %v, want ErrNotConfirmed", err)
	}
	if j, _ := q.get("run-running"); j.State != "leased" {
		t.Fatal("a refused cancel took effect")
	}
	for _, role := range []adminrbac.Role{adminrbac.RoleSupport, adminrbac.RoleBillingOps} {
		ctx := h.ctx(role)
		if _, err := h.jobs.Cancel(ctx, "run-running", "customer asked", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s cancelling a job: err = %v, want ErrDenied", role, err)
		}
		if _, err := h.jobs.Retry(ctx, "run-parked", "customer asked", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s retrying a job: err = %v, want ErrDenied", role, err)
		}
	}
	if j, _ := q.get("run-running"); j.State != "leased" {
		t.Fatal("a denied cancel took effect")
	}
}

// TestRetryingANonParkedJobIsRefused: retrying a leased item would put two workers on one run.
func TestRetryingANonParkedJobIsRefused(t *testing.T) {
	h := newHarness(t)
	q := h.seedJobs()
	sre := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.jobs.Retry(sre, "run-running", "impatient", adminops.Confirm()); !errors.Is(err, runqueue.ErrNotRetryable) {
		t.Fatalf("retrying a leased job: err = %v, want ErrNotRetryable", err)
	}
	if j, _ := q.get("run-running"); j.State != "leased" {
		t.Error("a refused retry changed the job's state")
	}
	// The refusal is still on the record: the write-ahead entry plus a FAILED outcome entry.
	entries := h.entriesFor(adminaudit.ActionJobRetry)
	if len(entries) != 2 {
		t.Fatalf("a failed retry wrote %d audit entries, want 2", len(entries))
	}
	if entries[1].Result != adminops.ResultFailed {
		t.Errorf("the outcome entry result = %q, want %q — a failed privileged action is exactly as "+
			"interesting as a successful one", entries[1].Result, adminops.ResultFailed)
	}
}

// TestFleetHealthReadsTheExistingQueue is FR11's second scenario.
func TestFleetHealthReadsTheExistingQueue(t *testing.T) {
	h := newHarness(t)
	h.seedJobs()
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	health, err := h.jobs.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Degraded {
		t.Fatalf("healthy queue reported degraded: %s", health.Detail)
	}
	if health.Leased != 2 || health.Ready != 1 || health.Done != 1 || health.Dead != 1 {
		t.Errorf("fleet depth = %+v, want 2 leased / 1 ready / 1 done / 1 dead", health)
	}
	if health.Workers["worker-a"] != 1 || health.Workers["worker-b"] != 1 {
		t.Errorf("fleet workers = %v, want one item each on worker-a and worker-b", health.Workers)
	}
	if health.ExpiredLeases != 1 {
		t.Errorf("expired leases = %d, want 1 — a worker holding an expired lease is how a dead worker looks", health.ExpiredLeases)
	}
	if health.Source != "seam:run_queue(p4/p6)" {
		t.Errorf("fleet health source = %q — it must name the EXISTING queue, not a second pipeline", health.Source)
	}
	if health.OldestLeaseAgeSeconds <= 0 {
		t.Error("fleet health reports no lease age, so a stuck run is invisible")
	}
}

// TestFleetHealthRendersDegradedRatherThanEmpty: an unreachable queue is DEGRADED, not an idle fleet
// (FR26). An empty page here would read as "nothing is running" during an outage.
func TestFleetHealthRendersDegradedRatherThanEmpty(t *testing.T) {
	h := newHarness(t)
	q := h.seedJobs()
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	q.failStats = true
	health, err := h.jobs.Health(ctx)
	if err != nil {
		t.Fatalf("Health during an outage returned an error instead of a degraded view: %v", err)
	}
	if !health.Degraded || health.Detail == "" {
		t.Fatalf("an unreachable queue was not reported as degraded: %+v", health)
	}
	if health.Ready != 0 || health.Leased != 0 {
		t.Error("a degraded health view reported depth it could not read")
	}

	q.failStats, q.failList = false, true
	health, err = h.jobs.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !health.Degraded {
		t.Error("a queue whose item list is unreadable was reported healthy")
	}
}

// TestPostgresQueueSatisfiesTheJobQueueSeam: the live queue really is what the console commands, so
// the seam cannot drift from the shipped implementation without failing to compile.
func TestPostgresQueueSatisfiesTheJobQueueSeam(t *testing.T) {
	var _ adminops.JobQueue = adminops.PostgresQueue{}
}
