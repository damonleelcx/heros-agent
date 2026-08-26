package improvementrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// deliver.go is FR18–FR25 and tasks 5.1–5.7: the run's last step.
//
// # 🚫 What this file is NOT
//
// A delivery implementation. `internal/forgedelivery` opens pull requests, enforces the gate, the
// entitlement, the halt, the route and the per-repository bound, records the append-only entry, and
// refuses to merge below Autonomous. Every one of those is a place a new caller can go around a shipped
// safety property, so this file's entire job is to CALL it correctly and to add nothing.
//
// The things P35 does add, and only these:
//
//	the surface-scoped default   `forgedelivery.DefaultModeFor` (R3). Console runs use the hosted App;
//	                             CLI and CI runs keep CI-mediated delivery.
//	the origin gate              a SCHEDULED run never delivers (decisions.md D-35.3), refused
//	                             server-side before entitlement is consulted.
//	the cancellation point       the last safe point before the push (decisions.md D-35.6). After it,
//	                             delivery is committed and the reconciliation pass owns completion.
//	the evidence                 the axis, the node, the eval-set decisiveness and how to revert, which
//	                             `Evidence` gained for this phase (task 5.7).

// DeliveryResult is one delivered proposal.
type DeliveryResult struct {
	ProposalID string          `json:"proposal_id"`
	Axis       assessment.Axis `json:"axis"`
	DeliveryID string          `json:"delivery_id"`
	// PullRequestURL is carried ONLY when the delivery record carries it. 🚫 Never composed from a
	// repository name and a number: a URL the platform invented is a URL that 404s in a customer's
	// browser while looking exactly like one that works.
	PullRequestURL string `json:"pull_request_url"`
	PullRequestRef string `json:"pull_request_ref"`
	Mode           string `json:"mode"`
	// Deduplicated is true when this call returned the FIRST delivery rather than creating a second
	// one (FR20). 🔴 Reported rather than hidden: it is the observable half of idempotency, and a
	// deduplication rate of zero over a busy deployment means the path is never exercised.
	Deduplicated bool `json:"deduplicated"`
	// Withheld is the named condition when nothing was pushed, or nil.
	Withheld *forgedelivery.Withheld `json:"withheld,omitempty"`
	AtMS     int64                   `json:"at_ms"`
}

// Delivered reports whether a pull request exists for this result.
func (d DeliveryResult) Delivered() bool { return d.PullRequestRef != "" }

// ErrScheduledRunMayNotDeliver is decisions.md D-35.3, enforced server-side.
//
// 🔴 It is checked BEFORE entitlement, and the order is the decision. Checking it after would make
// "may this deliver" answerable by buying a plan — an entitlement is not consent, and a scheduled run
// has no person in it to give any.
var ErrScheduledRunMayNotDeliver = errors.New(
	"improvementrun: a scheduled run stops at proposals and does not deliver at any automation level; " +
		"delivery requires a per-proposal approval, and an approval requires a person")

// ErrNotApproved is returned when delivery is attempted for a proposal nobody approved.
var ErrNotApproved = errors.New(
	"improvementrun: this proposal has no approval, and delivery is downstream of one")

// ErrWithdrawn is returned when delivery is attempted for a change that was withdrawn (FR16).
var ErrWithdrawn = errors.New(
	"improvementrun: this change was withdrawn after re-measurement and is not delivered")

// ErrCancelled is returned when the run was cancelled at the delivery gate (FR28 / D-35.6). Nothing was
// pushed, because the push had not happened yet.
var ErrCancelled = errors.New("improvementrun: this run was cancelled before anything was pushed")

// Deliverer is the shipped delivery core. `*forgedelivery.Deliverer` satisfies it.
//
// 🔴 The interface names `Deliver` and nothing else, so this package cannot reach `Prepare` and
// `OpenFromPrepared` separately and re-assemble them in an order the shipped one does not use. The
// enforcement funnel is `Prepare`, and a caller that could step around it would be the bypass this
// whole phase is written against.
type Deliverer interface {
	Deliver(ctx context.Context, p forgedelivery.Proposal, route *forgedelivery.Route, forge forgedelivery.ForgeWriter) (
		forgedelivery.Result, error)
}

