package linkingest

import (
	"sort"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// store.go holds the linked-run record and the link-coverage read model (tasks 3.4, 4.2). It is NOT a
// second cost store — the cost lives in P2.5. This records only run IDENTITY (for idempotency and the
// coverage numerator) and the denominator the CLI reported.

// LinkedRun is one run a tenant linked. Metrics are not here — they are in P2.5.
type LinkedRun struct {
	RunID          string
	TenantID       string
	WorkflowID     string
	ConfigHash     string
	SourceRevision string
	ToolVersion    string
	LinkedAt       time.Time
	// Scores are the eval scores + intervals AS COMPUTED by the CLI's P4 harness (task 4.3). Recorded so
	// a linked run's scorecard in the console is the same numbers the developer saw locally — parity is a
	// stored fact, not a re-derivation that could drift.
	Scores []runlink.Score

	// Eval is the EVIDENCE behind those scores — case count, the customer's own gate verdict, and the
	// provisional flag. Recorded for the same reason Scores are: the eval board ranks variants, and a
	// ranking has to say what qualifies each number. Without it the board could only be mounted by
	// inventing a verdict, which is why it was mounted nil instead.
	//
	// Zero-valued on a run linked before the evidence crossed. GateOutcome is "" there, which is
	// deliberately NOT one of the three verdicts — see EvalEvidencePresent.
	Eval runlink.EvalSummary
	// PerNode attributes cost/latency/tokens to node ids. The scorecard's entire subject: an aggregate
	// cannot say WHICH node is expensive.
	PerNode map[string]runlink.NodeMetric
}

// EvalEvidencePresent reports whether this run carries the eval evidence at all.
//
// 🔴 The distinction it protects: a run linked before migration 0023 has no case count and no verdict,
// and that is NOT "zero cases, gate failed". A board that rendered those older runs as failures would be
// accusing a customer's workflow of a regression that no measurement found. Readers branch on this and
// show "linked before this was recorded" rather than a number.
func (lr LinkedRun) EvalEvidencePresent() bool { return lr.Eval.GateOutcome != "" }

// Store records linked runs and the coverage denominator.
//
// # Why every method returns an error, including the reads
//
// This interface was written against a map, and a map cannot fail — so four of its methods returned no
// error, and that was invisible until the durable implementation arrived. A Postgres store had nowhere
// to report a failed read TO: the honest options were to swallow it, to log it where nobody reads it, or
// to map it onto a legitimate value and hope. All three make a database outage look like an ordinary,
// truthful answer — an empty run list, an unknown denominator, a run that is simply not linked.
//
// So the signatures carry the failure. It costs every caller an `if err != nil`, and it buys the one
// property that matters here: a tenant's link coverage can never be quietly wrong.
type Store interface {
	// Record marks a run linked. It returns already=true if this run was linked before — the property
	// that makes linking idempotent (FR14).
	Record(lr LinkedRun) (already bool, err error)
	// ObserveRunsReported updates the coverage denominator the CLI reported for a tenant. The stored
	// value is the MAXIMUM observed, a documented lower bound on total activity (contracts doc Q5).
	ObserveRunsReported(tenantID string, n int) error
	// Coverage returns the link coverage for a tenant.
	Coverage(tenantID string) (LinkCoverage, error)
	// LinkedRunIDs returns the run ids a tenant has linked, for tests and read models.
	LinkedRunIDs(tenantID string) ([]string, error)
	// Get returns one linked run's record, including its recorded scores. ok is false if not linked.
	//
	// 🔴 `ok=false` means NOT LINKED and nothing else. A read failure is the error — the two were once
	// the same value here, which made a database outage indistinguishable from a run nobody had linked.
	Get(tenantID, runID string) (LinkedRun, bool, error)
	// ForWorkflow returns every run a tenant has linked for one workflow, newest first.
	//
	// This is what the eval board reads. It returns RUNS, not variants: grouping runs by config_hash
	// into variants is a decision with a rule behind it (see internal/hostedboard), and putting that
	// rule in SQL would hide it where nobody looks for it and make it untestable without a database.
	ForWorkflow(tenantID, workflowID string) ([]LinkedRun, error)
}

// LinkCoverage is how much of a tenant's activity the linked figure reflects (FR17). It distinguishes
// COMPLETE coverage from UNKNOWN coverage — collapsing them loses the distinction that matters (task
// 5.3): a spend figure at 100% and a spend figure whose denominator is unknown look identical as a
// number and mean opposite things.
type LinkCoverage struct {
	RunsLinked   int `json:"runs_linked"`
	RunsReported int `json:"runs_reported"`
	// Known is false when no denominator has been reported — coverage is UNKNOWN, not zero and not
	// complete. Complete is true only when every reported run is linked AND the denominator is known.
	Known    bool `json:"known"`
	Complete bool `json:"complete"`
}

// MemStore is the reference Store.
type MemStore struct {
	mu       sync.RWMutex
	linked   map[string]map[string]LinkedRun // tenant -> runID -> record
	reported map[string]int                  // tenant -> max runs_reported
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{linked: map[string]map[string]LinkedRun{}, reported: map[string]int{}}
}

func (m *MemStore) Record(lr LinkedRun) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.linked[lr.TenantID]
	if t == nil {
		t = map[string]LinkedRun{}
		m.linked[lr.TenantID] = t
	}
	if _, exists := t[lr.RunID]; exists {
		return true, nil
	}
	t[lr.RunID] = lr
	return false, nil
}

func (m *MemStore) ObserveRunsReported(tenantID string, n int) error {
	if n <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > m.reported[tenantID] {
		m.reported[tenantID] = n
	}
	return nil
}

func (m *MemStore) Coverage(tenantID string) (LinkCoverage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	linked := len(m.linked[tenantID])
	reported, known := m.reported[tenantID]
	// A denominator at least as large as the numerator: a tenant cannot have linked more runs than it
	// reported observing. If reports lag (linked > reported), the denominator is the linked count and
	// coverage is complete, never over 100%.
	if linked > reported {
		reported = linked
	}
	cov := LinkCoverage{RunsLinked: linked, RunsReported: reported, Known: known}
	cov.Complete = known && reported > 0 && linked >= reported
	// If nothing was ever reported but runs are linked, coverage is complete (we saw everything we were
	// told about) but the denominator's provenance is the linked set — mark it known so the console does
	// not show "unknown" for a tenant that has linked runs.
	if !known && linked > 0 {
		cov.Known = true
		cov.RunsReported = linked
		cov.Complete = true
	}
	return cov, nil
}

func (m *MemStore) LinkedRunIDs(tenantID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for id := range m.linked[tenantID] {
		out = append(out, id)
	}
	return out, nil
}

func (m *MemStore) Get(tenantID, runID string) (LinkedRun, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lr, ok := m.linked[tenantID][runID]
	return lr, ok, nil
}

// ForWorkflow returns a tenant's runs for one workflow, newest first.
//
// Sorted with the run id as the tiebreak, not left to map order: two runs linked in the same instant
// would otherwise swap places between reloads, and the board built from them would reorder its rows for
// no reason a user could explain.
func (m *MemStore) ForWorkflow(tenantID, workflowID string) ([]LinkedRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []LinkedRun
	for _, lr := range m.linked[tenantID] {
		if lr.WorkflowID == workflowID {
			out = append(out, lr)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LinkedAt.Equal(out[j].LinkedAt) {
			return out[i].LinkedAt.After(out[j].LinkedAt)
		}
		return out[i].RunID < out[j].RunID
	})
	return out, nil
}
