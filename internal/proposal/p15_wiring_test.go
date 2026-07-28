package proposal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P15 §2 / §3 — the wiring operators: merge, free reorder, prune.
//
// Every test here asserts one of the three properties the wiring axis rests on and nothing else:
// the SHAPE of the candidate (what moved), its LINEAGE (the parent survives, byte for byte), and its
// IDENTITY (Order/Edges are hashed, so a wiring change is a new configuration). None of them needs a
// registry, a tree, or an eval — a wiring change touches no ref, which is exactly why it needs no new
// Dimension.

// wiringBase is a four-node chain A→B→C→D with one per-node override, so a test can prove a wiring
// candidate inherited the content dimensions unchanged.
func wiringBase() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"A", "B", "C", "D"},
		Nodes: map[string]variantspec.NodeOverride{
			"A": {ModelRef: refStrongModel},
			"C": {PromptRef: "prompt-ref-c"},
		},
		Edges: []variantspec.Edge{
			{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
			{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
			{FromNodeID: "C", ToNodeID: "D", Kind: "data"},
		},
	}
}

// redundant builds the structural target the merge/prune operators fire on.
func redundant(node string) Target {
	return Target{
		Diagnosis: diagnosis.Diagnosis{NodeID: node, EvidenceCaseIDs: []string{"c1"}},
		Signal:    SignalRedundantNode,
		Pattern:   patternclassifier.PromptChaining,
	}
}

// specJSON renders a spec's bytes, for the "the parent is never mutated" assertions.
func specJSON(t *testing.T, s *variantspec.VariantSpec) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	return string(b)
}

// wiringHash projects a spec's WIRING into the resolved shape config_hash is computed over and hashes
// it. Only the identity-bearing wiring fields matter here — that is the whole claim being tested: a
// merge/reorder/prune lands its effect in Order + Edges, which config_hash already covers, so the axis
// needs no hashed field of its own.
func wiringHash(t *testing.T, s *variantspec.VariantSpec) string {
	t.Helper()
	cfg := variantspec.ResolvedConfig{IRVersion: discovery.IRVersion}
	for _, id := range s.Order {
		cfg.Nodes = append(cfg.Nodes, variantspec.ResolvedNode{NodeID: id})
	}
	for _, e := range s.Edges {
		cfg.Edges = append(cfg.Edges, variantspec.ResolvedEdge(e))
	}
	h, err := cfg.Hash()
	if err != nil {
		t.Fatalf("hash resolved config: %v", err)
	}
	return h
}

// ── §2.1 / §7.1 — the merge shape ────────────────────────────────────────────────────────────────

