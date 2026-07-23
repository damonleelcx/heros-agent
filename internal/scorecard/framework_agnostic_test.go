package scorecard_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/scorecard"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// framework_agnostic_test.go is task 12.6: the load-bearing proof of G11 / FR13 / FR14 — the engine
// produces a scorecard from a trace set whose IR has NO edges, NO per-node output contracts, and NO
// P3.5 pattern labels (a hand-rolled agent with no discovered graph), and it does so EXPLICITLY:
// unclassified nodes are surfaced as such and pattern-scoped modes are refused, not misapplied.

// handRolledIR: bare nodes, no edges, no contracts, no pattern labels — modeled on a real hand-rolled
// agent (e.g. hermes-agent, whose discovery yielded call sites with 0 edges and no labels).
func handRolledIR() *discovery.IR {
	return &discovery.IR{
		Workflow: discovery.IRWorkflow{ID: "hand-rolled", Language: "python"},
		Nodes: []discovery.IRNode{
			{NodeID: "run_agent.loop"},
			{NodeID: "auxiliary_client.call"},
			{NodeID: "trajectory_compressor.summarize"},
		},
	}
}

func faSpan(caseID, node string, i int, cost, lat float64, attrs map[string]any) telemetry.Span {
	base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
	a := map[string]any{telemetry.AttrNodeID: node, telemetry.AttrCostUSD: cost, telemetry.AttrLatencyMS: lat, telemetry.AttrNodeFailed: false}
	for k, val := range attrs {
		a[k] = val
	}
	return telemetry.Span{TraceID: telemetry.TraceID(caseID), SpanID: telemetry.NodeSpanID(caseID + node),
		Kind: telemetry.SpanKindNode, Name: node, StartTime: base, EndTime: base.Add(time.Duration(lat) * time.Millisecond),
		Status: telemetry.SpanStatusOK, Attributes: a}
}

func faOverflow(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		faSpan(id, "run_agent.loop", 0, 0.001, 50, nil),
		faSpan(id, "auxiliary_client.call", 1, 0.100, 100, map[string]any{telemetry.MetricContextDropRatio: 0.5}), // overflow signal
		faSpan(id, "trajectory_compressor.summarize", 2, 0.002, 800, nil),
	}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "hand-rolled"}, Trace: tr}
}

func faToolEmpty(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	tool := telemetry.Span{TraceID: telemetry.TraceID(id), SpanID: telemetry.ToolSpanID(id+"aux", "search", 0),
		Kind: telemetry.SpanKindTool, Name: "search", Status: telemetry.SpanStatusOK,
		StartTime: time.Unix(1_700_000_100, 0), EndTime: time.Unix(1_700_000_100, 0).Add(30 * time.Millisecond),
		Attributes: map[string]any{telemetry.AttrNodeID: "auxiliary_client.call", telemetry.AttrToolReason: "empty"}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		faSpan(id, "run_agent.loop", 0, 0.001, 50, nil), faSpan(id, "auxiliary_client.call", 1, 0.002, 100, nil), tool}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "hand-rolled"}, Trace: tr}
}

func faMultiHop(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	var spans []telemetry.Span
	for i, n := range []string{"run_agent.loop", "auxiliary_client.call", "trajectory_compressor.summarize", "trajectory_compressor.summarize"} {
		spans = append(spans, faSpan(id, n, i, 0.002, 120, nil))
	}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "hand-rolled"}, Trace: tr}
}

// countingHandler records how many WARN records slog emitted, to prove the reduced-coverage signal is
// not silent (logging-conventions).
type countingHandler struct{ warns int32 }

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		atomic.AddInt32(&h.warns, 1)
	}
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }

