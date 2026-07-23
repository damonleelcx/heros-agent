package linkage

import (
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// 13.1: static recovery from the call-graph + shared-state signals, modeled on the real hand-rolled
// repo — a dispatch fn calls the create boundary, and calls share the `_session_messages` object.
func TestInferStatic_CallGraphAndSharedState(t *testing.T) {
	sites := []CallSite{
		{NodeID: "dispatch", EnclosingSymbol: "_dispatch_nonstreaming_api_request",
			Callees: []string{"create_boundary"}, StateRefs: []string{"self._session_messages"}, Order: 0},
		{NodeID: "create", EnclosingSymbol: "create_boundary",
			StateRefs: []string{"self._session_messages"}, Order: 1},
	}
	edges := InferStatic(sites)

	var callGraph, sharedState *Edge
	for i := range edges {
		switch edges[i].Signal {
		case SignalCallGraph:
			callGraph = &edges[i]
		case SignalSharedState:
			sharedState = &edges[i]
		}
	}
	if callGraph == nil || callGraph.From != "dispatch" || callGraph.To != "create" {
		t.Fatalf("call-graph edge dispatch→create not inferred; got %+v", edges)
	}
	if callGraph.Provenance != ProvInferredStatic {
		t.Errorf("call-graph edge provenance = %q, want inferred_static", callGraph.Provenance)
	}
	if sharedState == nil || sharedState.From != "dispatch" || sharedState.To != "create" {
		t.Fatalf("shared-state edge not inferred; got %+v", edges)
	}
	// shared-state is a weaker signal than an explicit call-graph edge.
	if sharedState.Confidence >= callGraph.Confidence {
		t.Errorf("shared-state confidence %.2f should be < call-graph %.2f", sharedState.Confidence, callGraph.Confidence)
	}
}

func dynSpan(node, spanID, parent, conv string, startSec int64) telemetry.Span {
	return telemetry.Span{
		SpanID: spanID, ParentSpanID: parent, Kind: telemetry.SpanKindNode, Name: node,
		StartTime:  time.Unix(1_700_000_000+startSec, 0),
		Attributes: map[string]any{telemetry.AttrNodeID: node, AttrConversationID: conv},
	}
}

// 13.2: dynamic recovery — parent-child is the strongest, temporal the weakest.
func TestInferDynamic_ParentChildAndThreadAndTemporal(t *testing.T) {
	spans := []telemetry.Span{
		dynSpan("A", "s1", "", "conv1", 0),
		dynSpan("B", "s2", "s1", "conv1", 1), // child of A, same conv
		dynSpan("C", "s3", "", "conv1", 2),   // same conv, later
	}
	edges := InferDynamic(spans)

	sig := map[Signal]*Edge{}
	for i := range edges {
		sig[edges[i].Signal] = &edges[i]
	}
	if e := sig[SignalSpanParent]; e == nil || e.From != "A" || e.To != "B" {
		t.Fatalf("parent-child edge A→B not inferred; got %+v", edges)
	}
	if sig[SignalSpanParent].Provenance != ProvInferredDynamic {
		t.Errorf("dynamic edge provenance wrong")
	}
	if sig[SignalSharedThread] == nil {
		t.Errorf("shared-thread edge not inferred; got %+v", edges)
	}
	if sig[SignalTemporal] == nil {
		t.Errorf("temporal edge not inferred")
	}
	// Ranking: parent-child (0.9) > shared-thread (0.7) > temporal (0.4).
	if !(sig[SignalSpanParent].Confidence > sig[SignalSharedThread].Confidence &&
		sig[SignalSharedThread].Confidence > sig[SignalTemporal].Confidence) {
		t.Errorf("dynamic signal confidences not ranked: %+v", edges)
	}
}

// 13.7: reconciliation — framework wins over inferred; a dynamic edge confirming a static one raises
// its confidence; an unconfirmed static edge is capped low.
func TestReconcile_PrecedenceAndConfirmation(t *testing.T) {
	static := []Edge{
		{From: "A", To: "B", Provenance: ProvInferredStatic, Confidence: 0.8, Signal: SignalCallGraph}, // confirmed below
		{From: "C", To: "D", Provenance: ProvInferredStatic, Confidence: 0.8, Signal: SignalCallGraph}, // never observed
	}
	dynamic := []Edge{
		{From: "A", To: "B", Provenance: ProvInferredDynamic, Confidence: 0.9, Signal: SignalSpanParent}, // confirms A→B
	}
	framework := []Edge{
		{From: "E", To: "F", Provenance: ProvFramework, Confidence: 1.0, Signal: SignalFramework},
	}
	topo := Reconcile(framework, static, dynamic)

	byEdge := map[string]Edge{}
	for _, e := range topo.Edges {
		byEdge[e.From+e.To] = e
	}
	// A→B: static wins the tier tie? No — dynamic rank(1) < static rank(2), so static wins the edge,
	// but its confidence is RAISED because a dynamic edge confirmed it.
	ab := byEdge["AB"]
	if ab.Provenance != ProvInferredStatic {
		t.Errorf("A→B should resolve to inferred_static (higher rank); got %q", ab.Provenance)
	}
	if ab.Confidence <= 0.8 {
		t.Errorf("confirmed static edge confidence should be raised above 0.8; got %.2f", ab.Confidence)
	}
	// C→D: static, never observed → capped low.
	cd := byEdge["CD"]
	if cd.Confidence > 0.5 {
		t.Errorf("unconfirmed static edge should be capped at 0.5; got %.2f", cd.Confidence)
	}
	// E→F: framework, untouched.
	if byEdge["EF"].Provenance != ProvFramework {
		t.Errorf("framework edge should survive reconciliation")
	}
}

// Topology.Order: first-divergence orders by the edge DAG, NOT wall-clock, when edges disagree.
func TestTopology_OrderFollowsEdgesNotStartTime(t *testing.T) {
	// Recovered chain A→B→C, but the tiebreak (start-time) order is C,A,B (scrambled).
	topo := Topology{Edges: []Edge{
		{From: "A", To: "B", Provenance: ProvInferredStatic, Confidence: 0.8, Signal: SignalDataFlow},
		{From: "B", To: "C", Provenance: ProvInferredStatic, Confidence: 0.8, Signal: SignalDataFlow},
	}}
	order, constrained := topo.Order([]string{"A", "B", "C"}, []string{"C", "A", "B"})
	if !constrained {
		t.Fatalf("topology should have constrained the order")
	}
	// A must precede B must precede C regardless of the scrambled tiebreak.
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["A"] >= pos["B"] || pos["B"] >= pos["C"] {
		t.Fatalf("order %v does not respect A→B→C", order)
	}
}

// Topology.Order: no edges → falls back to the tiebreak (flat trace order), not constrained.
func TestTopology_OrderFallsBackWhenNoEdges(t *testing.T) {
	order, constrained := Topology{}.Order([]string{"A", "B"}, []string{"B", "A"})
	if constrained {
		t.Fatalf("empty topology must not claim to constrain")
	}
	if len(order) != 2 || order[0] != "B" {
		t.Fatalf("fallback should be the tiebreak order; got %v", order)
	}
}

// A cycle (agent loop) terminates and returns a total order.
func TestTopology_OrderHandlesCycle(t *testing.T) {
	topo := Topology{Edges: []Edge{
		{From: "reflect", To: "reflect", Provenance: ProvInferredStatic, Confidence: 0.6, Signal: SignalSharedState},
		{From: "A", To: "reflect", Provenance: ProvInferredStatic, Confidence: 0.8, Signal: SignalCallGraph},
	}}
	order, _ := topo.Order([]string{"A", "reflect"}, []string{"A", "reflect"})
	if len(order) != 2 {
		t.Fatalf("cycle handling should still return all nodes; got %v", order)
	}
}
