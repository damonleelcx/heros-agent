// Package eventname is the platform's CENTRAL enum of STRUCTURED EVENT NAMES.
//
// # Why this exists beside internal/errorcode rather than inside it
//
// They answer different questions and are read by different people. An `errorcode.Code` classifies a
// FAILURE and is the only message-shaped value permitted to cross P24's error-reporting boundary. An
// event name identifies a POINT IN A FLOW — including the points where nothing failed — and never
// leaves the deployment. Merging them would put `console.conversation.turn_started` on the list of
// strings a third party may receive, which is a widening nobody asked for.
//
// # The failure a central enum prevents
//
// It is the failure `web/design-system/scan-events.mjs` already fences on the browser side, and the Go
// side had no equivalent. Two shapes, and the second is the one that matters:
//
//   - The ordinary one: `turn_started`, `turn-started`, `conversationTurnStarted` all live, none
//     comparable, and the dashboard that was supposed to answer a question answers three badly.
//   - The one that matters: an INVENTED name is a free-text field on the far side of a boundary.
//     `slog.Info(fmt.Sprintf("console.conversation.%s", intent))` is one plausible line and an
//     exfiltration path — the same shape as `fmt.Errorf("failed to resolve prompt %q", p)`, refused for
//     the same reason. A closed enum is what makes "you can read this file and know the complete set"
//     true.
//
// # Naming
//
// `<service>.<area>.<state>`, lowercase, dot-separated, as PRD P31 §9.3 states and as the repository's
// logging conventions require. `service` is the deployment component the event happens in; `area` is
// the capability; `state` is what happened, in the past tense where something completed.
//
// # 🚫 What a name may never encode
//
// A tenant, a person, a run, a conversation, a workflow, a repository, a path, or any other identifier.
// Those are ATTRIBUTES on the event, where they can be scrubbed and where cardinality is bounded. A name
// that interpolated one would reintroduce the leak this file exists to close, one layer down and harder
// to see. `Valid` is the enforcement: a name is a member of this list or it is not an event name.
package eventname

import "sort"

// Name is a member of the enum. A bare string is not assignable to it by accident from another package,
// and the emitting helper checks membership before anything is written.
type Name string

