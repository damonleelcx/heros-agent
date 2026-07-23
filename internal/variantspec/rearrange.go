package variantspec

import (
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

// This file is the bridge between the P5 ordering-coherence validator (internal/typedcontract) and the
// P2 configuration layer. It exists so that "re-arrangement produces a candidate Variant Spec that is
// validated BEFORE any codemod is generated" (task 1.7, ADR-001) is enforced in one place:
// GateReorder returns a runnable candidate ONLY on a coherent verdict, and returns nil (plus the
// verdict) on rejection, so a caller physically cannot hand a rejected ordering to the transform engine.

// Reorder derives a candidate Variant Spec from a parent by replacing its node order and edges. The
// candidate inherits the parent's workflow, source_revision, and per-node overrides unchanged — only
// the wiring moves — and records lineage via ParentVariantID (task 1.6). It does NOT validate: that is
// GateReorder's job, kept separate so the UI can build a candidate and validate it incrementally.
func Reorder(parent *VariantSpec, parentVariantID string, order []string, edges []Edge) *VariantSpec {
	nodes := make(map[string]NodeOverride, len(parent.Nodes))
	for id, o := range parent.Nodes {
		nodes[id] = o
	}
	return &VariantSpec{
		WorkflowID:      parent.WorkflowID,
		ParentVariantID: parentVariantID,
		SourceRevision:  parent.SourceRevision,
		Order:           append([]string(nil), order...),
		Nodes:           nodes,
		Edges:           append([]Edge(nil), edges...),
	}
}

// ordering projects a spec into the shape the validator consumes.
func ordering(s *VariantSpec) typedcontract.Ordering {
	edges := make([]typedcontract.Edge, len(s.Edges))
	for i, e := range s.Edges {
		edges[i] = typedcontract.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind}
	}
	return typedcontract.Ordering{Order: s.Order, Edges: edges}
}

// GateReorder validates a candidate ordering through the typed-contract check and, ONLY on a coherent
// or adapted verdict, returns a runnable candidate spec (task 1.7 — validation gates transform
// generation). On rejection it returns (nil, verdict): no runnable spec exists, so nothing is handed to
// the transform engine and no codemod, diff, or PR is generated (the "no silently-broken reorder"
// guarantee).
//
// When the verdict is `adapted`, the inserted adapters are recorded on the returned candidate as
// explicit InsertedAdapter entries and the candidate's edges are rewired through the adapter nodes, so
// the spec that leaves this function is the one the transform engine will materialise — the adapter is
// never a hidden coercion.
func GateReorder(ir *discovery.IR, candidate *VariantSpec, catalog *typedcontract.Catalog) (*VariantSpec, typedcontract.Verdict) {
	verdict := typedcontract.ValidateOrdering(ir, ordering(candidate), catalog)
	if !verdict.IsCoherent() {
		return nil, verdict // rejected: no runnable spec, no transform
	}
	if verdict.Kind == typedcontract.VerdictAdapted {
		candidate = withAdapters(candidate, verdict.Adapters)
	}
	return candidate, verdict
}

// withAdapters returns a copy of the candidate with each inserted adapter recorded and its edge rewired
// producer→adapter→consumer. The adapter node id is deterministic (derived from the edge + kind) so the
// same reorder yields the same spec and the same config_hash on every evaluation.
func withAdapters(candidate *VariantSpec, insertions []typedcontract.AdapterInsertion) *VariantSpec {
	out := *candidate
	out.Order = append([]string(nil), candidate.Order...)
	out.Edges = append([]Edge(nil), candidate.Edges...)
	out.InsertedAdapters = nil

	for _, ins := range insertions {
		adapterID := adapterNodeID(ins.FromNodeID, ins.ToNodeID, string(ins.Adapter.Kind))
		out.InsertedAdapters = append(out.InsertedAdapters, InsertedAdapter{
			AdapterNodeID: adapterID,
			FromNodeID:    ins.FromNodeID,
			ToNodeID:      ins.ToNodeID,
			CatalogKind:   string(ins.Adapter.Kind),
			Params:        ins.Adapter.Params,
			InSchema:      ins.Adapter.InSchema,
			OutSchema:     ins.Adapter.OutSchema,
		})
		// Rewire: from→to becomes from→adapter and adapter→to. The adapter node is inserted right after
		// its producer in the order so it always precedes the consumer.
		out.Edges = rewireThroughAdapter(out.Edges, ins.FromNodeID, ins.ToNodeID, adapterID)
		out.Order = insertAfter(out.Order, ins.FromNodeID, adapterID)
	}
	return &out
}

func adapterNodeID(from, to, kind string) string {
	return "adapter:" + kind + ":" + from + "->" + to
}

func rewireThroughAdapter(edges []Edge, from, to, adapter string) []Edge {
	out := make([]Edge, 0, len(edges)+1)
	for _, e := range edges {
		if e.FromNodeID == from && e.ToNodeID == to && e.Kind == "data" {
			out = append(out, Edge{FromNodeID: from, ToNodeID: adapter, Kind: "data"})
			out = append(out, Edge{FromNodeID: adapter, ToNodeID: to, Kind: "data"})
			continue
		}
		out = append(out, e)
	}
	return out
}

func insertAfter(order []string, anchor, id string) []string {
	out := make([]string, 0, len(order)+1)
	for _, n := range order {
		out = append(out, n)
		if n == anchor {
			out = append(out, id)
		}
	}
	return out
}
