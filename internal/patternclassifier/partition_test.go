package patternclassifier

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// A region's identity is CONTENT-ADDRESSED, so it does not depend on which detector found it, in
// what order, or on a run counter. Without this, "byte-identical labels across runs" is unreachable.
func TestSubgraphIDIsContentAddressed(t *testing.T) {
	a := SubgraphIDFor([]string{"n_c", "n_a", "n_b"})
	b := SubgraphIDFor([]string{"n_b", "n_c", "n_a", "n_a"})
	if a != b {
		t.Fatalf("id must not depend on order or duplicates: %q vs %q", a, b)
	}
	if c := SubgraphIDFor([]string{"n_a", "n_b"}); c == a {
		t.Fatal("a different member set must get a different id")
	}
	if !strings.HasPrefix(a, "sg_") {
		t.Errorf("id %q should be namespaced", a)
	}
}

// The residue handed to the LLM is split into weakly-connected COMPONENTS: two unclassified islands
// are two separate questions. Merging them would ask the model to name one pattern spanning nodes
// that never touch.
func TestResidueIsSplitIntoConnectedComponents(t *testing.T) {
	ir := buildIR(
		[]discovery.IRNode{node("n_a"), node("n_b"), node("n_x"), node("n_y"), node("n_lonely")},
		[]discovery.IREdge{dataEdge("n_a", "n_b"), controlEdge("n_x", "n_y")},
	)
	g := newGraph(ir)
	comps := g.residueComponents(map[string]bool{})
	if len(comps) != 3 {
		t.Fatalf("got %d components %v, want 3", len(comps), comps)
	}
	// Deterministic ordering, and each component is internally sorted.
	want := [][]string{{"n_a", "n_b"}, {"n_lonely"}, {"n_x", "n_y"}}
	for i := range want {
		if strings.Join(comps[i], ",") != strings.Join(want[i], ",") {
			t.Errorf("component %d = %v, want %v", i, comps[i], want[i])
		}
	}
	// Covered nodes are excluded — the LLM is only ever shown what the rules did not cover.
	comps = g.residueComponents(map[string]bool{"n_a": true, "n_b": true, "n_lonely": true})
	if len(comps) != 1 || comps[0][0] != "n_x" {
		t.Fatalf("covered nodes must be excluded from the residue: %v", comps)
	}
}

func TestGraphIgnoresDanglingEdges(t *testing.T) {
	ir := buildIR([]discovery.IRNode{node("n_a")}, []discovery.IREdge{dataEdge("n_a", "n_ghost")})
	g := newGraph(ir)
	if len(g.dataOut["n_a"]) != 0 {
		t.Fatal("an edge to a non-existent node must not invent adjacency")
	}
}

// Task 2.2: two patterns on two DIFFERENT subgraphs must be representable at once, with no contest.
func TestTwoDisjointRegionsCoexist(t *testing.T) {
	var d diagSink
	got := resolve([]RegionProposal{
		{Pattern: Routing, DetectorID: "routing.v1", Scope: ScopeRegion, NodeIDs: []string{"n_r", "n_a", "n_b"}, Confidence: 0.95},
		{Pattern: RetrievalRAG, DetectorID: "rag.v1", Scope: ScopeRegion, NodeIDs: []string{"n_ret", "n_emb", "n_gen"}, Confidence: 0.95},
	}, &d)
	if len(got) != 2 {
		t.Fatalf("got %d regions, want 2", len(got))
	}
	if got[0].SubgraphID == got[1].SubgraphID {
		t.Fatal("disjoint regions must get distinct subgraph ids")
	}
	if len(d.sorted()) != 0 {
		t.Fatalf("disjoint regions must not conflict: %v", d.sorted())
	}
}

// Task 2.3, case 1: a node-scoped capability NEVER contests. Tool Use on a node inside a Routing
// branch yields BOTH labels — Routing on the subgraph, Tool Use on the node.
func TestNodeScopedCapabilityCoexistsWithOwningRegion(t *testing.T) {
	var d diagSink
	got := resolve([]RegionProposal{
		{Pattern: Routing, DetectorID: "routing.v1", Scope: ScopeRegion, NodeIDs: []string{"n_r", "n_a", "n_b"}, Confidence: 0.95},
		{Pattern: ToolUse, DetectorID: "tooluse.v1", Scope: ScopeNode, NodeIDs: []string{"n_a"}, Confidence: 0.95},
	}, &d)
	if len(got) != 2 {
		t.Fatalf("got %d regions, want 2 (the region and the node capability)", len(got))
	}
	var tool *resolvedRegion
	for i := range got {
		if got[i].Pattern == ToolUse {
			tool = &got[i]
		}
	}
	if tool == nil {
		t.Fatal("the node-scoped Tool Use label was dropped")
	}
	if tool.SubgraphID != "n_a" {
		t.Errorf("a node-scoped label must reference the node itself, got %q", tool.SubgraphID)
	}
	if len(d.sorted()) != 0 {
		t.Fatalf("a node capability must not conflict with its owning region: %v", d.sorted())
	}
}

