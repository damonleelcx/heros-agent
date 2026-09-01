// Package worker is the agent loop: observe → plan → execute → verify → persist → continue.
//
// # The rule this package exists to enforce
//
// A long-running agent is not a long-running LLM call. Every cycle here is BOUNDED: the worker claims
// one task, does one unit of work under a lease, writes the result down, releases, and returns. Nothing
// is carried in memory between cycles, because a process that dies between two cycles must be
// indistinguishable from one that was never running.
//
// # Why RunOnce rather than Run
//
// The loop is the CALLER's, not this package's. A `Run(ctx)` that spins forever hides the two decisions
// that matter — when to wake and when to stop — inside a goroutine nobody can inspect. `RunOnce`
// returns an Outcome saying what happened and whether there is more to do, so a scheduler, a test, or a
// person at a terminal can drive it identically. Test recovery is then a matter of simply not calling
// RunOnce again, which is exactly what a crash is.
package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// Outcome says what one cycle did. It is returned rather than logged because the caller needs to decide
// whether to run again, and "did anything happen" is not recoverable from a log line.
type Outcome struct {
	// Did names what happened, from the closed set below.
	Did Did
	// TaskID is the task this cycle touched, empty when none was claimed.
	TaskID task.ID
	// Detail is the human-readable reason — which ceiling, which verification, which dependency.
	Detail string
	// More says whether calling RunOnce again could make progress. A caller that ignores it either
	// spins on an idle goal or stops on a live one.
	More bool
}

// Did is the closed set of cycle results.
type Did string

const (
	// DidWork — a task ran and reached a terminal state.
	DidWork Did = "work"
	// DidRetry — a task failed and was returned for another attempt.
	DidRetry Did = "retry"
	// DidNothing — no claimable task. Not an error: another worker may hold the only ready task.
	DidNothing Did = "nothing"
	// DidComplete — every completion criterion is met. Terminal.
	DidComplete Did = "complete"
	// DidStall — work remains but none of it can ever run. Terminal, and DETECTED rather than waited
	// out: a stalled goal otherwise burns its wall-clock ceiling to reach a conclusion that was
	// available immediately, looking busy the entire time.
	DidStall Did = "stall"
	// DidStop — a ceiling was reached, or the goal is not claimable (paused, cancelled, finished).
	DidStop Did = "stop"
	// DidReplan — the plan grew because results justified new work. No task ran this cycle.
	DidReplan Did = "replan"
	// DidBlockedOnApproval — every remaining task is waiting for a person. Terminal FOR THIS WORKER: no
	// amount of further polling changes anything until a human answers.
	//
	// 🔴 A distinct outcome rather than DidNothing, and the distinction is the whole reason it exists.
	// Both mean "I claimed nothing this cycle", but DidNothing says "try again shortly" and this says
	// "the ball is in your court". Collapsing them produces a goal that reports `no claimable task` on
	// every cycle forever — which reads to an operator as a healthy, busy run, while it is in fact
	// waiting on them and will wait until the wall-clock ceiling kills it.
	DidBlockedOnApproval Did = "blocked_on_approval"
	// DidAwaitApproval — the task's effect is gated on a human. The lease is RELEASED: a person may take
	// a week, and a week-long lease is indistinguishable from a hung worker.
	DidAwaitApproval Did = "await_approval"
)

// ApprovalPolicy decides whether a task's effect needs a human before it runs.
//
// An interface rather than a flag so the decision can consider the goal, the tenant and the task
// together — "this tenant has pre-approved source writes but never remote pushes" is a real policy and
// it is not expressible as a boolean on a task.
type ApprovalPolicy interface {
	NeedsApproval(g *goal.Goal, t *task.Task) (bool, string)
}

// GateEffectsOutsideThePlatform is the default policy: anything that changes the customer's world waits
// for a person. 🔴 Conservative on purpose. A policy that is wrong in the permissive direction is
// discovered by the customer.
type GateEffectsOutsideThePlatform struct{}

func (GateEffectsOutsideThePlatform) NeedsApproval(_ *goal.Goal, t *task.Task) (bool, string) {
	if task.EffectBearingKinds[t.Kind] {
		return true, fmt.Sprintf("%q changes something outside the platform", t.Kind)
	}
	return false, ""
}

