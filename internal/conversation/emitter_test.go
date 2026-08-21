package conversation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// ── test doubles ─────────────────────────────────────────────────────────────────────────────────

// resolver is an ArtifactResolver over a fixed set of references.
//
// 🔴 It resolves by EXACT MEMBERSHIP and refuses anything else. A double that accepted "anything
// well-formed" would make every fence over D7 pass while proving nothing: the whole point is that a
// model can produce a well-formed identifier and still not produce a ledger row.
type resolver struct {
	known map[string]bool
	err   error
	calls int
}

func resolves(refs ...string) *resolver {
	m := map[string]bool{}
	for _, r := range refs {
		m[r] = true
	}
	return &resolver{known: m}
}

func (r *resolver) Resolve(tenantID, ref string) (bool, error) {
	r.calls++
	if r.err != nil {
		return false, r.err
	}
	return r.known[ref], nil
}

// recorder is a Sink that keeps what it was given.
type recorder struct {
	msgs []Message
	next int64
}

func (r *recorder) Append(m Message) (Message, error) {
	r.next++
	m.ID = r.next
	r.msgs = append(r.msgs, m)
	return m, nil
}

// quietLogger keeps a refusal's WARN out of the test output while still exercising the log path — the
// path is where `request_id` / `trace_id` / `span_id` are attached, so it must not be skipped.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newEmitter(sink Sink, res Resolvers) *Emitter {
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	return &Emitter{
		ConversationID: "conv_1", TurnID: "turn_1", TenantID: "tenant-a",
		TraceID: "trace_1", RequestID: "req_1", SpanID: "span_1",
		Provenance: ProvenanceGenerated,
		Resolvers:  res, Sink: sink, Log: quietLogger(),
		Now: func() time.Time { return at },
	}
}

func goodPlan() *PlanPayload {
	return &PlanPayload{
		Intent: IntentMemory,
		Steps:  []PlanStep{{ID: "s1", Title: "read memory", Surface: "/app/memory"}},
		Budget: BudgetEnvelope{TurnCeiling: 4, TokenBudget: 10000, ToolCallCeiling: 8, WallClockSeconds: 120},
	}
}

// ── task 2.5 · a finding with no evidence ────────────────────────────────────────────────────────

func TestFindingWithNoEvidenceIsRefusedBeforeTheTransport(t *testing.T) {
	sink := &recorder{}
	em := newEmitter(sink, Resolvers{})
	_, err := em.Emit(context.Background(), Message{Kind: KindFinding, Finding: &FindingPayload{
		Surface: "memory", Claim: "this node forgets between calls", State: FindingMeasured,
		// EvidenceRef deliberately empty.
	}})
	if err == nil {
		t.Fatal("a finding with no evidence reference was accepted; the claim would be on the screen " +
			"with nothing behind it, which is the one thing this surface exists to prevent")
	}
	var refused ErrRefused
	if !errors.As(err, &refused) || refused.Kind != KindFinding {
		t.Fatalf("err = %v; want an ErrRefused naming the finding", err)
	}
	// 🔴 "Before the transport" is the requirement, so the sink must be untouched — not merely the
	// return value non-nil. A version that appended and then errored would pass a weaker assertion.
	if len(sink.msgs) != 0 {
		t.Errorf("the sink received %d messages; a refused finding must never reach it", len(sink.msgs))
	}
}

func TestFindingStatesEachRequireTheirOwnField(t *testing.T) {
	cases := map[string]struct {
		payload *FindingPayload
		wantErr bool
	}{
		"not_measured with no missing input": {&FindingPayload{
			Surface: "coverage", Claim: "c", EvidenceRef: "e", State: FindingNotMeasured}, true},
		"not_measured naming the input": {&FindingPayload{
			Surface: "coverage", Claim: "c", EvidenceRef: "e", State: FindingNotMeasured,
			MissingInput: "no eval set is bound to this workflow"}, false},
		"refused with no cause": {&FindingPayload{
			Surface: "memory", Claim: "c", EvidenceRef: "e", State: FindingRefused}, true},
		"refused carrying the cause": {&FindingPayload{
			Surface: "memory", Claim: "c", EvidenceRef: "e", State: FindingRefused,
			Cause: "ErrUnsafeRewrite: the node's memory strategy is expressed in a call this build cannot rewrite"}, false},
		"stale with no revision": {&FindingPayload{
			Surface: "memory", Claim: "c", EvidenceRef: "e", State: FindingStale}, true},
		"stale naming the revision": {&FindingPayload{
			Surface: "memory", Claim: "c", EvidenceRef: "e", State: FindingStale,
			SourceRevision: "9f2c1ab"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			em := newEmitter(&recorder{}, Resolvers{})
			_, err := em.Emit(context.Background(), Message{Kind: KindFinding, Finding: tc.payload})
			if tc.wantErr && err == nil {
				t.Fatal("accepted; a state that names nothing is the omission problem with a label on it")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("refused a well-formed finding: %v", err)
			}
		})
	}
}

