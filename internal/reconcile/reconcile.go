// Package reconcile implements P5's reconciler (Decision 4): it matches the calls observed on a real,
// instrumented run against the STATIC candidate graph Discovery produced, and resolves runtime-dynamic
// dispatch (loops, conditional routing, wrapper dispatch) concretely.
//
// The premise correction from the source plan: "how many nodes make LLM requests" is well-defined only
// for static call sites; an agent with a loop makes a VARIABLE number of runtime requests. The IR
// distinguishes a static DEFINITION from its runtime INVOCATIONS (P0) precisely so tracing can confirm
// the static graph and resolve dynamic dispatch — a loop is one definition with n invocations, never n
// definitions (task 5.3).
//
// # Additive, never destructive
//
// A static candidate the traced run did not exercise is marked UNCONFIRMED, not deleted — a path this
// input did not take is not a dead node. A runtime-only edge/node static analysis missed is ADDED to the
// IR additively (same ir_version MAJOR), marked observed-at-runtime. This is the P0 additive-evolution
// rule (Decision 7): a pre-P5 consumer still parses the enriched IR.
//
// # Reproducible and content-addressed
//
// Reconcile is a pure, deterministic function of (IR, ordered trace): the same {config_hash, seed}
// traced run reconciles to the same confirmed/unconfirmed/runtime-only verdicts, and the report carries
// a content hash so it is attributable to the exact run (task 5.4).
package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
)

// NodeStatus is a static candidate's verdict against the traced run.
type NodeStatus string

const (
	// StatusConfirmed: the static node was observed at runtime.
	StatusConfirmed NodeStatus = "confirmed"
	// StatusUnconfirmed: the static node was NOT observed on this run (not deleted — additive).
	StatusUnconfirmed NodeStatus = "unconfirmed"
)

// CallStatus is an observed call's verdict against the static graph.
type CallStatus string

const (
	// StatusMatched: the observed call maps to a static candidate node.
	StatusMatched CallStatus = "matched"
	// StatusRuntimeOnly: the observed call has no static candidate (dispatch static analysis missed).
	StatusRuntimeOnly CallStatus = "runtime_only"
)

// EdgeOrigin distinguishes a statically-known edge from one only the trace revealed.
type EdgeOrigin string

const (
	OriginStatic      EdgeOrigin = "static"
	OriginRuntimeOnly EdgeOrigin = "runtime_only"
)

// ReconciledNode is one static definition's verdict plus how many times it ran.
type ReconciledNode struct {
	NodeID          string     `json:"node_id"`
	Status          NodeStatus `json:"status"`
	InvocationCount int        `json:"invocation_count"`
}

// ReconciledCall is one observed runtime invocation mapped (or not) to a static definition.
type ReconciledCall struct {
	NodeID          string     `json:"node_id"`
	InvocationIndex int        `json:"invocation_index"`
	Status          CallStatus `json:"status"`
}

// ReconciledEdge is one edge in the reconciled graph, tagged with its origin.
type ReconciledEdge struct {
	FromNodeID string     `json:"from_node_id"`
	ToNodeID   string     `json:"to_node_id"`
	Origin     EdgeOrigin `json:"origin"`
}

// Report is the reconciliation verdict. Content-addressed via ContentHash (task 5.4).
type Report struct {
	RunID      string `json:"run_id"`
	ConfigHash string `json:"config_hash"`
	Seed       int64  `json:"seed"`

	Nodes []ReconciledNode `json:"nodes"`
	Calls []ReconciledCall `json:"calls"`
	Edges []ReconciledEdge `json:"edges"`
	// Invocations maps one static definition to its runtime invocation indices (0..n−1). The heart of
	// the static-def↔invocation distinction: a loop is one key with n indices.
	Invocations map[string][]int `json:"invocations"`

	ContentHash string `json:"content_hash"`
}

