package optimizer

import (
	"context"
	"sort"
)

// feedback.go closes the AI-Engineer loop (design Context): phase 10 ("ship safely; failures become
// new eval cases") folds back onto phase 3 ("build the eval harness FIRST"). A production failure
// observed after an applied change is ingested as a NEW eval case that re-enters at P4 — added to the
// eval set, coverage re-measured — so the NEXT optimization run is scored against it. The eval set is
// the living memory of the system.

// ProductionTrace is a failure observed in production after an applied change. It is the raw input to
// ingestion; its payload is content-addressed into the object store, never inlined into the ledger.
type ProductionTrace struct {
	TraceID    string `json:"trace_id"`
	WorkflowID string `json:"workflow_id"`
	// ConfigHash is the live config the failure occurred under — the applied change under suspicion.
	ConfigHash string `json:"config_hash"`
	// Payload is the trace body (inputs, outputs, the failing signal). It is hashed, never stored raw
	// in the audit trail (task 5.5).
	Payload []byte `json:"-"`
	// Signal is a short, secret-free label of what went wrong ("empty answer", "tool 500"), for the case
	// and the ledger summary.
	Signal string `json:"signal"`
}

// EvalCase is the new case ingestion produces. Per P4 it is WEAK-LABELED (design Q5): a production
// trace has no human-reviewed gold answer, so the case can widen coverage and BLOCK a merge via the
// regression check, but — being weak — it cannot be the sole basis for an apply.
type EvalCase struct {
	CaseID      string `json:"case_id"`
	Source      string `json:"source"`       // "production_failure"
	WeakLabeled bool   `json:"weak_labeled"` // always true for an ingested production failure (P4)
	PayloadHash string `json:"payload_hash"`
	Signal      string `json:"signal"`
}

// EvalSetStore is the P4 eval set the loop measures against. Ingestion adds a case and asks the store to
// re-measure coverage, so the next run scores the new case. It is an interface so the loop is
// decoupled from P4's storage; production wires the real eval-set store.
type EvalSetStore interface {
	// AddCase adds ec to the workflow's eval set (idempotent by CaseID) and returns the new case count.
	AddCase(workflowID string, ec EvalCase) (caseCount int, err error)
	// RemeasureCoverage recomputes the eval set's coverage after a change and returns the coverage value.
	RemeasureCoverage(workflowID string) (coverage float64, err error)
	// CaseIDs returns the current eval-set case ids for a workflow (so a test can assert the new case is
	// present and the next run scores against it).
	CaseIDs(workflowID string) []string
}

// IngestResult reports what ingestion did: the new case, the updated case count, and the re-measured
// coverage — the evidence that "the next run is scored against it".
type IngestResult struct {
	Case        EvalCase `json:"case"`
	CaseCount   int      `json:"case_count"`
	Coverage    float64  `json:"coverage"`
	PayloadHash string   `json:"payload_hash"`
}

// FeedbackIngestor turns production failures into eval cases and audits the intake.
type FeedbackIngestor struct {
	Store  EvalSetStore
	Ledger ChangeLedger
	// Put content-addresses the trace payload. Defaults to ContentHash when nil; a MemLedger's Put keeps
	// the bytes retrievable.
	Put func(payload []byte) string
	// Clock stamps the ledger event.
	Clock func() int64
}

// IngestProductionFailure adds trace as a new weak-labeled eval case, re-measures coverage, and records
// the intake in the change ledger (spec Requirement "A production failure SHALL be ingestible as a new
// eval case that re-enters at P4"). The returned result is the auditable evidence the next run will
// score against the new case.
func (f FeedbackIngestor) IngestProductionFailure(ctx context.Context, trace ProductionTrace) (IngestResult, error) {
	_ = ctx
	hash := ContentHash(trace.Payload)
	if f.Put != nil {
		hash = f.Put(trace.Payload)
	}
	ec := EvalCase{
		CaseID:      "prod-" + trace.TraceID,
		Source:      "production_failure",
		WeakLabeled: true, // P4: a production trace is weak-labeled — no human gold (design Q5)
		PayloadHash: hash,
		Signal:      trace.Signal,
	}
	count, err := f.Store.AddCase(trace.WorkflowID, ec)
	if err != nil {
		return IngestResult{}, err
	}
	coverage, err := f.Store.RemeasureCoverage(trace.WorkflowID)
	if err != nil {
		return IngestResult{}, err
	}
	if f.Ledger != nil {
		_, _ = f.Ledger.Append(LedgerEvent{
			RunID: trace.WorkflowID, Type: EventIngest, Actor: "production",
			PayloadHash: hash,
			Summary:     "ingested production failure as weak-labeled eval case " + ec.CaseID + " (" + trace.Signal + ")",
		})
	}
	return IngestResult{Case: ec, CaseCount: count, Coverage: coverage, PayloadHash: hash}, nil
}

// MemEvalSet is an in-memory EvalSetStore for the loop's default path and the tests. Coverage is a
// simple, honest proxy: the fraction of distinct signals covered relative to the case count, which is
// enough to prove "coverage re-measured after intake" without importing the whole P4 coverage model.
type MemEvalSet struct {
	cases map[string][]EvalCase
}

// NewMemEvalSet builds an empty in-memory eval set, optionally seeded per workflow.
func NewMemEvalSet(seed map[string][]EvalCase) *MemEvalSet {
	m := &MemEvalSet{cases: map[string][]EvalCase{}}
	for wf, cs := range seed {
		m.cases[wf] = append([]EvalCase(nil), cs...)
	}
	return m
}

func (m *MemEvalSet) AddCase(workflowID string, ec EvalCase) (int, error) {
	for _, existing := range m.cases[workflowID] {
		if existing.CaseID == ec.CaseID {
			return len(m.cases[workflowID]), nil // idempotent
		}
	}
	m.cases[workflowID] = append(m.cases[workflowID], ec)
	return len(m.cases[workflowID]), nil
}

func (m *MemEvalSet) RemeasureCoverage(workflowID string) (float64, error) {
	cs := m.cases[workflowID]
	if len(cs) == 0 {
		return 0, nil
	}
	sigs := map[string]bool{}
	for _, c := range cs {
		sigs[c.Signal] = true
	}
	return float64(len(sigs)) / float64(len(cs)), nil
}

func (m *MemEvalSet) CaseIDs(workflowID string) []string {
	var out []string
	for _, c := range m.cases[workflowID] {
		out = append(out, c.CaseID)
	}
	sort.Strings(out)
	return out
}
