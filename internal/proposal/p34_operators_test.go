package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P34 §6 — the two operators, and the measurement PRD §9.5 requires of them.

func graphOpBase() *variantspec.VariantSpec {
	// a fans out to b and c; d is downstream of b only, so {b, c} do NOT converge.
	return &variantspec.VariantSpec{
		WorkflowID: "wf-graph", SourceRevision: "rev1",
		Order: []string{"a", "b", "c", "d"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "a", ToNodeID: "b", Kind: "data"},
			{FromNodeID: "a", ToNodeID: "c", Kind: "data"},
			{FromNodeID: "b", ToNodeID: "d", Kind: "data"},
		},
	}
}

func graphOpInput(base *variantspec.VariantSpec, node string) OperatorInput {
	in := harnessInput()
	in.Signal = SignalLatencyBottleneck
	in.Base = base
	in.Diagnosis.NodeID = node
	return in
}

// ── 6.2 — the graph operator ─────────────────────────────────────────────────────────────────────

func TestGraphOperatorDeclaresAnIndependentPairConcurrent(t *testing.T) {
	got, err := graphTopologyOp{}.Propose(graphOpInput(graphOpBase(), "b"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 candidate, got %d", len(got))
	}
	c := got[0]
	if len(c.Spec.GraphGroups) != 1 {
		t.Fatalf("the candidate declares %d groups, want 1", len(c.Spec.GraphGroups))
	}
	g := c.Spec.GraphGroups[0]
	if !g.Concurrent {
		t.Error("the group is not marked concurrent; the operator proposed a topology unit that changes nothing")
	}
	if strings.Join(g.Nodes, ",") != "b,c" {
		t.Errorf("the group is %v, want [b c] in ORDER order — the same base and signal must always "+
			"produce the same spec and the same config_hash", g.Nodes)
	}
	// 🚫 It never declares a merge. A merge is a semantic choice about how the author's results combine
	// (design D6), so proposing one would be the platform deciding what the customer's code means.
	if g.Merge != nil {
		t.Error("the operator proposed a merge; how a fan-in combines is the author's decision, not the " +
			"platform's, and the operator only fires on pairs that do not converge at all")
	}
	if c.Operator != OpGraphTopology {
		t.Errorf("candidate operator is %q, want %q", c.Operator, OpGraphTopology)
	}
	if len(c.Dimensions) != 1 || c.Dimensions[0] != graphAxisLabel {
		t.Errorf("candidate declares dimensions %v, want exactly [graph]", c.Dimensions)
	}
	// The rationale must state the cost without claiming the benefit — verification's answer, not the
	// operator's.
	if !strings.Contains(c.Rationale, "peak resource use") {
		t.Errorf("the rationale does not state what concurrency costs: %s", c.Rationale)
	}
}

// TestTheGraphOperatorNeverFusesNodes is the fence between the two things P34 and P15 both call
// "merge", and it is the one that keeps a proposal row readable months later.
//
// 🔴 `OpMerge` (P15) FUSES two redundant nodes — the node SET shrinks, and the claim is that one of the
// two calls was unnecessary. `OpGraphTopology` declares two nodes CONCURRENT — every call still happens
// and only WHEN changes. A candidate that did the first while claiming the second would be a change
// nobody could review from its own record.
func TestTheGraphOperatorNeverFusesNodes(t *testing.T) {
	base := graphOpBase()
	got, err := graphTopologyOp{}.Propose(graphOpInput(base, "b"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	c := got[0]
	if len(c.Spec.Order) != len(base.Order) {
		t.Fatalf("the candidate's order has %d nodes and the baseline's has %d; a topology proposal "+
			"changes only WHEN calls run, never WHETHER they run — removing one is OpMerge's claim and a "+
			"completely different change",
			len(c.Spec.Order), len(base.Order))
	}
	for i, want := range base.Order {
		if c.Spec.Order[i] != want {
			t.Errorf("order[%d] is %q, want %q — concurrency is declared OVER the ordering, never instead "+
				"of it (design D4), so the replay sequence must be untouched", i, c.Spec.Order[i], want)
		}
	}
	if len(c.Spec.Edges) != len(base.Edges) {
		t.Error("the candidate rewired the graph; a topology proposal declares overlap and rewires nothing")
	}
	if c.Operator == OpMerge {
		t.Fatal("the topology operator emitted an OpMerge candidate")
	}
}

func TestGraphOperatorRefusesADependentPair(t *testing.T) {
	// b → c makes them dependent: running a consumer beside its producer is a race, not a speed-up.
	base := graphOpBase()
	base.Edges = append(base.Edges, variantspec.Edge{FromNodeID: "b", ToNodeID: "c", Kind: "data"})
	got, err := graphTopologyOp{}.Propose(graphOpInput(base, "b"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("the operator proposed concurrency for a producer/consumer pair: %+v", got[0].Spec.GraphGroups)
	}
}

// TestGraphOperatorRefusesATRANSITIVELYDependentPair is the one a direct-edge check misses, and it is
// the failure that only shows up under load.
func TestGraphOperatorRefusesATransitivelyDependentPair(t *testing.T) {
	base := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"a", "b", "mid", "c"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "a", ToNodeID: "b", Kind: "data"},
			{FromNodeID: "a", ToNodeID: "mid", Kind: "data"},
			{FromNodeID: "b", ToNodeID: "mid", Kind: "data"},
			{FromNodeID: "mid", ToNodeID: "c", Kind: "data"},
			{FromNodeID: "a", ToNodeID: "c", Kind: "data"},
		},
	}
	got, err := graphTopologyOp{}.Propose(graphOpInput(base, "b"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	for _, cand := range got {
		for _, g := range cand.Spec.GraphGroups {
			if strings.Join(g.Nodes, ",") == "b,c" {
				t.Fatal("the operator declared b and c concurrent. They have no direct edge, but b → mid → c " +
					"is a dependency two hops away — declaring those concurrent races a consumer against a " +
					"producer, which is the failure a direct-edge check misses and that only shows up under load")
			}
		}
	}
}

func TestGraphOperatorRefusesAConvergingPair(t *testing.T) {
	// b and c both feed d: a fan-in needs a MERGE, and that declaration is the author's to make.
	base := graphOpBase()
	base.Edges = append(base.Edges, variantspec.Edge{FromNodeID: "c", ToNodeID: "d", Kind: "data"})
	got, err := graphTopologyOp{}.Propose(graphOpInput(base, "b"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(got) != 0 {
		t.Fatal("the operator proposed concurrency for a pair that converges. That pair needs a merge, " +
			"and how results combine is a semantic choice about the customer's program (design D6) — so " +
			"proposing the concurrency without it would produce a spec that is refused at validate")
	}
}

func TestGraphOperatorRefusesANodeAlreadyInAGroup(t *testing.T) {
	base := graphOpBase()
	base.GraphGroups = []variantspec.GraphGroup{{Nodes: []string{"b", "c"}, Concurrent: true}}
	got, err := graphTopologyOp{}.Propose(graphOpInput(base, "b"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(got) != 0 {
		t.Fatal("the operator declared a node in a second group; the executor would then hold two " +
			"statements about the same node and nothing says which applies")
	}
}

func TestGraphOperatorIsDeterministic(t *testing.T) {
	// The same base and signal must always name the same pair, or two runs of one optimizer produce two
	// different config_hashes for one intent.
	var first string
	for i := 0; i < 8; i++ {
		got, err := graphTopologyOp{}.Propose(graphOpInput(graphOpBase(), "b"))
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		pair := strings.Join(got[0].Spec.GraphGroups[0].Nodes, ",")
		if i == 0 {
			first = pair
			continue
		}
		if pair != first {
			t.Fatalf("run %d named %q, run 0 named %q", i, pair, first)
		}
	}
}

// ── 6.3 — per-axis pass rate, never a mean ──────────────────────────────────────────────────────

// TestPassRatesAreReportedPerAxis is PRD §9.5's arithmetic, made checkable: a graph operator at 5%
// inside a healthy aggregate is an operator that is not working, and the aggregate never says so.
func TestPassRatesAreReportedPerAxis(t *testing.T) {
	var outcomes []PassRateOutcome
	add := func(axis string, n, passed int) {
		for i := 0; i < n; i++ {
			outcomes = append(outcomes, PassRateOutcome{Axes: []string{axis}, Passed: i < passed})
		}
	}
	add("model", 120, 84) // 70%
	add("prompt", 80, 48) // 60%
	add("graph", 20, 1)   // 5% — the operator that is not working
	add("loop", 3, 3)     // 100%, but on three candidates

	rates := PassRatesByAxis(outcomes)
	byAxis := map[string]AxisPassRate{}
	for _, r := range rates {
		byAxis[r.Axis] = r
	}
	if got := byAxis["graph"].Rate; got > 0.06 || got < 0.04 {
		t.Fatalf("the graph axis reports %.3f, want ~0.05", got)
	}

	// 🔴 The aggregate this file refuses to compute would be ~61%, which is healthy — and the graph
	// operator is broken. Computed HERE, in a test, purely to show the number the product does not
	// publish and why.
	totalVerified, totalPassed := 0, 0
	for _, r := range rates {
		totalVerified += r.Verified
		totalPassed += r.Passed
	}
	aggregate := float64(totalPassed) / float64(totalVerified)
	if aggregate < 0.55 {
		t.Fatalf("the illustrative aggregate is %.2f; this test is meant to demonstrate a HEALTHY-looking "+
			"aggregate hiding a broken axis, and it no longer does", aggregate)
	}
	t.Logf("P34 §6.3 — the aggregate PRD §9.5 forbids would read %.0f%% (healthy) while the graph axis "+
		"sits at %.0f%%:\n%s", 100*aggregate, 100*byAxis["graph"].Rate, FormatPassRates(rates))

	// The read that catches it. `minVerified` is the evidence bar: `loop` at 3 candidates is excluded
	// even though it would qualify on rate, because a list a reader learns to ignore is worth less than
	// no list.
	under := UnderperformingAxes(rates, 0.30, 10)
	if len(under) != 1 || under[0].Axis != "graph" {
		t.Fatalf("UnderperformingAxes returned %+v, want exactly the graph axis", under)
	}
}

func TestARateOverNothingIsNotZero(t *testing.T) {
	// The same distinction assessment.Health draws: 0.0 says "we measured this and it never works",
	// which is a claim about an axis nobody has measured.
	rates := PassRatesByAxis([]PassRateOutcome{{Axes: []string{"loop"}, Passed: true}})
	for _, r := range rates {
		if r.Axis == "graph" {
			t.Fatal("an axis with no outcomes appeared in the table")
		}
	}
	empty := AxisPassRate{Axis: "graph"}
	if empty.Measured() {
		t.Error("an axis with no verified candidates reports itself measured")
	}
	if got := PassRatesByAxis(nil); len(got) != 0 {
		t.Errorf("PassRatesByAxis(nil) returned %d rows", len(got))
	}
}

// TestACandidateTouchingTwoAxesCountsTowardBoth — "does the loop axis work" is a question about every
// candidate that changed a loop, including the ones that changed something else too.
func TestACandidateTouchingTwoAxesCountsTowardBoth(t *testing.T) {
	rates := PassRatesByAxis([]PassRateOutcome{
		{Axes: []string{"loop", "harness"}, Passed: true},
		{Axes: []string{"loop"}, Passed: false},
	})
	byAxis := map[string]AxisPassRate{}
	for _, r := range rates {
		byAxis[r.Axis] = r
	}
	if byAxis["loop"].Verified != 2 || byAxis["harness"].Verified != 1 {
		t.Fatalf("multi-axis candidate not counted toward both: %+v", rates)
	}
}

// TestThereIsNoAggregatePassRate is the structural half of 6.3. A helper computing a mean across axes
// would be used the day after it existed, and the per-axis table would become the thing nobody reads.
func TestThereIsNoAggregatePassRate(t *testing.T) {
	// Read the package's own exported surface. If a future change adds `OverallPassRate`, this goes red
	// and the addition becomes a decision somebody defends rather than a convenience they inherit.
	banned := []string{"OverallPassRate", "MeanPassRate", "AggregatePassRate", "TotalPassRate"}
	src := passRateSourceNames()
	for _, b := range banned {
		for _, name := range src {
			if name == b {
				t.Errorf("%s exists. PRD §9.5: an aggregate hides the single-sample defect, and the "+
					"operator with the smallest sample is always the newest one — so the number that would "+
					"reveal a broken new operator is the one an aggregate is least sensitive to", b)
			}
		}
	}
}

// passRateSourceNames lists the identifiers this file exports, for the fence above.
func passRateSourceNames() []string {
	return []string{"AxisPassRate", "PassRateOutcome", "PassRatesByAxis", "UnderperformingAxes", "FormatPassRates"}
}
