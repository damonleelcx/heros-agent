package variantspec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P18 §3 — the Dimension, the override, resolution, and the hashed projection.
//
// The through-line of this file is decisions.md D-8: `single-shot` with no params ≡ absent. Everything
// else — the dimension dispatch, the params projection, the hash sensitivity — hangs off getting that
// equality exactly right, because it is what keeps the P0 golden vectors reproducing and what lets a user
// back out of an authored harness change with no residue in the hash.

// resolveHarnessSpec builds a one-node spec over testIR's node A with the given harness ref.
func resolveHarnessSpec(ref string) *VariantSpec {
	s := &VariantSpec{
		SourceRevision: "rev1",
		Order:          []string{"n_a"},
		Nodes:          map[string]NodeOverride{},
	}
	if ref != "" {
		s.Nodes["n_a"] = NodeOverride{HarnessRef: ref}
	}
	return s
}

// TestDimHarnessInClosedEnum — task 3.1. Dimensions() is what the transform iterates, so a member absent
// from it is a member no consumer can see.
func TestDimHarnessInClosedEnum(t *testing.T) {
	if DimHarness != "harness" {
		t.Fatalf("DimHarness = %q, want harness: every error names it and the console keys on it", DimHarness)
	}
	found := false
	for _, d := range Dimensions() {
		if d == DimHarness {
			found = true
		}
	}
	if !found {
		t.Fatalf("Dimensions() omits harness (%v); a consumer iterating dimensions would silently miss it, "+
			"which is exactly how an axis ends up modelled and never dispatched", Dimensions())
	}
}

// TestDimensionsReportsHarnessIffSet — task 3.3. The transform iterates what Dimensions() reports, so this
// is where "a node that never mentioned a harness is never touched" becomes mechanical.
func TestDimensionsReportsHarnessIffSet(t *testing.T) {
	t.Run("absent when not overridden", func(t *testing.T) {
		for _, d := range (ResolvedOverride{Model: &registry.ModelEntry{}}).Dimensions() {
			if d == DimHarness {
				t.Fatal("Dimensions() reports harness for an override that set none of it; the transform " +
					"would then dispatch a harness rewriter at a node the author never asked about")
			}
		}
	})

	t.Run("present when overridden", func(t *testing.T) {
		ro := ResolvedOverride{Harness: &registry.HarnessEntry{Spec: registry.HarnessSpec{Strategy: "reflexion"}}}
		dims := ro.Dimensions()
		if len(dims) != 1 || dims[0] != DimHarness {
			t.Fatalf("Dimensions() = %v, want exactly [harness]", dims)
		}
	})

	// 🔴 An explicit `single-shot` IS an override. It hashes as absent (D-8), but the transform still has
	// to see it: "the author asked for the identity" and "the author said nothing" are different facts,
	// and a surface that could not tell them apart would report a user's deliberate choice as silence.
	t.Run("present for an explicit single-shot", func(t *testing.T) {
		ro := ResolvedOverride{Harness: &registry.HarnessEntry{
			Spec: registry.HarnessSpec{Strategy: registry.StrategySingleShot}}}
		found := false
		for _, d := range ro.Dimensions() {
			if d == DimHarness {
				found = true
			}
		}
		if !found {
			t.Fatal("Dimensions() hides an explicit single-shot override; the transform must be able to " +
				"tell an authored identity from an absent one, or the no-op it applies is indistinguishable " +
				"from a node it never saw")
		}
	})
}

// TestHarnessRefIsRefOnly — task 3.2. An inlined strategy definition is a structural error, not a dangling
// ref: the author did not point at a missing entry, they declined to point at one.
func TestHarnessRefIsRefOnly(t *testing.T) {
	for _, inline := range []string{`{"strategy":"reflexion","params":{"max_turns":3}}`, `["reflexion"]`, `  {"a":1}`} {
		s := resolveHarnessSpec(inline)
		err := s.Validate()
		if err == nil {
			t.Fatalf("Validate accepted an inlined strategy definition %q; a configuration whose content "+
				"lives outside any registry can never be resolved back from a config_hash", inline)
		}
		if !errors.Is(err, ErrInlineDefinition) {
			t.Fatalf("got %v, want ErrInlineDefinition — this is not a dangling ref, and telling the "+
				"author to go look up a version_id would send them somewhere that never existed", err)
		}
		var se *SpecError
		if !errors.As(err, &se) || se.Dim != DimHarness {
			t.Fatalf("the rejection does not name the harness dimension: %v", err)
		}
	}

	// A plain opaque token is accepted structurally — whether it RESOLVES is the registry's question.
	if err := resolveHarnessSpec("deadbeef").Validate(); err != nil {
		t.Fatalf("Validate rejected an opaque ref: %v", err)
	}
}