// FR13/FR14 load-bearing: a hand-rolled agent with no graph is still fully scorecarded, explicitly.
func TestFrameworkAgnostic_HandRolledNoGraph(t *testing.T) {
	h := &countingHandler{}
	eng := &scorecard.Engine{Store: reportstore.NewMemStore(), Log: slog.New(h)}
	cases := []attribution.FailingCase{
		faOverflow("of-1"), faOverflow("of-2"),
		faToolEmpty("te-1"), faToolEmpty("te-2"),
		faMultiHop("mh-1"), faMultiHop("mh-2"),
	}
	view, err := eng.Generate(context.Background(), scorecard.GenerateInput{
		IR: handRolledIR(), Variant: attribution.Variant{VariantID: "v-hr", ConfigHash: "cfg", EvalSetHash: "es", WorkflowID: "hand-rolled"},
		FailingCases: cases,
		Overall:      scorecard.OverallMetrics{TaskSuccess: 0.5, NCases: 12, NFailing: len(cases), CostUSD: 0.4, LatencyMS: 3000},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// A scorecard IS produced, from traces alone.
	if view.State != scorecard.StateReady {
		t.Fatalf("state = %q, want ready (a no-graph agent must still be scorecarded)", view.State)
	}
	if len(view.Nodes) != 3 {
		t.Fatalf("per-node breakdown from traces should have 3 nodes; got %d", len(view.Nodes))
	}
	// First-divergence from TRACE ORDER: the overflow-flagged node (auxiliary_client.call) is the
	// first node whose span carries the overflow signal; it should be a hotspot (bottleneck) at least.
	// Bottleneck flags come from the spans, not from any graph.
	var haveBottleneck bool
	for _, n := range view.Nodes {
		if len(n.BottleneckDimensions) > 0 {
			haveBottleneck = true
		}
	}
	if !haveBottleneck {
		t.Error("bottleneck flags should be derived from spans even with no graph")
	}
	// Clusters from traces.
	if len(view.Clusters) < 2 {
		t.Errorf("clustering should still produce categories from traces; got %d", len(view.Clusters))
	}

	// EXPLICIT degradation: every node is unclassified, surfaced as such, counted.
	if view.ClassifiedNodeCount != 0 || view.UnclassifiedNodeCount != 3 {
		t.Errorf("classification coverage wrong: classified=%d unclassified=%d, want 0/3", view.ClassifiedNodeCount, view.UnclassifiedNodeCount)
	}
	for _, n := range view.Nodes {
		if n.Classified {
			t.Errorf("node %s must be unclassified on a no-label IR", n.NodeID)
		}
	}
	// Pattern-agnostic detectors fire; pattern-scoped modes are refused.
	sawAgnostic := false
	for _, d := range view.Diagnoses {
		switch d.Code {
		case diagnosis.CauseContextOverflow, diagnosis.CausePromptFormatDrift, diagnosis.CauseLostInMiddle,
			diagnosis.CauseModelCapabilityGap, diagnosis.CauseToolSchemaMismatch, diagnosis.CauseRetrievalMiss:
			sawAgnostic = true
		case diagnosis.CauseMisroute, diagnosis.CauseNonConvergence, diagnosis.CauseInfeasiblePlan,
			diagnosis.CauseCircularPlan, diagnosis.CauseDegradationOnRevisit:
			t.Errorf("pattern-scoped code %q emitted on an unclassified node — must be refused", d.Code)
		}
		if d.Classified {
			t.Errorf("diagnosis on %s marked classified on a no-label IR", d.NodeID)
		}
	}
	if !sawAgnostic {
		t.Errorf("a pattern-agnostic detector (context overflow) should have fired; diagnoses=%+v", view.Diagnoses)
	}

	// The reduced coverage was NOT silent: one WARN per unclassified node.
	if got := atomic.LoadInt32(&h.warns); got != 3 {
		t.Errorf("expected 3 unclassified-node WARNs, got %d", got)
	}
}

// FR13/FR14: enrichment SHARPENS, it never GATES — the same traces with pattern labels classify the
// nodes, while the bare run already produced a usable scorecard.
func TestFrameworkAgnostic_EnrichmentSharpensNeverGates(t *testing.T) {
	ctx := context.Background()
	cases := []attribution.FailingCase{faOverflow("of-1"), faToolEmpty("te-1"), faMultiHop("mh-1")}
	v := attribution.Variant{VariantID: "v-e", ConfigHash: "cfg", EvalSetHash: "es", WorkflowID: "hand-rolled"}

	bare := &scorecard.Engine{Store: reportstore.NewMemStore()}
	bareView, err := bare.Generate(ctx, scorecard.GenerateInput{IR: handRolledIR(), Variant: v, FailingCases: cases,
		Overall: scorecard.OverallMetrics{NFailing: len(cases), NCases: 10}})
	if err != nil {
		t.Fatal(err)
	}

	// Enriched IR: same node ids, now WITH pattern labels.
	enrichedIR := handRolledIR()
	labels := map[string]patternclassifier.Pattern{
		"run_agent.loop": patternclassifier.Routing, "auxiliary_client.call": patternclassifier.ToolUse,
		"trajectory_compressor.summarize": patternclassifier.Reflection,
	}
	for i := range enrichedIR.Nodes {
		enrichedIR.Nodes[i].PatternLabels = []discovery.IRPatternLabel{{
			Pattern: string(labels[enrichedIR.Nodes[i].NodeID]), Confidence: patternclassifier.ConfidenceTopologyDetermined,
			Source: string(patternclassifier.SourceRule), DetectorID: "e", TaxonomyVersion: patternclassifier.TaxonomyVersion}}
	}
	enriched := &scorecard.Engine{Store: reportstore.NewMemStore()}
	enrichedView, err := enriched.Generate(ctx, scorecard.GenerateInput{IR: enrichedIR, Variant: v, FailingCases: cases,
		Overall: scorecard.OverallMetrics{NFailing: len(cases), NCases: 10}})
	if err != nil {
		t.Fatal(err)
	}

	// Bare run already usable.
	if bareView.State != scorecard.StateReady || len(bareView.Nodes) == 0 {
		t.Fatalf("bare (no-enrichment) run must already produce a usable scorecard; got state=%q nodes=%d", bareView.State, len(bareView.Nodes))
	}
	if bareView.ClassifiedNodeCount != 0 {
		t.Errorf("bare run should classify nothing; got %d", bareView.ClassifiedNodeCount)
	}
	// Enrichment sharpens: nodes now classified.
	if enrichedView.ClassifiedNodeCount != 3 || enrichedView.UnclassifiedNodeCount != 0 {
		t.Errorf("enriched run should classify all 3 nodes; got classified=%d unclassified=%d",
			enrichedView.ClassifiedNodeCount, enrichedView.UnclassifiedNodeCount)
	}
}