// TestMergeProducesFusedSpec: the absorbed node leaves the order, every edge that touched it now
// touches the survivor, and no other node's overrides move.
func TestMergeProducesFusedSpec(t *testing.T) {
	base := wiringBase()
	e := Engine{Menu: testMenu(), Base: base}
	em := e.Propose([]Target{redundant("C")})

	// C's predecessor B is the survivor: C is absorbed into the node it already runs beside.
	c := findCandidate(t, em.Candidates, OpMerge, "B")
	got := c.Spec

	if indexOf(got.Order, "C") >= 0 {
		t.Fatalf("the absorbed node must leave the order, got %v", got.Order)
	}
	if want := []string{"A", "B", "D"}; !equalStrings(got.Order, want) {
		t.Fatalf("order = %v, want %v (only the absorbed node leaves)", got.Order, want)
	}
	// B→C and C→D become B→D: the absorbed node's outbound edge re-sources from the survivor, and the
	// pair's own edge disappears into the fused node.
	wantEdges := []variantspec.Edge{
		{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
		{FromNodeID: "B", ToNodeID: "D", Kind: "data"},
	}
	if len(got.Edges) != len(wantEdges) {
		t.Fatalf("edges = %+v, want %+v", got.Edges, wantEdges)
	}
	for i, w := range wantEdges {
		if got.Edges[i] != w {
			t.Fatalf("edges[%d] = %+v, want %+v", i, got.Edges[i], w)
		}
	}
	// Content dimensions are inherited unchanged; the absorbed node's override goes with it (a spec
	// that kept it would be rejected as dead config by Validate).
	if got.Nodes["A"].ModelRef != base.Nodes["A"].ModelRef {
		t.Errorf("a merge changed an unrelated node's model: %+v", got.Nodes["A"])
	}
	if _, ok := got.Nodes["C"]; ok {
		t.Errorf("the absorbed node's override must not survive in a spec that no longer orders it")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("the merged spec must be structurally valid: %v", err)
	}
	if len(c.Dimensions) != 1 || c.Dimensions[0] != "order" {
		t.Errorf("a merge is a wiring change only, got dimensions %v", c.Dimensions)
	}
}

// TestMergeIsAdjacentPairOnly pins decisions.md D-1's one-way door: a merge never spans a gap in the
// order. The node two steps away is not a merge partner, however redundant the pair looks.
func TestMergeIsAdjacentPairOnly(t *testing.T) {
	base := wiringBase()
	e := Engine{Menu: testMenu(), Base: base}
	em := e.Propose([]Target{redundant("C")})

	var merges int
	for _, c := range em.Candidates {
		if c.Operator != OpMerge {
			continue
		}
		merges++
		// Exactly one node left the parent's order, and it was adjacent to the survivor.
		if len(c.Spec.Order) != len(base.Order)-1 {
			t.Fatalf("a merge fuses exactly one adjacent pair; order went from %v to %v", base.Order, c.Spec.Order)
		}
		if c.NodeID != "B" {
			t.Fatalf("the survivor must be the absorbed node's neighbour, got %q", c.NodeID)
		}
	}
	if merges != 1 {
		t.Fatalf("one signal on one node yields exactly one merge candidate, got %d", merges)
	}

	// A single-node order has no adjacent pair, so it yields no merge at all — rather than a fusion
	// with nothing, or a silent no-op candidate that would spend a verification slot.
	lone := &variantspec.VariantSpec{WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"only"}, Nodes: map[string]variantspec.NodeOverride{}}
	em = Engine{Menu: testMenu(), Base: lone}.Propose([]Target{redundant("only")})
	for _, c := range em.Candidates {
		if c.Operator == OpMerge {
			t.Fatalf("a lone node has no adjacent pair to fuse, got candidate %+v", c.Spec.Order)
		}
	}
}

// ── §2.2 — derivation carries lineage and leaves the parent byte-identical ───────────────────────

func TestMergeDerivesWithLineageParentUnchanged(t *testing.T) {
	base := wiringBase()
	before := specJSON(t, base)
	const baseHash = "0000000000000000000000000000000000000000000000000000000000000001"

	em := Engine{Menu: testMenu(), Base: base, BaseVariantID: baseHash}.Propose([]Target{redundant("C")})
	c := findCandidate(t, em.Candidates, OpMerge, "B")

	if c.Spec.ParentVariantID != baseHash {
		t.Fatalf("candidate must record the baseline it was derived from, got %q want %q",
			c.Spec.ParentVariantID, baseHash)
	}
	if after := specJSON(t, base); after != before {
		t.Fatalf("the parent spec must be byte-identical after derivation:\n before %s\n after  %s", before, after)
	}
	// The derived spec shares no backing storage with the parent: mutating the candidate cannot reach
	// back into the baseline every other caller holds.
	c.Spec.Order[0] = "MUTATED"
	if base.Order[0] != "A" {
		t.Fatalf("the candidate aliases the parent's order slice")
	}
}

// ── §2.3 / §2.4 — one catalog row, one live prior ────────────────────────────────────────────────

func TestDefaultCatalogIncludesMerge(t *testing.T) {
	var found Operator
	for _, op := range DefaultCatalog() {
		if op.Kind() == OpMerge {
			found = op
		}
	}
	if found == nil {
		t.Fatal("OpMerge is not registered in DefaultCatalog(): a reserved constant with no row is a " +
			"promise, not an operator")
	}
	if found.HandlesSignal() != SignalRedundantNode {
		t.Errorf("merge fires on the redundant-node signal, got %q", found.HandlesSignal())
	}
	if len(found.Handles()) != 0 {
		t.Errorf("merge is signal-driven and adds no taxonomy code, got %v", found.Handles())
	}
}

