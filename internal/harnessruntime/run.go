// Package harnessruntime is the first of the two artifacts P18 §5's refusal named as missing: a harness
// RUNTIME — a bounded turn loop, a stop condition, and a continuation rule — plus the one definition of
// each builtin strategy's loop behaviour that the generated call-site module calls rather than re-derives.
//
// # What "runtime" means here, and what it deliberately excludes
//
// This runtime backs a module that ships INTO THE CUSTOMER'S REPOSITORY and runs in their process,
// re-invoking a call their author already wrote. That single fact decides most of what follows:
//
//   - 🚫 It calls no provider, dispatches no tool, opens no connection, and reads no credential. A
//     generated file that reached a provider would put a credential in the customer's process and spend
//     it on turns the author never wrote (decisions.md D-10).
//   - 🚫 It reads no clock and no random source. A loop whose behaviour depended on when it ran would be
//     unscorable: the axis exists to be compared, and two runs of one configuration must agree.
//   - 🔴 It is BOUNDED BY CONSTRUCTION. No strategy and no combination of params expresses an unbounded
//     loop; a run that reaches the ceiling terminates, returns the last answer, and RECORDS that it
//     stopped there (NFR5, and the standing L1 blast-radius note in decisions.md).
//   - A planner, a tool executor and a critic are INJECTED host services. A strategy needing one that was
//     not supplied REFUSES by name rather than substituting a cheaper loop — because a `critic-loop` that
//     quietly runs without a critic IS `reflexion`, executing under a config_hash that says otherwise.
//
// # Why the loop lives here rather than on registry.HarnessStrategy
//
// The sealed vocabulary (internal/registry) says what a strategy IS; this package says what it DOES.
// Putting the loop on the sealed type would drag a host-service dependency into every consumer of a
// vocabulary — the correction the memory axis already had to make once — so the dispatch is keyed by
// strategy NAME, and a conformance test asserts the sealed vocabulary and this package name exactly the
// same set. That binds them without the import.
package harnessruntime

import (
	"errors"
	"fmt"
)

// Sentinel errors. Typed because three genuinely different things go wrong here and they are answered by
// different people: a strategy this build does not implement (us), a host service the caller did not
// supply (the caller), and a turn function that failed (the customer's own call).
var (
	// ErrUnknownStrategy means the config names a strategy this build has no loop for. 🔴 FAIL LOUD, never
	// fall back to one turn: running a single shot under a loop's config_hash reports one scaffold as
	// another, which is the failure the whole axis exists to prevent.
	ErrUnknownStrategy = errors.New("harnessruntime: no loop is defined for this strategy")
	// ErrMissingHostService means a strategy needs an injected planner, tool executor, or critic that the
	// caller did not supply. 🚫 Never degraded to a strategy that needs none.
	ErrMissingHostService = errors.New("harnessruntime: the strategy needs a host service that was not supplied")
	// ErrInvalidParams means the params could not produce a bounded loop — a ceiling below one, or above
	// the vocabulary's own ceiling.
	ErrInvalidParams = errors.New("harnessruntime: params do not describe a bounded loop")
	// ErrTurnFailed wraps a failure from the caller's turn function.
	ErrTurnFailed = errors.New("harnessruntime: the turn function failed")
)

// StopReason is the closed set of reasons a loop ended. Closed so a consumer's switch is exhaustive and
// so a surface can distinguish "it finished" from "it ran out of budget" without parsing prose.
type StopReason string

