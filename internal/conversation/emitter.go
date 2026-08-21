package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// emitter.go is where a message either becomes true or does not exist.
//
// # Why every check is HERE and not at the call sites
//
// Because the property being defended is *"nothing that reaches the transport is unsupported"*, and a
// property enforced at N call sites is a property that holds at N-1 of them within a quarter. One door,
// and the fences in §6 mutate the checks behind that one door and assert the tests go red.
//
// # 🔴 Before the transport, not before rendering
//
// FR2 says a `finding` with no evidence reference is refused SERVER-SIDE and never transmitted. The
// distinction matters because the alternative that always gets built is a client that renders such a
// finding as prose — at which point the claim is on the screen, the evidence is not, and the rule has
// been enforced everywhere except where it counts.
//
// # What a refusal does, besides refuse
//
// It returns an error, it emits `console.conversation.refused` from the central enum, and it logs a WARN
// carrying `request_id` / `trace_id` / `span_id` (task 2.10). 🚫 It never falls back to a weaker message:
// a refused `finding` does not become an `answer`, because that is the exact substitution — a claim
// about a repository re-expressed as prose — that FR3 exists to make impossible.

// Sink is where an accepted message goes. Implemented by the store, which assigns the monotonic id.
//
// 🔴 The id is assigned by the SINK and not by the emitter, because the id is the acknowledgement
// cursor: two turns emitting concurrently on one conversation must not both mint "17".
type Sink interface {
	Append(m Message) (Message, error)
}

// Resolvers are the artifact resolutions D7 requires. A nil resolver for an effect-bearing kind means
// this deployment cannot emit that kind — it FAILS rather than skipping the check, because a wiring
// mistake that silently disabled a security control is the worst outcome available here.
type Resolvers struct {
	// Proposal resolves a `proposal_id` in the verification ledger.
	Proposal ArtifactResolver
	// Delivery resolves a delivery record.
	Delivery ArtifactResolver
	// Verdict resolves a verification record (FR20).
	Verdict ArtifactResolver
	// Entitlement resolves the approval gate's decision for an `approval_request`.
	Entitlement ArtifactResolver
}

// Emitter emits one turn's messages. One per turn, never shared across turns: the per-turn facts below
// are what make provenance and prose-admissibility structural rather than per-call arguments a caller
// could get wrong.
type Emitter struct {
	// Identity of the turn.
	ConversationID string
	TurnID         string
	TenantID       string
	TraceID        string
	// RequestID is the inbound HTTP request's id, carried onto every WARN/ERROR (task 2.10).
	RequestID string
	// SpanID is this turn's span, carried onto every WARN/ERROR (task 2.10).
	SpanID string

	// Provenance is set ONCE, when the turn decides between replaying a pin and generating (FR13).
	// 🚫 Not a per-message argument: a field a caller sets per message is a field that will eventually
	// say `pinned` over generated output.
	Provenance Provenance

	// ClaimsRepository records what the `understand` phase concluded about the QUESTION: does answering
	// it require asserting a property of the customer's repository?
	//
	// 🔴 This is what makes FR3 structural. The alternative — classify the PROSE and refuse it if it
	// looks like a repository claim — makes the boundary exactly as strong as a classifier over
	// "everything a model might write about code", which is a class no classifier is reliable over.
	// Keying it to the ROUTE instead means a turn about a repository cannot emit prose at all, whatever
	// the prose says.
	ClaimsRepository bool

	// Plan is the plan this turn declared, set by Emit when the `plan` message is accepted. The
	// denominator every `result` reconciles against (FR19).
	Plan *PlanPayload

	Resolvers Resolvers
	Sink      Sink
	Log       *slog.Logger
	// Now is the injected clock. Nothing here reads time.Now directly.
	Now func() time.Time
	// Observe records an event from the central enum. Optional; nil is a no-op, so a caller that has
	// nowhere to send events is not forced to invent a sink.
	Observe func(eventname.Name, map[string]any)
}

// ErrRefused is what the emitter returns when a message may not be transmitted. It names the KIND and
// the RULE, never the payload — see ErrArtifactMissing for why an unresolvable reference is not echoed.
type ErrRefused struct {
	Kind Kind
	Rule string
}

