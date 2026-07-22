// Package evalgen is P4 Decision 4: "enough" is defined by a MEASURED coverage report, not by a
// case count.
//
// LLM-synthesized eval sets inherit their generator's blind spots and can be trivially easy; a
// passing score on a weak set is worthless. Measuring coverage against the IR graph turns "is this
// eval set good enough?" from a vibe into a checkable predicate, and the generator becomes a
// gap-filling loop — measure, target the gap, regenerate — rather than "generate 500 cases and
// hope". The layered generators escalate in cost and specificity so the expensive LLM is pointed
// only at the residual the cheap deterministic layers could not reach.
package evalgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// Coverage dimension names. Constants because the report, the thresholds, the UI and the tests all
// key off them, and a typo in one of four places is a dimension that silently never gates.
const (
	DimensionPath     = "path"
	DimensionNode     = "node"
	DimensionEdgeCase = "edge_case"
)

// Loop-iteration bounds a loop node must be exercised at (spec: "loops driven to min, typical, and
// max iterations"). Tracked separately because a loop that only ever runs once has not been tested
// as a loop at all.
const (
	LoopMin     = "min"
	LoopTypical = "typical"
	LoopMax     = "max"
)

// DefaultLoopBounds is what min/typical/max mean in iteration counts when the IR does not say. They
// are values, not literals at a call site, so changing what "typical" means is one visible edit.
var DefaultLoopBounds = map[string]int{LoopMin: 1, LoopTypical: 2, LoopMax: 5}

// Item is one coverage obligation and whether the eval set discharges it.
type Item struct {
	// ID is the obligation's stable identifier: an edge as "from->to", a branch outcome as
	// "router?outcome", a loop bound as "node@max", a node as its node_id, an edge case as its kind.
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Covered bool     `json:"covered"`
	Cases   []string `json:"cases,omitempty"`
	// Unreachable marks an obligation the generator has concluded no input can satisfy. It is
	// reported as a RESIDUAL, never silently dropped from the denominator — dropping it is how a
	// report claims 100% while a branch has never once executed.
	Unreachable bool `json:"unreachable,omitempty"`
}

// Dimension is one coverage axis: achieved vs. target, with every obligation enumerated.
type Dimension struct {
	Name     string  `json:"name"`
	Target   float64 `json:"target"`
	Achieved float64 `json:"achieved"`
	Items    []Item  `json:"items"`
	// Vacuous is true when the dimension has NO obligations at all. It is not "100% covered" — it
	// is "nothing was measured", and the two must never render the same.
	//
	// Found by running against a real repo: P1 discovery over nousresearch/hermes-agent emits 40
	// call sites and ZERO edges (inter-node flow is P5's dynamic tracing, not P1's static pass).
	// With no edges there are no path obligations, and an empty-set ratio of 1.0 reported "path
	// coverage 100%" for a workflow whose control flow had never been observed at all — the precise
	// false-100% this package exists to prevent, arrived at from the other direction.
	Vacuous bool `json:"vacuous,omitempty"`
}

// Met reports whether the dimension reached its target. A VACUOUS dimension is never met: a target
// cannot be satisfied by having nothing to satisfy it with.
func (d Dimension) Met() bool {
	if d.Vacuous {
		return d.Target <= 0
	}
	return d.Achieved >= d.Target
}

