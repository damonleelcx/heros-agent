package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// P18 §2 — the harness registry (Step 5 of the "add an axis" checklist).
//
// Every test here runs against `NewStore(nil, nil)` or the seal/decode primitives directly. That is not a
// convenience: a nil *sql.DB is the proof that a rejection happened BEFORE any write. If one of these
// started needing a database, the validation it asserts would have moved to the wrong side of the store.

// TestHarnessSealIsContentAddressed — task 2.1. The Kind is part of the content address, which is the
// mechanism (not the policy) behind every fail-closed claim this axis makes.
func TestHarnessSealIsContentAddressed(t *testing.T) {
	if KindHarness != "harness" {
		t.Fatalf("KindHarness = %q, want %q: the kind is hashed into every version_id and into the DB "+
			"trigger's argument, so it cannot drift", KindHarness, "harness")
	}
	if tableHarness != "harness_entry" {
		t.Fatalf("tableHarness = %q, want harness_entry (the name 0018 creates)", tableHarness)
	}

	spec := HarnessSpec{Strategy: StrategySingleShot, Params: json.RawMessage(`{}`)}
	id, env, err := seal(KindHarness, "baseline", spec)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := verifyEnvelope(id, env); err != nil {
		t.Fatalf("sealed bytes do not re-derive their own id: %v", err)
	}
	// Same content, same id — the property RegisterHarness's ON CONFLICT DO NOTHING relies on.
	again, _, err := seal(KindHarness, "baseline", spec)
	if err != nil {
		t.Fatalf("re-seal: %v", err)
	}
	if again != id {
		t.Fatalf("identical content sealed to two ids (%s, %s); content addressing is what makes a "+
			"pinned harness_ref resolve the same strategy bytes months later", id, again)
	}

	var out HarnessSpec
	name, err := decodeEnvelope(KindHarness, id, env, &out)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != "baseline" || out.Strategy != StrategySingleShot {
		t.Fatalf("round trip lost content: name=%q strategy=%q", name, out.Strategy)
	}
}

// TestHarnessRefFailsClosedCrossKind — task 2.5. The whole of D-1: if a harness entry and a memory entry
// with identical name and spec collided on one id, a harness ref pasted into the memory dimension would
// RESOLVE — silently binding the wrong thing — instead of failing closed.
func TestHarnessRefFailsClosedCrossKind(t *testing.T) {
	// Deliberately the same JSON shape under both Kinds, so the only difference is the Kind itself.
	body := struct {
		Strategy string          `json:"strategy"`
		Params   json.RawMessage `json:"params"`
	}{Strategy: "shared-name", Params: json.RawMessage(`{}`)}

	for _, other := range []Kind{KindMemory, KindContext, KindModel, KindPrompt, KindSkill} {
		harnessID, env, err := seal(KindHarness, "same-name", body)
		if err != nil {
			t.Fatalf("seal harness: %v", err)
		}
		otherID, _, err := seal(other, "same-name", body)
		if err != nil {
			t.Fatalf("seal %s: %v", other, err)
		}
		if harnessID == otherID {
			t.Fatalf("a harness entry and a %s entry with identical name and spec sealed to the same "+
				"version_id %s; the Kind is supposed to be part of the content address, and without that a "+
				"cross-dimension paste RESOLVES instead of failing closed (D-1)", other, harnessID)
		}
		// And the decode side refuses the wrong Kind even when the id is right.
		if _, err := decodeEnvelope(other, harnessID, env, nil); !errors.Is(err, ErrCorruptEntry) {
			t.Fatalf("decoding harness bytes as a %s entry returned %v, want ErrCorruptEntry: reading an "+
				"entry of the wrong Kind must fail, not succeed with the wrong meaning", other, err)
		}
	}
}

// TestFiveBuiltinStrategiesRegister — task 2.4. The cardinality assertion exists so a sixth strategy
// cannot be added without a version bump; a stored strategy name is only interpretable against the
// vocabulary version it was written under.
func TestFiveBuiltinStrategiesRegister(t *testing.T) {
	got := BuiltinHarnessStrategies()
	if len(got) != HarnessStrategySetSize {
		t.Fatalf("BuiltinHarnessStrategies() has %d entries but HarnessStrategySetSize is %d; adding a "+
			"strategy requires bumping HarnessStrategySetVersion (%s) too, or a stored strategy name "+
			"silently changes meaning", len(got), HarnessStrategySetSize, HarnessStrategySetVersion)
	}
	want := map[string]bool{
		"single-shot": true, "react-loop": true, "plan-execute": true,
		"reflexion": true, "critic-loop": true,
	}
	s := NewStore(nil, nil)
	for _, st := range got {
		if !want[st.Name()] {
			t.Errorf("unexpected builtin strategy %q", st.Name())
		}
		delete(want, st.Name())
		if st.Title() == "" || st.Description() == "" {
			t.Errorf("strategy %q has no human labels; the wire name, the entity and the label are three "+
				"layers and a surface must not have to invent the third", st.Name())
		}
		if _, ok := s.harnesses[st.Name()]; !ok {
			t.Errorf("strategy %q is a builtin but NewStore did not seed it, so RegisterHarness would "+
				"reject an entry naming it", st.Name())
		}
	}
	for missing := range want {
		t.Errorf("builtin strategy %q is absent from the catalog", missing)
	}

	// critic-loop carries a SEPARATE critic model ref (FR7) — asserted on the schema, because that is
	// where "required" is enforced rather than described.
	sch := string(CriticLoopHarness{}.ParamsSchema())
	if !strings.Contains(sch, "critic_model_ref") || !strings.Contains(sch, `"required":["max_turns","critic_model_ref"]`) {
		t.Errorf("critic-loop must REQUIRE a separate critic_model_ref; schema was %s", sch)
	}
}

