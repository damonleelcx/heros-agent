package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// P34 §3 — the loop registry, and §1.4, the ceiling fence.
//
// The through-line: the loop axis exists so that an ITERATION POLICY and an EXECUTION ENVELOPE stop
// being the same registry kind. Every test here is a way that could silently fail to be true.

// ── 3.2 — the Kind is hashed into the version_id ─────────────────────────────────────────────────

// TestLoopKindIsPartOfTheContentAddress — task 3.2. The Kind being in the content address is what makes
// a loop ref pasted into the harness dimension fail CLOSED instead of resolving to the wrong entry. It
// is asserted rather than assumed, because the failure it prevents is silent: a spec that resolved a
// loop ref through the harness path would bind an iteration policy where an envelope was asked for, and
// report the run under a config_hash that says otherwise.
func TestLoopKindIsPartOfTheContentAddress(t *testing.T) {
	body := LoopSpec{Strategy: "reflexion", Params: json.RawMessage(`{"max_turns":3}`)}
	loopID, env, err := seal(KindLoop, "same-name", body)
	if err != nil {
		t.Fatalf("seal loop: %v", err)
	}
	for _, other := range []Kind{KindHarness, KindMemory, KindContext, KindModel, KindPrompt, KindSkill} {
		otherID, _, err := seal(other, "same-name", body)
		if err != nil {
			t.Fatalf("seal %s: %v", other, err)
		}
		if loopID == otherID {
			t.Fatalf("a loop entry and a %s entry with identical name and spec sealed to the same "+
				"version_id %s; the Kind is supposed to be part of the content address, and without that a "+
				"cross-dimension paste RESOLVES instead of failing closed", other, loopID)
		}
		if _, err := decodeEnvelope(other, loopID, env, nil); !errors.Is(err, ErrCorruptEntry) {
			t.Fatalf("decoding loop bytes as a %s entry returned %v, want ErrCorruptEntry", other, err)
		}
	}
}

// TestKindsAndTablesAreTotal — task 3.3's runtime half. The BUILD half is kindAnswers's parameter list:
// adding a Kind adds a parameter, and every call site fails to compile. This asserts the other thing
// that could be wrong without a build failure — that the answers were passed in the RIGHT ORDER.
func TestKindsAndTablesAreTotal(t *testing.T) {
	want := map[Kind]string{
		KindModel: tableModel, KindPrompt: tablePrompt, KindSkill: tableSkill,
		KindContext: tableContext, KindMemory: tableMemory, KindHarness: tableHarness,
		KindLoop: tableLoop,
	}
	if len(Kinds()) != len(want) {
		t.Fatalf("Kinds() has %d members, this test knows %d; a Kind added without a case here is a Kind "+
			"whose table nobody checked", len(Kinds()), len(want))
	}
	for _, k := range Kinds() {
		got, err := tableFor(k)
		if err != nil {
			t.Errorf("tableFor(%q): %v", k, err)
			continue
		}
		if got != want[k] {
			t.Errorf("tableFor(%q) = %q, want %q — the answers were passed to kindAnswers in the wrong "+
				"ORDER, which compiles and seals every entry of one kind into another kind's table",
				k, got, want[k])
		}
	}
	if _, err := tableFor(Kind("nonesuch")); err == nil {
		t.Error("tableFor accepted a kind that is not in the closed set; a Kind that came from stored " +
			"bytes rather than from this package's constants must not resolve to a table by accident")
	}
}

// ── FR2 — the vocabulary is RELOCATED, not extended ──────────────────────────────────────────────

