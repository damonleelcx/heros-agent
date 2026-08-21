package assessment

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// holdout_test.go exercises §3.4 and §3.5.
//
// # 🔴 WHAT THESE TESTS MEASURE, AND WHAT THEY DO NOT
//
// They measure the HARNESS: that an abstention on an undeterminable case is scored as a SUCCESS, that
// an assertion on an undeterminable case is scored as WRONG, that precision is reported per axis and
// never as a mean, and that an axis with no cases is reported as untested rather than omitted.
//
// 🚫 THEY MEASURE NOTHING ABOUT ANY MODEL. The analyst below is scripted. Its answers come from a table
// in this file — deliberately not from `holdout.json`, which would make the test circular — and running
// this suite green says exactly nothing about how a provider would score.
//
// **No real-provider holdout run has been performed.** It requires a published model catalog and a
// provider credential, neither of which exists in a checkout. `make assessment-holdout` is the entry
// point for one, and until somebody runs it the per-axis precision and abstention rate of the actual
// inference are UNMEASURED — which is a real gap and is recorded as one in the phase's tasks rather
// than papered over by a green suite.

// scriptedAnalyst answers from a table keyed by (fixture-shaped subject, axis).
type scriptedAnalyst struct {
	// answers is keyed `workflowID/axis`.
	answers map[string]Answer
	// fallback is what an unlisted key gets. An ABSTENTION, so a key somebody forgot to script shows
	// up as a miss rather than as a silently correct answer.
	fallback Answer
	calls    int
}

func (s *scriptedAnalyst) Assess(_ context.Context, q Question) (Answer, error) {
	s.calls++
	if a, ok := s.answers[q.WorkflowID+"/"+string(q.Axis)]; ok {
		return a, nil
	}
	return s.fallback, nil
}

func holdoutInference(t *testing.T, a *scriptedAnalyst) Inference {
	t.Helper()
	inf, err := NewHerosInference(a, DefaultConfidenceFloor)
	if err != nil {
		t.Fatalf("NewHerosInference: %v", err)
	}
	return inf
}

func loadFixture(t *testing.T) SubjectLoader {
	t.Helper()
	return func(fixture string) (Subject, error) {
		return subjectFor(t, fixture), nil
	}
}

func holdoutCases(t *testing.T) []Case {
	t.Helper()
	cases, err := LoadCases(filepath.Join("testdata", "holdout.json"))
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	return cases
}

// ── The set itself ───────────────────────────────────────────────────────────────────────────────

// TestTheHoldoutContainsUndeterminableCases is task 3.4's explicit requirement, and it is the one that
// makes the rest of the file mean anything. Without these, a model that always answers is
// indistinguishable from one that knows when to stop.
func TestTheHoldoutContainsUndeterminableCases(t *testing.T) {
	cases := holdoutCases(t)
	n := 0
	for _, c := range cases {
		if c.Truth == TruthUndeterminable {
			n++
		}
	}
	if n == 0 {
		t.Fatal("the holdout contains no case whose correct answer is \"cannot be determined\", so it " +
			"cannot tell a model that abstains from one that guesses — which is the only thing FR10 " +
			"depends on")
	}
	if n == len(cases) {
		t.Fatal("EVERY case is undeterminable, so a model that abstains unconditionally scores perfectly. " +
			"The set needs cases with answers too, or the abstention discipline is unfalsifiable in the " +
			"other direction")
	}
}

// TestEveryHoldoutCaseIsAuditable keeps the ground truth reviewable. A holdout nobody can audit drifts
// to whatever the model happens to do.
func TestEveryHoldoutCaseIsAuditable(t *testing.T) {
	for _, c := range holdoutCases(t) {
		if !c.Axis.Valid() {
			t.Fatalf("%s/%s names an axis that is not one of the nine", c.Fixture, c.Axis)
		}
		if len(strings.TrimSpace(c.Why)) < 40 {
			t.Fatalf("%s/%s records no reason for its ground truth. A label with no argument behind it "+
				"is a label somebody will 'fix' the day the model disagrees with it", c.Fixture, c.Axis)
		}
		if c.Truth == TruthConclusive && c.Expect == "" {
			t.Fatalf("%s/%s is conclusive and states nothing a correct answer must contain", c.Fixture, c.Axis)
		}
		if c.Truth == TruthUndeterminable && c.Expect != "" {
			t.Fatalf("%s/%s is undeterminable and carries an expectation — it is graded on whether the "+
				"model ABSTAINED, and an expectation here would suggest otherwise", c.Fixture, c.Axis)
		}
	}
}