func TestMergeHasPrior(t *testing.T) {
	if p := operatorPrior[OpMerge]; p <= 0 {
		t.Fatalf("OpMerge has no gain prior (%v): the ranker would sort it last for a reason that is not "+
			"about the change", p)
	}
	if _, ok := verifyOrderHint[OpMerge]; !ok {
		t.Fatal("OpMerge has no verification order hint, so it would sort behind every unknown operator")
	}
	// The prior orders verification; it is never a result. A merge candidate's ExpectedGain must be a
	// function of that prior and the diagnosis severity, not a constant.
	em := Engine{Menu: testMenu(), Base: wiringBase()}.Propose([]Target{redundant("C")})
	c := findCandidate(t, em.Candidates, OpMerge, "B")
	if c.ExpectedGain <= 0 {
		t.Fatalf("merge candidate carries no expected gain (%v)", c.ExpectedGain)
	}
}

// ── §2.5 — Order/Edges are identity-bearing ──────────────────────────────────────────────────────

func TestMergeChangesConfigHash(t *testing.T) {
	base := wiringBase()
	em := Engine{Menu: testMenu(), Base: base}.Propose([]Target{redundant("C")})
	c := findCandidate(t, em.Candidates, OpMerge, "B")

	parentHash := wiringHash(t, base)
	mergedHash := wiringHash(t, c.Spec)
	if parentHash == mergedHash {
		t.Fatal("a merge must produce a new config_hash: Order and Edges are identity-bearing, so a " +
			"fused graph is a different configuration")
	}

	// The converse half of the requirement: identity is a property of the CONFIGURATION, not of the
	// edit path that reached it. A spec authored directly with the merged wiring — different lineage,
	// no operator — hashes identically.
	authored := &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"A", "B", "D"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
			{FromNodeID: "B", ToNodeID: "D", Kind: "data"},
		},
	}
	if got := wiringHash(t, authored); got != mergedHash {
		t.Fatalf("two specs denoting the same wiring must share a config_hash: %s vs %s", got, mergedHash)
	}
}

// ── §3.2 — free rewiring of data-independent neighbours, every candidate gated ───────────────────

// parallelBase is A → (B, C independent, C sequenced after B by a CONTROL edge only) → D.
// B and C exchange no data, so their order is a choice and their sequencing edge is a candidate for
// removal; A→B and C→D are real data dependencies and must survive every proposal.
func parallelBase() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"A", "B", "C", "D"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
			{FromNodeID: "B", ToNodeID: "C", Kind: "control"},
			{FromNodeID: "C", ToNodeID: "D", Kind: "data"},
		},
	}
}

func lostInMiddle(node string) Target {
	return Target{
		Diagnosis: diag(node, diagnosis.CauseLostInMiddle, "c1"),
		Pattern:   patternclassifier.PromptChaining,
	}
}

func TestFreeReorderIndependentNodes(t *testing.T) {
	base := parallelBase()
	em := Engine{Menu: testMenu(), Base: base}.Propose([]Target{lostInMiddle("C")})

	var swapped, parallelized bool
	for _, c := range em.Candidates {
		if c.Operator != OpReorder {
			continue
		}
		if equalStrings(c.Spec.Order, []string{"A", "C", "B", "D"}) {
			swapped = true
			// A reorder moves NOTHING but the wiring: every per-node override is inherited.
			if len(c.Spec.Nodes) != len(base.Nodes) {
				t.Errorf("a reorder changed the node overrides: %+v", c.Spec.Nodes)
			}
		}
		if equalStrings(c.Spec.Order, base.Order) && len(c.Spec.Edges) == len(base.Edges)-1 {
			parallelized = true
			for _, e := range c.Spec.Edges {
				if e.Kind != "data" {
					t.Errorf("the parallelize candidate must drop the sequencing control edge, still has %+v", e)
				}
			}
		}
	}
	if !swapped {
		t.Error("two data-independent neighbours were never proposed in the other order")
	}
	if !parallelized {
		t.Error("a control-only sequenced independent pair was never proposed as parallelizable")
	}

	// The load-bearing negative: a DATA dependency is a fact about the code, never an ordering choice.
	// No candidate may put a data consumer before its producer.
	for _, c := range em.Candidates {
		if c.Operator != OpReorder {
			continue
		}
		for _, e := range c.Spec.Edges {
			if e.Kind != "data" {
				continue
			}
			if indexOf(c.Spec.Order, e.FromNodeID) >= indexOf(c.Spec.Order, e.ToNodeID) {
				t.Errorf("candidate %v orders data consumer %s before producer %s", c.Spec.Order, e.ToNodeID, e.FromNodeID)
			}
		}
	}
}