// RouteSource resolves a tenant's delivery route for a workflow, and the forge writer that mode needs.
//
// 🔴 The WRITER comes from here rather than from the Service, because the credential is bound to the
// writer (ADR-005) and a Service field holding one would make every run in the process carry it. In
// CI-mediated mode there is no writer on the platform at all, and that absence is what
// `TestTheCLIPathHoldsNoForgeCredential` asserts.
type RouteSource interface {
	// Route returns the configured route for a target, the writer for its mode, and ok=false when no
	// route is configured — a REPORTED state, never silence.
	Route(ctx context.Context, tenantID, workflowID string, mode forgedelivery.Mode) (
		route *forgedelivery.Route, forge forgedelivery.ForgeWriter, ok bool, err error)
}

// Cancelled reports whether a run has been cancelled. Consulted at the delivery gate, which is the LAST
// safe point before a push (decisions.md D-35.6).
type Cancelled func(runID string) bool

// Deliver opens the pull request for one approved, re-measured proposal.
//
// The order is the phase's whole safety argument, and every step is a refusal that costs nothing:
//
//  1. the origin may deliver at all            (scheduled runs never do — before entitlement)
//  2. the proposal was approved                 (delivery is downstream of consent)
//  3. it was not withdrawn                      (FR16 — a withdrawn change is not delivered)
//  4. the run is not cancelled                  (🔴 THE LAST SAFE POINT — nothing has been pushed yet)
//  5. a route and, for App mode, an installation (a withheld delivery keeps the verified diff)
//  6. `forgedelivery.Deliver`                    (the gate, entitlement, halt, bound, record, no merge)
//
// 🔴 Step 4 is where decisions.md D-35.6 lands. FR28 requires that a cancelled run leave nothing partial
// on the repository, and P12 forbids the platform from ever deleting a branch — enforced by
// `ForgeWriter` having no delete method. Neither rule moves: the push is made the last step and the
// cancellation check sits immediately before it, so a cancelled run never pushed and there is nothing
// to delete. The fence asserts the forge received no `EnsureBranch` call, which is a stronger and more
// observable claim than "no branch remains".
func (s *Service) Deliver(ctx context.Context, run *Run, proposalID string) (DeliveryResult, error) {
	if err := s.checkDeliver(); err != nil {
		return DeliveryResult{}, err
	}
	p, ok := run.proposal(proposalID)
	if !ok {
		return DeliveryResult{}, fmt.Errorf("%w: %s", ErrNotSurfaced, proposalID)
	}
	out := DeliveryResult{ProposalID: proposalID, Axis: p.Axis, AtMS: s.nowMS()}

	// 1 · The origin. Before entitlement, deliberately.
	if !run.Plan.Origin.MayDeliver() {
		return out, ErrScheduledRunMayNotDeliver
	}
	// 2 · Consent.
	if d := run.DecisionFor(proposalID); d.State != DecisionApproved {
		return out, fmt.Errorf("%w (its decision is %q)", ErrNotApproved, d.State)
	}
	// 3 · Re-measurement's verdict.
	if w, withdrawn := run.WithdrawalFor(proposalID); withdrawn {
		return out, fmt.Errorf("%w: %s", ErrWithdrawn, w.Reason)
	}
	// 4 · 🔴 THE LAST SAFE POINT. After this, delivery is committed and the reconciliation pass owns
	// completion; before it, nothing has been pushed.
	//
	// BOTH stops are read here: the customer's cancel and the operator's brake. An operator who arms the
	// platform switch mid-run must stop the act that reaches a repository, not only the act that started
	// one — which is why this is a second check point rather than a trusted result from `Propose`.
	if s.Cancelled != nil && s.Cancelled(run.RunID) {
		return out, ErrCancelled
	}
	if err := s.checkBrake(ctx, run.TenantID); err != nil {
		return out, err
	}

	mode, err := forgedelivery.DefaultModeFor(surfaceFor(run.Plan.Origin))
	if err != nil {
		return out, err
	}
	out.Mode = string(mode)

	route, forge, hasRoute, err := s.Routes.Route(ctx, run.TenantID, run.Plan.WorkflowID, mode)
	if err != nil {
		return out, fmt.Errorf("improvementrun: resolving the delivery route: %w", err)
	}
	if !hasRoute {
		// A reported condition with the verified diff kept, not silence.
		w := forgedelivery.ClassifyWithheld(proposalID, forgedelivery.ErrNoRoute)
		out.Withheld = &w
		s.withheld(ctx, run, p, w)
		return out, nil
	}

	res, derr := s.Deliveries.Deliver(ctx, forgedelivery.Proposal{
		TenantID: run.TenantID, ProposalID: proposalID,
		ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		Title:     fmt.Sprintf("%s on %s (%s)", p.Operator, p.Node, p.Axis),
		DiffPatch: p.DiffRef, DiffStat: p.DiffStat,
		// 🔴 ASSISTED, always. P35 opens pull requests; it does not merge (a stated non-goal), and
		// `forgedelivery.Prepare` computes `AllowMerge` only at `LevelAutonomous`. Requesting Assisted
		// is therefore not a policy this file enforces — it is a value that makes the merge branch
		// unreachable from here, which is the difference between a rule and a structure.
		Level:      entitlement.LevelAssisted,
		ConsoleRef: p.DiffRef,
	}, route, forge)
	if derr != nil {
		if forgedelivery.IsReportedCondition(derr) {
			w := forgedelivery.ClassifyWithheld(proposalID, derr)
			out.Withheld = &w
			s.withheld(ctx, run, p, w)
			return out, nil
		}
		return out, fmt.Errorf("improvementrun: delivering: %w", derr)
	}

	out.DeliveryID, out.PullRequestRef = res.DeliveryID, res.PR.Ref
	out.PullRequestURL = res.PR.URL
	out.Deduplicated = !res.Created

	kind := KindDeliveryOpened
	if out.Deduplicated {
		kind = KindDeliveryDeduplicated
	}
	if _, lerr := s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: kind, PlanID: run.Plan.PlanID,
		ProposalID: proposalID, ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		DeliveryID: out.DeliveryID, Axis: p.Axis, Actor: "run",
		Detail: out.PullRequestURL, AtMS: out.AtMS,
	}); lerr != nil {
		// 🔴 The pull request EXISTS at this point. Returning the error without the result would lose
		// the URL, and the reconciliation pass would open a second one — so the result travels with the
		// error, and the caller renders both.
		return out, fmt.Errorf("improvementrun: the pull request was opened at %s and the delivery "+
			"record could not be written: %w", out.PullRequestURL, lerr)
	}
	run.Deliveries = append(run.Deliveries, out)
	bump(run.PerAxis, p.Axis, StageDelivered, 1)
	if s.Metrics != nil {
		s.Metrics.Delivered(p.Axis)
		if out.Deduplicated {
			s.Metrics.Deduplicated()
		}
	}
	if out.Deduplicated {
		s.emit(eventname.DeliveryDeduplicated, map[string]any{"axis": p.Axis.String()})
	} else {
		s.emit(eventname.DeliveryPROpened, map[string]any{"axis": p.Axis.String(), "mode": out.Mode})
	}
	return out, nil
}