const (
	// StopSatisfied — the strategy's stop condition was met. The loop did what it was for.
	StopSatisfied StopReason = "satisfied"
	// StopCeiling — the run reached max_turns. 🔴 Recorded distinctly from StopSatisfied, because the two
	// mean opposite things about whether the scaffold helped, and a surface that showed them alike would
	// present a budget exhaustion as a success.
	StopCeiling StopReason = "ceiling"
	// StopSingleShot — the identity strategy, which runs exactly one turn by definition. Its own reason
	// rather than StopCeiling: reaching a ceiling of one is not running out of budget.
	StopSingleShot StopReason = "single-shot"

	// ── P31 · the conversational turn's budget envelope (design.md D8, PRD FR17/FR18) ─────────────
	//
	// 🔴 EXTENDED HERE RATHER THAN FORKED, and that is the whole of task 1.7. A conversation that ran
	// out of tokens and a node loop that ran out of turns are the SAME CONCEPT — "this stopped because
	// it hit a limit, not because it was done" — and giving them two vocabularies means a surface
	// showing both has to translate between them, which is where one of the two eventually gets
	// rendered as a success.
	//
	// 🔴 THE EXTENSION IS EXPAND-ONLY. This is a closed set whose members are hashed into a
	// configuration's `version_id`, so a RENAMED or REMOVED member silently re-identifies every
	// configuration that referenced it: the same scaffold acquires a new address, historical runs stop
	// joining to it, and nothing errors. P34 carries the identical hazard for the loop/graph split and
	// records the same rule. Adding a member is safe (nothing previously hashed named it); changing one
	// is not. Add, never edit.
	//
	// PRD FR17's envelope is four limits, and each is separately attributable (spec: "Each limit is
	// separately attributable"). The TURN CEILING is `StopCeiling` above — it already means exactly
	// that — so only three new members are needed, plus cancellation.

	// StopTokenBudget — the turn exhausted the token budget its `plan` declared. 🔴 Distinct from
	// StopCeiling: an operator reading "ceiling" would go and look at max_turns, which is not what ran
	// out, and would find it untouched.
	StopTokenBudget StopReason = "token-budget"
	// StopToolCallCeiling — the turn made as many tool calls as its `plan` declared it might.
	StopToolCallCeiling StopReason = "tool-call-ceiling"
	// StopWallClock — the turn reached its declared wall-clock ceiling. Its own reason because it is
	// the only limit that can fire while nothing is being spent: a turn blocked on a slow upstream
	// stops here with tokens and tool calls to spare, and reporting that as a budget exhaustion would
	// send somebody to raise the wrong number.
	StopWallClock StopReason = "wall-clock"
	// StopCancelled — a person explicitly cancelled the run (PRD FR7). Not a limit and not a success;
	// its own member because a terminal message must always name a stop reason (task 4.13) and neither
	// `satisfied` nor any ceiling is honest about a run somebody stopped on purpose.
	StopCancelled StopReason = "cancelled"
)

// stopReasons is the closure. 🔴 Append-only, for the version_id reason recorded on the P31 block above.
var stopReasons = []StopReason{
	StopSatisfied, StopCeiling, StopSingleShot,
	StopTokenBudget, StopToolCallCeiling, StopWallClock, StopCancelled,
}

// StopReasons returns the closed vocabulary. A copy, so no caller can widen it.
//
// It exists so a consumer in another package — P31's conversational turn is the first — can assert its
// own terminal messages against THIS set rather than declaring a parallel one. The spec scenario "The
// stop vocabulary is not duplicated" is checked against exactly this function.
func StopReasons() []StopReason { return append([]StopReason(nil), stopReasons...) }

// Valid reports membership. An empty reason is invalid rather than defaulted: a terminal message whose
// stop reason nobody recorded must not be renderable as "finished".
func (r StopReason) Valid() bool {
	for _, v := range stopReasons {
		if v == r {
			return true
		}
	}
	return false
}

// String makes StopReason printable in an error without a conversion at every call site.
func (r StopReason) String() string { return string(r) }

// Limit reports whether this reason means the run hit a bound rather than finished or was stopped by a
// person. 🔴 The predicate a surface uses to decide whether a run may be rendered as COMPLETE — and the
// reason `satisfied` is the only member that returns false while still being terminal.
func (r StopReason) Limit() bool {
	switch r {
	case StopCeiling, StopTokenBudget, StopToolCallCeiling, StopWallClock:
		return true
	default:
		return false
	}
}

// Message is one turn's message, in the only shape this runtime needs: who said it and what they said.
// Deliberately minimal — the runtime never interprets a message beyond reading an answer's text against a
// stop condition, and a richer shape would be this package inventing a wire format nobody asked for.
type Message struct {
	Role    string
	Content string
}