// ── task 2.6 · a proposal whose id does not resolve ──────────────────────────────────────────────

func TestProposalWithAnUnresolvableIDIsRefused(t *testing.T) {
	ledger := resolves("prop_real")
	sink := &recorder{}
	em := newEmitter(sink, Resolvers{Proposal: ledger})

	// 🔴 A WELL-FORMED identifier that does not exist. This is the adversarial shape: a compromised
	// model can write something that looks exactly like a proposal id, and the defence must not depend
	// on the id looking wrong.
	_, err := em.Emit(context.Background(), Message{Kind: KindProposal, Proposal: &ProposalPayload{
		ProposalID: "prop_9c4f1a2e8b7d", Axis: "memory", Node: "extract",
	}})
	if err == nil {
		t.Fatal("a proposal referencing a non-existent ledger row was accepted")
	}
	var missing ErrArtifactMissing
	if !errors.As(err, &missing) || missing.Artifact != ArtifactProposalID {
		t.Fatalf("err = %v; want ErrArtifactMissing naming proposal_id", err)
	}
	if len(sink.msgs) != 0 {
		t.Errorf("the sink received %d messages; nothing unresolvable may reach it", len(sink.msgs))
	}
	if ledger.calls == 0 {
		t.Error("the ledger was never consulted; the check would pass for any id")
	}

	// The same message with a REAL id goes through, which is what makes the refusal above a check
	// rather than a blanket refusal of proposals.
	if _, err := em.Emit(context.Background(), Message{Kind: KindProposal, Proposal: &ProposalPayload{
		ProposalID: "prop_real", Axis: "memory", Node: "extract",
	}}); err != nil {
		t.Fatalf("a proposal with a resolvable id was refused: %v", err)
	}
}

func TestAnUnreachableLedgerRefusesRatherThanAdmits(t *testing.T) {
	broken := resolves()
	broken.err = errors.New("the verification ledger is unreachable")
	em := newEmitter(&recorder{}, Resolvers{Proposal: broken})
	_, err := em.Emit(context.Background(), Message{Kind: KindProposal,
		Proposal: &ProposalPayload{ProposalID: "prop_real"}})
	if err == nil {
		t.Fatal("a store outage let a proposal through; an outage must not be the moment the control " +
			"is switched off")
	}
}

func TestADeploymentWithNoResolverCannotEmitTheKind(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{}) // no Proposal resolver at all
	_, err := em.Emit(context.Background(), Message{Kind: KindProposal,
		Proposal: &ProposalPayload{ProposalID: "prop_real"}})
	var noResolver ErrNoResolver
	if !errors.As(err, &noResolver) {
		t.Fatalf("err = %v; want ErrNoResolver. A missing resolver that let the message through would "+
			"turn a wiring mistake into a silently disabled security control", err)
	}
}

// ── task 2.7 · a result whose delivery record does not exist ─────────────────────────────────────

func TestResultWithAnUnresolvableDeliveryRecordIsRefused(t *testing.T) {
	sink := &recorder{}
	em := newEmitter(sink, Resolvers{Delivery: resolves("del_real")})
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &ResultPayload{
		RunID: "run_1", StopReason: harnessruntime.StopSatisfied,
		Reconciliation: []ReconciliationEntry{{StepID: "s1", State: StepDone}},
		DeliveryRef:    "del_invented",
	}})
	if err == nil {
		t.Fatal("a result reported a delivery nobody recorded")
	}
	if got := len(sink.msgs); got != 1 {
		t.Errorf("the sink holds %d messages; only the plan should have landed", got)
	}
}

func TestAPullRequestURLCannotRideWithoutADeliveryRecord(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{Delivery: resolves("del_real")})
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &ResultPayload{
		RunID: "run_1", StopReason: harnessruntime.StopSatisfied,
		Reconciliation: []ReconciliationEntry{{StepID: "s1", State: StepDone}},
		PullRequestURL: "https://github.com/nousresearch/hermes-agent/pull/9999",
		VerifiedClaim:  false,
	}})
	if err == nil {
		t.Fatal("a composed pull-request URL was accepted; it would 404 in a customer's browser while " +
			"looking exactly like one that works")
	}
}

// ── task 2.12 · progress needs a phase, plan needs four limits ───────────────────────────────────

