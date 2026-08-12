package herosagent

import "errors"

// errors.go is the CENTRAL ENUMERATION of every code and event this subsystem emits (task 4.11).
//
// # Why one file and not a string at each call site
//
// A code a monitor matches on, or a console switches on, that is typed at three call sites is three
// codes as far as anything downstream is concerned — and the drift is invisible, because each of the
// three works perfectly at the moment it is written. The same argument `providergateway`'s source-kind
// enum makes, applied to a subsystem that will be read during incidents.
//
// 🚫 A literal code or event string anywhere else in this package fails
// TestEveryCodeAndEventComesFromThisFile, which parses the tree rather than trusting the convention.

// Code is a closed vocabulary of terminal outcomes. Every one is a state a surface renders and an
// operator acts on differently.
type Code string

const (
	// CodeOK: the inference completed and its result was stored.
	CodeOK Code = "ok"
	// CodeDisabled: HEROS is not enabled for this tenant. The DEFAULT (Q2) — it is not a failure, and a
	// surface must not render it as one.
	CodeDisabled Code = "disabled"
	// CodeCredentialUnresolved: the definition's credential reference does not resolve through the
	// configured secrets source. FAILS CLOSED (task 3.6): zero provider calls, no substitution, and
	// every surface falls back to rule-derived facts.
	CodeCredentialUnresolved Code = "credential_unresolved"
	// CodeModelUnregistered: the definition names a model the operator registry does not carry.
	CodeModelUnregistered Code = "model_unregistered"
	// CodeNoActiveDefinition: nothing has been published and activated, so there is no agent to run.
	// Distinct from `disabled`, which is a deliberate per-tenant choice.
	CodeNoActiveDefinition Code = "no_active_definition"
	// CodeRehearsalPending: a definition exists and has not passed its gate. It must not serve.
	CodeRehearsalPending Code = "rehearsal_pending"
	// CodeCapReached: a per-tenant or fleet cap was reached BEFORE the provider call (task 9.2).
	CodeCapReached Code = "cap_reached"
	// CodeBudgetExceeded: the per-run token or wall-clock budget was exceeded mid-run. The run aborts,
	// the abort is recorded, and NO partial IR is written (task 4.9).
	CodeBudgetExceeded Code = "budget_exceeded"
	// CodeProviderFailed: the provider call failed. Surfaces as `analysis failed` WITH THE CAUSE —
	// 🚫 never as an empty graph, which would read as a finding about the customer's workflow.
	CodeProviderFailed Code = "provider_failed"
	// CodeOutputRejected: the model's output was outside the closed vocabulary and was REJECTED, not
	// repaired (D8). A validator that coerced a near-miss into the nearest legal value would turn a
	// detectable failure into an undetectable one.
	CodeOutputRejected Code = "output_rejected"
	// CodeNothingToInfer: the residue was empty — every node and pair already carries a rule-derived
	// fact. A healthy answer, and ZERO provider calls.
	CodeNothingToInfer Code = "nothing_to_infer"
)

// AbstentionReason is the closed enum stored on `heros_abstention.reason` (FR3.4).
//
// 🚫 Never prose. A free-text reason is a reason nothing can aggregate, and "which abstention dominates
// on this workflow" is the question that tells an operator what to fix.
type AbstentionReason string

const (
	// AbstainBelowFloor: the agent produced a candidate and its confidence was under the floor.
	AbstainBelowFloor AbstentionReason = "below_confidence_floor"
	// AbstainNoCandidate: the agent examined the subject and offered nothing at all. Distinct from
	// below-floor: one is uncertainty, the other is silence, and they suggest different fixes.
	AbstainNoCandidate AbstentionReason = "no_candidate_offered"
	// AbstainOutOfVocabulary: the agent named something outside the closed vocabulary and it was
	// rejected. Recorded as an abstention rather than dropped, so the rejection has a trace.
	AbstainOutOfVocabulary AbstentionReason = "out_of_vocabulary"
	// AbstainUnknownNode: the agent referenced a node id the IR does not carry.
	AbstainUnknownNode AbstentionReason = "unknown_node"
	// AbstainFrontendOwns: the agent proposed an edge a frontend already established. 🔴 D3 fence 1 —
	// rule-derived topology is IMMUTABLE to HEROS. Recorded, never applied.
	AbstainFrontendOwns AbstentionReason = "frontend_already_established"
)

// Event is the closed set of things worth emitting to telemetry.
type Event string

const (
	EventInferenceStarted   Event = "heros.inference.started"
	EventInferenceStored    Event = "heros.inference.stored"
	EventInferenceCacheHit  Event = "heros.inference.cache_hit"
	EventInferenceAborted   Event = "heros.inference.aborted"
	EventOutputRejected     Event = "heros.output.rejected"
	EventAbstained          Event = "heros.abstained"
	EventCapReached         Event = "heros.cap.reached"
	EventDefinitionPublish  Event = "heros.definition.published"
	EventDefinitionActivate Event = "heros.definition.activated"
	EventRehearsalFailed    Event = "heros.rehearsal.failed"
)

// Sentinel errors. Every one is matchable with errors.Is, because a caller deciding between "fall back
// to rule-derived facts" and "tell the operator to fix their configuration" cannot do it on a string.
var (
	// ErrWiringOverride refuses a wiring (P15) override at publish (task 3.2).
	ErrWiringOverride = errors.New("herosagent: the wiring axis is fixed and cannot be overridden")
	// ErrModelUnregistered refuses a model the operator registry does not carry (task 3.4).
	ErrModelUnregistered = errors.New("herosagent: that model is not in the operator model registry")
	// ErrCredentialUnresolved is the fail-closed path (task 3.6).
	ErrCredentialUnresolved = errors.New("herosagent: the credential reference does not resolve")
	// ErrKeyValueOffered refuses anything that looks like a key where a REFERENCE belongs (D5).
	ErrKeyValueOffered = errors.New("herosagent: a credential is bound by provider NAME, never entered as a value")
	// ErrHostServiceMissing refuses a strategy whose host service the runner does not supply.
	ErrHostServiceMissing = errors.New("herosagent: that strategy needs a host service this runner does not supply")
	// ErrAlreadyActive is returned when a second definition would be activated.
	ErrAlreadyActive = errors.New("herosagent: another definition is already active")
	// ErrRehearsalNotPassed refuses activating a definition that has not met its floor (D7).
	ErrRehearsalNotPassed = errors.New("herosagent: this definition has not passed its rehearsal")
	// ErrNoChange reports an edit that resolves to the active definition (task 6.2): it creates no
	// version and says so, rather than minting a duplicate the operator has to reason about.
	ErrNoChange = errors.New("herosagent: this edit resolves to the active definition — no version was created")
	// ErrInvalidDefinition covers the structural refusals.
	ErrInvalidDefinition = errors.New("herosagent: invalid definition")
)
