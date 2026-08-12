// Package linkage recovers an agent's EFFECTIVE topology from multiple linkage signals, so a
// hand-rolled agent (no LangGraph/CrewAI graph) is not treated as edge-less just because framework
// detection found nothing. The observed motivation is concrete: on a real hand-rolled repo, framework
// detection returned 0 edges while the code plainly linked its LLM calls — a dispatch→create call
// graph, a `messages.append` data-flow chain, and a shared `_session_messages` conversation object.
//
// The design (P4.5 Decision 8) ranks edges by PROVENANCE — framework > inferred_static > inferred_
// dynamic > (flat trace order, which lives in the attribution engine as the last-resort fallback).
// Every recovered edge is a HYPOTHESIS carrying its provenance + confidence and the signal it came
// from; a consumer (P4.5 first-divergence / ablation scoping) prefers the higher-provenance edge set
// and never renders an inferred edge as a framework-certain one — ablation is what upgrades an
// inferred-edge localization to a measured cause.
//
// SCOPE (what is built): this package holds the inference ALGORITHM and reconciliation (fully tested),
// a REAL tree-sitter source extractor (pyextract.go — recovers the call-graph + shared-state signals
// from actual Python, proven on real hermes source), and the IR write path (ToIREdges) that persists
// recovered edges into the Workflow IR with provenance (IR 1.2.0, the schema having been evolved
// additively). The minimum-viable static subset — call-graph + shared-state — is what the extractor
// recovers today; a full inter-procedural data-flow graph is the later fidelity uplift (design Q11).
// P4.5 consumes the recovered Topology (from the IR's provenance-tagged edges via FromIR, plus dynamic
// edges the IR cannot carry from traces via InferDynamic).
package linkage

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// Provenance is where an edge came from, and — because the values are ranked — how much it is trusted.
type Provenance string

const (
	// ProvFramework: read from a declarative framework graph (LangGraph/CrewAI). Exact when present.
	ProvFramework Provenance = "framework"
	// ProvInferredStatic: inferred from the source (call-graph / data-flow / shared-state). A strong
	// hypothesis, but a hypothesis.
	ProvInferredStatic Provenance = "inferred_static"
	// ProvInferredDynamic: inferred from the run traces (span parent-child / shared-thread / temporal).
	ProvInferredDynamic Provenance = "inferred_dynamic"
)

// Rank is the trust order: higher wins when two edges disagree, and the highest rank present is the
// edge set attribution orders by. Framework(3) > static(2) > dynamic(1) > unknown(0).
func (p Provenance) Rank() int {
	switch p {
	case ProvFramework:
		return 3
	case ProvInferredStatic:
		return 2
	case ProvInferredDynamic:
		return 1
	default:
		return 0
	}
}

// Signal names the specific evidence an edge was recovered from — kept so a scorecard can say WHY an
// edge exists ("shared conversation state") rather than only its provenance tier.
type Signal string

const (
	SignalFramework    Signal = "framework"
	SignalCallGraph    Signal = "call_graph"    // fn holding call A calls the fn holding call B
	SignalDataFlow     Signal = "data_flow"     // A's response feeds B's prompt
	SignalSharedState  Signal = "shared_state"  // A and B read/write the same conversation/memory object
	SignalSpanParent   Signal = "span_parent"   // B's span is a child of A's span
	SignalSharedThread Signal = "shared_thread" // A and B carry the same conversation/thread id
	SignalTemporal     Signal = "temporal"      // A ran immediately before B (weakest)
)

// Edge is one recovered directed link between two node ids, with its provenance, confidence, and the
// signal it came from. It is never asserted as certain: confidence rides on every edge.
type Edge struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Provenance Provenance `json:"provenance"`
	Confidence float64    `json:"confidence"`
	Signal     Signal     `json:"signal"`
}

// Topology is the reconciled recovered edge set for one agent.
type Topology struct {
	Edges []Edge `json:"edges"`
}

// HighestProvenance returns the strongest provenance present, "" for an empty topology.
func (t Topology) HighestProvenance() Provenance {
	best := Provenance("")
	for _, e := range t.Edges {
		if e.Provenance.Rank() > best.Rank() {
			best = e.Provenance
		}
	}
	return best
}

// edgesAtLeast returns the edges whose provenance rank is >= the given provenance — the "highest-
// provenance edge set available" attribution orders by.
func (t Topology) edgesAtLeast(p Provenance) []Edge {
	var out []Edge
	for _, e := range t.Edges {
		if e.Provenance.Rank() >= p.Rank() {
			out = append(out, e)
		}
	}
	return out
}

