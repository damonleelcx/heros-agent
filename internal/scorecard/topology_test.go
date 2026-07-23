package scorecard_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/linkage"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/scorecard"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// topology_test.go is task 13.6: the load-bearing proof that first-divergence orders by the RECOVERED
// edge DAG, not wall-clock. The fixture is adversarial — the start-time order and the recovered-edge
// order DISAGREE — so a pass can only mean the edges were consumed.

// topoIR: three contract-carrying nodes, no framework edges (hand-rolled).
func topoIR() *discovery.IR {
	contract := map[string]any{"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}}}
	node := func(id string) discovery.IRNode {
		return discovery.IRNode{NodeID: id, Kind: "static_definition",
			IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: contract}}
	}
	return &discovery.IR{Workflow: discovery.IRWorkflow{ID: "wf-topo"},
		Nodes: []discovery.IRNode{node("A"), node("B"), node("C")}}
}

// topoCase: A and B BOTH drop their output contract; C is clean. The spans are timed B(0) < A(1) < C(2)
// so raw start-time order is [B, A, C] → contract-first would pick B. The recovered chain is A→B→C, so
// edge order is [A, B, C] → contract-first must pick A. That divergence is the whole test.
func topoCase(id string) attribution.FailingCase {
	span := func(node string, i int, invalid bool) telemetry.Span {
		base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
		return telemetry.Span{TraceID: telemetry.TraceID(id), SpanID: telemetry.NodeSpanID(id + node),
			Kind: telemetry.SpanKindNode, Name: node, StartTime: base, EndTime: base.Add(100 * time.Millisecond),
			Status:     telemetry.SpanStatusOK,
			Attributes: map[string]any{telemetry.AttrNodeID: node, telemetry.AttrCostUSD: 0.01, telemetry.AttrLatencyMS: 100.0}}
	}
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"A": json.RawMessage(`{"junk":1}`), // contract violation
		"B": json.RawMessage(`{"junk":2}`), // contract violation
		"C": json.RawMessage(`{"a":"ok"}`),
	}}
	// start times: B before A before C
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span("B", 0, true), span("A", 1, true), span("C", 2, false),
	}}
	tr.Output, tr.Failed = json.RawMessage(`{"junk":0}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-topo"}, Trace: tr}
}

// The recovered static chain A→B→C (A's fn calls B's fn calls C's fn).
func topoCallSites() []linkage.CallSite {
	return []linkage.CallSite{
		{NodeID: "A", EnclosingSymbol: "fA", Callees: []string{"fB"}, Order: 0},
		{NodeID: "B", EnclosingSymbol: "fB", Callees: []string{"fC"}, Order: 1},
		{NodeID: "C", EnclosingSymbol: "fC", Order: 2},
	}
}

func topoVariant() attribution.Variant {
	return attribution.Variant{VariantID: "v-topo", ConfigHash: "cfg", EvalSetHash: "es", WorkflowID: "wf-topo"}
}

// 13.6: with recovered edges, first-divergence orders by the DAG (A), not wall-clock (B).
func TestTopology_FirstDivergenceFollowsEdgesNotStartTime(t *testing.T) {
	eng := &scorecard.Engine{Store: reportstore.NewMemStore()}
	view, err := eng.Generate(context.Background(), scorecard.GenerateInput{
		IR: topoIR(), Variant: topoVariant(),
		FailingCases:    []attribution.FailingCase{topoCase("c1"), topoCase("c2")},
		Overall:         scorecard.OverallMetrics{NFailing: 2, NCases: 5},
		StaticCallSites: topoCallSites(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The top first-divergence node must be A (edge order), not B (start-time order).
	if view.Nodes[0].NodeID != "A" {
		t.Fatalf("first-divergence hotspot = %q, want A (edge-DAG order, not wall-clock B)", view.Nodes[0].NodeID)
	}
	if !view.Topology.OrderedByTopology {
		t.Error("view should report it was ordered by the recovered topology")
	}
	if view.Topology.Provenance != string(linkage.ProvInferredStatic) {
		t.Errorf("topology provenance = %q, want inferred_static", view.Topology.Provenance)
	}
	// The recovered edges are surfaced with their provenance/signal (never rendered as certain).
	var sawCallGraph, sawInferred bool
	for _, e := range view.Topology.Edges {
		if e.Signal == string(linkage.SignalCallGraph) {
			sawCallGraph = true
		}
		if e.Provenance == string(linkage.ProvInferredStatic) {
			sawInferred = true
		}
		if e.Provenance == string(linkage.ProvFramework) {
			t.Errorf("no edge should be framework provenance on a hand-rolled IR; got %+v", e)
		}
	}
	if !sawCallGraph || !sawInferred {
		t.Errorf("recovered inferred_static call-graph edges should be surfaced; got %+v", view.Topology.Edges)
	}
}

// 13.6: remove the recovered edges → falls back to start-time order and still localizes (B, the
// first contract violator by wall-clock). Enrichment sharpens; its absence does not gate.
func TestTopology_FallsBackToStartTimeWithoutEdges(t *testing.T) {
	eng := &scorecard.Engine{Store: reportstore.NewMemStore()}
	view, err := eng.Generate(context.Background(), scorecard.GenerateInput{
		IR: topoIR(), Variant: topoVariant(),
		FailingCases: []attribution.FailingCase{topoCase("c1")},
		Overall:      scorecard.OverallMetrics{NFailing: 1, NCases: 5},
		// no StaticCallSites → only dynamic temporal edges, which equal start-time order
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Still localizes; by wall-clock the first contract violator is B.
	if view.Nodes[0].NodeID != "B" {
		t.Fatalf("without recovered edges, first-divergence should follow start-time (B); got %q", view.Nodes[0].NodeID)
	}
	// Highest provenance present is at most inferred_dynamic (temporal), never inferred_static.
	if view.Topology.Provenance == string(linkage.ProvInferredStatic) {
		t.Errorf("no static edges were supplied; provenance should not be inferred_static")
	}
}
