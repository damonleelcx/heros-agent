package evalgen

import (
	"context"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// evidence.go turns a real executed run into coverage evidence.
//
// This is the production path and it reads the SAME P2.5 spans the evaluators read: the nodes a run
// entered, in order, are the nodes it covered; the consecutive pairs that correspond to real IR
// edges are the edges it traversed; a node appearing more than once is a loop and its repeat count
// is its iteration count. Nothing here is inferred from what a case CLAIMED it would do.

// Prober executes one case and reports what it exercised. The gap-filling loop calls it after every
// generation round, because coverage is measured from execution and a case that was generated but
// never run has covered nothing.
type Prober interface {
	Probe(ctx context.Context, ir *discovery.IR, c evalharness.Case) (Evidence, error)
}

// TraceEvidence derives coverage evidence from a completed run's telemetry trace.
//
// Consecutive node pairs are admitted as edges only when the IR actually declares that edge. A run
// that jumps A -> C in a graph whose only edges are A -> B -> C did not traverse an A -> C edge, and
// crediting it would mark a nonexistent path covered.
func TraceEvidence(ir *discovery.IR, caseID string, tr telemetry.Trace) Evidence {
	ev := Evidence{CaseID: caseID, LoopIterations: map[string]int{}}
	if ir == nil {
		return ev
	}
	declared := map[string]bool{}
	for _, e := range ir.Edges {
		declared[EdgeID(e.FromNodeID, e.ToNodeID)] = true
	}

	var order []string
	seenNode := map[string]bool{}
	for _, sp := range tr.NodeSpans() {
		id, _ := sp.Attributes[telemetry.AttrNodeID].(string)
		if id == "" {
			continue
		}
		order = append(order, id)
		ev.LoopIterations[id]++
		if !seenNode[id] {
			seenNode[id] = true
			ev.Nodes = append(ev.Nodes, id)
		}
	}
	seenEdge := map[string]bool{}
	for i := 1; i < len(order); i++ {
		id := EdgeID(order[i-1], order[i])
		if !declared[id] || seenEdge[id] {
			continue
		}
		seenEdge[id] = true
		ev.Edges = append(ev.Edges, id)
	}
	// A self-edge is traversed whenever the node executed more than once.
	for node, n := range ev.LoopIterations {
		id := EdgeID(node, node)
		if n > 1 && declared[id] && !seenEdge[id] {
			seenEdge[id] = true
			ev.Edges = append(ev.Edges, id)
		}
	}
	return ev
}

// ProbeAll runs the prober over every case and collects the evidence, keyed by case id.
//
// A case whose probe errors contributes NO evidence rather than partial evidence: a half-observed
// run would mark the nodes it reached as covered while hiding that it never finished, which is the
// difference between "this path is exercised" and "this path is exercised up to the point it
// breaks".
func ProbeAll(ctx context.Context, p Prober, ir *discovery.IR, cases []evalharness.Case) (map[string]Evidence, []error) {
	out := map[string]Evidence{}
	var errs []error
	for _, c := range cases {
		ev, err := p.Probe(ctx, ir, c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ev.CaseID = c.CaseID
		out[c.CaseID] = ev
	}
	return out, errs
}