// TestReorderCandidatesAreGated: every wiring candidate goes through variantspec.GateReorder — the ONE
// gate — before it can be surfaced, and the spec that leaves the gate is the one that gets compiled.
func TestReorderCandidatesAreGated(t *testing.T) {
	// A produces `summary`; B requires it. Any ordering that puts B first is incoherent.
	ir := &discovery.IR{IRVersion: discovery.IRVersion}
	ir.Nodes = append(ir.Nodes,
		discovery.IRNode{NodeID: "A", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: objSchema(map[string]any{"summary": strField()}),
		}},
		discovery.IRNode{NodeID: "B", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  objSchema(map[string]any{"summary": strField()}, "summary"),
			OutputSchema: map[string]any{"type": "object"},
		}},
	)
	base := &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"A", "B"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}},
	}
	e := Engine{Menu: testMenu(), Base: base, IR: ir, Gate: NewTypedContractGate(ir)}
	em := e.Propose([]Target{lostInMiddle("B")})

	for _, c := range em.Candidates {
		if c.Operator == OpReorder {
			t.Fatalf("an incoherent reorder reached the candidate list: %v", c.Spec.Order)
		}
	}
	if !hasRefusal(em.Refusals, OpReorder) {
		t.Fatalf("the gate's rejection was not recorded: %+v", em.Refusals)
	}

	// Ungated is NOT the same as admitted: an Engine with no gate is a unit-test configuration, and the
	// same candidate is then emitted — which is exactly why the gate must be wired in production.
	ungated := Engine{Menu: testMenu(), Base: base}.Propose([]Target{lostInMiddle("B")})
	if !hasCandidate(ungated.Candidates, OpReorder, "B") {
		t.Fatal("the ungated engine should still emit the candidate; the gate is what refuses it")
	}
}

// TestGatedAdaptedCandidateCarriesAdapter: on an `adapted` verdict the candidate that leaves the gate
// carries the bridging adapter, so the diff a reviewer reads contains it. Reading the verdict's
// adapters and discarding them would produce a candidate whose config_hash claims a bridge no diff
// holds (decisions.md D-2).
func TestGatedAdaptedCandidateCarriesAdapter(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion}
	ir.Nodes = append(ir.Nodes,
		discovery.IRNode{NodeID: "A", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: objSchema(map[string]any{"answer": strField()}),
		}},
		discovery.IRNode{NodeID: "B", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  objSchema(map[string]any{"response": strField()}, "response"),
			OutputSchema: map[string]any{"type": "object"},
		}},
		discovery.IRNode{NodeID: "C", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
		}},
	)
	base := &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"A", "B", "C"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}},
	}
	gate := NewTypedContractGate(ir)
	c := Candidate{Operator: OpReorder, NodeID: "C", Spec: base}
	gated, ok, reason := gate.Admit(c)
	if !ok {
		t.Fatalf("an adaptable mismatch is admissible, got refusal %q", reason)
	}
	if len(gated.Spec.InsertedAdapters) != 1 {
		t.Fatalf("the gated candidate must carry the inserted adapter, got %+v", gated.Spec.InsertedAdapters)
	}
	adapterID := gated.Spec.InsertedAdapters[0].AdapterNodeID
	for _, e := range gated.Spec.Edges {
		if e.FromNodeID == "A" && e.ToNodeID == "B" {
			t.Fatal("the direct edge must be rewired through the adapter")
		}
	}
	if indexOf(gated.Spec.Order, adapterID) < 0 {
		t.Fatalf("the adapter node must appear in the order, got %v", gated.Spec.Order)
	}
	// The gate never mutates what it was handed.
	if len(base.InsertedAdapters) != 0 || len(base.Edges) != 1 {
		t.Fatalf("the gate mutated the candidate it was given: %+v", base)
	}
}

// ── §3.3 — prune drops the dead node and rewires its neighbours ──────────────────────────────────

