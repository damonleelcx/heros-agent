package conversation

import (
	"path/filepath"
	"testing"
)

// router_holdout_test.go is the CI half of §3. The spike half is `cmd/proof/intentrouting`, and both
// call `Evaluate`, so the numbers a person sees while changing the router and the numbers CI enforces
// cannot drift.
//
// # 🔴 What this suite refuses to do
//
// It never computes an overall accuracy. Every assertion below is per-intent or is one of the two
// precision figures, because the failure this whole section is about is a mean of 93% over fourteen
// intents with one of them broken.

func loadHoldoutForTest(t *testing.T) Holdout {
	t.Helper()
	h, err := LoadHoldout(filepath.Join("testdata", "holdout.json"))
	if err != nil {
		t.Fatalf("holdout: %v", err)
	}
	return h
}

// TestEveryIntentHasHeldOutQuestions is the fence that keeps the measurement honest.
//
// 🔴 It is FIRST, because it is what stops the rest of this file passing vacuously. An intent with no
// held-out question has `Recall() == -1` and every threshold below skips it — so a router that lost an
// intent entirely would sail through a suite that only checked the intents somebody remembered to write
// questions for. This is the "green fence over nothing" shape, and this is where it would appear.
func TestEveryIntentHasHeldOutQuestions(t *testing.T) {
	report := Evaluate(NewRouter(), loadHoldoutForTest(t))
	if len(report.Rows) != len(Intents()) {
		t.Fatalf("the report has %d rows; the intent set has %d", len(report.Rows), len(Intents()))
	}
	for _, row := range report.Rows {
		if row.Labelled == 0 {
			t.Errorf("%s has NO held-out question. It is UNMEASURED, which is not the same as healthy: "+
				"every recall threshold below skips it, so it could be routing nowhere at all.", row.Intent)
		}
	}
}

// TestEveryIntentClearsItsOwnRecallFloor is task 3.4 and 3.7: fourteen rows, fourteen assertions.
func TestEveryIntentClearsItsOwnRecallFloor(t *testing.T) {
	report := Evaluate(NewRouter(), loadHoldoutForTest(t))
	failed := false
	for _, row := range report.Rows {
		if r := row.Recall(); r >= 0 && r < MinIntentRecall {
			failed = true
			t.Errorf("%s recall = %.1f%% (%d of %d), below the floor of %.0f%%",
				row.Intent, r*100, row.Correct, row.Labelled, MinIntentRecall*100)
		}
	}
	if failed {
		// The whole table, not just the failing row: a router change that fixes one intent by breaking
		// another is the most common way this file goes red, and the diff is the useful artifact.
		t.Logf("\n%s", report.Table())
	}
}

// TestAMisrouteIsWorseThanAnAbstention encodes D5 as an assertion.
//
// A router that never abstains and is 95% accurate answers a DIFFERENT question one time in twenty, and
// the reader has no way to notice — the answer is well-formed. So a wrong ROUTE is held to a tighter
// bound than a missed one.
func TestAMisrouteIsWorseThanAnAbstention(t *testing.T) {
	report := Evaluate(NewRouter(), loadHoldoutForTest(t))
	misrouted := 0
	for _, row := range report.Rows {
		misrouted += row.Misrouted
	}
	// One misroute in the whole set is the budget. It is not zero because a zero budget is a number
	// somebody satisfies by deleting the question that broke it.
	if misrouted > 1 {
		t.Errorf("%d questions routed to the WRONG intent. Each one is an answer that is well-formed, "+
			"confident, and about something else — the failure mode abstention exists to convert into a "+
			"visible refusal.\n%s", misrouted, report.Table())
	}
}

func TestAbstentionPrecisionClearsItsFloor(t *testing.T) {
	report := Evaluate(NewRouter(), loadHoldoutForTest(t))
	p := report.AbstentionPrecision()
	if p < 0 {
		t.Fatal("the router abstained on nothing at all across the whole holdout, including on \"help\" " +
			"and \"make it good\". A router that never declines is the one D5 rejects.")
	}
	if p < MinAbstentionPrecision {
		t.Errorf("abstention precision = %.1f%% (%d of %d), below %.0f%%.\n"+
			"Every wrong abstention is a question this surface can answer and refused to.\n%s",
			p*100, report.AbstainCorrect, report.AbstainTotal, MinAbstentionPrecision*100, report.Table())
	}
}

