package variantspec

import (
	"fmt"
	"sort"
	"strings"
)

// GRAPH TOPOLOGY — concurrency, conditional routing and merge, declared at the SPEC level
// ───────────────────────────────────────────────────────────────────────────────────────
//
// P34 §5, design D3–D6, decisions.md D-34.2 / D-34.3; PRD FR12–FR17.
//
// # Why this is not a Dimension
//
// Every member of `Dimensions()` is a property of ONE node. Topology is a property BETWEEN nodes.
// Making graph the first non-per-node Dimension would break the invariant that lets the transform
// engine iterate `Dimensions()` uniformly and lets the eval harness stay axis-agnostic — and once
// broken, every future consumer has to ask which kind of dimension it is holding. So topology lives
// beside `order` and `edges` on the spec, which is a structure that is already hashed and already in
// the right place.
//
// # Why concurrency is declared OVER `Order` and never instead of it
//
// The honest data model for a concurrent graph is a DAG, and replacing `Order` with one would change
// the serialization of EVERY spec in existence — the same orphaning chain ADR-014 spends its whole
// argument preventing, arriving through the spec shape instead of through the seal path. Declaring
// groups over the order is additive and `omitempty`, so a spec that declares none serialises
// byte-identically to its pre-P34 form.
//
// It also preserves REPLAY DETERMINISM, which is worth having on its own: a run that overlapped two
// nodes still has a defined order to be replayed in, which is what attribution, run diffing and
// failure reproduction all need.

// MergeStrategy is how a fan-in combines its nodes' outputs. A CLOSED set, because a stored
// `config_hash` must still mean the same thing months from now and a stored strategy name is only
// interpretable against the vocabulary it was written under.
//
// 🔴 Both members are TOTAL and DETERMINISTIC, and that is the selection criterion rather than a
// coincidence. "First result wins" and "last writer wins" are the defaults design D6 refuses, and
// they are refused for a reason that does not stop applying just because an author picked them
// explicitly: under concurrency, which node is "first" is a property of the machine. A spec whose
// meaning depends on scheduling cannot be scored, because two runs of one configuration would not
// agree.
type MergeStrategy string

const (
	// MergeAllFields unions every node's output object into one.
	//
	// 🔴 A key produced by two nodes is REFUSED at validate, not resolved by precedence. Precedence
	// here would be the platform deciding which of the author's two values is the real one — the exact
	// silent semantic choice D6 exists to prevent — and under concurrency the answer would additionally
	// depend on scheduling.
	MergeAllFields MergeStrategy = "all-fields"
	// MergeNamespaced nests each node's output under its own node id. It cannot collide by
	// construction, which is why it is the answer for a fan-in whose nodes genuinely produce the same
	// field names.
	MergeNamespaced MergeStrategy = "namespaced"
)

// MergeStrategies lists the closed set, sorted.
func MergeStrategies() []MergeStrategy { return []MergeStrategy{MergeAllFields, MergeNamespaced} }

func (m MergeStrategy) valid() bool {
	for _, v := range MergeStrategies() {
		if v == m {
			return true
		}
	}
	return false
}

// FailureMode is what happens to a concurrent group when one of its nodes fails (PRD §14 Q3,
// decisions.md D-34.3). REQUIRED on every merge and never defaulted.
//
// 🔴 "Cancel everything if the enrichment call fails" and "answer with whatever enrichment returned"
// are different products. A platform that picks one is deciding what the customer's code means, which
// is D6's reasoning applied to failure rather than to combination.
type FailureMode string

const (
	// FailFast — the first node failure aborts the group and the downstream node is not entered.
	FailFast FailureMode = "fail-fast"
	// CollectPartial — every node runs to completion and the merge receives only those that
	// succeeded.
	//
	// 🔴 It carries an ENFORCED consequence: the merge may deliver fewer inputs than the group has
	// nodes, so the downstream node's input contract must admit that absence. A `collect-partial`
	// merge whose downstream contract makes a contributed field REQUIRED is refused at validate —
	// otherwise this is a promise the type system does not keep, discovered at run time by whoever was
	// unlucky.
	CollectPartial FailureMode = "collect-partial"
)

// FailureModes lists the closed set, sorted.
func FailureModes() []FailureMode { return []FailureMode{CollectPartial, FailFast} }