func (e ErrRefused) Error() string {
	return fmt.Sprintf("conversation: a %s was refused before the transport: %s", e.Kind, e.Rule)
}

// Emit validates a message and, if it survives, appends it to the sink.
//
// The caller supplies `Kind` and exactly one payload. Everything else — id, conversation, turn, trace,
// timestamp, provenance — is filled in HERE, because every one of them is a fact about the turn rather
// than about the message, and a caller able to set them is a caller able to get them wrong.
func (e *Emitter) Emit(ctx context.Context, m Message) (Message, error) {
	m.ConversationID = e.ConversationID
	m.TurnID = e.TurnID
	m.TraceID = e.TraceID
	m.Provenance = e.Provenance
	if e.Now != nil {
		m.At = e.Now().UTC()
	}

	if err := e.validate(m); err != nil {
		e.refused(ctx, m.Kind, err)
		return Message{}, err
	}
	if err := e.resolveArtifact(m); err != nil {
		e.refused(ctx, m.Kind, err)
		return Message{}, err
	}

	if e.Sink == nil {
		return Message{}, fmt.Errorf("conversation: the emitter has no sink; nothing would be delivered")
	}
	out, err := e.Sink.Append(m)
	if err != nil {
		return Message{}, err
	}
	if m.Kind == KindPlan {
		// Recorded AFTER acceptance: a refused plan is not a denominator.
		plan := *m.Plan
		e.Plan = &plan
	}
	return out, nil
}

// validate is every structural rule. Split from Emit so a fence can exercise it directly and so the
// mutation drills in §6 have one function to mutate.
func (e *Emitter) validate(m Message) error {
	if !m.Kind.Valid() {
		return ErrRefused{Kind: m.Kind, Rule: "the kind is outside the closed vocabulary"}
	}
	if !m.Provenance.Valid() {
		return ErrRefused{Kind: m.Kind, Rule: "the message records no provenance; a message whose origin " +
			"nobody recorded would claim to have been generated"}
	}
	if err := e.exactlyOnePayload(m); err != nil {
		return err
	}

	switch m.Kind {
	case KindPlan:
		return e.validatePlan(m.Plan)
	case KindProgress:
		return e.validateProgress(m.Progress)
	case KindFinding:
		return e.validateFinding(m.Finding)
	case KindProposal:
		if m.Proposal.ProposalID == "" {
			return ErrRefused{Kind: m.Kind, Rule: "a proposal carries no proposal_id"}
		}
	case KindApprovalRequest:
		return e.validateApprovalRequest(m.ApprovalRequest)
	case KindResult:
		return e.validateResult(m.Result)
	case KindRefusal:
		return e.validateRefusal(m.Refusal)
	case KindAnswer:
		return e.validateAnswer(m.Answer)
	}
	return nil
}

// exactlyOnePayload is the discriminated-union invariant, checked rather than trusted.
//
// A message with two payloads is not a typo — it is what a `switch` that fell through produces, and the
// browser would render whichever arm it checked first. A message with none renders as a blank card.
func (e *Emitter) exactlyOnePayload(m Message) error {
	present := map[Kind]bool{
		KindPlan:            m.Plan != nil,
		KindProgress:        m.Progress != nil,
		KindFinding:         m.Finding != nil,
		KindProposal:        m.Proposal != nil,
		KindApprovalRequest: m.ApprovalRequest != nil,
		KindResult:          m.Result != nil,
		KindRefusal:         m.Refusal != nil,
		KindAnswer:          m.Answer != nil,
	}
	n := 0
	for _, ok := range present {
		if ok {
			n++
		}
	}
	if n != 1 {
		return ErrRefused{Kind: m.Kind, Rule: fmt.Sprintf(
			"exactly one payload must be present; %d were", n)}
	}
	if !present[m.Kind] {
		return ErrRefused{Kind: m.Kind, Rule: "the payload present is not the one the kind names"}
	}
	return nil
}

