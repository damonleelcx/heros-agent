package registry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func ptrF(f float64) *float64 { return &f }
func ptrI(i int) *int         { return &i }

func mustSeal(t *testing.T, kind Kind, name string, spec any) (string, []byte) {
	t.Helper()
	id, env, err := seal(kind, name, spec)
	if err != nil {
		t.Fatalf("seal(%s, %q): %v", kind, name, err)
	}
	return id, env
}

// The core FR6 property: the id IS the content. Everything else in this package is a consequence.
func TestSeal_VersionIDIsTheContentAddress(t *testing.T) {
	spec := ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8",
		Params: ModelParams{Temperature: ptrF(0), MaxTokens: ptrI(1024)}}

	id1, env1 := mustSeal(t, KindModel, "reviewer", spec)
	id2, env2 := mustSeal(t, KindModel, "reviewer", spec)

	if id1 != id2 {
		t.Errorf("same content sealed twice gave different ids: %s vs %s", id1, id2)
	}
	if string(env1) != string(env2) {
		t.Errorf("same content sealed twice gave different envelopes:\n %s\n %s", env1, env2)
	}
	if err := verifyEnvelope(id1, env1); err != nil {
		t.Errorf("sealed envelope does not verify against its own id: %v", err)
	}
	// The envelope is canonical JSON: recursively sorted keys, no whitespace. The SQL guards in
	// 0002 read `kind`/`name`/`spec.body_blob_hash` out of exactly these bytes.
	want := `{"kind":"model","name":"reviewer","spec":{"model_id":"claude-opus-4-8","params":{"max_tokens":1024,"temperature":0},"provider":"anthropic"}}`
	if string(env1) != want {
		t.Errorf("envelope is not canonical:\n got %s\nwant %s", env1, want)
	}
}

// FR6 / the registries spec's "editing produces a new version": a changed entry gets a different id
// with no versioning logic anywhere — the content address does it.
func TestSeal_AnyContentChangeChangesTheID(t *testing.T) {
	base := ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8",
		Params: ModelParams{Temperature: ptrF(0), MaxTokens: ptrI(1024)}}
	baseID, _ := mustSeal(t, KindModel, "reviewer", base)

	cases := []struct {
		what string
		spec ModelSpec
	}{
		{"provider", ModelSpec{Provider: "openai", ModelID: "claude-opus-4-8", Params: base.Params}},
		{"model_id", ModelSpec{Provider: "anthropic", ModelID: "claude-sonnet-5", Params: base.Params}},
		{"temperature", ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8",
			Params: ModelParams{Temperature: ptrF(0.7), MaxTokens: ptrI(1024)}}},
		{"max_tokens", ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8",
			Params: ModelParams{Temperature: ptrF(0), MaxTokens: ptrI(2048)}}},
		{"seed added", ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8",
			Params: ModelParams{Temperature: ptrF(0), MaxTokens: ptrI(1024), Seed: func() *int64 { s := int64(7); return &s }()}}},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			id, _ := mustSeal(t, KindModel, "reviewer", tc.spec)
			if id == baseID {
				t.Errorf("changing %s did not change the version_id (%s) — a param is not pinned by the version", tc.what, id)
			}
		})
	}

	// Renaming is also a content change: name is inside the envelope, so a version_id resolves to
	// exactly one (name, content) pair.
	if id, _ := mustSeal(t, KindModel, "summarizer", base); id == baseID {
		t.Error("changing the name did not change the version_id")
	}
}

// kind is hashed so a version_id is unique across all four registries: a ref pasted into the wrong
// dimension names nothing and fails closed, rather than silently resolving to a same-named entry.
func TestSeal_KindDisambiguatesAcrossRegistries(t *testing.T) {
	spec := map[string]string{"same": "content"}
	modelID, _ := mustSeal(t, KindModel, "thing", spec)
	promptID, _ := mustSeal(t, KindPrompt, "thing", spec)
	if modelID == promptID {
		t.Fatalf("a model and a prompt with identical name+spec collided onto one version_id: %s", modelID)
	}
}

// A number the canonical form cannot represent unambiguously must fail loud rather than be hashed in
// an ambiguous form (confighash.ErrNonCanonicalNumber). Silently hashing 1e+21 would mean two
// spellings of one value getting two version_ids.
func TestSeal_RejectsNonCanonicalNumber(t *testing.T) {
	_, _, err := seal(KindModel, "big", map[string]any{"n": 1e21})
	if err == nil {
		t.Fatal("expected a non-canonical number to be rejected, got nil")
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("want ErrInvalidEntry, got %v", err)
	}
}

