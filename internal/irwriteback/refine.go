package irwriteback

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

// ObservedShape is what tracing saw of a node's I/O: the fields (and their JSON types) actually present
// in its inputs and outputs across the traced run. It is the evidence a permissive schema is refined
// toward (task 8.3).
type ObservedShape struct {
	// InputFields / OutputFields map field name → observed JSON type ("string","number","object",…).
	InputFields  map[string]string
	OutputFields map[string]string
}

// RefinementReport surfaces what a refinement did and — critically — what it did NOT do silently: which
// nodes remain permissive, and which orderings a refinement would affect (Decision 7, Q3). Refinement
// that tightens a schema could retroactively make a previously-coherent ordering incoherent, so it is
// always reported, never applied silently.
type RefinementReport struct {
	// Refined lists node ids whose permissive schema was tightened toward the observed shape.
	Refined []string `json:"refined"`
	// StillPermissive lists node ids that remain `{"type":"object"}` — no observed shape to refine
	// toward, or none supplied. Surfaced so a consumer knows which coherence verdicts are still loose.
	StillPermissive []string `json:"still_permissive"`
}

// IsPermissive reports whether a schema is the permissive stub `{"type":"object"}` with no declared
// properties — the shape P0/P1 emit before any refinement, which admits more orderings as coherent than
// a strict one would.
func IsPermissive(schema map[string]any) bool {
	if schema == nil {
		return true
	}
	if t, _ := schema["type"].(string); t != "object" {
		return false
	}
	props, ok := schema["properties"].(map[string]any)
	return !ok || len(props) == 0
}

// RefineSchemas returns a copy of the IR with permissive OUTPUT schemas tightened toward the observed
// output shape, ADDITIVELY: it declares the observed fields as properties (no `required` is added, so
// no consumer's obligation changes), which only ENABLES more coherent orderings — it never retroactively
// breaks one. It refines only permissive schemas: a schema an author already tightened is authoritative
// and left alone. The report names what was refined and what stays permissive.
//
// Input schemas are deliberately NOT auto-tightened here: adding a `required` field to a consumer input
// is what could retroactively invalidate a saved ordering, so that decision is surfaced (see
// OrderingsAffectedByRefinement) rather than applied silently.
func RefineSchemas(ir *discovery.IR, observed map[string]ObservedShape) (*discovery.IR, RefinementReport) {
	out := *ir
	out.Nodes = append([]discovery.IRNode(nil), ir.Nodes...)

	var rep RefinementReport
	for i := range out.Nodes {
		n := &out.Nodes[i]
		shape, haveShape := observed[n.NodeID]
		if IsPermissive(n.IOContract.OutputSchema) {
			if haveShape && len(shape.OutputFields) > 0 {
				n.IOContract.OutputSchema = declareProperties(shape.OutputFields)
				rep.Refined = append(rep.Refined, n.NodeID)
			} else {
				rep.StillPermissive = append(rep.StillPermissive, n.NodeID)
			}
		}
	}
	sort.Strings(rep.Refined)
	sort.Strings(rep.StillPermissive)
	return &out, rep
}

// declareProperties builds a tightened object schema from observed fields. No `required` is set — the
// refinement declares WHAT the node can produce, not what it must, so it cannot break a consumer.
func declareProperties(fields map[string]string) map[string]any {
	props := map[string]any{}
	for name, typ := range fields {
		props[name] = map[string]any{"type": typ}
	}
	return map[string]any{"type": "object", "properties": props}
}

// AffectedEdge names a producer→consumer edge whose coherence verdict a refinement would change.
type AffectedEdge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Before     string `json:"before"` // verdict kind on this edge before refinement
	After      string `json:"after"`  // verdict kind after refinement
}

// OrderingsAffectedByRefinement compares an ordering's per-edge coherence before and after a refinement
// and returns the edges whose verdict changed. This is how a refinement is SURFACED (Decision 7, Q3):
// tightening a schema can flip an edge from coherent to adapted/rejected (or the reverse), and the user
// must see which orderings a refinement would affect before it is adopted — never a silent change.
func OrderingsAffectedByRefinement(before, after *discovery.IR, ordering typedcontract.Ordering) []AffectedEdge {
	catalog := typedcontract.DefaultCatalog()
	beforeEdges := edgeVerdicts(before, ordering, catalog)
	afterEdges := edgeVerdicts(after, ordering, catalog)

	var out []AffectedEdge
	for _, e := range ordering.Edges {
		if e.Kind != "data" {
			continue
		}
		key := e.FromNodeID + "->" + e.ToNodeID
		b, a := beforeEdges[key], afterEdges[key]
		if b != a {
			out = append(out, AffectedEdge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Before: b, After: a})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromNodeID != out[j].FromNodeID {
			return out[i].FromNodeID < out[j].FromNodeID
		}
		return out[i].ToNodeID < out[j].ToNodeID
	})
	return out
}

// edgeVerdicts returns each data edge's coherence classification (coherent | adapted | rejected) under
// one IR, by validating single-edge orderings so the verdict is attributable per edge.
func edgeVerdicts(ir *discovery.IR, ordering typedcontract.Ordering, catalog *typedcontract.Catalog) map[string]string {
	out := map[string]string{}
	for _, e := range ordering.Edges {
		if e.Kind != "data" {
			continue
		}
		one := typedcontract.Ordering{Order: ordering.Order, Edges: []typedcontract.Edge{e}}
		v := typedcontract.ValidateOrdering(ir, one, catalog)
		out[e.FromNodeID+"->"+e.ToNodeID] = string(v.Kind)
	}
	return out
}