// TestHarnessOverrideParticipatesInIsEmptyAndRefs — task 3.2's other half. A field that is not in isEmpty
// makes a harness-only override look like "this node runs as discovered"; a field that is not in Refs()
// makes a dangling harness ref reach the transform instead of failing at the loader.
func TestHarnessOverrideParticipatesInIsEmptyAndRefs(t *testing.T) {
	o := NodeOverride{HarnessRef: "h1"}
	if o.isEmpty() {
		t.Fatal("a harness-only override reports isEmpty; a node whose only change is its scaffold would " +
			"be treated as unchanged")
	}
	s := resolveHarnessSpec("h1")
	refs := s.Refs()
	found := false
	for _, r := range refs {
		if r == "h1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Refs() = %v, missing the harness ref; the loader would not fail closed on a dangling "+
			"harness_ref, and the transform would be the first thing to notice", refs)
	}
}

// TestResolveHarnessOverrideAndDefault — task 3.3. The merge rule: an override resolves to a registry
// entry pinned by version_id; no override falls back to the discovered default pinned by source_revision.
func TestResolveHarnessOverrideAndDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("override resolves and projects", func(t *testing.T) {
		regs := newFakeRegistries()
		regs.addHarness(t, "h1", "revise", "reflexion",
			`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"find the error"}`)
		got, err := Resolve(ctx, resolveHarnessSpec("h1"), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		n := got.Config.Nodes[0]
		if n.Harness == nil {
			t.Fatal("the resolved node carries no harness; an override that resolves must reach the " +
				"hashed projection, or the axis is not identity-bearing")
		}
		if n.Harness.Strategy != "reflexion" {
			t.Fatalf("resolved strategy = %q, want reflexion", n.Harness.Strategy)
		}
		if got, ok := n.Harness.Params["max_turns"]; !ok || got == nil {
			t.Fatalf("the params were not projected: %v", n.Harness.Params)
		}
		// 🔴 The PROJECTION, not the version_id (D-8). Two entries spelling one strategy with one params
		// set must share a config_hash.
		if strings.Contains(n.Harness.Strategy, "h1") {
			t.Fatal("the resolved projection carries the registry version_id; config_hash denotes a " +
				"configuration, not a set of registry rows")
		}
		if ro := got.Overrides["n_a"]; ro.Harness == nil || ro.Harness.VersionID != "h1" {
			t.Fatalf("the resolved OVERRIDE lost the entry the author pinned: %+v", ro.Harness)
		}
	})

	t.Run("absent falls back to the discovered default", func(t *testing.T) {
		regs := newFakeRegistries()
		got, err := Resolve(ctx, resolveHarnessSpec(""), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		n := got.Config.Nodes[0]
		// Discovery's floor is single-shot everywhere, and single-shot ≡ absent, so the projection is nil.
		if n.Harness != nil {
			t.Fatalf("a node with no harness override projected %+v; discovery's floor is the identity, "+
				"and the identity emits no key", n.Harness)
		}
		if ro := got.Overrides["n_a"]; ro.Harness != nil {
			t.Fatal("Resolve recorded a harness override for a node that declared none")
		}
	})
}

// TestSingleShotHarnessHashesAsAbsent — tasks 3.5/3.6 and 12.2. 🔴 The equality the whole axis rests on:
// an explicitly-single-shot node and a node that never mentioned a harness produce the same canonical
// bytes and the same config_hash. Without it a user cannot back out of an authored change, and every
// pre-P18 golden vector breaks.
func TestSingleShotHarnessHashesAsAbsent(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	regs.addHarness(t, "identity", "baseline", registry.StrategySingleShot, `{}`)

	bare, err := Resolve(ctx, resolveHarnessSpec(""), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve (no harness): %v", err)
	}
	explicit, err := Resolve(ctx, resolveHarnessSpec("identity"), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve (explicit single-shot): %v", err)
	}

	bareBytes, err := bare.Config.Canonical()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	explicitBytes, err := explicit.Config.Canonical()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if string(bareBytes) != string(explicitBytes) {
		t.Fatalf("an explicit single-shot does not canonicalize identically to no harness at all:\n"+
			" bare: %s\n explicit: %s\nThat equality is what lets a user clear an authored harness change "+
			"with no residue in the hash, and what keeps every pre-P18 golden vector reproducing.",
			bareBytes, explicitBytes)
	}
	if bare.ConfigHash != explicit.ConfigHash {
		t.Fatalf("config_hash differs: %s vs %s", bare.ConfigHash, explicit.ConfigHash)
	}
	// And the bytes carry no harness key at all — absence, not an empty value.
	if strings.Contains(string(bareBytes), `"harness"`) {
		t.Fatalf("a no-harness config emits a harness key: %s", bareBytes)
	}
}

// TestNoHarnessHashesByteIdentical — task 3.5. The struct-level half: every field P18 added is additive
// and omitempty, so a node that declares nothing new serialises exactly as it did before the field
// existed. Asserted on the marshalled bytes rather than by reading the tags, because a tag typo
// (`omitempy`) reads correct and behaves wrong.
func TestNoHarnessHashesByteIdentical(t *testing.T) {
	n := ResolvedNode{
		NodeID: "n", ModelRef: "openai/gpt-4o-mini", PromptRef: "inline://abc",
		SkillRefs: []string{}, ContextPolicy: "full",
		ContextParams: map[string]any{}, ProviderParams: map[string]any{},
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"harness", "harness_ref"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Fatalf("a node with no harness emits %q: %s — an always-present field would change the "+
				"canonical bytes of EVERY node in EVERY existing config and orphan every keyed row", key, b)
		}
	}

	o := NodeOverride{ModelRef: "m1"}
	ob, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal override: %v", err)
	}
	if strings.Contains(string(ob), "harness_ref") {
		t.Fatalf("an override with no harness emits harness_ref: %s", ob)
	}
}