func TestPruneRewiresNeighbours(t *testing.T) {
	base := wiringBase() // A→B→C→D
	em := Engine{Menu: testMenu(), Base: base}.Propose([]Target{redundant("C")})
	c := findCandidate(t, em.Candidates, OpPrune, "C")

	if indexOf(c.Spec.Order, "C") >= 0 {
		t.Fatalf("the pruned node must leave the order, got %v", c.Spec.Order)
	}
	// B→C and C→D collapse to B→D: the neighbours are rewired to each other, not left dangling.
	var rewired bool
	for _, e := range c.Spec.Edges {
		if e.FromNodeID == "C" || e.ToNodeID == "C" {
			t.Fatalf("an edge still touches the pruned node: %+v", e)
		}
		if e.FromNodeID == "B" && e.ToNodeID == "D" {
			rewired = true
		}
	}
	if !rewired {
		t.Fatalf("the pruned node's neighbours were not rewired to each other, got %+v", c.Spec.Edges)
	}
	if _, ok := c.Spec.Nodes["C"]; ok {
		t.Error("the pruned node's override must not survive")
	}
	if err := c.Spec.Validate(); err != nil {
		t.Fatalf("the pruned spec must be structurally valid: %v", err)
	}
	// Prune and merge both answer the redundant-node signal, and BOTH are proposed: measurement decides
	// between them, not the operator.
	//
	// On a straight chain they happen to denote the SAME graph — dropping C from A→B→C→D and fusing C
	// into B both leave A→B→D — and that is not a defect to hide: two operators reaching one
	// configuration means one config_hash, one eval, and a tie, which is the honest outcome when the two
	// hypotheses are indistinguishable in the wiring space P15 works in. Where they differ is a fan-in,
	// asserted below, and that is the case that shows they are genuinely different rewires.
	m := findCandidate(t, em.Candidates, OpMerge, "B")
	if wiringKey(m.Spec.Order, m.Spec.Edges) != wiringKey(c.Spec.Order, c.Spec.Edges) {
		t.Errorf("on a straight chain a prune and a merge denote the same graph; got %v/%+v vs %v/%+v",
			m.Spec.Order, m.Spec.Edges, c.Spec.Order, c.Spec.Edges)
	}
}

// TestPruneAndMergeDifferOnFanIn is the case that separates the two operators. With two producers
// feeding one redundant node, PRUNE rewires every predecessor straight to every successor, while
// MERGE folds the node into ONE neighbour and leaves the other predecessor feeding that neighbour.
// The graphs differ, so the two hypotheses get different config_hashes and are scored apart.
func TestPruneAndMergeDifferOnFanIn(t *testing.T) {
	base := &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"X", "Y", "C", "Z"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "X", ToNodeID: "C", Kind: "data"},
			{FromNodeID: "Y", ToNodeID: "C", Kind: "data"},
			{FromNodeID: "C", ToNodeID: "Z", Kind: "data"},
		},
	}
	em := Engine{Menu: testMenu(), Base: base}.Propose([]Target{redundant("C")})
	pruned := findCandidate(t, em.Candidates, OpPrune, "C")
	merged := findCandidate(t, em.Candidates, OpMerge, "Y")

	if wiringKey(pruned.Spec.Order, pruned.Spec.Edges) == wiringKey(merged.Spec.Order, merged.Spec.Edges) {
		t.Fatalf("prune and merge must differ on a fan-in: %+v vs %+v", pruned.Spec.Edges, merged.Spec.Edges)
	}
	// Prune: both producers now feed the successor directly.
	wantPruned := map[variantspec.Edge]bool{
		{FromNodeID: "X", ToNodeID: "Z", Kind: "data"}: true,
		{FromNodeID: "Y", ToNodeID: "Z", Kind: "data"}: true,
	}
	for _, e := range pruned.Spec.Edges {
		if !wantPruned[e] {
			t.Errorf("prune produced an unexpected edge %+v", e)
		}
	}
	// Merge: C is absorbed by its predecessor Y, so X now feeds Y and Y feeds Z.
	wantMerged := map[variantspec.Edge]bool{
		{FromNodeID: "X", ToNodeID: "Y", Kind: "data"}: true,
		{FromNodeID: "Y", ToNodeID: "Z", Kind: "data"}: true,
	}
	for _, e := range merged.Spec.Edges {
		if !wantMerged[e] {
			t.Errorf("merge produced an unexpected edge %+v", e)
		}
	}
	if hashesEqual := wiringHash(t, pruned.Spec) == wiringHash(t, merged.Spec); hashesEqual {
		t.Error("two different graphs must not share a config_hash")
	}
}

