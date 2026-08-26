package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/improvementrun"
)

// improvementruns.go is the P35 surface: turn a question into a plan, run the plan, and read a run back.
//
// # Three routes, and why not more
//
// `careful-api-creation` asks for the alternatives before a new endpoint. The console needs exactly
// three things and everything else it might want is a field on a response:
//
//	POST /api/v1/improvement-plans   a question becomes a plan. SPENDS NOTHING.
//	POST /api/v1/improvement-runs    a plan executes. This is the one that costs money.
//	GET  /api/v1/improvement-runs    read a run back, with its proposals and its per-axis breakdown.
//
// 🔴 **The plan and the run are two routes, and that is FR1/FR2 rather than API taste.** A single route
// that planned-and-ran would make the plan a receipt: the person would see the scope and the budget
// after the money was spent, which is the exact inversion design D1 exists to prevent. Two routes is
// what makes "shown before execution" a property of the transport rather than a promise about a
// renderer.
//
// A fourth route was considered and refused: `GET /api/v1/improvement-proposals?run_id=…`. It would be
// a second way to obtain a subset of one document, and the subset cannot be rendered honestly on its
// own — a proposal read out of its run has lost the run's bound and its per-axis breakdown, which are
// the two facts that say how to read it. Same refusal `assessments.go` makes, for the same reason.
//
// # 🔴 Both are FLAT
//
// `/api/v1/improvement-runs/{id}` is the natural shape and it cannot be published: an `Exact` ingress
// rule cannot match a variable segment, and the only rule that could is a `Prefix`, which would publish
// every sibling anybody adds under that head. P29's lesson, applied before the 404 rather than after —
// the same way P31, P32 and P33 applied it.
//
// # What P30 left as a warning, and what it costs to ignore
//
// P30 found the generate route **mounted, buttonless, and unpublished** — a button would have 404'd
// against production behind an entirely green build. So the route below is published in
// `publicroutes.go` AND in the checked-in ingress manifest in the same change that mounts it, and
// `TestEveryImprovementRunRouteIsPublishedExact` fails when the two disagree.

// ImprovementRunner is the P35 dependency.
//
// An interface rather than the concrete `*improvementrun.Service` so a deployment can mount the READ
// surface over a store this process does not write to, and so the handler tests can drive every refusal
// without a database or a provider.
type ImprovementRunner interface {
	// Plan translates a question into a bounded plan. It executes nothing and spends nothing.
	Plan(ctx context.Context, tenantID, question string, origin improvementrun.RunOrigin) (improvementrun.Plan, error)
	// Acknowledge records a person's agreement to a plan above the disclosure threshold.
	Acknowledge(ctx context.Context, a improvementrun.Acknowledgement) error
	// Propose executes a plan.
	Propose(ctx context.Context, p improvementrun.Plan) (improvementrun.Run, error)
	// Run reads one back. ok=false is "no such run", never an error.
	Run(ctx context.Context, tenantID, runID string) (improvementrun.Run, bool, error)
	// Decide applies ONE decision to ONE proposal. 🚫 There is no plural form — see design D4.
	Decide(ctx context.Context, tenantID, runID, proposalID string, kind improvementrun.DecisionKind, by string) (
		improvementrun.Run, improvementrun.Decision, error)
}

// compile-time assertion that the shipped service satisfies the surface. 🔴 Here rather than in a test:
// a mounting layer that took a narrower interface than the service provides would compile, and the
// method it silently stopped requiring would be the one nobody notices is unmounted.
var _ ImprovementRunner = (*improvementrun.Service)(nil)

// ── Console view types (registered in consoletypes.go; rendered by web/console) ──

// ImprovementPlanView is a plan as the console renders it, BEFORE anything runs.
type ImprovementPlanView struct {
	PlanID   string `json:"plan_id"`
	Origin   string `json:"origin"`
	Question string `json:"question"`

	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	// SourceRevisionShort is rendered; the full value is carried. A finding must stay attributable to an
	// exact revision, and a truncated one is not attributable.
	SourceRevisionShort string `json:"source_revision_short"`

	Axes         []string `json:"axes"`
	CandidateCap int      `json:"candidate_cap"`

	SpendBudgetUSD    float64 `json:"spend_budget_usd"`
	ProjectedSpendUSD float64 `json:"projected_spend_usd"`
	// DisclosureThresholdUSD is carried so the console does not hard-code the number it compares
	// against — the same reason `assessment.Health.AlertAbove` is published.
	DisclosureThresholdUSD float64 `json:"disclosure_threshold_usd"`
	// RequiresAcknowledgement is the SERVER's decision. 🚫 Not re-derived in the browser: the console
	// renders no run control while this is true and no acknowledgement has been given, and the decision
	// that gates money is not one a renderer gets to make.
	RequiresAcknowledgement bool `json:"requires_acknowledgement"`

	MinImprovement float64 `json:"min_improvement"`
	StoppingLabel  string  `json:"stopping_label"`

	CreatedAtMS int64 `json:"created_at_ms"`
}

