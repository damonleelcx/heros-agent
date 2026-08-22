package variantspec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P34 §3–§4 at the RESOLVE boundary: the loop axis, the ambiguity refusal, and the two gates that moved
// left from the run path.
//
// Everything here is a refusal that happens BEFORE any diff, worktree, build or provider call exists,
// which is the property that makes them worth having at all. A gate that fires after a codemod is in the
// tree is a diagnosis, not a gate.

func loopSpec(mutate func(o *NodeOverride)) *VariantSpec {
	s := &VariantSpec{
		SourceRevision: "rev1",
		Order:          []string{"n_a"},
		Nodes:          map[string]NodeOverride{},
	}
	var o NodeOverride
	mutate(&o)
	s.Nodes["n_a"] = o
	return s
}

// ── 3.1 — the dimension ──────────────────────────────────────────────────────────────────────────

func TestDimLoopInClosedEnum(t *testing.T) {
	if DimLoop != "loop" {
		t.Fatalf("DimLoop = %q, want loop: every error names it, spec rows record it, and the console "+
			"keys on it, so the wire value cannot drift (task 3.1)", DimLoop)
	}
	found := false
	for _, d := range Dimensions() {
		if d == DimLoop {
			found = true
		}
	}
	if !found {
		t.Fatalf("Dimensions() omits loop (%v); a consumer iterating dimensions would silently miss it, "+
			"which is exactly how an axis ends up modelled and never dispatched", Dimensions())
	}
}

func TestDimensionsReportsLoopIffSet(t *testing.T) {
	t.Run("absent when not overridden", func(t *testing.T) {
		for _, d := range (ResolvedOverride{Model: &registry.ModelEntry{}}).Dimensions() {
			if d == DimLoop {
				t.Fatal("Dimensions() reports loop for an override that set none of it")
			}
		}
	})
	t.Run("present for an explicit single-shot", func(t *testing.T) {
		// 🔴 An explicit identity IS an override. It hashes as absent, but the transform has to be able to
		// tell "the author asked for the identity" from "the author said nothing".
		ro := ResolvedOverride{Loop: &registry.LoopEntry{
			Spec: registry.LoopSpec{Strategy: registry.StrategySingleShot}}}
		found := false
		for _, d := range ro.Dimensions() {
			if d == DimLoop {
				found = true
			}
		}
		if !found {
			t.Fatal("Dimensions() hides an explicit single-shot loop override")
		}
	})
}

// TestLoopRefIsAdditiveInTheHash — the compatibility half at the value level. The recorded fixture
// (p34_compat_test.go) covers the bytes; this covers the equality D-8 extends to the new axis.
func TestLoopRefIsAdditiveInTheHash(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	regs.addLoop(t, "l-single", "identity", registry.StrategySingleShot, `{}`)

	bare, err := Resolve(ctx, baseSpec(), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	spec := baseSpec()
	spec.Nodes["n_a"] = NodeOverride{LoopRef: "l-single"}
	withIdentity, err := Resolve(ctx, spec, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bare.ConfigHash != withIdentity.ConfigHash {
		t.Errorf("an explicitly-single-shot loop hashed differently from no loop at all:\n  %s\n  %s\n"+
			"`single-shot` ≡ absent is what lets a user back out of an authored loop change with no "+
			"residue in the hash (D-8 applied to the new axis)", bare.ConfigHash, withIdentity.ConfigHash)
	}
	// ...and the transform can still tell them apart.
	if withIdentity.Overrides["n_a"].Loop == nil {
		t.Error("an explicit single-shot loop left no override for the transform to see")
	}

	// A real loop DOES move the hash — otherwise the equality above would be a property of the projection
	// dropping everything rather than of the identity being the identity.
	regs.addLoop(t, "l-reflex", "revise", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"improve it"}`)
	spec.Nodes["n_a"] = NodeOverride{LoopRef: "l-reflex"}
	withLoop, err := Resolve(ctx, spec, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if withLoop.ConfigHash == bare.ConfigHash {
		t.Error("binding a three-turn reflexion loop did not change config_hash; the loop is not reaching " +
			"the hashed projection, so two different computations share one identity")
	}
}

// ── 3.4 — a loop that names nothing, or names a strategy this build lacks ────────────────────────

func TestLoopRefThatResolvesToNothingIsRefused(t *testing.T) {
	_, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) { o.LoopRef = "l-nope" }),
		testIR(), newFakeRegistries())
	var se *SpecError
	if !errors.As(err, &se) || !errors.Is(err, ErrUnresolvedRef) {
		t.Fatalf("got %v, want ErrUnresolvedRef", err)
	}
	if se.Dim != DimLoop {
		t.Errorf("the refusal names dimension %q, want loop — a reader has to know which axis to look at", se.Dim)
	}
}

func TestALoopRefIsNotAHarnessRef(t *testing.T) {
	// The registries are separate maps, exactly as the real store's id spaces are separated by the Kind
	// being hashed into the content address. A harness ref pasted into loop_ref must MISS.
	regs := newFakeRegistries()
	regs.addHarness(t, "h-1", "legacy", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"x"}`)
	_, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) { o.LoopRef = "h-1" }), testIR(), regs)
	if !errors.Is(err, ErrUnresolvedRef) {
		t.Fatalf("a harness ref resolved through loop_ref (%v); the Kind is hashed into the version_id "+
			"precisely so a cross-axis paste fails closed rather than binding the wrong thing", err)
	}
}