// validatePlan is task 2.12's second half: all four limits, or the run does not start.
func (e *Emitter) validatePlan(p *PlanPayload) error {
	if !p.Intent.Valid() {
		return ErrRefused{Kind: KindPlan, Rule: "the plan names no intent from the closed set"}
	}
	if len(p.Steps) == 0 {
		// A plan with no steps has no denominator, which makes FR19's reconciliation vacuous — the
		// "green fence over nothing" shape, one layer up.
		return ErrRefused{Kind: KindPlan, Rule: "a plan declares no steps; the result would have nothing to reconcile"}
	}
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.ID == "" {
			return ErrRefused{Kind: KindPlan, Rule: "a plan step has no id; nothing could reconcile it"}
		}
		if seen[s.ID] {
			return ErrRefused{Kind: KindPlan, Rule: fmt.Sprintf("a plan declares step %q twice", s.ID)}
		}
		seen[s.ID] = true
	}
	if !p.Budget.Complete() {
		return ErrRefused{Kind: KindPlan, Rule: fmt.Sprintf(
			"a plan must declare all four limits before the first step runs; missing %v", p.Budget.MissingLimits())}
	}
	return nil
}

// validateProgress is task 2.12's first half: a turn that cannot name its phase is a defect.
func (e *Emitter) validateProgress(p *ProgressPayload) error {
	if !p.Phase.Valid() {
		return ErrRefused{Kind: KindProgress, Rule: "a progress message names no phase; a turn that " +
			"cannot say where it is is indistinguishable from a hung one"}
	}
	return nil
}

// validateFinding is FR2 and the four states.
func (e *Emitter) validateFinding(f *FindingPayload) error {
	// 🔴 TASK 2.5. The single most important line in this file: a claim about a customer's repository
	// with nothing behind it is exactly the "unverified LLM opinion in the result position" the whole
	// surface exists to prevent, and prose is not falsifiable so nothing downstream could catch it.
	if f.EvidenceRef == "" {
		return ErrRefused{Kind: KindFinding, Rule: "a finding carries no evidence reference"}
	}
	if !f.State.Valid() {
		return ErrRefused{Kind: KindFinding, Rule: "a finding is in no known state"}
	}
	if f.Claim == "" {
		return ErrRefused{Kind: KindFinding, Rule: "a finding states no claim"}
	}
	if f.Surface == "" {
		return ErrRefused{Kind: KindFinding, Rule: "a finding names no surface, so nothing owns it"}
	}
	switch f.State {
	case FindingNotMeasured:
		if f.MissingInput == "" {
			// "Not measured" with no named input is the omission problem with a label on it — the
			// precise failure PRD §9.1 says this state exists to prevent.
			return ErrRefused{Kind: KindFinding, Rule: "a not_measured finding names no missing input"}
		}
	case FindingRefused:
		if f.Cause == "" {
			return ErrRefused{Kind: KindFinding, Rule: "a refused finding carries no cause from the lower layer"}
		}
	case FindingStale:
		if f.SourceRevision == "" {
			return ErrRefused{Kind: KindFinding, Rule: "a stale finding names no revision, so 'stale' describes nothing"}
		}
	}
	return nil
}

func (e *Emitter) validateApprovalRequest(a *ApprovalRequestPayload) error {
	if a.ApprovalID == "" {
		return ErrRefused{Kind: KindApprovalRequest, Rule: "an approval request carries no approval id"}
	}
	if a.Action == "" || a.BlastRadius == "" || a.Reversible == "" {
		// FR10: a person cannot consent to an action whose blast radius and reversibility they were
		// not told. A missing field here is a consent nobody gave.
		return ErrRefused{Kind: KindApprovalRequest, Rule: "an approval request must state its action, " +
			"its blast radius and what is reversible"}
	}
	if !a.Approvable && a.UnapprovableReason == "" {
		// FR9: it arrives already un-approvable CARRYING THE REASON. Without one the console renders a
		// card with no control and no explanation, which reads as a bug.
		return ErrRefused{Kind: KindApprovalRequest, Rule: "an un-approvable request names no reason"}
	}
	return nil
}

