package conversation

import (
	"fmt"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// budget.go is FR17's envelope turned into an accountant that can say NO (tasks 2.15, 2.16).
//
// # Why the check is BEFORE the step and never after
//
// `internal/herosagent/caps.go` already makes this argument for provider spend and it is the same one:
// *a cap enforced after a call is an accounting record, not a cap.* The tokens are spent, the bill is
// incurred, and what the check produces is a slightly faster stop next time — which is the behaviour of
// having no cap at all on the run that mattered.
//
// So `Budget.Admit` is called BEFORE a step runs, is handed that step's declared cost, and refuses when
// the cost would not fit. A step that is refused has spent nothing.
//
// # Why the ledger is on the RUN and not on the conversation
//
// FR21 and ADR-015. Resume reads the remaining budget from here; a client that reconnects with a
// tampered "I had 90,000 tokens left" changes nothing, because nothing reads it. That is the whole of
// task 6.18, and it is a property of where the number lives rather than of a validation.
//
// # The clock is injected
//
// A wall-clock ceiling needs a clock, and a package that read `time.Now` directly would have a second
// clock beside the test's — the failure `Tests must not have a second clock` records. `Budget.now` is a
// field; production passes `time.Now`.

// StepReEntryCeiling is how many times one plan step may be entered (FR22, PRD §14 Q7).
//
// 🔴 It is `harnessruntime.TurnCeiling`, reused rather than re-chosen, and it is a CONSTANT — not
// configuration, not an operator setting, not a field on the envelope. The reason is the failure mode:
// an infinite loop in an agent is the DEFAULT failure of a loop whose stop condition depends on model
// output, and a ceiling an operator can raise is a ceiling that gets raised at 2am by whoever is being
// paged, after which the loop is infinite again with a paper trail.
//
// Per STEP rather than per run, because a plan of eight steps that each legitimately retry twice is not
// a loop, and a per-run counter could not tell it from one that is.
const StepReEntryCeiling = harnessruntime.TurnCeiling

// StepCost is what one step declares it may spend, checked before it runs.
//
// A step declares a cost rather than reporting one afterwards for the reason at the top of this file.
// The declared cost is an UPPER BOUND: `Settle` reconciles the actual spend afterwards and returns the
// difference to the run, so a cheap step does not permanently consume the ceiling it reserved.
type StepCost struct {
	Tokens    int
	ToolCalls int
	Turns     int
}

// ErrBudgetExhausted is why a step was refused admission. It names the LIMIT, because each of the four
// sends an operator somewhere different.
type ErrBudgetExhausted struct {
	Reason harnessruntime.StopReason
	StepID string
}

func (e ErrBudgetExhausted) Error() string {
	if e.StepID == "" {
		return fmt.Sprintf("conversation: the turn stopped on its %s", e.Reason)
	}
	return fmt.Sprintf("conversation: the turn stopped on its %s at step %q", e.Reason, e.StepID)
}

// Budget is one turn's live accounting. Safe for concurrent use: the turn runner and the resume path
// read it from different goroutines.
type Budget struct {
	mu sync.Mutex

	envelope BudgetEnvelope
	// remaining is decremented on admission and credited back on settle.
	remaining BudgetRemaining
	// entries counts how many times each step has been entered (FR22).
	entries map[string]int
	// startedAt and now give the wall-clock ceiling something to measure without a second clock.
	startedAt time.Time
	now       func() time.Time
	// stopped records the first limit that fired. 🔴 FIRST, not last: once a run has stopped on its
	// token budget, a subsequent wall-clock expiry must not overwrite the reason a person is reading.
	stopped harnessruntime.StopReason
	// stoppedAt names the step a re-entry ceiling fired on.
	stoppedAt string
}

// NewBudget opens the accounting for a turn. The envelope must be Complete — a caller that has not
// resolved all four limits has not resolved a budget, and this refuses rather than defaulting, because
// a defaulted limit is a limit nobody chose that will be discovered by being hit.
func NewBudget(env BudgetEnvelope, startedAt time.Time, now func() time.Time) (*Budget, error) {
	if !env.Complete() {
		return nil, fmt.Errorf("conversation: a budget envelope is missing %v", env.MissingLimits())
	}
	if now == nil {
		return nil, fmt.Errorf("conversation: a budget needs a clock; nothing may read time.Now here directly")
	}
	return &Budget{
		envelope: env,
		remaining: BudgetRemaining{
			Turns: env.TurnCeiling, Tokens: env.TokenBudget,
			ToolCalls: env.ToolCallCeiling, WallClockSeconds: env.WallClockSeconds,
		},
		entries:   map[string]int{},
		startedAt: startedAt,
		now:       now,
	}, nil
}

// Envelope returns what was declared. A value copy.
func (b *Budget) Envelope() BudgetEnvelope { return b.envelope }

// Remaining returns what is left, with the wall-clock component computed against the injected clock.
//
// It never returns a negative: a turn three seconds past its ceiling has zero seconds left, not minus
// three, because the number is rendered and "-3s remaining" reads as a bug rather than as an expiry.
func (b *Budget) Remaining() BudgetRemaining {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remainingLocked()
}

func (b *Budget) remainingLocked() BudgetRemaining {
	out := b.remaining
	out.WallClockSeconds = b.envelope.WallClockSeconds - int(b.now().Sub(b.startedAt)/time.Second)
	if out.WallClockSeconds < 0 {
		out.WallClockSeconds = 0
	}
	return out
}

// Admit is the check that makes this a limit rather than a record. It is called BEFORE a step runs.
//
// The order of the checks is the order a reader needs them distinguished, and each returns its OWN stop
// reason so the terminal message can name the specific limit (spec: "Each limit is separately
// attributable"):
//
//  1. wall clock — the only limit that can fire while nothing is being spent
//  2. step re-entry — the loop guard, which names the step
//  3. turns, tokens, tool calls — the three the plan declared as counts
//
// On refusal the step has spent NOTHING, and the budget records the first reason that fired.
func (b *Budget) Admit(stepID string, cost StepCost) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped != "" {
		return ErrBudgetExhausted{Reason: b.stopped, StepID: b.stoppedAt}
	}

	if b.now().Sub(b.startedAt) >= time.Duration(b.envelope.WallClockSeconds)*time.Second {
		return b.stopLocked(harnessruntime.StopWallClock, "")
	}
	if stepID != "" {
		if b.entries[stepID] >= StepReEntryCeiling {
			// 🔴 `StopCeiling` rather than a new reason. FR22 says the run terminates "with `ceiling`
			// and names the step" — the ceiling concept already exists and the step is the attribute
			// that distinguishes this from a turn ceiling.
			return b.stopLocked(harnessruntime.StopCeiling, stepID)
		}
	}
	if cost.Turns > b.remaining.Turns {
		return b.stopLocked(harnessruntime.StopCeiling, stepID)
	}
	if cost.Tokens > b.remaining.Tokens {
		return b.stopLocked(harnessruntime.StopTokenBudget, stepID)
	}
	if cost.ToolCalls > b.remaining.ToolCalls {
		return b.stopLocked(harnessruntime.StopToolCallCeiling, stepID)
	}

	b.remaining.Turns -= cost.Turns
	b.remaining.Tokens -= cost.Tokens
	b.remaining.ToolCalls -= cost.ToolCalls
	if stepID != "" {
		b.entries[stepID]++
	}
	return nil
}