func TestVerifyEnvelope_CatchesTamperedContent(t *testing.T) {
	id, env := mustSeal(t, KindModel, "reviewer",
		ModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8"})

	tampered := []byte(strings.Replace(string(env), "anthropic", "openai___", 1))
	if len(tampered) != len(env) {
		t.Fatalf("test bug: tampering changed the length")
	}
	err := verifyEnvelope(id, tampered)
	if !errors.Is(err, ErrCorruptEntry) {
		t.Fatalf("tampered content filed under a published id was not caught: %v", err)
	}
}

// ── Expand-contract (FR10, task 1.7) — the pure half. ────────────────────────────────────────────
// The live half (an old pinned version still resolving out of Postgres after a new one is published)
// is in store_pgproof_test.go. These prove the decode rules that make it work.

// Direction 1: an OLD row read by NEW code. The row was sealed before the spec type grew a field, so
// its envelope has no such key. Resolution must succeed, with the new field at its zero value — NOT
// fail, and NOT re-hash to something other than the id it is filed under.
func TestExpandContract_OldRowResolvesUnderNewerSpecType(t *testing.T) {
	// Simulate the old build: seal a spec with no `params` at all.
	type oldModelSpec struct {
		Provider string `json:"provider"`
		ModelID  string `json:"model_id"`
	}
	oldID, oldEnv := mustSeal(t, KindModel, "reviewer", oldModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8"})

	// The current build decodes it into today's richer ModelSpec.
	var spec ModelSpec
	name, err := decodeEnvelope(KindModel, oldID, oldEnv, &spec)
	if err != nil {
		t.Fatalf("an entry published by an older build no longer resolves: %v", err)
	}
	if name != "reviewer" || spec.Provider != "anthropic" || spec.ModelID != "claude-opus-4-8" {
		t.Errorf("old entry resolved to the wrong content: name=%q spec=%+v", name, spec)
	}
	if spec.Params.Temperature != nil || spec.Params.MaxTokens != nil {
		t.Errorf("fields absent from the old envelope should decode to nil, got %+v", spec.Params)
	}
}

// Direction 2: a NEW row read by OLD code. The envelope carries a key this build's spec type has
// never heard of. It must be ignored, and the id must still verify — the entry is resolvable, just
// not fully understood.
func TestExpandContract_UnknownFieldsAreIgnoredNotRejected(t *testing.T) {
	type futureModelSpec struct {
		Provider    string `json:"provider"`
		ModelID     string `json:"model_id"`
		RoutingTier string `json:"routing_tier"` // a field from some later phase (P6 model tiering)
	}
	id, env := mustSeal(t, KindModel, "reviewer",
		futureModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8", RoutingTier: "cheap"})

	var spec ModelSpec // today's type — knows nothing of routing_tier
	if _, err := decodeEnvelope(KindModel, id, env, &spec); err != nil {
		t.Fatalf("an entry carrying an unknown field failed to resolve: %v", err)
	}
	if spec.Provider != "anthropic" {
		t.Errorf("known fields decoded wrong: %+v", spec)
	}
}

// The trap this guards: re-marshalling a decoded spec and re-hashing THAT, instead of hashing the
// stored bytes. It looks equivalent and passes every same-version test, then silently orphans every
// entry published before the spec type last changed. Assert the two are genuinely different, so the
// reason resolve() reads stored bytes is pinned by a test rather than by a comment.
func TestExpandContract_ReMarshallingWouldOrphanOldEntries(t *testing.T) {
	type oldModelSpec struct {
		Provider string `json:"provider"`
		ModelID  string `json:"model_id"`
	}
	oldID, oldEnv := mustSeal(t, KindModel, "reviewer", oldModelSpec{Provider: "anthropic", ModelID: "claude-opus-4-8"})

	var spec ModelSpec
	if _, err := decodeEnvelope(KindModel, oldID, oldEnv, &spec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	reSealedID, _ := mustSeal(t, KindModel, "reviewer", spec)
	if reSealedID == oldID {
		t.Skip("today's ModelSpec happens to re-marshal an old envelope identically; " +
			"the hazard is real as soon as a non-omitempty field is added")
	}
	// Re-hashing the decoded struct yields a DIFFERENT id than the row is filed under. Reads must
	// therefore never do it.
	if err := verifyEnvelope(reSealedID, oldEnv); err == nil {
		t.Fatal("test bug: expected the re-sealed id not to verify against the old envelope")
	}
}

func TestDecodeEnvelope_RejectsWrongKind(t *testing.T) {
	id, env := mustSeal(t, KindPrompt, "greeting", PromptSpec{BodyBlobHash: strings.Repeat("a", 64), Slots: []string{}})
	var spec ModelSpec
	_, err := decodeEnvelope(KindModel, id, env, &spec)
	if !errors.Is(err, ErrCorruptEntry) {
		t.Fatalf("resolving a prompt entry as a model entry should fail closed, got %v", err)
	}
}

func TestSeal_RejectsEmptyName(t *testing.T) {
	_, _, err := seal(KindModel, "", ModelSpec{Provider: "anthropic", ModelID: "x"})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("want ErrInvalidEntry for an empty name, got %v", err)
	}
}

// json.RawMessage inside a spec is canonicalized like everything else, so a skill contract's key
// order cannot affect its version_id.
func TestSeal_NestedRawJSONIsCanonicalized(t *testing.T) {
	a := SkillSpec{ImplHandle: "builtin:search",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)}
	b := SkillSpec{ImplHandle: "builtin:search",
		InputSchema:  json.RawMessage(`{"properties":{"q":{"type":"string"}},  "type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)}

	idA, _ := mustSeal(t, KindSkill, "search", a)
	idB, _ := mustSeal(t, KindSkill, "search", b)
	if idA != idB {
		t.Errorf("key order / whitespace changed a skill's version_id: %s vs %s", idA, idB)
	}
}