// validateResult is tasks 2.13 and 2.14, plus the stop-vocabulary rule.
func (e *Emitter) validateResult(r *ResultPayload) error {
	if !r.StopReason.Valid() {
		return ErrRefused{Kind: KindResult, Rule: "a result carries no stop reason from the harness " +
			"runtime's closed vocabulary"}
	}
	if r.StoppedOnLimit != r.StopReason.Limit() {
		// The server's own predicate, asserted against the server's own vocabulary. A result claiming
		// it did not stop on a limit while naming one is how a budget exhaustion renders as complete.
		return ErrRefused{Kind: KindResult, Rule: "a result's stopped_on_limit disagrees with its stop reason"}
	}
	// 🔴 TASK 2.13. Every step the plan declared, reconciled — no more and no fewer. An agent that ran
	// three of eight steps produces prose indistinguishable from one that ran eight; the plan is the
	// denominator and this is the check that makes the shortfall a rendered state.
	if e.Plan == nil {
		return ErrRefused{Kind: KindResult, Rule: "this turn emitted no plan, so nothing can be reconciled"}
	}
	declared := map[string]bool{}
	for _, s := range e.Plan.Steps {
		declared[s.ID] = false
	}
	for _, entry := range r.Reconciliation {
		done, known := declared[entry.StepID]
		if !known {
			return ErrRefused{Kind: KindResult, Rule: fmt.Sprintf(
				"a result reconciles step %q, which its plan never declared", entry.StepID)}
		}
		if done {
			return ErrRefused{Kind: KindResult, Rule: fmt.Sprintf(
				"a result reconciles step %q twice", entry.StepID)}
		}
		declared[entry.StepID] = true
		if !entry.State.Valid() {
			return ErrRefused{Kind: KindResult, Rule: fmt.Sprintf(
				"step %q resolved to no known state", entry.StepID)}
		}
		if entry.State.NeedsReason() && entry.Reason == "" {
			return ErrRefused{Kind: KindResult, Rule: fmt.Sprintf(
				"step %q is %s and names no reason", entry.StepID, entry.State)}
		}
	}
	for _, s := range e.Plan.Steps {
		if !declared[s.ID] {
			return ErrRefused{Kind: KindResult, Rule: fmt.Sprintf(
				"a result leaves step %q unreconciled", s.ID)}
		}
	}
	// 🔴 TASK 2.14. A result that says a change was verified must cite the record that says so. This
	// is *diagnosis proposes, verification decides* stated as a message rule; the resolution of the
	// reference happens in resolveArtifact, because it is a store read rather than a shape check.
	if r.VerifiedClaim && r.VerdictRef == "" {
		return ErrRefused{Kind: KindResult, Rule: "a result asserts verification and cites no verdict"}
	}
	// A pull-request URL may only ride a delivery record, never be composed.
	if r.PullRequestURL != "" && r.DeliveryRef == "" {
		return ErrRefused{Kind: KindResult, Rule: "a result carries a pull-request URL with no delivery record behind it"}
	}
	return nil
}

func (e *Emitter) validateRefusal(r *RefusalPayload) error {
	if r.Cause == "" {
		return ErrRefused{Kind: KindRefusal, Rule: "a refusal carries no cause; a refusal nobody can act on " +
			"is a dead end wearing a message's clothes"}
	}
	if r.Failure != "" && !r.Failure.Valid() {
		return ErrRefused{Kind: KindRefusal, Rule: "a refusal names a failure class outside the three"}
	}
	if !r.StopReason.Valid() {
		return ErrRefused{Kind: KindRefusal, Rule: "a refusal is terminal and names no stop reason"}
	}
	return nil
}

// validateAnswer is FR3, enforced structurally.
func (e *Emitter) validateAnswer(a *AnswerPayload) error {
	if e.ClaimsRepository {
		// 🔴 The route decided this, not a classifier over the prose. A question that requires
		// asserting a property of the repository cannot be answered with prose AT ALL — the assertion
		// is only expressible as a `finding`, which inherits FR2's evidence requirement.
		return ErrRefused{Kind: KindAnswer, Rule: "this turn answers a question about the customer's " +
			"repository; such an assertion is expressible only as a finding with an evidence reference"}
	}
	if a.Text == "" {
		return ErrRefused{Kind: KindAnswer, Rule: "an answer carries no text"}
	}
	return nil
}