// TestOutOfScopeQuestionsAreRefusedByName is FR26. An abstention on "change my plan" is not wrong; it is
// worse than "that is done at /app/billing", and this is what stops a regression from the second to the
// first going unnoticed.
func TestOutOfScopeQuestionsAreRefusedByName(t *testing.T) {
	report := Evaluate(NewRouter(), loadHoldoutForTest(t))
	if report.OutOfScopeLabelled == 0 {
		t.Fatal("the holdout carries no out-of-scope question, so FR26 is unmeasured")
	}
	if report.OutOfScopeNamed != report.OutOfScopeLabelled {
		t.Errorf("%d of %d out-of-scope questions named the surface that performs them.\n"+
			"An agent that offers to change a plan or a password has crossed from answering ABOUT a "+
			"system to ADMINISTERING an account; naming the surface is what keeps the boundary useful "+
			"rather than merely closed.\n%s",
			report.OutOfScopeNamed, report.OutOfScopeLabelled, report.Table())
	}
}

// TestAdjacentIntentsAreRoutedCorrectly is task 3.8, reported separately from overall recall.
//
// 🔴 Separate because it can hide. `context` can sit at 100% overall while the ONE near-miss question in
// its set — the one that is almost `memory` — is wrong, if the other four are easy. Adjacent intents are
// where a confident wrong route is invisible, so they get their own denominator.
func TestAdjacentIntentsAreRoutedCorrectly(t *testing.T) {
	report := Evaluate(NewRouter(), loadHoldoutForTest(t))
	labelled, correct := 0, 0
	for _, row := range report.Rows {
		labelled += row.NearMissLabelled
		correct += row.NearMissCorrect
	}
	if labelled < len(Intents())/2 {
		t.Errorf("only %d near-miss questions across %d intents; task 3.8 asks for one per intent that "+
			"is NEARLY another", labelled, len(Intents()))
	}
	if correct != labelled {
		t.Errorf("%d of %d near-miss questions routed correctly.\n%s", correct, labelled, report.Table())
	}
}

// TestTheRouterIsDeterministic is the property FR11 is built on top of.
//
// A router whose tie-breaking came from map order would give the same sentence two answers on two runs,
// which would make every number above meaningless without anything looking wrong.
func TestTheRouterIsDeterministic(t *testing.T) {
	h := loadHoldoutForTest(t)
	r := NewRouter()
	for _, q := range h.Questions {
		first := r.Route(q.Text)
		for i := 0; i < 20; i++ {
			again := r.Route(q.Text)
			if again.Intent != first.Intent || again.Confidence != first.Confidence {
				t.Fatalf("%q routed to %q (%.4f) and then to %q (%.4f)",
					q.Text, first.Intent, first.Confidence, again.Intent, again.Confidence)
			}
		}
	}
}

// TestTheThresholdActuallyBinds is the mutation this file would otherwise need a drill for.
//
// If `AbstainThreshold` were zero, every question would route and this suite's recall assertions would
// all still pass — the abstention floor is the only thing that would notice, and only because the
// holdout carries abstainable questions. This asserts the threshold is doing work: at least one holdout
// question must score BELOW it, and at least one must score above.
func TestTheThresholdActuallyBinds(t *testing.T) {
	h := loadHoldoutForTest(t)
	r := NewRouter()
	below, above := 0, 0
	for _, q := range h.Questions {
		if r.Route(q.Text).Abstained() {
			below++
		} else {
			above++
		}
	}
	if below == 0 {
		t.Error("no holdout question falls below the abstain threshold; the threshold is not binding, " +
			"and every calibration claim about it is vacuous")
	}
	if above == 0 {
		t.Error("no holdout question clears the abstain threshold; the router routes nothing")
	}
}
