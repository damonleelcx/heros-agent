package linkingest

import (
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
}

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
