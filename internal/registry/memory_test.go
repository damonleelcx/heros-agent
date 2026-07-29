package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// P17 §5 — the memory registry (Step 5 of the "add an axis" checklist).
//
// Every test here runs against `NewStore(nil, nil)` or the seal/decode primitives directly. That is not a
// convenience: a nil *sql.DB is the proof that a rejection happened BEFORE any write. If one of these
// started needing a database, the validation it asserts would have moved to the wrong side of the store.

// TestKindMemoryHashedIntoVersionID — task 5.1. The Kind is part of the content address, which is the
// mechanism (not the policy) behind every fail-closed claim this axis makes.
func TestKindMemoryHashedIntoVersionID(t *testing.T) {
	if KindMemory != "memory" {
		t.Fatalf("KindMemory = %q, want %q: the kind is hashed into every version_id and into the DB "+
			"trigger's argument, so it cannot drift", KindMemory, "memory")
	}
	if tableMemory != "memory_entry" {
		t.Fatalf("tableMemory = %q, want memory_entry (the name 0017 creates)", tableMemory)
	}

	// The SAME name and the SAME spec bytes under two Kinds must produce DIFFERENT ids. This is the whole
	// of D1: if they collided, a memory ref pasted into the context dimension could resolve.
	spec := MemorySpec{Strategy: StrategyNone, Params: json.RawMessage(`{}`)}
	memID, _, err := seal(KindMemory, "same-name", spec)
	if err != nil {
		t.Fatalf("seal memory: %v", err)
	}
	ctxID, _, err := seal(KindContext, "same-name", spec)
	if err != nil {
		t.Fatalf("seal context: %v", err)
	}
	if memID == ctxID {
		t.Fatalf("a memory entry and a context entry with identical name and spec sealed to the same "+
			"version_id %s; the Kind is supposed to be part of the content address, and without that a "+
			"cross-dimension paste RESOLVES instead of failing closed (D1)", memID)
	}
}

// TestMemoryRegistrySealDecodeRoundTrip — task 5.2. The register/resolve path is model.go's, so what is
// worth asserting is the round trip through the STORED bytes: seal produces the id and the canonical
// bytes, and decoding those bytes reproduces the spec and the name.
func TestMemoryRegistrySealDecodeRoundTrip(t *testing.T) {
	in := MemorySpec{Strategy: "summary-buffer", Params: json.RawMessage(`{"max_tokens":2000}`)}
	id, env, err := seal(KindMemory, "chat-summary", in)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := verifyEnvelope(id, env); err != nil {
		t.Fatalf("sealed bytes do not re-derive their own id: %v", err)
	}

	var out MemorySpec
	name, err := decodeEnvelope(KindMemory, id, env, &out)
	if err != nil {
		t.Fatalf("decodeEnvelope: %v", err)
	}
	if name != "chat-summary" {
		t.Errorf("name = %q, want chat-summary", name)
	}
	if out.Strategy != "summary-buffer" {
		t.Errorf("strategy = %q, want summary-buffer", out.Strategy)
	}
	if string(out.Params) != `{"max_tokens":2000}` {
		t.Errorf("params = %s, want {\"max_tokens\":2000}", out.Params)
	}

	// Identical content seals to the identical id — content addressing, so re-registering is a no-op
	// rather than a second version (FR6).
	id2, _, err := seal(KindMemory, "chat-summary", in)
	if err != nil {
		t.Fatalf("seal again: %v", err)
	}
	if id2 != id {
		t.Errorf("the same strategy and params sealed to two ids (%s, %s); content addressing means "+
			"there is no second version to make", id, id2)
	}

	// Changed params seal to a DIFFERENT id, leaving the first resolvable — that is the whole of
	// "editing publishes a new version, the old one stays intact", with no versioning logic.
	changed, _, err := seal(KindMemory, "chat-summary", MemorySpec{Strategy: "summary-buffer", Params: json.RawMessage(`{"max_tokens":4000}`)})
	if err != nil {
		t.Fatalf("seal changed: %v", err)
	}
	if changed == id {
		t.Error("changing max_tokens did not change the version_id; a pinned spec's meaning could then shift underneath it")
	}
}