func TestProgressWithNoPhaseIsRefused(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{})
	_, err := em.Emit(context.Background(), Message{Kind: KindProgress, Progress: &ProgressPayload{
		Detail: "working", ElapsedMS: 1200,
	}})
	if err == nil {
		t.Fatal("a progress message with no phase was accepted; a turn that cannot say where it is is " +
			"indistinguishable from a hung one")
	}
}

func TestPlanMissingAnyLimitIsRefused(t *testing.T) {
	full := BudgetEnvelope{TurnCeiling: 4, TokenBudget: 10000, ToolCallCeiling: 8, WallClockSeconds: 120}
	// One subtest per limit: a test that removed all four at once would pass against an implementation
	// that only checked one of them.
	mutations := map[string]func(*BudgetEnvelope){
		"turn ceiling":      func(b *BudgetEnvelope) { b.TurnCeiling = 0 },
		"token budget":      func(b *BudgetEnvelope) { b.TokenBudget = 0 },
		"tool-call ceiling": func(b *BudgetEnvelope) { b.ToolCallCeiling = 0 },
		"wall clock":        func(b *BudgetEnvelope) { b.WallClockSeconds = 0 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			plan := goodPlan()
			plan.Budget = full
			mutate(&plan.Budget)
			em := newEmitter(&recorder{}, Resolvers{})
			if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: plan}); err == nil {
				t.Fatalf("a plan with no %s was accepted; the user would discover that ceiling by "+
					"hitting it, at which point 'I stopped' is indistinguishable from a bug", name)
			}
		})
	}
	// And the complete one is accepted, so the four assertions above are checks rather than a blanket
	// refusal of plans.
	em := newEmitter(&recorder{}, Resolvers{})
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
		t.Fatalf("a complete plan was refused: %v", err)
	}
}

// ── task 2.13 · a result reconciles every declared step ──────────────────────────────────────────

func TestResultMissingAReconciliationEntryIsRefused(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{})
	plan := goodPlan()
	plan.Steps = []PlanStep{{ID: "s1", Title: "one"}, {ID: "s2", Title: "two"}, {ID: "s3", Title: "three"}}
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: plan}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &ResultPayload{
		RunID: "run_1", StopReason: harnessruntime.StopSatisfied,
		Reconciliation: []ReconciliationEntry{
			{StepID: "s1", State: StepDone},
			{StepID: "s2", State: StepDone},
			// s3 missing — the shape an agent that quietly did two of three steps produces.
		},
	}})
	if err == nil {
		t.Fatal("a result left a planned step unreconciled; without the denominator, prose from a " +
			"partial run reads exactly like prose from a complete one")
	}
	if !contains(err.Error(), "s3") {
		t.Errorf("the refusal does not name the unreconciled step: %v", err)
	}
}

func TestEveryNonDoneStepMustNameAReason(t *testing.T) {
	for _, state := range []StepState{StepSkipped, StepRefused, StepNotMeasured} {
		t.Run(string(state), func(t *testing.T) {
			em := newEmitter(&recorder{}, Resolvers{})
			if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
				t.Fatalf("plan: %v", err)
			}
			_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &ResultPayload{
				RunID: "run_1", StopReason: harnessruntime.StopSatisfied,
				Reconciliation: []ReconciliationEntry{{StepID: "s1", State: state}},
			}})
			if err == nil {
				t.Fatalf("%q with no reason was accepted; %q alone is the omission problem with a "+
					"label on it", state, state)
			}
		})
	}
}

func TestAResultWithNoPlanBeforeItIsRefused(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{})
	_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &ResultPayload{
		RunID: "run_1", StopReason: harnessruntime.StopSatisfied,
	}})
	if err == nil {
		t.Fatal("a result was emitted with no plan behind it; its reconciliation would be vacuously " +
			"complete, which is a green fence over nothing")
	}
}

// ── task 2.14 · a result asserting verification cites a verdict that resolves ────────────────────

func TestResultAssertingVerificationWithAnUnresolvableVerdictIsRefused(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{Verdict: resolves("verdict_real")})
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	base := func() *ResultPayload {
		return &ResultPayload{
			RunID: "run_1", StopReason: harnessruntime.StopSatisfied,
			Reconciliation: []ReconciliationEntry{{StepID: "s1", State: StepDone}},
			VerifiedClaim:  true,
		}
	}
	t.Run("no verdict cited", func(t *testing.T) {
		if _, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: base()}); err == nil {
			t.Fatal("a result claimed verification and cited nothing")
		}
	})
	t.Run("verdict that does not resolve", func(t *testing.T) {
		r := base()
		r.VerdictRef = "verdict_9f2c" // well-formed, non-existent
		if _, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: r}); err == nil {
			t.Fatal("a result cited a verdict nobody recorded")
		}
	})
	t.Run("verdict that resolves", func(t *testing.T) {
		r := base()
		r.VerdictRef = "verdict_real"
		if _, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: r}); err != nil {
			t.Fatalf("a result citing a real verdict was refused: %v", err)
		}
	})
}

