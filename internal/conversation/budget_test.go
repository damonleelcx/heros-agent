package conversation

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// clock is an injected, movable clock. 🔴 Tests must not have a second clock: a wall-clock ceiling
// asserted against `time.Now` would be a test whose answer depends on how fast the machine is.
type clock struct{ at time.Time }

func (c *clock) now() time.Time      { return c.at }
func (c *clock) add(d time.Duration) { c.at = c.at.Add(d) }

func newClock() *clock {
	return &clock{at: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}
}

func fullEnvelope() BudgetEnvelope {
	return BudgetEnvelope{TurnCeiling: 8, TokenBudget: 10000, ToolCallCeiling: 6, WallClockSeconds: 120}
}

func TestABudgetRefusesAnIncompleteEnvelope(t *testing.T) {
	c := newClock()
	if _, err := NewBudget(BudgetEnvelope{TokenBudget: 1}, c.at, c.now); err == nil {
		t.Fatal("an incomplete envelope opened a budget; a defaulted limit is a limit nobody chose, " +
			"which a user then discovers by hitting it")
	}
	if _, err := NewBudget(fullEnvelope(), c.at, nil); err == nil {
		t.Fatal("a budget opened with no clock; it would read time.Now directly")
	}
}

// TestTheCheckHappensBeforeTheSpendNotAfter is the whole argument of task 2.15, asserted rather than
// commented: a refused step must have spent NOTHING.
func TestTheCheckHappensBeforeTheSpendNotAfter(t *testing.T) {
	c := newClock()
	b, err := NewBudget(BudgetEnvelope{TurnCeiling: 8, TokenBudget: 1000, ToolCallCeiling: 6, WallClockSeconds: 120}, c.at, c.now)
	if err != nil {
		t.Fatal(err)
	}
	before := b.Remaining()
	err = b.Admit("s1", StepCost{Tokens: 5000, ToolCalls: 1, Turns: 1})
	if err == nil {
		t.Fatal("a step costing five times the budget was admitted")
	}
	after := b.Remaining()
	if after.Tokens != before.Tokens || after.ToolCalls != before.ToolCalls || after.Turns != before.Turns {
		t.Errorf("the refused step still consumed budget: before %+v, after %+v.\n"+
			"A cap enforced after the spend is an accounting record, not a cap.", before, after)
	}
}

// TestEachLimitIsSeparatelyAttributable is the spec scenario of that name, and task 6.12's subject.
func TestEachLimitIsSeparatelyAttributable(t *testing.T) {
	cases := []struct {
		name     string
		envelope BudgetEnvelope
		advance  time.Duration
		cost     StepCost
		want     harnessruntime.StopReason
	}{
		{"turn ceiling", BudgetEnvelope{TurnCeiling: 1, TokenBudget: 99999, ToolCallCeiling: 99, WallClockSeconds: 999},
			0, StepCost{Turns: 2, Tokens: 1, ToolCalls: 1}, harnessruntime.StopCeiling},
		{"token budget", BudgetEnvelope{TurnCeiling: 9, TokenBudget: 100, ToolCallCeiling: 99, WallClockSeconds: 999},
			0, StepCost{Turns: 1, Tokens: 5000, ToolCalls: 1}, harnessruntime.StopTokenBudget},
		{"tool-call ceiling", BudgetEnvelope{TurnCeiling: 9, TokenBudget: 99999, ToolCallCeiling: 1, WallClockSeconds: 999},
			0, StepCost{Turns: 1, Tokens: 1, ToolCalls: 5}, harnessruntime.StopToolCallCeiling},
		{"wall clock", BudgetEnvelope{TurnCeiling: 9, TokenBudget: 99999, ToolCallCeiling: 99, WallClockSeconds: 30},
			31 * time.Second, StepCost{Turns: 1, Tokens: 1, ToolCalls: 1}, harnessruntime.StopWallClock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClock()
			b, err := NewBudget(tc.envelope, c.at, c.now)
			if err != nil {
				t.Fatal(err)
			}
			c.add(tc.advance)
			err = b.Admit("s1", tc.cost)
			var exhausted ErrBudgetExhausted
			if !errors.As(err, &exhausted) {
				t.Fatalf("Admit = %v; want an ErrBudgetExhausted", err)
			}
			if exhausted.Reason != tc.want {
				t.Errorf("stop reason = %q, want %q.\nEach limit sends an operator somewhere different; "+
					"a run that stopped on wall clock with tokens to spare means something upstream is "+
					"slow, and reporting it as a budget exhaustion sends somebody to raise the wrong number.",
					exhausted.Reason, tc.want)
			}
			if !exhausted.Reason.Limit() {
				t.Errorf("%q does not report itself as a limit; the result would render as complete", exhausted.Reason)
			}
			reason, _, stopped := b.Stopped()
			if !stopped || reason != tc.want {
				t.Errorf("the budget recorded %q, want %q", reason, tc.want)
			}
		})
	}
}