// TestBuiltinStrategySetClosedAndSized — task 5.3. The cardinality assertion is the point: a sixth
// strategy added without a version bump fails HERE rather than silently changing what a stored strategy
// name means. It mirrors TaxonomySize, for the same reason.
func TestBuiltinStrategySetClosedAndSized(t *testing.T) {
	got := BuiltinMemoryStrategies()
	if len(got) != MemoryStrategySetSize {
		t.Fatalf("the builtin set has %d strategies but MemoryStrategySetSize is %d.\n"+
			"If you ADDED a strategy: bump MemoryStrategySetSize AND MemoryStrategySetVersion (currently %s), "+
			"because a stored config_hash referencing a strategy name is only interpretable against the "+
			"vocabulary version it was written under.\n"+
			"If you REMOVED one: every stored entry naming it stops resolving — that is a breaking change, "+
			"not a tidy-up.", len(got), MemoryStrategySetSize, MemoryStrategySetVersion)
	}

	want := map[string]bool{"none": true, "scratchpad": true, "summary-buffer": true, "vector-recall": true, "entity-memory": true}
	seen := map[string]bool{}
	for _, st := range got {
		n := st.Name()
		if !want[n] {
			t.Errorf("unexpected builtin strategy %q; the set is CLOSED at this version", n)
		}
		if seen[n] {
			t.Errorf("strategy %q appears twice", n)
		}
		seen[n] = true

		// Each carries a human title and description DISTINCT from the wire name (FR6). Three layers —
		// interface, entity, code name — kept separate, so rewording a label is never a hash change.
		if st.Title() == "" {
			t.Errorf("strategy %q has no title; the interface layer needs a label that is not the wire name", n)
		}
		if st.Title() == n {
			t.Errorf("strategy %q's title equals its wire name; collapsing the two makes a rename a "+
				"breaking change to stored hashes", n)
		}
		if len(st.Description()) < 20 {
			t.Errorf("strategy %q has no usable description (%q); a user choosing between five strategies "+
				"needs to know what each trades away", n, st.Description())
		}

		// A strategy whose own schema is invalid would reject every params value at seal with a confusing
		// error, so the schemas are checked here rather than discovered by a caller.
		if _, err := compileSchema("params_schema", st.ParamsSchema()); err != nil {
			t.Errorf("strategy %q's own params schema is not a valid JSON Schema: %v", n, err)
		}
	}
	for n := range want {
		if !seen[n] {
			t.Errorf("builtin strategy %q is missing", n)
		}
	}

	// `none` is the identity and takes no params — asserted, because everything downstream (the
	// none≡absent hash contract, the transform's refusal boundary) reads that fact.
	if MemoryStrategyNamed(StrategyNone) == nil {
		t.Fatal("MemoryStrategyNamed(none) is nil; `none` must be a real member of the vocabulary")
	}
	if MemoryStrategyNamed("no-such-strategy") != nil {
		t.Error("MemoryStrategyNamed resolved a name outside the closed set; it must fail closed")
	}
}

// TestParamsSchemaViolationRejectedAtSeal — task 5.4. Rejection happens BEFORE the entry is stored, which
// the nil *sql.DB proves: an id for a rejected entry is an id someone can paste into a spec, and it would
// resolve to nothing forever.
func TestParamsSchemaViolationRejectedAtSeal(t *testing.T) {
	s := NewStore(nil, nil)
	ctx := context.Background()

	cases := []struct {
		name     string
		strategy string
		params   string
		why      string
	}{
		{"wrong type", "summary-buffer", `{"max_tokens":"lots"}`, "a string where an integer is required"},
		{"missing required", "vector-recall", `{"top_k":5}`, "embedding_ref is required: recall is only reproducible against a pinned embedding"},
		{"unknown key", "scratchpad", `{"max_entries":3,"window":50}`, "additionalProperties:false — a param silently ignored at runtime is the mistake this rejects"},
		{"params on none", "none", `{"max_tokens":10}`, "`none` takes no params; accepting them would silently ignore what the author asked for"},
		{"empty array", "entity-memory", `{"entity_keys":[]}`, "minItems:1 — entity memory with no keys carries nothing, which is `none` under a misleading name"},
		{"below minimum", "scratchpad", `{"max_entries":0}`, "minimum:1 — a zero-entry scratchpad is `none` under a misleading name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, err := s.RegisterMemory(ctx, "entry", c.strategy, json.RawMessage(c.params))
			if err == nil {
				t.Fatalf("params %s were accepted for strategy %q and returned %s (%s)", c.params, c.strategy, id, c.why)
			}
			if !errors.Is(err, ErrInvalidEntry) {
				t.Fatalf("want ErrInvalidEntry, got %v", err)
			}
			if id != "" {
				t.Errorf("a rejected entry returned version_id %q; an id for content that was never stored "+
					"is an id a spec can reference and never resolve", id)
			}
		})
	}

	t.Run("valid params pass the same validator", func(t *testing.T) {
		// The mirror of every case above, through the validator RegisterMemory itself calls — so a bug
		// that rejected everything could not pass this file.
		st, norm, err := s.ValidateMemoryParams("entry", "summary-buffer", json.RawMessage(`{"max_tokens":2000,"keep_last_turns":4}`))
		if err != nil {
			t.Fatalf("schema-valid params were rejected: %v", err)
		}
		if st == nil || st.Name() != "summary-buffer" {
			t.Fatalf("the validator returned strategy %v, want summary-buffer", st)
		}
		if len(norm) == 0 {
			t.Error("the validator returned empty params for a non-empty input")
		}
		// Empty params normalize to `{}` rather than staying nil — the sealed bytes must be one spelling,
		// or the same configuration would seal to two ids.
		if _, norm, err := s.ValidateMemoryParams("entry", StrategyNone, nil); err != nil {
			t.Fatalf("nil params on `none` were rejected: %v", err)
		} else if string(norm) != `{}` {
			t.Errorf("nil params normalized to %q, want `{}`: two spellings of empty would seal to two ids", norm)
		}
	})

	t.Run("an unregistered strategy is rejected naming the alternatives", func(t *testing.T) {
		id, err := s.RegisterMemory(ctx, "entry", "no-such-strategy", nil)
		if err == nil {
			t.Fatalf("an unregistered strategy was accepted and returned %s", id)
		}
		if !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("want ErrInvalidEntry, got %v", err)
		}
		// The unhappy path names what was rejected AND what was available — a user cannot act on
		// "invalid strategy" alone.
		if !strings.Contains(err.Error(), "no-such-strategy") || !strings.Contains(err.Error(), "summary-buffer") {
			t.Errorf("the rejection should name the bad strategy and the available ones, got: %v", err)
		}
	})
}