// ── §3.4 — determinism ───────────────────────────────────────────────────────────────────────────

func TestWiringProposalsAreDeterministic(t *testing.T) {
	targets := []Target{redundant("C"), lostInMiddle("C")}
	run := func() []Candidate {
		em := Engine{Menu: testMenu(), Base: parallelBase(), BaseVariantID: "base-hash"}.Propose(targets)
		SortCandidates(em.Candidates)
		return em.Candidates
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("candidate count is not stable: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Operator != second[i].Operator || first[i].NodeID != second[i].NodeID {
			t.Fatalf("candidate %d differs: %s/%s vs %s/%s", i,
				first[i].Operator, first[i].NodeID, second[i].Operator, second[i].NodeID)
		}
		if a, b := specJSON(t, first[i].Spec), specJSON(t, second[i].Spec); a != b {
			t.Fatalf("candidate %d spec is not byte-identical across runs:\n %s\n %s", i, a, b)
		}
		if a, b := wiringHash(t, first[i].Spec), wiringHash(t, second[i].Spec); a != b {
			t.Fatalf("candidate %d config_hash is not stable: %s vs %s", i, a, b)
		}
	}
	if len(first) == 0 {
		t.Fatal("the determinism assertion is vacuous: no wiring candidate was produced")
	}
}

// ── §5.2 — 🔴 reject-at-compile: an incoherent wiring yields no runnable spec, no diff, no PR ─────

// TestIncoherentWiringRejectedAtCompile extends the P5 editor's reject-at-compile guarantee to the
// operators that produce wiring candidates automatically.
//
// The graph: X produces nothing, P produces `summary`, C requires it. Removing P — by PRUNE or by
// MERGE — leaves X feeding C, and X cannot supply `summary`. No catalog adapter invents a missing
// field, so both candidates are incoherent.
//
// The gate must go RED. Not "the build catches it later": a wiring change that does not type-check has
// to be caught BEFORE a codemod, diff, or PR exists, and GateReorder returning (nil, verdict) is what
// makes handing a rejected ordering to the transform engine physically impossible rather than
// forbidden by convention.
func TestIncoherentWiringRejectedAtCompile(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion}
	ir.Nodes = append(ir.Nodes,
		discovery.IRNode{NodeID: "X", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		discovery.IRNode{NodeID: "P", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: objSchema(map[string]any{"summary": strField()}),
		}},
		discovery.IRNode{NodeID: "C", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  objSchema(map[string]any{"summary": strField()}, "summary"),
			OutputSchema: map[string]any{"type": "object"},
		}},
	)
	base := &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"X", "P", "C"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "X", ToNodeID: "P", Kind: "data"},
			{FromNodeID: "P", ToNodeID: "C", Kind: "data"},
		},
	}

	// 1. Ungated, both operators DO produce a candidate — so the refusal below is the gate's work, and
	// the test is not passing because nothing was proposed.
	raw := Engine{Menu: testMenu(), Base: base}.Propose([]Target{redundant("P")})
	if !hasCandidate(raw.Candidates, OpPrune, "P") || !hasCandidate(raw.Candidates, OpMerge, "X") {
		t.Fatalf("both wiring operators should propose here; got %d candidates", len(raw.Candidates))
	}

	// 2. Gated, neither survives, and each rejection is RECORDED rather than swallowed.
	em := Engine{Menu: testMenu(), Base: base, IR: ir, Gate: NewTypedContractGate(ir)}.
		Propose([]Target{redundant("P")})
	for _, c := range em.Candidates {
		if c.Operator == OpPrune || c.Operator == OpMerge {
			t.Fatalf("an incoherent %s reached the candidate list: %v %+v", c.Operator, c.Spec.Order, c.Spec.Edges)
		}
	}
	for _, op := range []OperatorKind{OpPrune, OpMerge} {
		if !hasRefusal(em.Refusals, op) {
			t.Errorf("%s's rejection was not recorded: %+v", op, em.Refusals)
		}
	}
	// The refusal names the offending edge, because "which edge, which field" is the whole of what a
	// user needs to understand why the change was withheld.
	for _, r := range em.Refusals {
		if r.Operator != OpPrune && r.Operator != OpMerge {
			continue
		}
		if !strings.Contains(r.Reason, "X") || !strings.Contains(r.Reason, "C") {
			t.Errorf("%s refusal must name the offending edge X→C, got %q", r.Operator, r.Reason)
		}
	}

	// 3. The structural guarantee itself: the gate returns NO runnable spec for these candidates, so
	// there is nothing a caller could hand to the transform engine even by mistake.
	for _, c := range raw.Candidates {
		if c.Operator != OpPrune && c.Operator != OpMerge {
			continue
		}
		got, verdict := variantspec.GateReorder(ir, c.Spec, typedcontract.DefaultCatalog())
		if got != nil {
			t.Fatalf("%s: an incoherent ordering must yield no runnable spec, got %+v", c.Operator, got.Order)
		}
		if verdict.Kind != typedcontract.VerdictRejected {
			t.Fatalf("%s: want a rejected verdict, got %s", c.Operator, verdict.Kind)
		}
		if len(verdict.Diagnostics) == 0 {
			t.Fatalf("%s: a rejection with no diagnostic tells the user nothing", c.Operator)
		}
	}
}

