package variantspec

import (
	"encoding/json"
	"errors"
	"testing"
)

// P17 §3 — Steps 1 and 2 of the eight-step "add an axis" checklist: the Dimension const and the
// NodeOverride field. Everything here is about the AUTHORING contract; resolution, hashing, the
// registry, and the transform refusal live in their own files.

// TestDimensionEnumClosedIncludesMemory — task 3.1. DimMemory is a member of the CLOSED enum, and
// Dimensions() reports it. The cardinality assertion is the point: a seventh dimension added without
// updating this test is a change to a closed vocabulary, and it should have to be deliberate.
func TestDimensionEnumClosedIncludesMemory(t *testing.T) {
	dims := Dimensions()
	// 🔴 Widened from 6 to 7 by P18, which appended `harness` through the same eight-step checklist and
	// with its own decision record (P18 decisions.md D-2). The assertion stays a CARDINALITY assertion —
	// its whole job is to go red when a closed vocabulary grows — and it went red on exactly the change
	// it was written to catch. Bumping it is the deliberate act; deleting it would not be.
	const want = 7
	if len(dims) != want {
		t.Fatalf("Dimensions() has %d members, want %d — the enum is CLOSED; adding a dimension is a "+
			"deliberate act, not a silent one (got %v)", len(dims), want, dims)
	}

	seen := map[Dimension]bool{}
	for _, d := range dims {
		if seen[d] {
			t.Fatalf("Dimensions() reports %q twice; a duplicate would make the transform iterate it twice", d)
		}
		seen[d] = true
	}
	for _, d := range []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools, DimMemory, DimHarness} {
		if !seen[d] {
			t.Errorf("Dimensions() omits %q — a consumer iterating dimensions would silently miss it", d)
		}
	}
	if DimMemory != "memory" {
		t.Errorf("DimMemory = %q, want %q: the wire value is stored on error records and spec rows, so it is stable", DimMemory, "memory")
	}
	// 🔴 Disjoint from context (decisions.md D2). Memory persists ACROSS invocations; context is within
	// one call. If these two ever collapsed onto one wire value, a cross-session concern would resolve
	// as a within-call one.
	if DimMemory == DimContext {
		t.Fatal("DimMemory and DimContext share a wire value; they are disjoint axes (D2)")
	}
}

// TestNodeOverrideMemoryRefIsEmptyAndRefs — task 3.2. The field is wired into isEmpty (so a
// memory-only override is not mistaken for "this node runs as discovered") and into Refs (so the
// loader fails closed on a dangling memory ref, exactly as it does for a model or context ref).
func TestNodeOverrideMemoryRefIsEmptyAndRefs(t *testing.T) {
	t.Run("isEmpty", func(t *testing.T) {
		if !(NodeOverride{}).isEmpty() {
			t.Fatal("a zero NodeOverride must be empty")
		}
		o := NodeOverride{MemoryRef: "mem1"}
		if o.isEmpty() {
			t.Fatal("a memory-only override reports empty; Resolve would then drop it from Overrides " +
				"and the transform would never see it — a silent drop, which D4 refuses")
		}
	})

	t.Run("Refs", func(t *testing.T) {
		s := &VariantSpec{
			SourceRevision: "rev",
			Order:          []string{"n_a", "n_b"},
			Nodes: map[string]NodeOverride{
				"n_a": {ModelRef: "m1", MemoryRef: "mem1"},
				"n_b": {MemoryRef: "mem2"},
			},
		}
		got := s.Refs()
		want := []string{"m1", "mem1", "mem2"}
		if len(got) != len(want) {
			t.Fatalf("Refs() = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Refs() = %v, want %v (sorted, deduplicated)", got, want)
			}
		}
	})

	t.Run("Refs deduplicates a shared strategy", func(t *testing.T) {
		s := &VariantSpec{
			SourceRevision: "rev",
			Order:          []string{"n_a", "n_b"},
			Nodes: map[string]NodeOverride{
				"n_a": {MemoryRef: "shared"},
				"n_b": {MemoryRef: "shared"},
			},
		}
		if got := s.Refs(); len(got) != 1 || got[0] != "shared" {
			t.Fatalf("Refs() = %v, want exactly [shared]: two nodes on one strategy is one ref to resolve", got)
		}
	})

	t.Run("Validate rejects an inlined strategy definition", func(t *testing.T) {
		for _, inline := range []string{`{"strategy":"summary-buffer","params":{"max_tokens":2000}}`, ` [1,2]`} {
			s := &VariantSpec{
				SourceRevision: "rev",
				Order:          []string{"n_a"},
				Nodes:          map[string]NodeOverride{"n_a": {MemoryRef: inline}},
			}
			err := s.Validate()
			if err == nil {
				t.Fatalf("Validate accepted an inlined memory definition %q; a configuration whose content "+
					"lives outside the registry can never be resolved back from a config_hash (FR3)", inline)
			}
			if !errors.Is(err, ErrInlineDefinition) {
				t.Fatalf("Validate(%q) = %v, want ErrInlineDefinition — the author declined to point at an "+
					"entry, which is not the same failure as pointing at a missing one", inline, err)
			}
			var se *SpecError
			if !errors.As(err, &se) || se.Dim != DimMemory || se.NodeID != "n_a" {
				t.Fatalf("rejection = %#v, want a SpecError naming node n_a and dimension memory", err)
			}
		}
	})

	t.Run("Validate accepts an opaque ref", func(t *testing.T) {
		s := &VariantSpec{
			SourceRevision: "rev",
			Order:          []string{"n_a"},
			Nodes:          map[string]NodeOverride{"n_a": {MemoryRef: "mem1"}},
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("Validate rejected an opaque memory ref: %v. Refs are opaque to this layer — whether "+
				"one RESOLVES is the registry's question, asked in Resolve", err)
		}
	})
}

// TestNoMemoryOverrideBytesUnchanged — task 3.3, and the whole of decisions.md D3 at the authoring
// layer. A node that binds no memory must serialise EXACTLY as it did before this field existed.
//
// 🔴 This is the guard on the P0 golden vectors. An always-present `memory_ref` field would change the
// canonical bytes of every existing override, break every frozen vector, and orphan every row keyed by
// a config_hash. `omitempty` on a string is what makes absence free.
func TestNoMemoryOverrideBytesUnchanged(t *testing.T) {
	// The pre-P17 spelling of the same override, written out literally rather than produced by the
	// type under test — a struct compared against itself proves nothing.
	const preP17 = `{"model_ref":"m1","prompt_ref":"p1","skill_refs":["s1"],"context_policy":"c1"}`

	o := NodeOverride{ModelRef: "m1", PromptRef: "p1", SkillRefs: []string{"s1"}, ContextPolicy: "c1"}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != preP17 {
		t.Fatalf("a no-memory override serialises as\n  %s\nwant (pre-P17 bytes)\n  %s\nAn added key here "+
			"moves the canonical bytes of every existing node and breaks the P0 golden vectors (D3)",
			got, preP17)
	}

	// And the field is live when it IS set: absence is free, presence is identity-bearing.
	withMem, err := json.Marshal(NodeOverride{ModelRef: "m1", MemoryRef: "mem1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(withMem) != `{"model_ref":"m1","memory_ref":"mem1"}` {
		t.Fatalf("a memory-bearing override serialises as %s, want the memory_ref key present", withMem)
	}
}
