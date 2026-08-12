package herosagent

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// residue.go selects THE ONLY THING HEROS IS EVER SHOWN (task 4.1).
//
// # NFR1 holds by construction, not by review
//
// 🔴 `Input` HAS NO FIELD FOR A WHOLE-REPOSITORY PASS. A caller cannot ask for one, because there is
// nowhere to put the request — not a boolean, not a mode, not an empty residue that means "everything".
// That is the difference between a scope somebody must remember to honour and a scope the type system
// will not let them break.
//
// Why the residue is the right scope (D3): the requested capability is *filling what the parsers
// missed*. Restricting HEROS to it costs nothing the operator asked for, makes cost proportional to the
// gap, and turns "HEROS did not break the Go path" into a byte-comparison rather than an argument.

// Residue is what the frontends did NOT establish: the pairs with no edge, and the nodes carrying an
// unresolved field.
type Residue struct {
	// Nodes are node ids whose IR carries an `unresolved` sentinel in a field a model could name.
	Nodes []string `json:"nodes"`
	// Pairs are ordered node pairs with NO edge between them in either direction. These are the only
	// pairs HEROS may propose an edge for — see EdgeIsAvailable.
	Pairs []Pair `json:"pairs"`
	// UnlabelledRegions are subgraph ids no rule detector covered.
	UnlabelledRegions []string `json:"unlabelled_regions"`
	// Frontends is the discovery report's record of which frontends produced this IR and how deeply
	// they analyse. It is carried INTO the agent's context, because "the python frontend is syntactic
	// and cannot follow a value across a statement" is the single most useful thing an analyser can be
	// told about why a gap exists.
	Frontends []discovery.FrontendRun `json:"frontends"`
}

// Pair is an ordered pair of node ids with no established edge.
type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Empty reports whether there is nothing to infer. A caller seeing this makes ZERO provider calls and
// reports CodeNothingToInfer — a healthy answer, and the one task 10.3 counts.
func (r Residue) Empty() bool {
	return len(r.Nodes) == 0 && len(r.Pairs) == 0 && len(r.UnlabelledRegions) == 0
}

// SelectResidue computes the residue from the IR and the discovery report.
//
// 🔴 A pair is in the residue only when NEITHER direction carries an edge. That is D3's first fence
// expressed as a SELECTION rather than as a check afterwards: an edge a frontend established is not
// offered to the agent at all, so the agent cannot propose one over it and cannot be asked to.
//
// The pair count is quadratic in nodes, which is why Budget exists and why the caller bounds it. A
// repository with 400 call sites has 160,000 pairs, and an agent shown all of them is an agent whose
// cost is proportional to the repository rather than to the gap.
func SelectResidue(ir *discovery.IR, rep discovery.DiscoveryReport, unlabelled []string) Residue {
	out := Residue{
		Nodes: []string{}, Pairs: []Pair{}, UnlabelledRegions: []string{},
		Frontends: append([]discovery.FrontendRun{}, rep.Frontends...),
	}
	if ir == nil {
		return out
	}

	established := map[Pair]bool{}
	for _, e := range ir.Edges {
		established[Pair{From: e.FromNodeID, To: e.ToNodeID}] = true
		// BOTH directions. An edge a→b means the frontend established a relationship between those two
		// nodes; offering b→a to the agent would let it propose a contradiction of a measured fact and
		// call it a gap.
		established[Pair{From: e.ToNodeID, To: e.FromNodeID}] = true
	}

	ids := make([]string, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		ids = append(ids, n.NodeID)
		if nodeHasUnresolvedField(n) {
			out.Nodes = append(out.Nodes, n.NodeID)
		}
	}
	sort.Strings(ids)
	sort.Strings(out.Nodes)

	for _, a := range ids {
		for _, b := range ids {
			if a == b || established[Pair{From: a, To: b}] {
				continue
			}
			out.Pairs = append(out.Pairs, Pair{From: a, To: b})
		}
	}
	out.UnlabelledRegions = append(out.UnlabelledRegions, unlabelled...)
	sort.Strings(out.UnlabelledRegions)
	return out
}

// nodeHasUnresolvedField reports whether a node carries the documented `unresolved` sentinel in a
// field the agent could plausibly resolve from surrounding source.
//
// It reads the SENTINEL rather than an emptiness test, because `discovery.UnresolvedSentinel`'s own doc
// says it is "the single documented constant emitted into IR fields that static analysis cannot
// resolve". An empty string means something else — a field the frontend does not populate at all — and
// asking a model to fill one of those is asking it to invent.
func nodeHasUnresolvedField(n discovery.IRNode) bool {
	return n.Model.ModelID == discovery.UnresolvedSentinel ||
		n.Model.Provider == discovery.UnresolvedSentinel ||
		n.Prompt.Inline == discovery.UnresolvedSentinel
}

// EdgeIsAvailable reports whether HEROS may write an edge between two nodes.
//
// 🔴 D3 FENCE 1, as a function, called at the WRITE boundary as well as honoured in the selection.
// Both, because they fail differently: the selection stops the agent being ASKED, and this stops an
// answer that names a pair it was not asked about — which a model can do, and which an injected
// instruction in the customer's source would specifically try to make it do.
//
// "Rule-derived topology is immutable to HEROS" is the whole of it: it may not emit an edge between two
// nodes where a frontend already emitted one, and it may not delete one.
func EdgeIsAvailable(ir *discovery.IR, from, to string) bool {
	if ir == nil {
		return false
	}
	for _, e := range ir.Edges {
		if (e.FromNodeID == from && e.ToNodeID == to) || (e.FromNodeID == to && e.ToNodeID == from) {
			return false
		}
	}
	return true
}

// NodeExists reports whether the IR carries a node id.
//
// D8: "the only thing HEROS can express is a graph over nodes that already exist, so the worst an
// injected instruction achieves is a wrong edge". This is the function that makes that true.
func NodeExists(ir *discovery.IR, id string) bool {
	if ir == nil {
		return false
	}
	for _, n := range ir.Nodes {
		if n.NodeID == id {
			return true
		}
	}
	return false
}
