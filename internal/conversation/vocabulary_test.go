package conversation

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// vocabulary_test.go fences the §1 enums.
//
// # What these can and cannot catch
//
// They catch a vocabulary that grew a member nobody closed over, a phase order that stopped being
// monotonic, and a stop vocabulary that got duplicated instead of extended. They do NOT catch a kind
// that is emitted wrongly — that is emitter_test.go — nor a kind the browser cannot render, which is
// the generated-union fence in cmd/consoletypes. Three different failures, three different fences.

func TestKindVocabularyIsClosedAndComplete(t *testing.T) {
	// The eight of PRD FR1, written out here rather than derived from Kinds(): a test that read the
	// same slice the implementation reads would pass no matter what that slice contained.
	want := []Kind{
		"plan", "progress", "finding", "proposal",
		"approval_request", "result", "refusal", "answer",
	}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("the vocabulary has %d kinds; PRD FR1 names %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("kind[%d] = %q, want %q", i, got[i], w)
		}
		if !w.Valid() {
			t.Errorf("%q is in the vocabulary but Valid() refuses it", w)
		}
	}
	for _, bad := range []Kind{"", "chat", "message", "Plan", "plan "} {
		if bad.Valid() {
			t.Errorf("Valid() admitted %q; the vocabulary is supposed to be closed", bad)
		}
	}
}

func TestTerminalKindsAreResultAndRefusal(t *testing.T) {
	for _, k := range Kinds() {
		want := k == KindResult || k == KindRefusal
		if k.Terminal() != want {
			t.Errorf("%s.Terminal() = %v, want %v", k, k.Terminal(), want)
		}
	}
}

func TestProvenanceHasNoDefault(t *testing.T) {
	if Provenance("").Valid() {
		t.Fatal("an empty provenance is valid; a message whose origin nobody recorded would then " +
			"claim to have been generated, which is exactly the unfalsifiable state D6 exists to prevent")
	}
	if !ProvenancePinned.Valid() || !ProvenanceGenerated.Valid() {
		t.Fatal("one of the two real provenances is refused")
	}
	if Provenance("cached").Valid() {
		t.Error("Valid() admitted a third provenance")
	}
}

