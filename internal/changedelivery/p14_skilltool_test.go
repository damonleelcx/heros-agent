package changedelivery

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/transform"
)

// p14_skilltool_test.go — the skills and tools axis's own delivery cells (P14 §12, FR53–FR58).
//
// # The one thing this file exists to prevent
//
// A single row reading "tools are not rollout-eligible". It would be true of both cells and useful for
// neither, because they point at OPPOSITE conclusions: binding is a boundary (stop asking), tool-set
// selection is a schema gap (ask again once the field lands). This axis already made exactly this
// mistake once for language coverage, where a call site's own shape was reported as its language's gap.

// TestBindingAndToolSetCarryDifferentCauses — task 12.1.
func TestBindingAndToolSetCarryDifferentCauses(t *testing.T) {
	binding, err := RuntimeEligibility(ChangeSkillBinding, true)
	if err != nil {
		t.Fatal(err)
	}
	toolset, err := RuntimeEligibility(ChangeToolSet, true)
	if err != nil {
		t.Fatal(err)
	}

	if binding.Cause != CauseNotRuntimeResolvable {
		t.Fatalf("skill binding: got %q, want %q", binding.Cause, CauseNotRuntimeResolvable)
	}
	if toolset.Cause != CauseNoRolloutBinding {
		t.Fatalf("tool set: got %q, want %q", toolset.Cause, CauseNoRolloutBinding)
	}
	if binding.Cause == toolset.Cause {
		t.Fatal("the two cells collapsed onto one cause")
	}
	if binding.Cause.Owner() == toolset.Cause.Owner() {
		t.Fatalf("both cells report owner %q; a reader cannot tell whose move it is", binding.Cause.Owner())
	}
	if binding.Cause.Permanent() == toolset.Cause.Permanent() {
		t.Fatal("permanence does not separate the boundary from the gap")
	}
	// The binding refusal must name the construction, which is WHY it is permanent.
	if !strings.Contains(strings.ToLower(binding.Note), "construct") {
		t.Fatalf("the binding refusal does not name construction: %q", binding.Note)
	}
	// The tool-set refusal must name the absent field and attribute it to us.
	if toolset.MissingArtifact == "" {
		t.Fatal("the tool-set refusal names no missing field")
	}
	if toolset.Cause.Owner() != "the platform" {
		t.Fatalf("the tool-set gap is owned by %q, want the platform", toolset.Cause.Owner())
	}
}

// TestSkillBindingRefusalIsNotABacklogItem — task 12.2.
func TestSkillBindingRefusalIsNotABacklogItem(t *testing.T) {
	got, _ := RuntimeEligibility(ChangeSkillBinding, true)
	if got.MissingArtifact != "" {
		t.Fatalf("a permanent boundary names artifact %q", got.MissingArtifact)
	}
	low := strings.ToLower(got.Note)
	for _, banned := range []string{"not yet", "coming", "planned", "roadmap", "will land", "q1", "q2", "q3", "q4"} {
		if strings.Contains(low, banned) {
			t.Fatalf("the binding refusal contains completion language %q: %s", banned, got.Note)
		}
	}
}

// TestSkillBindingRefusesEveryRuntimeCell — task 12.3.
func TestSkillBindingRefusesEveryRuntimeCell(t *testing.T) {
	for _, bound := range []bool{true, false} {
		got, err := RuntimeEligibility(ChangeSkillBinding, bound)
		if err != nil {
			t.Fatal(err)
		}
		if got.Eligible {
			t.Fatalf("bound=%v: skill binding reported eligible", bound)
		}
		if got.Cause != CauseNotRuntimeResolvable {
			t.Fatalf("bound=%v: got %q — apply mode must not change the answer", bound, got.Cause)
		}
	}
}

// TestToolSetNamesTheMissingField — task 12.4.
func TestToolSetNamesTheMissingField(t *testing.T) {
	got, _ := RuntimeEligibility(ChangeToolSet, true)
	if got.Cause != CauseNoRolloutBinding {
		t.Fatalf("got %q, want %q", got.Cause, CauseNoRolloutBinding)
	}
	if !strings.Contains(got.MissingArtifact, "binding document field") {
		t.Fatalf("the missing artifact does not name a document field: %q", got.MissingArtifact)
	}
	// 🚫 And it is not either of the other two. `notRuntimeResolvable` would tell the reader to stop
	// asking about something we can actually build; `nodeNotBound` would send them to migrate a node
	// that is already bound.
	if got.Cause == CauseNotRuntimeResolvable || got.Cause == CauseNodeNotBound {
		t.Fatalf("the tool-set gap borrowed cause %q", got.Cause)
	}
}

