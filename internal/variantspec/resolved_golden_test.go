package variantspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The frozen P0 golden vectors. internal/confighash calls itself "the Go REFERENCE IMPLEMENTATION and
// the home of the golden-vector regression test. The live producer is the P2 Config Layer; it MUST
// reproduce these vectors bit-for-bit." This file is that obligation discharged: it drives the golden
// document through THIS package's ResolvedConfig type and asserts the canonical bytes and the hash
// come out identical.
//
// That is a different assertion from schemas/test_config_hash.py, which hashes the golden JSON as an
// anonymous map. This one round-trips it through the typed struct P2 actually emits, so it catches
// the failure that matters here: a struct tag, an omitempty, or a nil-vs-empty slip changing the
// bytes. Those would be invisible to a map-based test and would silently re-key every stored result.

type goldenFile struct {
	Base struct {
		ResolvedConfig json.RawMessage `json:"resolved_config"`
		CanonicalJSON  string          `json:"canonical_json"`
		ConfigHash     string          `json:"config_hash"`
		Display12      string          `json:"config_hash_display12"`
	} `json:"base"`
	VariantB struct {
		ConfigHash string `json:"config_hash"`
	} `json:"variant_b_prompt_v4"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	path := filepath.Join("..", "..", "schemas", "samples", "config-hash.golden.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden vectors: %v", err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse golden vectors: %v", err)
	}
	return g
}

// decodeGolden parses the golden's resolved_config into THIS package's type. If a field of the frozen
// shape had no home in ResolvedConfig, it would be silently dropped here and the re-serialized bytes
// would not match — so DisallowUnknownFields makes that a loud failure instead.
func decodeGolden(t *testing.T, raw json.RawMessage) ResolvedConfig {
	t.Helper()
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	var rc ResolvedConfig
	if err := dec.Decode(&rc); err != nil {
		t.Fatalf("the frozen resolved_config does not fit ResolvedConfig: %v", err)
	}
	return rc
}

// (a) determinism — the P2 producer's type reproduces the frozen canonical bytes and hash.
func TestGolden_ResolvedConfigReproducesFrozenBytesAndHash(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)

	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Errorf("canonical bytes drifted from the frozen golden.\n got: %s\nwant: %s", canon, g.Base.CanonicalJSON)
	}

	got, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got != g.Base.ConfigHash {
		t.Errorf("config_hash = %s, want %s (every stored result is keyed by this)", got, g.Base.ConfigHash)
	}
	if Display(got) != g.Base.Display12 {
		t.Errorf("Display(%s) = %s, want %s", got, Display(got), g.Base.Display12)
	}
}

// (b) canonicalization — key order and whitespace in the input must not change the hash.
func TestGolden_KeyOrderAndWhitespaceDoNotChangeTheHash(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)
	want, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	// Re-serialize the golden with different key order and whitespace, re-decode, re-hash.
	var loose map[string]any
	if err := json.Unmarshal(g.Base.ResolvedConfig, &loose); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	reordered, err := json.MarshalIndent(loose, "", "    ") // Go maps marshal keys sorted; indent adds whitespace
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rc2 ResolvedConfig
	if err := json.Unmarshal(reordered, &rc2); err != nil {
		t.Fatalf("unmarshal reordered: %v", err)
	}
	got, err := rc2.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got != want {
		t.Errorf("a reordered/whitespaced serialization changed the hash: %s vs %s", got, want)
	}
}

// (c) version-sensitivity — repointing a prompt_ref @3 -> @4 must yield the frozen variant_b hash.
// This is the property that makes a config_hash a durable pointer to an exact configuration: if a
// registry version could change under a pinned spec without moving the hash, reproducibility and
// every A/B comparison built on it would be a coincidence.
func TestGolden_RepointingAPromptVersionChangesTheHashToTheFrozenValue(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)
	base, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	rc.Nodes[0].PromptRef = "prompt://triage/classify@4"
	got, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got == base {
		t.Fatal("repointing a prompt version did not change the config_hash")
	}
	if got != g.VariantB.ConfigHash {
		t.Errorf("variant_b config_hash = %s, want %s", got, g.VariantB.ConfigHash)
	}
}

// (d) seed-invariance — there is no run-time field in the shape for a seed to occupy.
func TestGolden_NoRuntimeFieldExistsInResolvedConfig(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)
	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	// Asserted over the emitted BYTES, not the struct's field list: the point is that nothing this
	// package emits can carry a run-time value, however it got in.
	for _, forbidden := range []string{`"seed"`, `"run_id"`, `"timestamp"`} {
		if containsBytes(canon, forbidden) {
			t.Errorf("resolved_config emitted %s; runs under different seeds would no longer share "+
				"one config_hash and multi-seed roll-up would break (config-hash-spec §5)", forbidden)
		}
	}
}

// The ordering guarantees JCS does NOT give us for free. JCS sorts object keys; it leaves arrays
// alone. So node order, skill_refs order, and edge order are all identity-bearing, and the golden's
// own ["search_kb@2","issue_lookup@1"] is proof the frozen data relies on it (it is not alphabetical).
func TestGolden_ArrayOrderIsIdentityBearing(t *testing.T) {
	g := loadGolden(t)
	base := decodeGolden(t, g.Base.ResolvedConfig)
	baseHash, err := base.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	t.Run("node order", func(t *testing.T) {
		rc := decodeGolden(t, g.Base.ResolvedConfig)
		rc.Nodes[0], rc.Nodes[1] = rc.Nodes[1], rc.Nodes[0]
		got, err := rc.Hash()
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if got == baseHash {
			t.Error("reordering nodes did not change the hash; the graph ordering is part of a " +
				"configuration's identity (P5 re-arrangement depends on this)")
		}
	})

	t.Run("skill_refs order", func(t *testing.T) {
		rc := decodeGolden(t, g.Base.ResolvedConfig)
		s := rc.Nodes[1].SkillRefs
		if len(s) < 2 {
			t.Fatalf("golden node 1 should bind 2 skills, got %v", s)
		}
		s[0], s[1] = s[1], s[0]
		got, err := rc.Hash()
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if got == baseHash {
			t.Error("reordering skill_refs did not change the hash")
		}
	})
}

// nil and empty must hash identically — they mean the same thing, and `null` vs `[]` would fork one
// configuration into two config_hashes depending on which construction path produced it.
func TestCanonical_NilAndEmptyHashIdentically(t *testing.T) {
	withNil := ResolvedConfig{
		IRVersion: "1.0.0",
		Nodes: []ResolvedNode{{
			NodeID: "n1", ModelRef: "anthropic/claude-opus-4-8", PromptRef: "prompt://p@1",
			SkillRefs: nil, ContextPolicy: "full", ContextParams: nil, ProviderParams: nil,
		}},
		Edges: nil,
	}
	withEmpty := ResolvedConfig{
		IRVersion: "1.0.0",
		Nodes: []ResolvedNode{{
			NodeID: "n1", ModelRef: "anthropic/claude-opus-4-8", PromptRef: "prompt://p@1",
			SkillRefs: []string{}, ContextPolicy: "full",
			ContextParams: map[string]any{}, ProviderParams: map[string]any{},
		}},
		Edges: []ResolvedEdge{},
	}
	a, err := withNil.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	b, err := withEmpty.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if a != b {
		t.Errorf("nil and empty hashed differently (%s vs %s); one configuration would get two "+
			"config_hashes depending on how it was constructed", a, b)
	}
	canon, err := withNil.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if containsBytes(canon, "null") {
		t.Errorf("a nil slice/map emitted null: %s", canon)
	}
}

func TestProviderParams_ExcludesSeedAndTimeout(t *testing.T) {
	temp, maxTok, think := 0.2, 1024, 4096
	got := providerParams(modelParamsView{Temperature: &temp, MaxTokens: &maxTok, ThinkingBudget: &think})

	for _, want := range []string{"temperature", "max_tokens", "thinking_budget"} {
		if _, ok := got[want]; !ok {
			t.Errorf("provider_params dropped %q, which changes what the model produces", want)
		}
	}
	// modelParamsView has no Seed/TimeoutSeconds field at all — the exclusion is structural, not a
	// filter someone can forget. This asserts the resulting map, so the guarantee is on the output.
	for _, forbidden := range []string{"seed", "timeout_seconds"} {
		if _, ok := got[forbidden]; ok {
			t.Errorf("provider_params leaked %q into the hash", forbidden)
		}
	}
}

// An unset param must be ABSENT, not present as a zero. `{"temperature":0}` means "pinned to 0" and
// `{}` means "the call site's own value stands" — the per-dimension independence FR2 requires. If
// unset collapsed to 0 they would hash the same and the codemod could not tell them apart.
func TestProviderParams_UnsetIsAbsentNotZero(t *testing.T) {
	zero := 0.0
	pinnedZero := providerParams(modelParamsView{Temperature: &zero})
	unset := providerParams(modelParamsView{})

	if _, ok := pinnedZero["temperature"]; !ok {
		t.Error("temperature pinned to 0 was dropped; 0 is a real setting")
	}
	if _, ok := unset["temperature"]; ok {
		t.Error("an unset temperature appeared in provider_params")
	}
	if len(unset) != 0 {
		t.Errorf("unset params should project to an empty map, got %v", unset)
	}
}
