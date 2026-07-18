package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

func sampleRC() RunContext {
	return RunContext{
		VariantID: "v1", RunID: "run_1", ConfigHash: testConfigHash, Seed: 4, CaseID: "case_1",
	}.WithNode("n_a", 0)
}

// Task 2.2 headline: an event missing a tag is rejected at the emission boundary and reaches no store.
func TestSection2_MissingTagIsRejectedAndReachesNoStore(t *testing.T) {
	down := &recordingSink{}
	g := NewGate(down)

	rc := sampleRC()
	ev := rc.event(MetricCostUSD, 0.01, UnitUSD, time.Now(), nil)
	ev.ConfigHash = "" // the exact scenario: a null/absent config_hash

	g.EmitMetric(context.Background(), ev)

	if len(down.metrics) != 0 {
		t.Fatalf("an under-tagged event reached the store: %+v", down.metrics)
	}
	if g.Stats().Rejected != 1 {
		t.Errorf("gate rejected count = %d, want 1", g.Stats().Rejected)
	}
}

// A fully-tagged event is forwarded unchanged.
func TestSection2_FullyTaggedEventIsForwarded(t *testing.T) {
	down := &recordingSink{}
	g := NewGate(down)
	rc := sampleRC()
	ev := rc.event(MetricLatencyTotalMS, 12, UnitMS, time.Now(), nil)

	g.EmitMetric(context.Background(), ev)
	if len(down.metrics) != 1 {
		t.Fatalf("a fully-tagged event was not forwarded: %d", len(down.metrics))
	}
	if err := down.metrics[0].Validate(); err != nil {
		t.Errorf("forwarded event is not valid: %v", err)
	}
}

// Task 2.3: config_hash present on every span; a span without it is rejected before any store.
func TestSection2_SpanWithoutConfigHashIsRejected(t *testing.T) {
	down := &recordingSink{}
	g := NewGate(down)

	good := Span{
		SpanID: "s1", Kind: SpanKindNode, Name: "chat gpt-5",
		Attributes: map[string]any{
			AttrConfigHash: testConfigHash, AttrRunID: "run_1", AttrVariantID: "v1", AttrNodeID: "n_a",
		},
	}
	bad := good
	bad.SpanID = "s2"
	bad.Attributes = map[string]any{AttrRunID: "run_1", AttrVariantID: "v1"} // no config_hash

	g.EmitSpan(context.Background(), good)
	g.EmitSpan(context.Background(), bad)

	if len(down.spans) != 1 || down.spans[0].SpanID != "s1" {
		t.Fatalf("span gate did not enforce config_hash: forwarded %+v", down.spans)
	}
	if g.Stats().Rejected != 1 {
		t.Errorf("gate rejected count = %d, want 1", g.Stats().Rejected)
	}
}

// Task 2.4: a retried invocation is measured once — no double-counted cost, no double-written event.
func TestSection2_RetriedInvocationIsMeasuredOnce(t *testing.T) {
	down := &recordingSink{}
	g := NewGate(down)
	rc := sampleRC()

	// The same logical invocation's per-call metrics, built twice (a redelivery / double-processing).
	d := callDetail{
		provider: "openai", modelID: "gpt-5", usage: tokenUsage{input: 100, output: 40},
		duration: 10 * time.Millisecond, attempts: 1, idempotencyKey: rc.IdempotencyKey(),
	}
	pb, _ := NewPriceBook("v1")
	pb.Set("openai", "gpt-5", ModelInfo{InputPerMTok: 1, OutputPerMTok: 1, ContextWindow: 1000})

	emit := func() {
		events, _ := MetricSet(rc, d, time.Now(), pb)
		for _, ev := range events {
			g.EmitMetric(context.Background(), ev)
		}
	}
	emit()
	emit() // the duplicate — must be dropped

	// Count cost events reaching the store: exactly one, not two.
	costs := 0
	for _, ev := range down.metrics {
		if ev.MetricName == MetricCostUSD {
			costs++
		}
	}
	if costs != 1 {
		t.Errorf("cost was written %d times for one invocation; a retry double-counted it", costs)
	}
	if g.Stats().Deduped == 0 {
		t.Error("gate did not dedup the retried invocation")
	}
}

// A retried span recomputes the same deterministic span_id, so the gate drops the duplicate.
func TestSection2_RetriedSpanIsNotDoubleWritten(t *testing.T) {
	down := &recordingSink{}
	g := NewGate(down)
	sp := Span{
		SpanID: NodeSpanID("run_1:n_a:0"), Kind: SpanKindNode,
		Attributes: map[string]any{AttrConfigHash: testConfigHash, AttrRunID: "run_1", AttrVariantID: "v1"},
	}
	g.EmitSpan(context.Background(), sp)
	g.EmitSpan(context.Background(), sp) // same id -> dropped
	if len(down.spans) != 1 {
		t.Errorf("a retried span was written %d times, want 1", len(down.spans))
	}
}

// Run-scoped throughput/concurrency metrics legitimately repeat and must NOT be deduped away.
func TestSection2_RunScopedMetricsAreNotDeduped(t *testing.T) {
	down := &recordingSink{}
	g := NewGate(down)
	rc := RunContext{VariantID: "v1", RunID: "run_1", ConfigHash: testConfigHash, Seed: 1, CaseID: "c"}
	rc.NodeID = NodeIDRun
	// Two concurrency samples over the run window: both must survive (it is a time series).
	g.EmitMetric(context.Background(), rc.event(MetricCallsInFlight, 1, UnitCalls, time.Now(), nil))
	g.EmitMetric(context.Background(), rc.event(MetricCallsInFlight, 2, UnitCalls, time.Now(), nil))
	n := 0
	for _, ev := range down.metrics {
		if ev.MetricName == MetricCallsInFlight {
			n++
		}
	}
	if n != 2 {
		t.Errorf("run-scoped concurrency samples collapsed to %d, want 2 (a time series, not one invocation)", n)
	}
}

var _ = metricevent.SchemaVersion
