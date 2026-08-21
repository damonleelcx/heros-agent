package conversation

// ── Kind (task 1.1) ──────────────────────────────────────────────────────────────────────────────

// Kind is the closed set of things the conversational console may say (PRD FR1).
//
// # Why closed, and why the closure is enforced twice
//
// The kind decides how the browser renders a message and, for three of the eight, whether an EFFECT is
// possible. An open vocabulary would mean the browser has a default branch, and a default branch is
// where an unrecognised kind renders as prose — which is the "unverified LLM opinion in the result
// position" this whole surface exists to prevent.
//
// So it is closed here (`Kind.Valid`, checked before the transport) and closed again in the browser:
// the generated console union has exactly these members, and a kind added here without a union member
// fails the console type-check rather than rendering blank. See cmd/consoletypes and ADR-007.
//
// # 🚫 What a kind may never be
//
// A kind is never chosen by a model. `Emitter.Emit` is called by platform code with a kind it wrote at
// the call site; a model's output is at most a FIELD inside a payload, never the discriminator. The
// three effect-bearing kinds tighten that further — see EffectArtifact.
type Kind string

const (
	// KindPlan is the ordered steps the agent intends, with the budget envelope it will spend
	// (FR17). 🔴 Emitted BEFORE the first step, because the plan is the denominator: without it,
	// "I looked at your repository" cannot be short, and an agent that ran three of eight steps
	// produces prose indistinguishable from one that ran eight.
	KindPlan Kind = "plan"

	// KindProgress is one step advancing, carrying the phase the turn is in (FR16). Rendered as an
	// updating line rather than a new message. A `progress` with no phase is refused — a turn that
	// cannot name its phase is a defect, not a slow turn.
	KindProgress Kind = "progress"

	// KindFinding is a claim about the customer's repository, and it MUST carry the evidence
	// reference that supports it (FR2). This is the only kind that may assert a repository property,
	// which is the whole of why `answer` may not.
	KindFinding Kind = "finding"

	// KindProposal is a change the platform is prepared to make, identified by a `proposal_id` that
	// resolves in the verification ledger. Effect-bearing: see EffectArtifact.
	KindProposal Kind = "proposal"

	// KindApprovalRequest asks a person to authorize an action, stating its blast radius and what is
	// reversible (FR10). Effect-bearing. 🔴 It is delivered even when it cannot be approved, carrying
	// the reason — a hidden control is indistinguishable from one that does not exist (design.md D4).
	KindApprovalRequest Kind = "approval_request"

	// KindResult is the terminal message of a turn: the stop reason, the reconciliation of every step
	// the plan declared (FR19), and the delivery reference when something was delivered.
	// Effect-bearing.
	KindResult Kind = "result"

	// KindRefusal carries a lower layer's typed cause VERBATIM (FR15), or an abstention naming what
	// this surface can do (FR14). 🚫 Never re-worded by a model: a softened safety boundary is a
	// second, weaker statement of it.
	KindRefusal Kind = "refusal"

	// KindAnswer is free prose, admissible ONLY for questions that assert nothing about the
	// customer's repository — "what can you do?", "what does context strategy mean?" (FR3). Anything
	// asserting a repository property is a `finding` and inherits the evidence requirement.
	KindAnswer Kind = "answer"
)

// kinds is the closure. Ordered as a reader meets them in a turn, because this slice is what the
// generated TypeScript union is emitted from and a reader of that file benefits from the same order.
var kinds = []Kind{
	KindPlan, KindProgress, KindFinding, KindProposal,
	KindApprovalRequest, KindResult, KindRefusal, KindAnswer,
}

// Kinds returns the closed vocabulary. A copy, so no caller can widen it.
func Kinds() []Kind { return append([]Kind(nil), kinds...) }

// Valid reports membership. The check `Emitter.Emit` performs before anything else.
func (k Kind) Valid() bool {
	for _, v := range kinds {
		if v == k {
			return true
		}
	}
	return false
}

// String makes Kind printable in an error without a conversion at every call site.
func (k Kind) String() string { return string(k) }

// Terminal reports whether this kind ends a turn.
//
// Two kinds do: `result` (the turn finished, for some value of finished) and `refusal` (the turn will
// not happen). Both must carry a stop reason — including `satisfied`, because task 4.13's rule is that
// a run which finished normally SAYS so rather than being the absence of a limit.
func (k Kind) Terminal() bool { return k == KindResult || k == KindRefusal }

// ── Provenance (task 1.2) ────────────────────────────────────────────────────────────────────────

// Provenance records whether a message replayed a pinned inference or was generated in this turn
// (FR13, design.md D6).
//
// # Why this is a field and not an implementation detail
//
// P30's determinism guarantee is invisible without it. A person who asks the same question twice and
// gets the same answer cannot tell whether the system is deterministic or merely consistent, and a
// guarantee nobody can falsify is a claim rather than a guarantee.
//
// # Where it is set
//
// In exactly one place: `Emitter.Emit` takes it from the emitter's per-turn provenance, which the turn
// runner sets ONCE when it decides between replaying a pin and generating (see FR11's resolution in
// internal/api/conversations.go). 🚫 A payload cannot carry its own provenance — a field a caller
// could set per message is a field that will eventually be set to `pinned` on generated output, which
// is the exact lie this enum exists to make impossible.
type Provenance string