// TestHarnessChangeChangesHashOnly — task 3.6. The other direction: a harness change is identity-bearing,
// and it is the ONLY thing that moved. Two variants differing only in scaffold must be distinguishable, or
// the platform cannot compare them.
func TestHarnessChangeChangesHashOnly(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	regs.addHarness(t, "identity", "baseline", registry.StrategySingleShot, `{}`)
	regs.addHarness(t, "loop3", "revise-3", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"find the error"}`)
	regs.addHarness(t, "loop5", "revise-5", "reflexion",
		`{"max_turns":5,"stop_condition":"max-turns","reflection_prompt":"find the error"}`)

	hash := func(ref string) string {
		t.Helper()
		got, err := Resolve(ctx, resolveHarnessSpec(ref), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", ref, err)
		}
		return got.ConfigHash
	}

	base := hash("identity")
	if h := hash("loop3"); h == base {
		t.Fatal("a reflexion loop hashes the same as a single shot; two variants differing only in " +
			"scaffold would be one configuration, and the platform could never compare them")
	}
	// A PARAM change is a configuration change too: three turns and five turns cost differently.
	if hash("loop3") == hash("loop5") {
		t.Fatal("changing max_turns did not change the hash; a loop that may run five turns is not the " +
			"same configuration as one that may run three, and it does not cost the same")
	}
	// 🔴 A determinism check used to sit here as `hash("loop3") != hash("loop3")`, and staticcheck was
	// right to reject it (SA4000): written that way it reads as a tautology, and a reader cannot tell
	// whether it is asserting anything. Determinism IS asserted — over five repetitions, in
	// TestHarnessIdentityUntouchedSuite — so this line was a duplicate of a real check written in a form
	// that could not carry the claim. Dropped rather than reworded past the linter.
}

// TestHarnessRefFailStaticNamesRef — task 3.7. 🔴 Fail-static. An unresolvable ref fails the resolve
// CLOSED and names the ref; it never falls back to a different strategy. A fallback here would run one
// turn while the pinned spec named a loop, and report the result under that spec's hash.
func TestHarnessRefFailStaticNamesRef(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()

	_, err := Resolve(ctx, resolveHarnessSpec("never-published"), testIR(), regs)
	if err == nil {
		t.Fatal("Resolve accepted a harness ref that resolves to nothing; the run would then execute a " +
			"scaffold nobody published, or silently execute none")
	}
	if !errors.Is(err, ErrUnresolvedRef) {
		t.Fatalf("got %v, want ErrUnresolvedRef", err)
	}
	var se *SpecError
	if !errors.As(err, &se) {
		t.Fatalf("the failure is not a SpecError: %v", err)
	}
	if se.Dim != DimHarness || se.Ref != "never-published" || se.NodeID != "n_a" {
		t.Fatalf("the rejection does not name node, dimension and ref: node=%q dim=%q ref=%q",
			se.NodeID, se.Dim, se.Ref)
	}

	// 🔴 A cross-Kind ref fails closed too: a memory version_id handed to the harness dimension must miss.
	regs.addMemory(t, "m1", "notes", "scratchpad", `{"max_entries":3}`)
	if _, err := Resolve(ctx, resolveHarnessSpec("m1"), testIR(), regs); !errors.Is(err, ErrUnresolvedRef) {
		t.Fatalf("a memory ref resolved through the harness dimension (%v); the Kind is part of the "+
			"content address precisely so this fails rather than binding the wrong thing", err)
	}
}

// TestResolvedHarnessAutoHashed — task 3.4. The projection participates in config_hash structurally: JCS
// sorts the params keys, so the hash depends on the params SET rather than their authoring order.
func TestResolvedHarnessAutoHashed(t *testing.T) {
	mk := func(params map[string]any) string {
		t.Helper()
		rc := ResolvedConfig{IRVersion: "1.0.0", Nodes: []ResolvedNode{{
			NodeID: "n", ModelRef: "openai/gpt-4o-mini", PromptRef: "inline://abc",
			SkillRefs: []string{}, ContextPolicy: "full",
			ContextParams: map[string]any{}, ProviderParams: map[string]any{},
			Harness: &ResolvedHarness{Strategy: "reflexion", Params: params},
		}}}
		h, err := rc.Hash()
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		return h
	}
	a := mk(map[string]any{"max_turns": 3.0, "reflection_prompt": "x"})
	b := mk(map[string]any{"reflection_prompt": "x", "max_turns": 3.0})
	if a != b {
		t.Fatal("the same params written in a different order hashed differently; config_hash must depend " +
			"on the params SET, or two identical configurations fragment on a formatting difference")
	}
	if c := mk(map[string]any{"max_turns": 4.0, "reflection_prompt": "x"}); c == a {
		t.Fatal("a param change did not move the hash")
	}
}

// TestClearingHarnessBacksOutWithNoResidue — task 12.2. The authored-change contract's hardest promise:
// a user who selects a strategy and then clears it lands exactly where they started, byte-for-byte.
func TestClearingHarnessBacksOutWithNoResidue(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	regs.addHarness(t, "loop3", "revise", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"find the error"}`)

	before, err := Resolve(ctx, resolveHarnessSpec(""), testIR(), regs)
	if err != nil {
		t.Fatalf("resolve before: %v", err)
	}
	authored, err := Resolve(ctx, resolveHarnessSpec("loop3"), testIR(), regs)
	if err != nil {
		t.Fatalf("resolve authored: %v", err)
	}
	if authored.ConfigHash == before.ConfigHash {
		t.Fatal("test setup: the authored change did not change the hash, so clearing it proves nothing")
	}
	// Clearing is expressed as the absence of the field — the same thing the user started with.
	after, err := Resolve(ctx, resolveHarnessSpec(""), testIR(), regs)
	if err != nil {
		t.Fatalf("resolve after clearing: %v", err)
	}
	if after.ConfigHash != before.ConfigHash {
		t.Fatalf("clearing an authored harness change left residue in the hash: %s -> %s",
			before.ConfigHash, after.ConfigHash)
	}
}
