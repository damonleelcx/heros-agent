package variantspec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P17 §4 — Steps 3 and 4: resolve and the ResolvedNode projection.
//
// The through-line of this file is decisions.md D3: `none` ≡ absent. Everything else — the dimension
// dispatch, the params projection, the hash sensitivity — hangs off getting that equality exactly right,
// because it is what keeps the P0 golden vectors reproducing and what lets a user back out of an authored
// memory change with no residue.

// resolveMemorySpec builds a one-node spec over testIR's node A with the given memory ref.
func resolveMemorySpec(ref string) *VariantSpec {
	s := &VariantSpec{
		SourceRevision: "rev1",
		Order:          []string{"n_a"},
		Nodes:          map[string]NodeOverride{},
	}
	if ref != "" {
		s.Nodes["n_a"] = NodeOverride{MemoryRef: ref}
	}
	return s
}

// TestDimensionsReportsMemoryIffSet — task 4.1. The transform iterates what Dimensions() reports, so this
// is where "a node that never mentioned memory is never touched" becomes mechanical.
func TestDimensionsReportsMemoryIffSet(t *testing.T) {
	t.Run("absent when not overridden", func(t *testing.T) {
		for _, d := range (ResolvedOverride{Model: &registry.ModelEntry{}}).Dimensions() {
			if d == DimMemory {
				t.Fatal("Dimensions() reports memory for an override that set none of it; the transform " +
					"would then dispatch a memory rewriter at a node the author never asked about")
			}
		}
	})

	t.Run("present when overridden", func(t *testing.T) {
		ro := ResolvedOverride{Memory: &registry.MemoryEntry{Spec: registry.MemorySpec{Strategy: "scratchpad"}}}
		dims := ro.Dimensions()
		if len(dims) != 1 || dims[0] != DimMemory {
			t.Fatalf("Dimensions() = %v, want exactly [memory]", dims)
		}
	})

	// 🔴 An explicit `none` IS an override. It hashes as absent (D3), but the transform still has to see
	// it — see ResolvedOverride.Memory's comment. If Dimensions() hid it, "the author asked for the
	// identity strategy" and "the author said nothing" would be indistinguishable downstream, and the
	// refusal boundary would be drawn on the wrong fact.
	t.Run("present for an explicit none", func(t *testing.T) {
		ro := ResolvedOverride{Memory: &registry.MemoryEntry{Spec: registry.MemorySpec{Strategy: registry.StrategyNone}}}
		found := false
		for _, d := range ro.Dimensions() {
			if d == DimMemory {
				found = true
			}
		}
		if !found {
			t.Fatal("an explicit `none` override is not reported as a dimension; the transform cannot then " +
				"tell it from no override at all")
		}
	})

	t.Run("resolution wires it end to end", func(t *testing.T) {
		regs := newFakeRegistries()
		regs.addMemory(t, "mem1", "notes", "scratchpad", `{"max_entries":5}`)
		got, err := Resolve(context.Background(), resolveMemorySpec("mem1"), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		ro, ok := got.Overrides["n_a"]
		if !ok {
			t.Fatal("the memory-only override did not survive into Resolved.Overrides; it would never reach " +
				"the transform, which is a silent drop by another name")
		}
		if ro.Memory == nil || ro.Memory.Spec.Strategy != "scratchpad" {
			t.Fatalf("resolved memory = %+v, want the scratchpad entry", ro.Memory)
		}
	})

	t.Run("a dangling memory ref fails closed naming the dimension", func(t *testing.T) {
		regs := newFakeRegistries()
		_, err := Resolve(context.Background(), resolveMemorySpec("nope"), testIR(), regs)
		if err == nil {
			t.Fatal("a dangling memory_ref resolved; resolution must fail closed before any diff exists")
		}
		if !errors.Is(err, ErrUnresolvedRef) {
			t.Fatalf("err = %v, want ErrUnresolvedRef", err)
		}
		var se *SpecError
		if !errors.As(err, &se) || se.Dim != DimMemory || se.Ref != "nope" {
			t.Fatalf("rejection = %#v, want a SpecError naming dimension memory and ref nope", err)
		}
	})
}

// TestMemoryInlineDefinitionRejected — task 5.6 🚫. The resolve-side half of the inline check, which is
// defense in depth for a caller that assembled a Resolve by hand. Both halves read one helper, so there
// is a single inline-vs-reference rule.
func TestMemoryInlineDefinitionRejected(t *testing.T) {
	regs := newFakeRegistries()
	spec := resolveMemorySpec(`{"strategy":"summary-buffer","params":{"max_tokens":2000}}`)
	_, err := Resolve(context.Background(), spec, testIR(), regs)
	if err == nil {
		t.Fatal("an inlined memory strategy resolved; a configuration whose content lives outside any " +
			"registry can never be resolved back from a config_hash, which is the one thing lineage exists to do")
	}
	if !errors.Is(err, ErrInlineDefinition) {
		t.Fatalf("err = %v, want ErrInlineDefinition (NOT ErrUnresolvedRef — the author declined to point "+
			"at an entry, which is a different mistake from pointing at a missing one)", err)
	}
	if regs.calls != 0 {
		t.Errorf("the registry was consulted %d time(s) for an inlined definition; there is nothing to look "+
			"up, and the rejection must precede the lookup", regs.calls)
	}
}

// TestResolvedNodeMemoryOmittedWhenNone — task 4.2. The projection: a node that binds no memory, and a
// node that binds `none`, both emit NO memory key.
func TestResolvedNodeMemoryOmittedWhenNone(t *testing.T) {
	t.Run("no override emits no key", func(t *testing.T) {
		b, err := json.Marshal(ResolvedNode{NodeID: "n", ModelRef: "m", PromptRef: "p",
			SkillRefs: []string{}, ContextPolicy: "full", ContextParams: map[string]any{}, ProviderParams: map[string]any{}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if bytesContain(b, `"memory"`) {
			t.Fatalf("a no-memory node emitted a memory key: %s\nEvery pre-P17 config_hash would change and "+
				"every keyed row would orphan (D3)", b)
		}
	})

	t.Run("an explicit none resolves to no key", func(t *testing.T) {
		regs := newFakeRegistries()
		regs.addMemory(t, "memNone", "off", registry.StrategyNone, `{}`)
		got, err := Resolve(context.Background(), resolveMemorySpec("memNone"), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Config.Nodes[0].Memory != nil {
			t.Fatalf("a `none` node projected memory %+v, want nil; `none` IS the absence of memory, so "+
				"projecting it would fork one configuration into two hashes", got.Config.Nodes[0].Memory)
		}
		// But the OVERRIDE is still recorded — resolved, not hashed.
		if got.Overrides["n_a"].Memory == nil {
			t.Error("the `none` override vanished from Resolved.Overrides; the transform must be able to see " +
				"that the author asked for something, even when it changes nothing")
		}
	})

	t.Run("a real strategy projects strategy and params", func(t *testing.T) {
		regs := newFakeRegistries()
		regs.addMemory(t, "mem1", "sum", "summary-buffer", `{"max_tokens":2000,"keep_last_turns":4}`)
		got, err := Resolve(context.Background(), resolveMemorySpec("mem1"), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		m := got.Config.Nodes[0].Memory
		if m == nil {
			t.Fatal("a real strategy projected no memory; the config_hash would then claim the base configuration")
		}
		if m.Strategy != "summary-buffer" {
			t.Errorf("strategy = %q, want summary-buffer", m.Strategy)
		}
		if m.Params["max_tokens"] != float64(2000) {
			t.Errorf("params = %v, want max_tokens 2000 decoded", m.Params)
		}
		// 🚫 The projection carries the STRATEGY, never the version_id: config_hash denotes a
		// CONFIGURATION, so two specs pinning different entries with identical content share a hash.
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if bytesContain(b, "mem1") {
			t.Errorf("the projection leaked the version_id: %s", b)
		}
	})
}

// TestNoneMemoryHashesAsAbsent — task 4.3 🔴 and the QA identity suite (task 11.2). The single most
// important assertion in the phase: if this breaks, every stored config_hash is wrong.
func TestNoneMemoryHashesAsAbsent(t *testing.T) {
	regs := newFakeRegistries()
	regs.addMemory(t, "memNone", "off", registry.StrategyNone, `{}`)

	noMemory, err := Resolve(context.Background(), resolveMemorySpec(""), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve (no memory): %v", err)
	}
	explicitNone, err := Resolve(context.Background(), resolveMemorySpec("memNone"), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve (explicit none): %v", err)
	}

	a, err := noMemory.Config.Canonical()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	b, err := explicitNone.Config.Canonical()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("a `none` node and a no-memory node canonicalize differently.\n  none: %s\nabsent: %s\n"+
			"`none` IS the absence of memory (D3); if the two spellings diverge, one configuration forks "+
			"into two config_hashes and a user cannot back out of an authored memory change", b, a)
	}
	if noMemory.ConfigHash != explicitNone.ConfigHash {
		t.Fatalf("config_hash differs: %s vs %s", noMemory.ConfigHash, explicitNone.ConfigHash)
	}

	// And the frozen P0 vectors still reproduce through the type that just grew a field. This is the
	// assertion that would catch an accidental always-present `memory` key.
	t.Run("the P0 golden vectors reproduce", TestGolden_ResolvedConfigReproducesFrozenBytesAndHash)
}

// TestConfigHashChangesIffMemoryChanges — task 4.4 / 11.3. Hash participation in both directions: it moves
// when the configuration moves, and it does not move when only presentation does.
func TestConfigHashChangesIffMemoryChanges(t *testing.T) {
	regs := newFakeRegistries()
	regs.addMemory(t, "scratch5", "notes", "scratchpad", `{"max_entries":5}`)
	regs.addMemory(t, "scratch9", "notes", "scratchpad", `{"max_entries":9}`)
	regs.addMemory(t, "sum2000", "sum", "summary-buffer", `{"max_tokens":2000}`)
	regs.addMemory(t, "scratch5reordered", "notes", "scratchpad", `{"max_entries":5}`)

	hashOf := func(t *testing.T, ref string) string {
		t.Helper()
		got, err := Resolve(context.Background(), resolveMemorySpec(ref), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", ref, err)
		}
		return got.ConfigHash
	}

	base := hashOf(t, "")
	scratch5 := hashOf(t, "scratch5")
	scratch9 := hashOf(t, "scratch9")
	sum2000 := hashOf(t, "sum2000")

	if scratch5 == base {
		t.Error("binding a memory strategy did not change the config_hash; the hash would then claim two " +
			"different computations are one, and every eval comparison would fragment")
	}
	if scratch5 == scratch9 {
		t.Error("changing max_entries did not change the config_hash; a params change IS a configuration change")
	}
	if scratch5 == sum2000 {
		t.Error("two different strategies produced the same config_hash")
	}

	// The other direction: two entries with the same strategy and params, differing only in their
	// version_id, denote ONE configuration and must share a hash. The projection is what guarantees it.
	if got := hashOf(t, "scratch5reordered"); got != scratch5 {
		t.Errorf("two memory entries with identical strategy and params hashed differently (%s vs %s); "+
			"config_hash denotes a CONFIGURATION, not a set of registry rows", got, scratch5)
	}

	// Params key ORDER is not identity-bearing — JCS sorts object keys, so the hash depends on the params
	// SET rather than the order they were written in.
	regs.addMemory(t, "sumA", "sum", "summary-buffer", `{"max_tokens":2000,"keep_last_turns":4}`)
	regs.addMemory(t, "sumB", "sum", "summary-buffer", `{"keep_last_turns":4,"max_tokens":2000}`)
	if hashOf(t, "sumA") != hashOf(t, "sumB") {
		t.Error("the same params written in a different key order hashed differently; JCS sorts keys, so " +
			"authoring order must not be identity-bearing")
	}
}

// TestMemoryRefResolvesAndHashesDespiteRefusal — task 7.5. The refusal is a property of the TRANSFORM
// only. This test lives here, on the resolve side, because that is where the claim has to hold.
func TestMemoryRefResolvesAndHashesDespiteRefusal(t *testing.T) {
	regs := newFakeRegistries()
	regs.addMemory(t, "mem1", "notes", "scratchpad", `{"max_entries":5}`)

	first, err := Resolve(context.Background(), resolveMemorySpec("mem1"), testIR(), regs)
	if err != nil {
		t.Fatalf("a spec carrying a MemoryRef failed to resolve: %v.\nD4 refuses at TRANSFORM, not at "+
			"resolve — blocking here would discard the modeling, hashing and proposal that are entirely safe", err)
	}
	if first.ConfigHash == "" {
		t.Fatal("the resolved spec produced no config_hash")
	}

	// Reproducible: the same spec resolves to the same hash every time, which is what makes an authored
	// memory change survivable — it is re-materializable unchanged the day the rewriter lands.
	second, err := Resolve(context.Background(), resolveMemorySpec("mem1"), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve (second): %v", err)
	}
	if first.ConfigHash != second.ConfigHash {
		t.Fatalf("the same spec resolved to two hashes: %s, %s", first.ConfigHash, second.ConfigHash)
	}
}

// TestMemoryContextDisjoint — task 11.5. The boundary suite: no memory construct is expressible as a
// context construct or vice versa (decisions.md D2). A structural check, because prose cannot enforce it.
func TestMemoryContextDisjoint(t *testing.T) {
	t.Run("the dimensions are distinct wire values", func(t *testing.T) {
		if DimMemory == DimContext {
			t.Fatal("DimMemory and DimContext share a wire value")
		}
	})

	t.Run("the override fields are distinct", func(t *testing.T) {
		o := NodeOverride{ContextPolicy: "ctx1"}
		if o.MemoryRef != "" {
			t.Fatal("setting ContextPolicy also set MemoryRef")
		}
		o = NodeOverride{MemoryRef: "mem1"}
		if o.ContextPolicy != "" {
			t.Fatal("setting MemoryRef also set ContextPolicy")
		}
		// And they hash to different keys, so neither can be read as the other by a consumer of the wire form.
		ctxBytes, _ := json.Marshal(NodeOverride{ContextPolicy: "x"})
		memBytes, _ := json.Marshal(NodeOverride{MemoryRef: "x"})
		if string(ctxBytes) == string(memBytes) {
			t.Fatalf("a context override and a memory override serialise identically: %s", ctxBytes)
		}
	})

	t.Run("a context ref does not resolve as memory", func(t *testing.T) {
		// The fake registry keeps separate maps precisely because the real store keeps separate id spaces
		// (the Kind is part of the content address). A context ref handed to the memory dimension MUST miss.
		regs := newFakeRegistries()
		regs.contexts["ctx1"] = &registry.ContextEntry{VersionID: "ctx1", Name: "full"}
		_, err := Resolve(context.Background(), resolveMemorySpec("ctx1"), testIR(), regs)
		if err == nil {
			t.Fatal("a context version_id resolved as a memory strategy; a within-call policy would then be " +
				"bound as a cross-invocation store")
		}
		if !errors.Is(err, ErrUnresolvedRef) {
			t.Fatalf("err = %v, want ErrUnresolvedRef", err)
		}
	})

	t.Run("a memory ref does not resolve as a context policy", func(t *testing.T) {
		regs := newFakeRegistries()
		regs.addMemory(t, "mem1", "notes", "scratchpad", `{"max_entries":5}`)
		s := &VariantSpec{SourceRevision: "rev1", Order: []string{"n_a"},
			Nodes: map[string]NodeOverride{"n_a": {ContextPolicy: "mem1"}}}
		_, err := Resolve(context.Background(), s, testIR(), regs)
		if err == nil {
			t.Fatal("a memory version_id resolved as a context policy")
		}
		if !errors.Is(err, ErrUnresolvedRef) {
			t.Fatalf("err = %v, want ErrUnresolvedRef", err)
		}
	})

	t.Run("the resolved projections are distinct keys", func(t *testing.T) {
		n := ResolvedNode{ContextPolicy: "full", ContextParams: map[string]any{"w": 1},
			Memory: &ResolvedMemory{Strategy: "scratchpad", Params: map[string]any{"max_entries": 5}}}
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !bytesContain(b, `"context_policy"`) || !bytesContain(b, `"memory"`) {
			t.Fatalf("the two axes are not both present as their own keys: %s", b)
		}
	})
}