const (
	// ProvenancePinned — replayed from an inference already pinned for this
	// `(source_revision, agent config_hash)`. No provider call was made.
	ProvenancePinned Provenance = "pinned"
	// ProvenanceGenerated — produced in this turn.
	ProvenanceGenerated Provenance = "generated"
)

// Valid reports membership. An empty provenance is INVALID rather than defaulted: a message whose
// origin nobody recorded must not silently claim to have been generated.
func (p Provenance) Valid() bool {
	return p == ProvenancePinned || p == ProvenanceGenerated
}

// String makes Provenance printable in an error.
func (p Provenance) String() string { return string(p) }

// ── Phase (task 1.6) ─────────────────────────────────────────────────────────────────────────────

// Phase is the five named phases every turn advances through (FR16, design.md D8).
//
// # Why `verify` is a PHASE and not a message kind
//
// This is the decision worth reading. The obvious design gives verification its own message — a
// `verification` kind beside `finding` — and it is wrong, because the platform already has a place
// where "checked" means something: `internal/verification` and the verdict ledger it writes. A second
// notion of checked, living in a message kind, would be a second answer to "was this verified?" that
// can disagree with the one the proposal gate actually reads.
//
// So `verify` is a phase — a stretch of TIME in the turn during which claims are checked against the
// artifacts that support them — and its OUTPUT is a field on `result`: the verdict reference (FR20)
// and the plan reconciliation (FR19). The turn observably passes through verification, and what
// verification concluded is carried by the terminal message rather than announced by an extra one.
//
// The same argument kills a `checkpoint` kind: a checkpoint is `progress` with a phase on it, which is
// what `progress` already is.
//
// # Monotonic
//
// The phases advance in order and never go backwards. `Phase.After` is what enforces it; a turn that
// re-enters a step stays in `act` and increments the step's re-entry counter (FR22) rather than
// rewinding the phase, because a phase that can move backwards cannot answer "how far along is this?"
// — which is the one question a spinner cannot answer and this enum exists to answer.
type Phase string

const (
	// PhaseUnderstand — the question is routed to one intent, or the router abstains.
	// Proves it happened: a `plan`, or a `refusal` on abstention.
	PhaseUnderstand Phase = "understand"
	// PhasePlan — the ordered steps, the surfaces they will read, and the budget envelope.
	// Proves it happened: the `plan` message.
	PhasePlan Phase = "plan"
	// PhaseAct — the steps run, each emitting its own evidence.
	// Proves it happened: `progress` and `finding` messages.
	PhaseAct Phase = "act"
	// PhaseVerify — every claim is checked against the artifact that supports it.
	// Proves it happened: the reconciliation and verdict reference carried on `result`.
	PhaseVerify Phase = "verify"
	// PhaseRespond — the terminal message, with every planned step reconciled.
	// Proves it happened: `result`, or `refusal`.
	PhaseRespond Phase = "respond"
)

// phaseOrder is the monotonic sequence. Index is the phase's position; nothing else depends on the
// numbers, so inserting a phase is an edit to one slice.
var phaseOrder = []Phase{PhaseUnderstand, PhasePlan, PhaseAct, PhaseVerify, PhaseRespond}

// Phases returns the five in order. A copy.
func Phases() []Phase { return append([]Phase(nil), phaseOrder...) }

// Valid reports membership.
func (p Phase) Valid() bool { return p.index() >= 0 }

// String makes Phase printable in an error.
func (p Phase) String() string { return string(p) }

func (p Phase) index() int {
	for i, v := range phaseOrder {
		if v == p {
			return i
		}
	}
	return -1
}

// After reports whether p comes at or after other in the sequence — the check a turn runner makes
// before advancing. Both must be valid; an invalid phase is never "after" anything, because treating
// an unknown phase as late would let a typo skip `verify`.
func (p Phase) After(other Phase) bool {
	pi, oi := p.index(), other.index()
	if pi < 0 || oi < 0 {
		return false
	}
	return pi >= oi
}

// ── StepState — plan reconciliation (task 1.8) ───────────────────────────────────────────────────

// StepState is how one planned step resolved (FR19). Every step the `plan` declared carries exactly
// one, and every state that is not `done` names its reason.
//
// # 🔴 Why this is a SECOND enum beside FindingState, and not the same one
//
// Task 1.8 asks for this to be the same enum P33 uses for surface state, or for the reason it must be
// a second one. It must be a second one, and the reason is that the two answer different questions.
//
//	StepState     answers "did this planned step run?"           — about WORK
//	FindingState  answers "was a measurement taken, and is it     — about a CLAIM
//	              still true of the current revision?"
//
// They overlap on `not_measured` and `refused`, and that overlap is why the temptation exists. But
// `done` and `skipped` have no meaning about a claim (a finding is never "skipped"), and `measured`
// and `stale` have no meaning about work (a step is never "stale" — the finding it produced is). One
// merged enum would have six members of which two are illegal in each position, which is a vocabulary
// whose validity depends on where it appears — the exact shape that makes an exhaustive switch useless.
//
// What IS shared is the spelling, deliberately: `not_measured` and `refused` are spelled identically in
// both so a reader who learns them once has learned them, and so a UI can use one copy string for each.
// P33 imports FindingState from this package rather than declaring its own — the single-truth-source
// rule applies to the vocabulary even though it does not apply to the enum boundary.
type StepState string

