package proposal

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// THE TWO OPERATORS ADR-014'S SPLIT CREATED (P34 tasks 6.1, 6.2)
// ──────────────────────────────────────────────────────────────
//
// Two new operators are two new SEARCH SPACES, and PRD §9.5 states how they must be measured: by
// per-axis pass rate through the P5.5 verification gate, never a mean across axes. A graph operator
// with a 5% pass rate hidden inside a healthy average is an operator that is not working. See
// axis_passrate.go.
//
// # Why `loop` replaces the harness operator's emission rather than joining it
//
// FR9 says new authoring cannot create a legacy loop-bearing harness entry, and a PROPOSAL IS NEW
// AUTHORING. `harnessStrategyOp` wrote a loop strategy into `harness_ref`, which is exactly the shape
// FR9 forbids — so `OpLoopStrategy` supersedes it and writes `loop_ref`.
//
// 🔴 `OpHarnessStrategy` stays in the enum as a RESERVED wire value. It is stored on proposal rows, and
// removing a member of a persisted vocabulary re-identifies every row that named it — the same hazard
// the harness stop-reason block records, arriving through a different table. Nothing emits it; every
// row that already does keeps decoding.

// OpLoopStrategy proposes swapping the ITERATION POLICY around a node's call — which control loop runs,
// what stops it, and how many turns it takes (P34 task 6.1).
//
// It is `OpHarnessStrategy`'s successor rather than a sibling: the same hypothesis ("this node's failing
// cases needed more than one turn"), on the axis ADR-014 moved that hypothesis to.
const OpLoopStrategy OperatorKind = "loop_strategy_switch"

// OpGraphTopology proposes a TOPOLOGY change: two nodes that already run independently, declared
// concurrent (P34 task 6.2).
//
// 🔴 It is emphatically NOT `OpMerge`, and the two must never be conflated even though P34 uses the word
// "merge" for something else entirely:
//
//	OpMerge (P15)        FUSES two redundant nodes into one. The node SET shrinks. It is a claim that one
//	                     of the two calls was unnecessary.
//	OpGraphTopology      declares two nodes CONCURRENT. The node set is unchanged, every call still
//	                     happens, and the claim is only about WHEN. A P34 `Merge` declaration then says
//	                     how their results combine at the node they converge on.
//
// One shrinks the graph and changes what runs; the other changes only the order things run in. A
// proposal row that meant either would be unreadable months later, which is why they are two kinds with
// a fence between them (TestTheGraphOperatorNeverFusesNodes).
const OpGraphTopology OperatorKind = "graph_topology_switch"

// SignalLatencyBottleneck is the structural driver behind a topology proposal: two independent calls
// run one after the other and their combined wall-clock dominates the run.
//
// 🔴 A Signal rather than a taxonomy code, for the reason SignalCostBottleneck is one: the frozen P4.5
// taxonomy is about what WENT WRONG in a case, and "this was correct but slow" is not a failure.
// Inventing a code for it would put a performance observation in a vocabulary about correctness.
const SignalLatencyBottleneck Signal = "latency_bottleneck"

// ── the loop operator ────────────────────────────────────────────────────────────────────────────

type loopStrategyOp struct{}

func (loopStrategyOp) Kind() OperatorKind { return OpLoopStrategy }

// Handles is Reflection's own non-convergence code: "revisions never converge" is a statement about the
// LOOP, so an iteration-policy change is a first-class answer to it.
func (loopStrategyOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseNonConvergence}
}

func (loopStrategyOp) HandlesSignal() Signal { return SignalScaffoldMismatch }

// AdmissiblePatterns restricts the operator to the patterns whose WORK is a control loop — unchanged
// from the harness operator it succeeds, because the split moved the axis and not the hypothesis.
//
// 🔴 Not nil. Changing the iteration policy of a node that does one thing once is a change with a
// mechanism but no hypothesis: it would resolve, hash, occupy a verification slot, and pay for turns
// nobody had a reason to expect would help.
func (loopStrategyOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return []patternclassifier.Pattern{
		patternclassifier.Reflection,
		patternclassifier.Planning,
		patternclassifier.ToolUse,
	}
}

