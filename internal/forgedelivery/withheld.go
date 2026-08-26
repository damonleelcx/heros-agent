package forgedelivery

import "errors"

// withheld.go names WHY a verified proposal was not served, so the answer is a condition rather than an
// absence.
//
// # The silence this replaces
//
// Service.Pending used to drop every proposal Prepare refused with a bare `continue`. That collapsed
// five very different causes into one indistinguishable outcome — an empty list:
//
//	the gate has not passed          a designed withholding, and the surface already has a state for it
//	the tenant is not entitled       a plan action the CUSTOMER can take
//	delivery is halted               an operator action, in progress
//	the open-PR bound is reached     self-clearing; the proposal is retained
//	the route names gitlab           a product boundary — and the customer saw NOTHING
//
// The last one is the sharpest. Migration 0026's CHECK admits gitlab and bitbucket on purpose, because
// storing a route to a declared forge is legitimate configuration; delivering to one is refused in Go
// "where the reason can be stated". It was not being stated. A customer who configured gitlab got a
// 200 and an empty array, which reads as "nothing to deliver" — the product working normally.
//
// This is design Decision 6 ("no route is a reported state, not silence") applied one level down. It
// was already true of the ROUTE and false of everything the route let through.
//
// # What a reason may carry
//
// A named condition and a next action, never a raw error string. Two deliberate limits:
//
//   - The HALT REASON is not echoed. HaltedError carries what an operator wrote, and the console does
//     render it — but this response is served to a customer's CI and lands in their build log, which is
//     the one place an incident note written for internal readers should not travel to. The kind says
//     delivery is paused; the console still shows the operator's words where they belong.
//   - An INTERNAL failure (an unreadable gate, an unreadable halt, an unreadable record) is not
//     described. It is reported as WithheldUnavailable with a fixed sentence: the customer needs to know
//     that this is not a verdict about their change, and nothing beyond that is theirs to read.

// WithheldKind is why one verified proposal was not served. A CLOSED set: each value leads to a
// different next action, and a caller that cannot tell them apart can only say "nothing happened".
type WithheldKind string

const (
	// WithheldNotVerified: the change did not pass the P5.5 gate. The one DESIGNED withholding here —
	// nothing unverified is ever delivered — and the only one that is not a problem to fix.
	WithheldNotVerified WithheldKind = "not_verified"
	// WithheldNoDiff: the proposal has no compiled diff. On a deployment that generates proposals but
	// does not compile them, this is the reason EVERY proposal is withheld — and saying so is the
	// difference between a product with a stated limit and one that silently delivers nothing.
	WithheldNoDiff WithheldKind = "no_diff"
	// WithheldNotEntitled: the tenant's plan does not include this delivery level.
	WithheldNotEntitled WithheldKind = "not_entitled"
	// WithheldHalted: an operator has halted delivery for this tenant.
	WithheldHalted WithheldKind = "halted"
	// WithheldBoundReached: the per-repository open-PR bound is reached. Self-clearing — the proposal is
	// retained and delivered once a pull request is merged or closed.
	WithheldBoundReached WithheldKind = "bound_reached"
	// WithheldNoRoute: no route is configured for this target.
	WithheldNoRoute WithheldKind = "no_route"
	// WithheldNoInstallation: the run originated in the CONSOLE, whose default mode is the hosted Git
	// App (R3), and no installation covers this repository.
	//
	// 🔴 A DISTINCT kind from `no_route`, and the distinction is the whole of P35 spec scenario "A
	// console-driven run with no installation". A route can exist and be perfectly well-formed while
	// the installation behind it does not — the customer configured delivery and then never installed
	// the App, or revoked it. Collapsing the two would tell somebody to configure a route they already
	// have.
	//
	// 🚫 It is NOT a reason to fall back to CI-mediated delivery. That mode requires a CI integration a
	// console customer does not have, so falling back would replace a stated condition with a silent
	// one that never completes.
	WithheldNoInstallation WithheldKind = "no_installation"
	// WithheldInstallationRevoked: an installation existed and the customer revoked it. Distinct from
	// `no_installation` because the next action is different — one is "install it", the other is
	// "you removed this, and that worked" — and because a revocation mid-run is a state the customer
	// caused deliberately and should see reflected rather than reported as a missing setup step.
	WithheldInstallationRevoked WithheldKind = "installation_revoked"
	// WithheldForgeNotImplemented: the route names a forge P12 declares but has not built.
	WithheldForgeNotImplemented WithheldKind = "forge_not_implemented"
	// WithheldRouteInvalid: the route cannot be used — a row no route store would have written.
	WithheldRouteInvalid WithheldKind = "route_invalid"
	// WithheldUnavailable: a dependency could not be read and delivery FAILED CLOSED. Emphatically not a
	// verdict about the change: reporting an unreadable halt state as "not verified" would tell a
	// customer their change was measured and rejected on a day the database was merely unreachable.
	WithheldUnavailable WithheldKind = "delivery_unavailable"
)

