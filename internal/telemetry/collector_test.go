package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// collectorRig wires the instrument's sink to a real Collector fanning out to in-memory stores — the
// same pipeline shape a real Tempo/Prometheus/Postgres deployment has.
func collectorRig(t *testing.T) (*providergateway.Gateway, *Instrument, *Collector, *MemSpanStore, *MemTSDB, *MemEvalStore, *registry.ModelEntry) {
	t.Helper()
	spans := NewMemSpanStore(0)
	tsdb := NewMemTSDB(0)
	eval := NewMemEvalStore()
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: tsdb, Eval: eval})
	t.Cleanup(col.Close)

	gw, inst, entry := testRigWithSink(t, col)
	return gw, inst, col, spans, tsdb, eval, entry
}

// Task 5.1 / 5.2: telemetry routes by shape — metrics -> TSDB, spans -> span store — every record keyed
// by config_hash.
func TestSection5_ThreeStoreRouting(t *testing.T) {
	gw, inst, col, spans, tsdb, _, entry := collectorRig(t)
	runFixture(t, gw, inst, entry, []string{"n_a", "n_b"})
	col.Flush()

	// Metrics landed in the TSDB, keyed by config_hash.
	samples, err := tsdb.Query(map[string]string{AttrConfigHash: testConfigHash})
	if err != nil {
		t.Fatalf("TSDB query: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("no metric samples routed to the TSDB")
	}
	// Spans landed in the span store, keyed by config_hash.
	if got := spans.SpansByConfigHash(testConfigHash); len(got) == 0 {
		t.Fatal("no spans routed to the span store")
	}
	// A different config_hash returns nothing — the records are genuinely keyed, not a firehose.
	other := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if s, _ := tsdb.Query(map[string]string{AttrConfigHash: other}); len(s) != 0 {
		t.Errorf("TSDB returned samples for a config_hash that never ran: %d", len(s))
	}
}

// Task 5.4: each query shape hits the store built for it, filterable by config_hash.
func TestSection5_QueryShapesHitTheirStore(t *testing.T) {
	gw, inst, col, spans, tsdb, eval, entry := collectorRig(t)
	rc := runFixture(t, gw, inst, entry, []string{"n_a"})

	// A quality event routed to the eval store (the evaluator seam is §7; here we prove the route).
	seed := rc.Seed
	col.EmitEval(context.Background(), QualityMetricEvent{
		Event: (RunContext{VariantID: rc.VariantID, RunID: rc.RunID, ConfigHash: rc.ConfigHash, Seed: seed, CaseID: rc.CaseID}).
			WithNode("n_a", 0).event("quality_score", 1, UnitCount, time.Now(), nil),
		EvaluatorName: "reference",
	})
	col.Flush()

	// Trend -> TSDB, filterable by config_hash.
	trend, err := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash, "metric_name": MetricLatencyTotalMS})
	if err != nil || len(trend) == 0 {
		t.Fatalf("trend query (TSDB): %d samples, err %v", len(trend), err)
	}
	// Drill-down -> span store, per-run and per-config.
	if len(spans.Trace(rc.RunID)) == 0 {
		t.Error("drill-down query (span store) returned no trace for the run")
	}
	if len(spans.SpansByConfigHash(rc.ConfigHash)) == 0 {
		t.Error("drill-down query (span store) not filterable by config_hash")
	}
	// Comparison -> eval store (Postgres in prod), filterable by config_hash.
	rows, err := eval.ByConfigHash(context.Background(), rc.ConfigHash)
	if err != nil || len(rows) == 0 {
		t.Fatalf("comparison query (eval store): %d rows, err %v", len(rows), err)
	}

	// The cardinality contract at the query layer: the TSDB refuses a high-cardinality matcher rather
	// than silently returning nothing (case_id was never a label there).
	if _, err := tsdb.Query(map[string]string{AttrCaseID: "case_1"}); err == nil {
		t.Error("TSDB should refuse a query by case_id — it is not a series label")
	}
}

// The collector is degrade-safe: a store that panics does not crash the worker or fail the run.
func TestSection5_PanickingStoreDegradesNotCrashes(t *testing.T) {
	col := NewCollector(CollectorConfig{
		Spans: panicSpanStore{}, TSDB: NewMemTSDB(0), Eval: NewMemEvalStore(),
	})
	t.Cleanup(col.Close)
	sp := Span{SpanID: "s1", TraceID: "t1", Status: SpanStatusOK,
		Attributes: map[string]any{AttrConfigHash: testConfigHash, AttrRunID: "r", AttrVariantID: "v"}}
	col.EmitSpan(context.Background(), sp)
	col.Flush() // must not hang or panic
	// A metric after the panic still flows — the worker survived.
	col.EmitMetric(context.Background(), sampleRC().event(MetricCostUSD, 0.01, UnitUSD, time.Now(), nil))
	col.Flush()
}

type panicSpanStore struct{}

func (panicSpanStore) PutSpan(context.Context, Span)   { panic("span store is down") }
func (panicSpanStore) Trace(string) []Span             { return nil }
func (panicSpanStore) SpansByConfigHash(string) []Span { return nil }