func (f FailureMode) valid() bool {
	for _, v := range FailureModes() {
		if v == f {
			return true
		}
	}
	return false
}

// Merge declares how a fan-in's inputs are combined and what happens when one of its nodes fails.
//
// 🚫 There is no zero value that means anything. Every field is required; a fan-in with no Merge is
// refused at validate (FR14), and a Merge with no `on_node_failure` is refused too. A fan-in whose
// results are dropped is not a topology, it is a bug with a diagram.
type Merge struct {
	// Into is the downstream node the group's nodes converge on. Explicit rather than derived from the edges,
	// for the reason HarnessGroup.Edges is explicit: a derived answer is a SECOND definition of the
	// convergence point, and it can disagree with the edges the executor actually walks.
	Into string `json:"into"`
	// Strategy is how the inputs combine. Closed set, never defaulted.
	Strategy MergeStrategy `json:"strategy"`
	// OnNodeFailure is what happens when one of the group's nodes fails. Closed set, REQUIRED.
	OnNodeFailure FailureMode `json:"on_node_failure"`
}

// GraphGroup declares that a set of nodes form a topology unit: they may run concurrently, and where
// they converge, how their results combine.
//
// Additive and `omitempty` on the spec, which is what makes the byte-identical guarantee hold.
type GraphGroup struct {
	// Nodes are the group's constituents; they must ALL appear in `Order`. A node the ordering does not
	// contain would be one the executor never walks, declared concurrent with nodes it will never run
	// beside.
	//
	// 🔴 The wire name is `nodes` and NOT `members`, and the reason is worth a line rather than a rename
	// nobody can explain later. `p27_hash_recording_test.go` bans the ownership vocabulary — tenant,
	// owner, account, member, seat — from anything that reaches `config_hash`, because a hashed field
	// naming WHO forks one configuration per organization and orphans every result filed under the old
	// hash. The ban is deliberately on the VOCABULARY rather than on a list of known fields, "because
	// the thing being prevented is a field nobody has written yet" — so it caught `members` here, in a
	// completely different sense of the word.
	//
	// The graph axis yields. Narrowing a fence built to catch unwritten fields, in order to admit one
	// written today, spends the fence to save a synonym; `nodes` is at least as clear in a graph, and
	// `on_node_failure` reads the same way.
	Nodes []string `json:"nodes"`
	// Concurrent declares that its nodes MAY overlap. `Order` still contains every one of them in a
	// defined sequence, and a replay visits them in that sequence even when the live run overlapped
	// them (design D4).
	Concurrent bool `json:"concurrent,omitempty"`
	// Merge is required when the nodes fan in, and refused when they do not.
	Merge *Merge `json:"merge,omitempty"`
}

// EdgeKindPredicate is the conditional edge (FR13, design D5, decisions.md D-34.2).
//
// The predicate is an `expr` binding and is validated by the SAME check that validates a prompt slot's
// `expr`: the symbol must be recorded as in scope at the producing call site, or the spec is refused,
// naming the symbol. One grammar, one validator — see predicateInScope.
const EdgeKindPredicate = "predicate"

// EdgeKinds is the closed set. `data` and `control` are P5's, unchanged; `predicate` is P34's.
func EdgeKinds() []string { return []string{"control", "data", EdgeKindPredicate} }