// TestFrontendGapIsNotADeliveryCause — task 12.5.
//
// 🔴 Three different backlogs, and the refusal must send the reader to the right one: the frontend that
// records the tool split, the rewriter that emits the change, or the document schema that would carry
// it. The delivery layer's job here is to pass the transform's cause through UNTRANSLATED.
func TestFrontendGapIsNotADeliveryCause(t *testing.T) {
	// Find a language whose tool prune is blocked on the frontend rather than on a rewriter.
	var found bool
	for _, lang := range transform.RegisteredLanguages() {
		out := SourceOutcomeFor(ChangeToolSet, lang, "a statically-written tool list")
		if out.Materializes {
			continue
		}
		if !strings.Contains(out.MissingArtifact, "frontend") {
			continue
		}
		found = true

		// The artifact names the FRONTEND and the recorded split — not a rewriter, not a document field.
		if !strings.Contains(out.MissingArtifact, "recording") {
			t.Fatalf("%s: the frontend gap does not name what the frontend must record: %q", lang, out.MissingArtifact)
		}
		if strings.Contains(strings.ToLower(out.MissingArtifact), "binding document") {
			t.Fatalf("%s: a frontend gap was described as a document-schema gap: %q", lang, out.MissingArtifact)
		}
		// And it is distinguishable from the delivery layer's own `noRolloutBinding`, whose artifact is
		// always a document field.
		rt, _ := RuntimeEligibility(ChangeToolSet, true)
		if out.MissingArtifact == rt.MissingArtifact {
			t.Fatalf("%s: the frontend gap and the rollout-binding gap name the same artifact", lang)
		}
	}
	if !found {
		t.Skip("no language currently reports a frontend tool-split gap; the assertion has nothing to bind to")
	}
}

// TestUnpackedCallSiteGetsOneAnswerFromBothRoutes — task 12.6.
//
// 🔴 The most useful refusal on this axis and the easiest to get wrong in the reader's favour. An
// unpacked-argument call site has no tool argument to replace and no written tool list to select from,
// and it would still have none the day both the materializer and the schema field land. Telling that
// author "your language is pending" or "our document has no field yet" is true and useless.
func TestUnpackedCallSiteGetsOneAnswerFromBothRoutes(t *testing.T) {
	// A real call-site-shape refusal from the coverage table: a Python SDK that binds before the call.
	src := SourceOutcomeFor(ChangeModelWithinProvider, "python", "py.langchain.chatopenai.invoke")
	if src.Materializes {
		t.Skip("the chosen form now materializes; pick another call-site-shape refusal")
	}
	if !src.CallSiteCannotCarry {
		t.Fatalf("a call-site-shape refusal was not marked as one: %+v", src)
	}

	rep, err := BuildReport(ChangeModelWithinProvider, "python", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	srcOut, _ := rep.Outcome(RouteSource)
	rtOut, _ := rep.Outcome(RouteRuntime)

	if !srcOut.Refused() || !rtOut.Refused() {
		t.Fatalf("a call site that cannot carry the change was offered a route: source=%v runtime=%v", srcOut.Status, rtOut.Status)
	}
	if srcOut.Cause != rtOut.Cause {
		t.Fatalf("the two routes name different causes for one call-site fact: %q vs %q", srcOut.Cause, rtOut.Cause)
	}
	if srcOut.Owner != "you" || rtOut.Owner != "you" {
		t.Fatalf("a call-site fact is not owned by the reader: source=%q runtime=%q", srcOut.Owner, rtOut.Owner)
	}
	// 🚫 And neither route directs the author to wait for a materializer or a schema field.
	for _, o := range []RouteOutcome{srcOut, rtOut} {
		if o.MissingArtifact != "" {
			t.Fatalf("route %s tells the author to wait for %q", o.Route, o.MissingArtifact)
		}
	}
	// Specifically: the runtime route did NOT fall through to its own table's `noRolloutBinding`.
	if rtOut.Cause == string(CauseNoRolloutBinding) {
		t.Fatal("the runtime route offered a schema field to an author whose call site would still have nothing to act on")
	}
}

// TestBothRefusalsAreSurfacedTogether — task 12.7.
func TestBothRefusalsAreSurfacedTogether(t *testing.T) {
	// Skill binding in a language with no materializer: source refuses (no materializer), runtime
	// refuses (permanent boundary). Both must be readable, and the change must read as undeliverable.
	var lang string
	for _, l := range transform.RegisteredLanguages() {
		out := SourceOutcomeFor(ChangeSkillBinding, l, "")
		if !out.Materializes && !out.Varies {
			lang = l
			break
		}
	}
	if lang == "" {
		t.Skip("every registered language now materializes or partially materializes skill binding")
	}

	src := SourceOutcomeFor(ChangeSkillBinding, lang, "")
	rep, err := BuildReport(ChangeSkillBinding, lang, true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateUndeliverable {
		t.Fatalf("state %q, want %q", rep.State, StateUndeliverable)
	}
	srcOut, _ := rep.Outcome(RouteSource)
	rtOut, _ := rep.Outcome(RouteRuntime)
	if srcOut.Cause == "" || rtOut.Cause == "" {
		t.Fatal("a route reports no cause")
	}
	if srcOut.Cause == rtOut.Cause {
		t.Fatalf("both routes report %q; they refuse for different reasons", srcOut.Cause)
	}
	// 🚫 Never pending.
	for _, bad := range []State{StateSourcePending, StateRolloutActive, StateDelivered} {
		if rep.State == bad {
			t.Fatalf("an undeliverable change reads as %q", bad)
		}
	}
}
