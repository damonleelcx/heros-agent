package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// ── doubles ──────────────────────────────────────────────────────────────────────────────────────

// fakeReader is a SurfaceReader whose answer is scripted per call.
//
// 🔴 It counts its calls. Several of the assertions below are about how many times something happened —
// "no provider call was made", "the step was entered until the ceiling" — and a double that could not
// count would let those tests assert on the ANSWER instead, which proves nothing: two runs agreeing is
// what a deterministic model does too.
type fakeReader struct {
	readings  []SurfaceReading
	errs      []error
	calls     int
	unmounted map[Intent]bool
}

func (f *fakeReader) Read(_ context.Context, _, _ string, _ IntentSpec) (SurfaceReading, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return SurfaceReading{}, f.errs[i]
	}
	if i < len(f.readings) {
		return f.readings[i], nil
	}
	if len(f.readings) > 0 {
		return f.readings[len(f.readings)-1], nil
	}
	return SurfaceReading{Claim: "c", EvidenceRef: "ev_1", State: FindingMeasured}, nil
}

func (f *fakeReader) Mounted(spec IntentSpec) bool { return !f.unmounted[spec.Intent] }

type fixedBudget struct{ env BudgetEnvelope }

func (f fixedBudget) Envelope(context.Context, string) (BudgetEnvelope, error) { return f.env, nil }

// fakePins returns a fixed pin. The thing FR11 promises never happens on a replay — a read of the
// surface — is counted by `fakeReader`, not here, which is why this double is as thin as it looks.
type fakePins struct{ pin Pin }

func (f fakePins) Resolve(context.Context, string, string, IntentSpec) (Pin, error) {
	return f.pin, nil
}

type fixedRouter struct{ routing Routing }

func (f fixedRouter) Route(string) Routing { return f.routing }

// harness assembles a runner over a real Store and a real Emitter. 🔴 A REAL store and a REAL emitter:
// the properties under test are "the emitter refused it" and "the run holds the state", and a harness
// that stubbed either would be testing a simplified re-implementation of the thing that ships.
type harness struct {
	store   *Store
	clock   *clock
	reader  *fakeReader
	runner  *Runner
	emitter *Emitter
	owner   Owner
}

func newHarness(t *testing.T, routing Routing, reader *fakeReader, env BudgetEnvelope, pins PinResolver) *harness {
	t.Helper()
	c := newClock()
	store := NewStore(c.now)
	if _, err := store.Create("conv_1", alice, "wf_1", "run_1"); err != nil {
		t.Fatal(err)
	}
	em := &Emitter{
		ConversationID: "conv_1", TurnID: "turn_1", TenantID: alice.TenantID,
		TraceID: "trace_1", RequestID: "req_1", SpanID: "span_1",
		Provenance: ProvenanceGenerated,
		Resolvers:  Resolvers{},
		Sink:       store, Log: quietLogger(), Now: c.now,
	}
	return &harness{
		store: store, clock: c, reader: reader, emitter: em, owner: alice,
		runner: &Runner{
			Store: store, Router: fixedRouter{routing}, Reader: reader,
			Pins: pins, Budgets: fixedBudget{env}, Now: c.now,
		},
	}
}

