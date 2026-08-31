package intent

import "testing"

// TestTheSetIsClosedAndComplete is the structural fence. It fails when an intent is added without a
// tier, a question, or a valid axis — the three ways an intent becomes unreachable in a way that looks
// exactly like the feature not existing.
func TestTheSetIsClosedAndComplete(t *testing.T) {
	if got, want := len(All()), 19; got != want {
		t.Fatalf("the intent set has %d members; the design declared %d "+
			"(15 carried forward + skills + tools + evalset + the prompt_model split)", got, want)
	}
	axes := map[string]bool{}
	for _, a := range Axes() {
		axes[a] = true
	}
	seen := map[Intent]bool{}
	for _, s := range All() {
		if seen[s.Intent] {
			t.Errorf("%s appears twice; a duplicated intent silently shadows one of its rows", s.Intent)
		}
		seen[s.Intent] = true
		if s.Question == "" {
			t.Errorf("%s has no question, so a refusal cannot render it and the user is shown "+
				"a boundary that omits a thing the system can do", s.Intent)
		}
		switch s.Tier {
		case TierGoal, TierQuery, TierEffect:
		default:
			t.Errorf("%s has tier %q, which is none of the three", s.Intent, s.Tier)
		}
		if s.Axis != "" && !axes[s.Axis] {
			t.Errorf("%s names axis %q, which is not one of the nine", s.Intent, s.Axis)
		}
	}
}

// TestEveryAxisIsReachable is the fence that would have caught the actual gap in the previous system.
//
// 🔴 `skills` and `tools` were configuration dimensions AND assessment axes there, and neither had an
// intent. Nothing was red. A person asking about them got a well-formed refusal indistinguishable from
// the feature not existing — and they are two of the six surfaces the product is named after.
func TestEveryAxisIsReachable(t *testing.T) {
	reached := map[string]bool{}
	for _, s := range All() {
		if s.Axis != "" {
			reached[s.Axis] = true
		}
	}
	for _, a := range Axes() {
		if !reached[a] {
			t.Errorf("axis %q has NO intent: a person can ask about it and the agent will refuse, "+
				"and the refusal is indistinguishable from the axis not existing", a)
		}
	}
}

// TestDurabilityIsDecidedInExactlyOnePlace. Tier is the single discriminator, so a new intent cannot
// accidentally inherit a queue, a lease and a checkpoint by being declared next to one that has them.
func TestDurabilityIsDecidedInExactlyOnePlace(t *testing.T) {
	for _, s := range All() {
		if got, want := s.Intent.Durable(), s.Tier == TierGoal; got != want {
			t.Errorf("%s: Durable()=%v but tier is %q", s.Intent, got, s.Tier)
		}
	}
	if got, want := len(InTier(TierGoal)), 4; got != want {
		t.Fatalf("%d durable goals, want %d: putting a queue behind a database read is the "+
			"over-application this tiering exists to prevent", got, want)
	}
}

// TestDurabilityIsNotAProxyForSafety. `improve` is long-running AND opens a pull request. A system that
// treats "durable" as "safe" will eventually let a goal write to a repository without a gate.
func TestDurabilityIsNotAProxyForSafety(t *testing.T) {
	if !Improve.Durable() || !Improve.EffectBearing() {
		t.Fatalf("improve must be both durable and effect-bearing; got durable=%v effect=%v",
			Improve.Durable(), Improve.EffectBearing())
	}
	if Assess.EffectBearing() {
		t.Error("assess is read-only; marking it effect-bearing would put a gate in front of a report")
	}
	for _, i := range InTier(TierQuery) {
		if i.EffectBearing() {
			t.Errorf("%s is a query and must not be effect-bearing", i)
		}
	}
}

// TestTheRefusalListMatchesTheTable. The list a user reads is generated, never written as copy.
func TestTheRefusalListMatchesTheTable(t *testing.T) {
	if len(CanDo()) != len(All()) {
		t.Fatalf("CanDo() lists %d things and the set has %d", len(CanDo()), len(All()))
	}
}

// TestOutOfScopeRedirectionsNameASurfaceAndWhatItDoes. A redirection that does not say where the thing
// IS done is just a rejection wearing a helpful tone.
func TestOutOfScopeRedirectionsNameASurfaceAndWhatItDoes(t *testing.T) {
	for _, o := range OutOfScopeTopics() {
		if o.Surface == "" || o.Does == "" {
			t.Errorf("out-of-scope topic %q does not name both a surface and what it does", o.Topic)
		}
		if Intent(o.Topic).Valid() {
			t.Errorf("%q is both an intent and out of scope", o.Topic)
		}
	}
}

// TestUnknownIntentsAreInvalid. Fail closed: an unrecognised intent is never defaulted to a tier.
func TestUnknownIntentsAreInvalid(t *testing.T) {
	for _, bad := range []Intent{"", "prompt_model", "billing", "PROMPT"} {
		if bad.Valid() {
			t.Errorf("%q reports valid", bad)
		}
		if bad.Durable() || bad.EffectBearing() {
			t.Errorf("%q was given properties despite not being in the set", bad)
		}
	}
}