// validateGraph runs every topology check that needs no IR and no registry — the structural half.
//
// The checks that need the IR (a predicate's lexical scope, a merge against the downstream node's
// typed input contract) live in Resolve, exactly as a skill_ref's existence does. That split keeps
// "your JSON is malformed" separate from "your JSON is fine but points at something that isn't there".
func (s *VariantSpec) validateGraph(inOrder map[string]bool) error {
	// Edge kinds and predicates. A predicate on a non-predicate edge, or a predicate edge without one,
	// are both refused: the kind and the payload must agree, or the executor and the reader disagree
	// about whether this edge is conditional.
	for i, e := range s.Edges {
		if e.Kind == EdgeKindPredicate && strings.TrimSpace(e.Predicate) == "" {
			return specErr("", graphDim, ErrInvalidSpec,
				"edges[%d] (%s -> %s) declares kind %q and no predicate; a conditional edge with no "+
					"condition is an unconditional edge that a reader will believe is guarded",
				i, e.FromNodeID, e.ToNodeID, EdgeKindPredicate)
		}
		if e.Kind != EdgeKindPredicate && strings.TrimSpace(e.Predicate) != "" {
			return specErr("", graphDim, ErrInvalidSpec,
				"edges[%d] (%s -> %s) carries a predicate %q but its kind is %q; the predicate would "+
					"never be evaluated, so the author believes this edge is guarded and it is not",
				i, e.FromNodeID, e.ToNodeID, e.Predicate, e.Kind)
		}
	}

	declaredIn := map[string]int{} // node -> how many groups declare a merge into it
	for i, g := range s.GraphGroups {
		if len(g.Nodes) < 2 {
			return specErr("", graphDim, ErrInvalidSpec,
				"graph_groups[%d] has %d node(s); a topology unit is a statement about how nodes relate "+
					"to EACH OTHER, and one node relates to nothing", i, len(g.Nodes))
		}
		seenNode := map[string]bool{}
		for j, m := range g.Nodes {
			if m == "" {
				return specErr("", graphDim, ErrInvalidSpec, "graph_groups[%d].nodes[%d] is empty", i, j)
			}
			if seenNode[m] {
				// Rejected rather than de-duplicated: a node written twice means the author believes
				// something about it that is not true, and collapsing it hides the misunderstanding.
				return specErr(m, graphDim, ErrInvalidSpec,
					"graph_groups[%d] names %q twice", i, m)
			}
			seenNode[m] = true
			// FR12 / task 5.1: `Order` is still the deterministic walk, so a group node outside it is one
			// the executor never visits.
			if !inOrder[m] {
				return specErr(m, graphDim, ErrInvalidSpec,
					"graph_groups[%d] names %q, which order does not contain. Concurrency is declared OVER "+
						"the ordering, never instead of it: order still lists every node the executor walks, "+
						"and a replay visits them in that sequence even when the live run overlapped them",
					i, m)
			}
		}

		// A FAN-IN is two or more of a group's nodes with an edge into one downstream node. Derived from the edges
		// the spec ALREADY DECLARES rather than from the merge, so "a fan-in must declare a merge" is a
		// check rather than a restatement of what the author wrote.
		fanIns := fanInTargets(s.Edges, g.Nodes)
		if len(fanIns) > 1 {
			return specErr("", graphDim, ErrInvalidSpec,
				"graph_groups[%d] converges on more than one node (%s); a group with two convergence "+
					"points is two groups, and one merge declaration cannot describe both",
				i, strings.Join(fanIns, ", "))
		}
		if len(fanIns) == 0 {
			if g.Merge != nil {
				return specErr("", graphDim, ErrInvalidSpec,
					"graph_groups[%d] declares a merge but its nodes converge on nothing; the merge would "+
						"never run, so the author believes results are being combined and they are not", i)
			}
			continue
		}

		// FR14 / task 5.4: refused at validate, NEVER defaulted. Every available default — first result
		// wins, concatenate, last writer — is a semantic choice about the author's program, and none is
		// more obviously right than the others.
		if g.Merge == nil {
			return specErr(fanIns[0], graphDim, ErrInvalidSpec,
				"graph_groups[%d]: %d nodes converge on %q and no merge is declared. This is REFUSED "+
					"rather than defaulted: first-result-wins, concatenate and last-writer are all semantic "+
					"choices about YOUR program, none of them is more obviously right than the others, and a "+
					"fan-in whose results are silently dropped is not a topology. Declare a merge with a "+
					"strategy (%s) and an on_node_failure (%s).",
				i, countConverging(s.Edges, g.Nodes, fanIns[0]), fanIns[0],
				joinStrategies(), joinFailureModes())
		}
		if g.Merge.Into != fanIns[0] {
			return specErr(g.Merge.Into, graphDim, ErrInvalidSpec,
				"graph_groups[%d].merge.into is %q but the group's edges converge on %q; the merge and the "+
					"wiring describe two different graphs, and the executor walks the wiring",
				i, g.Merge.Into, fanIns[0])
		}
		if !g.Merge.Strategy.valid() {
			return specErr(g.Merge.Into, graphDim, ErrInvalidSpec,
				"graph_groups[%d].merge.strategy is %q, want one of %s", i, g.Merge.Strategy, joinStrategies())
		}
		// decisions.md D-34.3: required, from a closed set, never defaulted.
		if !g.Merge.OnNodeFailure.valid() {
			return specErr(g.Merge.Into, graphDim, ErrInvalidSpec,
				"graph_groups[%d].merge.on_node_failure is %q, want one of %s. It is REQUIRED and has no "+
					"default: what happens to the other nodes when one fails is a statement about YOUR "+
					"program, and %q and %q are different products",
				i, g.Merge.OnNodeFailure, joinFailureModes(), FailFast, CollectPartial)
		}
		declaredIn[g.Merge.Into]++
		if declaredIn[g.Merge.Into] > 1 {
			return specErr(g.Merge.Into, graphDim, ErrInvalidSpec,
				"two graph groups declare a merge into %q; the node would be handed two combinations of "+
					"two different input sets, and nothing says which arrives", g.Merge.Into)
		}
	}
	return nil
}