// TestLoopVocabularyIsTheHarnessVocabularyRelocated is what makes "P34 adds no strategy" mechanical.
// A sixth loop strategy, or a loop strategy the harness axis never had, would mean the phase quietly
// grew the search space while claiming to have only moved it.
func TestLoopVocabularyIsTheHarnessVocabularyRelocated(t *testing.T) {
	loops := LoopStrategyNames()
	if len(loops) != LoopStrategySetSize {
		t.Fatalf("BuiltinLoopStrategies() has %d entries but LoopStrategySetSize is %d; adding one "+
			"requires bumping LoopStrategySetVersion (%s) too", len(loops), LoopStrategySetSize, LoopStrategySetVersion)
	}
	// The harness vocabulary minus the one member P34 ADDED there (`envelope`) must equal the loop
	// vocabulary exactly.
	legacy := map[string]bool{}
	for _, n := range HarnessStrategyNames() {
		if n != StrategyEnvelope {
			legacy[n] = true
		}
	}
	for _, n := range loops {
		if !legacy[n] {
			t.Errorf("loop strategy %q is not a member of the harness vocabulary it was relocated from; "+
				"P34 relocates, it does not extend", n)
		}
		delete(legacy, n)
	}
	for n := range legacy {
		t.Errorf("harness strategy %q has no loop counterpart; the relocation is incomplete, so a legacy "+
			"entry naming it could never be re-expressed on the new axis", n)
	}

	// The four stop conditions, read off the schemas rather than re-listed. A fifth appearing here would
	// be a vocabulary extension wearing a params change.
	want := map[string]bool{"answer-marker": true, "no-tool-call": true, "plan-complete": true, "max-turns": true}
	for _, st := range BuiltinLoopStrategies() {
		for _, cond := range stopConditionsIn(t, st.ParamsSchema()) {
			if !want[cond] {
				t.Errorf("loop strategy %q admits stop condition %q, which is outside the closed four",
					st.Name(), cond)
			}
		}
	}
}