// Withheld is one proposal that was not served, and why.
type Withheld struct {
	ProposalID string       `json:"proposal_id"`
	Kind       WithheldKind `json:"kind"`
	// Detail is a named condition. Never a raw error string.
	Detail string `json:"detail"`
	// NextAction is what to do about it, or "" for a condition with no action (a change that failed the
	// gate needs a better change, not a step). An empty NextAction is meaningful and is not a gap.
	NextAction string `json:"next_action,omitempty"`
	// Entitlement is the gate's own Decision on a not_entitled withholding — the platform's words about
	// which plan lifts the boundary, rather than a reconstruction. Nil for every other kind.
	Entitlement *WithheldEntitlement `json:"entitlement,omitempty"`
}

// WithheldEntitlement is the subset of entitlement.Decision this surface carries: what was denied and
// which plan lifts it.
//
// Its own type rather than the Decision itself, and that is a boundary rather than a style choice:
// Decision grows fields for the console's benefit (a LimitHit, a ReasonCode, a plan id), and this
// response is served to a customer's CI. A field added there for one audience must not appear here for
// another by default — the same reasoning the P11 allowlist applies to a payload.
type WithheldEntitlement struct {
	Feature         string `json:"feature"`
	Reason          string `json:"reason,omitempty"`
	PlanName        string `json:"plan_name,omitempty"`
	UpgradePlanName string `json:"upgrade_plan_name,omitempty"`
}

// ClassifyWithheld turns a delivery refusal into a reported condition, for a caller outside this
// package that holds an error and must render it as a state with a next action.
//
// 🔴 Exported for ONE caller — `internal/improvementrun`, which is P35's new delivery caller and must
// render the same conditions this package's own `Service.Pending` does. The alternative is a second
// classifier there, and a second classifier is the thing this function's own comment says drifts.
func ClassifyWithheld(proposalID string, err error) Withheld {
	return classifyWithheld(proposalID, err)
}