// TestMemoryRefFailsClosedCrossDimension — task 5.5 🔴. Both directions, because each is a different
// false-acceptance: a memory ref that resolves as a context policy binds the wrong strategy, and a
// context ref that resolves as memory binds a policy as a memory store.
func TestMemoryRefFailsClosedCrossDimension(t *testing.T) {
	memSpec := MemorySpec{Strategy: "scratchpad", Params: json.RawMessage(`{"max_entries":5}`)}
	memID, memEnv, err := seal(KindMemory, "notes", memSpec)
	if err != nil {
		t.Fatalf("seal memory: %v", err)
	}
	ctxID, ctxEnv, err := seal(KindContext, "notes", ContextSpec{Policy: "full", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("seal context: %v", err)
	}

	t.Run("a memory envelope decoded as another kind fails closed", func(t *testing.T) {
		for _, kind := range []Kind{KindContext, KindModel, KindPrompt, KindSkill} {
			var spec ContextSpec
			if _, err := decodeEnvelope(kind, memID, memEnv, &spec); err == nil {
				t.Errorf("a memory envelope decoded cleanly as kind %q; a cross-dimension paste must FAIL, "+
					"not bind the wrong entry", kind)
			}
		}
	})

	t.Run("a foreign envelope decoded as memory fails closed", func(t *testing.T) {
		var spec MemorySpec
		if _, err := decodeEnvelope(KindMemory, ctxID, ctxEnv, &spec); err == nil {
			t.Error("a context envelope decoded cleanly as a memory entry; a policy would then be bound as " +
				"a memory strategy")
		}
	})

	t.Run("the ids themselves are disjoint", func(t *testing.T) {
		if memID == ctxID {
			t.Fatal("a memory entry and a context entry sealed to the same id")
		}
	})
}

// TestMemoryEntryIsNone pins the ONE place `none`-ness is decided. resolve, the transform refusal, and the
// console all read it, and three separate string comparisons against "none" would be three things to keep
// true (禁止分裂 source-of-truth).
func TestMemoryEntryIsNone(t *testing.T) {
	none := &MemoryEntry{Spec: MemorySpec{Strategy: StrategyNone}}
	if !none.IsNone() {
		t.Error("a `none` entry does not report IsNone")
	}
	real := &MemoryEntry{Spec: MemorySpec{Strategy: "scratchpad"}}
	if real.IsNone() {
		t.Error("a scratchpad entry reports IsNone; the transform would then decline to refuse it")
	}
	var nilEntry *MemoryEntry
	if nilEntry.IsNone() {
		t.Error("a nil entry reports IsNone; nil means NO OVERRIDE, which is a different question from " +
			"`the override is the identity strategy` and must not answer it")
	}
}

// TestMemoryRegistrySuite — task 11.4, the QA acceptance gate's registry suite. It runs the four
// guarantees together rather than restating them: five builtins resolve, a sixth without a version bump
// fails the cardinality assertion, a cross-dimension ref fails closed, a params violation is rejected at
// seal.
func TestMemoryRegistrySuite(t *testing.T) {
	t.Run("five builtins resolve", TestBuiltinStrategySetClosedAndSized)
	t.Run("cardinality is asserted", func(t *testing.T) {
		if MemoryStrategySetSize != len(BuiltinMemoryStrategies()) {
			t.Fatal("the cardinality assertion is not live")
		}
		if MemoryStrategySetVersion == "" {
			t.Fatal("the strategy set carries no version; a stored strategy name would be un-interpretable")
		}
	})
	t.Run("cross-dimension fails closed", TestMemoryRefFailsClosedCrossDimension)
	t.Run("params violation rejected at seal", TestParamsSchemaViolationRejectedAtSeal)
}