// ImprovementRefusalView is a question that could not be bounded (FR3).
//
// 🔴 Its own type rather than a bare `{"error": "…"}`, because a refusal here has a CAUSE from a closed
// set and a NEXT ACTION, and a console that received a string could only render an apology. The whole
// argument for refusing rather than defaulting is that the person can act on the refusal.
type ImprovementRefusalView struct {
	Cause      string `json:"cause"`
	Detail     string `json:"detail"`
	NextAction string `json:"next_action"`
}

// AxisStageView is one axis's count at every stage (§9.5, task 7.15).
type AxisStageView struct {
	Axis string `json:"axis"`
	// InScope distinguishes "the plan excluded this axis" from "this axis produced nothing" — opposite
	// findings that a bare zero cannot tell apart.
	InScope   bool `json:"in_scope"`
	Generated int  `json:"generated"`
	Verified  int  `json:"verified"`
	Approved  int  `json:"approved"`
	Delivered int  `json:"delivered"`
	Withdrawn int  `json:"withdrawn"`
}

// ImprovementProposalView is one verified proposal as the console renders it.
type ImprovementProposalView struct {
	ProposalID string `json:"proposal_id"`
	Axis       string `json:"axis"`
	Node       string `json:"node"`
	Operator   string `json:"operator"`
	Rationale  string `json:"rationale"`

	ConfigHash      string `json:"config_hash"`
	ConfigHashShort string `json:"config_hash_short"`
	SourceRevision  string `json:"source_revision"`

	// DeltaLabel is rendered SERVER-SIDE, once. 🚫 The browser derives nothing — P9's founding rule, and
	// a conversation is not an exemption from it. The raw numbers travel too, for a chart.
	DeltaLabel  string  `json:"delta_label"`
	DeltaMean   float64 `json:"delta_mean"`
	DeltaLow    float64 `json:"delta_ci_low"`
	DeltaHigh   float64 `json:"delta_ci_high"`
	NSeeds      int     `json:"n_seeds"`
	NCases      int     `json:"n_cases"`
	Significant bool    `json:"significant"`
	// HeldOut false means no held-out split could be formed, so the delta generalizes unproven. Carried
	// so "verified" never silently means "verified on the cases that produced it".
	HeldOut bool `json:"held_out"`
	// EvalSetCannotFail states that the set behind this number could not have failed. When true the
	// console must not present the score as evidence of quality.
	EvalSetCannotFail bool `json:"eval_set_cannot_fail"`

	CostDelta    float64 `json:"cost_delta"`
	LatencyDelta float64 `json:"latency_delta"`

	DiffRef  string `json:"diff_ref"`
	DiffStat string `json:"diff_stat"`

	ProviderModelVersion string `json:"provider_model_version"`
}

// ImprovementDecisionSummary is a decision as it appears INSIDE a run view — the same facts as
// `ImprovementDecisionView` without the run, which would be circular.
type ImprovementDecisionSummary struct {
	State      string `json:"state"`
	By         string `json:"by,omitempty"`
	AtMS       int64  `json:"at_ms,omitempty"`
	Sentence   string `json:"sentence"`
	VoidReason string `json:"void_reason,omitempty"`
}

// ImprovementWithdrawalView is one withdrawn change (FR16).
type ImprovementWithdrawalView struct {
	ProposalID string `json:"proposal_id"`
	Axis       string `json:"axis"`
	Reason     string `json:"reason"`
	// AboutTheChange is false for `provider_moved`, `pin_broken` and `unmeasurable`. 🔴 The console
	// switches on THIS rather than on the reason string: rendering all four alike would tell somebody
	// their change was bad on a day a vendor shipped a model.
	AboutTheChange bool `json:"about_the_change"`
	// VerifiedLabel and RemeasuredLabel are BOTH rendered, through one formatter. Two formatters for
	// one comparison is how a reader concludes the difference is bigger than it is.
	VerifiedLabel   string  `json:"verified_label"`
	RemeasuredLabel string  `json:"remeasured_label"`
	Sentence        string  `json:"sentence"`
	SpendUSD        float64 `json:"spend_usd"`
	AtMS            int64   `json:"at_ms"`
}