const (
	// StepDone — the step ran and produced what it said it would.
	StepDone StepState = "done"
	// StepSkipped — the step was not attempted, and the reason names why. 🔴 A skipped step with no
	// reason is the omission problem with a label on it; `Reconciliation.Validate` refuses one.
	StepSkipped StepState = "skipped"
	// StepRefused — a lower layer refused the step, and the cause text is carried verbatim.
	StepRefused StepState = "refused"
	// StepNotMeasured — the step was attempted and no measurement could be taken, naming the missing
	// input. This is the state a step degrades to when a budget ran out mid-plan (FR18) — 🚫 never to
	// a shorter answer presented as the whole one.
	StepNotMeasured StepState = "not_measured"
)

var stepStates = []StepState{StepDone, StepSkipped, StepRefused, StepNotMeasured}

// StepStates returns the four. A copy.
func StepStates() []StepState { return append([]StepState(nil), stepStates...) }

// Valid reports membership.
func (s StepState) Valid() bool {
	for _, v := range stepStates {
		if v == s {
			return true
		}
	}
	return false
}

// String makes StepState printable in an error.
func (s StepState) String() string { return string(s) }

// NeedsReason reports whether this state must name why. Everything except `done`: a step that did what
// it said needs no explanation, and every other outcome is a shortfall the reader is owed a cause for.
func (s StepState) NeedsReason() bool { return s != StepDone }

// ── FindingState ─────────────────────────────────────────────────────────────────────────────────

// FindingState is what a `finding` knows about its own claim. Four states, and the console renders
// four (task 4.3) — a component that renders three is wrong.
//
// # Why `not_measured` is a state and not an omission
//
// A conversational surface makes absence feel like an answer: silence about a surface reads as
// "nothing wrong with it". So the surface that could not be measured produces a message SAYING so,
// naming the input it lacked, exactly as P29 made "not reported" a rendered state.
type FindingState string

const (
	// FindingMeasured — a measurement was taken and the evidence reference resolves to it.
	FindingMeasured FindingState = "measured"
	// FindingNotMeasured — the surface was examined and no measurement could be taken. The payload
	// names the missing input. 🔴 NOT omitted from the conversation.
	FindingNotMeasured FindingState = "not_measured"
	// FindingRefused — a lower layer refused to measure it, and the cause text is carried verbatim.
	FindingRefused FindingState = "refused"
	// FindingStale — replayed from a pin taken at an earlier source revision than the workflow's
	// current one (PRD §14 Q2: answer from the pin, label it stale, offer a re-run). The payload names
	// the revision the claim describes, so "stale" is a fact about a revision rather than a warning
	// about nothing.
	FindingStale FindingState = "stale"
)

var findingStates = []FindingState{FindingMeasured, FindingNotMeasured, FindingRefused, FindingStale}

// FindingStates returns the four. A copy.
func FindingStates() []FindingState { return append([]FindingState(nil), findingStates...) }

// Valid reports membership.
func (f FindingState) Valid() bool {
	for _, v := range findingStates {
		if v == f {
			return true
		}
	}
	return false
}

// String makes FindingState printable in an error.
func (f FindingState) String() string { return string(f) }

// ── FailureClass ─────────────────────────────────────────────────────────────────────────────────

// FailureClass keeps P9's three failure classes three, inside a surface whose natural tendency is to
// flatten everything into one apologetic sentence (FR4).
//
// The three are distinguished because a person does three different things about them: mount the
// subsystem, check the identifier, retry. A single "something went wrong" tells them to do none.
type FailureClass string

const (
	// FailureNotMounted — 503: the subsystem is not present in this deployment. Remedy: mount it.
	FailureNotMounted FailureClass = "not_mounted"
	// FailureNotFound — 404: the named subject does not exist. 🚫 Never rendered as a business state.
	FailureNotFound FailureClass = "not_found"
	// FailureTransport — the request could not be completed. Remedy: retry.
	FailureTransport FailureClass = "transport"
)

var failureClasses = []FailureClass{FailureNotMounted, FailureNotFound, FailureTransport}

// FailureClasses returns the three. A copy.
func FailureClasses() []FailureClass { return append([]FailureClass(nil), failureClasses...) }

// Valid reports membership.
func (f FailureClass) Valid() bool {
	for _, v := range failureClasses {
		if v == f {
			return true
		}
	}
	return false
}

// String makes FailureClass printable in an error.
func (f FailureClass) String() string { return string(f) }