// Reviser revises a plan from what the DAG has produced. Optional: a worker with no reviser executes a
// fixed plan, which is correct for goals whose shape is known up front.
//
// An interface rather than the concrete planner registry so this package does not depend on it —
// worker executes tasks and must not acquire an opinion about how they are chosen.
type Reviser interface {
	Revise(g *goal.Goal, d *task.DAG, now time.Time) ([]*task.Task, error)
}

// Clock is injected so tests can move time without sleeping. A worker that calls time.Now() directly
// cannot have its lease-expiry behaviour tested except by waiting, and a test that waits is a test that
// gets deleted.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Worker executes one task per cycle.
type Worker struct {
	ID     string
	Store  store.Store
	Tools  *toolcontract.Registry
	Policy ApprovalPolicy
	// Episodes records what happened, if a store is wired.
	//
	// 🔴 Optional, and every write is best-effort: a memory failure must never fail a task. Episodic
	// history is a record OF the work, not part of it, and a run that dies because its diary was
	// unavailable has inverted which one matters. Failures to record are surfaced in the outcome detail
	// rather than swallowed entirely.
	Episodes memory.Store
	// Reviser is optional. A worker without one executes a fixed plan, which is correct for goals whose
	// shape is known from the goal alone.
	Reviser Reviser
	Clock   Clock
	// Lease is how long a claim is held. Short on purpose: a worker doing long work RENEWS. A long lease
	// is indistinguishable from a hung worker, and the reclaim logic cannot tell them apart.
	Lease time.Duration
}

// New builds a worker with the conservative defaults.
func New(id string, s store.Store, tools *toolcontract.Registry) *Worker {
	return &Worker{ID: id, Store: s, Tools: tools,
		Policy: GateEffectsOutsideThePlatform{}, Clock: realClock{}, Lease: 30 * time.Second}
}

