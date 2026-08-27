package herosagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// p36_topology_test.go is §4 — the agent's graph, validated by the customer's validator and executed
// without the scheduler leaking into the result.

// 🔴 §4.1 / §9.13 — THE AGENT AND A CUSTOMER SHARE ONE VALIDATOR, ASSERTED ON THE CODE PATH.
//
// A lookalike is the failure design D1 is about: a parallel validator for our own configuration is
// where a rule gets quietly weaker, and the way it is discovered is a customer noticing the platform
// does not hold itself to a rule it enforces on them.
//
// The assertion is not "both refuse". Two independent validators would also both refuse. It is that
// the agent's refusal and the CUSTOMER'S refusal, on the same declaration, carry the SAME SENTINEL and
// the SAME SENTENCE — which two implementations do not produce by coincidence.
func TestTheAgentsTopologyGoesThroughTheCustomersValidator(t *testing.T) {
	ctx := context.Background()

	// The same malformed topology, expressed twice: once as the agent's definition, once as a
	// customer's Variant Spec. Two nodes fan into a third with no merge declared.
	d := fanInDefinition()
	d.GraphGroups[0].Merge = nil

	pub, _ := p36Publisher(t, RunnerHosts{}, nil)
	_, agentErr := pub.Publish(ctx, d)
	if agentErr == nil {
		t.Fatal("the agent published a fan-in with no merge")
	}

	// The customer's path, over the same declaration.
	spec := d.Spec()
	_, _, customerErr := variantspec.ValidateTopology(ctx, &spec, AgentIR(d), nil)
	if customerErr == nil {
		t.Fatal("the shared validator accepted a fan-in with no merge — the fence above is passing " +
			"for the wrong reason")
	}

	// 🔴 The agent's refusal CONTAINS the customer's, verbatim. It is wrapped with the agent's own
	// sentinel so `errors.Is(err, ErrInvalidDefinition)` still works at a publish call site, and the
	// validator's sentence travels inside it unaltered.
	if !strings.Contains(agentErr.Error(), customerErr.Error()) {
		t.Errorf("the agent's refusal is not the validator's refusal.\n  agent:    %v\n  customer: %v\n\n"+
			"  Two sentences means two implementations, and the second one is always the one that is "+
			"wrong — written by somebody who believed the problem was simpler.", agentErr, customerErr)
	}
	if !errors.Is(agentErr, ErrInvalidDefinition) {
		t.Errorf("the agent's refusal lost its sentinel: %v", agentErr)
	}

	// 🔴 ANTI-VACUITY: the same definition WITH a merge publishes, and the validator accepts it. A
	// validator that refused everything would satisfy the assertions above.
	ok := fanInDefinition()
	if _, err := pub.Publish(ctx, ok); err != nil {
		t.Errorf("a well-formed fan-in was refused: %v", err)
	}
}

// 🔴 §4.1 — the agent's topology TYPES are the customer's, so there is no conversion step where the
// semantics could differ. This is the structural half of "one code path".
func TestNoSecondTopologyValidatorExistsInThisPackage(t *testing.T) {
	// The vocabulary a second implementation would have to spell. If any of it appears in this
	// package's own topology handling, a rule has been re-derived here.
	//
	// 🔴 Asserted on the DEFINITION's validation path specifically. `graphrun.go` legitimately mentions
	// merges and fan-ins — it EXECUTES them — and the distinction being drawn is between deciding what
	// is legal (variantspec's) and doing what was declared (ours).
	spec := twoNodeDefinition().Spec()
	if spec.WorkflowID != "heros" {
		t.Fatalf("the agent's spec does not identify itself: %q", spec.WorkflowID)
	}
	// The projection carries the customer's own types, not copies.
	if len(spec.Edges) != 1 || spec.Edges[0].FromNodeID != "analyst" {
		t.Errorf("the agent's edges did not reach the spec as variantspec.Edge: %+v", spec.Edges)
	}
	// And every declared node is in the ordering the validator walks.
	if len(spec.Order) != len(spec.Nodes) {
		t.Errorf("the projected ordering (%d) and node map (%d) disagree; the validator would refuse a "+
			"node it cannot place", len(spec.Order), len(spec.Nodes))
	}
}