const (
	// ── console · conversation (P31) ─────────────────────────────────────────
	//
	// The four the conversational console emits. They are named for the four questions an operator
	// actually asks about this surface: is anyone using it, is it refusing them, are approvals landing,
	// and are the long-lived streams surviving the deployment.

	// ConversationTurnStarted — a question was accepted and routed, and a turn began.
	ConversationTurnStarted Name = "console.conversation.turn_started"
	// ConversationRefused — a turn ended in a refusal: an abstention, a lower-layer cause, a
	// cross-tenant probe, or a message the emitter would not let through. 🔴 One name for all of them,
	// with the cause as an attribute: a separate name per refusal reason would make "how often does
	// this surface say no" a question requiring an operator to know the list in advance.
	ConversationRefused Name = "console.conversation.refused"
	// ConversationApprovalRecorded — an approval given inside the conversation was recorded through
	// `internal/approval`. Emitted AFTER the gate returns, never before: an event emitted on the way in
	// would count intent rather than effect, and a 200 is not evidence of a write.
	ConversationApprovalRecorded Name = "console.conversation.approval_recorded"
	// ConversationStreamResumed — a client reconnected and delivery resumed from its last acknowledged
	// message. The signal that distinguishes "the stream is fine" from "the stream dies every ninety
	// seconds and the client hides it", which are indistinguishable from the user's side.
	ConversationStreamResumed Name = "console.conversation.stream_resumed"

	// ── agentd · source ingest (P32) ─────────────────────────────────────────
	//
	// The points an operator and a customer actually ask about repository intake. Note what is NOT
	// here: nothing named per forge, per tenant or per repository. Those are ATTRIBUTES on the event,
	// where cardinality is bounded and a scrubber can reach them — a name interpolating a repository
	// would put a customer's private repository name into every log index that ever sees it.

	// IngestConnectionCreated — a customer authorized the platform to read one repository.
	IngestConnectionCreated Name = "agentd.ingest.connection_created"
	// IngestConnectionRefused — an authorization was refused at connect, most importantly because the
	// resulting grant was broader than the one repository named. 🔴 One name for every refusal reason,
	// with the reason as an attribute: a separate name per reason would make "how often do we refuse a
	// connection" a question requiring an operator to know the list in advance.
	IngestConnectionRefused Name = "agentd.ingest.connection_refused"
	// IngestConnectionRevoked — a grant, its credential and every tree derived from it were deleted.
	// Emitted AFTER all three succeed, never before: an event on the way in would count intent rather
	// than effect, and the whole point of the cascade is that its second half actually happened.
	IngestConnectionRevoked Name = "agentd.ingest.connection_revoked"
	// IngestCloneSucceeded — a repository was read at a revision.
	IngestCloneSucceeded Name = "agentd.ingest.clone_succeeded"
	// IngestCloneFailed — a read failed. The CAUSE is an attribute drawn from the four-member closed
	// set, so "which failure class is rising" is a group-by rather than four separate counters.
	IngestCloneFailed Name = "agentd.ingest.clone_failed"
	// IngestRetentionSwept — the retention sweep completed. Emitted on EVERY completed run including
	// the zero-deletion one, because "the job is alive" and "the job found nothing" must not produce
	// identical output.
	IngestRetentionSwept Name = "agentd.ingest.retention_swept"
	// IngestRetentionFailed — the sweep could not complete. Consecutive occurrences escalate.
	IngestRetentionFailed Name = "agentd.ingest.retention_failed"

	// ── agentd · surface assessment (P33) ────────────────────────────────────
	//
	// Five names, and the set is chosen so that the two questions an operator actually asks are
	// group-bys rather than counters somebody has to think to add: "is assessment working" and "how
	// much of what we return is absence". Note what is NOT here: nothing per axis and nothing per
	// state. Those are ATTRIBUTES — nine axes × four states as thirty-six event names would make
	// "which axis is failing" a question requiring an operator to know the matrix in advance, and
	// would put a closed vocabulary into a place a scrubber cannot reach.

	// AssessmentRunStarted — an assessment began. Carries the tenant, the workflow and the revision
	// as attributes, and the spend cap it will be held to.
	AssessmentRunStarted Name = "agentd.assessment.run_started"
	// AssessmentAxisNotMeasured — one axis returned `not_measured`. 🔴 Emitted for EVERY such finding,
	// with the named missing input as an attribute, because the logging rule's target is exactly this
	// shape: a code path that falls back to a default without saying so. Here the "default" is
	// absence, and absence is the phase's most common answer — so this is the highest-volume event in
	// the set by design, and the one that makes the volume itself the signal.
	AssessmentAxisNotMeasured Name = "agentd.assessment.axis_not_measured"
	// AssessmentInferencePinned — an inference ran and its result was pinned against
	// `(source_revision, agent config_hash)`. Emitted when a PROVIDER CALL happened.
	AssessmentInferencePinned Name = "agentd.assessment.inference_pinned"
	// AssessmentInferenceReplayed — a pinned inference answered without a provider call. 🔴 The pair
	// with the one above is what makes FR15's determinism observable rather than merely asserted: a
	// deployment where `pinned` keeps pace with `replayed` has a pin key that is not stable, and
	// nothing else would ever say so.
	AssessmentInferenceReplayed Name = "agentd.assessment.inference_replayed"
	// AssessmentBudgetExhausted — the spend cap stopped an assessment before every axis was reached.
	// A first-class outcome, not an error: the report degrades to `not_measured` with
	// `budget_exhausted` and says it is partial.
	AssessmentBudgetExhausted Name = "agentd.assessment.budget_exhausted"
)

// names is the closure. Sorted output is produced by Names(); the declaration order here groups by
// capability, which is how somebody adding one reads it.
var names = []Name{
	ConversationTurnStarted,
	ConversationRefused,
	ConversationApprovalRecorded,
	ConversationStreamResumed,
	IngestConnectionCreated,
	IngestConnectionRefused,
	IngestConnectionRevoked,
	IngestCloneSucceeded,
	IngestCloneFailed,
	IngestRetentionSwept,
	IngestRetentionFailed,
	AssessmentRunStarted,
	AssessmentAxisNotMeasured,
	AssessmentInferencePinned,
	AssessmentInferenceReplayed,
	AssessmentBudgetExhausted,
}

// Names returns every event name, sorted. A copy, so no caller can widen the enum.
func Names() []Name {
	out := append([]Name(nil), names...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports membership. 🔴 An empty name is invalid rather than defaulted: an event nobody named is
// an event nobody can find, and a placeholder would make it look like one that can be.
func (n Name) Valid() bool {
	for _, v := range names {
		if v == n {
			return true
		}
	}
	return false
}

// String makes Name printable without a conversion at every call site.
func (n Name) String() string { return string(n) }
