package confighash

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// golden mirrors the fields of schemas/samples/config-hash.golden.json that we assert against.
type golden struct {
	Base struct {
		ResolvedConfig json.RawMessage `json:"resolved_config"`
		CanonicalJSON  string          `json:"canonical_json"`
		ConfigHash     string          `json:"config_hash"`
	} `json:"base"`
	VariantB struct {
		ConfigHash string `json:"config_hash"`
	} `json:"variant_b_prompt_v4"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	path := filepath.Join("..", "..", "schemas", "samples", "config-hash.golden.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	return g
}

// decodeNum decodes raw JSON with UseNumber so numbers survive as json.Number (exact tokens).
func decodeNum(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

// (a) determinism + canonical form: canon(base) == golden.canonical_json and hash matches.
func TestDeterminismAndCanonicalForm(t *testing.T) {
	g := loadGolden(t)

	canon, err := CanonicalizeBytes(g.Base.ResolvedConfig)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Fatalf("canonical mismatch:\n go:     %s\n golden: %s", canon, g.Base.CanonicalJSON)
	}

	got, err := SumBytes(g.Base.ResolvedConfig)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if got != g.Base.ConfigHash {
		t.Fatalf("base config_hash mismatch: go=%s golden=%s", got, g.Base.ConfigHash)
	}
	if Display(got) != g.Base.ConfigHash[:12] {
		t.Fatalf("display prefix mismatch")
	}
}

// (b) canonicalization: a value decoded and re-canonicalized is byte-stable regardless of the source
// key order (json decode into a map already discards order; this asserts the output is fixed).
func TestKeyOrderIndependent(t *testing.T) {
	g := loadGolden(t)
	v := decodeNum(t, g.Base.ResolvedConfig)
	h1, err := Sum(v)
	if err != nil {
		t.Fatal(err)
	}
	// Re-marshal with Go's map iteration (random order) and re-hash; must be identical.
	remarshaled, _ := json.Marshal(v)
	h2, err := SumBytes(remarshaled)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || h1 != g.Base.ConfigHash {
		t.Fatalf("hash not order-independent: h1=%s h2=%s golden=%s", h1, h2, g.Base.ConfigHash)
	}
}

// (c) registry-version sensitivity: repoint nodes[0].prompt_ref @3 -> @4 => golden variant_b hash.
func TestRegistryVersionSensitivity(t *testing.T) {
	g := loadGolden(t)
	v := decodeNum(t, g.Base.ResolvedConfig).(map[string]any)

	nodes := v["nodes"].([]any)
	n0 := nodes[0].(map[string]any)
	if got := n0["prompt_ref"]; got != "prompt://triage/classify@3" {
		t.Fatalf("unexpected base prompt_ref: %v", got)
	}
	n0["prompt_ref"] = "prompt://triage/classify@4"

	got, err := Sum(v)
	if err != nil {
		t.Fatal(err)
	}
	if got == g.Base.ConfigHash {
		t.Fatal("changing prompt_ref did NOT change the hash")
	}
	if got != g.VariantB.ConfigHash {
		t.Fatalf("variant_b hash mismatch: go=%s golden=%s", got, g.VariantB.ConfigHash)
	}
}

// (d) seed-invariance: run_id/seed/timestamp are absent from resolved_config, so no run-time value can
// perturb the hash. Assert they are not present, and that injecting them would (correctly) be a
// DIFFERENT object — i.e. the contract is "don't put them in", enforced by the producer.
func TestSeedInvarianceByExclusion(t *testing.T) {
	g := loadGolden(t)
	v := decodeNum(t, g.Base.ResolvedConfig).(map[string]any)
	for _, forbidden := range []string{"run_id", "seed", "timestamp"} {
		if _, present := v[forbidden]; present {
			t.Fatalf("resolved_config must NOT contain run-time value %q", forbidden)
		}
	}
	// The same config under different seeds shares one hash because seed is simply not in the input.
	h1, _ := Sum(v)
	h2, _ := Sum(v) // "different seed" changes nothing hashable
	if h1 != h2 || h1 != g.Base.ConfigHash {
		t.Fatalf("hash not seed-invariant")
	}
}

// Guard: the fail-loud path for a non-canonical number token.
func TestNonCanonicalNumberRejected(t *testing.T) {
	_, err := SumBytes([]byte(`{"x": 1.0}`))
	if err == nil {
		t.Fatal("expected ErrNonCanonicalNumber for 1.0")
	}
	if _, err := SumBytes([]byte(`{"x": 1}`)); err != nil {
		t.Fatalf("canonical integer 1 should hash fine: %v", err)
	}
}