// Reconcile matches the ordered trace against the static IR and returns the deterministic report.
//
// The trace MUST be in observation order: edge inference reads consecutive calls as the observed path,
// so the caller orders the trace by observation (the sink's sequence), not by hash. Given the same
// ordered trace over the same IR, the report — including its content hash — is identical.
func Reconcile(ir *discovery.IR, trace []dynamictracing.TracedCall) Report {
	rep := Report{Invocations: map[string][]int{}}
	if len(trace) > 0 {
		rep.RunID = trace[0].Tags.RunID
		rep.ConfigHash = trace[0].Tags.ConfigHash
		rep.Seed = trace[0].Tags.Seed
	}

	staticNodes := map[string]bool{}
	for _, n := range ir.Nodes {
		staticNodes[n.NodeID] = true
	}
	staticEdges := map[[2]string]bool{}
	for _, e := range ir.Edges {
		staticEdges[[2]string{e.FromNodeID, e.ToNodeID}] = true
	}

	// Per-node invocation indices and observed status.
	observed := map[string]bool{}
	for _, c := range trace {
		observed[c.Tags.NodeID] = true
		rep.Invocations[c.Tags.NodeID] = append(rep.Invocations[c.Tags.NodeID], c.InvocationIndex)
		status := StatusRuntimeOnly
		if staticNodes[c.Tags.NodeID] {
			status = StatusMatched
		}
		rep.Calls = append(rep.Calls, ReconciledCall{
			NodeID: c.Tags.NodeID, InvocationIndex: c.InvocationIndex, Status: status})
	}

	// Every static candidate: confirmed iff observed. Unobserved candidates are UNCONFIRMED, not deleted.
	for _, id := range sortedKeys(staticNodes) {
		rep.Nodes = append(rep.Nodes, ReconciledNode{
			NodeID: id, Status: nodeStatus(observed[id]), InvocationCount: len(rep.Invocations[id])})
	}
	// A runtime-only node (observed but no static candidate) also appears in the node list, so the
	// reconciled graph is complete — it is confirmed-by-observation but had no prior definition.
	for _, id := range sortedKeys(observed) {
		if !staticNodes[id] {
			rep.Nodes = append(rep.Nodes, ReconciledNode{
				NodeID: id, Status: StatusConfirmed, InvocationCount: len(rep.Invocations[id])})
		}
	}
	sort.Slice(rep.Nodes, func(i, j int) bool { return rep.Nodes[i].NodeID < rep.Nodes[j].NodeID })

	// Observed edges from consecutive calls. A self-invocation (loop-back) is a real observed edge too.
	seenEdge := map[[2]string]bool{}
	for i := 0; i+1 < len(trace); i++ {
		from, to := trace[i].Tags.NodeID, trace[i+1].Tags.NodeID
		key := [2]string{from, to}
		if seenEdge[key] {
			continue
		}
		seenEdge[key] = true
		origin := OriginRuntimeOnly
		if staticEdges[key] {
			origin = OriginStatic
		}
		rep.Edges = append(rep.Edges, ReconciledEdge{FromNodeID: from, ToNodeID: to, Origin: origin})
	}
	// Static edges the trace did not exercise stay in the graph as static (a path not taken this run).
	for _, e := range ir.Edges {
		key := [2]string{e.FromNodeID, e.ToNodeID}
		if !seenEdge[key] {
			seenEdge[key] = true
			rep.Edges = append(rep.Edges, ReconciledEdge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Origin: OriginStatic})
		}
	}
	sort.Slice(rep.Edges, func(i, j int) bool {
		if rep.Edges[i].FromNodeID != rep.Edges[j].FromNodeID {
			return rep.Edges[i].FromNodeID < rep.Edges[j].FromNodeID
		}
		return rep.Edges[i].ToNodeID < rep.Edges[j].ToNodeID
	})

	rep.ContentHash = hashReport(rep)
	return rep
}

// RuntimeOnlyEdges returns the edges the trace revealed that static analysis missed. Convenience for
// the eval-set targeter (§7) and the UI.
func (r Report) RuntimeOnlyEdges() []ReconciledEdge {
	var out []ReconciledEdge
	for _, e := range r.Edges {
		if e.Origin == OriginRuntimeOnly {
			out = append(out, e)
		}
	}
	return out
}

// ReconcileIntoIR returns a NEW IR with the reconciler's runtime-only edges added ADDITIVELY (task 5.2,
// Decision 7): same ir_version MAJOR, unconfirmed candidates untouched, runtime-only edges marked with
// Provenance="runtime_only" so a consumer can tell an observed edge from a statically-recovered one. It
// does not mutate the input IR.
func ReconcileIntoIR(ir *discovery.IR, report Report) *discovery.IR {
	out := *ir
	out.Nodes = append([]discovery.IRNode(nil), ir.Nodes...)
	out.Edges = append([]discovery.IREdge(nil), ir.Edges...)

	existing := map[[2]string]bool{}
	for _, e := range ir.Edges {
		existing[[2]string{e.FromNodeID, e.ToNodeID}] = true
	}
	// Bump the declared IR version to the edge-provenance minor (additive; a pre-P5 consumer pinned to
	// MAJOR 1 still parses it), but only if we actually add a runtime-only edge.
	added := false
	for _, e := range report.RuntimeOnlyEdges() {
		key := [2]string{e.FromNodeID, e.ToNodeID}
		if existing[key] {
			continue
		}
		existing[key] = true
		out.Edges = append(out.Edges, discovery.IREdge{
			FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: "data",
			Provenance: "runtime_only"})
		added = true
	}
	if added && out.IRVersion == discovery.IRVersion {
		out.IRVersion = discovery.IRVersionEdgeProvenance
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].FromNodeID != out.Edges[j].FromNodeID {
			return out.Edges[i].FromNodeID < out.Edges[j].FromNodeID
		}
		return out.Edges[i].ToNodeID < out.Edges[j].ToNodeID
	})
	return &out
}

func nodeStatus(observed bool) NodeStatus {
	if observed {
		return StatusConfirmed
	}
	return StatusUnconfirmed
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hashReport content-addresses the report over its verdict fields (not ContentHash itself). Deterministic
// because every slice is already sorted and Invocations is marshalled with sorted keys by encoding/json.
func hashReport(r Report) string {
	r.ContentHash = ""
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