// TurnRecord is what one turn did. The observable half of the standing L1 note: the enlarged turn surface
// is not asserted away, it is written down.
type TurnRecord struct {
	// Turn is 1-based.
	Turn int
	// Answer is what the turn produced.
	Answer string
	// Continued reports whether the loop took another turn after this one.
	Continued bool
	// Reason is why the loop stopped, on the last turn; empty on a turn that continued.
	Reason StopReason
}

// Result is a completed run.
//
// 🚫 None of it participates in config_hash (decisions.md D-13). Turns, stop reason and trace are
// properties of a RUN, and hashing one would give a single configuration as many hashes as it had
// outcomes.
type Result struct {
	// Answer is the final turn's answer.
	Answer string
	// Turns is how many turns actually executed, always in [1, MaxTurns].
	Turns int
	// Stop is why the loop ended.
	Stop StopReason
	// Trace is one record per executed turn, in order.
	Trace []TurnRecord
}

// Config is a resolved harness: which strategy, and the params it runs with. It mirrors the sealed
// registry entry's projection — strategy name plus params — and nothing else.
type Config struct {
	Strategy string
	Params   Params
}

// Invoke performs ONE turn: it is handed the message list for this turn and returns the answer.
//
// 🔴 This is the ONLY thing the runtime calls. It is the customer's own call, re-invoked, which is what
// makes "the added turns reach nothing new" true by construction rather than by policy: whatever that
// call could reach before, it can reach now, and the runtime adds no second path to anything.
type Invoke func(messages []Message) (string, error)

// Run executes the strategy's loop, bounded by construction.
//
// The order of the checks is the order of the failures a caller most needs distinguished: params that
// cannot describe a bounded loop, a strategy with no definition, and a host service that was not supplied
// — all three BEFORE the first turn, so a run that cannot complete honestly never spends a call.
func Run(cfg Config, hosts Hosts, messages []Message, invoke Invoke) (Result, error) {
	if invoke == nil {
		return Result{}, fmt.Errorf("harnessruntime: no turn function supplied")
	}
	st, ok := strategyFor(cfg.Strategy)
	if !ok {
		return Result{}, fmt.Errorf("%w: %q (defined here: %v)", ErrUnknownStrategy, cfg.Strategy, StrategyNames())
	}
	ceiling, err := st.ceiling(cfg.Params)
	if err != nil {
		return Result{}, err
	}
	if err := hosts.require(st.hostService()); err != nil {
		return Result{}, err
	}

	// 🔴 The bound. `turn <= ceiling` is a `for` bound rather than a break inside the body, so there is no
	// path through this function that runs more turns than the ceiling — including one where a strategy's
	// Plan wrongly says "continue" forever. A bound a strategy could talk its way past would not be a bound.
	out := Result{Trace: make([]TurnRecord, 0, ceiling)}
	turnMsgs := append([]Message(nil), messages...)
	for turn := 1; turn <= ceiling; turn++ {
		answer, err := invoke(turnMsgs)
		if err != nil {
			return out, fmt.Errorf("%w: turn %d: %v", ErrTurnFailed, turn, err)
		}
		out.Answer = answer
		out.Turns = turn

		rec := TurnRecord{Turn: turn, Answer: answer}
		if turn == ceiling {
			// The ceiling, recorded as itself. For the identity that is not an exhausted budget, so it gets
			// its own reason.
			rec.Reason = StopCeiling
			if ceiling == 1 {
				rec.Reason = StopSingleShot
			}
			out.Stop = rec.Reason
			out.Trace = append(out.Trace, rec)
			break
		}

		d := st.plan(cfg.Params, turn, answer)
		if !d.Continue {
			rec.Reason = StopSatisfied
			out.Stop = StopSatisfied
			out.Trace = append(out.Trace, rec)
			break
		}
		rec.Continued = true
		out.Trace = append(out.Trace, rec)
		turnMsgs = append(turnMsgs, d.Append...)
	}
	return out, nil
}