// ── §6.2 — 🚫 a wiring change is scored by the EXISTING harness, with no eval-side addition ───────

// TestWiringScoredByExistingHarness is a negative test about a thing that was NOT added, which is
// precisely the kind nothing else would notice.
//
// A wiring change lands entirely in Order/Edges, which config_hash already covers, and the harness
// consumes config_hash + Trace. So there is nothing for the eval side to learn about wiring: no metric,
// no scorer, no `if dimension == "order"` branch. A bespoke wiring number — "graph depth reduced",
// "nodes fused" — would be a SECOND definition of better for one axis, and a change that measures
// itself always looks like a win. "Fewer nodes" is not a goal; a better score at equal or lower cost
// is, and task_success / eval_cost_usd / eval_latency_ms already say that.
func TestWiringScoredByExistingHarness(t *testing.T) {
	// The metrics P15's claim rests on are the ones already there.
	for _, want := range []string{
		evalharness.MetricTaskSuccess, evalharness.MetricRunCostUSD, evalharness.MetricRunLatencyMS,
	} {
		if !containsString(evalharness.StandardFamily, want) {
			t.Fatalf("%s is not in the standard family; a wiring win would have nowhere to surface", want)
		}
	}
	// 🚫 And nothing wiring-shaped joined them.
	for _, m := range append(append([]string(nil), evalharness.StandardFamily...), evalharness.ContributionFamily...) {
		lower := strings.ToLower(m)
		for _, banned := range []string{"wiring", "merge", "reorder", "graph", "node_count", "depth", "parallel"} {
			if strings.Contains(lower, banned) {
				t.Errorf("the metric family gained %q, which measures the wiring CHANGE rather than its "+
					"EFFECT; P15 ships no new metric (design Decision 6)", m)
			}
		}
	}
	if len(evalharness.StandardFamily) != 6 {
		t.Errorf("the standard metric family has %d members: %v — P15 adds none",
			len(evalharness.StandardFamily), evalharness.StandardFamily)
	}

	// A merge candidate is an ORDINARY config: a plain Variant Spec that validates and hashes through
	// the same path every content change uses. That is what makes "no dimension-label branch" true
	// rather than promised — there is nothing about this spec for a scorer to branch on.
	em := Engine{Menu: testMenu(), Base: wiringBase()}.Propose([]Target{redundant("C")})
	c := findCandidate(t, em.Candidates, OpMerge, "B")
	if err := c.Spec.Validate(); err != nil {
		t.Fatalf("a merge candidate must be an ordinary valid spec: %v", err)
	}
	// A wiring change INTRODUCES no registry ref — it rearranges nodes that already exist. (It may drop
	// one, when the absorbed node carried an override; that is the fusion, not a new resolution.)
	baseRefs := map[string]bool{}
	for _, r := range wiringBase().Refs() {
		baseRefs[r] = true
	}
	for _, r := range c.Spec.Refs() {
		if !baseRefs[r] {
			t.Errorf("a wiring change resolved a NEW registry ref %q; the axis selects nothing", r)
		}
	}
	if h := wiringHash(t, c.Spec); len(h) != 64 {
		t.Errorf("a wiring candidate must hash through the ordinary config_hash path, got %q", h)
	}
}
