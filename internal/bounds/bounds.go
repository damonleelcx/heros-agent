// Package bounds is the answer to "prevent infinite execution", and to the question that comes just
// before it: what happens when a person asks for something unbounded.
//
// # Why a question that cannot be bounded is REFUSED rather than run with defaults
//
// Defaults are how an agent spends someone's money on a search they did not ask for. The failure is
// silent — the run looks exactly like a run they wanted — and it is discovered on an invoice. An
// unbounded search is not a larger version of a bounded one; it is a different product with a different
// risk, and the person who typed a sentence did not choose it.
//
// The pressure against this rule is predictable and worth naming in advance: somebody will observe that
// refusing is a worse first-run experience than picking sensible bounds. They are right about the
// experience and wrong about the product. So every refusal below names a NEXT ACTION, which is the
// thing that actually fixes the experience.
//
// # 🚫 Nothing a person can type may widen a bound
//
// Ceilings come from the tenant's entitlement and the surface, never from the sentence. A sentence that
// could raise its own budget is a sentence that spends a month's allowance.
package bounds

import (
	"errors"
	"fmt"
	"time"
)

// Ceilings are the hard limits on one durable goal. Every field is a stop condition, and a zero value
// means "not permitted at all" rather than "unlimited" — see Validate.
//
// 🔴 Zero-means-forbidden rather than zero-means-unlimited is the single most important decision in this
// file. The opposite convention turns every forgotten field, every partially-populated struct and every
// zero-valued test fixture into an unbounded run. Fail closed.
type Ceilings struct {
	// MaxIterations bounds the agent loop: how many observe→plan→execute→verify cycles this goal may run.
	MaxIterations int
	// MaxTasks bounds the DAG, including tasks created by replanning. Without it, a planner that emits
	// one follow-up per completed task never terminates.
	MaxTasks int
	// MaxAttemptsPerTask bounds the retry ladder for a single task.
	MaxAttemptsPerTask int
	// MaxToolCalls bounds side-effect surface across the whole goal.
	MaxToolCalls int
	// MaxTokens bounds model spend in tokens.
	MaxTokens int64
	// MaxCostCents bounds model spend in money, in whole CENTS — the unit a person sets ("$5.00").
	// Carried alongside tokens rather than derived from them: a price change would silently move a token
	// ceiling's real cost, and the ceiling a customer agreed to was denominated in money.
	//
	// 🔴 The ceiling is in cents and the LEDGER is in micro-cents (see Spend.CostMicroCents). The units
	// differ on purpose: a person sets a bound at a resolution they can reason about, and the system
	// accumulates at a resolution that does not distort. MicroCentsPerCent below is the only place the
	// two meet.
	MaxCostCents int64
	// MaxWallClock bounds elapsed time from goal start, including time spent asleep between wake-ups.
	MaxWallClock time.Duration
	// MaxSpawnDepth bounds recursive task creation: a task created by a task created by a task. Depth
	// rather than count, because the runaway shape is recursive rather than wide.
	MaxSpawnDepth int
}

// ErrCeilingUnset means a ceiling was left at zero. Typed so a caller can tell a misconfigured goal from
// a goal that legitimately hit a limit.
var ErrCeilingUnset = errors.New("bounds: ceiling is unset, which is not a licence to run unbounded")

// Validate reports whether these ceilings may start a run. Every field must be positive.
func (c Ceilings) Validate() error {
	checks := []struct {
		name string
		ok   bool
	}{
		{"MaxIterations", c.MaxIterations > 0},
		{"MaxTasks", c.MaxTasks > 0},
		{"MaxAttemptsPerTask", c.MaxAttemptsPerTask > 0},
		{"MaxToolCalls", c.MaxToolCalls > 0},
		{"MaxTokens", c.MaxTokens > 0},
		{"MaxCostCents", c.MaxCostCents > 0},
		{"MaxWallClock", c.MaxWallClock > 0},
		{"MaxSpawnDepth", c.MaxSpawnDepth > 0},
	}
	for _, ch := range checks {
		if !ch.ok {
			return fmt.Errorf("%w: %s", ErrCeilingUnset, ch.name)
		}
	}
	return nil
}

// MicroCentsPerCent converts the ledger's unit to the ceiling's unit. The single conversion point.
const MicroCentsPerCent = 1_000_000

// Spend is what a goal has consumed so far. It is persisted with the goal and reconstructed on every
// wake-up: an in-memory counter would reset on restart, and a ceiling that resets is not a ceiling.
type Spend struct {
	Iterations int
	Tasks      int
	ToolCalls  int
	Tokens     int64
	// CostMicroCents is spend in MILLIONTHS of a cent. See Ceilings.MaxCostCents for why the units differ.
	CostMicroCents int64
	Elapsed        time.Duration
	SpawnDepth     int
}

// Exceeded reports the first ceiling this spend has reached, and whether any has.
//
// Returns the FIRST rather than all of them because the caller's next action is identical in every case
// — stop the goal and say which limit stopped it — and a list invites a caller to decide that some
// limits matter more than others.
func (s Spend) Exceeded(c Ceilings) (string, bool) {
	switch {
	case s.Iterations >= c.MaxIterations:
		return "MaxIterations", true
	case s.Tasks >= c.MaxTasks:
		return "MaxTasks", true
	case s.ToolCalls >= c.MaxToolCalls:
		return "MaxToolCalls", true
	case s.Tokens >= c.MaxTokens:
		return "MaxTokens", true
	case s.CostMicroCents >= c.MaxCostCents*MicroCentsPerCent:
		return "MaxCostCents", true
	case s.Elapsed >= c.MaxWallClock:
		return "MaxWallClock", true
	case s.SpawnDepth >= c.MaxSpawnDepth:
		return "MaxSpawnDepth", true
	}
	return "", false
}

