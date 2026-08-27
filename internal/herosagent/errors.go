package herosagent

import (
	"errors"
	"time"
)

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
	// CodeWrongPlacement: this HOST may not run this tenant — it analyses on the other one (task 7.5).
	//
	// 🔴 Its own member rather than a reading of `disabled`, and the difference is what a surface tells
	// somebody. `disabled` means nothing analyses this tenant anywhere; this means something does, on a
	// machine that is not this one, and the answer will arrive from there. Collapsing them would have a
	// customer-placed tenant's console report HEROS as off while their own CI was producing inferences.
	CodeWrongPlacement Code = "wrong_placement"
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

// Placement is where a tenant's inference RUNS (D6). Three named states, and `disabled` is one of them
// rather than the absence of the other two.
//
// 🔴 It lives in this file for the same reason every Code does, and the reason is sharper here: one of
// its values collides with a Code. `CodeDisabled` and `PlacementDisabled` are both the string
// `disabled`, they answer different questions — "why did this analysis not happen" versus "where does
// this tenant's analysis run" — and a literal `"disabled"` typed at a call site is indistinguishable
// between them. TestEveryCodeAndEventComesFromTheCentralEnumeration reserves these values too, so the
// collision is caught by a parser rather than by whoever reads the diff.
type Placement string

const (
	// PlacementPlatform: the platform runs the inference, spending the PLATFORM's credential. It reads
	// the tenant's source, which is why Q2 made it something somebody must choose.
	PlacementPlatform Placement = "platform"
	// PlacementCustomer: the tenant runs it on their own machine with their own credential, and submits
	// the result. The supported answer for a customer whose source may not leave their network — the
	// platform is never offered a way to hold their key on their behalf.
	PlacementCustomer Placement = "customer"
	// PlacementDisabled: no inference runs anywhere for this tenant. THE DEFAULT (Q2), and a real state
	// rather than the absence of a setting — the console distinguishes it from "nobody has decided yet",
	// because "we turned it off" and "nobody looked" are different facts about a fleet.
	PlacementDisabled Placement = "disabled"
)

// Host is WHICH RUNNER is executing — not where the tenant is placed, which is the setting the host is
// checked against.
//
// Two words for what reads like one thing, and the distinction is the whole of task 7.5: the platform
// runner asking "may I run this tenant" and the customer runner asking the same question must get
// DIFFERENT answers for the same placement, and a single value cannot express that.
type Host string

const (
	// HostPlatform is the runner inside the platform.
	HostPlatform Host = "host_platform"
	// HostCustomer is the runner on the customer's machine, reached through the CLI.
	HostCustomer Host = "host_customer"
)

// ReadyState is the closed vocabulary `/readyz` reports for the agent (task 9.1).
//
// 🔴 It lives here for the same reason Placement does, and the collision is sharper: THREE of its
// values are also Code values. `disabled`, `credential_unresolved` and `no_active_definition` are each
// both a terminal outcome of ONE inference and a posture of the WHOLE deployment, and they coincide
// because a deployment's posture is "what would happen if an inference ran right now".
//
// They are still two vocabularies answering two questions, and a literal typed at a call site is
// indistinguishable between them — so TestEveryCodeAndEventComesFromTheCentralEnumeration reserves
// these too. It caught exactly this the first time they were written in readiness.go.
type ReadyState string

