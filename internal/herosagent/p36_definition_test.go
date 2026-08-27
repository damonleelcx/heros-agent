package herosagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// p36_definition_test.go is §3 — the definition itself: nine axes, node identity as data, the narrowed
// wiring refusal, the loop axis refused at publish, and ErrNoChange still refusing a duplicate.

// 🔴 §3.2 — `AuthorableAxes()` RETURNS NINE, and they are the product's nine rather than this
// package's own list.
func TestAuthorableAxesAreTheProductsNine(t *testing.T) {
	got := AuthorableAxes()
	if len(got) != 9 {
		t.Fatalf("AuthorableAxes returned %d: %v. P36 is the phase that makes it nine — `loop` and "+
			"`graph` join the seven.", len(got), got)
	}
	want := map[Axis]bool{
		AxisModel: true, AxisPrompt: true, AxisSkills: true, AxisContext: true, AxisTools: true,
		AxisMemory: true, AxisHarness: true, AxisLoop: true, AxisGraph: true,
	}
	for _, a := range got {
		if !want[a] {
			t.Errorf("AuthorableAxes names %q, which is not one of the nine", a)
		}
		delete(want, a)
	}
	for a := range want {
		t.Errorf("AuthorableAxes omits %q", a)
	}

	// 🔴 DERIVED from `variantspec.Dimensions()`, not written down twice. A dimension added there and
	// missing here would be an axis the platform's own agent cannot be configured on, discovered by
	// whoever tried to configure it.
	for _, d := range variantspec.Dimensions() {
		if _, ok := want[Axis(d)]; ok {
			t.Errorf("variantspec declares dimension %q and AuthorableAxes does not carry it", d)
		}
	}
	// And `graph` is NOT a Dimension — it is a property BETWEEN nodes (P34 design D3). If it ever
	// becomes one, `AuthorableAxes` would name it twice, which is exactly how P34's own `loop` bug
	// surfaced.
	seen := map[Axis]int{}
	for _, a := range got {
		seen[a]++
	}
	for a, n := range seen {
		if n > 1 {
			t.Errorf("AuthorableAxes names %q %d times", a, n)
		}
	}
	// `graph` is definition-level, so it must NOT be in the per-node set.
	for _, a := range PerNodeAxes() {
		if a == AxisGraph {
			t.Error("`graph` is in PerNodeAxes. Topology is a property BETWEEN nodes; a per-node graph " +
				"axis would let an operator believe one node owns the graph.")
		}
	}
}