// classifyWithheld turns a Prepare refusal into a reported condition.
//
// The order matters where the checks overlap: the typed errors are tested before the sentinels they
// wrap, so a route error is reported as a route condition rather than as whatever generic wrapper it
// happens to carry.
func classifyWithheld(proposalID string, err error) Withheld {
	w := Withheld{ProposalID: proposalID}

	var halted *HaltedError
	var notEnt *NotEntitledError

	switch {
	case errors.Is(err, ErrNoDiff):
		w.Kind = WithheldNoDiff
		w.Detail = "This proposal has no compiled diff, so there is nothing to open a pull request with. " +
			"The change was generated but not compiled: the codemod needs your source at a revision and a " +
			"build check."
		w.NextAction = "No action is available yet; this deployment does not compile proposals into diffs."

	case errors.Is(err, ErrNotVerified):
		w.Kind = WithheldNotVerified
		w.Detail = "This change has not passed the verification gate, so it is not delivered. " +
			"Nothing unverified is ever delivered."
		// No next action, deliberately: the answer is a better change or a completed verification run,
		// neither of which is a step this surface can name.

	case errors.As(err, &notEnt):
		w.Kind = WithheldNotEntitled
		d := notEnt.Decision
		w.Detail = d.Reason
		if w.Detail == "" {
			w.Detail = "Your plan does not include this delivery level."
		}
		w.Entitlement = &WithheldEntitlement{
			Feature: string(d.Feature), Reason: d.Reason,
			PlanName: d.PlanName, UpgradePlanName: d.UpgradePlanName,
		}
		if d.UpgradePlanName != "" {
			w.NextAction = "Upgrade to " + d.UpgradePlanName + " to enable delivery for this workflow."
		} else {
			w.NextAction = "Contact your account owner: no configured plan lifts this boundary."
		}

	case errors.As(err, &halted):
		// The operator's own words are NOT echoed here. See the file header.
		w.Kind = WithheldHalted
		w.Detail = "Delivery is paused platform-side by an operator. Your proposal is retained."
		w.NextAction = "No action is needed from you; delivery resumes when the pause is lifted."

	case errors.Is(err, ErrBoundReached):
		w.Kind = WithheldBoundReached
		// The number is NOT named. The bound is per-Deliverer and configurable (NewDeliverer takes it),
		// and this classifier does not have it — quoting DefaultOpenPRBound here would confidently state
		// "10" to a deployment that configured 3. A wrong specific number is worse than no number: it is
		// the kind a customer counts their open pull requests against and concludes the product is broken.
		w.Detail = "This repository has reached its limit of open delivery pull requests. " +
			"The proposal is retained, not discarded."
		w.NextAction = "Merge or close an open delivery pull request; the next fetch will deliver this one."

	case errors.Is(err, ErrForgeNotImplemented):
		w.Kind = WithheldForgeNotImplemented
		w.Detail = "This repository's delivery route names a forge that is declared but not implemented. " +
			"GitHub is the only forge P12 delivers to."
		w.NextAction = "Point this route at a GitHub repository, or wait for support for the configured forge."

	case errors.Is(err, ErrRouteInvalid):
		w.Kind = WithheldRouteInvalid
		w.Detail = "This repository's delivery route is not usable — it is missing something a pull " +
			"request cannot be opened without."
		w.NextAction = "Reconfigure the delivery route for this repository."

	case errors.Is(err, ErrNoInstallation):
		w.Kind = WithheldNoInstallation
		w.Detail = "This run started in the console, where the platform opens the pull request itself, " +
			"and no hosted Git App installation covers this repository. The verified change and its " +
			"evidence are kept; only the pull request is withheld."
		w.NextAction = "Install the hosted Git App for this repository. It is per-repository, it may " +
			"open and update pull requests and nothing else, and you can revoke it at any time."

	case errors.Is(err, ErrInstallationRevoked):
		w.Kind = WithheldInstallationRevoked
		w.Detail = "The hosted Git App installation for this repository was revoked, so the platform " +
			"can no longer push to it. That is the revocation working. The verified change and its " +
			"evidence are kept."
		w.NextAction = "Re-install the hosted Git App for this repository if you want the platform to " +
			"open pull requests again."

	case errors.Is(err, ErrNoRoute):
		w.Kind = WithheldNoRoute
		w.Detail = "No delivery route is configured for this repository."
		w.NextAction = nextActionFor(RouteAbsent)

	default:
		// Everything else is an INTERNAL failure that failed closed — an unreadable gate, an unreadable
		// halt state, an unreadable delivery record. Described generically on purpose: the customer needs
		// to know this is not a verdict about their change, and the error's text is not theirs to read.
		w.Kind = WithheldUnavailable
		w.Detail = "Delivery could not be evaluated for this proposal and was withheld. This is not a " +
			"verdict about the change."
		w.NextAction = "Retry; if it persists, contact support with the proposal id."
	}
	return w
}

// IsReportedCondition reports whether an error from Deliver is a legible terminal state the console
// renders as a condition-with-next-action, rather than a server fault. It lets a caller separate
// "nothing to render as broken" from "something failed".
//
// 🔴 It DELEGATES to classifyWithheld rather than keeping its own switch, and that is the point: two
// lists of "which errors are conditions" drift, and the one that drifts is the one nobody reads. It
// used to name five errors and knew nothing about a route naming an unimplemented forge — so
// ErrForgeNotImplemented would have been reported as a server fault by one path and as a condition by
// the other.
func IsReportedCondition(err error) bool {
	if err == nil {
		return false
	}
	return classifyWithheld("", err).Kind != WithheldUnavailable
}