// Task 2.3, case 2: identical regions are two true statements about one region, sharing one id.
func TestIdenticalRegionsShareOneSubgraph(t *testing.T) {
	var d diagSink
	got := resolve([]RegionProposal{
		{Pattern: Parallelization, DetectorID: "par.v1", Scope: ScopeRegion, NodeIDs: []string{"n_a", "n_b", "n_c"}, Confidence: 0.95},
		{Pattern: MultiAgentCollaboration, DetectorID: "mac.v1", Scope: ScopeRegion, NodeIDs: []string{"n_c", "n_a", "n_b"}, Confidence: 0.8},
	}, &d)
	if len(got) != 2 {
		t.Fatalf("both labels must survive on the same region, got %d", len(got))
	}
	if got[0].SubgraphID != got[1].SubgraphID {
		t.Errorf("identical member sets must share one subgraph id: %q vs %q", got[0].SubgraphID, got[1].SubgraphID)
	}
	if len(d.sorted()) != 0 {
		t.Fatalf("identical regions are not a conflict: %v", d.sorted())
	}
}

// Task 2.3, case 3: nesting is a real composition; each level keeps its own subgraph, so each
// dispatches its own metric-set.
func TestNestedRegionsBothSurviveAsSeparateSubgraphs(t *testing.T) {
	var d diagSink
	got := resolve([]RegionProposal{
		{Pattern: Parallelization, DetectorID: "par.v1", Scope: ScopeRegion, NodeIDs: []string{"n_f", "n_a", "n_b", "n_m"}, Confidence: 0.95},
		{Pattern: PromptChaining, DetectorID: "chain.v1", Scope: ScopeRegion, NodeIDs: []string{"n_a", "n_b"}, Confidence: 0.95},
	}, &d)
	if len(got) != 2 {
		t.Fatalf("a nested region must not be dropped, got %d", len(got))
	}
	if got[0].SubgraphID == got[1].SubgraphID {
		t.Error("a nested region is its own subgraph")
	}
	if len(d.sorted()) != 0 {
		t.Fatalf("nesting is not a conflict: %v", d.sorted())
	}
}

// Task 2.3, case 4: partial overlap is the only true contest. The higher-precedence region owns the
// nodes, and the loser is dropped WITH A DIAGNOSTIC — a dropped label is a metric-set that will not
// be computed, so it may never vanish silently.
func TestPartialOverlapIsResolvedByPrecedenceAndDiagnosed(t *testing.T) {
	var d diagSink
	got := resolve([]RegionProposal{
		{Pattern: ResourceAwareOptimization, DetectorID: "rao.v1", Scope: ScopeRegion, NodeIDs: []string{"n_b", "n_c"}, Confidence: 0.8},
		{Pattern: Routing, DetectorID: "routing.v1", Scope: ScopeRegion, NodeIDs: []string{"n_a", "n_b"}, Confidence: 0.95},
	}, &d)
	if len(got) != 1 {
		t.Fatalf("partial overlap must leave exactly one owner, got %d: %+v", len(got), got)
	}
	if got[0].Pattern != Routing {
		t.Errorf("the control-flow pattern owns the subgraph, got %q", got[0].Pattern)
	}
	diags := d.sorted()
	if len(diags) != 1 {
		t.Fatalf("the dropped proposal must be diagnosed, got %d diagnostics", len(diags))
	}
	if diags[0].RawPattern != string(ResourceAwareOptimization) || !strings.Contains(diags[0].Reason, "partially overlaps") {
		t.Errorf("diagnostic does not explain the drop: %s", diags[0])
	}
}

// Arbitration must not depend on the order the detectors ran in.
func TestResolveIsOrderIndependent(t *testing.T) {
	mk := func() []RegionProposal {
		return []RegionProposal{
			{Pattern: ToolUse, DetectorID: "tooluse.v1", Scope: ScopeNode, NodeIDs: []string{"n_b"}, Confidence: 0.95},
			{Pattern: ResourceAwareOptimization, DetectorID: "rao.v1", Scope: ScopeRegion, NodeIDs: []string{"n_b", "n_c"}, Confidence: 0.8},
			{Pattern: Routing, DetectorID: "routing.v1", Scope: ScopeRegion, NodeIDs: []string{"n_a", "n_b"}, Confidence: 0.95},
		}
	}
	var d1, d2 diagSink
	forward := resolve(mk(), &d1)
	rev := mk()
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	backward := resolve(rev, &d2)
	if len(forward) != len(backward) {
		t.Fatalf("resolution depends on proposal order: %d vs %d", len(forward), len(backward))
	}
	for i := range forward {
		if forward[i].SubgraphID != backward[i].SubgraphID || forward[i].Pattern != backward[i].Pattern {
			t.Errorf("index %d differs: %v/%v vs %v/%v", i, forward[i].SubgraphID, forward[i].Pattern, backward[i].SubgraphID, backward[i].Pattern)
		}
	}
}

func TestEmptyRegionProposalIsRejectedNotSilent(t *testing.T) {
	var d diagSink
	got := resolve([]RegionProposal{{Pattern: Routing, DetectorID: "routing.v1", Scope: ScopeRegion}}, &d)
	if len(got) != 0 {
		t.Fatal("an empty region must not become a subgraph")
	}
	if len(d.sorted()) != 1 {
		t.Fatal("an empty region proposal must be diagnosed")
	}
}