// stopConditionsIn reads a schema's stop_condition enum.
func stopConditionsIn(t *testing.T, schema json.RawMessage) []string {
	t.Helper()
	var s struct {
		Properties struct {
			StopCondition struct {
				Enum []string `json:"enum"`
			} `json:"stop_condition"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return s.Properties.StopCondition.Enum
}

// ── 3.4 — an unimplemented strategy fails to resolve, and does NOT fall back ─────────────────────

func TestLoopNamingAnUnimplementedStrategyFailsClosed(t *testing.T) {
	s := NewStore(nil, nil)
	_, err := s.bindLoopStrategy("v1", "swarm-of-agents")
	if err == nil {
		t.Fatal("a loop naming a strategy this build does not implement bound successfully; the next " +
			"thing that happens is a single shot running under a multi-turn config_hash")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error is %v, want ErrNotFound so a caller can tell it from an infrastructure failure", err)
	}
	// The refusal must not merely fail — it must say it is not falling back, because "unsupported
	// strategy" reads to a hurried reader as "we ran the default".
	if !strings.Contains(err.Error(), StrategySingleShot) {
		t.Errorf("the refusal does not mention %q; a reader cannot tell whether a fallback happened. got: %v",
			StrategySingleShot, err)
	}
	// And it must not have quietly returned a usable strategy alongside the error.
	if st, _ := s.bindLoopStrategy("v1", "swarm-of-agents"); st != nil {
		t.Error("bindLoopStrategy returned a non-nil strategy with an error; a caller that checks the " +
			"value before the error would run the wrong loop")
	}
}

// ── 3.5 — max_turns < 1 refused, not defaulted; single-shot cannot express a turn count ──────────

func TestLoopRefusesAnInvalidTurnCount(t *testing.T) {
	s := NewStore(nil, nil)

	t.Run("zero is refused, not read as one", func(t *testing.T) {
		// Straight through the cross-field check, which is the layer that has to hold when a schema's
		// `minimum` is relaxed or a strategy is added without one.
		err := validateLoopDependencies("l", "reflexion", json.RawMessage(`{"max_turns":0}`))
		if err == nil {
			t.Fatal("max_turns 0 was accepted; a loop with no turns is not a loop, and reading it as 1 " +
				"runs a single shot under a multi-turn config_hash")
		}
		if !strings.Contains(err.Error(), StrategySingleShot) {
			t.Errorf("the refusal does not tell the author what to use for one turn: %v", err)
		}
	})

	t.Run("negative is refused", func(t *testing.T) {
		if err := validateLoopDependencies("l", "react-loop", json.RawMessage(`{"max_turns":-3}`)); err == nil {
			t.Fatal("max_turns -3 was accepted")
		}
	})

	t.Run("the schema refuses it too, before the entry is sealed", func(t *testing.T) {
		_, _, err := s.ValidateLoopParams("l", "reflexion",
			json.RawMessage(`{"max_turns":0,"stop_condition":"max-turns","reflection_prompt":"x"}`))
		if err == nil {
			t.Fatal("ValidateLoopParams accepted max_turns 0; an entry that acquires a version_id for this " +
				"is an id someone can paste into a spec forever")
		}
		if !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("error is %v, want ErrInvalidEntry", err)
		}
	})

	t.Run("single-shot cannot express a turn count at all", func(t *testing.T) {
		_, _, err := s.ValidateLoopParams("l", StrategySingleShot, json.RawMessage(`{"max_turns":3}`))
		if err == nil {
			t.Fatal("single-shot accepted max_turns; a param silently ignored is the exact mistake " +
				"someone makes when they think they selected a different strategy, and it is discovered " +
				"by reading a bill")
		}
		// And nothing can make it report more than one turn.
		e := &LoopEntry{Spec: LoopSpec{Strategy: StrategySingleShot, Params: json.RawMessage(`{"max_turns":9}`)}}
		if n, chose := e.MaxTurns(); n != 1 || chose {
			t.Errorf("single-shot MaxTurns() = (%d, %v), want (1, false): it expresses no turn count", n, chose)
		}
	})

	t.Run("a chosen count is reported as chosen", func(t *testing.T) {
		e := &LoopEntry{Spec: LoopSpec{Strategy: "reflexion", Params: json.RawMessage(`{"max_turns":4}`)}}
		if n, chose := e.MaxTurns(); n != 4 || !chose {
			t.Errorf("MaxTurns() = (%d, %v), want (4, true)", n, chose)
		}
	})

	t.Run("an absent count is not fabricated as one", func(t *testing.T) {
		// 🔴 The bool matters here: the envelope's ceiling check has nothing to compare against when no
		// value was chosen, and comparing a fabricated 1 would make the check silently pass on a shape it
		// never examined.
		e := &LoopEntry{Spec: LoopSpec{Strategy: "reflexion", Params: json.RawMessage(`{}`)}}
		if _, chose := e.MaxTurns(); chose {
			t.Error("MaxTurns() reported a chosen turn count for params that contain none")
		}
	})
}

// TestLoopRefusesARetryBudget — the retry budget is an ENVELOPE fact (decisions.md D-34.1): retries
// multiply turns, so an unbounded budget would defeat the turn ceiling from the side. A loop entry
// declaring one must be refused loudly rather than have it ignored.
func TestLoopRefusesARetryBudget(t *testing.T) {
	s := NewStore(nil, nil)
	_, _, err := s.ValidateLoopParams("l", "react-loop",
		json.RawMessage(`{"max_turns":4,"stop_condition":"max-turns","retry_budget":3}`))
	if err == nil {
		t.Fatal("a loop entry declared a retry_budget and was accepted; the author believes they have " +
			"bounded their retries and they have not — the envelope is where that is imposed")
	}
}

// ── §1.4 — raising a ceiling moves NO loop entry ─────────────────────────────────────────────────

// TestRaisingTheTurnCeilingMovesNoLoopEntry is P34 task 1.4, and it is the fence on design D2.
//
// The failure it prevents is the same orphaning chain as ADR-014's, arriving by a different door: if a
// loop entry's content carried the ceiling, then an OPERATOR raising a policy would re-hash every
// engineer's loop configuration underneath them, and every measurement filed under the old hashes would
// become unreachable. So the ceiling lives on the envelope and the value lives on the loop, and this
// asserts that the separation is structural rather than a convention.
func TestRaisingTheTurnCeilingMovesNoLoopEntry(t *testing.T) {
	loopID, _, err := seal(KindLoop, "revise-twice",
		LoopSpec{Strategy: "reflexion", Params: json.RawMessage(`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"improve it"}`)})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Two envelopes that differ ONLY in the ceiling — an operator raising a policy.
	beforeID, _, err := seal(KindHarness, "team-envelope", HarnessSpec{Strategy: StrategyEnvelope,
		Params: json.RawMessage(`{"sandbox_posture":"no-network","spend_ceiling_usd":1,"turn_ceiling":4}`)})
	if err != nil {
		t.Fatalf("seal envelope: %v", err)
	}
	afterID, _, err := seal(KindHarness, "team-envelope", HarnessSpec{Strategy: StrategyEnvelope,
		Params: json.RawMessage(`{"sandbox_posture":"no-network","spend_ceiling_usd":1,"turn_ceiling":8}`)})
	if err != nil {
		t.Fatalf("seal envelope: %v", err)
	}
	if beforeID == afterID {
		t.Fatal("raising the turn ceiling produced the SAME envelope version_id; the ceiling is not part " +
			"of the envelope's content, so a policy change would not be pinned by any config_hash and " +
			"could not be audited after the fact")
	}

	// The loop entry, re-sealed from identical content, is unchanged. It has no ceiling to carry.
	loopAgain, _, err := seal(KindLoop, "revise-twice",
		LoopSpec{Strategy: "reflexion", Params: json.RawMessage(`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"improve it"}`)})
	if err != nil {
		t.Fatalf("re-seal: %v", err)
	}
	if loopAgain != loopID {
		t.Fatalf("the loop entry's version_id moved from %s to %s with no change to its content", loopID, loopAgain)
	}

	// The structural half: a ceiling is INEXPRESSIBLE on a loop entry. If it were merely absent from
	// these fixtures, the equality above would be a property of the fixtures and not of the design.
	s := NewStore(nil, nil)
	if _, _, err := s.ValidateLoopParams("l", "reflexion",
		json.RawMessage(`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"x","turn_ceiling":8}`)); err == nil {
		t.Error("a loop entry accepted a turn_ceiling. While that is expressible on a loop, an operator " +
			"raising a policy CAN move a loop entry's hash, and design D2's guarantee is a convention " +
			"rather than a structure")
	}
	// And the mirror: max_turns is inexpressible on an envelope.
	if _, _, err := s.ValidateHarnessParams("e", StrategyEnvelope,
		json.RawMessage(`{"sandbox_posture":"no-network","spend_ceiling_usd":1,"turn_ceiling":4,"max_turns":2}`)); err == nil {
		t.Error("an envelope accepted max_turns; the value an author chooses would then be settable by " +
			"whoever owns the policy, which is the conflation this phase exists to end")
	}
}

// ── the envelope ─────────────────────────────────────────────────────────────────────────────────

// TestEnvelopeRequiresItsBlastRadiusStatements — task 4.1. The three required fields are each a
// blast-radius statement, and the honest default for one of those is that there is not one.
func TestEnvelopeRequiresItsBlastRadiusStatements(t *testing.T) {
	s := NewStore(nil, nil)
	for _, tc := range []struct{ name, params string }{
		{"no posture", `{"turn_ceiling":4,"spend_ceiling_usd":1}`},
		{"no turn ceiling", `{"sandbox_posture":"no-network","spend_ceiling_usd":1}`},
		{"no spend ceiling", `{"sandbox_posture":"no-network","turn_ceiling":4}`},
		{"nothing at all", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.ValidateHarnessParams("e", StrategyEnvelope, json.RawMessage(tc.params)); err == nil {
				t.Fatalf("an envelope with %s was accepted; an omitted ceiling reads as \"unbounded\" to a "+
					"reader and has to be read as SOME number by the code, and those two readings differing "+
					"is how a policy stops being a policy", tc.name)
			}
		})
	}
	// The valid shape, for contrast — otherwise the loop above would pass on a schema that rejects
	// everything.
	if _, _, err := s.ValidateHarnessParams("e", StrategyEnvelope,
		json.RawMessage(`{"sandbox_posture":"no-network","turn_ceiling":4,"spend_ceiling_usd":0.5}`)); err != nil {
		t.Fatalf("a well-formed envelope was rejected: %v", err)
	}
}

// TestLoopBearingIsOneRuleInOnePlace — task 3.6's predicate. The ambiguity refusal keys on this, so a
// wrong answer here is a spec that resolves with two contradictory iteration policies.
func TestLoopBearingIsOneRuleInOnePlace(t *testing.T) {
	for _, strategy := range HarnessStrategyNames() {
		e := &HarnessEntry{VersionID: "h", Spec: HarnessSpec{Strategy: strategy}}
		wantLoop := strategy != StrategyEnvelope
		if got := e.IsLoopBearing(); got != wantLoop {
			t.Errorf("harness strategy %q: IsLoopBearing() = %v, want %v", strategy, got, wantLoop)
		}
		if got := e.IsEnvelope(); got != (strategy == StrategyEnvelope) {
			t.Errorf("harness strategy %q: IsEnvelope() = %v", strategy, got)
		}
	}
	// 🔴 single-shot IS loop-bearing, deliberately: it states an iteration policy ("exactly one turn"),
	// so a spec that also sets a loop_ref has stated the policy twice and the two may disagree.
	single := &HarnessEntry{Spec: HarnessSpec{Strategy: StrategySingleShot}}
	if !single.IsLoopBearing() {
		t.Error("single-shot is not reported as loop-bearing; a spec pairing it with a loop_ref would " +
			"then resolve with two iteration policies, one of which is silently ignored")
	}
}

// TestEnvelopeOfFailsClosed — a corrupt envelope must not read as the most permissive possible policy.
func TestEnvelopeOfFailsClosed(t *testing.T) {
	bad := &HarnessEntry{VersionID: "h", Spec: HarnessSpec{Strategy: StrategyEnvelope,
		Params: json.RawMessage(`"not an object"`)}}
	env, isEnv, err := EnvelopeOf(bad)
	if !isEnv {
		t.Fatal("an entry naming the envelope strategy was not recognised as one")
	}
	if err == nil {
		t.Fatal("undecodable envelope params returned no error; a zero Envelope provides no host service " +
			"and imposes no ceiling, so a corrupt entry would read as the most permissive policy available")
	}
	if env.Provides(HostServiceToolExecutor) {
		t.Error("the zero envelope claims to provide a host service")
	}
}

// TestHostServicesForLoopAgreesWithTheRuntime — the resolve-time gate and the run-time refusal must
// name the same requirement. Two answers to "what does react-loop need" is how a preflight permits
// something the run then refuses, which is worse than either check alone.
func TestHostServicesForLoopAgreesWithTheRuntime(t *testing.T) {
	// Spelled here rather than imported: internal/harnessruntime imports nothing from this package's
	// direction, and a test that imported it would couple the sealed vocabulary to a runtime. The
	// counterpart assertion lives in internal/harnessruntime and reads THIS function.
	want := map[string][]string{
		StrategySingleShot: nil,
		"reflexion":        nil,
		"react-loop":       {HostServiceToolExecutor},
		"plan-execute":     {HostServicePlanner},
		"critic-loop":      {HostServiceCritic},
	}
	for _, st := range BuiltinLoopStrategies() {
		got := HostServicesForLoop(st.Name())
		exp := want[st.Name()]
		if strings.Join(got, ",") != strings.Join(exp, ",") {
			t.Errorf("HostServicesForLoop(%q) = %v, want %v", st.Name(), got, exp)
		}
	}
}

// TestLoopStrategyNamedFailsClosed — a caller must never invent a strategy from a miss.
func TestLoopStrategyNamedFailsClosed(t *testing.T) {
	if LoopStrategyNamed("nonesuch") != nil {
		t.Error("LoopStrategyNamed invented a strategy for a name outside the closed set")
	}
	for _, n := range LoopStrategyNames() {
		if LoopStrategyNamed(n) == nil {
			t.Errorf("LoopStrategyNamed(%q) is nil for a builtin", n)
		}
	}
}

// TestRegisterLoopRefusesAnUnresolvableCritic — the cross-registry half, on the same terms as the
// harness axis's: an entry that acquired a version_id and then failed to resolve its critic would be an
// id someone can paste into a spec forever.
func TestRegisterLoopRefusesAnUnresolvableCritic(t *testing.T) {
	err := validateLoopCriticRef(context.Background(), missingModels{}, "l",
		json.RawMessage(`{"max_turns":3,"critic_model_ref":"m-nope"}`))
	if err == nil {
		t.Fatal("a loop naming a critic nobody published was accepted")
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("error is %v, want ErrInvalidEntry — the reader's next question is \"was anything "+
			"stored?\" and the answer is no", err)
	}
}

type missingModels struct{}

func (missingModels) ResolveModel(context.Context, string) (*ModelEntry, error) {
	return nil, ErrNotFound
}
