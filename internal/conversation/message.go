package conversation

import (
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// message.go is the wire shape of everything the conversational console can say.
//
// # Why one struct with eight optional payloads rather than eight structs
//
// Because the CONSUMER is a browser reading a stream, and a stream of heterogeneous events needs a
// discriminator it can switch on before it knows what it has. `kind` is that discriminator, and the
// payload fields are pointers so exactly one is non-null. The generated TypeScript is therefore a
// discriminated union the type-checker can narrow — `if (m.kind === "finding") m.finding!.claim` — and
// a kind added in Go with no payload field is a compile error rather than a blank card.
//
// The alternative — one `payload: any` — is the shape that makes ADR-007's generator refuse, and for
// the right reason: `any` is a contract that has quietly stopped being one.
//
// # 🔴 The invariant every payload shares
//
// Every field that carries a CLAIM carries the reference that supports it, in the same struct. Not in a
// sibling message, not resolvable by convention from an id — in the struct, so `Emitter.Emit` can check
// it without a second read and so a reviewer can see it without following anything.

// Message is one thing the agent said.
type Message struct {
	// ID is monotonic per conversation and is the ACKNOWLEDGEMENT CURSOR: a client reconnecting sends
	// the last id it processed and delivery resumes after it. 🔴 Assigned by the store under its own
	// lock, never by a caller — an id a caller chose could repeat, and a repeated cursor is a gap or a
	// duplicate depending on which way the client rounds.
	ID int64 `json:"id"`
	// ConversationID is the conversation this belongs to.
	ConversationID string `json:"conversation_id"`
	// TurnID is the turn that produced it. Present on every message including the terminal one, so a
	// reader can group a transcript by turn without inferring boundaries from kinds.
	TurnID string `json:"turn_id"`
	// Kind is the discriminator.
	Kind Kind `json:"kind"`
	// Provenance says whether this replayed a pin or was generated in this turn (FR13).
	Provenance Provenance `json:"provenance"`
	// At is when the message was emitted, RFC 3339 on the wire, rendered through the console's pinned
	// en-US formatter and never by the browser's own locale.
	At time.Time `json:"at"`
	// TraceID is the turn's trace id (FR23). Carried on EVERY message rather than only the terminal
	// one: a person whose turn is stuck at four minutes needs the id most, and that is exactly when no
	// terminal message exists.
	TraceID string `json:"trace_id"`

	// Exactly one of the following is non-nil, and it is the one `Kind` names.
	Plan            *PlanPayload            `json:"plan,omitempty"`
	Progress        *ProgressPayload        `json:"progress,omitempty"`
	Finding         *FindingPayload         `json:"finding,omitempty"`
	Proposal        *ProposalPayload        `json:"proposal,omitempty"`
	ApprovalRequest *ApprovalRequestPayload `json:"approval_request,omitempty"`
	Result          *ResultPayload          `json:"result,omitempty"`
	Refusal         *RefusalPayload         `json:"refusal,omitempty"`
	Answer          *AnswerPayload          `json:"answer,omitempty"`
}

// ── plan ─────────────────────────────────────────────────────────────────────────────────────────

// PlanStep is one step the agent intends, and the surface it will read to do it.
//
// `Surface` is the console route that owns the answer (PRD FR25). It is on the PLAN rather than only on
// the finding because it is what makes the plan reviewable before it runs: a person who asked about
// memory and sees a plan whose steps all read `/app/harness` knows the route was wrong before four
// minutes are spent.
type PlanStep struct {
	// ID is stable within the turn and is what a `progress` and a reconciliation entry name.
	ID string `json:"id"`
	// Title is what the step does, in the product's own nouns.
	Title string `json:"title"`
	// Surface is the console route this step reads. Empty for a step that reads no surface.
	Surface string `json:"surface"`
}

// BudgetEnvelope is FR17's four numbers, declared BEFORE the first step runs.
//
// # Why four and not one
//
// Because each of them can be the one that fires, and an operator's next action differs for each. A run
// that stopped on wall clock with tokens to spare means something upstream is slow; a run that stopped
// on tokens in nine seconds means the plan was too big. One "budget" number cannot tell those apart, and
// the message a user gets would be the same for both.
//
// # Where the numbers come from (PRD §14 Q6)
//
// The tenant's entitlement. Displayed, NOT editable in the conversation — an authorable budget is how
// one question spends a month's allowance, and the person typing it is the one least able to price it.
type BudgetEnvelope struct {
	// TurnCeiling is the maximum number of agent turns. Reaching it stops the run with
	// `harnessruntime.StopCeiling`.
	TurnCeiling int `json:"turn_ceiling"`
	// TokenBudget is the total provider tokens this turn may spend.
	TokenBudget int `json:"token_budget"`
	// ToolCallCeiling is the maximum number of tool calls.
	ToolCallCeiling int `json:"tool_call_ceiling"`
	// WallClockSeconds is the maximum elapsed wall-clock time.
	WallClockSeconds int `json:"wall_clock_seconds"`
}

// Complete reports whether all four limits are present and positive.
//
// 🔴 A ZERO IS NOT A LIMIT OF ZERO, it is an absent limit, and the two must not be conflated: a plan
// with `token_budget: 0` would either stop instantly or — far more likely, because a `<=` against zero
// reads as "no budget configured" at some call site — run unbounded. `Emitter.Emit` refuses a plan that
// is not Complete, so the ambiguity has no runtime.
func (b BudgetEnvelope) Complete() bool {
	return b.TurnCeiling > 0 && b.TokenBudget > 0 && b.ToolCallCeiling > 0 && b.WallClockSeconds > 0
}

// MissingLimits names the limits a plan failed to declare, for the refusal message. Ordered, so the
// error text is stable.
func (b BudgetEnvelope) MissingLimits() []string {
	var out []string
	if b.TurnCeiling <= 0 {
		out = append(out, "turn_ceiling")
	}
	if b.TokenBudget <= 0 {
		out = append(out, "token_budget")
	}
	if b.ToolCallCeiling <= 0 {
		out = append(out, "tool_call_ceiling")
	}
	if b.WallClockSeconds <= 0 {
		out = append(out, "wall_clock_seconds")
	}
	return out
}

// PlanPayload is the first message of every turn.
type PlanPayload struct {
	// Intent is the intent the question routed to, so the person can see the routing decision rather
	// than infer it from the steps. Typed, so the browser's rendering of it is exhaustive too.
	Intent Intent `json:"intent"`
	// Steps are ordered and are the DENOMINATOR the result reconciles against (FR19).
	Steps []PlanStep `json:"steps"`
	// Budget is the envelope, declared before anything runs.
	Budget BudgetEnvelope `json:"budget"`
}

// ── progress ─────────────────────────────────────────────────────────────────────────────────────

// BudgetRemaining is what is left of the envelope, read from the run.
//
// It is a separate struct from BudgetEnvelope with the same shape on purpose. They are different facts
// — what was promised and what is left — and a UI that received one type for both would be one refactor
// away from rendering the promise as the remainder, which reads as a run that has spent nothing.
type BudgetRemaining struct {
	Turns            int `json:"turns"`
	Tokens           int `json:"tokens"`
	ToolCalls        int `json:"tool_calls"`
	WallClockSeconds int `json:"wall_clock_seconds"`
}

// ProgressPayload is one step advancing.
type ProgressPayload struct {
	// Phase is REQUIRED. A `progress` with no phase is refused before the transport: a turn that
	// cannot name its phase is a defect, not a slow turn (FR16).
	Phase Phase `json:"phase"`
	// StepID names the plan step this concerns; empty during `understand`, which precedes the plan.
	StepID string `json:"step_id"`
	// Detail is a short line about what is happening. Prose, and safe as prose because it asserts
	// nothing about the repository — anything that does is a `finding` (FR3).
	Detail string `json:"detail"`
	// ElapsedMS is how long the turn has been running.
	ElapsedMS int64 `json:"elapsed_ms"`
	// Remaining is the live budget, read from the run. Carried on every `progress` so the four facts a
	// spinner withholds are all on screen at once (PRD §9.1).
	Remaining BudgetRemaining `json:"remaining"`
}

// ── finding ──────────────────────────────────────────────────────────────────────────────────────

// FindingPayload is a claim about the customer's repository. The only kind that may make one.
type FindingPayload struct {
	// Surface is the console surface that owns this claim, e.g. "memory". Used for grouping.
	Surface string `json:"surface"`
	// SurfaceHref is the route a reader follows to see the number in the place it is computed
	// (FR25). 🔴 The conversation LINKS rather than restates: the moment it renders the number itself
	// it is the second place that number is formatted, and the two will disagree.
	SurfaceHref string `json:"surface_href"`
	// Axis and Node locate the claim. Empty when the claim is about the workflow as a whole.
	Axis string `json:"axis"`
	Node string `json:"node"`
	// Claim is what was found, in one sentence.
	Claim string `json:"claim"`
	// EvidenceRef is REQUIRED (FR2). A finding with an empty reference is refused server-side and
	// never transmitted; there is no client-side path that renders it as prose.
	EvidenceRef string `json:"evidence_ref"`
	// State is one of the four the console renders.
	State FindingState `json:"state"`
	// MissingInput is REQUIRED when State is `not_measured`, and names what was absent. "Not measured"
	// with no named input is the omission problem with a label on it.
	MissingInput string `json:"missing_input"`
	// Cause is REQUIRED when State is `refused`, carried VERBATIM from the lower layer (FR15).
	// 🚫 Never re-worded by a model.
	Cause string `json:"cause"`
	// SourceRevision is REQUIRED when State is `stale`, and names the revision the claim describes
	// (PRD §14 Q2). "Stale" without a revision is a warning about nothing.
	SourceRevision string `json:"source_revision"`
}

// ── proposal ─────────────────────────────────────────────────────────────────────────────────────

// ProposalPayload is a change the platform is prepared to make. Effect-bearing: `ProposalID` must
// resolve in the verification ledger before this can be emitted (D7).
type ProposalPayload struct {
	// ProposalID is the ledger artifact. 🚫 A model cannot mint one.
	ProposalID string `json:"proposal_id"`
	Axis       string `json:"axis"`
	Node       string `json:"node"`
	// Delta and its interval are computed SERVER-SIDE and rendered as received. The browser derives
	// nothing — that is P9's founding rule, and a conversation is not an exemption from it.
	DeltaLabel string `json:"delta_label"`
	// DiffRef points at the diff. A reference, so the diff is read where diffs are read.
	DiffRef string `json:"diff_ref"`
	// Href links to the proposal's own page.
	Href string `json:"href"`
}

// ── approval_request ─────────────────────────────────────────────────────────────────────────────

// ApprovalRequestPayload asks a person to authorize an action.
//
// 🔴 It is DELIVERED even when it cannot be approved (FR9, design.md D4). A hidden control is
// indistinguishable from one that does not exist, and the person needs to know an action was considered
// and why it is unavailable — which is usually a plan or an automation-level fact they can act on.
type ApprovalRequestPayload struct {
	// ApprovalID is what the approval route names. Resolved through `internal/approval`.
	ApprovalID string `json:"approval_id"`
	// ProposalID is what would be applied.
	ProposalID string `json:"proposal_id"`
	// Action is ONE act, stated plainly: "open a pull request". 🔴 "Open a pull request" and "merge a
	// pull request" are two requests and are never bundled (FR10) — they have different blast radii and
	// different reversibility, and a single "yes" covering both is a "yes" nobody gave.
	Action string `json:"action"`
	// BlastRadius is what this touches if it goes wrong.
	BlastRadius string `json:"blast_radius"`
	// Reversible is how it is undone, or the fact that it cannot be.
	Reversible string `json:"reversible"`
	// Approvable is the SERVER's entitlement decision. The console renders no approval control when
	// this is false — the decision is not re-derived in the browser.
	Approvable bool `json:"approvable"`
	// UnapprovableReason is REQUIRED when Approvable is false, and names the reason in the product's
	// own nouns (a plan, an automation level), never "not permitted".
	UnapprovableReason string `json:"unapprovable_reason"`
}

// ── result ───────────────────────────────────────────────────────────────────────────────────────

// ReconciliationEntry is one planned step, resolved (FR19).
type ReconciliationEntry struct {
	// StepID names a step the plan declared. Every declared step has exactly one entry.
	StepID string `json:"step_id"`
	// State is one of `done` | `skipped` | `refused` | `not_measured`.
	State StepState `json:"state"`
	// Reason is REQUIRED for every state except `done`. A `skipped` with no reason is an omission with
	// a label on it, which is the thing FR19 exists to prevent.
	Reason string `json:"reason"`
}

// ResultPayload is the terminal message of a turn.
type ResultPayload struct {
	RunID string `json:"run_id"`
	// StopReason comes from `harnessruntime`'s vocabulary — the SAME one a node loop uses (design.md
	// D8). It is present on every result including a satisfied one, because "it finished" must be
	// SAID rather than inferred from the absence of a limit (task 4.13).
	StopReason harnessruntime.StopReason `json:"stop_reason"`
	// StoppedOnLimit is the server's own answer to "may this be rendered as complete?". Carried rather
	// than derived in the browser for P9's founding reason, and because the predicate lives with the
	// vocabulary it reads.
	StoppedOnLimit bool `json:"stopped_on_limit"`
	// StoppedAtStep names the step a re-entry ceiling fired on (FR22). Empty otherwise.
	StoppedAtStep string `json:"stopped_at_step"`
	// Reconciliation carries EXACTLY as many entries as the plan declared steps. Fewer is refused
	// before the transport, naming the unreconciled step.
	Reconciliation []ReconciliationEntry `json:"reconciliation"`
	// Summary is one sentence about the outcome. Prose, asserting nothing about the repository.
	Summary string `json:"summary"`
	// VerifiedClaim is true when this result reports that a change was verified. When it is,
	// VerdictRef is REQUIRED and must resolve (FR20).
	VerifiedClaim bool `json:"verified_claim"`
	// VerdictRef references the verification record that produced the verdict.
	VerdictRef string `json:"verdict_ref"`
	// DeliveryRef references a delivery record that EXISTS. Effect-bearing (D7).
	DeliveryRef string `json:"delivery_ref"`
	// PullRequestURL is carried ONLY when the delivery record carries it. 🚫 Never composed from a
	// repository name and a number: a URL the platform invented is a URL that 404s in a customer's
	// browser while looking exactly like one that works.
	PullRequestURL string `json:"pull_request_url"`
}

// ── refusal ──────────────────────────────────────────────────────────────────────────────────────

// RefusalPayload carries a lower layer's cause verbatim, or an abstention.
type RefusalPayload struct {
	// Axis and Node locate the refusal when the lower layer named them.
	Axis string `json:"axis"`
	Node string `json:"node"`
	// Cause is the lower layer's own text, UNMODIFIED (FR15). 🚫 A model never re-words it: a softened
	// safety boundary is a second, weaker statement of it.
	Cause string `json:"cause"`
	// Failure is the failure class when this refusal is one of P9's three; empty when it is a
	// business refusal such as an abstention. Kept distinct so a 503 stays a 503 inside a surface
	// whose natural tendency is to flatten everything into one apologetic sentence (FR4).
	Failure FailureClass `json:"failure"`
	// CanDo is what this surface CAN be asked, for an abstention (FR14). An open text box implies
	// infinity; this is the finite list.
	CanDo []string `json:"can_do"`
	// SurfaceHref points at the surface that performs what was asked, when the answer is "not here"
	// rather than "not at all" — account, billing and identity questions (FR26).
	SurfaceHref string `json:"surface_href"`
	// StopReason is present because a refusal is terminal and every terminal message names one.
	StopReason harnessruntime.StopReason `json:"stop_reason"`
}

// ── answer ───────────────────────────────────────────────────────────────────────────────────────

// AnswerPayload is free prose, and the ONLY kind that is.
//
// 🔴 Admissible only for questions that assert nothing about the customer's repository (FR3). The
// emitter enforces it structurally rather than by classifying the prose: an `answer` may only be
// emitted for an intent whose `ProseOnly` is true, which is a property of the closed intent set and not
// of the text. Classifying the text would make the boundary as accurate as a classifier; keying it to
// the intent makes it a property of the route.
type AnswerPayload struct {
	Text string `json:"text"`
	// Topic is the capability or term the answer is about, so the console can link to the
	// documentation for it rather than leaving prose as the only artifact.
	Topic string `json:"topic"`
}