// 🔴 §3.2 — `loop` and `graph` are REFERENCES AND DECLARATIONS, never inlined.
func TestLoopAndGraphAreReferencesNeverInlined(t *testing.T) {
	// The loop axis is a registry version_id on the node, exactly like the other seven refs. The
	// structural proof is that `Node` has NO field able to hold a strategy's params: an inlined loop
	// would be a configuration whose content lives outside any registry, unresolvable from a stored
	// config_hash months later.
	b, err := json.Marshal(Node{NodeID: "n", LoopRef: "loop-v1"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, inlined := range []string{"loop_params", "loop_strategy", "strategy", "params",
		"max_turns", "stop_condition"} {
		if _, present := doc[inlined]; present {
			t.Errorf("a node carries %q inline. Every axis is a REFERENCE: an inlined strategy is a "+
				"configuration that cannot be resolved back from a config_hash.", inlined)
		}
	}
	if doc["loop_ref"] != "loop-v1" {
		t.Errorf("the loop axis did not serialise as a ref: %s", b)
	}

	// The graph axis is a DECLARATION over ids the definition already declares — `variantspec.Edge` and
	// `variantspec.GraphGroup`, the customer's own types, unchanged. Carrying a private copy is how a
	// second validator begins (design D1).
	d := twoNodeDefinition()
	if _, ok := any(d.Edges).([]variantspec.Edge); !ok {
		t.Error("the agent's edges are not variantspec.Edge — a private edge type is where the agent's " +
			"topology quietly acquires different semantics from a customer's")
	}
	if _, ok := any(d.GraphGroups).([]variantspec.GraphGroup); !ok {
		t.Error("the agent's graph groups are not variantspec.GraphGroup")
	}
}

// 🔴 §3.4 / §9.6 — THE WIRING REFUSAL NARROWED. A single-node definition STILL refuses an ordering;
// a multi-node one is validated rather than refused.
func TestTheOrderingRefusalNarrowsRatherThanDisappearing(t *testing.T) {
	single := goodDefinition()
	single.Order = []string{DefaultNodeID}
	err := single.Validate()
	if !errors.Is(err, ErrWiringOverride) {
		t.Fatalf("a single-node definition carrying an ordering was accepted (%v). The reason the "+
			"refusal always gave — there is no second node to order it against — is STILL TRUE for one "+
			"node, and one node is still the default.", err)
	}
	if !strings.Contains(err.Error(), "second node") {
		t.Errorf("the refusal no longer states its reason: %v", err)
	}

	// The same shape with a second node is VALIDATED rather than refused.
	multi := twoNodeDefinition()
	if err := multi.Validate(); err != nil {
		t.Fatalf("a multi-node definition with an ordering was refused: %v", err)
	}

	// 🔴 ANTI-VACUITY: multi-node validation is real, not a bypass. A bad ordering is still refused.
	bad := twoNodeDefinition()
	bad.Order = []string{"analyst"} // `critic` is declared and absent from the ordering
	if err := bad.Validate(); err == nil {
		t.Error("a multi-node definition whose ordering omits a declared node was accepted. Concurrency " +
			"is declared OVER the ordering, never instead of it — a node outside it never runs.")
	}
	dup := twoNodeDefinition()
	dup.Order = []string{"analyst", "analyst", "critic"}
	if err := dup.Validate(); err == nil {
		t.Error("an ordering naming a node twice was accepted; it would silently run it twice")
	}
}

// 🔴 §3.4 — the legacy `wiring` SPELLING is refused BY NAME with the rename stated, never translated.
func TestTheLegacyWiringSpellingIsRefusedByNameRatherThanTranslated(t *testing.T) {
	_, err := DefinitionFromAxes(AxisEdit{AxisWiring: "a,b"}, ListEdit{}, "anthropic")
	if !errors.Is(err, ErrWiringOverride) {
		t.Fatalf("an edit naming the pre-P36 `wiring` axis produced %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"wiring", "graph"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so a caller cannot find the new name: %s", want, msg)
		}
	}
	// 🚫 And it is NOT accepted as the graph axis. A rename that quietly accepts the old spelling never
	// finishes: both names live on, and the noun dictionary has two entries for one axis.
	if err == nil {
		t.Error("the legacy spelling was translated rather than refused")
	}
}

// 🔴 §3.5 / §9.8 — A LOOP WHOSE HOST SERVICE NOTHING SUPPLIES IS REFUSED AT PUBLISH, NOT AT RUN.
func TestALoopNeedingAnUnavailableHostServiceIsRefusedAtPublish(t *testing.T) {
	ctx := context.Background()
	d := goodDefinition()
	d.Nodes[0].LoopRef = "loop-critic-v1"

	pub, _ := p36Publisher(t, RunnerHosts{}, fakeAxisRegistry{
		loops: map[string]*registry.LoopEntry{
			"loop-critic-v1": {VersionID: "loop-critic-v1", Spec: registry.LoopSpec{Strategy: "critic-loop"}},
		},
		harnesses: map[string]*registry.HarnessEntry{
			"harness-single-shot-v1": envelopeEntry(t, "harness-single-shot-v1", nil, 0),
		},
	})
	_, err := pub.Publish(ctx, d)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("a critic-loop published on a deployment with no critic produced %v. It is refused at "+
			"PUBLISH so the operator who chose it reads the refusal — at run it is read by somebody who "+
			"did not choose it and cannot tell a bug from a configuration.", err)
	}
	msg := err.Error()
	for _, want := range []string{"critic-loop", "critic"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "reflexion") {
		t.Errorf("the refusal does not say why it is not degraded to the neighbouring strategy — which "+
			"is the substitution somebody will otherwise make: %s", msg)
	}

	// 🔴 ANTI-VACUITY: the SAME definition publishes on a deployment that supplies the service.
	pubOK, _ := p36Publisher(t, RunnerHosts{Critic: true}, fakeAxisRegistry{
		loops: map[string]*registry.LoopEntry{
			"loop-critic-v1": {VersionID: "loop-critic-v1", Spec: registry.LoopSpec{Strategy: "critic-loop"}},
		},
		harnesses: map[string]*registry.HarnessEntry{
			"harness-single-shot-v1": envelopeEntry(t, "harness-single-shot-v1",
				[]string{registry.HostServiceCritic}, 0),
		},
	})
	if _, err := pubOK.Publish(ctx, d); err != nil {
		t.Errorf("a critic-loop was refused on a deployment that DOES supply a critic: %v. The fence "+
			"above is refusing everything, which passes and breaks the loop axis entirely.", err)
	}
}