func TestAResultCannotDisagreeWithItsOwnStopReason(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{})
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &ResultPayload{
		RunID: "run_1", StopReason: harnessruntime.StopTokenBudget,
		StoppedOnLimit: false, // the lie: a budget exhaustion presented as a completion
		Reconciliation: []ReconciliationEntry{{StepID: "s1", State: StepNotMeasured, Reason: "budget"}},
	}})
	if err == nil {
		t.Fatal("a result claimed it did not stop on a limit while naming one")
	}
}

// ── FR3 · prose may not assert a repository property ─────────────────────────────────────────────

func TestAnAnswerIsRefusedOnARepositoryQuestion(t *testing.T) {
	sink := &recorder{}
	em := newEmitter(sink, Resolvers{})
	em.ClaimsRepository = true
	_, err := em.Emit(context.Background(), Message{Kind: KindAnswer, Answer: &AnswerPayload{
		// Deliberately innocuous prose. The point is that the refusal does NOT depend on what the text
		// says: the route decided this turn is about a repository, so prose is inadmissible whatever
		// it contains.
		Text: "Your extraction node looks fine to me.",
	}})
	if err == nil {
		t.Fatal("prose asserting a repository property was accepted; it would be a claim with no " +
			"evidence requirement, which is FR2 with a different message kind on it")
	}
	if len(sink.msgs) != 0 {
		t.Error("the refused answer reached the sink")
	}

	// The same emitter on a capability question accepts it.
	em.ClaimsRepository = false
	if _, err := em.Emit(context.Background(), Message{Kind: KindAnswer, Answer: &AnswerPayload{
		Text: "A context strategy decides what conversation history a node receives.", Topic: "context",
	}}); err != nil {
		t.Fatalf("a capability answer was refused: %v", err)
	}
}

// ── the union invariant ──────────────────────────────────────────────────────────────────────────

func TestAMessageCarriesExactlyOnePayloadAndItMatchesTheKind(t *testing.T) {
	em := newEmitter(&recorder{}, Resolvers{})
	t.Run("two payloads", func(t *testing.T) {
		_, err := em.Emit(context.Background(), Message{Kind: KindAnswer,
			Answer: &AnswerPayload{Text: "hello"}, Progress: &ProgressPayload{Phase: PhaseAct}})
		if err == nil {
			t.Fatal("two payloads were accepted; the browser would render whichever arm it checked first")
		}
	})
	t.Run("no payload", func(t *testing.T) {
		if _, err := em.Emit(context.Background(), Message{Kind: KindAnswer}); err == nil {
			t.Fatal("no payload was accepted; it would render as a blank card")
		}
	})
	t.Run("mismatched payload", func(t *testing.T) {
		_, err := em.Emit(context.Background(), Message{Kind: KindFinding,
			Answer: &AnswerPayload{Text: "hello"}})
		if err == nil {
			t.Fatal("a payload that is not the one the kind names was accepted")
		}
	})
	t.Run("unknown kind", func(t *testing.T) {
		if _, err := em.Emit(context.Background(), Message{Kind: "chat"}); err == nil {
			t.Fatal("a kind outside the vocabulary was accepted")
		}
	})
	t.Run("no provenance", func(t *testing.T) {
		bare := newEmitter(&recorder{}, Resolvers{})
		bare.Provenance = ""
		if _, err := bare.Emit(context.Background(), Message{Kind: KindAnswer,
			Answer: &AnswerPayload{Text: "hello"}}); err == nil {
			t.Fatal("a message with no provenance was accepted")
		}
	})
}

func TestTheEmitterStampsTheTurnsIdentityOntoEveryMessage(t *testing.T) {
	sink := &recorder{}
	em := newEmitter(sink, Resolvers{})
	// A caller trying to set these itself must not win: they are facts about the TURN.
	if _, err := em.Emit(context.Background(), Message{
		Kind: KindAnswer, Answer: &AnswerPayload{Text: "hello"},
		ConversationID: "somebody-elses", TurnID: "forged", TraceID: "forged",
		Provenance: ProvenancePinned,
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := sink.msgs[0]
	if got.ConversationID != "conv_1" || got.TurnID != "turn_1" || got.TraceID != "trace_1" {
		t.Errorf("a caller-supplied identity survived: %+v", got)
	}
	if got.Provenance != ProvenanceGenerated {
		t.Errorf("provenance = %q; a caller set it, and a per-message provenance eventually says "+
			"'pinned' over generated output", got.Provenance)
	}
	if got.At.IsZero() {
		t.Error("the message carries no timestamp")
	}
}
