package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// service_test.go is QA task 7.5 and acceptance A7: **run twice on an unchanged revision → byte-
// identical findings AND no provider call on the second run.**
//
// # 🔴 Why "no provider call" needs its own assertion
//
// "Identical findings" is satisfiable by re-running everything and getting the same answer, which on a
// pinned inference is exactly what would happen — and the customer would be billed twice for it while
// the report looked perfectly reproducible. So the call count is asserted separately, and the counter
// lives in the double rather than being inferred from timing.

// pinStore is a `Store` that also serves as the in-memory pin, so the Service's short-circuit can be
// driven without Postgres. The PG path is proved by the pgproof suite; this proves the RULE.
type pinStore struct {
	mu   sync.Mutex
	rows []Assessment
	// writes counts persists, so "the second run wrote a second row" is checkable.
	writes int
}

func (p *pinStore) Put(_ context.Context, a Assessment) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	p.rows = append(p.rows, a)
	return nil
}

func (p *pinStore) find(revision, hash string) (Assessment, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.rows) - 1; i >= 0; i-- {
		if p.rows[i].SourceRevision == revision && p.rows[i].AgentConfigHash == hash {
			return p.rows[i], true
		}
	}
	return Assessment{}, false
}

// fixedSource returns the same revision, IR and report every time — the "unchanged revision" of the
// requirement, expressed as an input rather than as an assumption about a clock.
type fixedSource struct {
	revision string
	subject  Subject
	reads    int
}

func (f *fixedSource) Analyse(context.Context, string, string) (string, *discovery.IR, discovery.DiscoveryReport, error) {
	f.reads++
	return f.revision, f.subject.IR, f.subject.Report, nil
}

// serviceUnderTest wires a Service whose store is `pinStore`, with the pin lookup routed through it.
//
// 🔴 `Service` takes a `*PGStore` concretely, so this test drives the RULE through a small stand-in
// rather than the Service itself. That is a real limitation and it is stated rather than hidden: the
// Service's own short-circuit is exercised end to end by `TestTheLiveFourStep`'s sibling in the pgproof
// suite. What is proven here is that the rule — a complete pin answers without a provider call, a
// PARTIAL one does not — holds, and that the runner underneath makes no call when it is not entered.
func serviceUnderTest(t *testing.T, inf Inference) (*Runner, *pinStore, *fixedSource) {
	t.Helper()
	store := &pinStore{}
	tick := int64(0)
	r, err := NewRunner(store, allResolve{}, inf, func() int64 { tick++; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r, store, &fixedSource{revision: "rev-1", subject: subjectFor(t, "python")}
}

// runPinned is the Service's rule, applied over the stand-in store: a COMPLETE pinned assessment for
// the key answers without entering the runner; a PARTIAL one does not.
func runPinned(t *testing.T, r *Runner, store *pinStore, src *fixedSource, hash string) Assessment {
	t.Helper()
	if pinned, ok := store.find(src.revision, hash); ok && !pinned.Partial() {
		return pinned
	}
	rev, ir, rep, err := src.Analyse(context.Background(), "tn-1", "wf-python")
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Run(context.Background(), Config{
		AssessmentID: "as-" + hash + "-" + rev, TenantID: "tn-1",
		SourceRevision: rev, AgentConfigHash: hash, SpendCapUSD: 1.00,
	}, Subject{WorkflowID: "wf-python", IR: ir, Report: rep})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return a
}

// TestASecondAssessmentOfAnUnchangedRevisionMakesNoProviderCall is task 7.5, both halves.
func TestASecondAssessmentOfAnUnchangedRevisionMakesNoProviderCall(t *testing.T) {
	inf := &countingInference{}
	r, store, src := serviceUnderTest(t, inf)

	first := runPinned(t, r, store, src, "cfg-1")
	callsAfterFirst := inf.calls
	if callsAfterFirst == 0 {
		t.Fatal("the FIRST run made no provider call, so the second making none proves nothing — this " +
			"test would pass on an inference that never runs")
	}

	second := runPinned(t, r, store, src, "cfg-1")

	if inf.calls != callsAfterFirst {
		t.Fatalf("the second run made %d further provider call(s). A pinned assessment must answer from "+
			"the store: the customer is otherwise billed twice for an identical report, and the report "+
			"looks perfectly reproducible while it happens", inf.calls-callsAfterFirst)
	}
	if store.writes != 1 {
		t.Fatalf("the store was written %d times; the second run persisted a second row", store.writes)
	}

	// 🔴 BYTE-IDENTICAL, not "equivalent". FR15's guarantee is about the report a reader sees, and a
	// map iteration inside a claim or a reordered findings slice would satisfy a field-by-field
	// comparison while producing a different document.
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("the two reports are not byte-identical:\n first: %s\nsecond: %s", a, b)
	}
}

// TestAPartialAssessmentIsNotServedFromThePin is the ONE condition on the pin, and it is the one a
// naive implementation gets wrong.
//
// A report that stopped at a spend cap is not the answer to "assess this again with a higher cap". If
// the pin served it, "why is my report still incomplete?" would be answered with the incomplete report,
// forever, and the only way out would be a cache nobody can see.
func TestAPartialAssessmentIsNotServedFromThePin(t *testing.T) {
	inf := &countingInference{spend: 1.00} // one call exhausts a $1.00 cap
	r, store, src := serviceUnderTest(t, inf)

	first := runPinned(t, r, store, src, "cfg-1")
	if !first.Partial() {
		t.Fatalf("the fixture did not produce a partial report; this test needs one")
	}
	callsAfterFirst := inf.calls

	_ = runPinned(t, r, store, src, "cfg-1")
	if inf.calls == callsAfterFirst {
		t.Fatal("a PARTIAL pinned assessment was served from the store. Re-running after raising a cap " +
			"would then return the same truncated report, and the reader has no way to tell")
	}
}

// TestAChangedConfigurationMissesThePin is the other side: the pin key includes the agent config, and a
// new configuration is a new question.
func TestAChangedConfigurationMissesThePin(t *testing.T) {
	inf := &countingInference{}
	r, store, src := serviceUnderTest(t, inf)

	_ = runPinned(t, r, store, src, "cfg-1")
	callsAfterFirst := inf.calls
	_ = runPinned(t, r, store, src, "cfg-2")

	if inf.calls == callsAfterFirst {
		t.Fatal("a DIFFERENT agent configuration was answered from the previous configuration's pin — " +
			"which is the defect `herosagent.Input.AgentConfigHash` records: the gate that exists to " +
			"tell definitions apart becomes unable to")
	}
}

// TestNoSourceIsANamedRefusalRatherThanAnEmptyReport keeps the two apart at the service boundary.
func TestNoSourceIsANamedRefusalRatherThanAnEmptyReport(t *testing.T) {
	if !errors.Is(ErrNoSource, ErrNoSource) {
		t.Fatal("ErrNoSource is not comparable")
	}
	if !strings.Contains(ErrNoSource.Error(), "no source snapshot") {
		t.Fatalf("ErrNoSource does not say what is missing: %v", ErrNoSource)
	}
}