// 🔴 §3.5 — a loop bound with NO axis registry wired is REFUSED, not published-and-hoped.
func TestALoopIsRefusedWhenNothingCanValidateIt(t *testing.T) {
	d := goodDefinition()
	d.Nodes[0].LoopRef = "loop-anything"
	pub, _ := p36Publisher(t, RunnerHosts{}, nil)
	_, err := pub.Publish(context.Background(), d)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("a loop was published on a deployment that cannot resolve it (%v). Publishing defers "+
			"the host-service check to whoever an analysis reaches — the failure the check exists to "+
			"move left.", err)
	}
	// 🔴 ANTI-VACUITY: a definition binding NO loop still publishes with no registry. Every pre-P36
	// definition is that shape, and requiring a registry to publish them would break a working path.
	if _, err := pub.Publish(context.Background(), goodDefinition()); err != nil {
		t.Errorf("a definition binding no loop was refused for want of an axis registry: %v", err)
	}
}

// 🔴 §3.6 — A LOOP EXCEEDING ITS NODE'S ENVELOPE CEILING IS REFUSED AT PUBLISH, NAMING BOTH VALUES.
func TestALoopOverItsEnvelopeCeilingIsRefusedNamingBothValues(t *testing.T) {
	ctx := context.Background()
	d := goodDefinition()
	d.Nodes[0].LoopRef = "loop-reflexion-v1"

	pub, _ := p36Publisher(t, RunnerHosts{}, fakeAxisRegistry{
		loops: map[string]*registry.LoopEntry{
			"loop-reflexion-v1": {VersionID: "loop-reflexion-v1", Spec: registry.LoopSpec{
				Strategy: "reflexion", Params: json.RawMessage(`{"max_turns":9}`)}},
		},
		harnesses: map[string]*registry.HarnessEntry{
			"harness-single-shot-v1": envelopeEntry(t, "harness-single-shot-v1", nil, 4),
		},
	})
	_, err := pub.Publish(ctx, d)
	if err == nil {
		t.Fatal("a loop asking for 9 turns published under an envelope whose ceiling is 4")
	}
	msg := err.Error()
	// 🔴 BOTH NUMBERS. "Too many turns" leaves an operator unable to tell whether to lower their value
	// or ask for a higher policy — and those are requests to two different people.
	for _, want := range []string{"9", "4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q. The ceiling is IMPOSED and the turn count is CHOSEN; "+
				"without both numbers the reader cannot tell which to change: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "turn_ceiling") {
		t.Errorf("the refusal does not name the envelope field to ask about: %s", msg)
	}
}

// 🔴 §3.7 / ErrNoChange — content determines identity, so republishing an identical definition creates
// NO second version. Unchanged by the shape change, for single-node AND multi-node.
func TestRepublishingAnIdenticalDefinitionCreatesNoSecondVersion(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		def  Definition
	}{
		{"single node", goodDefinition()},
		{"multi node", twoNodeDefinition()},
	} {
		pub, store := p36Publisher(t, RunnerHosts{}, nil)
		first, err := pub.Publish(ctx, c.def)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !first.Created {
			t.Fatalf("%s: the first publish created nothing", c.name)
		}
		second, err := pub.Publish(ctx, c.def)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if second.Created {
			t.Errorf("%s: republishing an identical definition created a SECOND version. A duplicate row "+
				"gives an operator two identities for one configuration to reason about.", c.name)
		}
		if second.ConfigHash != first.ConfigHash {
			t.Errorf("%s: two publishes of one definition produced two hashes: %s and %s",
				c.name, first.ConfigHash, second.ConfigHash)
		}
		all, err := store.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 {
			t.Errorf("%s: the store holds %d versions after two identical publishes", c.name, len(all))
		}
	}
}