func (h *harness) run(t *testing.T, question string) harnessruntime.StopReason {
	t.Helper()
	stop, err := h.runner.Run(context.Background(), TurnRequest{
		ConversationID: "conv_1", Owner: h.owner, WorkflowID: "wf_1",
		TurnID: "turn_1", TraceID: "trace_1", Question: question, Emitter: h.emitter,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return stop
}

func (h *harness) messages(t *testing.T) []Message {
	t.Helper()
	msgs, err := h.store.Messages("conv_1", h.owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func (h *harness) first(t *testing.T, k Kind) Message {
	t.Helper()
	for _, m := range h.messages(t) {
		if m.Kind == k {
			return m
		}
	}
	t.Fatalf("no %s message was emitted; kinds were %v", k, kindsOf(h.messages(t)))
	return Message{}
}

func kindsOf(ms []Message) []Kind {
	out := make([]Kind, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Kind)
	}
	return out
}

func routed(i Intent) Routing {
	return Routing{Intent: i, Confidence: 0.9, ClaimsRepository: true}
}

// ── the happy path, and the shape of a turn ──────────────────────────────────────────────────────

func TestATurnEmitsAPlanFirstAndAResultLast(t *testing.T) {
	h := newHarness(t, routed(IntentMemory), &fakeReader{}, fullEnvelope(), nil)
	stop := h.run(t, "what does this node remember between calls?")
	if stop != harnessruntime.StopSatisfied {
		t.Errorf("stop = %q, want satisfied", stop)
	}
	msgs := h.messages(t)
	if len(msgs) == 0 {
		t.Fatal("the turn emitted nothing")
	}
	if msgs[0].Kind != KindPlan {
		t.Errorf("the first message is %q; the plan is the DENOMINATOR and must arrive before any "+
			"step runs, or 'I looked at your repository' cannot be short", msgs[0].Kind)
	}
	last := msgs[len(msgs)-1]
	if last.Kind != KindResult {
		t.Errorf("the last message is %q, want result", last.Kind)
	}
	// 🔴 Task 4.13: a run that finished normally SAYS so. `satisfied` is a stated reason, not the
	// absence of a limit.
	if last.Result.StopReason != harnessruntime.StopSatisfied || last.Result.StoppedOnLimit {
		t.Errorf("terminal stop = %q, on_limit=%v", last.Result.StopReason, last.Result.StoppedOnLimit)
	}
}

func TestEveryPlannedStepIsReconciled(t *testing.T) {
	h := newHarness(t, routed(IntentMemory), &fakeReader{}, fullEnvelope(), nil)
	h.run(t, "what does this node remember between calls?")
	plan := h.first(t, KindPlan)
	result := h.first(t, KindResult)
	if len(result.Result.Reconciliation) != len(plan.Plan.Steps) {
		t.Fatalf("%d reconciliation entries for %d planned steps",
			len(result.Result.Reconciliation), len(plan.Plan.Steps))
	}
	declared := map[string]bool{}
	for _, s := range plan.Plan.Steps {
		declared[s.ID] = true
	}
	for _, e := range result.Result.Reconciliation {
		if !declared[e.StepID] {
			t.Errorf("the result reconciles %q, which the plan never declared", e.StepID)
		}
	}
}

func TestProgressCarriesThePhaseAndTheRemainingBudget(t *testing.T) {
	h := newHarness(t, routed(IntentMemory), &fakeReader{}, fullEnvelope(), nil)
	h.run(t, "what does this node remember between calls?")
	sawAct, sawVerify := false, false
	for _, m := range h.messages(t) {
		if m.Kind != KindProgress {
			continue
		}
		if !m.Progress.Phase.Valid() {
			t.Fatalf("a progress message carries no valid phase: %q", m.Progress.Phase)
		}
		switch m.Progress.Phase {
		case PhaseAct:
			sawAct = true
		case PhaseVerify:
			sawVerify = true
		}
		// The four facts a spinner withholds (PRD §9.1) — the remaining budget is one of them, on
		// every progress message rather than only at the end.
		if m.Progress.Remaining.Tokens <= 0 && m.Progress.Remaining.WallClockSeconds <= 0 {
			t.Error("a progress message carries no remaining budget")
		}
	}
	if !sawAct || !sawVerify {
		t.Errorf("phases observed: act=%v verify=%v; a turn that never observably verifies has no "+
			"place for the reconciliation to come from", sawAct, sawVerify)
	}
}

// ── the failure classes stay distinguishable ─────────────────────────────────────────────────────

func TestAnUnmountedCapabilityRefusesWithTheNotMountedClass(t *testing.T) {
	reader := &fakeReader{unmounted: map[Intent]bool{IntentAssess: true}}
	h := newHarness(t, routed(IntentAssess), reader, fullEnvelope(), nil)
	stop := h.run(t, "look at my repository and tell me what is weak")
	refusal := h.first(t, KindRefusal)
	if refusal.Refusal.Failure != FailureNotMounted {
		t.Errorf("failure class = %q, want %q.\nA 503 must stay a 503: 'not available in this "+
			"deployment' is a remedy; 'not found' sends somebody to check an identifier that is correct.",
			refusal.Refusal.Failure, FailureNotMounted)
	}
	for _, m := range h.messages(t) {
		if m.Kind == KindPlan {
			t.Error("a plan was emitted for an unmounted capability; a budget would have been spent")
		}
	}
	if stop.Limit() {
		t.Errorf("stop = %q; a refusal is not a limit", stop)
	}
}

func TestAnAbstentionNamesWhatTheSurfaceCanDoAndStartsNoRun(t *testing.T) {
	h := newHarness(t, Routing{Cause: "this question could be about more than one thing"},
		&fakeReader{}, fullEnvelope(), nil)
	h.run(t, "make it good")
	refusal := h.first(t, KindRefusal)
	if len(refusal.Refusal.CanDo) != len(Intents()) {
		t.Errorf("the refusal lists %d things this surface can do; the intent set has %d.\n"+
			"An open text box implies infinity, so the boundary has to be stated.",
			len(refusal.Refusal.CanDo), len(Intents()))
	}
	if h.reader.calls != 0 {
		t.Error("an abstention still read a surface; no run may be started")
	}
}

func TestATransportFailureReconcilesAsRefusedCarryingTheCause(t *testing.T) {
	cause := errors.New("the memory read model is unreachable")
	reader := &fakeReader{errs: []error{nil, cause, nil}}
	h := newHarness(t, routed(IntentMemory), reader, fullEnvelope(), nil)
	h.run(t, "what does this node remember between calls?")
	result := h.first(t, KindResult)
	found := false
	for _, e := range result.Result.Reconciliation {
		if e.State == StepRefused && contains(e.Reason, "unreachable") {
			found = true
		}
	}
	if !found {
		t.Errorf("no step reconciled as refused carrying the cause: %+v", result.Result.Reconciliation)
	}
}

// ── the budget, end to end ───────────────────────────────────────────────────────────────────────

// TestABudgetStopTerminatesWithoutRenderingAsComplete is task 6.12's subject at the turn level.
func TestABudgetStopTerminatesWithoutRenderingAsComplete(t *testing.T) {
	// A token budget that admits one step and refuses the rest.
	env := BudgetEnvelope{TurnCeiling: 9, TokenBudget: 2500, ToolCallCeiling: 9, WallClockSeconds: 600}
	h := newHarness(t, routed(IntentMemory), &fakeReader{}, env, nil)
	stop := h.run(t, "what does this node remember between calls?")
	if stop != harnessruntime.StopTokenBudget {
		t.Fatalf("stop = %q, want the token budget", stop)
	}
	result := h.first(t, KindResult)
	if !result.Result.StoppedOnLimit {
		t.Error("a budget-exhausted run reported that it did not stop on a limit; it would render as complete")
	}
	notMeasured := 0
	for _, e := range result.Result.Reconciliation {
		if e.State == StepNotMeasured {
			notMeasured++
			if e.Reason == "" {
				t.Error("a not_measured step names no reason")
			}
			if !contains(e.Reason, string(harnessruntime.StopTokenBudget)) {
				t.Errorf("the reason does not name the limit that stopped it: %q", e.Reason)
			}
		}
	}
	if notMeasured == 0 {
		t.Error("no step degraded to not_measured; the steps that did not run were silently dropped, " +
			"which is the shorter-answer-presented-as-the-whole-one failure FR18 forbids")
	}
	// Every planned step still has an entry: a shortfall is a RENDERED STATE, not an absence.
	plan := h.first(t, KindPlan)
	if len(result.Result.Reconciliation) != len(plan.Plan.Steps) {
		t.Errorf("%d entries for %d steps", len(result.Result.Reconciliation), len(plan.Plan.Steps))
	}
}

// TestALoopWhoseStopConditionNeverFiresTerminatesOnTheStepCeiling is task 6.16, at the turn level.
func TestALoopWhoseStopConditionNeverFiresTerminatesOnTheStepCeiling(t *testing.T) {
	// A reader that ALWAYS asks to be entered again. 🔴 Nothing in the loop decides to give up, which
	// is the point: the guard must not live in the thing that can go wrong.
	always := make([]error, 0, StepReEntryCeiling*4)
	for i := 0; i < StepReEntryCeiling*4; i++ {
		always = append(always, ErrStepIncomplete)
	}
	reader := &fakeReader{errs: always}
	// Generous on every other limit so the ONLY thing that can stop this is the re-entry ceiling.
	env := BudgetEnvelope{TurnCeiling: 1 << 20, TokenBudget: 1 << 30, ToolCallCeiling: 1 << 20, WallClockSeconds: 1 << 20}
	h := newHarness(t, routed(IntentMemory), reader, env, nil)

	done := make(chan harnessruntime.StopReason, 1)
	go func() { done <- h.run(t, "what does this node remember between calls?") }()
	select {
	case stop := <-done:
		if stop != harnessruntime.StopCeiling {
			t.Fatalf("stop = %q, want %q", stop, harnessruntime.StopCeiling)
		}
		result := h.first(t, KindResult)
		if result.Result.StoppedAtStep == "" {
			t.Error("the terminal message does not name the step the ceiling fired on")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not terminate; an infinite loop is the default failure of a loop whose " +
			"stop condition depends on model output")
	}
}

// ── determinism: the pin (FR11, task 2.8) ────────────────────────────────────────────────────────

func TestAPinnedInferenceIsReplayedWithoutReadingTheSurface(t *testing.T) {
	reader := &fakeReader{}
	pins := fakePins{pin: Pin{
		Found: true, SourceRevision: "abc123", CurrentRevision: "abc123",
		Reading: SurfaceReading{Claim: "the extraction node keeps no memory",
			EvidenceRef: "ev_pinned", State: FindingMeasured},
	}}
	h := newHarness(t, routed(IntentMemory), reader, fullEnvelope(), pins)
	h.run(t, "what does this node remember between calls?")

	// 🔴 THE ASSERTION IS THAT NOTHING WAS CALLED, not that the answers matched. Two answers matching
	// is what a deterministic model does too, so matching proves nothing about the pin.
	if reader.calls != 0 {
		t.Errorf("the surface was read %d times on a pinned replay; FR11's guarantee is that a "+
			"repeated question costs nothing", reader.calls)
	}
	for _, m := range h.messages(t) {
		if m.Provenance != ProvenancePinned {
			t.Errorf("a message on a pinned turn carries provenance %q; without the label the "+
				"determinism guarantee is unfalsifiable and therefore a claim rather than a guarantee",
				m.Provenance)
			break
		}
	}
}

func TestAPinTakenAtAnOlderRevisionIsLabelledStaleAndNamesTheRevision(t *testing.T) {
	pins := fakePins{pin: Pin{
		Found: true, SourceRevision: "abc123", CurrentRevision: "def456",
		Reading: SurfaceReading{Claim: "the extraction node keeps no memory",
			EvidenceRef: "ev_pinned", State: FindingMeasured},
	}}
	h := newHarness(t, routed(IntentMemory), &fakeReader{}, fullEnvelope(), pins)
	h.run(t, "what does this node remember between calls?")
	f := h.first(t, KindFinding)
	if f.Finding.State != FindingStale {
		t.Errorf("state = %q, want stale", f.Finding.State)
	}
	if f.Finding.SourceRevision != "abc123" {
		t.Errorf("source revision = %q, want the revision the claim actually describes; 'stale' with "+
			"no revision is a warning about nothing", f.Finding.SourceRevision)
	}
}

// ── FR3, at the turn level ───────────────────────────────────────────────────────────────────────

func TestARepositoryQuestionMarksTheTurnSoProseIsInadmissible(t *testing.T) {
	h := newHarness(t, routed(IntentMemory), &fakeReader{}, fullEnvelope(), nil)
	h.run(t, "what does this node remember between calls?")
	if !h.emitter.ClaimsRepository {
		t.Fatal("the turn was not marked as claiming a repository property; prose would be admissible " +
			"on a question about the customer's code")
	}
	if _, err := h.emitter.Emit(context.Background(), Message{Kind: KindAnswer,
		Answer: &AnswerPayload{Text: "it all looks fine"}}); err == nil {
		t.Fatal("prose was accepted on a repository turn")
	}
}
