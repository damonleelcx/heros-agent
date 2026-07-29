package variantspec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// fakeRegistries is an in-memory Registries. Resolve's contract is "fail closed on anything that does
// not resolve", so what these tests need is control over what resolves — not a database.
type fakeRegistries struct {
	models   map[string]*registry.ModelEntry
	prompts  map[string]*registry.PromptEntry
	skills   map[string]*registry.SkillEntry
	contexts map[string]*registry.ContextEntry
	memories map[string]*registry.MemoryEntry
	// calls counts lookups, so a test can prove resolution ABORTED rather than carried on.
	calls int
}

func newFakeRegistries() *fakeRegistries {
	return &fakeRegistries{
		models:   map[string]*registry.ModelEntry{},
		prompts:  map[string]*registry.PromptEntry{},
		skills:   map[string]*registry.SkillEntry{},
		contexts: map[string]*registry.ContextEntry{},
		memories: map[string]*registry.MemoryEntry{},
	}
}

func (f *fakeRegistries) ResolveModel(_ context.Context, id string) (*registry.ModelEntry, error) {
	f.calls++
	if e, ok := f.models[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
func (f *fakeRegistries) ResolvePrompt(_ context.Context, id string) (*registry.PromptEntry, error) {
	f.calls++
	if e, ok := f.prompts[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
func (f *fakeRegistries) ResolveSkill(_ context.Context, id string) (*registry.SkillEntry, error) {
	f.calls++
	if e, ok := f.skills[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
func (f *fakeRegistries) ResolveContextPolicy(_ context.Context, id string) (*registry.ContextEntry, error) {
	f.calls++
	if e, ok := f.contexts[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}

// ResolveMemory looks up ONLY the memory map (P17). Its own map, and not a shared one keyed by id, is
// what makes the cross-dimension fail-closed check meaningful in these tests: a context ref handed here
// must miss, exactly as it would against the real store, where the Kind is part of the content address.
func (f *fakeRegistries) ResolveMemory(_ context.Context, id string) (*registry.MemoryEntry, error) {
	f.calls++
	if e, ok := f.memories[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}

// addMemory seals a real memory entry into the fake registry — sealed, not hand-built, so the version_id
// the tests resolve is the one the production seal path would produce for the same content.
func (f *fakeRegistries) addMemory(t *testing.T, ref, name, strategy, params string) *registry.MemoryEntry {
	t.Helper()
	st := registry.MemoryStrategyNamed(strategy)
	if st == nil {
		t.Fatalf("addMemory: %q is not a builtin strategy", strategy)
	}
	e := &registry.MemoryEntry{
		VersionID: ref,
		Name:      name,
		Spec:      registry.MemorySpec{Strategy: strategy, Params: json.RawMessage(params)},
		Strategy:  st,
	}
	f.memories[ref] = e
	return e
}

func ptrF(f float64) *float64 { return &f }
func ptrI(i int) *int         { return &i }

// testIR is a two-node graph standing in for what P1 discovers: node A has a statically-resolved
// model, node B does not (the IR's honest "unresolved" sentinel).
func testIR() *discovery.IR {
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{
			{
				NodeID: "n_a", Kind: "static_definition",
				CallSite:        discovery.IRCallSite{File: "pipeline.go", Symbol: "classify", LineStart: 7, LineEnd: 7},
				Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-opus-4-8", Params: map[string]any{}},
				Prompt:          discovery.IRPrompt{Inline: "classify this", Variables: []string{}},
				ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			},
			{
				NodeID: "n_b", Kind: "static_definition",
				CallSite:        discovery.IRCallSite{File: "pipeline.go", Symbol: "agent", LineStart: 13, LineEnd: 13},
				Model:           discovery.IRModel{Provider: "anthropic", ModelID: "unresolved", Params: map[string]any{}},
				Prompt:          discovery.IRPrompt{Inline: "", Variables: []string{}},
				ToolsSkills:     []string{"search"},
				ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			},
		},
		Edges: []discovery.IREdge{},
	}
}

func baseSpec() *VariantSpec {
	return &VariantSpec{
		WorkflowID:     "wf1",
		SourceRevision: "abc123",
		Order:          []string{"n_a", "n_b"},
		Nodes:          map[string]NodeOverride{},
		Edges:          []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}},
	}
}

func modelEntry(id, provider, modelID string) *registry.ModelEntry {
	return &registry.ModelEntry{VersionID: id, Name: "m",
		Spec: registry.ModelSpec{Provider: provider, ModelID: modelID,
			Params: registry.ModelParams{Temperature: ptrF(0.2), MaxTokens: ptrI(1024)}}}
}

// ── The merge (2.5/2.6) ──────────────────────────────────────────────────────────────────────────

// A spec with no overrides at all still resolves to a COMPLETE configuration: the dimensions nobody
// overrode are pinned by source_revision, because they are properties of the code.
func TestResolve_EmptySpecResolvesToTheDiscoveredDefaults(t *testing.T) {
	got, err := Resolve(context.Background(), baseSpec(), testIR(), newFakeRegistries())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Config.Nodes) != 2 {
		t.Fatalf("want 2 resolved nodes, got %d", len(got.Config.Nodes))
	}
	n := got.Config.Nodes[0]
	if n.ModelRef != "anthropic/claude-opus-4-8" {
		t.Errorf("model_ref = %q, want the discovered binding", n.ModelRef)
	}
	if !strings.HasPrefix(n.PromptRef, "inline://") {
		t.Errorf("prompt_ref = %q, want an inline:// content address", n.PromptRef)
	}
	if got.ConfigHash == "" || len(got.ConfigHash) != 64 {
		t.Errorf("config_hash = %q, want 64 hex chars", got.ConfigHash)
	}
	// No overrides means the Transform Engine has nothing to edit.
	if len(got.Overrides) != 0 {
		t.Errorf("a spec with no overrides produced %d overrides", len(got.Overrides))
	}
}

// FR2: overriding one dimension must not disturb the others.
func TestResolve_ModelOnlyOverrideLeavesOtherDimensionsAtTheirDiscoveredValues(t *testing.T) {
	regs := newFakeRegistries()
	regs.models["m1"] = modelEntry("m1", "openai", "gpt-5")
	spec := baseSpec()
	spec.Nodes["n_a"] = NodeOverride{ModelRef: "m1"}

	got, err := Resolve(context.Background(), spec, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	n := got.Config.Nodes[0]
	if n.ModelRef != "openai/gpt-5" {
		t.Errorf("model_ref = %q, want the override", n.ModelRef)
	}
	if !strings.HasPrefix(n.PromptRef, "inline://") {
		t.Errorf("prompt was disturbed by a model-only override: %q", n.PromptRef)
	}
	if n.ContextPolicy != "inline_messages" {
		t.Errorf("context was disturbed by a model-only override: %q", n.ContextPolicy)
	}
	// The Transform Engine is told to edit exactly one dimension.
	dims := got.Overrides["n_a"].Dimensions()
	if len(dims) != 1 || dims[0] != DimModel {
		t.Errorf("overridden dimensions = %v, want [model] only", dims)
	}
	if _, ok := got.Overrides["n_b"]; ok {
		t.Error("an untouched node appeared in the override set; its call site must not be edited")
	}
}

// FR4: config_hash changes iff a referenced version or the ordering changes.
func TestResolve_ConfigHashSensitivity(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	regs.models["m1"] = modelEntry("m1", "openai", "gpt-5")
	regs.models["m2"] = modelEntry("m2", "anthropic", "claude-sonnet-5")

	base, err := Resolve(ctx, baseSpec(), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	t.Run("same spec resolves to the same hash", func(t *testing.T) {
		again, err := Resolve(ctx, baseSpec(), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if again.ConfigHash != base.ConfigHash {
			t.Errorf("resolution is not deterministic: %s vs %s", again.ConfigHash, base.ConfigHash)
		}
	})

	t.Run("a referenced version changes the hash", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_a"] = NodeOverride{ModelRef: "m1"}
		got, err := Resolve(ctx, s, testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.ConfigHash == base.ConfigHash {
			t.Error("overriding a model did not change config_hash")
		}

		s2 := baseSpec()
		s2.Nodes["n_a"] = NodeOverride{ModelRef: "m2"}
		got2, err := Resolve(ctx, s2, testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got2.ConfigHash == got.ConfigHash {
			t.Error("pointing at a different model version did not change config_hash")
		}
	})

	t.Run("the ordering changes the hash", func(t *testing.T) {
		s := baseSpec()
		s.Order = []string{"n_b", "n_a"}
		s.Edges = []Edge{{FromNodeID: "n_b", ToNodeID: "n_a", Kind: "data"}}
		got, err := Resolve(ctx, s, testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.ConfigHash == base.ConfigHash {
			t.Error("reordering the graph did not change config_hash")
		}
	})

	t.Run("source_revision alone does not change the hash", func(t *testing.T) {
		// It is a separate reproducibility axis (PRD §7); the transform is keyed by the PAIR.
		s := baseSpec()
		s.SourceRevision = "def456"
		got, err := Resolve(ctx, s, testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.ConfigHash != base.ConfigHash {
			t.Error("source_revision leaked into config_hash; it is a separate axis and the " +
				"transform is keyed by (config_hash, source_revision)")
		}
	})
}

// Two model entries differing ONLY in seed must share a config_hash, so P4's multi-seed runs roll up
// under one configuration (config-hash-spec §5). This is the observable consequence of the FR9/P0
// tension resolved in providerParams().
func TestResolve_ModelsDifferingOnlyInSeedShareAConfigHash(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	s1, s2 := int64(1), int64(2)
	a := modelEntry("m_seed1", "openai", "gpt-5")
	a.Spec.Params.Seed = &s1
	b := modelEntry("m_seed2", "openai", "gpt-5")
	b.Spec.Params.Seed = &s2
	regs.models["m_seed1"], regs.models["m_seed2"] = a, b

	specA := baseSpec()
	specA.Nodes["n_a"] = NodeOverride{ModelRef: "m_seed1"}
	specB := baseSpec()
	specB.Nodes["n_a"] = NodeOverride{ModelRef: "m_seed2"}

	ra, err := Resolve(ctx, specA, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rb, err := Resolve(ctx, specB, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ra.ConfigHash != rb.ConfigHash {
		t.Errorf("two models differing only in seed got different config_hashes (%s vs %s); "+
			"multi-seed results would no longer roll up under one configuration",
			Display(ra.ConfigHash), Display(rb.ConfigHash))
	}
}

// ── Fail closed (2.6 / FR5 / FR11) ───────────────────────────────────────────────────────────────

func TestResolve_RejectsUnknownNode(t *testing.T) {
	regs := newFakeRegistries()
	spec := baseSpec()
	spec.Order = append(spec.Order, "n_ghost")

	_, err := Resolve(context.Background(), spec, testIR(), regs)
	if !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("want ErrUnknownNode, got %v", err)
	}
	var se *SpecError
	if !errors.As(err, &se) || se.NodeID != "n_ghost" {
		t.Errorf("the rejection must name the node, got: %v", err)
	}
}

// The headline fail-closed case: an unresolved ref aborts BEFORE anything downstream, and the error
// names the node and the dimension.
func TestResolve_RejectsDanglingRefNamingNodeAndDimension(t *testing.T) {
	cases := []struct {
		name     string
		override NodeOverride
		wantDim  Dimension
	}{
		{"model", NodeOverride{ModelRef: "nope"}, DimModel},
		{"prompt", NodeOverride{PromptRef: "nope"}, DimPrompt},
		{"skills", NodeOverride{SkillRefs: []string{"nope"}}, DimSkills},
		{"context", NodeOverride{ContextPolicy: "nope"}, DimContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := baseSpec()
			spec.Nodes["n_a"] = tc.override

			got, err := Resolve(context.Background(), spec, testIR(), newFakeRegistries())
			if err == nil {
				t.Fatalf("a dangling %s ref resolved: %+v", tc.name, got)
			}
			if !errors.Is(err, ErrUnresolvedRef) {
				t.Fatalf("want ErrUnresolvedRef, got %v", err)
			}
			var se *SpecError
			if !errors.As(err, &se) {
				t.Fatalf("want a *SpecError, got %T", err)
			}
			if se.NodeID != "n_a" || se.Dim != tc.wantDim || se.Ref != "nope" {
				t.Errorf("the rejection must name node, dimension, and ref; got node=%q dim=%q ref=%q",
					se.NodeID, se.Dim, se.Ref)
			}
			if got != nil {
				t.Error("a rejected spec must not produce a Resolved")
			}
		})
	}
}

// "Fails closed" means it stops, not that it collects errors. A spec with a bad ref on the first node
// must not go on resolving the rest — the point of aborting early is that nothing downstream ever
// sees a half-resolved config.
func TestResolve_AbortsOnFirstFailureWithoutResolvingTheRest(t *testing.T) {
	regs := newFakeRegistries()
	regs.models["m1"] = modelEntry("m1", "openai", "gpt-5")
	spec := baseSpec()
	spec.Nodes["n_a"] = NodeOverride{ModelRef: "nope"} // fails
	spec.Nodes["n_b"] = NodeOverride{ModelRef: "m1"}   // would succeed

	if _, err := Resolve(context.Background(), spec, testIR(), regs); err == nil {
		t.Fatal("expected rejection")
	}
	if regs.calls != 1 {
		t.Errorf("resolution made %d registry lookups after failing on the first; it must abort", regs.calls)
	}
}

// FR3: refs are version IDs only. A spec that inlines a definition has content living outside every
// registry, so its config_hash could never be resolved back to bytes — the one thing lineage exists
// for. The type system does most of this work (NodeOverride has only string refs), so what is left to
// check is that the JSON shape rejects an inlined object rather than quietly ignoring it.
func TestVariantSpec_InlineDefinitionIsRejectedByTheWireShape(t *testing.T) {
	raw := `{"workflow_id":"wf1","source_revision":"abc","order":["n_a"],
	         "nodes":{"n_a":{"model_ref":{"provider":"openai","model_id":"gpt-5"}}}}`
	var spec VariantSpec
	err := json.Unmarshal([]byte(raw), &spec)
	if err == nil {
		t.Fatal("a spec inlining a model definition was accepted; every ref must be a version_id")
	}
}

// ── Structural validation ────────────────────────────────────────────────────────────────────────

func TestValidate_RejectsMalformedSpecs(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*VariantSpec)
	}{
		{"no source_revision", func(s *VariantSpec) { s.SourceRevision = "" }},
		{"empty order", func(s *VariantSpec) { s.Order = nil }},
		{"duplicate node in order", func(s *VariantSpec) { s.Order = []string{"n_a", "n_a"} }},
		{"override for a node not in order", func(s *VariantSpec) {
			s.Nodes["n_ghost"] = NodeOverride{ModelRef: "m1"}
		}},
		{"edge from a node not in order", func(s *VariantSpec) {
			s.Edges = []Edge{{FromNodeID: "n_ghost", ToNodeID: "n_a", Kind: "data"}}
		}},
		{"edge with a bad kind", func(s *VariantSpec) {
			s.Edges = []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "sideways"}}
		}},
		{"duplicate skill binding", func(s *VariantSpec) {
			s.Nodes["n_a"] = NodeOverride{SkillRefs: []string{"s1", "s1"}}
		}},
		{"empty skill ref", func(s *VariantSpec) {
			s.Nodes["n_a"] = NodeOverride{SkillRefs: []string{""}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := baseSpec()
			tc.mut(s)
			if err := s.Validate(); err == nil {
				t.Fatalf("Validate accepted a spec with: %s", tc.name)
			} else if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("want ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestValidate_AcceptsAWellFormedSpec(t *testing.T) {
	if err := baseSpec().Validate(); err != nil {
		t.Fatalf("a well-formed spec was rejected: %v", err)
	}
}

// Error messages must not vary run to run: map iteration order leaking into a rejection would make
// the same bad spec report a different node each time.
func TestValidate_ErrorMessageIsDeterministic(t *testing.T) {
	var first string
	for i := 0; i < 50; i++ {
		s := baseSpec()
		s.Nodes["z_ghost"] = NodeOverride{ModelRef: "m1"}
		s.Nodes["a_ghost"] = NodeOverride{ModelRef: "m1"}
		err := s.Validate()
		if err == nil {
			t.Fatal("expected rejection")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("rejection text varies between runs (map order leaked):\n %s\n %s", first, err.Error())
		}
	}
}

func TestRefs_ReturnsEveryPinnedVersionSortedAndDeduplicated(t *testing.T) {
	s := baseSpec()
	s.Nodes["n_a"] = NodeOverride{ModelRef: "m1", PromptRef: "p1", SkillRefs: []string{"s2", "s1"}, ContextPolicy: "c1"}
	s.Nodes["n_b"] = NodeOverride{ModelRef: "m1"} // duplicate m1
	got := s.Refs()
	want := []string{"c1", "m1", "p1", "s1", "s2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Refs() = %v, want %v", got, want)
	}
}