const (
	// ReadyDisabled: no tenant is placed anywhere but `disabled`. THE DEFAULT, and not a fault.
	ReadyDisabled ReadyState = "disabled"
	// ReadyReady: an active definition, a credential that RESOLVES, and headroom under every ceiling.
	ReadyReady ReadyState = "ready"
	// ReadyCredentialUnresolved: the active definition's credential reference does not resolve through
	// the configured secrets source. Inference fails closed, so this is a fault — but a contained one.
	ReadyCredentialUnresolved ReadyState = "credential_unresolved"
	// ReadyCapped: a ceiling is reached, so nothing will spend until it is raised or the window rolls.
	// The deployment is HEALTHY and declining to spend.
	ReadyCapped ReadyState = "capped"
	// ReadyNoDefinition: nothing is published and activated. Distinct from `disabled` — somebody has
	// enabled a tenant and there is no agent to run for it, which is a configuration half-done.
	ReadyNoDefinition ReadyState = "no_active_definition"
	// ReadyUnexecutable: a definition IS active and THIS BUILD CANNOT RUN IT (P36 task 8.3).
	//
	// 🔴 Its own state, and not `credential_unresolved` or `no_active_definition`. The three send an
	// operator to three different places: a credential that does not resolve is a secrets problem, no
	// active definition is a configuration half-done, and this one is a DEPLOYMENT MISMATCH — the
	// definition is fine and the binary is older than it.
	//
	// It is the state P36 makes reachable. A definition binding a node kind, a loop strategy or a
	// topology this build does not implement is publishable on one deployment and unrunnable on
	// another; without this, that deployment reports `ready` and fails at the first analysis, and the
	// failure names a strategy rather than the build.
	ReadyUnexecutable ReadyState = "definition_unexecutable"
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
	// ErrWiringOverride refuses a TOPOLOGY on a definition that has nothing to order (P30 task 3.2,
	// NARROWED by P36 task 3.4 / design D3).
	//
	// 🔴 It NARROWED; it was not deleted. Before P36 it refused unconditionally, because HEROS was one
	// node and there was no second node to order it against. That reason is STILL TRUE for a
	// single-node definition — which is still the default (D2) — so the rule now applies to the cases
	// where its premise holds instead of being dropped because a new case appeared.
	//
	// This is the small version of the discipline ADR-014 applies at scale. Deleting a rule whose
	// premise stopped holding in SOME cases discards a correct check for the majority of definitions.
	//
	// It also carries the refusal of the legacy `wiring` SPELLING: the axis is called `graph` now, and
	// an edit naming `wiring` is refused BY NAME with the rename stated rather than silently
	// translated — a rename that quietly accepts the old spelling never finishes.
	ErrWiringOverride = errors.New("herosagent: a topology needs a second node to order against")
	// ErrModelUnregistered refuses a model the operator registry does not carry (task 3.4).
	ErrModelUnregistered = errors.New("herosagent: that model is not in the operator model registry")
	// ErrCredentialUnresolved is the fail-closed path (task 3.6).
	ErrCredentialUnresolved = errors.New("herosagent: the credential reference does not resolve")
	// ErrKeyValueOffered refuses anything that looks like a key where a REFERENCE belongs (D5).
	ErrKeyValueOffered = errors.New("herosagent: a credential is bound by provider NAME, never entered as a value")
	// ErrHostServiceMissing refuses a strategy whose host service the runner does not supply.
	//
	// 🔴 Extended by P36 task 3.5 to the LOOP axis, and refused at PUBLISH rather than at run. D11's
	// argument, one axis later: a loop whose host service nothing supplies, discovered when an analysis
	// reaches the node, is discovered by somebody who did not choose it and cannot tell whether it is a
	// bug or a configuration — while the operator who bound it has moved on.
	//
	// 🚫 It never degrades to a strategy that needs no second actor. `critic-loop` without a critic IS
	// `reflexion`, and running it under critic-loop's config_hash would report one strategy as another.
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
	// ErrWrongPlacement refuses a run on the host the tenant's placement does not name (task 7.5).
	ErrWrongPlacement = errors.New("herosagent: this tenant's placement does not run inference on this host")
	// ErrUnknownAgentVersion refuses an ingested result naming a `config_hash` no version row carries
	// (task 7.4). Matchable because "your CLI is ahead of this deployment" and "your submission is
	// malformed" send a developer to different places.
	ErrUnknownAgentVersion = errors.New("herosagent: no published agent version carries that config_hash")
	// ErrUnattributedInference refuses a submitted fact the agent authored that names no agent version.
	// A `heros` fact whose definition cannot be named is a fact nothing can ever be re-derived from.
	ErrUnattributedInference = errors.New("herosagent: an agent-authored fact must name the agent version that produced it")
	// ErrCapReached refuses a run whose tenant or fleet token ceiling is reached (task 9.2). Matchable
	// because a caller deciding between "tell the customer analysis is paused" and "this is broken"
	// cannot do it on a string.
	ErrCapReached = errors.New("herosagent: a token ceiling is reached")
	// ErrRolloutStageSkipped refuses a rollout step that jumps a rung (task 9.4).
	ErrRolloutStageSkipped = errors.New("herosagent: a rollout stage cannot be skipped")
	// ErrNoFleetCap refuses reaching a customer-facing rollout stage with no fleet ceiling set.
	ErrNoFleetCap = errors.New("herosagent: no fleet token ceiling is set")
	// ErrAssemblerBypassed reports a ModelInput that was not produced by AssembleModelInput (task 7.2).
	ErrAssemblerBypassed = errors.New("herosagent: this model input did not come from the shared assembler")
)

// DefaultConfidenceFloor is the confidence below which an output becomes an abstention rather than a
// fact, for a deployment that has not chosen its own.
//
// 🔴 It is a DEFAULT and not a measurement, stated here the way `docs/heros/ablation-protocol.md` §2
// states the rehearsal floors: no definition has been activated, so no model has been measured, and a
// number presented as calibrated when nothing was calibrated is worse than one presented as a starting
// point. 0.7 is chosen to be clearly above "the model guessed" and clearly below a threshold that would
// make the agent abstain on everything — the first real measurement replaces it, and Q4's protocol is
// how.
//
// It is the PLATFORM's, and the customer-side runner is handed it rather than choosing one: a runner
// that picked its own floor would submit facts the platform would have declined, under a `config_hash`
// claiming otherwise.
const DefaultConfidenceFloor = 0.7

// DefaultBudget is the per-run ceiling a deployment runs the agent under.
//
// Both halves are real limits rather than round numbers meaning "plenty": 60s is longer than any
// single provider call should take and short enough that a hung analysis fails a CI step instead of
// occupying it, and 200k tokens is roughly the largest residue a repository-sized graph produces before
// the pair count itself becomes the problem — which is the case D3 exists to bound.
func DefaultBudget() Budget {
	return Budget{MaxTokens: 200_000, MaxWall: 60 * time.Second}
}