// 🔴 §4.2 — CONCURRENCY IS DECLARED OVER THE ORDERING, and the ordering still contains every node.
func TestConcurrencyIsDeclaredOverTheOrderingRatherThanInsteadOfIt(t *testing.T) {
	ctx := context.Background()
	d := fanInDefinition()
	if !d.GraphGroups[0].Concurrent {
		t.Fatal("the fixture does not declare concurrency, so this test proves nothing")
	}
	// Every group member is in Order.
	inOrder := map[string]bool{}
	for _, id := range d.Ordering() {
		inOrder[id] = true
	}
	for _, m := range d.GraphGroups[0].Nodes {
		if !inOrder[m] {
			t.Errorf("group member %q is not in the ordering", m)
		}
	}
	if len(d.Ordering()) != len(d.Nodes) {
		t.Errorf("the ordering lists %d of %d nodes. A replay visits nodes in this sequence even when "+
			"the live run overlapped them, so a node outside it has no defined replay position.",
			len(d.Ordering()), len(d.Nodes))
	}

	// A group naming a node the ordering does not contain is REFUSED, through the shared validator.
	bad := fanInDefinition()
	bad.GraphGroups[0].Nodes = append(bad.GraphGroups[0].Nodes, "ghost")
	pub, _ := p36Publisher(t, RunnerHosts{}, nil)
	if _, err := pub.Publish(ctx, bad); err == nil {
		t.Error("a concurrent group naming a node outside the ordering was accepted; the executor would " +
			"never visit it, and its author believes it runs")
	}
}