// TestHarnessParamsSchemaPerStrategy — task 2.2. A param inapplicable to a strategy must be
// INEXPRESSIBLE for it, not silently ignored. That is the difference between "you selected the wrong
// strategy" being a loud error and being a surprise on a bill.
func TestHarnessParamsSchemaPerStrategy(t *testing.T) {
	s := NewStore(nil, nil)

	// `single-shot` takes nothing. max_turns on it is the exact mistake someone makes when they believe
	// they selected a loop.
	if _, _, err := s.ValidateHarnessParams("x", StrategySingleShot, json.RawMessage(`{"max_turns":3}`)); err == nil {
		t.Fatal("single-shot accepted max_turns; a strategy that runs exactly one turn must make a turn " +
			"ceiling inexpressible, or a user who thinks they chose a loop is never told otherwise")
	}
	if _, _, err := s.ValidateHarnessParams("x", StrategySingleShot, nil); err != nil {
		t.Fatalf("single-shot with no params was rejected: %v", err)
	}

	// A critic ref on a strategy that has no critic is equally inexpressible.
	if _, _, err := s.ValidateHarnessParams("x", "reflexion", json.RawMessage(
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"check it","critic_model_ref":"abc"}`)); err == nil {
		t.Fatal("reflexion accepted critic_model_ref; reflexion has no critic, so declaring one must be an " +
			"error rather than a param that quietly does nothing")
	}

	// Every multi-turn strategy REQUIRES a bounded max_turns (NFR5). Absence is a rejection, not a default.
	for _, name := range []string{"react-loop", "plan-execute", "reflexion", "critic-loop"} {
		if _, _, err := s.ValidateHarnessParams("x", name, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s accepted empty params; every multi-turn strategy must declare a bounded max_turns, "+
				"because a defaulted ceiling is a ceiling nobody chose", name)
		}
	}
}

// TestHarnessParamsValidatedAtSeal — task 2.3. 🔴 Out-of-range and unresolvable params fail REGISTRATION,
// not resolution. The nil *sql.DB is the proof: if any of these reached a write, the test would panic
// instead of returning an error.
func TestHarnessParamsValidatedAtSeal(t *testing.T) {
	s := NewStore(nil, nil)
	ctx := context.Background()

	cases := []struct {
		name, strategy, params, why string
	}{
		{"ceiling", "react-loop", `{"max_turns":9999,"stop_condition":"no-tool-call"}`,
			"a turn ceiling above MaxTurnsCeiling must be rejected by the schema itself, so a spec cannot " +
				"be authored against a bound it then violates"},
		{"zero", "react-loop", `{"max_turns":0,"stop_condition":"no-tool-call"}`,
			"a non-positive ceiling is not a loop"},
		{"type", "react-loop", `{"max_turns":"lots","stop_condition":"no-tool-call"}`,
			"a non-integer ceiling must fail at registration, not when a run reaches the node"},
		{"retry", "react-loop", `{"max_turns":3,"stop_condition":"no-tool-call","retry_budget":99}`,
			"an unbounded retry budget defeats the turn ceiling from the side"},
		{"stop", "react-loop", `{"max_turns":3,"stop_condition":"whenever"}`,
			"a stop condition outside the closed set is not interpretable against a stored vocabulary"},
		{"marker", "reflexion", `{"max_turns":3,"stop_condition":"answer-marker","reflection_prompt":"redo"}`,
			"a marker-terminated loop with no marker can only stop at the ceiling, which is a different " +
				"and more expensive configuration than the one asked for"},
		{"unknown", "hyper-loop", `{}`,
			"a strategy outside the closed set must not be registerable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := s.ValidateHarnessParams("e", c.strategy, json.RawMessage(c.params)); err == nil {
				t.Fatalf("validate accepted %s: %s", c.params, c.why)
			}
			// And the same input through the registering path, which must reject before it writes. A nil
			// db means a write would panic; returning an error is the assertion.
			if _, err := s.RegisterHarness(ctx, "e", c.strategy, json.RawMessage(c.params)); err == nil {
				t.Fatalf("RegisterHarness accepted %s: %s", c.params, c.why)
			} else if !errors.Is(err, ErrInvalidEntry) {
				t.Fatalf("RegisterHarness(%s) returned %v, want ErrInvalidEntry", c.params, err)
			}
		})
	}

	// The valid forms are accepted, so the rejections above are about the values and not about the
	// validator refusing everything.
	for _, ok := range []struct{ strategy, params string }{
		{StrategySingleShot, `{}`},
		{"react-loop", `{"max_turns":6,"stop_condition":"no-tool-call","retry_budget":1}`},
		{"plan-execute", `{"max_turns":4,"stop_condition":"plan-complete"}`},
		{"reflexion", `{"max_turns":3,"stop_condition":"answer-marker","answer_marker":"FINAL","reflection_prompt":"find the error"}`},
		{"critic-loop", `{"max_turns":3,"critic_model_ref":"deadbeef"}`},
	} {
		if _, _, err := s.ValidateHarnessParams("e", ok.strategy, json.RawMessage(ok.params)); err != nil {
			t.Errorf("valid %s params rejected: %v", ok.strategy, err)
		}
	}
}

// TestValidateHarnessParamsPerformsNoWrite — task 12.1. The authoring surface calls this on every
// keystroke, so "no write, no database round-trip" is a requirement rather than an implementation detail.
// A nil *sql.DB is how it is proved: a validator that touched the store would panic here.
func TestValidateHarnessParamsPerformsNoWrite(t *testing.T) {
	s := NewStore(nil, nil)
	for _, st := range BuiltinHarnessStrategies() {
		// Both a rejection and an acceptance, because only one of the two proves the absence of a write.
		if _, _, err := s.ValidateHarnessParams("draft", st.Name(), json.RawMessage(`{"nonsense":true}`)); err == nil {
			t.Errorf("%s accepted a nonsense param", st.Name())
		}
	}
	if _, _, err := s.ValidateHarnessParams("draft", StrategySingleShot, nil); err != nil {
		t.Fatalf("a valid draft was rejected: %v", err)
	}
}

// fakeModels resolves exactly the model ids it was given, and nothing else.
type fakeModels struct{ known map[string]bool }

func (f fakeModels) ResolveModel(_ context.Context, id string) (*ModelEntry, error) {
	if f.known[id] {
		return &ModelEntry{VersionID: id}, nil
	}
	return nil, fmt.Errorf("%w: model %s", ErrNotFound, id)
}

// TestCriticRefMustResolveToAModelEntry — task 2.3's cross-registry half. A critic that cannot be
// resolved cannot be pinned, so the entry is rejected BEFORE it acquires a version_id.
func TestCriticRefMustResolveToAModelEntry(t *testing.T) {
	ctx := context.Background()
	models := fakeModels{known: map[string]bool{"a-real-model": true}}

	good := json.RawMessage(`{"max_turns":3,"critic_model_ref":"a-real-model"}`)
	if err := validateCriticRef(ctx, models, "e", good); err != nil {
		t.Fatalf("a resolvable critic ref was rejected: %v", err)
	}

	bad := json.RawMessage(`{"max_turns":3,"critic_model_ref":"never-published"}`)
	err := validateCriticRef(ctx, models, "e", bad)
	if err == nil {
		t.Fatal("an unresolvable critic ref was accepted; the loop it describes would be judged by a " +
			"model nobody published, and its result would not be reproducible")
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("got %v, want ErrInvalidEntry: this is a REGISTRATION rejection, and the reader's next "+
			"question is whether anything was stored", err)
	}
	if !strings.Contains(err.Error(), "never-published") {
		t.Errorf("the rejection does not name the offending ref: %v", err)
	}

	// A strategy with no critic is unaffected — the check must not fire on the four that have none.
	if err := validateCriticRef(ctx, models, "e", json.RawMessage(`{"max_turns":3}`)); err != nil {
		t.Fatalf("a strategy with no critic ref was rejected by the critic check: %v", err)
	}
}

// TestHarnessResolveFailsClosedOnUnknownStrategy — the fail-closed direction that matters most: an entry
// naming a strategy this build does not implement must FAIL, never fall back to `single-shot`. Falling
// back would run one turn while the pinned spec named a loop, and report it under that spec's hash.
func TestHarnessResolveFailsClosedOnUnknownStrategy(t *testing.T) {
	s := NewStore(nil, nil)
	delete(s.harnesses, "react-loop") // simulate a build that predates the strategy

	st, err := s.bindHarnessStrategy("deadbeef", "react-loop")
	if err == nil {
		t.Fatalf("an unimplemented strategy bound to %q; falling back would run one turn while the "+
			"pinned spec named a loop, and report the result under that spec's config_hash", st.Name())
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "react-loop") {
		t.Errorf("the failure does not name the strategy that could not be bound: %v", err)
	}

	// The implemented strategies still bind, so the failure above is about the missing one.
	if _, err := s.bindHarnessStrategy("deadbeef", StrategySingleShot); err != nil {
		t.Fatalf("an implemented strategy failed to bind: %v", err)
	}
}
