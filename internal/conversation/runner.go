package conversation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// runner.go is the turn: `understand → plan → act → verify → respond`, with the budget checked before
// every step and every planned step reconciled at the end.
//
// # Why this is a LOOP with named phases rather than a handler that takes a while
//
// The failure being prevented is not a crash. It is a **plausible short answer**: an agent that ran
// three of eight planned steps because it hit a token budget produces prose indistinguishable from one
// that ran all eight — same tone, same confidence, fewer facts. Nothing errors, nothing logs, and the
// reader has no denominator.
//
// Announcing the plan first creates the denominator. Reconciling it at the end makes the shortfall a
// RENDERED STATE rather than an absence. Neither of those is an implementation detail; they are the
// product (PRD §6.6).
//
// # The seams, and why they are interfaces here
//
// `SurfaceReader`, `PinResolver` and `BudgetSource` are the three things this package must not own: a
// read model, a pin store and an entitlement. They are interfaces so the loop above can be tested for
// the properties that matter — budget refuses BEFORE spending, re-entry terminates naming the step,
// every planned step is reconciled — without a database, and so those tests cannot be satisfied by a
// simplified re-implementation of the loop.

// SurfaceReading is what one working surface answered about a question.
//
// 🔴 The reader returns a STATE, never an absence. A reader that could return "nothing" would make
// silence about a surface indistinguishable from "nothing is wrong with it", which is the exact reading
// error a conversational surface invites (PRD §9.1).
type SurfaceReading struct {
	Claim       string
	EvidenceRef string
	State       FindingState
	// MissingInput is required when State is not_measured.
	MissingInput string
	// Cause is required when State is refused, and is the lower layer's own words.
	Cause string
	// SourceRevision is required when State is stale.
	SourceRevision string
	Axis           string
	Node           string
}

// ErrStepIncomplete is a reader's way of saying "enter this step again".
//
// 🔴 It carries NO retry budget and NO backoff of its own, on purpose. A reader that could ask for "up
// to five more attempts" would be a second stop condition beside the accountant's, and two stop
// conditions is how a loop acquires one that nobody enforces. The reader says only that it is not done;
// `Budget.Admit` decides how many times that may be true, and terminates with `ceiling` naming the step.
var ErrStepIncomplete = errors.New("conversation: the step is not finished and asks to be entered again")

// SurfaceReader reads the working surface an intent resolves to.
type SurfaceReader interface {
	// Read answers a question about one surface. An error is a TRANSPORT failure; an unmeasurable
	// surface is a reading with State not_measured, which is a different thing and renders differently.
	Read(ctx context.Context, tenantID, workflowID string, spec IntentSpec) (SurfaceReading, error)
	// Mounted reports whether the capability an intent needs is present in this deployment. False
	// produces the `not_mounted` failure class — never a 404, and never a business state.
	Mounted(spec IntentSpec) bool
}

// Pin is a pinned inference, resolved for a question (FR11, task 2.8).
type Pin struct {
	// Found is false when nothing is pinned for this (source_revision, config_hash).
	Found bool
	// SourceRevision is the revision the pinned answer describes.
	SourceRevision string
	// CurrentRevision is the workflow's revision now. When it differs, the replay is STALE.
	CurrentRevision string
	// Reading is the pinned answer itself.
	Reading SurfaceReading
}

// Stale reports whether the pin describes an earlier revision than the workflow's current one.
func (p Pin) Stale() bool {
	return p.Found && p.CurrentRevision != "" && p.SourceRevision != p.CurrentRevision
}

// PinResolver resolves a pinned inference without making a provider call.
//
// 🔴 The "without a provider call" half is the requirement, not an optimisation. FR11's guarantee is
// that a repeated question costs nothing and returns the same answer, and the fence for it (task 6.7)
// asserts *no provider call was made* rather than asserting the answers matched — two answers matching
// is what a deterministic model does too, so matching proves nothing about the pin.
type PinResolver interface {
	Resolve(ctx context.Context, tenantID, workflowID string, spec IntentSpec) (Pin, error)
}

// BudgetSource supplies the envelope for a turn (PRD §14 Q6).
//
// 🚫 It takes no input from the request. An envelope the person could influence is an envelope one
// question uses to spend a month's allowance, and the person typing the question is the one least able
// to price it.
type BudgetSource interface {
	Envelope(ctx context.Context, tenantID string) (BudgetEnvelope, error)
}

// Router routes a question to one intent, or abstains. §3 implements it.
type Router interface {
	Route(question string) Routing
}

// Runner executes one turn.
type Runner struct {
	Store   *Store
	Router  Router
	Reader  SurfaceReader
	Pins    PinResolver
	Budgets BudgetSource
	// Now is the injected clock; nothing here reads time.Now directly.
	Now func() time.Time
	// Observe records events from the central enum. Optional.
	Observe func(eventname.Name, map[string]any)
}

