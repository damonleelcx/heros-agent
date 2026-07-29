package authoring

import "sort"

// Authoring health and audit SIGNALS (P13 13c task 12.3).
//
// # Why an operator needs these at all
//
// The question an operator is asked about this feature is always the same shape: "somebody says they
// cannot change a node — why?" Without externally-readable signals the only way to answer it is to
// reproduce the user's draft, which needs their repository, their session and their exact edit. With
// them it is a query.
//
// # Why the names are identifiers and not sentences
//
// Every signal here is a STABLE IDENTIFIER. The refusal's prose is written for the person who has to
// act on it and is deliberately free to improve; a dashboard, an alert rule and a support runbook are
// not, and pinning them to prose means a clearer sentence silently breaks the alert that watched for the
// old one. So the cause a signal carries is a slug, and the sentence stays where it belongs — in front
// of the user.
//
// 🚫 No prompt text, no source, no diff, no environment value, no credential, and no draft content
// crosses this boundary. A signal says WHAT happened and to WHICH kind of thing; it never carries the
// thing itself.

// SignalName is a stable event identifier. Adding a member is additive; renaming one breaks every
// consumer, so the vocabulary is closed and each member is named for the FACT rather than the wording.
type SignalName string

const (
	// SignalSubmitted: an authored change was recorded.
	SignalSubmitted SignalName = "authoring.submitted"
	// SignalRefused: a draft was declined, with the cause slug attached.
	SignalRefused SignalName = "authoring.refused"
	// SignalNotYetMeasurable: a gate could not decide because a measurement is missing. Deliberately
	// its OWN signal rather than a variety of refusal — an operator watching refusals should not be
	// paged for a measurement gap, and someone filling measurement gaps should not have to filter
	// refusals to find them.
	SignalNotYetMeasurable SignalName = "authoring.not_yet_measurable"
	// SignalConflict: a submission lost a concurrency check.
	SignalConflict SignalName = "authoring.conflict"
	// SignalReverted: an authored change was undone.
	SignalReverted SignalName = "authoring.reverted"
)

// CauseSlug is the machine-readable reason attached to a refusal signal. It is derived from the
// dimension and the shape, never from the message text.
type CauseSlug string

const (
	CauseCrossProvider     CauseSlug = "cross_provider_swap"
	CauseInlineParams      CauseSlug = "inline_node_cannot_carry_params"
	CauseUnappliedSlot     CauseSlug = "prompt_slot_no_longer_binds"
	CauseNoMaterializer    CauseSlug = "no_materializer_for_language"
	CauseToolNotDiscovered CauseSlug = "tool_not_in_discovered_set"
	CauseUnresolvedRef     CauseSlug = "ref_resolves_to_nothing"
	CauseUnmaterializable  CauseSlug = "shape_cannot_be_materialized"
	CauseOther             CauseSlug = "other"
)

// Signal is one externally-readable event. Every field is a low-cardinality label or an identifier —
// there is deliberately nowhere here to put free text.
type Signal struct {
	Name SignalName `json:"name"`
	// Axis is the dimension involved ("model", "context", …). Low cardinality by construction: the
	// dimension enum is closed.
	Axis string `json:"axis,omitempty"`
	// Cause is the slug, on a refusal.
	Cause CauseSlug `json:"cause,omitempty"`
	// Missing names the measurement kind, on a not-yet-measurable. A KIND, never a value.
	Missing string `json:"missing,omitempty"`
	// TenantID scopes the signal. Actor is deliberately ABSENT: an operator diagnosing "why do changes
	// on this tenant refuse?" needs the tenant and the cause, and per-person telemetry is a different
	// question with a different privacy posture. The audit RECORD carries the actor; this does not.
	TenantID string `json:"tenant_id,omitempty"`
}

// Emitter receives signals. An interface so the CLI can drop them on the floor and the server can route
// them to whatever it already runs, without either importing the other's telemetry stack.
type Emitter interface {
	EmitAuthoringSignal(Signal)
}

// SignalNames is every signal this package can emit, sorted. Exported so a dashboard, an alert rule, or
// a test can enumerate the vocabulary rather than discovering it one incident at a time.
func SignalNames() []SignalName {
	out := []SignalName{SignalSubmitted, SignalRefused, SignalNotYetMeasurable, SignalConflict, SignalReverted}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// CauseSlugs is every refusal cause slug, sorted.
func CauseSlugs() []CauseSlug {
	out := []CauseSlug{CauseCrossProvider, CauseInlineParams, CauseUnappliedSlot, CauseNoMaterializer,
		CauseToolNotDiscovered, CauseUnresolvedRef, CauseUnmaterializable, CauseOther}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SignalFor turns a preflight result into the signal it warrants.
//
// The mapping lives here, once, rather than at each surface: the console and the CLI must classify the
// same refusal the same way, or a dashboard counting "cross-provider refusals" counts a different set
// depending on which surface the user was on.
func SignalFor(r Result, tenantID string) (Signal, bool) {
	switch r.Verdict {
	case VerdictRefused:
		return Signal{Name: SignalRefused, Axis: r.Refusal.Field, TenantID: tenantID,
			Cause: classifyCause(r.Refusal)}, true
	case VerdictNotYetMeasurable:
		return Signal{Name: SignalNotYetMeasurable, TenantID: tenantID,
			Axis: r.Refusal.Field, Missing: r.Missing.Kind}, true
	}
	// An admissible preflight is not an event. Emitting one would make the signal volume proportional to
	// keystrokes rather than to outcomes, and the interesting thing about authoring is what it declines.
	return Signal{}, false
}

// classifyCause derives the slug from what the refusal NAMED, never from its prose.
//
// The default is CauseOther rather than a guess. A slug invented by pattern-matching a sentence is a
// slug that silently changes meaning the day the sentence improves — and an operator would have no way
// to tell that the category under their alert had shifted.
func classifyCause(r Refusal) CauseSlug {
	switch {
	case r.Shape != "" && r.Shape != r.Field:
		return CauseUnmaterializable
	case r.Field == "skills":
		return CauseNoMaterializer
	case r.Field == "tools":
		return CauseToolNotDiscovered
	case r.Field == "prompt":
		return CauseUnappliedSlot
	case r.Field == "context":
		return CauseNoMaterializer
	case r.Field == "model":
		return CauseCrossProvider
	default:
		return CauseOther
	}
}
