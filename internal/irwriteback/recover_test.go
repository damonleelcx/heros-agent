package irwriteback

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

func siteNode(id, file, symbol string, line int) discovery.IRNode {
	return discovery.IRNode{NodeID: id, Kind: "static_definition",
		CallSite:   discovery.IRCallSite{File: file, Symbol: symbol, LineStart: line},
		IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}}
}

// A non-graph agent — nodes, zero edges — has its topology recovered from shared enclosing state, with
// the edges carried as inferred hypotheses (provenance + confidence), never framework-certain.
func TestRecoverTopology_NonGraphAgent(t *testing.T) {
	// Three LLM calls in one function (a fallback chain), like hermes-agent's call_llm.
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Workflow: discovery.IRWorkflow{Language: "python"},
		Nodes: []discovery.IRNode{
			siteNode("a", "auxiliary_client.py", "call_llm", 10),
			siteNode("b", "auxiliary_client.py", "call_llm", 20),
			siteNode("c", "auxiliary_client.py", "call_llm", 30),
		}}
	if !IsNonGraph(ir) {
		t.Fatal("an edgeless multi-node IR must be detected as non-graph")
	}

	out, added := RecoverTopology(ir)
	if added == 0 {
		t.Fatal("topology must be recovered for co-located calls sharing state")
	}
	// Every recovered edge is an inferred hypothesis, never framework-certain.
	for _, e := range out.Edges {
		if e.Provenance == "" || e.Provenance == "framework" {
			t.Fatalf("recovered edge must be inferred, not framework-certain: %+v", e)
		}
		if e.Confidence <= 0 || e.Confidence > 1 {
			t.Fatalf("recovered edge must carry a confidence in (0,1]: %+v", e)
		}
	}
	// The chain is directed by source order (earlier → later), so a→b exists and b→a does not.
	var haveAB, haveBA bool
	for _, e := range out.Edges {
		if e.FromNodeID == "a" && e.ToNodeID == "b" {
			haveAB = true
		}
		if e.FromNodeID == "b" && e.ToNodeID == "a" {
			haveBA = true
		}
	}
	if !haveAB || haveBA {
		t.Fatalf("recovered edges must be directed by source order, got %+v", out.Edges)
	}
	// Additive: same MAJOR, input untouched.
	if out.IRVersion[0] != discovery.IRVersion[0] {
		t.Fatalf("recovery must stay at the same ir_version MAJOR, got %s", out.IRVersion)
	}
	if len(ir.Edges) != 0 {
		t.Fatal("recovery must not mutate the input IR")
	}
}

// A framework-graph agent (already has edges) is left alone — recovery is only for non-graph agents.
func TestRecoverTopology_FrameworkGraphUntouched(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{siteNode("a", "f.py", "s", 1), siteNode("b", "f.py", "s", 2)},
		Edges: []discovery.IREdge{{FromNodeID: "a", ToNodeID: "b", Kind: "data"}}}
	if IsNonGraph(ir) {
		t.Fatal("an IR with edges is not a non-graph agent")
	}
}
