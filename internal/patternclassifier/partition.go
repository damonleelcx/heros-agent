package patternclassifier

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// Scope says what a label is ATTACHED to. It is the axis the overlap rule turns on, and it is not
// the same axis as the taxonomy group.
//
// A region-scoped pattern is a claim about a SHAPE spanning several nodes (a router and its
// branches; a retriever chain feeding a generator). Two region claims can contest the same nodes.
// A node-scoped pattern is a claim about ONE node's capability (this node is bound to tools). It
// contests nothing: it co-exists with whatever region owns the node — which is exactly the
// resolution for "Tool Use inside a Routing branch" (design Decision 4 / PRD Q3).
type Scope string

const (
	ScopeRegion Scope = "region"
	ScopeNode   Scope = "node"
)

// RegionProposal is what a structural detector emits: "this set of nodes matches my signature".
// It is a PROPOSAL, not a label — the partitioner assigns the region its identity and resolves
// overlaps before anything becomes a Label. Keeping detection and arbitration separate is what lets
// each detector stay a pure, local predicate while the precedence rule lives in exactly one place.
type RegionProposal struct {
	Pattern    Pattern
	DetectorID string
	Scope      Scope
	// NodeIDs are the members of the region. Order is irrelevant; the partitioner normalises it.
	NodeIDs []string
	// Confidence is the detector's calibrated band for this signature.
	Confidence float64
	// Candidate marks a behavioral pattern the detector could only find a structural candidate for.
	Candidate bool
}

// Subgraph is a named region of the IR, the unit classification is emitted against. It mirrors the
// P0-reserved `subgraphs[]` shape so it can be written straight back to the IR.
type Subgraph struct {
	SubgraphID string   `json:"subgraph_id"`
	NodeIDs    []string `json:"node_ids"`
}

// SubgraphIDFor derives a region's identity DETERMINISTICALLY from its members, the same discipline
// as node_id: two classifier runs over the same IR must produce the same subgraph_id, or labels are
// not diffable and "byte-identical across runs" is unachievable. A counter ("sg_1", "sg_2") would
// depend on detector execution order, which is exactly the coupling to avoid.
//
// The id is content-addressed, so the SAME region proposed by two different detectors gets the SAME
// id — which is what makes "two patterns co-exist on one subgraph" representable at all.
func SubgraphIDFor(nodeIDs []string) string {
	norm := normalizeNodeIDs(nodeIDs)
	h := sha256.Sum256([]byte(strings.Join(norm, "\x00")))
	return "sg_" + hex.EncodeToString(h[:])[:12]
}

// normalizeNodeIDs returns a sorted, de-duplicated copy. Sorting is what makes the id independent of
// the order a detector happened to walk the graph in.
func normalizeNodeIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// graph is the indexed view of an IR the detectors read. Built once per classification: every
// detector is a pure function OF THIS, so none of them re-walks the edge slice and none of them can
// disagree about what "downstream" means.
type graph struct {
	ir    *discovery.IR
	nodes map[string]*discovery.IRNode
	// order is the node_ids in the IR's own (sorted) order — the deterministic iteration order
	// every detector uses instead of ranging over a map.
	order []string

	dataOut, dataIn       map[string][]string
	controlOut, controlIn map[string][]string
	// anyOut/anyIn ignore edge kind; used for connectivity (residue components).
	anyOut, anyIn map[string][]string
}

func newGraph(ir *discovery.IR) *graph {
	g := &graph{
		ir: ir, nodes: make(map[string]*discovery.IRNode, len(ir.Nodes)),
		dataOut: map[string][]string{}, dataIn: map[string][]string{},
		controlOut: map[string][]string{}, controlIn: map[string][]string{},
		anyOut: map[string][]string{}, anyIn: map[string][]string{},
	}
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		g.nodes[n.NodeID] = n
		g.order = append(g.order, n.NodeID)
	}
	sort.Strings(g.order)
	for _, e := range ir.Edges {
		// A dangling endpoint is not a graph fact; P1 already drops such edges, and if one survives
		// we must not invent a node for it.
		if g.nodes[e.FromNodeID] == nil || g.nodes[e.ToNodeID] == nil {
			continue
		}
		switch e.Kind {
		case "data":
			g.dataOut[e.FromNodeID] = append(g.dataOut[e.FromNodeID], e.ToNodeID)
			g.dataIn[e.ToNodeID] = append(g.dataIn[e.ToNodeID], e.FromNodeID)
		case "control":
			g.controlOut[e.FromNodeID] = append(g.controlOut[e.FromNodeID], e.ToNodeID)
			g.controlIn[e.ToNodeID] = append(g.controlIn[e.ToNodeID], e.FromNodeID)
		}
		g.anyOut[e.FromNodeID] = append(g.anyOut[e.FromNodeID], e.ToNodeID)
		g.anyIn[e.ToNodeID] = append(g.anyIn[e.ToNodeID], e.FromNodeID)
	}
	for _, m := range []map[string][]string{g.dataOut, g.dataIn, g.controlOut, g.controlIn, g.anyOut, g.anyIn} {
		for k, v := range m {
			m[k] = normalizeNodeIDs(v)
		}
	}
	return g
}

// residueComponents returns the weakly-connected components of the nodes NO region proposal covers —
// the ambiguous residue, and the only thing the LLM fallback is ever shown.
//
// Components, not one bag: two unrelated unclassified islands are two different questions, and
// merging them would ask the model to find one pattern spanning nodes that never touch.
func (g *graph) residueComponents(covered map[string]bool) [][]string {
	seen := map[string]bool{}
	var comps [][]string
	for _, id := range g.order { // deterministic seed order
		if covered[id] || seen[id] {
			continue
		}
		var comp []string
		stack := []string{id}
		seen[id] = true
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, cur)
			for _, nb := range append(append([]string{}, g.anyOut[cur]...), g.anyIn[cur]...) {
				if covered[nb] || seen[nb] {
					continue
				}
				seen[nb] = true
				stack = append(stack, nb)
			}
		}
		comps = append(comps, normalizeNodeIDs(comp))
	}
	sort.Slice(comps, func(i, j int) bool { return strings.Join(comps[i], "\x00") < strings.Join(comps[j], "\x00") })
	return comps
}