// RunOnce performs exactly one bounded cycle.
func (w *Worker) RunOnce(ctx context.Context, goalID goal.ID) (Outcome, error) {
	now := w.Clock.Now()

	// ── observe ──────────────────────────────────────────────────────────────────────────────────
	//
	// Read from the store, never from anything this worker remembers. This is what makes "never rely on
	// model context as memory" structural: there is no other source to be tempted by.
	g, err := w.Store.LoadGoal(goalID)
	if err != nil {
		return Outcome{Did: DidStop}, err
	}
	if !g.Claimable() {
		return Outcome{Did: DidStop, Detail: fmt.Sprintf("goal is %s", g.State)}, nil
	}

	// Ceilings are checked BEFORE claiming. Starting work that cannot be paid for spends the budget
	// getting to the point of discovering it could not be paid for.
	if which, hit := g.CheckCeilings(now); hit {
		if err := w.Store.SaveGoal(g); err != nil {
			return Outcome{Did: DidStop}, err
		}
		return Outcome{Did: DidStop, Detail: "ceiling reached: " + which}, nil
	}

	// ── plan ─────────────────────────────────────────────────────────────────────────────────────
	//
	// Planning here is the readiness question, and it is answered by the STORE rather than by a DAG this
	// worker holds: a stale in-memory graph would run work out of order.
	t, err := w.Store.Claim(goalID, w.ID, w.Lease, now)
	switch {
	case errors.Is(err, store.ErrNoWork):
		return w.idle(goalID, g, now)
	case err != nil:
		return Outcome{Did: DidStop}, err
	}

	// An effect-bearing task with no idempotency key must never reach a tool: a retry would duplicate
	// the effect, and the duplicate is found by the customer.
	if err := t.RequireIdempotency(); err != nil {
		return w.fail(goalID, t, g, now, err.Error())
	}

	// ── approval gate ────────────────────────────────────────────────────────────────────────────
	//
	// Before the effect, not after. Checked after claiming because the policy may depend on the task,
	// and the lease is released immediately so the human's thinking time is not a held claim.
	// 🔴 `!t.Approved` is the whole of the fix for a gate that re-fired forever. The policy is asked
	// whether this KIND of work needs a person; the task remembers whether one has answered.
	if need, why := w.Policy.NeedsApproval(g, t); need && !t.Approved {
		w.record(goalID, t, memory.EpisodeDecision,
			fmt.Sprintf("%s parked for approval", t.ID), why, now)
		if err := w.Store.Complete(goalID, t.ID, w.ID, task.AwaitingApproval, nil, why, now); err != nil {
			return Outcome{Did: DidStop}, err
		}
		return Outcome{Did: DidAwaitApproval, TaskID: t.ID, Detail: why, More: true}, nil
	}

	// ── execute ──────────────────────────────────────────────────────────────────────────────────
	// A join reads its edges. Gathered from the store rather than carried in memory, because the worker
	// that produced a dependency's result may have been a different process on a different machine.
	inputs, err := w.dependencyResults(goalID, t)
	if err != nil {
		return Outcome{Did: DidStop, TaskID: t.ID}, err
	}
	res, execErr := w.Tools.Invoke(ctx, toolcontract.Call{
		TaskID: string(t.ID), GoalID: string(goalID), Kind: t.Kind,
		IdempotencyKey: t.IdempotencyKey, Input: t.Result, Inputs: inputs, Attempt: t.Attempt,
	})

	// Spend is recorded whether the call succeeded or failed. 🔴 A failed attempt still cost money, and
	// a ceiling that only counts successes is a ceiling a failing loop walks straight through.
	g.Spend.Iterations++
	g.Spend.ToolCalls += res.ToolCalls
	g.Spend.Tokens += res.Tokens
	g.Spend.CostMicroCents += res.CostMicroCents
	if saveErr := w.Store.SaveGoal(g); saveErr != nil {
		return Outcome{Did: DidStop}, saveErr
	}

	// ── cancellation ─────────────────────────────────────────────────────────────────────────────
	//
	// 🔴 A cancelled context RELEASES the lease rather than abandoning it. Abandoning leaves the task
	// claimed until expiry, so a clean shutdown looks exactly like a crash and delays every task the
	// shutdown was meant to hand over cleanly.
	if ctx.Err() != nil {
		if relErr := w.Store.Release(goalID, t.ID, w.ID, now); relErr != nil && !errors.Is(relErr, store.ErrLeaseLost) {
			return Outcome{Did: DidStop, TaskID: t.ID}, relErr
		}
		return Outcome{Did: DidRetry, TaskID: t.ID, Detail: "cancelled; lease released", More: true}, ctx.Err()
	}

	if execErr != nil {
		return w.handleFailure(goalID, t, g, now, execErr)
	}

	// ── persist ──────────────────────────────────────────────────────────────────────────────────
	w.record(goalID, t, memory.EpisodeObservation,
		fmt.Sprintf("%s succeeded", t.ID),
		fmt.Sprintf("%d tokens, %d micro-cents", res.Tokens, res.CostMicroCents), now)
	if err := w.Store.Complete(goalID, t.ID, w.ID, task.Succeeded, res.Output, "", now); err != nil {
		return Outcome{Did: DidStop, TaskID: t.ID}, err
	}
	if err := w.Store.Checkpoint(store.Checkpoint{GoalID: goalID, Iteration: g.Spend.Iterations,
		Note: fmt.Sprintf("%s succeeded", t.ID), At: now}); err != nil {
		return Outcome{Did: DidStop, TaskID: t.ID}, err
	}
	return Outcome{Did: DidWork, TaskID: t.ID, More: true}, nil
}

// record writes an episode, best-effort.
//
// 🔴 A failure here is reported to the caller's log path and never returned: the run is the product and
// the diary is not. Silently dropping it would be worse — a record that is sometimes absent and never
// says so is one nobody can reason about — so the error is counted in the outcome detail by the caller
// that cares.
func (w *Worker) record(goalID goal.ID, t *task.Task, kind memory.EpisodeKind, summary, detail string, now time.Time) {
	if w.Episodes == nil {
		return
	}
	taskID := ""
	if t != nil {
		taskID = string(t.ID)
	}
	_, _ = w.Episodes.AppendEpisode(memory.Episode{
		GoalID: string(goalID), TaskID: taskID, Kind: kind,
		Summary: summary, Detail: detail, At: now,
	})
}