// ── Refusals ─────────────────────────────────────────────────────────────────────────────────────

// RefusalCause is why a request could not become a bounded plan.
//
// A CLOSED set: each value leads to a different next action, and a caller that cannot tell them apart
// can only say "I cannot do that", which is the shape of refusal that teaches a person nothing.
type RefusalCause string

const (
	// NoSubject: no repository or workflow was named and none was supplied by the surface.
	// 🔴 Distinct from every other cause because the person has typed nothing WRONG — they simply have
	// not said which repository — and the next action is to pick one, not to rephrase.
	NoSubject RefusalCause = "no_subject"
	// NoSourceRevision: the subject has no resolved revision to pin. An unpinned run cannot be
	// re-measured against what it changed, so it is refused rather than run unpinned.
	NoSourceRevision RefusalCause = "no_source_revision"
	// MultipleSubjects: more than one repository or workflow was named. One subject, one revision is a
	// stated boundary, not a limitation to work around.
	MultipleSubjects RefusalCause = "multiple_subjects"
	// UnboundedRequested: the request explicitly asks for no bound — "keep going until", "as many as it
	// takes", "no limit".
	// 🔴 This is the case the whole rule exists for, and the one where refusing feels least helpful and
	// is most necessary: the person has asked, in words, for the product this platform does not sell.
	UnboundedRequested RefusalCause = "unbounded_requested"
	// UnknownAxis: the request names a surface outside the closed set of nine.
	UnknownAxis RefusalCause = "unknown_axis"
	// NoBudget: the tenant's entitlement yields no spend budget. Not an error — a plan that does not
	// include durable runs is a real state — but emphatically not a reason to run with a default budget.
	NoBudget RefusalCause = "no_budget"
	// CeilingExceeded: a running goal reached a limit. Terminal for that goal, and the next action is to
	// raise the ceiling deliberately or narrow the question.
	CeilingExceeded RefusalCause = "ceiling_exceeded"
	// PlanExhausted: every task in the plan reached a terminal state and the objective was still not
	// met. Nothing was over-spent — usually nothing was spent at all.
	//
	// 🔴 This used to be reported as CeilingExceeded, and that sent every reader to the wrong place.
	// A real run of it on eval: nine axis tasks, all failed, with 0/60 tasks, 0/400000 tokens, $0.00
	// of $1.00 and 18/200 iterations — not one ceiling within sight of its limit. The cause named a
	// budget problem for a run whose actual finding was "there is no agent in this repository", and
	// the next action followed the cause, so the product told somebody to raise a ceiling that had
	// never been touched.
	PlanExhausted RefusalCause = "plan_exhausted"
	// PlanStalled: work remains and none of it can ever become ready — every unfinished task depends
	// on something that failed. Distinct from PlanExhausted because the plan did not run out: it
	// seized partway.
	PlanStalled RefusalCause = "plan_stalled"
)

// nextActions maps each cause to what the person should do about it.
//
// 🔴 A table rather than a switch at each call site, so that adding a cause without a next action fails
// the fence instead of shipping a refusal that ends the conversation.
var nextActions = map[RefusalCause]string{
	NoSubject:          "name a repository, or pick one from your connected repositories",
	NoSourceRevision:   "push a revision, or connect the repository so a revision can be resolved",
	MultipleSubjects:   "ask about one repository at a time",
	UnboundedRequested: "give the run a limit — a number of proposals, a spend, or a deadline",
	UnknownAxis:        "name one of the nine axes: model, prompt, skills, context, tools, memory, harness, loop, graph",
	NoBudget:           "add budget to this organization's plan, or run the read-only assessment instead",
	CeilingExceeded:    "raise the ceiling for this goal deliberately, or narrow the question and run again",
	// 🔴 Neither of these says "raise the ceiling". The run did not spend anything, so a bigger
	// budget changes nothing; what it found is that the subject does not support the question.
	PlanExhausted: "read what the tasks reported — if there is no agent in this repository there is " +
		"nothing to assess, so point her at one, or ask a narrower question of this one",
	PlanStalled: "read which task failed first — everything after it was waiting on it",
}

// Refusal is a request that could not be bounded, with what to do about it.
type Refusal struct {
	Cause RefusalCause
	// Detail is the specific fact — which ceiling, which axis, which subjects.
	Detail string
}

// NextAction is what the person should do. Never empty for a valid cause.
func (r Refusal) NextAction() string { return nextActions[r.Cause] }

// Error makes a Refusal usable at a call boundary without losing its structure.
func (r Refusal) Error() string {
	msg := fmt.Sprintf("refused: %s", r.Cause)
	if r.Detail != "" {
		msg += " (" + r.Detail + ")"
	}
	if na := r.NextAction(); na != "" {
		msg += " — " + na
	}
	return msg
}

// Causes returns the closed set, for the fence and for a surface that renders them.
func Causes() []RefusalCause {
	return []RefusalCause{NoSubject, NoSourceRevision, MultipleSubjects,
		UnboundedRequested, UnknownAxis, NoBudget, CeilingExceeded, PlanExhausted, PlanStalled}
}