// Settle reconciles a step's ACTUAL spend against what it reserved, crediting the difference back.
//
// Without it, an eight-step plan whose steps each reserve a generous ceiling exhausts the envelope on
// step three while having spent almost nothing — a budget that stops runs for reasons the accounting
// does not support, which is worse than no budget because the stop reason is a lie.
//
// An actual spend LARGER than the reservation is charged in full and can drive the remainder negative
// internally; the next `Admit` then refuses. 🚫 It is not clamped to zero, because clamping would let a
// step that overran twice look like two steps that fitted.
func (b *Budget) Settle(actual StepCost, reserved StepCost) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.remaining.Turns += reserved.Turns - actual.Turns
	b.remaining.Tokens += reserved.Tokens - actual.Tokens
	b.remaining.ToolCalls += reserved.ToolCalls - actual.ToolCalls
}

// Cancel records a person's explicit cancellation as the stop reason (FR7).
func (b *Budget) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.stopLocked(harnessruntime.StopCancelled, "")
}

// Satisfy records that the turn finished because it was done. 🔴 Recorded rather than left empty: a
// terminal message must name a stop reason even when nothing went wrong (task 4.13).
func (b *Budget) Satisfy() {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.stopLocked(harnessruntime.StopSatisfied, "")
}

// Stopped reports the reason the turn stopped and the step it stopped at, and whether it stopped at all.
func (b *Budget) Stopped() (harnessruntime.StopReason, string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopped, b.stoppedAt, b.stopped != ""
}

// Entries reports how many times a step has been entered. For the terminal message and for the fence.
func (b *Budget) Entries(stepID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.entries[stepID]
}

// stopLocked records the FIRST stop reason and returns the error for it.
func (b *Budget) stopLocked(r harnessruntime.StopReason, stepID string) error {
	if b.stopped == "" {
		b.stopped, b.stoppedAt = r, stepID
	}
	if b.stopped == harnessruntime.StopSatisfied || b.stopped == harnessruntime.StopCancelled {
		return nil
	}
	return ErrBudgetExhausted{Reason: b.stopped, StepID: b.stoppedAt}
}