// graphDim is the axis name carried on a topology refusal. A plain string and deliberately NOT a
// `Dimension`, for the reason design D3 gives and the reason `wiringRefusalDim` is one too: minting a
// Dimension const for a between-nodes property would be the invariant break the design refuses. The
// error still names the axis, which is what a reader needs.
const graphDim Dimension = "graph"

// fanInTargets returns the downstream nodes two or more of these group nodes have an edge into, sorted.
//
// 🔴 Derived from the DECLARED edges. A group could have inferred its own convergence point, which
// would be more convenient and would be a SECOND definition of the graph — one that can drift from the
// wiring the executor actually walks. Same discipline as HarnessGroup consuming P15's edges.
func fanInTargets(edges []Edge, groupNodes []string) []string {
	inGroup := map[string]bool{}
	for _, m := range groupNodes {
		inGroup[m] = true
	}
	into := map[string]map[string]bool{}
	for _, e := range edges {
		if !inGroup[e.FromNodeID] || inGroup[e.ToNodeID] {
			continue // an edge INSIDE the group is sequencing, not a fan-in out of it
		}
		if into[e.ToNodeID] == nil {
			into[e.ToNodeID] = map[string]bool{}
		}
		into[e.ToNodeID][e.FromNodeID] = true
	}
	var out []string
	for target, froms := range into {
		if len(froms) > 1 {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out
}

// countConverging counts how many of these group nodes have an edge into `target`.
func countConverging(edges []Edge, groupNodes []string, target string) int {
	inGroup := map[string]bool{}
	for _, m := range groupNodes {
		inGroup[m] = true
	}
	froms := map[string]bool{}
	for _, e := range edges {
		if inGroup[e.FromNodeID] && e.ToNodeID == target {
			froms[e.FromNodeID] = true
		}
	}
	return len(froms)
}

// MembersConvergingOn returns the nodes of this group that feed `target`, sorted. Exported because
// the resolve-time contract check and the transform both need the exact producer set, and two answers
// to "who feeds this node" is how a merge gets validated against the wrong schemas.
func (g GraphGroup) MembersConvergingOn(edges []Edge, target string) []string {
	inGroup := map[string]bool{}
	for _, m := range g.Nodes {
		inGroup[m] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if inGroup[e.FromNodeID] && e.ToNodeID == target && !seen[e.FromNodeID] {
			seen[e.FromNodeID] = true
			out = append(out, e.FromNodeID)
		}
	}
	sort.Strings(out)
	return out
}

func joinStrategies() string {
	out := make([]string, 0, len(MergeStrategies()))
	for _, m := range MergeStrategies() {
		out = append(out, string(m))
	}
	return strings.Join(out, ", ")
}

func joinFailureModes() string {
	out := make([]string, 0, len(FailureModes()))
	for _, f := range FailureModes() {
		out = append(out, string(f))
	}
	return strings.Join(out, ", ")
}

// GraphWidth is the widest concurrent group this spec declares, or 0 when it declares none. Read by
// the envelope's concurrency check, which needs one number rather than a walk.
func (s *VariantSpec) GraphWidth() (width, groupIndex int) {
	for i, g := range s.GraphGroups {
		if g.Concurrent && len(g.Nodes) > width {
			width, groupIndex = len(g.Nodes), i
		}
	}
	return width, groupIndex
}

// describeGroup renders a group for an error message.
func describeGroup(i int, g GraphGroup) string {
	return fmt.Sprintf("graph_groups[%d] (%s)", i, strings.Join(g.Nodes, ", "))
}