// TurnRequest is one question.
type TurnRequest struct {
	ConversationID string
	Owner          Owner
	WorkflowID     string
	TurnID         string
	TraceID        string
	Question       string
	// Emitter carries the per-turn identity and the artifact resolvers. Supplied by the API layer,
	// which is the only place the request ids and the stores are both in scope.
	Emitter *Emitter
}

// Run executes the turn to a terminal message and returns the stop reason it ended on.
//
// The shape is deliberately linear and readable: every phase transition, every emission and every
// budget check is on this page. A loop whose control flow is spread across five helpers is a loop
// nobody can confirm terminates, and termination is the property FR22 is about.
func (r *Runner) Run(ctx context.Context, req TurnRequest) (harnessruntime.StopReason, error) {
	if r.Store == nil || r.Router == nil || r.Reader == nil || r.Budgets == nil || r.Now == nil {
		return "", errors.New("conversation: the runner is missing a collaborator; it would run a " +
			"different turn than the one configured")
	}
	em := req.Emitter

	// ── understand ───────────────────────────────────────────────────────────────────────────────
	routing := r.Router.Route(req.Question)
	if routing.Abstained() {
		// FR14: an abstention names what this surface CAN do, and 🔴 NO RUN IS STARTED. Starting one
		// would spend a budget on a question nobody could route.
		return r.refuse(ctx, em, RefusalPayload{
			Cause:       routing.Cause,
			CanDo:       CanDo(),
			SurfaceHref: routing.SurfaceHref,
			StopReason:  harnessruntime.StopSatisfied,
		})
	}
	spec, ok := Lookup(routing.Intent)
	if !ok {
		return r.refuse(ctx, em, RefusalPayload{
			Cause:      fmt.Sprintf("the router produced %q, which is not in the intent set", routing.Intent),
			CanDo:      CanDo(),
			StopReason: harnessruntime.StopSatisfied,
		})
	}
	if !r.Reader.Mounted(spec) {
		// FR4's not-mounted class, preserved into the conversation. 🔴 A 503 stays a 503: the person
		// is told the subsystem is absent from this deployment, which is a remedy, rather than being
		// told the subject was not found, which sends them to check an identifier that is correct.
		return r.refuse(ctx, em, RefusalPayload{
			Cause:      fmt.Sprintf("%q is not available in this deployment", spec.Surface),
			Failure:    FailureNotMounted,
			CanDo:      CanDo(),
			StopReason: harnessruntime.StopSatisfied,
		})
	}
	// The route decides whether prose is admissible, not a classifier over the prose (FR3).
	em.ClaimsRepository = routing.ClaimsRepository

	// ── plan ─────────────────────────────────────────────────────────────────────────────────────
	envelope, err := r.Budgets.Envelope(ctx, req.Owner.TenantID)
	if err != nil {
		return r.refuse(ctx, em, RefusalPayload{
			Cause:      "the budget for this organization could not be resolved: " + err.Error(),
			Failure:    FailureTransport,
			StopReason: harnessruntime.StopSatisfied,
		})
	}
	budget, err := NewBudget(envelope, r.Now(), r.Now)
	if err != nil {
		return r.refuse(ctx, em, RefusalPayload{
			Cause:      err.Error(),
			Failure:    FailureTransport,
			StopReason: harnessruntime.StopSatisfied,
		})
	}

	// FR11: a pinned inference is resolved BEFORE the plan, because whether this turn replays or
	// generates decides the provenance of every message it will emit — and provenance is set once.
	pin := Pin{}
	if r.Pins != nil {
		pin, err = r.Pins.Resolve(ctx, req.Owner.TenantID, req.WorkflowID, spec)
		if err != nil {
			// A pin store that cannot be read is not a reason to refuse the turn: generating is the
			// correct fallback and it is honest, because provenance will say `generated`.
			pin = Pin{}
		}
	}
	if pin.Found {
		em.Provenance = ProvenancePinned
	} else {
		em.Provenance = ProvenanceGenerated
	}

	steps := planSteps(spec)
	if _, err := r.Store.StartTurn(req.ConversationID, req.Owner, req.TurnID, req.TraceID, spec.Intent, budget); err != nil {
		return "", err
	}
	if r.Observe != nil {
		r.Observe(eventname.ConversationTurnStarted, map[string]any{
			"intent": spec.Intent.String(), "turn_id": req.TurnID, "provenance": em.Provenance.String(),
		})
	}
	if err := r.Store.AdvancePhase(req.TurnID, PhasePlan); err != nil {
		return "", err
	}
	if _, err := em.Emit(ctx, Message{Kind: KindPlan, Plan: &PlanPayload{
		Intent: spec.Intent, Steps: steps, Budget: envelope,
	}}); err != nil {
		return "", err
	}

	// ── act ──────────────────────────────────────────────────────────────────────────────────────
	if err := r.Store.AdvancePhase(req.TurnID, PhaseAct); err != nil {
		return "", err
	}
	startedAt := r.Now()
	reconciliation := make([]ReconciliationEntry, 0, len(steps))
	stop := harnessruntime.StopSatisfied

	for i := 0; i < len(steps); {
		step := steps[i]
		cost := stepCost(step)
		if err := budget.Admit(step.ID, cost); err != nil {
			// 🔴 The budget refused BEFORE the step ran, so nothing was spent on it. Every remaining
			// step — this one included — reconciles as `not_measured` naming the limit that stopped
			// it, and NEVER as a shorter answer presented as the whole one (FR18).
			var exhausted ErrBudgetExhausted
			if errors.As(err, &exhausted) {
				stop = exhausted.Reason
			} else {
				stop = harnessruntime.StopCeiling
			}
			for _, remaining := range steps[i:] {
				reconciliation = append(reconciliation, ReconciliationEntry{
					StepID: remaining.ID, State: StepNotMeasured,
					Reason: fmt.Sprintf("the turn stopped on its %s before this step ran", stop),
				})
				_ = r.Store.RecordStep(req.TurnID, remaining.ID, StepNotMeasured)
			}
			break
		}

		if _, err := em.Emit(ctx, Message{Kind: KindProgress, Progress: &ProgressPayload{
			Phase: PhaseAct, StepID: step.ID, Detail: step.Title,
			ElapsedMS: r.Now().Sub(startedAt).Milliseconds(), Remaining: budget.Remaining(),
		}}); err != nil {
			return "", err
		}

		entry, again := r.runStep(ctx, em, req, spec, step, pin)
		budget.Settle(StepCost{Tokens: cost.Tokens / 2, ToolCalls: 1, Turns: 1}, cost)
		if again {
			// 🔴 FR22. The step asked to be entered again, and the loop obliges — WITHOUT advancing.
			// Nothing here decides when to give up: `Admit` refuses on the next pass once the step's
			// entry count reaches StepReEntryCeiling, and it refuses with `ceiling` naming this step.
			//
			// This is deliberate. A stop condition that lives in the loop is a stop condition somebody
			// can weaken while fixing an unrelated bug; a stop condition that lives in the accountant
			// is one the accountant enforces for every step in every turn. Infinite loops in agents are
			// not exotic — they are the DEFAULT failure of a loop whose stop condition depends on model
			// output — so the guard must not be part of the thing that can go wrong.
			continue
		}
		reconciliation = append(reconciliation, entry)
		_ = r.Store.RecordStep(req.TurnID, step.ID, entry.State)
		i++
	}

	// ── verify ───────────────────────────────────────────────────────────────────────────────────
	//
	// The phase exists; its OUTPUT is the reconciliation and the verdict reference carried on `result`
	// (design.md D8). Nothing is emitted here, because a `verification` message kind would be a second
	// notion of "checked" beside the ledger the proposal gate already reads.
	if err := r.Store.AdvancePhase(req.TurnID, PhaseVerify); err != nil {
		return "", err
	}
	if _, err := em.Emit(ctx, Message{Kind: KindProgress, Progress: &ProgressPayload{
		Phase: PhaseVerify, Detail: "checking each claim against the artifact behind it",
		ElapsedMS: r.Now().Sub(startedAt).Milliseconds(), Remaining: budget.Remaining(),
	}}); err != nil {
		return "", err
	}

	// ── respond ──────────────────────────────────────────────────────────────────────────────────
	if err := r.Store.AdvancePhase(req.TurnID, PhaseRespond); err != nil {
		return "", err
	}
	if stop == harnessruntime.StopSatisfied {
		budget.Satisfy()
	}
	// 🔴 The stop reason and the step it fired on are read back from the ACCOUNTANT, not from the local
	// variable. The two can disagree in exactly one direction and it is the dangerous one: a person
	// cancelling mid-run sets the reason on the budget while this function is inside a step, and a
	// terminal message built from the local variable would render that run as satisfied.
	stop, stoppedAt, _ := budget.Stopped()
	reason, onLimit := TerminalStop(stop)
	if _, err := em.Emit(ctx, Message{Kind: KindResult, Result: &ResultPayload{
		RunID:          req.ConversationID,
		StopReason:     reason,
		StoppedOnLimit: onLimit,
		StoppedAtStep:  stoppedAt,
		Reconciliation: reconciliation,
		Summary:        summarise(spec, reconciliation, reason),
	}}); err != nil {
		return "", err
	}
	if err := r.Store.FinishTurn(req.TurnID, reason); err != nil {
		return "", err
	}
	return reason, nil
}