func TestPhasesAreTheFiveInOrder(t *testing.T) {
	want := []Phase{"understand", "plan", "act", "verify", "respond"}
	got := Phases()
	if len(got) != 5 {
		t.Fatalf("there are %d phases; FR16 names five: %v", len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("phase[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestPhaseOrderIsMonotonicAndVerifyPrecedesRespond(t *testing.T) {
	// The spec scenario: "the phase advances monotonically through the five, never skipping `verify`
	// before `respond`". Stated as an assertion over the order rather than as a comment.
	if !PhaseRespond.After(PhaseVerify) {
		t.Error("respond does not come after verify; a turn could answer without verifying")
	}
	if PhaseVerify.After(PhaseRespond) {
		t.Error("verify claims to come after respond")
	}
	all := Phases()
	for i := range all {
		for j := range all {
			if want := i >= j; all[i].After(all[j]) != want {
				t.Errorf("%s.After(%s) = %v, want %v", all[i], all[j], all[i].After(all[j]), want)
			}
		}
	}
	// An unknown phase is never "after" anything: treating a typo as late is how a turn skips verify.
	if Phase("done").After(PhaseUnderstand) {
		t.Error("an unknown phase reported itself as after understand")
	}
	if PhaseRespond.After(Phase("done")) {
		t.Error("a real phase reported itself as after an unknown one")
	}
}

func TestStepStatesAreTheFourAndOnlyDoneNeedsNoReason(t *testing.T) {
	want := map[StepState]bool{ // state → needs a reason
		StepDone: false, StepSkipped: true, StepRefused: true, StepNotMeasured: true,
	}
	got := StepStates()
	if len(got) != len(want) {
		t.Fatalf("there are %d step states; FR19 names four: %v", len(got), got)
	}
	for _, s := range got {
		needs, ok := want[s]
		if !ok {
			t.Errorf("unexpected step state %q", s)
			continue
		}
		if s.NeedsReason() != needs {
			t.Errorf("%s.NeedsReason() = %v, want %v", s, s.NeedsReason(), needs)
		}
	}
	if StepState("").Valid() || StepState("pending").Valid() {
		t.Error("the step-state vocabulary is not closed")
	}
}

func TestFindingStatesAreTheFourTheConsoleRenders(t *testing.T) {
	// Task 4.3 renders four. A fifth added here without a renderer is a blank card, so the count is
	// asserted as well as the membership.
	want := []FindingState{"measured", "not_measured", "refused", "stale"}
	got := FindingStates()
	if len(got) != len(want) {
		t.Fatalf("there are %d finding states; the console renders %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("finding state[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestFailureClassesStayThree(t *testing.T) {
	if len(FailureClasses()) != 3 {
		t.Fatalf("the three failure classes became %d: %v", len(FailureClasses()), FailureClasses())
	}
	for _, f := range FailureClasses() {
		if !f.Valid() {
			t.Errorf("%q is listed but Valid() refuses it", f)
		}
	}
	if FailureClass("error").Valid() {
		t.Error("a fourth failure class was admitted; flattening three into one is the failure this prevents")
	}
}

// TestConversationUsesTheHarnessStopVocabulary is the spec scenario "The stop vocabulary is not
// duplicated", made mechanical.
//
// 🔴 It asserts that this package declares NO stop reasons of its own. The way this requirement fails in
// practice is not a wrong value — it is a `type StopReason string` appearing in this package one
// afternoon because importing harnessruntime felt heavy, after which two vocabularies exist and a
// surface showing both has to translate.
func TestConversationUsesTheHarnessStopVocabulary(t *testing.T) {
	//
	// 🔴 The count moved 7 → 8 when P34 appended `spend-ceiling`, and that is the vocabulary's own rule
	// working rather than being loosened: harnessruntime's block says these members are hashed into a
	// configuration's version_id, so ADDING one is safe (nothing previously hashed named it) while
	// renaming or removing one silently re-identifies every configuration that referenced it. Add, never
	// edit. Bumping the number here is the deliberate act; replacing it with `len(reasons)` would make
	// this permanently green and would be the same test in name only.
	reasons := harnessruntime.StopReasons()
	if len(reasons) != 8 {
		t.Fatalf("the stop vocabulary has %d members; P31 extended it to seven and P34 appended "+
			"spend-ceiling for eight: %v", len(reasons), reasons)
	}
	for _, want := range []harnessruntime.StopReason{
		harnessruntime.StopSatisfied, harnessruntime.StopCeiling, harnessruntime.StopSingleShot,
		harnessruntime.StopTokenBudget, harnessruntime.StopToolCallCeiling,
		harnessruntime.StopWallClock, harnessruntime.StopCancelled,
		// P34: the execution envelope's money bound. Distinct from StopTokenBudget, which is a TURN's
		// token allowance — an operator reading "token-budget" would raise a per-turn number that is not
		// what ran out.
		harnessruntime.StopSpendCeiling,
	} {
		if !want.Valid() {
			t.Errorf("%q is not a member of its own vocabulary", want)
		}
	}
	if harnessruntime.StopReason("").Valid() {
		t.Error("an empty stop reason is valid; a terminal message with none would render as finished")
	}
}

// TestOnlyLimitsReportThemselvesAsLimits guards the predicate a surface uses to decide whether a run
// may be rendered as complete (task 4.13, FR18).
func TestOnlyLimitsReportThemselvesAsLimits(t *testing.T) {
	limits := map[harnessruntime.StopReason]bool{
		harnessruntime.StopSatisfied:       false,
		harnessruntime.StopSingleShot:      false,
		harnessruntime.StopCancelled:       false,
		harnessruntime.StopCeiling:         true,
		harnessruntime.StopTokenBudget:     true,
		harnessruntime.StopToolCallCeiling: true,
		harnessruntime.StopWallClock:       true,
		// P34. A limit, so no surface may render a run that ran out of money as COMPLETE.
		harnessruntime.StopSpendCeiling: true,
	}
	for reason, want := range limits {
		if reason.Limit() != want {
			t.Errorf("%s.Limit() = %v, want %v", reason, reason.Limit(), want)
		}
	}
	// The limits, counted. A limit added without a fence entry would slip through the map above, so the
	// count is asserted separately — FR17's envelope of four, plus P34's spend ceiling.
	n := 0
	for _, r := range harnessruntime.StopReasons() {
		if r.Limit() {
			n++
		}
	}
	if n != 5 {
		t.Errorf("%d stop reasons are limits; FR17 declares an envelope of four and P34 adds the "+
			"envelope's spend ceiling, for five", n)
	}
}
