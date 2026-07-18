package telemetry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// adversarial_test.go is task 9.4: the self-review that TRIES to break each invariant. Each test is a
// specific attack the design names as a risk; a green here means the attack failed.

// captureLogger records warnings so a test can assert a drop/gap was LOGGED, not silent.
type captureLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureLogger) Warnf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *captureLogger) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.msgs...)
}
func (c *captureLogger) contains(sub string) bool {
	for _, m := range c.all() {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// Attack 1: an under-tagged event. It must reach NO store.
func TestAdversarial_UnderTaggedEventReachesNoStore(t *testing.T) {
	tsdb := NewMemTSDB(0)
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: tsdb, Eval: NewMemEvalStore()})
	t.Cleanup(col.Close)
	ev := sampleRC().event(MetricCostUSD, 1, UnitUSD, time.Now(), nil)
	ev.ConfigHash = "" // sabotage
	col.EmitMetric(context.Background(), ev)
	col.Flush()
	if s, _ := tsdb.Query(map[string]string{"metric_name": MetricCostUSD}); len(s) != 0 {
		t.Fatal("an under-tagged event was written to the TSDB")
	}
	if col.GateStats().Rejected == 0 {
		t.Error("the gate did not record the rejection")
	}
}

// Attack 2: try to make case_id a TSDB series label. It must never become one.
func TestAdversarial_CaseIDCannotBecomeATSDBLabel(t *testing.T) {
	ev := sampleRC().event(MetricLatencyTotalMS, 5, UnitMS, time.Now(), nil)
	labels := SeriesLabels(ev)
	if _, isLabel := labels[AttrCaseID]; isLabel {
		t.Fatal("case_id became a TSDB label")
	}
	// And a query BY case_id is refused, not silently answered with the wrong series.
	tsdb := NewMemTSDB(0)
	tsdb.PutMetric(context.Background(), ev)
	if _, err := tsdb.Query(map[string]string{AttrCaseID: "case_1"}); err == nil {
		t.Error("the TSDB answered a case_id query; it must refuse (case_id is not a label)")
	}
}

// Attack 3: plant a secret in a span attribute. It must be scrubbed before the store.
func TestAdversarial_SecretInASpanIsScrubbed(t *testing.T) {
	spans := NewMemSpanStore(0)
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: NewMemTSDB(0), Eval: NewMemEvalStore()})
	t.Cleanup(col.Close)
	col.EmitSpan(context.Background(), Span{
		SpanID: "s1", TraceID: "t1",
		Attributes: map[string]any{AttrConfigHash: testConfigHash, AttrRunID: "r", AttrVariantID: "v",
			"stash": "sk-super-secret-key-abcdef123456"},
	})
	col.Flush()
	got := spans.SpansByConfigHash(testConfigHash)
	if len(got) != 1 || containsSecret(got[0].Attributes["stash"].(string)) {
		t.Fatalf("secret survived into the span store: %+v", got)
	}
}

// Attack 4: kill the collector mid-run. The run must complete.
func TestAdversarial_CollectorDownMidRunCompletesTheRun(t *testing.T) {
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: NewMemTSDB(0), Eval: NewMemEvalStore()})
	gw, inst, entry := testRigWithSink(t, col)
	col.Close() // dead before the run even starts
	seed := int64(1)
	rc := RunContext{VariantID: "v", RunID: "run_x", ConfigHash: testConfigHash, Seed: seed, CaseID: "c"}
	tracer := inst.StartRun(rc)
	for _, n := range []string{"n_a", "n_b"} {
		ctx := NewContext(context.Background(), rc.WithNode(n, 0))
		tracer.NodeStarted(ctx, n)
		if _, err := executor.CallProvider(ctx, gw, entry,
			providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
			executor.NodeInvocation{RunID: "run_x", NodeID: n, AttemptGroup: 0, Seed: &seed}); err != nil {
			t.Fatalf("the run failed because telemetry was down: %v", err)
		}
		tracer.NodeFinished(ctx, n)
	}
	tracer.EndRun(context.Background())
}

// Attack 5: process the same invocation twice (a retry / redelivery). Cost must be counted once.
func TestAdversarial_RetryDoesNotDoubleCountCost(t *testing.T) {
	tsdb := NewMemTSDB(0)
	col := NewCollector(CollectorConfig{Spans: NewMemSpanStore(0), TSDB: tsdb, Eval: NewMemEvalStore()})
	t.Cleanup(col.Close)
	rc := sampleRC()
	pb, _ := NewPriceBook("v1")
	pb.Set("openai", "gpt-5", ModelInfo{InputPerMTok: 1, OutputPerMTok: 1, ContextWindow: 1000})
	d := callDetail{provider: "openai", modelID: "gpt-5", usage: tokenUsage{input: 100, output: 40},
		duration: time.Millisecond, attempts: 1, idempotencyKey: rc.IdempotencyKey()}
	for i := 0; i < 2; i++ { // emitted twice
		evs, _ := MetricSet(rc, d, time.Now(), pb)
		for _, ev := range evs {
			col.EmitMetric(context.Background(), ev)
		}
	}
	col.Flush()
	s, _ := tsdb.Query(map[string]string{"metric_name": MetricCostUSD, AttrConfigHash: rc.ConfigHash})
	if len(s) != 1 {
		t.Errorf("cost was written %d times for one invocation; a retry double-counted", len(s))
	}
}

// Attack 6: try to run a node through the gateway WITHOUT instrumentation. With the observer attached
// there is no such path — every call emits. And a call that somehow lacks a run context is logged
// loudly, never silently swallowed as un-instrumented.
func TestAdversarial_NoUnInstrumentedNode(t *testing.T) {
	// (a) With the instrument attached, a fixture node's call emits the full operational set — there is
	// no way to execute it un-instrumented.
	gw, inst, sink, entry := testRig(t)
	runFixture(t, gw, inst, entry, []string{"n_a"})
	if len(sink.metricsFor("n_a")) == 0 {
		t.Fatal("a node executed through the gateway with no metrics — an un-instrumented node")
	}

	// (b) A call with no run context is logged, not silently dropped (which would look like an
	// un-instrumented node to a downstream consumer).
	log := &captureLogger{}
	pb, _ := NewPriceBook("v1")
	pb.Set(providergateway.ProviderOpenAI, "gpt-5", ModelInfo{InputPerMTok: 1, OutputPerMTok: 1, ContextWindow: 1000})
	rec := &recordingSink{}
	noCtxInst, _ := NewInstrument(rec, pb, WithLogger(log))
	noCtxInst.OnCall(context.Background(), providergateway.CallInfo{Provider: "openai", ModelID: "gpt-5"})
	if len(rec.metrics) != 0 {
		t.Error("a call with no run context emitted under-tagged metrics")
	}
	if !log.contains("no run context") {
		t.Error("a call with no run context was silently dropped instead of logged")
	}
}