// resolveArtifact is D7: an effect-bearing kind needs an artifact a model cannot mint.
//
// 🔴 Separate from validate on purpose. `validate` is SHAPE — it can be checked with no stores at all,
// and a compromised model can satisfy every one of its rules by writing well-formed text. This function
// is the one that reads a ledger, and it is the defence that does not depend on any classifier working.
func (e *Emitter) resolveArtifact(m Message) error {
	artifact, needed := EffectArtifact(m.Kind)
	if !needed {
		return nil
	}
	var (
		resolver ArtifactResolver
		ref      string
	)
	switch m.Kind {
	case KindProposal:
		resolver, ref = e.Resolvers.Proposal, m.Proposal.ProposalID
	case KindApprovalRequest:
		resolver, ref = e.Resolvers.Entitlement, m.ApprovalRequest.ApprovalID
	case KindResult:
		resolver, ref = e.Resolvers.Delivery, m.Result.DeliveryRef
		if ref == "" {
			// A result that delivered nothing needs no delivery record. The requirement is that a
			// result REPORTING a delivery references one that exists — not that every turn delivers.
			resolver = nil
		}
	}
	if ref != "" || resolver != nil {
		if resolver == nil {
			return ErrNoResolver{Kind: m.Kind, Artifact: artifact}
		}
		ok, err := resolver.Resolve(e.TenantID, ref)
		if err != nil {
			// 🔴 A store failure REFUSES too. The alternative — let it through when the ledger is
			// unreachable — makes an outage the moment the security control is off.
			return fmt.Errorf("conversation: could not resolve the %s a %s requires: %w", artifact, m.Kind, err)
		}
		if !ok {
			return ErrArtifactMissing{Kind: m.Kind, Artifact: artifact}
		}
	}
	// A verified claim's verdict is a SECOND artifact on the same message, so it is resolved here
	// rather than folded into the table: `result` requires a delivery record when it delivers AND a
	// verdict when it asserts verification, and those are independent facts.
	if m.Kind == KindResult && m.Result.VerifiedClaim {
		if e.Resolvers.Verdict == nil {
			return ErrNoResolver{Kind: KindResult, Artifact: "verdict"}
		}
		ok, err := e.Resolvers.Verdict.Resolve(e.TenantID, m.Result.VerdictRef)
		if err != nil {
			return fmt.Errorf("conversation: could not resolve the verdict a result cites: %w", err)
		}
		if !ok {
			return ErrArtifactMissing{Kind: KindResult, Artifact: "verdict"}
		}
	}
	return nil
}

// refused records a refusal: the central-enum event and a WARN carrying the three correlation ids.
//
// 🔴 It logs rather than returning silently because a refusal is the ONE signal that something upstream
// is wrong — a model producing unsupported findings, or repository content trying to mint a proposal.
// A refusal that only becomes an error return is a defence nobody can see working.
func (e *Emitter) refused(ctx context.Context, k Kind, cause error) {
	if e.Observe != nil {
		e.Observe(eventname.ConversationRefused, map[string]any{
			"kind": k.String(), "turn_id": e.TurnID, "conversation_id": e.ConversationID,
		})
	}
	log := e.Log
	if log == nil {
		log = slog.Default()
	}
	// The three ids the logging conventions require on every WARN/ERROR (task 2.10).
	log.WarnContext(ctx, "conversation message refused before the transport",
		"event", eventname.ConversationRefused.String(),
		"kind", k.String(),
		"request_id", e.RequestID,
		"trace_id", e.TraceID,
		"span_id", e.SpanID,
		"cause", cause.Error(),
	)
}

// TerminalStop is a small helper for the terminal message: it turns a stop reason into the pair a
// `result` carries, so no caller re-derives `stopped_on_limit` and gets it backwards.
func TerminalStop(r harnessruntime.StopReason) (harnessruntime.StopReason, bool) {
	return r, r.Limit()
}