// TestEveryExpectationIsDiscriminating is the guard on the set's weakest link.
//
// A `conclusive` case is graded by substring, which grades phrasing as well as correctness. The two
// things that would make it a rubber stamp are a term too short to be meaningful, and a term the model
// was ALREADY TOLD — so both are refused here.
func TestEveryExpectationIsDiscriminating(t *testing.T) {
	for _, c := range holdoutCases(t) {
		if c.Truth != TruthConclusive {
			continue
		}
		if len([]rune(c.Expect)) < 4 {
			t.Fatalf("%s/%s expects %q, which is too short to discriminate: a one- or two-word "+
				"expectation is a coin flip dressed as a grade", c.Fixture, c.Axis, c.Expect)
		}
		// The structural claim is part of what the model is reasoning from. An expectation that appears
		// in it can be satisfied by echoing, which measures nothing.
		s := subjectFor(t, c.Fixture)
		structural := findingFor(t, s, c.Axis)
		if containsFold(structural.Claim(), c.Expect) {
			t.Fatalf("%s/%s expects %q, which already appears in the structural claim (%q) — a model "+
				"could satisfy it by repeating what it was given", c.Fixture, c.Axis, c.Expect, structural.Claim())
		}
	}
}

// ── The scorer ───────────────────────────────────────────────────────────────────────────────────

