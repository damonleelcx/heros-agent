package optimizer

import (
	"context"
	"testing"
)

// Section 6.1 / 6.2 / 8.7: a production-failure trace becomes a new weak-labeled eval case, coverage is
// re-measured, and the next run scores against it.
func TestFeedback_IngestProductionFailure(t *testing.T) {
	store := NewMemEvalSet(map[string][]EvalCase{
		"wf-1": {{CaseID: "c1", Signal: "baseline"}, {CaseID: "c2", Signal: "baseline"}},
	})
	ledger := NewMemLedger()
	ing := FeedbackIngestor{Store: store, Ledger: ledger, Put: ledger.Put}

	before := len(store.CaseIDs("wf-1"))
	res, err := ing.IngestProductionFailure(context.Background(), ProductionTrace{
		TraceID: "t-99", WorkflowID: "wf-1", ConfigHash: "cfg", Signal: "empty_answer",
		Payload: []byte(`{"input":"x","output":"","pii":"redact-me"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The new case is present in the P4 eval set (task 6.2).
	if res.CaseCount != before+1 {
		t.Fatalf("expected case count to grow by 1, got %d (was %d)", res.CaseCount, before)
	}
	ids := store.CaseIDs("wf-1")
	if !contains(ids, res.Case.CaseID) {
		t.Fatalf("new eval case %s not present in the eval set %v", res.Case.CaseID, ids)
	}
	// Per P4 the case is weak-labeled (design Q5): it can widen coverage / block via regression but
	// cannot be the sole basis for an apply.
	if !res.Case.WeakLabeled {
		t.Fatal("an ingested production failure must be weak-labeled")
	}
	// Coverage was re-measured (task 6.2).
	if res.Coverage <= 0 {
		t.Fatal("coverage should have been re-measured to a positive value")
	}
	// The intake is audited, and the trace payload (with PII) is content-hashed, never inlined.
	if !hasEvent(ledger.Events("wf-1"), EventIngest) {
		t.Fatal("the intake must be recorded in the audit trail")
	}
	for _, ev := range ledger.Events("wf-1") {
		if containsSubstr(ev.Summary, "redact-me") {
			t.Fatal("trace PII leaked into the ledger summary")
		}
	}
}

// Idempotent intake: ingesting the same trace twice does not double-count.
func TestFeedback_IdempotentByCaseID(t *testing.T) {
	store := NewMemEvalSet(nil)
	ing := FeedbackIngestor{Store: store}
	tr := ProductionTrace{TraceID: "t-1", WorkflowID: "wf-1", Signal: "s", Payload: []byte("p")}
	_, _ = ing.IngestProductionFailure(context.Background(), tr)
	res, _ := ing.IngestProductionFailure(context.Background(), tr)
	if res.CaseCount != 1 {
		t.Fatalf("re-ingesting the same trace must not double-count, got %d", res.CaseCount)
	}
}
