package telemetry

import (
	"context"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// recordingTSDB captures raw events (before series-label projection) so a test can assert the P0 tag
// set and dimensions actually rode along, not just that a sample landed.
type recordingTSDB struct {
	mu     sync.Mutex
	events []metricevent.Event
}

func (r *recordingTSDB) PutMetric(_ context.Context, ev metricevent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}
func (r *recordingTSDB) Query(map[string]string) ([]Sample, error) { return nil, nil }

func (r *recordingTSDB) byName() map[string]metricevent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[string]metricevent.Event{}
	for _, e := range r.events {
		m[e.MetricName] = e
	}
	return m
}

func p0() P0Tags {
	return P0Tags{
		VariantID:  "var_1",
		RunID:      "run_1",
		NodeID:     "node_1",
		CaseID:     "case_1",
		Seed:       7,
		ConfigHash: "b6d81b360a5672d80c27430f39153e2c04e9a1a5b3d3a1c0e2b8f7a6c5d4e3f2",
	}
}

// Task 1.9: a lossy policy emits assembled tokens, source messages, AND a drop-ratio event, each
// carrying the full P0 tag set (so the emission-boundary gate admits it) and the policy dimension.
func TestEmitContextAssembly_LossyPolicyEmitsDropRatioWithP0Tags(t *testing.T) {
	tsdb := &recordingTSDB{}
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: tsdb, Eval: NewMemEvalStore()})
	defer col.Close()

	EmitContextAssembly(col, p0(), ContextAssembly{
		Policy: "semantic-compaction", AssembledTokens: 120, SourceMessages: 40, Lossy: true, DropRatio: 0.7,
	})
	col.Flush()

	got := tsdb.byName()
	for _, name := range []string{MetricContextAssembledTokens, MetricContextSourceMessages, MetricContextDropRatio} {
		ev, ok := got[name]
		if !ok {
			t.Fatalf("metric %q was not emitted (gate may have rejected it for a missing tag)", name)
		}
		// The event survived the emission-boundary gate, which proves the seven-tag set is complete.
		if err := ev.Validate(); err != nil {
			t.Errorf("%q failed the P0 tag contract: %v", name, err)
		}
		if ev.Dimensions[AttrContextPolicy] != "semantic-compaction" {
			t.Errorf("%q missing context_policy dimension: %+v", name, ev.Dimensions)
		}
	}
	if v := got[MetricContextDropRatio].Value; v == nil || *v != 0.7 {
		t.Errorf("drop ratio value wrong: %v", got[MetricContextDropRatio].Value)
	}
	if _, hasChunks := got[MetricContextRetrievedChunks]; hasChunks {
		t.Error("non-retrieval policy must not emit a retrieved-chunks event")
	}
}

// A lossless policy emits no drop-ratio event (a 0.0 must not be published as if it were measured).
func TestEmitContextAssembly_LosslessPolicyOmitsDropRatio(t *testing.T) {
	tsdb := &recordingTSDB{}
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: tsdb, Eval: NewMemEvalStore()})
	defer col.Close()

	EmitContextAssembly(col, p0(), ContextAssembly{Policy: "sliding-window", AssembledTokens: 50, SourceMessages: 8})
	col.Flush()

	got := tsdb.byName()
	if _, ok := got[MetricContextDropRatio]; ok {
		t.Error("lossless policy emitted a drop-ratio event")
	}
	if _, ok := got[MetricContextAssembledTokens]; !ok {
		t.Error("assembled-tokens event missing")
	}
}

// A retrieval policy additionally emits the retrieved-chunk count.
func TestEmitContextAssembly_RetrievalEmitsChunkCount(t *testing.T) {
	tsdb := &recordingTSDB{}
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: tsdb, Eval: NewMemEvalStore()})
	defer col.Close()

	EmitContextAssembly(col, p0(), ContextAssembly{Policy: "rag-retrieval", AssembledTokens: 300, SourceMessages: 5, RetrievedChunks: 4})
	col.Flush()

	got := tsdb.byName()
	ev, ok := got[MetricContextRetrievedChunks]
	if !ok {
		t.Fatal("retrieval policy did not emit a retrieved-chunks event")
	}
	if v := ev.Value; v == nil || *v != 4 {
		t.Errorf("retrieved-chunks value = %v, want 4", ev.Value)
	}
}