func (op loopStrategyOp) Propose(in OperatorInput) ([]Candidate, error) {
	if in.Base == nil {
		return nil, nil
	}
	nodeID := in.NodeID()
	current := in.Base.Nodes[nodeID].LoopRef

	var out []Candidate
	for _, l := range in.Menu.loopStrategiesExcept(current) {
		spec := cloneSpec(in.Base)
		setLoop(spec, nodeID, l.Ref)
		label := l.Title
		if label == "" {
			label = l.Strategy
		}
		driver := string(op.HandlesSignal())
		if in.Signal != SignalNone {
			driver = string(in.Signal)
		} else if in.Diagnosis.TaxonomyCode != "" {
			driver = string(in.Diagnosis.TaxonomyCode)
		}
		out = append(out, newCandidate(op.Kind(), in, nodeID, []string{string(variantspec.DimLoop)}, spec,
			fmt.Sprintf("%s on %d case(s) → switch the control loop to %s (%s). %s",
				driver, len(in.Diagnosis.EvidenceCaseIDs), label, turnsPhrase(l.MaxTurns),
				harnessTradeoffPhrase(l.MaxTurns))))
	}
	return out, nil
}

// setLoop binds a loop-registry version_id at a node. It sets ONLY LoopRef: the loop axis is
// per-dimension independent like every other, so proposing a policy swap must not disturb the node's
// envelope, its model, or anything else the baseline pinned.
//
// 🚫 It never writes HarnessRef. That is FR9 made mechanical rather than remembered — there is no line
// in this package that can put a loop strategy into the harness dimension.
func setLoop(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.LoopRef = ref
	s.Nodes[node] = o
}

// ── the graph operator ───────────────────────────────────────────────────────────────────────────

type graphTopologyOp struct{}

func (graphTopologyOp) Kind() OperatorKind                { return OpGraphTopology }
func (graphTopologyOp) Handles() []diagnosis.TaxonomyCode { return nil }
func (graphTopologyOp) HandlesSignal() Signal             { return SignalLatencyBottleneck }