// runStep performs one step and returns how it reconciled.
//
// A step that reads a surface emits a `finding`; a step that could not emits a `finding` in the
// `not_measured` state naming what was missing. 🚫 Neither path may emit nothing: an absent finding is
// how a surface with a problem reads as a surface without one.
func (r *Runner) runStep(ctx context.Context, em *Emitter, req TurnRequest, spec IntentSpec, step PlanStep, pin Pin) (ReconciliationEntry, bool) {
	reading := pin.Reading
	if !pin.Found {
		got, err := r.Reader.Read(ctx, req.Owner.TenantID, req.WorkflowID, spec)
		if errors.Is(err, ErrStepIncomplete) {
			// The surface says it has more to do. The step is re-entered; the ceiling is the
			// accountant's job, not this function's — see the caller.
			return ReconciliationEntry{}, true
		}
		if err != nil {
			// A transport failure is a REFUSED step carrying the lower layer's words, not a silent skip.
			return ReconciliationEntry{StepID: step.ID, State: StepRefused, Reason: err.Error()}, false
		}
		reading = got
	} else if pin.Stale() {
		// PRD §14 Q2: answer from the pin, LABEL IT STALE, name the revision it describes.
		reading.State = FindingStale
		reading.SourceRevision = pin.SourceRevision
	}

	finding := &FindingPayload{
		Surface: surfaceName(spec), SurfaceHref: spec.Surface,
		Axis: reading.Axis, Node: reading.Node,
		Claim: reading.Claim, EvidenceRef: reading.EvidenceRef, State: reading.State,
		MissingInput: reading.MissingInput, Cause: reading.Cause, SourceRevision: reading.SourceRevision,
	}
	if _, err := em.Emit(ctx, Message{Kind: KindFinding, Finding: finding}); err != nil {
		// 🔴 The emitter refused the finding — no evidence reference, or a state with no reason. The
		// step reconciles as REFUSED carrying that cause. It does NOT become an `answer`: re-expressing
		// a rejected claim as prose is precisely the substitution FR3 exists to prevent.
		return ReconciliationEntry{StepID: step.ID, State: StepRefused, Reason: err.Error()}, false
	}

	switch reading.State {
	case FindingNotMeasured:
		return ReconciliationEntry{StepID: step.ID, State: StepNotMeasured, Reason: reading.MissingInput}, false
	case FindingRefused:
		return ReconciliationEntry{StepID: step.ID, State: StepRefused, Reason: reading.Cause}, false
	default:
		return ReconciliationEntry{StepID: step.ID, State: StepDone}, false
	}
}