// Order returns a stable order over exactly `executed` that respects the recovered topology: a node
// is placed after every executed predecessor the (highest-provenance) edge set links to it. Nodes not
// constrained by any edge — and cycles (an agent loop is a cycle) — are broken by `tiebreak`, the
// caller's fallback order (in P4.5, span start-time). This is what makes first-divergence a claim
// about the agent's ACTUAL order rather than wall-clock coincidence, while still terminating on the
// loops real agents are full of.
//
// Returns (order, true) when the topology constrained the order; (tiebreak, false) when no edge
// touched the executed set (the flat-trace-order fallback case).
func (t Topology) Order(executed []string, tiebreak []string) ([]string, bool) {
	if len(t.Edges) == 0 {
		return tiebreak, false
	}
	set := map[string]bool{}
	for _, n := range executed {
		set[n] = true
	}
	// Restrict to the highest-provenance edges among executed nodes.
	top := t.HighestProvenance()
	indeg := map[string]int{}
	adj := map[string][]string{}
	seenEdge := map[string]bool{}
	touched := false
	for _, e := range t.edgesAtLeast(top) {
		if !set[e.From] || !set[e.To] || e.From == e.To {
			continue
		}
		key := e.From + "\x00" + e.To
		if seenEdge[key] {
			continue
		}
		seenEdge[key] = true
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
		touched = true
	}
	if !touched {
		return tiebreak, false
	}

	// Deterministic tiebreak position for Kahn's algorithm: earlier in `tiebreak` = earlier.
	pos := map[string]int{}
	for i, n := range tiebreak {
		pos[n] = i
	}
	rank := func(n string) int {
		if p, ok := pos[n]; ok {
			return p
		}
		return len(tiebreak) + len(n) // deterministic for nodes absent from tiebreak
	}

	// Kahn's topological sort, preferring the tiebreak order among ready nodes.
	var ready []string
	for _, n := range executed {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	sortByRank := func(s []string) {
		sort.SliceStable(s, func(i, j int) bool {
			if rank(s[i]) != rank(s[j]) {
				return rank(s[i]) < rank(s[j])
			}
			return s[i] < s[j]
		})
	}
	sortByRank(ready)

	var out []string
	placed := map[string]bool{}
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		if placed[n] {
			continue
		}
		placed[n] = true
		out = append(out, n)
		var newly []string
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 && !placed[m] {
				newly = append(newly, m)
			}
		}
		sortByRank(newly)
		ready = append(ready, newly...)
		sortByRank(ready)
	}
	// A cycle (agent loop) leaves nodes unplaced: append them in tiebreak order so the order is total
	// and deterministic rather than dropping nodes.
	if len(out) < len(executed) {
		var rest []string
		for _, n := range executed {
			if !placed[n] {
				rest = append(rest, n)
			}
		}
		sortByRank(rest)
		out = append(out, rest...)
	}
	return out, true
}

// FromIR lifts the edges the P1 IR carries into recovered edges, reading each edge's PROVENANCE from
// the IR (IR 1.2.0) when present. An edge with no recorded provenance is treated as framework-strength
// — the strongest tier — because a pre-1.2.0 IR only carried edges a framework graph or the P1
// call-graph produced, and treating them as strong preserves the prior behavior. An IR that DID record
// provenance (P1 emitted inferred_static edges into it) flows through with its real provenance +
// confidence, so P4.5 consumes exactly what P1 recovered. Additional inferred edges (e.g. dynamic,
// from traces the IR cannot carry) are layered on via InferStatic / InferDynamic + Reconcile.
func FromIR(ir *discovery.IR) []Edge {
	if ir == nil {
		return nil
	}
	out := make([]Edge, 0, len(ir.Edges))
	for _, e := range ir.Edges {
		prov := Provenance(e.Provenance)
		conf := e.Confidence
		sig := Signal(e.Signal)
		if e.Provenance == "" { // unrecorded provenance → framework-strength (pre-1.2.0 contract)
			prov, conf, sig = ProvFramework, 1.0, SignalFramework
		} else if e.Confidence == 0 {
			conf = 1.0
		}
		out = append(out, Edge{From: e.FromNodeID, To: e.ToNodeID, Provenance: prov, Confidence: conf, Signal: sig})
	}
	return out
}

// ToIREdges converts recovered edges into IR edges tagged with their provenance/confidence/signal, so
// P1 (or a recovery pass) can PERSIST them into the Workflow IR (IR 1.2.0). `kind` is derived from the
// signal: a control-flow-ish signal maps to "control", everything else to "data" (the producer→consumer
// default). This is the write path that makes recovered topology a durable part of the IR rather than a
// P4.5-only artifact.
func ToIREdges(edges []Edge) []discovery.IREdge {
	out := make([]discovery.IREdge, 0, len(edges))
	for _, e := range edges {
		kind := "data"
		if e.Signal == SignalSpanParent {
			kind = "control"
		}
		out = append(out, discovery.IREdge{
			FromNodeID: e.From, ToNodeID: e.To, Kind: kind,
			Provenance: string(e.Provenance), Confidence: e.Confidence, Signal: string(e.Signal),
			// P30 D4. `detector`: this package's own inference rules established it. Its `Provenance`
			// says how strong the evidence is; this says who wrote it, and the two are orthogonal.
			Author: string(discovery.AuthorDetector),
		})
	}
	return out
}