// ImprovementDeliveryView is one delivered — or withheld — proposal.
type ImprovementDeliveryView struct {
	ProposalID     string `json:"proposal_id"`
	Axis           string `json:"axis"`
	DeliveryID     string `json:"delivery_id,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
	PullRequestRef string `json:"pull_request_ref,omitempty"`
	Mode           string `json:"mode"`
	// Deduplicated says this call returned the FIRST delivery rather than creating a second one (FR20).
	Deduplicated bool `json:"deduplicated"`
	// WithheldKind and the two sentences beside it are present when nothing was pushed. 🔴 A withheld
	// delivery is a CONDITION with a next action, never an absence: the verified diff stays available
	// and the person is told what would make it deliverable.
	WithheldKind       string `json:"withheld_kind,omitempty"`
	WithheldDetail     string `json:"withheld_detail,omitempty"`
	WithheldNextAction string `json:"withheld_next_action,omitempty"`
	AtMS               int64  `json:"at_ms"`
}

// ImprovementEmptyView is the named "nothing to propose" answer (FR7).
type ImprovementEmptyView struct {
	State      string `json:"state"`
	Headline   string `json:"headline"`
	Detail     string `json:"detail"`
	NextAction string `json:"next_action,omitempty"`
	// Healthy says the platform looked and found nothing WRONG. Rendering `no_bottleneck` in the same
	// tone as `no_linked_runs` tells a customer their setup is broken when it is finished.
	Healthy bool `json:"healthy"`
}

// ImprovementRunView is one run as the console renders it.
type ImprovementRunView struct {
	RunID string              `json:"run_id"`
	Plan  ImprovementPlanView `json:"plan"`

	// Bound is which bound stopped the run, by name, and BoundSentence is what the console says about
	// it. Empty bound plus a non-empty Fault means the run ended on OUR dependency, not on a limit the
	// customer reached — the two must never render alike.
	Bound         string `json:"bound"`
	BoundSentence string `json:"bound_sentence"`
	Fault         string `json:"fault,omitempty"`

	Proposals []ImprovementProposalView `json:"proposals"`
	// Decisions is keyed by proposal id. 🔴 A DECLINED proposal keeps its entry and stays in
	// `Proposals`: one that disappeared when it was declined looks like one that was never made.
	Decisions map[string]ImprovementDecisionSummary `json:"decisions"`
	// Withdrawals are approved, applied changes that failed to reproduce (FR16). BOTH measurements
	// travel on each, because a withdrawal with one number looks like a bug.
	Withdrawals []ImprovementWithdrawalView `json:"withdrawals"`
	// Deliveries are the pull requests opened. 🔴 Each carries the URL the FORGE returned; a URL the
	// platform composed from a repository name and a number is a URL that 404s in a customer's browser
	// while looking exactly like one that works.
	Deliveries []ImprovementDeliveryView `json:"deliveries"`
	Empty      *ImprovementEmptyView     `json:"empty,omitempty"`
	PerAxis    []AxisStageView           `json:"per_axis"`

	SpendUSD float64 `json:"spend_usd"`
	// WithdrawnSpendUSD is the part of the spend that produced no delivery (decisions.md D-35.4).
	// Reported separately because a run that spent 40% of its budget on withdrawn candidates is telling
	// somebody something an aggregate hides.
	WithdrawnSpendUSD float64 `json:"withdrawn_spend_usd"`

	StartedAtMS  int64 `json:"started_at_ms"`
	FinishedAtMS int64 `json:"finished_at_ms"`
}

// MountImprovementRuns registers the P35 routes. Call after New.
func (s *Server) MountImprovementRuns(r ImprovementRunner) {
	s.improvementRuns = r
	s.Mux.HandleFunc("POST /api/v1/improvement-plans", s.handleImprovementPlan)
	s.Mux.HandleFunc("POST /api/v1/improvement-runs", s.handleImprovementRun)
	s.Mux.HandleFunc("GET /api/v1/improvement-runs", s.handleReadImprovementRun)
	s.Mux.HandleFunc("POST /api/v1/improvement-decisions", s.handleImprovementDecision)
}

// improvementDecisionRequest is ONE decision on ONE proposal.
//
// # Why a `decision` discriminator and not two routes
//
// `careful-api-creation`'s enum-variant alternative, taken rather than dismissed: approving and
// declining are the same act on the same resource with opposite values, and two endpoints would be two
// places the hash-binding check could diverge — at which point the safe answer would be whichever one
// somebody remembered to write it in.
//
// # 🚫 Why this is not `POST /api/v1/conversation-approvals`
//
// That route exists and was considered. It is scoped to a CONVERSATION and addresses an `approval_id`;
// this is scoped to a RUN and addresses a `proposal_id`, and it has a decline verb that route has no
// value for. Overloading it would mean a body whose meaningful fields depend on which other field is
// set — the shape ADR-007's generator refuses, and for the right reason.
//
// # 🚫 There is no `proposal_ids`
//
// Design D4. A bundle approval is one click that means several things, and the person will read the
// first item and accept the rest. `TestTheDecisionRouteHasNoPluralForm` asserts the payload by
// reflection, because the pressure for this arrives in a future phase under delivery pressure and a
// comment does not stop it.
type improvementDecisionRequest struct {
	RunID      string `json:"run_id"`
	ProposalID string `json:"proposal_id"`
	// Decision is `approve` or `decline`. No third value.
	Decision string `json:"decision"`
}

func (s *Server) handleImprovementDecision(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.improvementPrincipal(w, r)
	if !ok {
		return
	}
	if principal.UserID == "" {
		// 🔴 A MACHINE credential is refused. A decision is a PERSON's act, and `internal/approval`
		// refuses an empty actor for the reason this refusal exists one layer up: a row that records a
		// decision and cannot say who made it is worse than no row, because it is believed.
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "approving a change is a person's act and this credential names none. Sign in to " +
				"the console; a machine credential cannot approve."})
		return
	}
	var req improvementDecisionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "the request body must be {\"run_id\": \"…\", \"proposal_id\": \"…\", " +
				"\"decision\": \"approve\"|\"decline\"}: " + err.Error()})
		return
	}
	kind := improvementrun.DecisionKind(strings.TrimSpace(req.Decision))
	if !kind.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "decision must be \"approve\" or \"decline\""})
		return
	}
	if strings.TrimSpace(req.RunID) == "" || strings.TrimSpace(req.ProposalID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "run_id and proposal_id are required"})
		return
	}

	run, decision, err := s.improvementRuns.Decide(
		r.Context(), principal.TenantID, req.RunID, req.ProposalID, kind, principal.UserID)
	if err != nil {
		if errors.Is(err, improvementrun.ErrApprovalVoid) {
			// 🔴 409, and the RUN travels with it. The approval is void because its subject moved, and
			// the person needs to see the re-requested proposal — a bare error would leave the console
			// rendering a stale card with an approve button that will fail again.
			writeJSON(w, http.StatusConflict, improvementDecisionView(run, decision))
			return
		}
		if errors.Is(err, improvementrun.ErrNotSurfaced) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		s.writeImprovementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, improvementDecisionView(run, decision))
}

// improvementPlanRequest is the plan request.
//
// 🚫 There is deliberately no `tenant_id`, no `origin`, no `budget` and no `axes`. The tenant comes from
// the credential; the origin comes from the TRANSPORT (see `originFor`); the budget comes from the
// tenant's entitlement; and the axes are read from the question. A body field for any of them would be
// a field a caller sets to widen its own run — which is the failure `Bounds` exists as a separate type
// to make unexpressible.
type improvementPlanRequest struct {
	Question string `json:"question"`
	// Acknowledge, when true, records the person's agreement to the plan this call produces, in the same
	// round trip. 🔴 It is on the PLAN route, not the run route, and that is the point: acknowledging is
	// agreeing to a plan you have SEEN, so a client that sets it without rendering the plan first has
	// acknowledged a plan it never showed anybody — which is a client defect this field makes visible
	// (the acknowledgement records what was projected) rather than one it prevents.
	Acknowledge bool `json:"acknowledge,omitempty"`
}

func (s *Server) handleImprovementPlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.improvementPrincipal(w, r)
	if !ok {
		return
	}
	var req improvementPlanRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "the request body must be {\"question\": \"…\"} and nothing else: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "question is required"})
		return
	}

	plan, err := s.improvementRuns.Plan(r.Context(), principal.TenantID, req.Question, originFor(r))
	if err != nil {
		s.writeImprovementError(w, err)
		return
	}
	if req.Acknowledge {
		if err := s.improvementRuns.Acknowledge(r.Context(), improvementrun.Acknowledgement{
			PlanID: plan.PlanID, TenantID: principal.TenantID,
			// 🔴 From the SESSION. `approval.Approve`'s reason applies verbatim: a row that records an
			// agreement and cannot say who gave it is worse than no row, because it is believed. Nothing
			// request-supplied may reach this argument.
			By:                principal.UserID,
			ProjectedSpendUSD: plan.ProjectedSpendUSD,
			AtMS:              plan.CreatedAtMS,
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusCreated, improvementPlanView(plan))
}

// improvementRunRequest executes a plan the caller has already been shown.
//
// 🔴 It takes a PLAN ID rather than a question. A run route that took a question would re-translate,
// and re-translation is where the plan somebody saw and the plan that ran can differ — a budget that
// moved between the two calls would produce a run under bounds nobody was shown. The plan id is derived
// from every field that changes what the run does, so a moved bound is a different id and this route
// 404s rather than running something else.
type improvementRunRequest struct {
	PlanID string `json:"plan_id"`
	// Question is required and must be the one the plan was built from. It is re-supplied so the server
	// can re-derive the plan deterministically without holding run state between two requests, and the
	// id check below is what makes tampering with it a 404 rather than a different run.
	Question string `json:"question"`
}

func (s *Server) handleImprovementRun(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.improvementPrincipal(w, r)
	if !ok {
		return
	}
	var req improvementRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "the request body must be {\"plan_id\": \"…\", \"question\": \"…\"}: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.PlanID) == "" || strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "plan_id and question are both required; the question is re-supplied so the plan " +
				"can be re-derived deterministically, and the plan_id is what proves it is the same plan"})
		return
	}

	plan, err := s.improvementRuns.Plan(r.Context(), principal.TenantID, req.Question, originFor(r))
	if err != nil {
		s.writeImprovementError(w, err)
		return
	}
	if plan.PlanID != req.PlanID {
		// 🔴 404, not 400, and not "run it anyway". The plan the caller was shown is not the plan this
		// question now produces — most often because the tenant's budget moved between the two calls.
		// Running the new one would spend under bounds nobody saw.
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "this plan no longer describes what would run — its scope or its bounds have moved " +
				"since it was shown. Ask again to see the current plan before running it."})
		return
	}

	run, err := s.improvementRuns.Propose(r.Context(), plan)
	if err != nil {
		s.writeImprovementError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, improvementRunView(run))
}

func (s *Server) handleReadImprovementRun(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.improvementPrincipal(w, r)
	if !ok {
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	if runID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "run_id is required"})
		return
	}
	run, found, err := s.improvementRuns.Run(r.Context(), principal.TenantID, runID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such run"})
		return
	}
	writeJSON(w, http.StatusOK, improvementRunView(run))
}

// writeImprovementError renders a refusal as the condition it is.
//
// 🔴 A `*improvementrun.Refusal` is a 422 with a CAUSE and a NEXT ACTION, not a 500 with a sentence.
// The entire argument for refusing an unboundable question rather than defaulting is that the person
// can act on the refusal, and a console that received a string could only render an apology.
func (s *Server) writeImprovementError(w http.ResponseWriter, err error) {
	var ref *improvementrun.Refusal
	if errors.As(err, &ref) {
		writeJSON(w, http.StatusUnprocessableEntity, ImprovementRefusalView{
			Cause: string(ref.Cause), Detail: ref.Detail, NextAction: ref.NextAction,
		})
		return
	}
	if errors.Is(err, improvementrun.ErrAwaitingAcknowledgement) {
		// 402 would be wrong (nothing is owed) and 403 would be wrong (nothing is forbidden). 409 says
		// the request conflicts with the resource's current state, which is exactly true: the plan is
		// unacknowledged and the console's next act is to show it and acknowledge.
		writeJSON(w, http.StatusConflict, ImprovementRefusalView{
			Cause:      "awaiting_acknowledgement",
			Detail:     err.Error(),
			NextAction: "Show the plan and acknowledge it; nothing has run and nothing has been spent.",
		})
		return
	}
	if errors.Is(err, improvementrun.ErrNotConfigured) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func (s *Server) improvementPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.improvementRuns == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "the improvement run is not mounted in this deployment"})
		return auth.Principal{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "running an improvement against a repository requires an authenticated tenant"})
		return auth.Principal{}, false
	}
	return principal, true
}

// originFor decides which SURFACE a request came from.
//
// 🔴 It reads the TRANSPORT, never the body, and that is a security boundary rather than a style
// choice: the origin selects the delivery mode (R3 — console runs use the hosted Git App) and decides
// whether the run may deliver at all (decisions.md D-35.3 — a scheduled run never does). An origin a
// caller could set is an origin a caller sets to `console` to obtain the write-credential path.
//
// The console reaches this handler through its own server side, which forwards inside the cluster with
// the console's user agent; anything else is a machine and is treated as the CLI, which keeps
// CI-mediated delivery and gives the platform no forge credential. ⚠️ The conservative direction is
// deliberate: mistaking a console request for a CLI one withholds delivery with a stated reason;
// mistaking a CLI request for a console one would reach for a credential.
func originFor(r *http.Request) improvementrun.RunOrigin {
	if strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), consoleUserAgent) {
		return improvementrun.OriginConsole
	}
	return improvementrun.OriginCLI
}

// consoleUserAgent is the substring the console's server side sends. Lowercase; matched
// case-insensitively.
const consoleUserAgent = "heros-console"

// ImprovementDecisionView is one decision, WITH the run it changed.
//
// 🔴 The run travels with the decision because approving does more than record consent: it applies and
// re-measures, so the run may have gained a withdrawal in the same call. A response carrying only the
// decision would let a console render "approved" over a change that was withdrawn three lines later,
// and §9.4 asks for exactly that sequence — approved → applied → withdrawn — to be tellable.
type ImprovementDecisionView struct {
	ProposalID string `json:"proposal_id"`
	State      string `json:"state"`
	By         string `json:"by,omitempty"`
	AtMS       int64  `json:"at_ms,omitempty"`
	// Sentence is rendered SERVER-SIDE, one per state, so a console cannot invent a fifth.
	Sentence string `json:"sentence"`
	// VoidReason names WHICH half of the binding moved. Empty unless the state is `void`.
	VoidReason string `json:"void_reason,omitempty"`
	// Run is the run as it stands after this decision.
	Run ImprovementRunView `json:"run"`
}

func improvementDecisionView(r improvementrun.Run, d improvementrun.Decision) ImprovementDecisionView {
	return ImprovementDecisionView{
		ProposalID: d.ProposalID, State: d.State.String(), By: d.By, AtMS: d.AtMS,
		Sentence: d.Sentence(), VoidReason: d.VoidReason, Run: improvementRunView(r),
	}
}

// ── view rendering ───────────────────────────────────────────────────────────────────────────────

// ImprovementPlanViewOf renders a plan as the console receives it. Exported for the proof binary, which
// runs the whole pipeline against a real repository and must show what the console would show — a proof
// that rendered its own version of the view would prove nothing about the view.
func ImprovementPlanViewOf(p improvementrun.Plan) ImprovementPlanView { return improvementPlanView(p) }

// ImprovementRunViewOf renders a run as the console receives it. Exported for the same one caller.
func ImprovementRunViewOf(r improvementrun.Run) ImprovementRunView { return improvementRunView(r) }

func improvementPlanView(p improvementrun.Plan) ImprovementPlanView {
	axes := make([]string, 0, len(p.Axes))
	for _, a := range p.Axes {
		axes = append(axes, a.String())
	}
	return ImprovementPlanView{
		PlanID: p.PlanID, Origin: p.Origin.String(), Question: p.Question,
		WorkflowID: p.WorkflowID, SourceRevision: p.SourceRevision,
		SourceRevisionShort:     shortConfigHash(p.SourceRevision),
		Axes:                    axes,
		CandidateCap:            p.CandidateCap,
		SpendBudgetUSD:          p.SpendBudgetUSD,
		ProjectedSpendUSD:       p.ProjectedSpendUSD,
		DisclosureThresholdUSD:  improvementrun.DisclosureThresholdUSD,
		RequiresAcknowledgement: p.RequiresAcknowledgement(),
		MinImprovement:          p.Stopping.MinImprovement,
		StoppingLabel:           improvementrun.BoundStoppingCondition.Sentence(),
		CreatedAtMS:             p.CreatedAtMS,
	}
}

func improvementRunView(r improvementrun.Run) ImprovementRunView {
	out := ImprovementRunView{
		RunID: r.RunID, Plan: improvementPlanView(r.Plan),
		Bound: r.Outcome.Bound.String(), BoundSentence: r.Outcome.Sentence(),
		Fault:             r.Outcome.Fault,
		Proposals:         []ImprovementProposalView{},
		Decisions:         map[string]ImprovementDecisionSummary{},
		Withdrawals:       []ImprovementWithdrawalView{},
		Deliveries:        []ImprovementDeliveryView{},
		PerAxis:           []AxisStageView{},
		SpendUSD:          r.SpendUSD,
		WithdrawnSpendUSD: r.WithdrawnSpendUSD,
		StartedAtMS:       r.StartedAtMS, FinishedAtMS: r.FinishedAtMS,
	}
	for _, p := range r.Proposals {
		out.Proposals = append(out.Proposals, ImprovementProposalView{
			ProposalID: p.ProposalID, Axis: p.Axis.String(), Node: p.Node,
			Operator: p.Operator, Rationale: p.Rationale,
			ConfigHash: p.ConfigHash, ConfigHashShort: shortConfigHash(p.ConfigHash),
			SourceRevision: p.SourceRevision,
			DeltaLabel:     p.DeltaLabel(),
			DeltaMean:      p.Delta.Mean, DeltaLow: p.Delta.Low, DeltaHigh: p.Delta.High,
			NSeeds: p.Delta.NSeeds, NCases: p.Delta.NCases,
			Significant: p.Significant, HeldOut: p.HeldOut,
			EvalSetCannotFail:    p.EvalSetCannotFail,
			CostDelta:            p.CostDelta,
			LatencyDelta:         p.LatencyDelta,
			DiffRef:              p.DiffRef,
			DiffStat:             p.DiffStat,
			ProviderModelVersion: p.ProviderModelVersion,
		})
	}
	for _, p := range r.Proposals {
		// 🔴 EVERY surfaced proposal gets a decision entry, defaulting to `pending`. A missing entry
		// would leave a console switching on an absent value and rendering a blank card, which a reader
		// cannot tell from a proposal that was never made.
		d := r.DecisionFor(p.ProposalID)
		out.Decisions[p.ProposalID] = ImprovementDecisionSummary{
			State: d.State.String(), By: d.By, AtMS: d.AtMS,
			Sentence: d.Sentence(), VoidReason: d.VoidReason,
		}
	}
	for _, w := range r.Withdrawals {
		out.Withdrawals = append(out.Withdrawals, ImprovementWithdrawalView{
			ProposalID: w.ProposalID, Axis: w.Axis.String(), Reason: w.Reason.String(),
			AboutTheChange:  w.Reason.AboutTheChange(),
			VerifiedLabel:   w.Verified.Label(),
			RemeasuredLabel: w.Remeasured.Label(),
			Sentence:        w.Sentence(),
			SpendUSD:        w.SpendUSD, AtMS: w.AtMS,
		})
	}
	for _, d := range r.Deliveries {
		v := ImprovementDeliveryView{
			ProposalID: d.ProposalID, Axis: d.Axis.String(), DeliveryID: d.DeliveryID,
			PullRequestURL: d.PullRequestURL, PullRequestRef: d.PullRequestRef,
			Mode: d.Mode, Deduplicated: d.Deduplicated, AtMS: d.AtMS,
		}
		if d.Withheld != nil {
			v.WithheldKind = string(d.Withheld.Kind)
			v.WithheldDetail = d.Withheld.Detail
			v.WithheldNextAction = d.Withheld.NextAction
		}
		out.Deliveries = append(out.Deliveries, v)
	}
	for _, a := range r.PerAxis {
		out.PerAxis = append(out.PerAxis, AxisStageView{
			Axis: a.Axis.String(), InScope: a.InScope,
			Generated: a.Generated, Verified: a.Verified, Approved: a.Approved,
			Delivered: a.Delivered, Withdrawn: a.Withdrawn,
		})
	}
	if r.Empty != nil {
		out.Empty = &ImprovementEmptyView{
			State: r.Empty.State.String(), Headline: r.Empty.Headline, Detail: r.Empty.Detail,
			NextAction: r.Empty.NextAction, Healthy: r.Empty.Healthy,
		}
	}
	return out
}
