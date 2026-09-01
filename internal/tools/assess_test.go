package tools

import (
	"strings"
	"testing"

	"github.com/heros-foreal/heros/internal/intent"
)

// TestEveryAxisHasABudget. An axis missing from the table falls back to a default, which is correct but
// silent — this makes the omission visible instead.
func TestEveryAxisHasABudget(t *testing.T) {
	for _, axis := range intent.Axes() {
		if _, ok := axisBudgets[axis]; !ok {
			t.Errorf("axis %q has no measured budget; it would silently take the default", axis)
		}
	}
	for axis := range axisBudgets {
		found := false
		for _, a := range intent.Axes() {
			if a == axis {
				found = true
			}
		}
		if !found {
			t.Errorf("budget table names %q, which is not one of the nine axes", axis)
		}
	}
}

// TestARetryChangesSomething.
//
// 🔴 A retry that repeats the identical request is not a retry — it is the same failure purchased twice.
// The ladder already knows which attempt this is; using it to raise the budget is what makes the second
// attempt worth its cost.
func TestARetryChangesSomething(t *testing.T) {
	a := AssessAxis{}
	first := a.budgetFor("context", 1)
	second := a.budgetFor("context", 2)
	third := a.budgetFor("context", 3)
	if second <= first {
		t.Fatalf("attempt 2 budget %d is not above attempt 1's %d; the retry repeats an identical "+
			"request and buys the same failure twice", second, first)
	}
	if third <= second {
		t.Fatalf("attempt 3 budget %d is not above attempt 2's %d", third, second)
	}
}

// TestEscalationIsBounded. An axis truncating for some other reason must not walk its budget up
// indefinitely across a long ladder.
func TestEscalationIsBounded(t *testing.T) {
	a := AssessAxis{}
	capped := a.budgetFor("context", 3)
	for _, attempt := range []int{4, 9, 100} {
		if got := a.budgetFor("context", attempt); got != capped {
			t.Errorf("attempt %d budget %d exceeds the cap %d", attempt, got, capped)
		}
	}
}

// TestAnExplicitBudgetWins, so a test can force truncation deliberately.
func TestAnExplicitBudgetWins(t *testing.T) {
	a := AssessAxis{MaxTokens: 32}
	if got := a.budgetFor("context", 1); got != 32 {
		t.Errorf("explicit budget ignored: got %d", got)
	}
	if got := a.budgetFor("context", 2); got != 64 {
		t.Errorf("an explicit budget must still escalate: got %d", got)
	}
}

// TestZeroAttemptDoesNotUnderflow. Claim increments the attempt before a tool ever runs, so 0 should not
// occur — but a shift by a negative count panics, and a panic in a worker takes its lease with it.
func TestZeroAttemptDoesNotUnderflow(t *testing.T) {
	a := AssessAxis{}
	for _, attempt := range []int{0, -1, -100} {
		if got := a.budgetFor("model", attempt); got != axisBudgets["model"] {
			t.Errorf("attempt %d gave budget %d", attempt, got)
		}
	}
}

// TestTheAssessmentPromptAsksForJSONByName. The provider rejects json_object unless the word appears in
// the prompt, and the failure is a 400 on a task that has already been claimed and counted.
func TestTheAssessmentPromptAsksForJSONByName(t *testing.T) {
	if !strings.Contains(strings.ToLower(assessSystem), "json") {
		t.Error("the system prompt does not mention JSON, which the provider requires for json_object")
	}
}
