package evalgen

import (
	"context"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// Task 4.1 — the coverage report enumerates every IR edge, every branch outcome, and every loop
// bound, and states per obligation whether it is covered.
func TestCoverageReportEnumeratesUncoveredPaths(t *testing.T) {
	ir := fxRouterIR()
	p := newSimProber("branch_a", "branch_b")

	// One case, routed to branch_a, one loop turn: branch_b and the loop's typical/max bounds are
	// left explicitly uncovered.
	cases := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	ev, _ := ProbeAll(context.Background(), p, ir, cases)
	rep := Measure(ir, cases, ev, DefaultThresholds())

	if rep.Path.Met() {
		t.Fatal("one case cannot cover both branches and all three loop bounds")
	}
	unc := rep.Path.Uncovered()
	if !containsAll(unc, EdgeID("router", "branch_b"), BranchID("router", "branch_b")) {
		t.Fatalf("the unexercised branch outcome must be listed explicitly, got %s", joined(unc))
	}
	if !containsAll(unc, LoopBoundID("reflect", LoopTypical), LoopBoundID("reflect", LoopMax)) {
		t.Fatalf("loop min/typical/max must be tracked separately, got %s", joined(unc))
	}
	// The bound that WAS exercised is covered.
	if containsAll(unc, LoopBoundID("reflect", LoopMin)) {
		t.Fatalf("the loop's min bound was exercised and must be covered, got %s", joined(unc))
	}
	// Node coverage sees the three nodes actually entered, not the fourth.
	if !containsAll(rep.Node.Uncovered(), "branch_b") {
		t.Fatalf("an unreached node must be listed uncovered, got %s", joined(rep.Node.Uncovered()))
	}
}

// Coverage is measured from EXECUTION, never from a case's declared PathTags. A case that claims to
// exercise a branch it never reaches must not mark it covered.
func TestCoverageIgnoresDeclaredIntent(t *testing.T) {
	ir := fxRouterIR()
	p := newSimProber("branch_a") // branch_b is not reachable in this simulation

	c := handCase("liar", "branch_b", "hello", 1)
	c.PathTags = []string{EdgeID("router", "branch_b"), BranchID("router", "branch_b")}
	cases := []evalharness.Case{c}

	ev, _ := ProbeAll(context.Background(), p, ir, cases)
	rep := Measure(ir, cases, ev, DefaultThresholds())

	if !containsAll(rep.Path.Uncovered(), EdgeID("router", "branch_b")) {
		t.Fatal("a case's declared path tags must not be able to mark a path covered")
	}
}

// The edge-case dimension walks the CLOSED taxonomy: every slot is an obligation whether or not any
// case happens to carry it.
func TestEdgeCaseDimensionWalksTheClosedTaxonomy(t *testing.T) {
	ir := fxRouterIR()
	cases := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	rep := Measure(ir, cases, map[string]Evidence{}, DefaultThresholds())

	if len(rep.EdgeCase.Items) != len(evalharness.EdgeCaseKinds) {
		t.Fatalf("want one obligation per taxonomy slot (%d), got %d",
			len(evalharness.EdgeCaseKinds), len(rep.EdgeCase.Items))
	}
	if rep.EdgeCase.Achieved != 0 {
		t.Fatalf("a set with no edge cases achieves 0, got %v", rep.EdgeCase.Achieved)
	}
}

// TraceEvidence admits only edges the IR actually declares: a run that jumps A -> C in an
// A -> B -> C graph did not traverse an A -> C edge.
func TestTraceEvidenceRefusesUndeclaredEdges(t *testing.T) {
	ir := fxRouterIR()
	b := newTraceBuilder("run-1")
	b.node("router")
	b.node("reflect") // router -> reflect is NOT an IR edge

	ev := TraceEvidence(ir, "c1", b.build())
	for _, e := range ev.Edges {
		if e == EdgeID("router", "reflect") {
			t.Fatal("an edge the IR does not declare must not be credited as traversed")
		}
	}
	if !containsAll(ev.Nodes, "router", "reflect") {
		t.Fatalf("both nodes were entered and must be covered, got %s", joined(ev.Nodes))
	}
}

// A repeated node is a loop, and its repeat count is its iteration count.
func TestTraceEvidenceCountsLoopIterations(t *testing.T) {
	ir := fxRouterIR()
	b := newTraceBuilder("run-1")
	b.node("router")
	b.node("branch_a")
	for i := 0; i < 5; i++ {
		b.node("reflect")
	}
	ev := TraceEvidence(ir, "c1", b.build())
	if ev.LoopIterations["reflect"] != 5 {
		t.Fatalf("want 5 reflect iterations, got %d", ev.LoopIterations["reflect"])
	}
	if !containsAll(ev.Edges, EdgeID("reflect", "reflect")) {
		t.Fatal("a node that executed more than once traverses its declared self-edge")
	}
}

// A probe error contributes NO evidence: half-observed coverage would mark the nodes a broken run
// reached as covered while hiding that it never finished.
func TestProbeErrorContributesNoEvidence(t *testing.T) {
	ir := fxRouterIR()
	cases := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	ev, errs := ProbeAll(context.Background(), failingProber{}, ir, cases)
	if len(errs) != 1 {
		t.Fatalf("want the probe error surfaced, got %v", errs)
	}
	if len(ev) != 0 {
		t.Fatalf("a failed probe must contribute no evidence, got %v", ev)
	}
}

type failingProber struct{}

func (failingProber) Probe(context.Context, *discovery.IR, evalharness.Case) (Evidence, error) {
	return Evidence{}, errors.New("the run never completed")
}

// REGRESSION FENCE, found by running against a real repository.
//
// P1 static discovery over nousresearch/hermes-agent emits 40 LLM call sites and ZERO edges —
// inter-node flow is P5's dynamic tracing, not P1's static pass. With no edges there are no path
// obligations, and an empty-set covered-fraction of 1.0 reported "path coverage 100%" for a workflow
// whose control flow had never been observed at all. That is the same false-100% the unreachable-path
// test guards, reached from the opposite direction: not by dropping obligations, but by never having
// any.
func TestDimensionWithNoObligationsIsNotMeasurableRatherThan100Percent(t *testing.T) {
	// An IR shaped like real static discovery output: nodes, no edges.
	ir := fxRouterIR()
	ir.Edges = nil

	cases := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	ev, _ := ProbeAll(context.Background(), newSimProber("branch_a"), ir, cases)
	rep := Measure(ir, cases, ev, DefaultThresholds())

	if !rep.Path.Vacuous {
		t.Fatal("a dimension with no obligations must be flagged vacuous")
	}
	if rep.Path.Achieved != 0 {
		t.Fatalf("a vacuous dimension has an achieved fraction of 0, not 1; got %v", rep.Path.Achieved)
	}
	if rep.Path.Met() {
		t.Fatal("a 100% target cannot be met by having nothing to cover")
	}
	if rep.Met() {
		t.Fatal("the whole report must not read as met when an axis was never measurable")
	}
	if !containsAll(rep.Vacuous(), DimensionPath) {
		t.Fatalf("the vacuous axis must be named, got %v", rep.Vacuous())
	}
	if !containsSubstr(rep.Summary(), "NOT MEASURABLE") {
		t.Fatalf("the summary must say so out loud, got %q", rep.Summary())
	}

	// Node coverage still works — it has obligations.
	if rep.Node.Vacuous {
		t.Fatal("node coverage has obligations and must not be vacuous")
	}
}

func containsSubstr(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
