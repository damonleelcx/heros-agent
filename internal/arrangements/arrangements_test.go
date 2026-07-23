package arrangements

import (
	"reflect"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

func node(id string, in, out map[string]any) discovery.IRNode {
	return discovery.IRNode{NodeID: id, Kind: "static_definition",
		IOContract: discovery.IRIOContract{InputSchema: in, OutputSchema: out}}
}

func obj(props map[string]any, req ...string) map[string]any {
	r := make([]any, len(req))
	for i, x := range req {
		r[i] = x
	}
	m := map[string]any{"type": "object", "properties": props}
	if len(r) > 0 {
		m["required"] = r
	}
	return m
}

// A linear pipeline A→B→C: exactly the arrangements whose topological order is valid are approved; the
// rest are rejected. The approved ones rank above the rejected ones, and the ranking is deterministic.
func TestEnumerate_PipelineApprovedOnTop(t *testing.T) {
	str := map[string]any{"type": "string"}
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{
		node("A", map[string]any{"type": "object"}, obj(map[string]any{"x": str})),
		node("B", obj(map[string]any{"x": str}, "x"), obj(map[string]any{"y": str})),
		node("C", obj(map[string]any{"y": str}, "y"), map[string]any{"type": "object"}),
	}}
	edges := []typedcontract.Edge{
		{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
		{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
	}
	rank := Enumerate(ir, []string{"A", "B", "C"}, edges, nil, 0)

	if rank.Total != 6 || rank.Considered != 6 || rank.Truncated {
		t.Fatalf("3 nodes → 6 orderings, all considered: %+v", rank)
	}
	// Only A,B,C (the topological order) is approved.
	if rank.ApprovedCount != 1 {
		t.Fatalf("only the topological order should be approved, got %d", rank.ApprovedCount)
	}
	top := rank.Arrangements[0]
	if !top.Approved || !reflect.DeepEqual(top.Order, []string{"A", "B", "C"}) {
		t.Fatalf("the approved order must rank first, got %+v", top)
	}
	// Everything below the first is rejected, and each approved precedes each rejected.
	seenRejected := false
	for _, a := range rank.Arrangements {
		if !a.Approved {
			seenRejected = true
		} else if seenRejected {
			t.Fatalf("an approved arrangement appeared below a rejected one: %+v", a)
		}
	}
	// Rejected arrangements are ordered by score descending (fewer broken edges first).
	var lastScore = 2.0
	for _, a := range rank.Arrangements {
		if a.Score > lastScore+1e-9 {
			t.Fatalf("arrangements not sorted by score descending: %v after %v", a.Score, lastScore)
		}
		lastScore = a.Score
	}
}

// An adapter-augmented arrangement is approved (ranks with coherent, above rejected) but scores below a
// fully-coherent one.
func TestEnumerate_AdaptedIsApprovedBelowCoherent(t *testing.T) {
	str := map[string]any{"type": "string"}
	// A outputs `answer`, B requires `response` → rename adapter. Coherent-via-adapter when A precedes B.
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{
		node("A", map[string]any{"type": "object"}, obj(map[string]any{"answer": str})),
		node("B", obj(map[string]any{"response": str}, "response"), map[string]any{"type": "object"}),
	}}
	edges := []typedcontract.Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}}
	rank := Enumerate(ir, []string{"A", "B"}, edges, nil, 0)
	if rank.ApprovedCount != 1 {
		t.Fatalf("A→B is adaptable (approved); B→A is rejected: %+v", rank)
	}
	top := rank.Arrangements[0]
	if top.Kind != typedcontract.VerdictAdapted || top.AdapterCount != 1 {
		t.Fatalf("top arrangement must be adapted with 1 adapter, got %+v", top)
	}
	if top.Score >= 1.0 {
		t.Fatalf("an adapted arrangement must score below a coherent 1.0, got %v", top.Score)
	}
}

// Enumeration is deterministic.
func TestEnumerate_Deterministic(t *testing.T) {
	str := map[string]any{"type": "string"}
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{
		node("A", map[string]any{"type": "object"}, obj(map[string]any{"x": str})),
		node("B", obj(map[string]any{"x": str}, "x"), obj(map[string]any{"y": str})),
		node("C", obj(map[string]any{"y": str}, "y"), map[string]any{"type": "object"}),
	}}
	edges := []typedcontract.Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}, {FromNodeID: "B", ToNodeID: "C", Kind: "data"}}
	first := Enumerate(ir, []string{"A", "B", "C"}, edges, nil, 0)
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(Enumerate(ir, []string{"A", "B", "C"}, edges, nil, 0), first) {
			t.Fatal("enumeration is not deterministic")
		}
	}
}

// The cap is enforced AND surfaced (no silent truncation).
func TestEnumerate_CapSurfaced(t *testing.T) {
	var nodes []discovery.IRNode
	var order []string
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		nodes = append(nodes, node(id, map[string]any{"type": "object"}, map[string]any{"type": "object"}))
		order = append(order, id)
	}
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: nodes}
	rank := Enumerate(ir, order, nil, nil, 10) // 5! = 120, cap 10
	if rank.Total != 120 {
		t.Fatalf("total must report 120, got %d", rank.Total)
	}
	if rank.Considered != 10 || !rank.Truncated {
		t.Fatalf("cap must be enforced and surfaced: considered=%d truncated=%v", rank.Considered, rank.Truncated)
	}
}