// 🔴 §3.8 — an inference names the NODE and the DEFINITION VERSION that produced it.
func TestAnInferenceNamesTheNodeAndTheDefinitionVersionThatProducedIt(t *testing.T) {
	ctx := context.Background()
	store := NewMemInferenceStore()
	st := Stored{
		InferenceID: "inf-1", WorkflowID: "wf", SourceRevision: "rev-1", AgentConfigHash: "cfg-1",
		Edges: []ProvenancedEdge{{From: "a", To: "b", Kind: "data", Confidence: 0.9,
			ProducedByNode: "critic"}},
		Nodes: []NodeRun{
			{NodeID: "analyst", ProviderCalls: 1, TokensIn: 10, TokensOut: 4, Edges: 0},
			{NodeID: "critic", ProviderCalls: 1, TokensIn: 12, TokensOut: 6, Edges: 1},
		},
	}
	if err := store.Put(ctx, st); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, "wf", "rev-1", "cfg-1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// The definition version: already the pin's own key.
	if got.AgentConfigHash != "cfg-1" {
		t.Errorf("the inference does not name its definition version: %q", got.AgentConfigHash)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("the per-node record did not survive the store: %+v", got.Nodes)
	}
	// And a customer-visible FINDING resolves to a node.
	if got.Edges[0].ProducedByNode != "critic" {
		t.Errorf("an edge does not name the node that produced it: %+v. `agent_config_hash` already "+
			"answered WHICH DEFINITION; the question a graph creates is which of its nodes wrote the "+
			"edge somebody is disputing.", got.Edges[0])
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

// twoNodeDefinition is the smallest real graph: an analyst and a critic, ordered, with an edge.
func twoNodeDefinition() Definition {
	return Definition{
		Nodes: []Node{
			{NodeID: "analyst", PromptRef: "prompt-v1", ModelRef: "claude-opus-5",
				CredentialRef: "anthropic", ContextRef: "ctx-v1", HarnessRef: "harness-single-shot-v1"},
			{NodeID: "critic", PromptRef: "prompt-v2", ModelRef: "claude-opus-5",
				CredentialRef: "anthropic", ContextRef: "ctx-v1", HarnessRef: "harness-single-shot-v1"},
		},
		Order: []string{"analyst", "critic"},
		Edges: []variantspec.Edge{{FromNodeID: "analyst", ToNodeID: "critic", Kind: "data"}},
	}
}

// p36Publisher wires a publisher over the fakes, with an optional axis registry.
func p36Publisher(t *testing.T, hosts RunnerHosts, axes AxisRegistry) (*Publisher, *MemVersionStore) {
	t.Helper()
	store := NewMemVersionStore()
	pub, err := NewPublisher(
		fakeCatalogue{models: []RegisteredModel{{ModelID: "claude-opus-5", Provider: "anthropic"}}},
		fakeSecrets{known: map[string]bool{"anthropic": true}}, store, hosts,
		func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	if axes != nil {
		pub = pub.WithAxisRegistry(axes)
	}
	return pub, store
}

// fakeAxisRegistry resolves loop and harness entries from maps.
type fakeAxisRegistry struct {
	loops     map[string]*registry.LoopEntry
	harnesses map[string]*registry.HarnessEntry
}

func (f fakeAxisRegistry) ResolveLoop(_ context.Context, id string) (*registry.LoopEntry, error) {
	if e, ok := f.loops[id]; ok {
		return e, nil
	}
	return nil, errors.New("no such loop entry")
}

func (f fakeAxisRegistry) ResolveHarness(_ context.Context, id string) (*registry.HarnessEntry, error) {
	if e, ok := f.harnesses[id]; ok {
		return e, nil
	}
	return nil, errors.New("no such harness entry")
}

// envelopeEntry builds a harness ENVELOPE entry with the given host services and turn ceiling.
func envelopeEntry(t *testing.T, id string, services []string, ceiling int) *registry.HarnessEntry {
	t.Helper()
	params := map[string]any{
		"sandbox_posture":   "none",
		"spend_ceiling_usd": 1.0,
	}
	if services != nil {
		params["host_services"] = services
	}
	if ceiling > 0 {
		params["turn_ceiling"] = ceiling
	} else {
		params["turn_ceiling"] = nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return &registry.HarnessEntry{
		VersionID: id,
		Spec:      registry.HarnessSpec{Strategy: registry.StrategyEnvelope, Params: b},
	}
}
