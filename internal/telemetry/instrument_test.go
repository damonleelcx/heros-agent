package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// recordingSink captures everything the instrument emits, for assertions. Non-blocking and unfailing,
// as the Sink contract requires.
type recordingSink struct {
	mu      sync.Mutex
	metrics []metricevent.Event
	spans   []Span
}

func (r *recordingSink) EmitMetric(_ context.Context, ev metricevent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, ev)
}

func (r *recordingSink) EmitSpan(_ context.Context, sp Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, sp)
}

func (r *recordingSink) metricsFor(nodeID string) []metricevent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []metricevent.Event
	for _, m := range r.metrics {
		if m.NodeID == nodeID {
			out = append(out, m)
		}
	}
	return out
}

func (r *recordingSink) spansOfKind(k SpanKind) []Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Span
	for _, s := range r.spans {
		if s.Kind == k {
			out = append(out, s)
		}
	}
	return out
}

// A valid 64-char lowercase-hex config_hash (16-char block × 4).
const testConfigHash = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

// openAITokenBody is an OpenAI-shaped response with token usage, so cost/token metrics have real
// numbers to derive from.
const openAITokenBody = `{"choices":[{"finish_reason":"stop","message":{"content":"hi"}}],
	"usage":{"prompt_tokens":100,"completion_tokens":40}}`

// testRig stands up the whole instrumented path: a stub provider, a gateway with the Instrument
// attached (one line), a priced model. It is the substrate; the "workflow" is the node loop in the
// test, which contains ZERO telemetry code.
func testRig(t *testing.T) (*providergateway.Gateway, *Instrument, *recordingSink, *registry.ModelEntry) {
	t.Helper()
	sink := &recordingSink{}
	gw, inst, entry := testRigWithSink(t, sink)
	return gw, inst, sink, entry
}

// testRigWithSink stands up the instrumented path against any Sink (a recording sink for span/metric
// assertions, or a real Collector for routing tests). The "workflow" is still zero telemetry code.
func testRigWithSink(t *testing.T, sink Sink) (*providergateway.Gateway, *Instrument, *registry.ModelEntry) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(openAITokenBody))
	}))
	t.Cleanup(srv.Close)

	pb, err := NewPriceBook("2026-07-18.1")
	if err != nil {
		t.Fatalf("PriceBook: %v", err)
	}
	pb.Set(providergateway.ProviderOpenAI, "gpt-5", ModelInfo{
		InputPerMTok: 1.0, OutputPerMTok: 2.0, CacheReadPerMTok: 0.1, ContextWindow: 200_000,
	})

	inst, err := NewInstrument(sink, pb)
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}
	gw := providergateway.New(
		providergateway.StaticSecrets{providergateway.ProviderOpenAI: {APIKey: "sk-test-openai-secret-value"}},
		providergateway.WithBaseURL(providergateway.ProviderOpenAI, srv.URL),
		providergateway.WithObserver(inst),
	)
	entry := &registry.ModelEntry{
		VersionID: strings.Repeat("d", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: providergateway.ProviderOpenAI, ModelID: "gpt-5"},
	}
	return gw, inst, entry
}

// runFixture executes a multi-node "workflow" through the gateway with ZERO telemetry code in the node
// bodies — the run path (this substrate loop) attaches the run context and brackets each call. It is
// the fixture tasks 1.7 / 9.1 require: no per-node annotation, full operational set out the other side.
func runFixture(t *testing.T, gw *providergateway.Gateway, inst *Instrument, entry *registry.ModelEntry, nodes []string) RunContext {
	t.Helper()
	seed := int64(7)
	rc := RunContext{
		VariantID: "variant_alpha", RunID: "run_fixture_1", ConfigHash: testConfigHash,
		Seed: seed, CaseID: "case_1",
	}
	tracer := inst.StartRun(rc)
	for _, node := range nodes {
		nodeRC := rc.WithNode(node, 0)
		ctx := NewContext(context.Background(), nodeRC)
		tracer.NodeStarted(ctx, node)
		// The ONLY line the substrate needs to call a provider for a node. No metrics API, no annotation.
		_, err := executor.CallProvider(ctx, gw, entry,
			providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
			executor.NodeInvocation{RunID: rc.RunID, NodeID: node, AttemptGroup: 0, Seed: &seed})
		if err != nil {
			t.Fatalf("node %s: CallProvider: %v", node, err)
		}
		tracer.NodeFinished(ctx, node)
	}
	tracer.EndRun(context.Background())
	return rc
}

