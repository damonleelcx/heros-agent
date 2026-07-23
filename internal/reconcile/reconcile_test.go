package reconcile

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
)

func node(id string) discovery.IRNode {
	return discovery.IRNode{NodeID: id, Kind: "static_definition",
		IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}}
}

func call(nodeID string, idx int) dynamictracing.TracedCall {
	return dynamictracing.TracedCall{
		Tags:            dynamictracing.Tags{RunID: "run1", ConfigHash: "cfg1", Seed: 5, NodeID: nodeID},
		InvocationIndex: idx, Provider: "anthropic", ModelID: "m"}
}

// TASK 5.5: the conditional-router fixture flags the branch edge runtime-only and adds it additively;
// the unexercised static branch stays as an unconfirmed candidate, not deleted.
func TestReconcile_ConditionalRouterRuntimeOnlyEdge(t *testing.T) {
	// Static: router with a resolved edge router→branchA. branchB exists but static analysis could not
	// resolve router→branchB.
	ir := &discovery.IR{
		IRVersion: discovery.IRVersion,
		Nodes:     []discovery.IRNode{node("router"), node("branchA"), node("branchB")},
		Edges:     []discovery.IREdge{{FromNodeID: "router", ToNodeID: "branchA", Kind: "data"}},
	}
	// Runtime: the router activated branchB.
	trace := []dynamictracing.TracedCall{call("router", 0), call("branchB", 0)}

	rep := Reconcile(ir, trace)

	// router and branchB confirmed; branchA unconfirmed (not deleted).
	status := map[string]NodeStatus{}
	for _, n := range rep.Nodes {
		status[n.NodeID] = n.Status
	}
	if status["router"] != StatusConfirmed || status["branchB"] != StatusConfirmed {
		t.Fatalf("router and branchB must be confirmed: %+v", status)
	}
	if status["branchA"] != StatusUnconfirmed {
		t.Fatalf("the unexercised branchA must be unconfirmed, not deleted: %v", status["branchA"])
	}
	// router→branchB is a runtime-only edge.
	ro := rep.RuntimeOnlyEdges()
	if len(ro) != 1 || ro[0].FromNodeID != "router" || ro[0].ToNodeID != "branchB" {
		t.Fatalf("router→branchB must be flagged runtime_only, got %+v", ro)
	}

	// Reconciled additively into the IR: the runtime-only edge is added, marked observed-at-runtime,
	// and branchA's static edge survives.
	enriched := ReconcileIntoIR(ir, rep)
	var haveRuntimeEdge, haveStaticEdge bool
	for _, e := range enriched.Edges {
		if e.FromNodeID == "router" && e.ToNodeID == "branchB" && e.Provenance == "runtime_only" {
			haveRuntimeEdge = true
		}
		if e.FromNodeID == "router" && e.ToNodeID == "branchA" {
			haveStaticEdge = true
		}
	}
	if !haveRuntimeEdge {
		t.Fatalf("enriched IR must carry router→branchB as runtime_only, got %+v", enriched.Edges)
	}
	if !haveStaticEdge {
		t.Fatal("the static router→branchA edge must survive (additive, not destructive)")
	}
	// Same MAJOR: a pre-P5 consumer still parses it.
	if enriched.IRVersion[0] != discovery.IRVersion[0] {
		t.Fatalf("write-back must stay at the same ir_version MAJOR, got %s", enriched.IRVersion)
	}
}

// TASK 5.3 / 5.5: a self-looping agent is ONE definition and MANY invocations.
func TestReconcile_LoopOneDefinitionManyInvocations(t *testing.T) {
	ir := &discovery.IR{
		IRVersion: discovery.IRVersion,
		Nodes:     []discovery.IRNode{node("gen")},
		Edges:     []discovery.IREdge{{FromNodeID: "gen", ToNodeID: "gen", Kind: "data"}}, // static self-edge
	}
	var trace []dynamictracing.TracedCall
	for k := 0; k < 7; k++ {
		trace = append(trace, call("gen", k))
	}

	rep := Reconcile(ir, trace)

	// Exactly one node definition.
	genNodes := 0
	for _, n := range rep.Nodes {
		if n.NodeID == "gen" {
			genNodes++
			if n.InvocationCount != 7 {
				t.Fatalf("gen must have 7 invocations, got %d", n.InvocationCount)
			}
		}
	}
	if genNodes != 1 {
		t.Fatalf("a loop must be ONE definition, got %d", genNodes)
	}
	// Seven invocation indices 0..6.
	idx := rep.Invocations["gen"]
	if len(idx) != 7 {
		t.Fatalf("want 7 invocation indices, got %d", len(idx))
	}
	for k := 0; k < 7; k++ {
		if idx[k] != k {
			t.Fatalf("invocation index %d = %d, want %d", k, idx[k], k)
		}
	}
	// The self-edge was static, so it is NOT runtime-only.
	if len(rep.RuntimeOnlyEdges()) != 0 {
		t.Fatalf("a statically-known self-edge must not be runtime_only, got %+v", rep.RuntimeOnlyEdges())
	}
}

// TASK 5.4: reproducibility — the same traced run reconciles to identical verdicts and a stable hash.
func TestReconcile_Reproducible(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{node("a"), node("b"), node("c")},
		Edges: []discovery.IREdge{{FromNodeID: "a", ToNodeID: "b", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{call("a", 0), call("b", 0), call("c", 0)}

	first := Reconcile(ir, trace)
	if first.ContentHash == "" {
		t.Fatal("report must be content-addressed")
	}
	for i := 0; i < 10; i++ {
		got := Reconcile(ir, trace)
		if got.ContentHash != first.ContentHash {
			t.Fatalf("reconciliation is not reproducible: %s != %s", got.ContentHash, first.ContentHash)
		}
	}
}

// A runtime-only NODE (observed, no static candidate) is surfaced and confirmed-by-observation.
func TestReconcile_RuntimeOnlyNode(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{node("a")}}
	trace := []dynamictracing.TracedCall{call("a", 0), call("wrapper_dispatched", 0)}
	rep := Reconcile(ir, trace)

	var found bool
	for _, c := range rep.Calls {
		if c.NodeID == "wrapper_dispatched" && c.Status == StatusRuntimeOnly {
			found = true
		}
	}
	if !found {
		t.Fatalf("a call with no static candidate must be runtime_only, got %+v", rep.Calls)
	}
}