// 🔴 §4.3 / §9.7 — A FAN-IN WITH NO DECLARED MERGE IS REFUSED AT PUBLISH, and no default is applied.
func TestAFanInWithoutAMergeIsRefusedAtPublish(t *testing.T) {
	ctx := context.Background()
	d := fanInDefinition()
	d.GraphGroups[0].Merge = nil

	pub, store := p36Publisher(t, RunnerHosts{}, nil)
	_, err := pub.Publish(ctx, d)
	if err == nil {
		t.Fatal("a fan-in with no merge was published")
	}
	msg := err.Error()
	// The refusal must say WHY it is a refusal rather than a default, or somebody adds the default.
	for _, want := range []string{"merge", "first-result-wins", "all-fields", "namespaced"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
	// 🔴 NO VERSION ROW. A refused publish that wrote a row would leave a config_hash pointing at a
	// configuration nothing can run.
	all, lerr := store.List(ctx)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(all) != 0 {
		t.Errorf("a refused publish wrote %d version row(s)", len(all))
	}
}

// 🔴 §4.4 — A CONDITIONAL EDGE IS VALIDATED AT PUBLISH THROUGH THE EXISTING EXPRESSION PATH.
//
// The predicate is an `expr` binding. It is refused when it names a symbol the producing call site does
// not record as in scope — by `variantspec.validatePredicates`, the same check that governs a prompt
// slot's `expr`, with no new code on this side.
func TestAConditionalEdgeIsValidatedAtPublishByTheExpressionPath(t *testing.T) {
	ctx := context.Background()
	pub, _ := p36Publisher(t, RunnerHosts{}, nil)

	// A predicate naming a symbol nothing declares.
	bad := twoNodeDefinition()
	bad.Edges = []variantspec.Edge{{FromNodeID: "analyst", ToNodeID: "critic",
		Kind: variantspec.EdgeKindPredicate, Predicate: "analyst_had_a_hunch"}}
	_, err := pub.Publish(ctx, bad)
	if err == nil {
		t.Fatal("a predicate naming an unavailable symbol was published; it would be evaluated by " +
			"nothing and the edge would silently never be taken")
	}
	if !strings.Contains(err.Error(), "analyst_had_a_hunch") {
		t.Errorf("the refusal does not name the offending symbol: %v", err)
	}

	// 🔴 ANTI-VACUITY: every symbol the closed vocabulary DOES declare publishes.
	for _, p := range AgentPredicates() {
		ok := twoNodeDefinition()
		ok.Edges = []variantspec.Edge{{FromNodeID: "analyst", ToNodeID: "critic",
			Kind: variantspec.EdgeKindPredicate, Predicate: p.Symbol}}
		if _, err := pub.Publish(ctx, ok); err != nil {
			t.Errorf("the declared predicate %q was refused: %v. The fence above is refusing "+
				"everything, which passes and makes conditional routing unusable.", p.Symbol, err)
		}
	}

	// And a kind/payload mismatch is refused in both directions — the kind and the payload must agree,
	// or the executor and the reader disagree about whether this edge is guarded.
	for _, c := range []struct {
		name string
		edge variantspec.Edge
	}{
		{"predicate kind, no predicate", variantspec.Edge{FromNodeID: "analyst", ToNodeID: "critic",
			Kind: variantspec.EdgeKindPredicate}},
		{"data kind, carrying a predicate", variantspec.Edge{FromNodeID: "analyst", ToNodeID: "critic",
			Kind: "data", Predicate: "produced_edges"}},
	} {
		d := twoNodeDefinition()
		d.Edges = []variantspec.Edge{c.edge}
		if _, err := pub.Publish(ctx, d); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
}

// 🔴 §4.4 — the predicate vocabulary is RECORDED as scope, not left nil.
//
// A nil `InScope` makes `InScopeRecorded()` false, and resolve then ACCEPTS every predicate. That is
// the vacuous pass arriving through an empty slice, and it would make the test above pass while the
// check did nothing.
func TestTheAgentIRRecordsItsPredicateVocabularyRatherThanDeferring(t *testing.T) {
	ir := AgentIR(twoNodeDefinition())
	if len(ir.Nodes) == 0 {
		t.Fatal("the agent IR has no nodes")
	}
	for _, n := range ir.Nodes {
		if !n.CallSite.InScopeRecorded() {
			t.Errorf("node %q records no in-scope set, so `validatePredicates` DEFERS instead of "+
				"checking — every predicate would be accepted, including one nothing can evaluate",
				n.NodeID)
		}
		for _, p := range AgentPredicates() {
			if !n.CallSite.HasInScope(p.Symbol) {
				t.Errorf("node %q does not record the declared predicate %q as in scope, so publishing "+
					"one would be refused for a symbol this build can evaluate", n.NodeID, p.Symbol)
			}
		}
	}
	// 🚫 And the IR's contract is not permissive. An empty output schema makes every merge trivially
	// satisfiable, which is the "quietly weaker internal path" D1 exists to prevent.
	out := ir.Nodes[0].IOContract.OutputSchema
	if len(out) == 0 || out["properties"] == nil {
		t.Errorf("the agent's node contract declares no output properties, so a merge against it would "+
			"be satisfied by anything: %+v", out)
	}
	req, _ := out["required"].([]any)
	if len(req) == 0 {
		t.Error("the agent's node contract requires no output field. A contract with no requirements " +
			"cannot refuse a merge, so the agent would pass a check a customer fails on the same shape.")
	}
}

// 🔴 §4.1 — THE MERGE CONTRACT HAS TEETH, proved rather than assumed.
//
// The agent's node INPUT contract deliberately requires nothing — the assessment input is ambient, so
// requiring it from a predecessor would assert a delivery obligation no topology can meet (see
// `agentIOContract`). The force lives in the OUTPUT contract instead, and this is the assertion that
// it is real: two HEROS nodes both declare `edges` and `labels`, so an `all-fields` merge between them
// COLLIDES and is refused.
//
// Without this, "the input requires nothing" would be indistinguishable from "the check does nothing".
func TestAnAllFieldsMergeBetweenTwoAgentNodesIsRefused(t *testing.T) {
	ctx := context.Background()
	d := fanInDefinition()
	d.GraphGroups[0].Merge.Strategy = variantspec.MergeAllFields

	pub, _ := p36Publisher(t, RunnerHosts{}, nil)
	_, err := pub.Publish(ctx, d)
	if err == nil {
		t.Fatal("an `all-fields` merge between two nodes that both produce `edges` was accepted. " +
			"Precedence here would be the platform deciding which of two answers is the real one — and " +
			"under concurrency the answer would additionally depend on scheduling.")
	}
	msg := err.Error()
	for _, want := range []string{"edges", "left", "right"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q, so the author cannot see which field collided "+
				"between which producers: %s", want, msg)
		}
	}
	// 🔴 ANTI-VACUITY: `namespaced` — which "cannot collide by construction", and is the documented
	// answer for a fan-in whose nodes produce the same field names — IS accepted.
	if _, err := pub.Publish(ctx, fanInDefinition()); err != nil {
		t.Errorf("a `namespaced` merge was refused too, so the check is refusing every fan-in rather "+
			"than the colliding one: %v", err)
	}
}