// TestAbstentionOnAnUndeterminableCaseIsASuccess is the whole of §3.5.
//
// 🔴 If this ever inverts, FR10 dies quietly: the evaluation would punish the discipline, somebody
// would tune the floor down to raise the number, and the platform would start asserting things about
// repositories that cannot support the assertion.
func TestAbstentionOnAnUndeterminableCaseIsASuccess(t *testing.T) {
	// A model that abstains on everything.
	analyst := &scriptedAnalyst{
		answers:  map[string]Answer{},
		fallback: Answer{Abstained: true, AbstentionReason: "the source does not establish this", ProviderModelVersion: "test/scripted-1"},
	}
	rep, err := Run(context.Background(), holdoutInference(t, analyst), holdoutCases(t), loadFixture(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, s := range rep.PerAxis {
		if s.Cases == 0 {
			continue
		}
		if s.Wrong != 0 {
			t.Fatalf("%s: an all-abstaining model was scored %d wrong. Abstention is never a wrong "+
				"answer — it is either a success or a miss", s.Axis, s.Wrong)
		}
		if s.AbstainedCorrectly == 0 {
			t.Fatalf("%s: no abstention was scored as correct, so the discipline earns nothing", s.Axis)
		}
		if got := s.Precision(); got != -1 {
			t.Fatalf("%s: precision is %v for a model that answered nothing, want -1. A precision of 0.0 "+
				"says everything it said was wrong; saying nothing has no precision at all", s.Axis, got)
		}
	}
}

// TestAssertingOnAnUndeterminableCaseIsWrong is the other half, and it is the outcome that matters
// most: a confident sentence about a repository that cannot support it is exactly what a reader
// cannot detect.
func TestAssertingOnAnUndeterminableCaseIsWrong(t *testing.T) {
	analyst := &scriptedAnalyst{
		answers: map[string]Answer{},
		fallback: Answer{
			Claim: "this repository keeps a per-session store that is never pruned",
			// Above the floor, so the floor does not save it. This is a model that is CONFIDENT and
			// wrong, which is the case the holdout exists for.
			Confidence:           0.95,
			ProviderModelVersion: "test/scripted-1",
		},
	}
	rep, err := Run(context.Background(), holdoutInference(t, analyst), holdoutCases(t), loadFixture(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wrong := 0
	for _, s := range rep.PerAxis {
		wrong += s.Wrong
	}
	if wrong == 0 {
		t.Fatal("a model that asserted a claim on every undeterminable case scored nothing wrong — " +
			"the scorer cannot detect the failure mode this phase is built around")
	}
}

// TestTheFloorTurnsALowConfidenceConclusionIntoAnAbstention is FR10 at the boundary. A conclusion below
// the floor must not reach a reader as a sentence.
func TestTheFloorTurnsALowConfidenceConclusionIntoAnAbstention(t *testing.T) {
	analyst := &scriptedAnalyst{
		answers: map[string]Answer{},
		fallback: Answer{
			Claim:                "this repository keeps a per-session store",
			Confidence:           DefaultConfidenceFloor - 0.01,
			ProviderModelVersion: "test/scripted-1",
		},
	}
	inf := holdoutInference(t, analyst)
	f, _, err := inf.Infer(context.Background(), AxisMemory, subjectFor(t, "java"))
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if f.State() != StateNotMeasured {
		t.Fatalf("a %.2f-confidence conclusion arrived as %s. Below the floor it must become an "+
			"abstention, or the number that would have warned the reader is rendered nowhere",
			DefaultConfidenceFloor-0.01, f.State())
	}
	if f.MissingInput() != MissingInferenceAbstained {
		t.Fatalf("the abstention names %q, want %q", f.MissingInput(), MissingInferenceAbstained)
	}
	if !strings.Contains(f.Claim(), "floor") {
		t.Fatalf("the abstention does not tell the reader why: %q", f.Claim())
	}
}

// TestACorrectConclusiveAnswerScoresAsPrecision closes the loop: the scorer must be able to award a
// point, or the whole report is a list of zeros that proves nothing.
func TestACorrectConclusiveAnswerScoresAsPrecision(t *testing.T) {
	cases := holdoutCases(t)
	analyst := &scriptedAnalyst{
		answers:  map[string]Answer{},
		fallback: Answer{Abstained: true, AbstentionReason: "unscripted", ProviderModelVersion: "test/scripted-1"},
	}
	for _, c := range cases {
		if c.Truth != TruthConclusive {
			continue
		}
		analyst.answers["wf-"+c.Fixture+"/"+string(c.Axis)] = Answer{
			Claim:                "the scaffold here is the " + c.Expect + " orchestration",
			Confidence:           0.9,
			ProviderModelVersion: "test/scripted-1",
		}
	}
	rep, err := Run(context.Background(), holdoutInference(t, analyst), cases, loadFixture(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	correct := 0
	for _, s := range rep.PerAxis {
		correct += s.Correct
		if s.Answered > 0 && s.Precision() < 0 {
			t.Fatalf("%s answered %d and reports no precision", s.Axis, s.Answered)
		}
	}
	if correct == 0 {
		t.Fatal("no conclusive answer was scored correct, so the scorer can only ever report failure")
	}
}

// TestTheReportNamesUntestedAxes is the anti-aggregate rule applied to the holdout itself: "we never
// tested this axis" is the finding a reader most needs and it is invisible in a table that only shows
// what was tested.
func TestTheReportNamesUntestedAxes(t *testing.T) {
	analyst := &scriptedAnalyst{answers: map[string]Answer{},
		fallback: Answer{Abstained: true, AbstentionReason: "x", ProviderModelVersion: "test/scripted-1"}}
	rep, err := Run(context.Background(), holdoutInference(t, analyst), holdoutCases(t), loadFixture(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.PerAxis) != len(Axes()) {
		t.Fatalf("the report covers %d axes, want all %d — an axis omitted from the table is an axis "+
			"nobody notices is untested", len(rep.PerAxis), len(Axes()))
	}
	if len(rep.UntestedAxes) == 0 {
		t.Fatal("no axis is reported untested, but only memory and harness have cases today")
	}
}

// TestTheHoldoutReportHasNoOverallNumber is §9.5's fence, and it is the same refusal as R4 one level
// down: an aggregate over axes hides the one axis that is broken.
func TestTheHoldoutReportHasNoOverallNumber(t *testing.T) {
	banned := []string{"overall", "total", "accuracy", "mean", "average", "grade", "score"}
	for _, name := range structFieldNames(Report{}) {
		lower := strings.ToLower(name)
		for _, w := range banned {
			if strings.Contains(lower, w) {
				t.Fatalf("Report.%s is an aggregate over axes. §9.5: it hides the one axis that is broken, "+
					"and it is the number people stop reading at", name)
			}
		}
	}
}

// structFieldNames returns a struct's field names by reflection, so the fence discovers the type
// rather than reading a list somebody maintains.
func structFieldNames(v any) []string {
	rt := reflect.TypeOf(v)
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		out = append(out, rt.Field(i).Name)
	}
	return out
}