// TestTheFirstLimitWins guards a subtle one: once a run has stopped on its token budget, a later
// wall-clock expiry must not overwrite the reason a person is reading.
func TestTheFirstLimitWins(t *testing.T) {
	c := newClock()
	b, err := NewBudget(BudgetEnvelope{TurnCeiling: 9, TokenBudget: 100, ToolCallCeiling: 9, WallClockSeconds: 30}, c.at, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Admit("s1", StepCost{Tokens: 5000, Turns: 1, ToolCalls: 1}); err == nil {
		t.Fatal("expected the token budget to refuse")
	}
	c.add(time.Hour)
	_ = b.Admit("s2", StepCost{Tokens: 1, Turns: 1, ToolCalls: 1})
	reason, _, _ := b.Stopped()
	if reason != harnessruntime.StopTokenBudget {
		t.Errorf("stop reason = %q; the first limit that fired was the token budget and it must not "+
			"be overwritten by a later expiry", reason)
	}
}

// TestStepReEntryTerminatesNamingTheStep is FR22 (task 2.16, fenced again in 6.16).
func TestStepReEntryTerminatesNamingTheStep(t *testing.T) {
	c := newClock()
	// Generous on every other limit, so the ONLY thing that can stop this is the re-entry ceiling. A
	// test whose token budget also ran out would pass for the wrong reason.
	b, err := NewBudget(BudgetEnvelope{
		TurnCeiling: 1 << 20, TokenBudget: 1 << 30, ToolCallCeiling: 1 << 20, WallClockSeconds: 1 << 20,
	}, c.at, c.now)
	if err != nil {
		t.Fatal(err)
	}
	cost := StepCost{Tokens: 1, ToolCalls: 1, Turns: 1}
	entered := 0
	for {
		if err := b.Admit("loop-step", cost); err != nil {
			var exhausted ErrBudgetExhausted
			if !errors.As(err, &exhausted) {
				t.Fatalf("Admit = %v; want an ErrBudgetExhausted", err)
			}
			if exhausted.Reason != harnessruntime.StopCeiling {
				t.Errorf("stop reason = %q, want %q", exhausted.Reason, harnessruntime.StopCeiling)
			}
			if exhausted.StepID != "loop-step" {
				t.Errorf("the ceiling did not name the step: %q", exhausted.StepID)
			}
			break
		}
		entered++
		if entered > StepReEntryCeiling*4 {
			t.Fatalf("the step was entered %d times with no ceiling; an infinite loop is the DEFAULT "+
				"failure of a loop whose stop condition depends on model output", entered)
		}
	}
	if entered != StepReEntryCeiling {
		t.Errorf("the step ran %d times; the ceiling is %d", entered, StepReEntryCeiling)
	}
	if b.Entries("loop-step") != StepReEntryCeiling {
		t.Errorf("entry count = %d, want %d", b.Entries("loop-step"), StepReEntryCeiling)
	}
}

func TestSettleReturnsWhatAStepDidNotSpend(t *testing.T) {
	c := newClock()
	b, err := NewBudget(fullEnvelope(), c.at, c.now)
	if err != nil {
		t.Fatal(err)
	}
	reserved := StepCost{Tokens: 4000, ToolCalls: 3, Turns: 1}
	if err := b.Admit("s1", reserved); err != nil {
		t.Fatal(err)
	}
	b.Settle(StepCost{Tokens: 100, ToolCalls: 1, Turns: 1}, reserved)
	got := b.Remaining()
	if got.Tokens != 10000-100 {
		t.Errorf("tokens remaining = %d, want %d.\nWithout settling, a plan whose steps each reserve "+
			"generously exhausts the envelope while having spent almost nothing — a budget that stops "+
			"runs for reasons the accounting does not support.", got.Tokens, 10000-100)
	}
	if got.ToolCalls != 6-1 {
		t.Errorf("tool calls remaining = %d, want %d", got.ToolCalls, 5)
	}
}

func TestRemainingWallClockNeverGoesNegative(t *testing.T) {
	c := newClock()
	b, err := NewBudget(fullEnvelope(), c.at, c.now)
	if err != nil {
		t.Fatal(err)
	}
	c.add(10 * time.Minute)
	if got := b.Remaining().WallClockSeconds; got != 0 {
		t.Errorf("wall clock remaining = %d; a rendered '-480s remaining' reads as a bug rather than "+
			"as an expiry", got)
	}
}

func TestCancelAndSatisfyAreRecordedStopReasons(t *testing.T) {
	c := newClock()
	b, _ := NewBudget(fullEnvelope(), c.at, c.now)
	b.Satisfy()
	if reason, _, ok := b.Stopped(); !ok || reason != harnessruntime.StopSatisfied {
		t.Errorf("Satisfy recorded %q; a terminal message must name a reason even when nothing went wrong", reason)
	}
	c2 := newClock()
	b2, _ := NewBudget(fullEnvelope(), c2.at, c2.now)
	b2.Cancel()
	if reason, _, ok := b2.Stopped(); !ok || reason != harnessruntime.StopCancelled {
		t.Errorf("Cancel recorded %q, want %q", reason, harnessruntime.StopCancelled)
	}
	if harnessruntime.StopCancelled.Limit() {
		t.Error("cancellation reports itself as a limit; it is neither a limit nor a success")
	}
}
