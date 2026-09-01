package autonomy

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/task"
)

// TestEveryEffectBearingKindHasAClass.
//
// # 🔴 The build failure this exists to cause
//
// `task.EffectBearingKinds` is the closed set of things that change the world outside the platform.
// Every one of them must have an autonomy class, because the alternative to a build failure here is a
// DEFAULT — and a default is a decision in the permissive direction for whichever kind somebody forgot.
// A future "push to production" landing in Workspace would go out unapproved under the level an
// organization chose for editing files in a checkout.
//
// This is the reason the class table is not `map[Class][]string` and not a switch: both let a kind be
// absent without anything noticing.
func TestEveryEffectBearingKindHasAClass(t *testing.T) {
	for kind := range task.EffectBearingKinds {
		if _, ok := ClassOf(kind); !ok {
			t.Errorf("the effect-bearing kind %q has no autonomy class. Add it to classOf — deciding it "+
				"is a Publish change is a perfectly good answer, and leaving it out is the same answer "+
				"with nobody's name on it", kind)
		}
	}
	// And nothing in the class table that is not an effect. A class on a kind that bears no effect is
	// dead weight that reads as a guarantee.
	for kind := range classOf {
		if !task.EffectBearingKinds[kind] {
			t.Errorf("classOf carries %q, which is not an effect-bearing kind — either the kind was "+
				"renamed and this row was left behind, or it does not need a class at all", kind)
		}
	}
}

// TestTheLevelTableIsExhaustive.
func TestTheLevelTableIsExhaustive(t *testing.T) {
	classes := []Class{Workspace, Publish}
	if len(mayProceed) != len(Levels) {
		t.Errorf("the table has %d levels and Levels lists %d", len(mayProceed), len(Levels))
	}
	for _, l := range Levels {
		byClass, ok := mayProceed[l]
		if !ok {
			t.Errorf("level %q is listed but has no row, so it permits nothing", l)
			continue
		}
		for _, c := range classes {
			if _, decided := byClass[c]; !decided {
				t.Errorf("nobody has decided whether %q permits a %q change", l, c)
			}
		}
	}
	// The shape that makes an intermediate level worth having at all.
	if !MayProceed(Assisted, Workspace) || MayProceed(Assisted, Publish) {
		t.Error("`assisted` does not mean what it says: it must allow workspace changes and stop " +
			"anything reaching the customer's repository")
	}
	if MayProceed(Supervised, Workspace) || MayProceed(Supervised, Publish) {
		t.Error("`supervised` permits something without a person")
	}
	if !MayProceed(Autonomous, Publish) {
		t.Error("`autonomous` does not permit publishing, which leaves it indistinguishable from assisted")
	}
}

// TestAnUnknownLevelPermitsNothing.
//
// 🔴 A column value from a newer build, a row edited by hand, a typo in a repair script. Each must
// permit nothing — so the failure is "it keeps asking for approval", which somebody reports, rather than
// "it stopped asking", which nobody does.
func TestAnUnknownLevelPermitsNothing(t *testing.T) {
	for _, l := range []Level{"", "full", "AUTONOMOUS", " autonomous", "autonomous "} {
		for _, c := range []Class{Workspace, Publish} {
			if MayProceed(l, c) {
				t.Errorf("the unrecognised level %q permits %q", l, c)
			}
		}
		if Valid(string(l)) && l != "" {
			t.Errorf("%q is accepted by Valid but permits nothing, which is a contradiction", l)
		}
	}
}

// ── the policy ───────────────────────────────────────────────────────────────────────────────────

type fixedSource struct {
	level Level
	err   error
}

func (f fixedSource) AutonomyFor(string) (Level, error) { return f.level, f.err }

func effect(kind string) *task.Task { return &task.Task{ID: "t1", Kind: kind} }