func TestLoopRefRefusesAnInlineDefinition(t *testing.T) {
	_, err := Resolve(context.Background(),
		loopSpec(func(o *NodeOverride) { o.LoopRef = `{"strategy":"reflexion"}` }), testIR(), newFakeRegistries())
	if !errors.Is(err, ErrInlineDefinition) {
		t.Fatalf("got %v, want ErrInlineDefinition: a spec carrying its own strategy params is a "+
			"configuration whose content lives outside any registry, so it could never be resolved back "+
			"from a config_hash months later", err)
	}
}

// ── 3.6 — the ambiguity refusal, naming BOTH refs ───────────────────────────────────────────────

// TestBothRefsSetIsRefusedNamingBoth is P34 FR10 and QA fence 9.4.
//
// 🔴 Naming both is the requirement. The author has stated their iteration policy twice and the two may
// disagree; a refusal that named one of them would send them to change the one that was fine.
func TestBothRefsSetIsRefusedNamingBoth(t *testing.T) {
	regs := newFakeRegistries()
	regs.addHarness(t, "h-legacy", "legacy-loop", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"x"}`)
	regs.addLoop(t, "l-new", "new-loop", "reflexion",
		`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"y"}`)

	_, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) {
		o.HarnessRef = "h-legacy"
		o.LoopRef = "l-new"
	}), testIR(), regs)

	if !errors.Is(err, ErrAmbiguousAxis) {
		t.Fatalf("got %v, want ErrAmbiguousAxis", err)
	}
	msg := err.Error()
	for _, want := range []string{"h-legacy", "l-new"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q. Both refs must appear: the author stated their "+
				"iteration policy twice, and naming one of them sends them to change the wrong one.\n  got: %s",
				want, msg)
		}
	}
	if !strings.Contains(msg, registry.StrategyEnvelope) {
		t.Errorf("the refusal does not say what to do instead (repoint harness_ref at an %q entry): %s",
			registry.StrategyEnvelope, msg)
	}
}

// TestAnEnvelopeHarnessRefAndALoopRefAreNotAmbiguous — the other side of the same rule, and the one that
// makes the axis usable. An ENVELOPE plus a loop is the post-P34 shape, not a conflict.
func TestAnEnvelopeHarnessRefAndALoopRefAreNotAmbiguous(t *testing.T) {
	regs := newFakeRegistries()
	regs.addEnvelope(t, "h-env", "team", `{"sandbox_posture":"no-network","turn_ceiling":8,"spend_ceiling_usd":1}`)
	regs.addLoop(t, "l-new", "revise", "reflexion",
		`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"y"}`)

	got, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) {
		o.HarnessRef = "h-env"
		o.LoopRef = "l-new"
	}), testIR(), regs)
	if err != nil {
		t.Fatalf("an envelope plus a loop was refused: %v. That pairing IS the post-P34 shape — the "+
			"envelope imposes, the loop chooses — so refusing it would leave no way to author the axis", err)
	}
	ro := got.Overrides["n_a"]
	if ro.Envelope == nil {
		t.Error("the envelope was not decoded onto the override; every downstream gate reads it, and a nil " +
			"here means each of them would fall back to a permissive default")
	}
	if ro.Loop == nil {
		t.Error("the loop was not recorded on the override")
	}
}

// TestLegacyLoopBearingHarnessAloneStillResolves is ADR-014's promise, checked at the value level. The
// recorded fixture checks the BYTES; this checks that nothing new refuses it.
func TestLegacyLoopBearingHarnessAloneStillResolves(t *testing.T) {
	regs := newFakeRegistries()
	// A react-loop needs a tool executor. Under the LOOP axis that is refused at resolve when no envelope
	// provides one — and under the LEGACY axis it must NOT be, or every pre-P34 spec that pinned one
	// stops resolving, which is the failure ADR-014 spent its whole argument preventing.
	regs.addHarness(t, "h-react", "legacy-react", "react-loop", `{"max_turns":6,"stop_condition":"no-tool-call"}`)
	if _, err := Resolve(context.Background(),
		loopSpec(func(o *NodeOverride) { o.HarnessRef = "h-react" }), testIR(), regs); err != nil {
		t.Fatalf("a pre-P34 spec on a loop-bearing harness entry stopped resolving: %v.\n"+
			"The legacy path is permanent (decisions.md D-34.5); a resolve-time gate added to it would "+
			"orphan every measurement taken on a multi-turn node", err)
	}
}

// ── 4.2 — the ceiling, naming BOTH values ────────────────────────────────────────────────────────

func TestMaxTurnsAboveTheEnvelopeCeilingIsRefused(t *testing.T) {
	regs := newFakeRegistries()
	regs.addEnvelope(t, "h-env", "tight", `{"sandbox_posture":"no-network","turn_ceiling":3,"spend_ceiling_usd":1}`)
	regs.addLoop(t, "l-big", "long", "reflexion",
		`{"max_turns":9,"stop_condition":"max-turns","reflection_prompt":"y"}`)

	_, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) {
		o.HarnessRef = "h-env"
		o.LoopRef = "l-big"
	}), testIR(), regs)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded", err)
	}
	msg := err.Error()
	for _, want := range []string{"9", "3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name the value %q. BOTH numbers are required: without them an "+
				"author cannot tell whether to lower their value or ask for a higher policy, and those are "+
				"requests to two different people.\n  got: %s", want, msg)
		}
	}
}

func TestMaxTurnsAtTheCeilingIsAdmitted(t *testing.T) {
	regs := newFakeRegistries()
	regs.addEnvelope(t, "h-env", "exact", `{"sandbox_posture":"no-network","turn_ceiling":4,"spend_ceiling_usd":1}`)
	regs.addLoop(t, "l-4", "four", "reflexion",
		`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"y"}`)
	if _, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) {
		o.HarnessRef = "h-env"
		o.LoopRef = "l-4"
	}), testIR(), regs); err != nil {
		t.Fatalf("max_turns exactly AT the ceiling was refused: %v. A ceiling is an upper bound, and an "+
			"off-by-one here makes every declared policy quietly one turn tighter than it reads", err)
	}
}

// TestNoEnvelopeLeavesThePlatformCeilingStanding — a node may bind a loop without binding an envelope,
// and it is still bounded. Refusing every un-enveloped loop would couple the two axes back together,
// which is the thing the split exists to undo.
func TestNoEnvelopeLeavesThePlatformCeilingStanding(t *testing.T) {
	regs := newFakeRegistries()
	regs.addLoop(t, "l-max", "at-the-cap", "reflexion",
		`{"max_turns":16,"stop_condition":"max-turns","reflection_prompt":"y"}`)
	if _, err := Resolve(context.Background(),
		loopSpec(func(o *NodeOverride) { o.LoopRef = "l-max" }), testIR(), regs); err != nil {
		t.Fatalf("a loop with no envelope was refused: %v", err)
	}
	// And the platform ceiling is real: a loop entry above it cannot be SEALED at all, so no spec can
	// reference one. That is what makes the absence of an envelope safe rather than merely untested.
	s := registry.NewStore(nil, nil)
	if _, _, err := s.ValidateLoopParams("l", "reflexion",
		json.RawMessage(`{"max_turns":17,"stop_condition":"max-turns","reflection_prompt":"y"}`)); err == nil {
		t.Error("a loop above the platform ceiling sealed; an un-enveloped loop would then be unbounded")
	}
}

// ── 4.3 — the host-service refusal, moved LEFT ──────────────────────────────────────────────────

// TestMissingHostServiceIsRefusedAtResolve is FR7 and QA fence 9.6.
//
// 🔴 The assertion is about WHERE, not only whether. The equivalent refusal already existed in
// internal/harnessruntime and fired when a RUN reached the node — after the codemod had been generated
// and applied. This one is a preflight answer.
func TestMissingHostServiceIsRefusedAtResolve(t *testing.T) {
	for _, tc := range []struct{ strategy, params, service string }{
		{"react-loop", `{"max_turns":4,"stop_condition":"no-tool-call"}`, registry.HostServiceToolExecutor},
		{"plan-execute", `{"max_turns":4,"stop_condition":"plan-complete"}`, registry.HostServicePlanner},
		{"critic-loop", `{"max_turns":4,"critic_model_ref":"m-1"}`, registry.HostServiceCritic},
	} {
		t.Run(tc.strategy, func(t *testing.T) {
			regs := newFakeRegistries()
			// An envelope that provides SOME service but not this one — a stricter subject than an empty
			// one, because it proves the check reads the set rather than merely its emptiness.
			regs.addEnvelope(t, "h-env", "partial",
				`{"sandbox_posture":"no-network","turn_ceiling":8,"spend_ceiling_usd":1,"host_services":["planner"]}`)
			regs.addLoop(t, "l-x", "needs-a-host", tc.strategy, tc.params)

			_, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) {
				o.HarnessRef = "h-env"
				o.LoopRef = "l-x"
			}), testIR(), regs)

			if tc.service == registry.HostServicePlanner {
				if err != nil {
					t.Fatalf("the envelope provides a planner and %s was still refused: %v", tc.strategy, err)
				}
				return
			}
			if !errors.Is(err, ErrMissingHostService) {
				t.Fatalf("got %v, want ErrMissingHostService", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.strategy) || !strings.Contains(msg, tc.service) {
				t.Errorf("the refusal must name the loop AND the missing service; got: %s", msg)
			}
			// 🚫 And it must say it is not degrading. "Unsupported" reads to a hurried reader as "we ran
			// something else", which is precisely the substitution that reports one strategy under
			// another's config_hash.
			if !strings.Contains(msg, "NOT degraded") {
				t.Errorf("the refusal does not say it declines to substitute a strategy: %s", msg)
			}
		})
	}
}

func TestNoEnvelopeAtAllStillRefusesAHostNeedingLoop(t *testing.T) {
	// 🔴 Deliberately asymmetric with the ceiling: a missing ceiling leaves the platform ceiling standing,
	// so absence is safe. A missing host service has no fallback — `react-loop` with nothing to run its
	// tools is not a slower react-loop, it is a different strategy.
	regs := newFakeRegistries()
	regs.addLoop(t, "l-react", "tools", "react-loop", `{"max_turns":4,"stop_condition":"no-tool-call"}`)
	_, err := Resolve(context.Background(),
		loopSpec(func(o *NodeOverride) { o.LoopRef = "l-react" }), testIR(), regs)
	if !errors.Is(err, ErrMissingHostService) {
		t.Fatalf("got %v, want ErrMissingHostService for a tool loop with no envelope at all", err)
	}
	if !strings.Contains(err.Error(), "binds no execution envelope") {
		t.Errorf("the refusal does not distinguish \"no envelope\" from \"an envelope that grants nothing\"; "+
			"they need different fixes: %v", err)
	}
}

// TestALoopNeedingNoHostResolvesWithoutAnEnvelope keeps the gate from becoming a blanket refusal. If
// this went red, the axis would be unusable for the one strategy that can actually be materialized.
func TestALoopNeedingNoHostResolvesWithoutAnEnvelope(t *testing.T) {
	regs := newFakeRegistries()
	regs.addLoop(t, "l-reflex", "revise", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"improve it"}`)
	if _, err := Resolve(context.Background(),
		loopSpec(func(o *NodeOverride) { o.LoopRef = "l-reflex" }), testIR(), regs); err != nil {
		t.Fatalf("reflexion needs no second actor and was refused anyway: %v", err)
	}
}

// TestACorruptEnvelopeIsRefusedRatherThanReadPermissively — the zero Envelope provides no host service
// and imposes no ceiling, which is the most permissive policy available. Reaching it by accident, at the
// exact moment the ceilings are being checked, is the failure this asserts against.
func TestACorruptEnvelopeIsRefusedRatherThanReadPermissively(t *testing.T) {
	regs := newFakeRegistries()
	// Bypasses the schema on purpose: this is the shape a row written by some other path would have.
	regs.addHarness(t, "h-bad", "corrupt", registry.StrategyEnvelope, `"not an object"`)
	regs.addLoop(t, "l-big", "long", "reflexion",
		`{"max_turns":9,"stop_condition":"max-turns","reflection_prompt":"y"}`)
	_, err := Resolve(context.Background(), loopSpec(func(o *NodeOverride) {
		o.HarnessRef = "h-bad"
		o.LoopRef = "l-big"
	}), testIR(), regs)
	if err == nil {
		t.Fatal("a spec resolved against an undecodable envelope; every ceiling under it was then checked " +
			"against a zero value, which permits everything")
	}
}