// 🔴 §4.5 / §9.11 — A PINNED RESULT DOES NOT DEPEND ON INTERLEAVING. RUN REPEATEDLY.
//
// This failure is intermittent by nature, which is why the fence runs the same pinned inference many
// times rather than once. A single run of a racy merge passes most of the time.
func TestARepeatedPinnedInferenceUnderConcurrencyIsByteIdentical(t *testing.T) {
	ctx := context.Background()
	d := fanInDefinition()

	// Two nodes that answer DIFFERENTLY and with DELIBERATELY VARYING delays, so a merge that appended
	// in completion order would produce a different document on almost every run.
	models := map[string]Model{
		"left":  &jitterModel{edges: [][2]string{{"a", "b"}}, narrative: "left says so"},
		"right": &jitterModel{edges: [][2]string{{"b", "c"}}, narrative: "right says so"},
		"merge": &jitterModel{edges: [][2]string{{"a", "c"}}, narrative: "merged"},
	}

	const runs = 40
	var first string
	for i := 0; i < runs; i++ {
		store := NewMemInferenceStore()
		r, err := NewRunner(models["left"], store, 0.5, func() int64 { return 1 },
			WithNodeModels(func(n Node) (Model, error) { return models[n.NodeID], nil }))
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.Infer(ctx, inputFor(irWith([]string{"a", "b", "c"})),
			BindDefinition("cfg-graph", d), PlacementPlatform)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		b, err := json.Marshal(struct {
			Edges     []ProvenancedEdge
			Labels    []patternclassifier.RegionProposal
			Narrative string
		}{res.Edges, res.Labels, res.Narrative})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(b)
			continue
		}
		if string(b) != first {
			t.Fatalf("run %d produced different bytes from run 0. A pinned result that depends on "+
				"interleaving means two runs of ONE configuration do not agree, so the configuration "+
				"cannot be scored — and D2's guarantee that the same revision always shows you the same "+
				"graph is false.\n  run 0: %s\n  run %d: %s", i, first, i, b)
		}
	}
	// 🔴 ANTI-VACUITY: the runs must actually have produced something. Forty identical empty documents
	// would pass the loop above.
	if !strings.Contains(first, `"from":"a"`) || !strings.Contains(first, "says so") {
		t.Errorf("the repeated runs produced no edges or no narrative, so byte-identity was asserted "+
			"over an empty result: %s", first)
	}
}

