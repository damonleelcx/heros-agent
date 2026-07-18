package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// Task 4.1: a run of a 3-node graph with a tool call produces one run span, three node spans, and the
// tool call as a child span — an operator can drill run -> node -> tool.
func TestSection4_DrillableHierarchy(t *testing.T) {
	gw, inst, sink, entry := testRig(t)
	seed := int64(7)
	rc := RunContext{VariantID: "v1", RunID: "run_h", ConfigHash: testConfigHash, Seed: seed, CaseID: "c1"}
	tracer := inst.StartRun(rc)

	nodes := []string{"n_a", "n_b", "n_c"}
	for i, node := range nodes {
		nodeRC := rc.WithNode(node, 0)
		ctx := NewContext(context.Background(), nodeRC)
		tracer.NodeStarted(ctx, node)
		if _, err := executor.CallProvider(ctx, gw, entry,
			providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
			executor.NodeInvocation{RunID: rc.RunID, NodeID: node, AttemptGroup: 0, Seed: &seed}); err != nil {
			t.Fatalf("node %s: %v", node, err)
		}
		if i == 0 {
			// One tool call on the first node.
			start := time.Now()
			tracer.ToolCall(ctx, nodeRC, "web_search", 0, start, start.Add(time.Millisecond), true)
		}
		tracer.NodeFinished(ctx, node)
	}
	tracer.EndRun(context.Background())

	runSpans := sink.spansOfKind(SpanKindRun)
	nodeSpans := sink.spansOfKind(SpanKindNode)
	toolSpans := sink.spansOfKind(SpanKindTool)
	if len(runSpans) != 1 || len(nodeSpans) != 3 || len(toolSpans) != 1 {
		t.Fatalf("hierarchy = %d run, %d node, %d tool; want 1/3/1", len(runSpans), len(nodeSpans), len(toolSpans))
	}
	run := runSpans[0]

	// Drill: every node hangs under the run; the tool hangs under its node; one trace throughout.
	nodeByID := map[string]Span{}
	for _, ns := range nodeSpans {
		if ns.ParentSpanID != run.SpanID {
			t.Errorf("node span %v is not a child of the run span", ns.Attributes[AttrNodeID])
		}
		if ns.TraceID != run.TraceID {
			t.Errorf("node span is in a different trace from the run")
		}
		nodeByID[ns.Attributes[AttrNodeID].(string)] = ns
	}
	tool := toolSpans[0]
	if tool.ParentSpanID != nodeByID["n_a"].SpanID {
		t.Errorf("tool child span does not hang under node n_a")
	}
	if tool.TraceID != run.TraceID {
		t.Errorf("tool span is in a different trace from the run")
	}
}

// Task 4.2: spans follow the OTel GenAI semantic conventions and carry the seven tags as attributes —
// not a bespoke logging layer.
func TestSection4_SpansUseGenAIConventions(t *testing.T) {
	gw, inst, sink, entry := testRig(t)
	runFixture(t, gw, inst, entry, []string{"n_a"})

	nodeSpans := sink.spansOfKind(SpanKindNode)
	if len(nodeSpans) != 1 {
		t.Fatalf("want 1 node span, got %d", len(nodeSpans))
	}
	sp := nodeSpans[0]

	// GenAI convention attributes present.
	for _, key := range []string{AttrGenAISystem, AttrGenAIOperationName, AttrGenAIRequestModel, AttrGenAIUsageInput, AttrGenAIUsageOutput} {
		if _, ok := sp.Attributes[key]; !ok {
			t.Errorf("node span missing GenAI convention attribute %q", key)
		}
	}
	if sp.Name != "chat gpt-5" {
		t.Errorf("span name = %q, want GenAI convention %q", sp.Name, "chat gpt-5")
	}
	// The seven tags carried as OTel attributes.
	for _, tag := range []string{AttrVariantID, AttrRunID, AttrNodeID, AttrCaseID, AttrSeed, AttrConfigHash} {
		if v, ok := sp.Attributes[tag]; !ok || v == nil {
			t.Errorf("node span missing seven-tag attribute %q", tag)
		}
	}
}

// Task 4.3: the sampler keeps whole traces (never half a trace), keeps error spans unconditionally,
// and is deterministic.
func TestSection4_SamplerBoundsSpanVolume(t *testing.T) {
	// Keep-all and drop-all extremes.
	all := NewSpanSampler(1, true)
	none := NewSpanSampler(0, true)
	okSpan := Span{TraceID: "trace_x", Status: SpanStatusOK}
	errSpan := Span{TraceID: "trace_x", Status: SpanStatusError}
	if !all.Keep(okSpan) {
		t.Error("ratio=1 should keep every span")
	}
	if none.Keep(okSpan) {
		t.Error("ratio=0 should drop a healthy span")
	}
	if !none.Keep(errSpan) {
		t.Error("an error span must be kept even at ratio=0 — a failure must never be sampled away")
	}

	// Whole-trace consistency + determinism: every span of a trace gets one verdict, stable on re-ask.
	half := NewSpanSampler(0.5, false)
	for _, tid := range []string{"a", "b", "c", "d", "e"} {
		v1 := half.KeepTrace(tid)
		v2 := half.KeepTrace(tid)
		if v1 != v2 {
			t.Errorf("sampler is non-deterministic for trace %q", tid)
		}
	}

	// Roughly `ratio` of many traces are kept (statistical, wide tolerance).
	kept := 0
	const n = 2000
	for i := 0; i < n; i++ {
		if half.KeepTrace(TraceID("run_" + itoa(i))) {
			kept++
		}
	}
	if kept < n/4 || kept > 3*n/4 {
		t.Errorf("kept %d/%d traces at ratio 0.5; expected roughly half", kept, n)
	}
}

// Retention defaults are bounded and ordered by store volume/value (spans shortest, eval longest).
func TestSection4_RetentionDefaultsAreBounded(t *testing.T) {
	r := DefaultRetention()
	if r.Spans <= 0 || r.Metrics <= 0 || r.EvalResults <= 0 {
		t.Fatalf("retention must be bounded: %+v", r)
	}
	if r.Spans > r.Metrics || r.Metrics > r.EvalResults {
		t.Errorf("retention should grow with value/shrink with volume: spans %v, metrics %v, eval %v",
			r.Spans, r.Metrics, r.EvalResults)
	}
	now := time.Now()
	if !r.expired(r.Spans, now.Add(-8*24*time.Hour), now) {
		t.Error("an 8-day-old span should be past the 7-day span retention")
	}
	if r.expired(r.Spans, now.Add(-1*time.Hour), now) {
		t.Error("a 1-hour-old span should be within retention")
	}
}

var _ = registry.ModelEntry{}
