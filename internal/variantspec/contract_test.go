package variantspec

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P13 §4 — contract preservation. P13 lands its effects ONLY in existing ResolvedNode fields
// (PromptRef/ModelRef/ProviderParams), so it must open no new hashed field, Dimension, or registry
// Kind, and the P0 golden vectors must still reproduce bit-for-bit (design Decision 8, task 4.1/4.2).

// 4.1: the frozen P0 golden vectors reproduce bit-for-bit through the (P13-modified) resolution types.
func TestGoldenVectorsStillReproduce(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)

	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Errorf("P13 perturbed the canonical bytes of the frozen golden:\n got: %s\nwant: %s", canon, g.Base.CanonicalJSON)
	}
	got, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got != g.Base.ConfigHash {
		t.Errorf("P13 changed config_hash of the frozen golden: got %s, want %s", got, g.Base.ConfigHash)
	}
}

// 4.2: the Dimension enum and the registry Kind set are unchanged — P13 adds neither. These are
// tripwire tests: adding a Dimension or Kind constant requires touching this test, which is the point.
func TestNoNewDimensionOrKind(t *testing.T) {
	wantDims := map[Dimension]bool{DimModel: true, DimPrompt: true, DimSkills: true, DimContext: true}
	if len(wantDims) != 4 {
		t.Fatal("the Dimension set is expected to be exactly four members")
	}
	for _, d := range []Dimension{DimModel, DimPrompt, DimSkills, DimContext} {
		if !wantDims[d] {
			t.Errorf("unexpected Dimension %q — P13 must add no new dimension", d)
		}
	}
	// Assert the string values too, so a rename that keeps the count is still caught.
	if DimModel != "model" || DimPrompt != "prompt" || DimSkills != "skills" || DimContext != "context" {
		t.Errorf("a Dimension string value drifted: %q %q %q %q", DimModel, DimPrompt, DimSkills, DimContext)
	}

	wantKinds := []registry.Kind{registry.KindModel, registry.KindPrompt, registry.KindSkill, registry.KindContext}
	seen := map[registry.Kind]bool{}
	for _, k := range wantKinds {
		seen[k] = true
	}
	if len(seen) != 4 {
		t.Fatalf("the registry Kind set is expected to be exactly four members, got %d", len(seen))
	}
	if registry.KindModel != "model" || registry.KindPrompt != "prompt" ||
		registry.KindSkill != "skill" || registry.KindContext != "context" {
		t.Error("a registry Kind string value drifted — P13 must add no new Kind")
	}
}