// 🔴 §4.6 — AN IN-FLIGHT ASSESSMENT COMPLETES UNDER THE DEFINITION IT STARTED WITH, and the record
// names which.
//
// Made structural rather than checked: the definition is a VALUE the run holds, so there is no read of
// "the active definition" inside a run that could return a different one.
func TestAnInFlightAssessmentFinishesUnderTheDefinitionItStartedWith(t *testing.T) {
	ctx := context.Background()
	store := NewMemInferenceStore()

	started := twoNodeDefinition()
	startedHash, err := started.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}

	// A model that ACTIVATES A DIFFERENT DEFINITION mid-run — the exact hazard, staged. If the runner
	// re-read "the active definition" anywhere below its first node, the second node would run under
	// the new one.
	versions := NewMemVersionStore()
	activated := make(chan struct{}, 1)
	switcher := &sideEffectModel{
		onCall: func() {
			select {
			case activated <- struct{}{}:
				other := goodDefinition()
				otherHash, _ := other.ConfigHash()
				_ = versions.Put(ctx, Version{ConfigHash: otherHash, Definition: other,
					RehearsalState: RehearsalPassed})
				_ = versions.Activate(ctx, otherHash, 2)
			default:
			}
		},
		edges: [][2]string{{"a", "b"}},
	}

	r, err := NewRunner(switcher, store, 0.5, func() int64 { return 1 },
		WithNodeModels(func(Node) (Model, error) { return switcher, nil }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Infer(ctx, inputFor(irWith([]string{"a", "b", "c"})),
		BindDefinition(startedHash, started), PlacementPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if len(activated) == 0 && switcher.calls == 0 {
		t.Fatal("the staged activation never fired, so this test proves nothing")
	}

	stored, ok, err := store.Get(ctx, "wf", "rev1", startedHash)
	if err != nil || !ok {
		t.Fatalf("the assessment was not stored under the definition it started with: ok=%v err=%v", ok, err)
	}
	if stored.AgentConfigHash != startedHash {
		t.Errorf("the report names %q and the assessment started under %q. A report with two "+
			"configurations in it has no honest label: half its findings came from one agent and half "+
			"from another.", stored.AgentConfigHash, startedHash)
	}
	// BOTH nodes ran under the started definition — the record names them.
	if len(stored.Nodes) != 2 {
		t.Errorf("the record names %d node(s); the definition it started with declares 2: %+v",
			len(stored.Nodes), stored.Nodes)
	}
	if res.ProviderCalls != 2 {
		t.Errorf("the assessment made %d provider call(s); a two-node definition makes 2", res.ProviderCalls)
	}
}

// ── fixtures ────────────────────────────────────────────────────────────────────────────────────

// fanInDefinition is two concurrent nodes converging on a third, with a declared merge.
func fanInDefinition() Definition {
	node := func(id, prompt string) Node {
		return Node{NodeID: id, PromptRef: prompt, ModelRef: "claude-opus-5",
			CredentialRef: "anthropic", ContextRef: "ctx-v1", HarnessRef: "harness-single-shot-v1"}
	}
	return Definition{
		Nodes: []Node{node("left", "p-left"), node("right", "p-right"), node("merge", "p-merge")},
		Order: []string{"left", "right", "merge"},
		Edges: []variantspec.Edge{
			{FromNodeID: "left", ToNodeID: "merge", Kind: "data"},
			{FromNodeID: "right", ToNodeID: "merge", Kind: "data"},
		},
		GraphGroups: []variantspec.GraphGroup{{
			Nodes: []string{"left", "right"}, Concurrent: true,
			Merge: &variantspec.Merge{Into: "merge", Strategy: variantspec.MergeNamespaced,
				OnNodeFailure: variantspec.FailFast},
		}},
	}
}

// jitterModel answers with fixed content after a VARYING delay, so a merge that recorded completion
// order would produce a different document on almost every run.
type jitterModel struct {
	mu        sync.Mutex
	n         int
	edges     [][2]string
	narrative string
}

func (m *jitterModel) Infer(context.Context, Input) (RawResult, providercall.Usage, error) {
	m.mu.Lock()
	m.n++
	spins := (m.n * 7919) % 2000
	m.mu.Unlock()
	// A busy wait rather than a sleep: it varies with real scheduling pressure, which is what a racy
	// merge is sensitive to, and it introduces no second clock.
	acc := 0
	for i := 0; i < spins*100; i++ {
		acc += i % 3
	}
	_ = acc
	conf := 0.9
	out := RawResult{Narrative: m.narrative}
	for _, e := range m.edges {
		out.Edges = append(out.Edges, RawEdge{From: e[0], To: e[1], Kind: "data", Confidence: &conf})
	}
	return out, providercall.Usage{InputTokens: 1, OutputTokens: 1}, nil
}

// sideEffectModel runs a callback on its first call — used to stage an activation mid-assessment.
type sideEffectModel struct {
	onCall func()
	edges  [][2]string
	calls  int
	mu     sync.Mutex
}

func (m *sideEffectModel) Infer(context.Context, Input) (RawResult, providercall.Usage, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.onCall != nil {
		m.onCall()
	}
	conf := 0.9
	out := RawResult{Narrative: "n"}
	for _, e := range m.edges {
		out.Edges = append(out.Edges, RawEdge{From: e[0], To: e[1], Kind: "data", Confidence: &conf})
	}
	return out, providercall.Usage{InputTokens: 1, OutputTokens: 1}, nil
}

var _ = discovery.IRNode{}
var _ = registry.LoopSpec{}