// refuse emits the terminal refusal and reports the stop reason.
func (r *Runner) refuse(ctx context.Context, em *Emitter, p RefusalPayload) (harnessruntime.StopReason, error) {
	if _, err := em.Emit(ctx, Message{Kind: KindRefusal, Refusal: &p}); err != nil {
		return "", err
	}
	return p.StopReason, nil
}

// planSteps is the plan for one intent. Three steps, and the third is what makes `verify` observable.
func planSteps(spec IntentSpec) []PlanStep {
	name := surfaceName(spec)
	return []PlanStep{
		{ID: "resolve-workflow", Title: "resolve the workflow and its current source revision", Surface: spec.Surface},
		{ID: "read-" + name, Title: "read what " + spec.Surface + " knows about this question", Surface: spec.Surface},
		{ID: "check-evidence", Title: "check every claim against the artifact behind it", Surface: spec.Surface},
	}
}

// stepCost is what a step reserves. Modest and uniform for now: the point of the reservation is that
// the check happens BEFORE the step, and a per-step estimate that is merely plausible does not weaken
// that — `Settle` reconciles the difference so an over-reservation costs nothing.
func stepCost(PlanStep) StepCost {
	return StepCost{Tokens: 2000, ToolCalls: 2, Turns: 1}
}

// surfaceName is the last path segment of a route-backed surface, or the capability name.
func surfaceName(spec IntentSpec) string {
	s := spec.Surface
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

// summarise is the terminal message's one sentence. Prose, and safe as prose: it asserts nothing about
// the repository — every such assertion is in a `finding` and carries its evidence.
func summarise(spec IntentSpec, entries []ReconciliationEntry, stop harnessruntime.StopReason) string {
	done := 0
	for _, e := range entries {
		if e.State == StepDone {
			done++
		}
	}
	if stop.Limit() {
		return fmt.Sprintf("Stopped on the %s after %d of %d planned steps, on the %s question.",
			stop, done, len(entries), spec.Intent)
	}
	return fmt.Sprintf("Completed %d of %d planned steps on the %s question.", done, len(entries), spec.Intent)
}
