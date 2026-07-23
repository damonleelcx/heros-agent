package irwriteback

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/linkage"
)

// RecoverTopology adapts P5 for a NON-GRAPH agent (task: "adapt for non-graph agent nodes"). Many real
// agents — hermes-agent's `call_llm` fallback chain, a ReAct loop, a script of independent LLM calls —
// carry NO declarative framework graph, so Discovery emits nodes with ZERO edges. A flat, edgeless list
// gives the re-arrangement validator nothing to check and hides the agent's real structure.
//
// This reuses the P4.5 recovered-topology machinery (internal/linkage): it infers edges from the call
// sites' shared enclosing function (calls threaded through the same conversation/messages object are
// linked, earlier→later) and writes them back into the IR as `inferred_static` edges CARRYING THEIR
// PROVENANCE + CONFIDENCE. The discipline is P4.5's: an inferred edge is a HYPOTHESIS, never rendered as
// framework-certain — the editor shows it as recovered, and the validator's verdict on it is qualified
// by its confidence.
//
// It is additive (task 8.2): it only ADDS edges (a node with framework edges keeps them), and it
// declares the edge-provenance MINOR so a pre-P5 consumer pinned to MAJOR 1 still parses the result.
func RecoverTopology(ir *discovery.IR) (*discovery.IR, int) {
	// Build linkage call sites from the IR. The enclosing symbol is the shared-state proxy: two LLM
	// calls in the same function thread the same conversation object, which is exactly linkage's
	// shared-state signal. Order is the source line, so the recovered chain follows lexical order.
	sites := make([]linkage.CallSite, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		enclosing := n.CallSite.File + "::" + n.CallSite.Symbol
		sites = append(sites, linkage.CallSite{
			NodeID:          n.NodeID,
			EnclosingSymbol: enclosing,
			StateRefs:       []string{enclosing}, // co-located calls share the conversation object
			Order:           n.CallSite.LineStart,
		})
	}

	recovered := linkage.Reconcile(linkage.InferStatic(sites))
	if len(recovered.Edges) == 0 {
		return ir, 0
	}

	out := *ir
	out.Edges = append([]discovery.IREdge(nil), ir.Edges...)
	existing := map[[2]string]bool{}
	for _, e := range ir.Edges {
		existing[[2]string{e.FromNodeID, e.ToNodeID}] = true
	}
	added := 0
	for _, e := range recovered.Edges {
		key := [2]string{e.From, e.To}
		if existing[key] {
			continue
		}
		existing[key] = true
		out.Edges = append(out.Edges, discovery.IREdge{
			FromNodeID: e.From, ToNodeID: e.To, Kind: "data",
			Provenance: string(e.Provenance), Confidence: e.Confidence, Signal: string(e.Signal),
		})
		added++
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].FromNodeID != out.Edges[j].FromNodeID {
			return out.Edges[i].FromNodeID < out.Edges[j].FromNodeID
		}
		return out.Edges[i].ToNodeID < out.Edges[j].ToNodeID
	})
	if added > 0 && sameOrEarlier(out.IRVersion, discovery.IRVersionEdgeProvenance) {
		out.IRVersion = discovery.IRVersionEdgeProvenance
	}
	return &out, added
}

// IsNonGraph reports whether an IR arrived with no declarative structure — nodes but zero edges — the
// signal that topology must be recovered before the re-arrangement editor can be useful.
func IsNonGraph(ir *discovery.IR) bool {
	return len(ir.Nodes) > 0 && len(ir.Edges) == 0
}