// AdmissiblePatterns is pattern-agnostic: whether two calls can overlap is a property of the DATA
// DEPENDENCY between them, which the edges carry, not of the label the classifier gave either node.
func (graphTopologyOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

// Propose emits at most ONE candidate: the flagged node declared concurrent with the sibling it already
// runs independently of.
//
// # The eligibility rule, and why it is this narrow
//
// A pair is eligible when BOTH hold:
//
//  1. Neither has an edge to the other, in either direction, DIRECTLY OR TRANSITIVELY. An edge is a data
//     or control dependency, and running a consumer beside its producer is not a speed-up, it is a race.
//  2. They share a predecessor. Two calls that are independent but unrelated may be far apart in the
//     order for reasons the spec does not record; a shared predecessor is the evidence that they are a
//     fan-out — the shape concurrency is actually for.
//
// 🚫 It does NOT declare a merge, and that is the important omission. A merge is a semantic choice about
// how the author's results combine (design D6), so proposing one would be the platform deciding what the
// customer's code means. The operator therefore only proposes concurrency for a pair that does NOT fan
// in — a pair that converges needs a merge, and that declaration has to be authored.
//
// 🔴 The candidate is a PROPOSAL. Concurrency's benefit is wall-clock and its cost is peak resource use;
// only verification on held-out data decides whether the trade was worth it.
func (op graphTopologyOp) Propose(in OperatorInput) ([]Candidate, error) {
	if in.Base == nil {
		return nil, nil
	}
	node := in.NodeID()
	if indexOf(in.Base.Order, node) < 0 {
		return nil, nil // the signal names a node this spec does not order
	}
	// Already in a group: declaring it in a second one would hand the executor two statements about the
	// same node, and nothing says which applies.
	for _, g := range in.Base.GraphGroups {
		for _, m := range g.Nodes {
			if m == node {
				return nil, nil
			}
		}
	}
	sibling := concurrencySibling(in.Base, node)
	if sibling == "" {
		return nil, nil
	}
	spec := cloneSpec(in.Base)
	spec.ParentVariantID = parentVariantID(in)
	// Declared in ORDER order, so the same base + signal always produces the same spec and the same
	// config_hash — the determinism every operator owes (task 3.4).
	pair := []string{node, sibling}
	if indexOf(in.Base.Order, sibling) < indexOf(in.Base.Order, node) {
		pair = []string{sibling, node}
	}
	spec.GraphGroups = append(append([]variantspec.GraphGroup(nil), in.Base.GraphGroups...),
		variantspec.GraphGroup{Nodes: pair, Concurrent: true})

	c := newCandidate(op.Kind(), in, node, []string{graphAxisLabel}, spec,
		fmt.Sprintf("%s → %s and %s share a predecessor and neither depends on the other, so declare them "+
			"concurrent. Every call still happens and the node set is unchanged; only WHEN they run "+
			"changes. It multiplies this run's peak resource use by the group's width, and whether the "+
			"wall-clock it buys is worth that is verification's answer on held-out data, not this "+
			"proposal's.", SignalLatencyBottleneck, pair[0], pair[1]))
	return []Candidate{c}, nil
}

// graphAxisLabel is the axis a topology candidate records. A plain string, like the wiring axis's
// "order", because topology is not a `Dimension` — design D3 keeps it spec-level, since every Dimension
// is a property of ONE node and topology is a property BETWEEN nodes.
const graphAxisLabel = "graph"

// concurrencySibling names the node this one may be declared concurrent with, or "".
//
// Deterministic in both directions: it returns the FIRST eligible sibling in the spec's order, so the
// same base and signal always name the same pair.
func concurrencySibling(s *variantspec.VariantSpec, node string) string {
	preds := predecessorsOf(s.Edges, node)
	if len(preds) == 0 {
		return "" // no shared predecessor is possible
	}
	reach := reachability(s)
	for _, cand := range s.Order {
		if cand == node {
			continue
		}
		// (1) independence, transitively. A path in either direction is a dependency, and running a
		// consumer beside its producer is a race rather than a speed-up.
		if reach[node][cand] || reach[cand][node] {
			continue
		}
		// (2) a shared predecessor — the evidence that this is a fan-out rather than two unrelated calls.
		if !sharesAny(preds, predecessorsOf(s.Edges, cand)) {
			continue
		}
		// 🚫 And they must not already converge: a pair that fans in needs a MERGE, which is a semantic
		// choice about the author's program and therefore not the platform's to propose.
		if sharesAny(successorsOf(s.Edges, node), successorsOf(s.Edges, cand)) {
			continue
		}
		return cand
	}
	return ""
}

// reachability is the transitive closure over the spec's edges: reach[a][b] means a path a → … → b.
//
// 🔴 Transitive, not direct. Two nodes with no edge between them can still be ordered by a chain, and
// declaring those concurrent would race a consumer against a producer two hops away — the failure that
// a direct-edge check misses and that only shows up under load.
func reachability(s *variantspec.VariantSpec) map[string]map[string]bool {
	reach := map[string]map[string]bool{}
	for _, n := range s.Order {
		reach[n] = map[string]bool{}
	}
	for _, e := range s.Edges {
		if reach[e.FromNodeID] != nil {
			reach[e.FromNodeID][e.ToNodeID] = true
		}
	}
	// Floyd–Warshall over the node set. The graphs here are a workflow's call sites, so the cubic cost
	// is over tens of nodes; a cleverer traversal would be less obviously correct for no measurable gain.
	for _, k := range s.Order {
		for _, i := range s.Order {
			if !reach[i][k] {
				continue
			}
			for _, j := range s.Order {
				if reach[k][j] {
					reach[i][j] = true
				}
			}
		}
	}
	return reach
}

func predecessorsOf(edges []variantspec.Edge, node string) []string {
	var out []string
	for _, e := range edges {
		if e.ToNodeID == node {
			out = append(out, e.FromNodeID)
		}
	}
	sort.Strings(out)
	return out
}

func successorsOf(edges []variantspec.Edge, node string) []string {
	var out []string
	for _, e := range edges {
		if e.FromNodeID == node {
			out = append(out, e.ToNodeID)
		}
	}
	sort.Strings(out)
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
