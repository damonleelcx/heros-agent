package sourceingest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/eventname"
)

// retention.go removes expired snapshots on a schedule that runs whether or not anything else does,
// and publishes its last successful run on a readable health endpoint (FR17, task 5.2).
//
// # Why "whether or not anything else does" is the requirement and not a nicety
//
// Retention triggered by a read is retention that never happens for a workflow nobody reads — which is
// exactly the workflow whose snapshot nobody wants us still holding. The job is a ticker with its own
// goroutine and its own error path, and it does not depend on any request arriving.
//
// # 🔴 Why the last successful run is on a health endpoint and not only in a log
//
// `health-signal-surface`: a health signal that lives only in logs is a signal nobody reads until an
// audit. The failure this closes is the one the k8s consent-retention CronJob comment already names —
// *"a retention job that silently never connects is indistinguishable from one that found nothing to
// remove"* — and it is indistinguishable in exactly the direction that looks fine. Zero deletions is
// the NORMAL result. So the signal cannot be "did it delete anything"; it has to be "when did it last
// COMPLETE", which is a value only the job itself can publish.
//
// # Consecutive failures escalate (task 5.3)
//
// A job failing forever at WARN is a job nobody fixes. After EscalateAfterConsecutiveFailures runs
// with no success, `Health().Status` becomes `escalated` and the reason is named — so a monitor can
// alert on a field rather than on a log-line regex.

// RetentionHealth is what /readyz publishes about the retention job.
type RetentionHealth struct {
	// Status is `ready` | `degraded` | `escalated` | `never_run`.
	//
	// 🔴 `never_run` is a distinct state rather than `degraded`. A process that started ninety seconds
	// ago has not run the job yet and nothing is wrong; a process that has been up for a day and never
	// run it has a dead goroutine. Collapsing them would make the first one page somebody and the
	// second one look identical to it.
	Status string `json:"status"`
	// LastSuccessMS is when the job last COMPLETED, in epoch milliseconds. Zero when it never has.
	LastSuccessMS int64 `json:"last_success_ms"`
	// LastRunMS is when it last STARTED a run that finished, successfully or not.
	LastRunMS int64 `json:"last_run_ms"`
	// Runs and Deleted are cumulative totals since process start.
	Runs    int64 `json:"runs"`
	Deleted int64 `json:"deleted"`
	// ConsecutiveFailures drives the escalation.
	ConsecutiveFailures int64 `json:"consecutive_failures"`
	// Detail names the most recent failure. Never a credential and never a customer's path — the
	// store's errors are about rows and blobs.
	Detail string `json:"detail,omitempty"`
	// IntervalSeconds and WindowHours are published so a monitor can compute "should it have run by
	// now" without hard-coding this deployment's configuration.
	IntervalSeconds int64 `json:"interval_seconds"`
	WindowHours     int64 `json:"window_hours"`
}

// Retention statuses. Central constants for logging-conventions' reason: a status a monitor matches on
// must not be a literal typed at four call sites.
const (
	RetentionNeverRun  = "never_run"
	RetentionReady     = "ready"
	RetentionDegraded  = "degraded"
	RetentionEscalated = "escalated"
)

// DefaultRetentionInterval is how often the sweep runs.
//
// Fifteen minutes, which is far more often than the 72-hour window needs. That is deliberate: the
// interval bounds how long an EXPIRED snapshot lingers, and the window bounds when it expires. A
// daily sweep would mean a tree expiring at 00:05 is held for another 24 hours, which quietly turns a
// 72-hour rule into a 96-hour one.
const DefaultRetentionInterval = 15 * time.Minute

// RetentionJob sweeps expired snapshots.
type RetentionJob struct {
	snaps    SnapshotStore
	interval time.Duration
	window   time.Duration
	log      *slog.Logger
	nowMS    func() int64

	mu     sync.Mutex
	health RetentionHealth
}

// RetentionConfig wires the job.
type RetentionConfig struct {
	Snapshots SnapshotStore
	Interval  time.Duration
	// Window is published on the health endpoint. The expiry itself is stamped on the row at write
	// time, so changing this does not retroactively move existing snapshots — which is the honest
	// behaviour: a snapshot was taken under a stated rule and that rule is the one it is held under.
	Window time.Duration
	Logger *slog.Logger
	NowMS  func() int64
}

// NewRetentionJob builds the sweeper.
func NewRetentionJob(cfg RetentionConfig) *RetentionJob {
	j := &RetentionJob{
		snaps:    cfg.Snapshots,
		interval: cfg.Interval,
		window:   cfg.Window,
		log:      cfg.Logger,
		nowMS:    cfg.NowMS,
	}
	if j.interval <= 0 {
		j.interval = DefaultRetentionInterval
	}
	if j.window <= 0 {
		j.window = DefaultCloneRetention
	}
	if j.log == nil {
		j.log = slog.Default()
	}
	if j.nowMS == nil {
		j.nowMS = func() int64 { return time.Now().UnixMilli() }
	}
	j.health = RetentionHealth{
		Status:          RetentionNeverRun,
		IntervalSeconds: int64(j.interval / time.Second),
		WindowHours:     int64(j.window / time.Hour),
	}
	return j
}

// RunOnce performs one sweep and records the outcome. Exported so the fence can drive it without a
// ticker — a test that has to wait fifteen minutes is a test nobody runs.
func (j *RetentionJob) RunOnce(ctx context.Context) (int, error) {
	now := j.nowMS()
	n, err := j.snaps.DeleteExpired(ctx, now)

	j.mu.Lock()
	defer j.mu.Unlock()
	j.health.Runs++
	j.health.LastRunMS = now
	if err != nil {
		j.health.ConsecutiveFailures++
		j.health.Detail = err.Error()
		j.health.Status = RetentionDegraded
		if j.health.ConsecutiveFailures >= EscalateAfterConsecutiveFailures {
			j.health.Status = RetentionEscalated
		}
		j.log.ErrorContext(ctx, "the source-snapshot retention sweep failed",
			append(logBase(ctx, eventname.IngestRetentionFailed, errorcode.StoreWriteFailed),
				"consecutive_failures", j.health.ConsecutiveFailures)...)
		return 0, err
	}
	j.health.ConsecutiveFailures = 0
	j.health.Detail = ""
	j.health.Status = RetentionReady
	j.health.LastSuccessMS = now
	j.health.Deleted += int64(n)
	// INFO at every run, including the zero-deletion one. A log line only when something was deleted
	// would make "the job is alive" and "the job found nothing" produce identical output — which is
	// the exact ambiguity the health endpoint exists to remove, so the log must not reintroduce it.
	j.log.InfoContext(ctx, "the source-snapshot retention sweep completed",
		append(logBase(ctx, eventname.IngestRetentionSwept, ""), "deleted", n)...)
	return n, nil
}

// Start runs the sweep on its interval until ctx is done. Returns immediately; the goroutine is the
// job. Non-blocking so a launch path can start it beside every other background worker.
//
// 🔴 It sweeps ONCE before the first tick. A process that restarts more often than the interval would
// otherwise never sweep at all — which is the failure shape where a deployment that is being actively
// worked on is the one whose retention silently stops.
func (j *RetentionJob) Start(ctx context.Context) {
	go func() {
		_, _ = j.RunOnce(ctx)
		t := time.NewTicker(j.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = j.RunOnce(ctx)
			}
		}
	}()
}

// Health reports the job's state for /readyz.
func (j *RetentionJob) Health() RetentionHealth {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.health
}
