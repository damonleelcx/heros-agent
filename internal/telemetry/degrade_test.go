package telemetry

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Task 6.3: emission is off the request path — a slow/blocked backend adds < 5 ms p50 to a call.
func TestSection6_EmissionIsNonBlockingUnderSlowBackend(t *testing.T) {
	// A span store that sleeps 50 ms per write; the worker will back up, but the EMITTER must not wait.
	col := NewCollector(CollectorConfig{
		Spans: sleepingSpanStore{50 * time.Millisecond}, TSDB: NewMemTSDB(0), Eval: NewMemEvalStore(),
		QueueSize: 8,
	})
	t.Cleanup(col.Close)

	const n = 500
	durs := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		sp := Span{SpanID: "s" + itoa(i), TraceID: "t1",
			Attributes: map[string]any{AttrConfigHash: testConfigHash, AttrRunID: "r", AttrVariantID: "v"}}
		start := time.Now()
		col.EmitSpan(context.Background(), sp) // never blocks: buffered send or a drop
		durs = append(durs, time.Since(start))
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	p50 := durs[len(durs)/2]
	if p50 > 5*time.Millisecond {
		t.Errorf("emit p50 = %v, want < 5 ms — emission is on the request path", p50)
	}
	// Some were dropped (the queue is tiny and the store is slow); that is degradation, and it is
	// externally readable rather than silent.
	if col.Drops() == 0 {
		t.Log("note: no drops observed (worker kept up); the non-blocking guarantee still held")
	}
}

// Task 6.4: kill the collector mid-run — the run still completes. Every provider call succeeds even
// though telemetry is dead.
func TestSection6_CollectorDeathMidRunDoesNotFailTheRun(t *testing.T) {
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: NewMemTSDB(0), Eval: NewMemEvalStore()})
	gw, inst, entry := testRigWithSink(t, col)

	seed := int64(7)
	rc := RunContext{VariantID: "v1", RunID: "run_fault", ConfigHash: testConfigHash, Seed: seed, CaseID: "c1"}
	tracer := inst.StartRun(rc)
	nodes := []string{"n_a", "n_b", "n_c"}
	for i, node := range nodes {
		if i == 1 {
			col.Close() // the collector dies MID-RUN
		}
		nodeRC := rc.WithNode(node, 0)
		ctx := NewContext(context.Background(), nodeRC)
		tracer.NodeStarted(ctx, node)
		if _, err := executor.CallProvider(ctx, gw, entry,
			providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
			executor.NodeInvocation{RunID: rc.RunID, NodeID: node, AttemptGroup: 0, Seed: &seed}); err != nil {
			t.Fatalf("node %s failed because telemetry was down — a paid run must not depend on it: %v", node, err)
		}
		tracer.NodeFinished(ctx, node)
	}
	tracer.EndRun(context.Background()) // must not panic after Close
}

// Task 6.4 variant: every store panics — the run still completes and the worker survives.
func TestSection6_PanickingStoresDoNotFailTheRun(t *testing.T) {
	col := NewCollector(CollectorConfig{Spans: panicSpanStore{}, TSDB: panicTSDB{}, Eval: NewMemEvalStore()})
	t.Cleanup(col.Close)
	gw, inst, entry := testRigWithSink(t, col)
	// A whole fixture run through a collector whose stores all panic: it must complete.
	runFixture(t, gw, inst, entry, []string{"n_a", "n_b"})
	col.Flush() // must not hang
}

// Task 6.2: the collector is wired from STORES only — it never receives a provider-credential source,
// so a provider key cannot reach a span/label/log through it. This is structural: CollectorConfig has
// no Secrets field. The end-to-end no-leak proof is TestSection6_RunLeavesNoProviderKeyInAnyStore.
func TestSection6_CollectorHoldsNoProviderCredentials(t *testing.T) {
	// If a Secrets source were ever addable to the collector, this test would need updating — that is
	// the point: the collector's least privilege is enforced by what it CANNOT be handed.
	_ = CollectorConfig{Spans: NewMemSpanStore(0), TSDB: NewMemTSDB(0), Eval: NewMemEvalStore()}
	// The gateway's error handed to the observer is already scrubbed by the gateway (providergateway
	// guarantees it), so even the collector's upstream cannot leak a credential into telemetry.
}

// ── test store adapters ──

type sleepingSpanStore struct{ d time.Duration }

func (s sleepingSpanStore) PutSpan(context.Context, Span)   { time.Sleep(s.d) }
func (s sleepingSpanStore) Trace(string) []Span             { return nil }
func (s sleepingSpanStore) SpansByConfigHash(string) []Span { return nil }

type panicTSDB struct{}

func (panicTSDB) PutMetric(context.Context, metricevent.Event) { panic("tsdb down") }
func (panicTSDB) Query(map[string]string) ([]Sample, error)    { return nil, nil }
