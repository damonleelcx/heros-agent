package linkage

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// infer.go holds the recovery algorithms (task 13.1 static, 13.2 dynamic) and the reconciliation
// (13.7). Each is deterministic and reads only its inputs — the same signals yield the same edges
// every run, the property the whole provenance discipline rests on.

// ─────────────────────────────────────────────────────────────────────────────
// 13.1 — static recovery (call-graph + shared-state; the minimum-viable subset, design Q11)
// ─────────────────────────────────────────────────────────────────────────────

// CallSite is the structured per-call-site signal a P1 language frontend extracts. This package
// infers edges FROM it; extracting Callees / StateRefs from source (the deep inter-procedural
// analysis) is the P1 frontend's job and the staged uplift — the seam is here so the inference
// algorithm is real and fully tested regardless of how rich the extractor becomes.
type CallSite struct {
	NodeID string
	// EnclosingSymbol is the function/method the LLM call sits in (e.g. "_dispatch_nonstreaming_api_request").
	EnclosingSymbol string
	// Callees are the function/method names called within the enclosing function (the local call graph).
	Callees []string
	// StateRefs are the shared conversation/memory identifiers this call site reads or writes
	// (e.g. "self._session_messages", "messages"). Two call sites sharing one are linked through it.
	StateRefs []string
	// Order is the call site's source/lexical order, used to direct a shared-state edge deterministically.
	Order int
}

// InferStatic recovers inferred_static edges from the call sites' call-graph and shared-state signals.
//
//   - call-graph: if call site A's enclosing function calls call site B's enclosing function, A→B
//     (a dispatch forwarding to a primitive is exactly this shape). Confidence 0.8.
//   - shared-state: if two call sites reference the same conversation/memory identifier, they are
//     linked through it; the edge is directed by source order (earlier → later), since that is the
//     order the shared object is threaded. Confidence 0.6 (weaker than an explicit call-graph edge).
//
// Deterministic: sites are processed in a stable order and duplicate edges collapse to the highest
// confidence.
func InferStatic(sites []CallSite) []Edge {
	byEnclosing := map[string][]CallSite{}
	for _, s := range sites {
		byEnclosing[s.EnclosingSymbol] = append(byEnclosing[s.EnclosingSymbol], s)
	}
	acc := newEdgeAcc()

	sorted := append([]CallSite(nil), sites...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Order != sorted[j].Order {
			return sorted[i].Order < sorted[j].Order
		}
		return sorted[i].NodeID < sorted[j].NodeID
	})

	// call-graph edges
	for _, a := range sorted {
		for _, callee := range a.Callees {
			for _, b := range byEnclosing[callee] {
				if a.NodeID == b.NodeID {
					continue
				}
				acc.add(Edge{From: a.NodeID, To: b.NodeID, Provenance: ProvInferredStatic, Confidence: 0.8, Signal: SignalCallGraph})
			}
		}
	}

	// shared-state edges (directed by source order)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sharesAny(sorted[i].StateRefs, sorted[j].StateRefs) && sorted[i].NodeID != sorted[j].NodeID {
				acc.add(Edge{From: sorted[i].NodeID, To: sorted[j].NodeID, Provenance: ProvInferredStatic, Confidence: 0.6, Signal: SignalSharedState})
			}
		}
	}
	return acc.edges()
}

// ─────────────────────────────────────────────────────────────────────────────
// 13.2 — dynamic recovery (span parent-child, shared thread id, temporal)
// ─────────────────────────────────────────────────────────────────────────────

// AttrConversationID is the span attribute grouping the spans of one logical agent chain. Reused by
// name so the deriver reads the same key P2.5 would write.
const AttrConversationID = "conversation_id"