// Uncovered returns the obligation ids not yet discharged, in stable order. This is what the
// LLM-driven generator is POINTED AT — it is handed the specific uncovered paths, not the whole
// input space.
func (d Dimension) Uncovered() []string {
	var out []string
	for _, it := range d.Items {
		if !it.Covered {
			out = append(out, it.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Residual returns the uncovered obligations concluded unreachable.
func (d Dimension) Residual() []string {
	var out []string
	for _, it := range d.Items {
		if !it.Covered && it.Unreachable {
			out = append(out, it.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Thresholds are the per-dimension coverage targets. Path defaults to 1.0 (every IR edge) because
// an edge that never executes is a branch the leaderboard is ranking blind.
type Thresholds struct {
	Path     float64 `json:"path"`
	Node     float64 `json:"node"`
	EdgeCase float64 `json:"edge_case"`
}

// DefaultThresholds is 100% path, 100% node, 100% edge-case taxonomy.
func DefaultThresholds() Thresholds { return Thresholds{Path: 1, Node: 1, EdgeCase: 1} }

// CoverageReport is the answer to "is this eval set good enough?".
type CoverageReport struct {
	WorkflowID string    `json:"workflow_id"`
	NCases     int       `json:"n_cases"`
	Path       Dimension `json:"path"`
	Node       Dimension `json:"node"`
	EdgeCase   Dimension `json:"edge_case"`
	// Iterations is how many gap-filling rounds produced this set.
	Iterations int `json:"iterations"`
	// Residual is every obligation still uncovered when the loop stopped. A non-empty residual with
	// Met == false is the honest answer; a report that claimed 100% here would be the single most
	// damaging lie this package could tell.
	Residual []string `json:"residual,omitempty"`
}

// Met reports whether every dimension reached its threshold.
func (r CoverageReport) Met() bool { return r.Path.Met() && r.Node.Met() && r.EdgeCase.Met() }

// Vacuous names the dimensions that had no obligations to measure. A caller that sees a non-empty
// list here is looking at a report that cannot answer "is this eval set good enough?" for those
// axes — usually because the IR carries no edges, which means the workflow's control flow has not
// been observed yet (P1 static discovery finds call sites; inter-node flow arrives with P5).
func (r CoverageReport) Vacuous() []string {
	var out []string
	for _, d := range r.Dimensions() {
		if d.Vacuous {
			out = append(out, d.Name)
		}
	}
	return out
}

// Dimensions returns the three axes in report order.
func (r CoverageReport) Dimensions() []Dimension { return []Dimension{r.Path, r.Node, r.EdgeCase} }

// Evidence is what ONE case actually exercised, observed from its execution. Coverage is measured
// from evidence and never from a case's declared PathTags: a case's tags are what the generator was
// AIMING at, and scoring coverage off intent would let a generator mark a branch covered by writing
// a label on a case that never reaches it.
type Evidence struct {
	CaseID string `json:"case_id"`
	// Nodes are the node ids the run actually entered, in execution order.
	Nodes []string `json:"nodes"`
	// Edges are the IR edges traversed, as "from->to".
	Edges []string `json:"edges"`
	// LoopIterations is, per node, how many times it executed in this run.
	LoopIterations map[string]int `json:"loop_iterations,omitempty"`
}

// EdgeID renders an IR edge's coverage id.
func EdgeID(from, to string) string { return from + "->" + to }

// BranchID renders a router branch-outcome coverage id.
func BranchID(router, outcome string) string { return router + "?" + outcome }

// LoopBoundID renders a loop-iteration-bound coverage id.
func LoopBoundID(node, bound string) string { return node + "@" + bound }

// Measure computes achieved coverage of an eval set against the IR, from execution evidence.
//
// Every obligation the IR implies is enumerated FIRST, then marked covered by the evidence. Building
// the report the other way round — listing what the evidence touched — would make an eval set that
// exercises one branch look like 100% coverage of one branch.
func Measure(ir *discovery.IR, cases []evalharness.Case, evidence map[string]Evidence, th Thresholds) CoverageReport {
	rep := CoverageReport{NCases: len(cases)}
	if ir == nil {
		return rep
	}
	rep.WorkflowID = ir.Workflow.ID

	rep.Path = measurePath(ir, evidence, th.Path)
	rep.Node = measureNode(ir, evidence, th.Node)
	rep.EdgeCase = measureEdgeCase(cases, th.EdgeCase)
	rep.Residual = append(append(rep.Path.Uncovered(), rep.Node.Uncovered()...), rep.EdgeCase.Uncovered()...)
	sort.Strings(rep.Residual)
	return rep
}

// measurePath enumerates every IR edge, every router branch outcome, and every loop node's
// min/typical/max iteration bound.
func measurePath(ir *discovery.IR, evidence map[string]Evidence, target float64) Dimension {
	d := Dimension{Name: DimensionPath, Target: target}
	covered := map[string][]string{}

	for caseID, ev := range evidence {
		for _, e := range ev.Edges {
			covered[e] = append(covered[e], caseID)
		}
		for node, n := range ev.LoopIterations {
			for bound, want := range DefaultLoopBounds {
				if boundSatisfied(bound, n, want) {
					covered[LoopBoundID(node, bound)] = append(covered[LoopBoundID(node, bound)], caseID)
				}
			}
		}
	}

	// Every IR edge.
	for _, e := range ir.Edges {
		id := EdgeID(e.FromNodeID, e.ToNodeID)
		d.Items = append(d.Items, item(id, "edge", covered))
	}
	// Every branch outcome of every node with more than one outgoing control edge — that node is a
	// router whether or not it carries the Routing label, and each of its outcomes is an obligation.
	for _, router := range routers(ir) {
		for _, out := range router.outcomes {
			id := BranchID(router.nodeID, out)
			// A branch outcome is covered by the edge that realizes it.
			it := item(id, "branch_outcome", covered)
			if !it.Covered {
				if cs, ok := covered[EdgeID(router.nodeID, out)]; ok {
					it.Covered, it.Cases = true, dedupeStrings(cs)
				}
			}
			d.Items = append(d.Items, it)
		}
	}
	// Every loop node at min / typical / max.
	for _, node := range loopNodes(ir) {
		for _, bound := range []string{LoopMin, LoopTypical, LoopMax} {
			d.Items = append(d.Items, item(LoopBoundID(node, bound), "loop_bound", covered))
		}
	}

	sortItems(d.Items)
	d.Achieved, d.Vacuous = ratio(d.Items)
	return d
}

// boundSatisfied reports whether an observed iteration count discharges a bound. `max` is satisfied
// by reaching OR exceeding the bound (a loop driven past its typical count has been tested at its
// ceiling); min and typical must be hit exactly, because "at least 1" would let a single-iteration
// run claim the typical bound too.
func boundSatisfied(bound string, observed, want int) bool {
	if bound == LoopMax {
		return observed >= want
	}
	return observed == want
}

func measureNode(ir *discovery.IR, evidence map[string]Evidence, target float64) Dimension {
	d := Dimension{Name: DimensionNode, Target: target}
	covered := map[string][]string{}
	for caseID, ev := range evidence {
		for _, n := range ev.Nodes {
			covered[n] = append(covered[n], caseID)
		}
	}
	for _, n := range ir.Nodes {
		d.Items = append(d.Items, item(n.NodeID, "node", covered))
	}
	sortItems(d.Items)
	d.Achieved, d.Vacuous = ratio(d.Items)
	return d
}

// measureEdgeCase walks the CLOSED failure taxonomy. It is measured from the cases' declared kinds
// rather than from evidence because "this case carries a malformed input" is a property of the case
// itself, not of what the run did with it.
func measureEdgeCase(cases []evalharness.Case, target float64) Dimension {
	d := Dimension{Name: DimensionEdgeCase, Target: target}
	covered := map[string][]string{}
	for _, c := range cases {
		if c.EdgeCase == evalharness.EdgeCaseNone {
			continue
		}
		covered[string(c.EdgeCase)] = append(covered[string(c.EdgeCase)], c.CaseID)
	}
	for _, k := range evalharness.EdgeCaseKinds {
		d.Items = append(d.Items, item(string(k), "edge_case", covered))
	}
	sortItems(d.Items)
	d.Achieved, d.Vacuous = ratio(d.Items)
	return d
}

func item(id, kind string, covered map[string][]string) Item {
	cs, ok := covered[id]
	return Item{ID: id, Kind: kind, Covered: ok && len(cs) > 0, Cases: dedupeStrings(cs)}
}

// ratio returns the covered fraction and whether the dimension is VACUOUS (no obligations).
// The two are returned together so no caller can read the fraction without the caveat: an empty set
// has a covered fraction of 1.0 by arithmetic, and reporting that alone is a lie.
func ratio(items []Item) (achieved float64, vacuous bool) {
	if len(items) == 0 {
		return 0, true
	}
	n := 0
	for _, it := range items {
		if it.Covered {
			n++
		}
	}
	return float64(n) / float64(len(items)), false
}

func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// routerInfo is a node with more than one outgoing control edge, plus its outcomes.
type routerInfo struct {
	nodeID   string
	outcomes []string
}

func routers(ir *discovery.IR) []routerInfo {
	out := map[string][]string{}
	for _, e := range ir.Edges {
		if e.Kind != "control" {
			continue
		}
		if e.FromNodeID == e.ToNodeID {
			continue // a self-edge is a loop, not a branch
		}
		out[e.FromNodeID] = append(out[e.FromNodeID], e.ToNodeID)
	}
	var infos []routerInfo
	for node, outs := range out {
		if len(outs) < 2 {
			continue
		}
		sort.Strings(outs)
		infos = append(infos, routerInfo{nodeID: node, outcomes: outs})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].nodeID < infos[j].nodeID })
	return infos
}

// loopNodes are nodes with a self-edge, or that sit on a back-edge in the IR. A self-edge is the
// shape the IR uses for an iterating node (Reflection), so it is the shape coverage tracks.
func loopNodes(ir *discovery.IR) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range ir.Edges {
		if e.FromNodeID == e.ToNodeID && !seen[e.FromNodeID] {
			seen[e.FromNodeID] = true
			out = append(out, e.FromNodeID)
		}
	}
	sort.Strings(out)
	return out
}

// Gap is the set of obligations still uncovered, handed to the generators. It is a STRUCTURED gap,
// not a count: "one branch outcome uncovered: router?branch_b" is what lets the LLM generator target
// the residual instead of sampling the whole input space and hoping.
type Gap struct {
	Paths     []string `json:"paths"`
	Nodes     []string `json:"nodes"`
	EdgeCases []string `json:"edge_cases"`
}

// Empty reports whether there is nothing left to target.
func (g Gap) Empty() bool { return len(g.Paths)+len(g.Nodes)+len(g.EdgeCases) == 0 }

// String renders the gap for a prompt or a log.
func (g Gap) String() string {
	var parts []string
	if len(g.Paths) > 0 {
		parts = append(parts, "paths: "+strings.Join(g.Paths, ", "))
	}
	if len(g.Nodes) > 0 {
		parts = append(parts, "nodes: "+strings.Join(g.Nodes, ", "))
	}
	if len(g.EdgeCases) > 0 {
		parts = append(parts, "edge cases: "+strings.Join(g.EdgeCases, ", "))
	}
	if len(parts) == 0 {
		return "no gap"
	}
	return strings.Join(parts, "; ")
}

// GapOf extracts the uncovered obligations from a report.
func GapOf(rep CoverageReport) Gap {
	return Gap{
		Paths:     rep.Path.Uncovered(),
		Nodes:     rep.Node.Uncovered(),
		EdgeCases: rep.EdgeCase.Uncovered(),
	}
}

// ParseEdgeID splits an edge coverage id back into its endpoints.
func ParseEdgeID(id string) (from, to string, ok bool) {
	i := strings.Index(id, "->")
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+2:], true
}

// ParseBranchID splits a branch coverage id back into its router and outcome.
func ParseBranchID(id string) (router, outcome string, ok bool) {
	i := strings.Index(id, "?")
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// ParseLoopBoundID splits a loop-bound coverage id.
func ParseLoopBoundID(id string) (node, bound string, ok bool) {
	i := strings.LastIndex(id, "@")
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// Summary renders a one-line human read of the report, used in logs and the demo driver.
func (r CoverageReport) Summary() string {
	if v := r.Vacuous(); len(v) > 0 {
		return fmt.Sprintf("NOT MEASURABLE on %s (no obligations to cover) · %s",
			strings.Join(v, "+"), r.measuredSummary())
	}
	return r.measuredSummary()
}

func (r CoverageReport) measuredSummary() string {
	return fmt.Sprintf("path %.0f%%/%.0f%% node %.0f%%/%.0f%% edge %.0f%%/%.0f%% (%d cases, %d iterations, %d residual)",
		r.Path.Achieved*100, r.Path.Target*100,
		r.Node.Achieved*100, r.Node.Target*100,
		r.EdgeCase.Achieved*100, r.EdgeCase.Target*100,
		r.NCases, r.Iterations, len(r.Residual))
}