func TestThePolicyGatesByClassAndLevel(t *testing.T) {
	g := &goal.Goal{ID: "g", Tenant: "acme"}
	for _, c := range []struct {
		level    Level
		kind     string
		wantGate bool
	}{
		{Supervised, "write_source", true},
		{Supervised, "open_pull_request", true},
		{Assisted, "write_source", false},
		{Assisted, "publish_eval_set", false},
		{Assisted, "open_pull_request", true},
		{Assisted, "deliver_change", true},
		{Autonomous, "write_source", false},
		{Autonomous, "open_pull_request", false},
	} {
		p := Policy{Source: fixedSource{level: c.level}}
		got, why := p.NeedsApproval(g, effect(c.kind))
		if got != c.wantGate {
			t.Errorf("%s + %s: gated=%v, want %v (%s)", c.level, c.kind, got, c.wantGate, why)
		}
		// 🔴 A reason in BOTH directions. When a task proceeds, that sentence is what the worker records
		// as the answer to "who approved this?" — and an empty one would leave silence, which reads as an
		// approval somebody has forgotten giving.
		if why == "" {
			t.Errorf("%s + %s: no reason given (gated=%v)", c.level, c.kind, got)
		}
		if !got && !strings.Contains(why, string(c.level)) {
			t.Errorf("%s + %s: the record of proceeding does not name the setting that allowed it: %q",
				c.level, c.kind, why)
		}
	}
}

// TestEveryFailureGates.
//
// # 🔴 The only property in this package that really matters
//
// The permissive branch is reached only when the organization's choice was read successfully AND is a
// level this build knows AND the kind has a declared class. Everything else — a database that is down, a
// tenant that does not exist, a level nobody recognises, an effect nobody classified, a policy wired
// with no source at all — ends in "wait for a person".
//
// A policy that is wrong in the permissive direction is discovered by the customer.
func TestEveryFailureGates(t *testing.T) {
	g := &goal.Goal{ID: "g", Tenant: "acme"}
	for name, p := range map[string]Policy{
		"the setting cannot be read": {Source: fixedSource{err: errors.New("connection refused")}},
		"the level is not known":     {Source: fixedSource{level: "whatever-comes-next"}},
		"the level is empty":         {Source: fixedSource{level: ""}},
		"no source is wired at all":  {},
	} {
		for _, kind := range []string{"write_source", "open_pull_request"} {
			gated, why := p.NeedsApproval(g, effect(kind))
			if !gated {
				t.Errorf("%s: %q proceeded without a person", name, kind)
			}
			if why == "" {
				t.Errorf("%s: gated with no explanation", name)
			}
		}
	}
	// An effect-bearing kind this build has no class for also gates, even at the most permissive level.
	//
	// ⚠️ Reaching this branch means registering a kind as effect-bearing WITHOUT giving it a class, which
	// TestEveryEffectBearingKindHasAClass makes impossible to commit. The branch is the backstop behind
	// that fence, and a backstop nobody has watched work is a backstop of unknown polarity — so the test
	// creates the forbidden state deliberately and puts it back.
	//
	// My first attempt used a kind that was simply unknown, which `NeedsApproval` correctly treats as
	// "not an effect at all" and never gates. It reported a hole in the policy; the hole was in the test.
	const unclassified = "launch_the_missiles"
	task.EffectBearingKinds[unclassified] = true
	defer delete(task.EffectBearingKinds, unclassified)

	p := Policy{Source: fixedSource{level: Autonomous}}
	gated, why := p.NeedsApproval(g, effect(unclassified))
	if !gated {
		t.Error("an effect-bearing kind with no autonomy class proceeded under `autonomous`; the " +
			"backstop behind the build fence does not hold")
	}
	if !strings.Contains(why, "no autonomy class") {
		t.Errorf("the refusal does not say why: %q", why)
	}
}

// TestNonEffectsAreNeverGated.
//
// Reading source and calling a model are not effects on the customer's world, and gating them would
// stop every run at its first step regardless of setting.
func TestNonEffectsAreNeverGated(t *testing.T) {
	p := Policy{Source: fixedSource{level: Supervised}}
	for _, kind := range []string{"assess_axis", "synthesise", "propose_change", "verify_proposal"} {
		if gated, why := p.NeedsApproval(&goal.Goal{Tenant: "acme"}, effect(kind)); gated {
			t.Errorf("%q was gated though it changes nothing outside the platform: %s", kind, why)
		}
	}
}