// dependencyResults collects what this task's prerequisites produced.
//
// 🔴 Only DECLARED dependencies, and only successful ones. A task that could read any result would make
// the DAG's edges decorative — the graph would say what waits for what, while the data flowed wherever
// a tool reached. Restricting it to the declared edges is what keeps "why did this task see that value"
// answerable from the plan alone.
func (w *Worker) dependencyResults(goalID goal.ID, t *task.Task) (map[string][]byte, error) {
	edges := append(append([]task.ID(nil), t.DependsOn...), t.Contributes...)
	if len(edges) == 0 {
		return nil, nil
	}
	d, err := w.Store.LoadDAG(goalID)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(edges))
	for _, dep := range edges {
		dt := d.Tasks[dep]
		if dt == nil || dt.State != task.Succeeded {
			continue
		}
		out[string(dep)] = dt.Result
	}
	return out, nil
}

// handleFailure runs the retry ladder.
//
// # 🔴 An unconfirmable effect is NOT retried
//
// ErrVerifyInconclusive means the tool may have changed the world and we could not check. Retrying
// would risk doing it twice; the idempotency key protects the ones that have it, but a tool whose
// effect cannot even be READ is one whose duplicate cannot be detected either. So it fails terminally
// and a person looks. Choosing the safe direction here costs a manual intervention; choosing the other
// one costs a duplicate pull request, or two.
func (w *Worker) handleFailure(goalID goal.ID, t *task.Task, g *goal.Goal, now time.Time, execErr error) (Outcome, error) {
	if errors.Is(execErr, toolcontract.ErrVerifyInconclusive) {
		return w.fail(goalID, t, g, now,
			"effect could not be confirmed and must not be retried blindly: "+execErr.Error())
	}
	if t.Attempt >= g.Ceilings.MaxAttemptsPerTask {
		return w.fail(goalID, t, g, now,
			fmt.Sprintf("%d attempts exhausted: %v", t.Attempt, execErr))
	}
	// Return it for another attempt. The attempt counter was already incremented at Claim, so the ladder
	// cannot run forever even if this path is reached by an unexpected route.
	if err := w.Store.Release(goalID, t.ID, w.ID, now); err != nil {
		return Outcome{Did: DidStop, TaskID: t.ID}, err
	}
	return Outcome{Did: DidRetry, TaskID: t.ID,
		Detail: fmt.Sprintf("attempt %d/%d failed: %v", t.Attempt, g.Ceilings.MaxAttemptsPerTask, execErr),
		More:   true}, nil
}

// fail marks a task terminally failed. Downstream blocking is the store's job, so a worker cannot
// forget to do it.
func (w *Worker) fail(goalID goal.ID, t *task.Task, _ *goal.Goal, now time.Time, why string) (Outcome, error) {
	w.record(goalID, t, memory.EpisodeFailure, fmt.Sprintf("%s failed", t.ID), why, now)
	if err := w.Store.Complete(goalID, t.ID, w.ID, task.Failed, nil, why, now); err != nil {
		return Outcome{Did: DidStop, TaskID: t.ID}, err
	}
	return Outcome{Did: DidWork, TaskID: t.ID, Detail: why, More: true}, nil
}