// Task 1.7 / 9.1 headline: a workflow with ZERO telemetry code emits the full operational taxonomy for
// every provider call, fully tagged and keyed by config_hash, plus a node span per node.
func TestSection1_ZeroUserCodeEmitsFullOperationalSet(t *testing.T) {
	gw, inst, sink, entry := testRig(t)
	nodes := []string{"n_a", "n_b", "n_c"}
	rc := runFixture(t, gw, inst, entry, nodes)

	// Every emitted metric event is FULLY TAGGED and keyed by config_hash — proved by running each
	// through the same emission-boundary rule a store applies (metricevent.Validate).
	sink.mu.Lock()
	all := append([]metricevent.Event(nil), sink.metrics...)
	sink.mu.Unlock()
	if len(all) == 0 {
		t.Fatal("no metrics emitted for a run with instrumented nodes")
	}
	for _, ev := range all {
		if err := ev.Validate(); err != nil {
			t.Errorf("emitted event is not fully tagged: %v (%+v)", err, ev)
		}
		if ev.ConfigHash != rc.ConfigHash {
			t.Errorf("event %q not keyed by the run's config_hash: %q", ev.MetricName, ev.ConfigHash)
		}
	}

	// Per node, the FULL operational per-call taxonomy is present — exactly OperationalMetricNames.
	for _, node := range nodes {
		got := map[string]bool{}
		for _, ev := range sink.metricsFor(node) {
			got[ev.MetricName] = true
		}
		for _, want := range OperationalMetricNames {
			if !got[want] {
				t.Errorf("node %s: missing operational metric %q — the taxonomy is incomplete", node, want)
			}
		}
	}

	// A node span per node, drillable under one run span.
	nodeSpans := sink.spansOfKind(SpanKindNode)
	if len(nodeSpans) != len(nodes) {
		t.Errorf("got %d node spans, want %d (one per node execution)", len(nodeSpans), len(nodes))
	}
	runSpans := sink.spansOfKind(SpanKindRun)
	if len(runSpans) != 1 {
		t.Fatalf("got %d run spans, want exactly 1", len(runSpans))
	}
	for _, ns := range nodeSpans {
		if ns.ParentSpanID != runSpans[0].SpanID {
			t.Errorf("node span %s does not hang under the run span", ns.Name)
		}
		if ns.TraceID != runSpans[0].TraceID {
			t.Errorf("node span %s is in a different trace from the run", ns.Name)
		}
	}

	// Run-scoped throughput/concurrency (task 1.6) — present, tagged with the run-scope sentinel.
	runScoped := map[string]bool{}
	for _, ev := range sink.metricsFor(NodeIDRun) {
		runScoped[ev.MetricName] = true
	}
	for _, want := range RunScopedMetricNames {
		if !runScoped[want] {
			t.Errorf("missing run-scoped metric %q", want)
		}
	}
}

// A node's cost is derived from token counts × the pinned price, and carries the pricebook version so
// it is attributable (task 1.3).
func TestSection1_CostIsTokensTimesPinnedPrice(t *testing.T) {
	gw, inst, sink, entry := testRig(t)
	runFixture(t, gw, inst, entry, []string{"n_a"})

	var cost *metricevent.Event
	for i := range sink.metrics {
		if sink.metrics[i].MetricName == MetricCostUSD {
			cost = &sink.metrics[i]
			break
		}
	}
	if cost == nil {
		t.Fatal("no cost_usd metric emitted")
	}
	// 100 input @ $1/M + 40 output @ $2/M = 0.0001 + 0.00008 = 0.00018.
	const want = 100*1.0/1e6 + 40*2.0/1e6
	if got := *cost.Value; got < want-1e-12 || got > want+1e-12 {
		t.Errorf("cost_usd = %v, want %v (tokens × pinned price)", got, want)
	}
	if cost.Dimensions[AttrPriceBookVer] != "2026-07-18.1" {
		t.Errorf("cost is not attributable to a price source: dims=%v", cost.Dimensions)
	}
}

// Reliability metrics are emitted even when a call fails, carrying the retry count and rate-limit
// signal a failure most needs (task 1.5). A provider that 429s then fails is the case that breaks a
// naive instrument that reads attempts from a nil response.
func TestSection1_ReliabilityMetricsSurviveFailure(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusTooManyRequests) // always 429 -> retried, then exhausted
	}))
	t.Cleanup(srv.Close)

	pb, _ := NewPriceBook("v1")
	pb.Set(providergateway.ProviderOpenAI, "gpt-5", ModelInfo{InputPerMTok: 1, OutputPerMTok: 1, ContextWindow: 1000})
	sink := &recordingSink{}
	inst, _ := NewInstrument(sink, pb)
	gw := providergateway.New(
		providergateway.StaticSecrets{providergateway.ProviderOpenAI: {APIKey: "sk-x"}},
		providergateway.WithBaseURL(providergateway.ProviderOpenAI, srv.URL),
		providergateway.WithObserver(inst),
		providergateway.WithMaxRetries(2),
	)
	entry := &registry.ModelEntry{VersionID: strings.Repeat("e", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: providergateway.ProviderOpenAI, ModelID: "gpt-5"}}

	seed := int64(3)
	nodeRC := RunContext{VariantID: "v", RunID: "run_fail", ConfigHash: testConfigHash, Seed: seed, CaseID: "c"}.WithNode("n_a", 0)
	ctx := NewContext(context.Background(), nodeRC)
	_, err := executor.CallProvider(ctx, gw, entry,
		providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
		executor.NodeInvocation{RunID: "run_fail", NodeID: "n_a", AttemptGroup: 0, Seed: &seed})
	if err == nil {
		t.Fatal("expected the call to fail after exhausting retries")
	}

	find := func(name string) float64 {
		for _, ev := range sink.metrics {
			if ev.MetricName == name {
				return *ev.Value
			}
		}
		t.Fatalf("metric %q not emitted on a failed call", name)
		return 0
	}
	if find(MetricError) != 1 {
		t.Error("reliability_error should be 1 on a failed call")
	}
	if find(MetricRateLimitHit) != 1 {
		t.Error("reliability_rate_limit_hit should be 1 — the 429 must survive into the metric")
	}
	if got := find(MetricRetryCount); got != 2 {
		t.Errorf("reliability_retry_count = %v, want 2 (3 attempts, 2 retries) even though the response was nil", got)
	}
}