// withheld records a withheld delivery. 🔴 The verified diff stays available — nothing about the
// proposal is discarded, which is the spec's own requirement for the no-installation case.
func (s *Service) withheld(ctx context.Context, run *Run, p VerifiedProposal, w forgedelivery.Withheld) {
	if s.Metrics != nil {
		s.Metrics.Withheld()
	}
	_, _ = s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: KindCandidateRejected, PlanID: run.Plan.PlanID,
		ProposalID: p.ProposalID, ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		Axis: p.Axis, Actor: "run", Detail: "delivery withheld: " + string(w.Kind), AtMS: s.nowMS(),
	})
}

// surfaceFor maps a run origin onto the delivery surface. 🔴 A total function over the closed origin
// set, so a fourth origin added later is a compile-visible decision rather than a silent default.
func surfaceFor(o RunOrigin) forgedelivery.Surface {
	switch o {
	case OriginConsole:
		return forgedelivery.SurfaceConsole
	case OriginCI:
		return forgedelivery.SurfaceCI
	default:
		// 🔴 CLI, and `OriginScheduled` lands here too — which is safe because a scheduled run is
		// refused at step 1 of `Deliver` and never reaches this function. The safe mode is the one that
		// needs no platform credential.
		return forgedelivery.SurfaceCLI
	}
}

func (s *Service) checkDeliver() error {
	if err := s.checkPlan(); err != nil {
		return err
	}
	var missing []string
	if s.Deliveries == nil {
		missing = append(missing, "a deliverer (without it, a verified and approved change has nowhere to go)")
	}
	if s.Routes == nil {
		missing = append(missing, "a route source (without it, no target can be resolved)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrNotConfigured, missing)
	}
	return nil
}