// idle decides what "nothing to claim" means: finished, stalled, or simply busy elsewhere.
//
// 🔴 The three are genuinely different and a caller that cannot tell them apart either spins forever on
// a stalled goal or stops early on a healthy one where another worker holds the only ready task.
func (w *Worker) idle(goalID goal.ID, g *goal.Goal, now time.Time) (Outcome, error) {
	d, err := w.Store.LoadDAG(goalID)
	if err != nil {
		return Outcome{Did: DidNothing, More: true}, nil //nolint:nilerr // a goal may have no DAG yet
	}

	// ── replan ───────────────────────────────────────────────────────────────────────────────────
	//
	// 🔴 BEFORE the completion check, and the order is load-bearing. An `improve` goal's initial plan is
	// an assessment; when every assessment task succeeds, every task is terminal and "all tasks
	// succeeded" is TRUE — so a completion check that ran first would declare victory at exactly the
	// moment the real work became possible, and the customer would receive an empty improvement run
	// reported as a success.
	//
	// Replanning is a comparison of the goal against what the tasks actually produced, not a question
	// put to a model. Its bounds live in the reviser.
	if w.Reviser != nil {
		added, err := w.Reviser.Revise(g, d, now)
		if err != nil {
			// A reviser that cannot bound its additions stops the goal rather than truncating silently.
			g.State = goal.Failed
			g.Refusal = &bounds.Refusal{Cause: bounds.CeilingExceeded, Detail: err.Error()}
			g.UpdatedAt = now
			if serr := w.Store.SaveGoal(g); serr != nil {
				return Outcome{Did: DidStop}, serr
			}
			return Outcome{Did: DidStop, Detail: "replanning refused: " + err.Error()}, nil
		}
		if len(added) > 0 {
			w.record(goalID, nil, memory.EpisodeDecision, "the plan grew",
				fmt.Sprintf("%d task(s) added from what the run found", len(added)), now)
			merged := make([]*task.Task, 0, len(d.Tasks)+len(added))
			for _, t := range d.Tasks {
				merged = append(merged, t)
			}
			merged = append(merged, added...)
			grown, err := task.NewDAG(string(goalID), merged)
			if err != nil {
				return Outcome{Did: DidStop}, err
			}
			if err := w.Store.SaveDAG(grown); err != nil {
				return Outcome{Did: DidStop}, err
			}
			return Outcome{Did: DidReplan,
				Detail: fmt.Sprintf("%d task(s) added from what the run has found so far", len(added)),
				More:   true}, nil
		}
	}

	done, total := d.Progress()
	succeeded, awaiting := 0, 0
	for _, t := range d.Tasks {
		switch t.State {
		case task.Succeeded:
			succeeded++
		case task.AwaitingApproval:
			awaiting++
		}
	}

	// 🔴 Checked before completion and before the stall arms, because a goal waiting on a person is
	// neither finished nor broken. It is the one no-progress state whose resolution is somebody else's
	// action, so the worker must stop polling and SAY SO.
	if awaiting > 0 {
		return Outcome{Did: DidBlockedOnApproval,
			Detail: fmt.Sprintf("%d task(s) waiting for a person; %d/%d done", awaiting, done, total)}, nil
	}

	// Observations are counted from what the tasks actually produced, per kind. 🔴 Counting by TASK KIND
	// rather than by name: a criterion satisfied by "any succeeded task" would be satisfied by the wrong
	// ones, and the goal would report success on work nobody asked for.
	observed := map[goal.CriterionKind]int{}
	if total > 0 && succeeded == total {
		observed[goal.AllTasksSucceeded] = 1
	}
	for _, t := range d.Tasks {
		if t.State != task.Succeeded {
			continue
		}
		switch t.Kind {
		case "assess_axis":
			observed[goal.AxesAssessed]++
		case "publish_eval_set":
			observed[goal.EvalCasesGenerated]++
		case "compare_results":
			observed[goal.ComparisonDrawn]++
		}
	}

	if g.EvaluateCompletion(observed, now) {
		g.State = goal.Succeeded
		g.UpdatedAt = now
		if err := w.Store.SaveGoal(g); err != nil {
			return Outcome{Did: DidStop}, err
		}
		return Outcome{Did: DidComplete,
			Detail: fmt.Sprintf("%d/%d tasks succeeded", succeeded, total)}, nil
	}

	// 🔴 Two different ways a goal can be unable to progress, and both must be DETECTED rather than
	// waited out. Otherwise the goal is polled until its wall-clock ceiling expires, looking busy the
	// entire time, to reach a conclusion that was available on this very cycle.
	//
	//   1. `d.Stalled()` — work remains but none of it can ever become ready.
	//   2. every task is terminal and the criteria are STILL unmet — the run finished its plan and did
	//      not achieve its objective. Without this arm the worker returns "nothing to do" forever on a
	//      goal that is definitively over, which reads to an operator as a healthy idle goal.
	exhausted := total > 0 && done == total
	if d.Stalled() || exhausted {
		detail := fmt.Sprintf("%d/%d tasks terminal, %d succeeded; completion criteria not met",
			done, total, succeeded)
		if !exhausted {
			detail = fmt.Sprintf("%d/%d tasks terminal, none of the remainder can run", done, total)
		}
		g.State = goal.Failed
		g.Refusal = &bounds.Refusal{Cause: bounds.CeilingExceeded, Detail: detail}
		g.UpdatedAt = now
		if err := w.Store.SaveGoal(g); err != nil {
			return Outcome{Did: DidStop}, err
		}
		return Outcome{Did: DidStall, Detail: detail}, nil
	}

	// Not finished, not stalled: another worker holds the only claimable task.
	return Outcome{Did: DidNothing, Detail: "no claimable task right now", More: true}, nil
}