// InferDynamic recovers inferred_dynamic edges from a run's spans:
//
//   - span parent-child: a node span whose parent is another node span → parent→child (confidence 0.9,
//     the strongest dynamic signal — it is the caller→callee structure observed at run time).
//   - shared conversation/thread id: node spans carrying the same conversation id are one chain, linked
//     in start-time order (confidence 0.7).
//   - temporal: consecutive node executions by start-time (confidence 0.4, the weakest — this is the
//     flat order the attribution engine would otherwise use, promoted to an explicit low-confidence
//     edge only so it is visible as such).
func InferDynamic(spans []telemetry.Span) []Edge {
	acc := newEdgeAcc()

	// Index node spans by span id → node id, and collect node spans in start order.
	nodeOfSpan := map[string]string{}
	type ns struct {
		nodeID string
		spanID string
		parent string
		conv   string
		start  int64
	}
	var nodes []ns
	for _, sp := range spans {
		if sp.Kind != telemetry.SpanKindNode {
			continue
		}
		nid := attrStr(sp.Attributes, telemetry.AttrNodeID)
		if nid == "" {
			nid = sp.Name
		}
		nodeOfSpan[sp.SpanID] = nid
		nodes = append(nodes, ns{nid, sp.SpanID, sp.ParentSpanID, attrStr(sp.Attributes, AttrConversationID), sp.StartTime.UnixNano()})
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].start != nodes[j].start {
			return nodes[i].start < nodes[j].start
		}
		return nodes[i].nodeID < nodes[j].nodeID
	})

	// parent-child
	for _, n := range nodes {
		if parentNode, ok := nodeOfSpan[n.parent]; ok && parentNode != n.nodeID {
			acc.add(Edge{From: parentNode, To: n.nodeID, Provenance: ProvInferredDynamic, Confidence: 0.9, Signal: SignalSpanParent})
		}
	}

	// shared conversation id → chain in start order
	byConv := map[string][]ns{}
	for _, n := range nodes {
		if n.conv != "" {
			byConv[n.conv] = append(byConv[n.conv], n)
		}
	}
	for _, chain := range byConv {
		for i := 0; i+1 < len(chain); i++ {
			if chain[i].nodeID != chain[i+1].nodeID {
				acc.add(Edge{From: chain[i].nodeID, To: chain[i+1].nodeID, Provenance: ProvInferredDynamic, Confidence: 0.7, Signal: SignalSharedThread})
			}
		}
	}

	// temporal (consecutive distinct nodes)
	for i := 0; i+1 < len(nodes); i++ {
		if nodes[i].nodeID != nodes[i+1].nodeID {
			acc.add(Edge{From: nodes[i].nodeID, To: nodes[i+1].nodeID, Provenance: ProvInferredDynamic, Confidence: 0.4, Signal: SignalTemporal})
		}
	}
	return acc.edges()
}

// ─────────────────────────────────────────────────────────────────────────────
// 13.7 — reconciliation + precedence
// ─────────────────────────────────────────────────────────────────────────────

// Reconcile merges edge sets into one Topology, applying the precedence + confirmation rules (design
// Q10): for a given (from,to), the HIGHEST-provenance edge wins; a dynamically-observed edge that
// confirms a statically-inferred one RAISES the static edge's confidence (capped at 0.95); a
// statically-inferred edge never observed dynamically is kept but capped low (0.5) so "inferred but
// unconfirmed" is visibly weaker than "inferred and observed".
func Reconcile(sets ...[]Edge) Topology {
	// Group all edges by (from,to).
	type key struct{ from, to string }
	grouped := map[key][]Edge{}
	var order []key
	for _, set := range sets {
		for _, e := range set {
			k := key{e.From, e.To}
			if _, seen := grouped[k]; !seen {
				order = append(order, k)
			}
			grouped[k] = append(grouped[k], e)
		}
	}

	var out []Edge
	for _, k := range order {
		group := grouped[k]
		// Winner = highest provenance rank, then highest confidence.
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Provenance.Rank() != group[j].Provenance.Rank() {
				return group[i].Provenance.Rank() > group[j].Provenance.Rank()
			}
			return group[i].Confidence > group[j].Confidence
		})
		winner := group[0]

		// Static/dynamic confirmation reconciliation: was the winning static edge also observed at
		// runtime (a dynamic edge on the same from,to)?
		hasDynamic := false
		for _, e := range group {
			if e.Provenance == ProvInferredDynamic {
				hasDynamic = true
			}
		}
		if winner.Provenance == ProvInferredStatic {
			if hasDynamic {
				// confirmed by observation → raise confidence
				winner.Confidence = min(0.95, winner.Confidence+0.2)
			} else {
				// inferred but never observed at runtime → keep but cap low
				winner.Confidence = min(winner.Confidence, 0.5)
			}
		}
		out = append(out, winner)
	}
	// Stable final order for determinism.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return Topology{Edges: out}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

type edgeAcc struct {
	best  map[string]Edge
	order []string
}

func newEdgeAcc() *edgeAcc { return &edgeAcc{best: map[string]Edge{}} }

func (a *edgeAcc) add(e Edge) {
	k := e.From + "\x00" + e.To + "\x00" + string(e.Signal)
	if prev, ok := a.best[k]; !ok || e.Confidence > prev.Confidence {
		if !ok {
			a.order = append(a.order, k)
		}
		a.best[k] = e
	}
}

func (a *edgeAcc) edges() []Edge {
	out := make([]Edge, 0, len(a.order))
	for _, k := range a.order {
		out = append(out, a.best[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].To != out[j].To {
			return out[i].To < out[j].To
		}
		return out[i].Signal < out[j].Signal
	})
	return out
}

func sharesAny(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if set[y] {
			return true
		}
	}
	return false
}

func attrStr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	if s, ok := attrs[key].(string); ok {
		return s
	}
	return ""
}
