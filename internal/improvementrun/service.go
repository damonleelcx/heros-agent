package improvementrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/optimizer"
)

// service.go is the run: plan → generate → verify → surface. §4 adds approval and re-measurement, §5
// adds delivery, and they extend this type rather than introducing a second orchestrator.
//
// # The shape, and the one rule that decides it
//
// **Every irreversible step is preceded by a durable ledger entry, and a ledger that cannot write stops
// the step.** That is `optimizer.Controller.apply`'s write-ahead rule, applied to the run as a whole,
// and it is what makes design D6's reconciliation pass possible: a crash between two acts leaves a
// record of the first, so the pass can complete or clean up with no human step.
//
// # 🚫 What Service is not allowed to do
//
// Decide anything a shipped gate decides. It calls `optimizer.Controller` for the loop, `internal/proposal`
// for candidates, `internal/verification` (through the composed verifier) for the verdict,
// `internal/approval` for consent and `internal/forgedelivery` for delivery. Every one of those is a
// place where a P35-local shortcut would be invisible, so the type holds them as REQUIRED collaborators
// and refuses to run without one.

// BoundsSource supplies a tenant's entitlement-derived ceilings.
//
// 🚫 It takes no input from the request. `conversation.BudgetSource` states the reason and it is the
// same one: an envelope the person could influence is an envelope one question uses to spend a month's
// allowance, and the person typing the question is the one least able to price it.
type BoundsSource interface {
	BoundsFor(ctx context.Context, tenantID string) (Bounds, error)
}

// Enumeration is the delegate enumerator for one plan, plus the generation pass's own state.
//
// 🔴 The STATE travels with the enumerator, and that is the whole of FR7's plumbing. A source that
// returned only an enumerator would make "no candidates" and "no linked runs" the same event by the
// time the run saw it, which is the P30 defect re-created one layer up.
type Enumeration struct {
	// Enumerator is the delegate. Nil is legitimate when the pass found nothing.
	Enumerator optimizer.Enumerator
	// Targets are the attributed nodes to propose against.
	Targets []optimizer.Target
	// State is `proposalgen`'s own answer, by name.
	State EmptyState
	// BaselineSpecBytes is the live spec the loop starts from.
	BaselineSpecBytes []byte
	// EvalSetCaseIDs is the set the gate measures on.
	EvalSetCaseIDs []string
}

// EnumerationSource produces the candidate generation for one plan.
type EnumerationSource interface {
	Enumerate(ctx context.Context, p Plan) (Enumeration, error)
}

// ProposalReader reads a surfaced proposal back by id.
type ProposalReader interface {
	// Proposal returns one proposal. ok=false is "no such proposal", never an error.
	Proposal(ctx context.Context, tenantID, proposalID string) (VerifiedProposal, bool, error)
}

// ProposalRecorder turns a verified candidate into the durable artifacts a surface renders: the
// proposal id, the compiled diff reference, and the diff stat.
//
// It is a seam because compiling a candidate into a diff is `internal/hostedcompile`'s job and storing
// it is `internal/proposalstore`'s, and P35 owns neither.
type ProposalRecorder interface {
	Record(ctx context.Context, p Plan, runID string, cand optimizer.SearchCandidate, vr optimizer.VerifyResult) (
		proposalID, diffRef, diffStat, providerModelVersion string, evalSet *assessment.EvalSetReport, err error)
}

// Service runs improvement runs.
type Service struct {
	Bounds       BoundsSource
	Acks         AckStore
	Enumerations EnumerationSource
	Proposals    ProposalRecorder
	Ledger       Ledger
	Metrics      *Metrics

	// Verifier is the SHIPPED composed verifier, built by NewVerifier. Held as the interface so the
	// service cannot reach into it and reorder its stages.
	Verifier optimizer.Verifier
	// Repo, ChangeLedger and Locks are the loop's own substrate, passed through unchanged.
	Repo         optimizer.Repo
	ChangeLedger optimizer.ChangeLedger
	Locks        *optimizer.LockSet
	// ProposalReader reads a surfaced proposal back by id, so a run reconstructed from the ledger
	// carries its proposals.
	//
	// 🔴 A SEAM rather than a `runs` table, and that is `careful-table-creation`'s alternative taken
	// rather than dismissed. The ledger already records which proposals a run verified — id, axis,
	// config hash, source revision — and `internal/proposalstore` already holds the delta and the diff.
	// A `runs` table would be a projection of both, and when a projection disagrees with the record the
	// reconciliation pass reads, the projection is the one the console shows.
	//
	// Optional. A deployment without one gets a run view with an empty `Proposals` list and a populated
	// per-axis breakdown, which is the honest split: the breakdown IS a ledger fact and the delta is not.
	ProposalReader ProposalReader
	// Approvals is the consent primitive — `internal/approval`. 🚫 P35 adds no second approval path.
	Approvals ApprovalGate
	// Remeasure takes the second observation after a change is applied (FR15).
	Remeasure Remeasurer
	// Subject resolves what the proposal's subject IS NOW, so an approval can be voided when it moves
	// (FR13).
	//
	// 🔴 A function rather than a stored value: the plan's binding was resolved when the question was
	// asked, and the entire point of the check is that time has passed since then.
	//
	// It takes the PROPOSAL as well as the plan, because the two halves of a binding move for different
	// reasons and are resolved from different places. The source revision is the customer's tree and
	// moves on its own; the configuration hash identifies the candidate and is re-read from wherever
	// the proposal is held. An implementation that re-resolved only one half and echoed the other must
	// say which — see `internal/launch`, which does exactly that and explains why it is sound there.
	Subject func(ctx context.Context, p Plan, proposal VerifiedProposal) (Binding, error)

	// Deliveries is the shipped delivery core — `*forgedelivery.Deliverer`.
	Deliveries Deliverer
	// Routes resolves a tenant's delivery route and the writer its mode needs.
	Routes RouteSource
	// Brake is the PLATFORM's kill switch — the operator console's, set from outside this process
	// (task 9.3). It is separate from `Cancelled`, which is the customer's own stop, and the two are
	// wired to the same check points rather than to two that can drift.
	Brake OperatorBrake
	// Cancelled reports whether a run has been cancelled. Consulted at the delivery gate, which is the
	// LAST safe point before a push (decisions.md D-35.6). Nil means nothing can cancel, which is
	// correct for a deployment with no cancel control and is NOT the same as "never cancelled" — the
	// difference is that a nil here is a decision somebody made, visible in the wiring.
	Cancelled Cancelled

	// Contract is the typed I/O check. Held here as well as inside the verifier because
	// `RejectBeforeVerification` runs it at the run level too — see that function for why the
	// redundancy is the assertion rather than a duplicate check.
	Contract optimizer.ContractChecker

	// NewRunID mints a run id. Injected so a run is reproducible under test.
	NewRunID func(p Plan) string
	// Now is the injected clock. Nothing here reads the wall clock directly.
	Now func() time.Time
	// Observe emits central event names. Optional: telemetry is not a precondition for running.
	Observe func(eventname.Name, map[string]any)
}

// ErrNotConfigured is what a Service missing a collaborator returns, naming what is missing and what
// would go wrong without it. 🚫 Never a nil-pointer panic: a misconfiguration that takes down the
// process takes down every other tenant's turn too.
var ErrNotConfigured = errors.New("improvementrun: the service is missing a collaborator")

// checkPlan is what the PLAN phase needs: a bounds source, a ledger and a clock. Nothing else.
//
// # 🔴 Why this is separate from `check`, and why that is a requirement rather than convenience
//
// PRD §12 stage 1 is *"Plan only. A question produces a plan and stops. No generation, no spend."* — a
// rollout stage that exists so the translation and the budget disclosure can be validated BEFORE
// anything costs money. A single readiness check would make that stage unreachable: a deployment with
// no eval runner would refuse to produce a plan, and the stage designed to run without spending would
// require every collaborator that spends.
//
// So a deployment that cannot verify can still show a person what a run WOULD cost and touch, and
// `Propose` refuses with a named reason. That is the honest shape of a staged rollout, and it is the
// difference between "this feature is off" and "this feature is at stage 1".
func (s *Service) checkPlan() error {
	var missing []string
	if s.Bounds == nil {
		missing = append(missing, "a bounds source (without it, a plan would be built from defaults nobody agreed to)")
	}
	if s.Ledger == nil {
		missing = append(missing, "a run ledger (without it, a plan is offered and no record says it was)")
	}
	if s.Now == nil {
		missing = append(missing, "a clock")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrNotConfigured, missing)
	}
	return nil
}

func (s *Service) check() error {
	if err := s.checkPlan(); err != nil {
		return err
	}
	var missing []string
	if s.Enumerations == nil {
		missing = append(missing, "an enumeration source (without it, there are no candidates and no named reason why)")
	}
	if s.Proposals == nil {
		missing = append(missing, "a proposal recorder (without it, a verified candidate has no diff to review)")
	}
	if s.Verifier == nil {
		missing = append(missing, "a verifier (without it, nothing is measured and everything would be surfaced)")
	}
	if s.Repo == nil {
		missing = append(missing, "a repository (the loop cannot read the live configuration)")
	}
	if s.ChangeLedger == nil {
		missing = append(missing, "an optimizer change ledger (the loop fails closed without one)")
	}
	if s.Contract == nil {
		missing = append(missing, "a typed-contract checker (without it, a contract-violating candidate reaches verification)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: this deployment can produce a plan but cannot execute one, because it is "+
			"missing %v. 🔴 That is PRD §12 stage 1 rather than a broken deployment: the plan is shown "+
			"and nothing spends", ErrNotConfigured, missing)
	}
	return nil
}

func (s *Service) nowMS() int64 { return s.Now().UnixMilli() }

func (s *Service) emit(n eventname.Name, attrs map[string]any) {
	if s.Observe == nil {
		return
	}
	s.Observe(n, attrs)
}

// Plan translates a question into a bounded plan and records it. It executes NOTHING.
//
// 🔴 A separate method from `Propose`, and the separation is FR1/FR2 rather than API taste. The plan is
// the artifact a person may decline, so it has to be obtainable without starting anything — a surface
// that could only get a plan by starting a run would be showing a receipt and calling it a plan.
//
// The ledger entry is written HERE, before anything spends, so "how many runs were planned" and "how
// many ran" are separately answerable. A deployment where those diverge is one where the disclosure
// threshold is stopping people, which is a product signal and is invisible if only started runs count.
func (s *Service) Plan(ctx context.Context, tenantID, question string, origin RunOrigin) (Plan, error) {
	if err := s.checkPlan(); err != nil {
		return Plan{}, err
	}
	b, err := s.Bounds.BoundsFor(ctx, tenantID)
	if err != nil {
		return Plan{}, fmt.Errorf("improvementrun: this organization's run bounds could not be read, so "+
			"no plan can be built: %w", err)
	}
	b.TenantID, b.Origin, b.NowMS = tenantID, origin, s.nowMS()
	p, err := Translate(question, b)
	if err != nil {
		return Plan{}, err
	}
	if _, err := s.Ledger.Append(ctx, Entry{
		RunID: "plan:" + p.PlanID, TenantID: tenantID, Kind: KindPlanCreated,
		PlanID: p.PlanID, SourceRevision: p.SourceRevision,
		WorkflowID: p.WorkflowID, Origin: p.Origin, Axes: p.Axes,
		Actor: "run", AtMS: p.CreatedAtMS,
		Detail: fmt.Sprintf("%d axes, cap %d, budget $%.2f, projected $%.2f",
			len(p.Axes), p.CandidateCap, p.SpendBudgetUSD, p.ProjectedSpendUSD),
	}); err != nil {
		return Plan{}, fmt.Errorf("improvementrun: the plan could not be recorded, so it is not "+
			"offered: %w", err)
	}
	s.emit(eventname.RunPlanCreated, map[string]any{
		"origin": origin.String(), "axes": len(p.Axes), "requires_acknowledgement": p.RequiresAcknowledgement(),
	})
	if s.Metrics != nil {
		s.Metrics.PlanCreated(p)
	}
	return p, nil
}

// Propose executes the plan: it drives the shipped loop under the plan's bounds, surfaces only
// P5.5-verified candidates, and reports which bound stopped it.
//
// 🔴 It refuses an unacknowledged plan above the disclosure threshold BEFORE any collaborator is
// touched, so a refused run costs nothing — which is the only way FR2's guarantee is real rather than
// procedural.
func (s *Service) Propose(ctx context.Context, p Plan) (Run, error) {
	if err := s.check(); err != nil {
		return Run{}, err
	}
	if err := p.Validate(); err != nil {
		return Run{}, err
	}
	if err := RequireAcknowledgement(ctx, s.Acks, p); err != nil {
		return Run{}, err
	}
	// 🔴 The operator's brake, BEFORE anything is enumerated, so a halted run costs nothing. It is
	// checked again at the delivery gate — the same two points `optimizer.Controller` checks its own
	// kill switch at — because a switch thrown mid-run must stop the act that reaches a repository, not
	// only the act that started it.
	if err := s.checkBrake(ctx, p.TenantID); err != nil {
		return Run{}, err
	}

	runID := s.runID(p)
	run := Run{
		RunID: runID, TenantID: p.TenantID, Plan: p,
		Proposals: []VerifiedProposal{}, PerAxis: axisIndex(p),
		StartedAtMS: s.nowMS(),
	}
	if s.Metrics != nil {
		s.Metrics.RunStarted()
	}

	// 🔴 The run records the plan it is running under, in its OWN stream. The entry `Plan` wrote lives
	// under `plan:<id>` and answers a different question — "how many plans were offered", which is the
	// disclosure-threshold signal and must count plans nobody ran. This one answers "what did THIS run
	// run under", and it is what `Service.Run` reads to reconstruct a run's subject and origin.
	//
	// Written BEFORE anything is enumerated, so a run that crashes during generation is still
	// attributable to a plan rather than being an orphan the reconciliation pass cannot classify.
	if _, err := s.Ledger.Append(ctx, Entry{
		RunID: runID, TenantID: p.TenantID, Kind: KindPlanCreated, PlanID: p.PlanID,
		WorkflowID: p.WorkflowID, Origin: p.Origin, Axes: p.Axes, SourceRevision: p.SourceRevision,
		Actor: "run", AtMS: run.StartedAtMS,
		Detail: fmt.Sprintf("%d axes, cap %d, budget $%.2f", len(p.Axes), p.CandidateCap, p.SpendBudgetUSD),
	}); err != nil {
		return run, fmt.Errorf("improvementrun: the run could not record the plan it is running under, "+
			"so it did not start — a run the reconciliation pass cannot attribute to a plan is one it "+
			"cannot complete: %w", err)
	}

	enum, err := s.Enumerations.Enumerate(ctx, p)
	if err != nil {
		return s.faulted(ctx, run, "the candidate generation pass could not run: "+err.Error())
	}
	if enum.Enumerator == nil || len(enum.Targets) == 0 {
		// 🔴 NOT an empty result. The pass's own named state travels, so a customer with no linked runs
		// and a customer whose workflow is healthy read different sentences and take different actions.
		st := enum.State
		run.Empty = &st
		run.FinishedAtMS = s.nowMS()
		run.Outcome = Outcome{Bound: BoundStoppingCondition, State: optimizer.StateStalled,
			Detail: st.Headline}
		if s.Metrics != nil {
			s.Metrics.RunFinished(run)
		}
		return run, nil
	}

	bounded := NewBoundedEnumerator(p, enum.Enumerator)
	kill := optimizer.NewKillSwitch()
	ctrl := &optimizer.Controller{
		Search:   optimizer.Search{Enum: bounded},
		Verifier: &recordingVerifier{inner: s.Verifier, contract: s.Contract},
		Repo:     s.Repo,
		Ledger:   s.ChangeLedger,
		Kill:     kill,
		Locks:    s.Locks,
		Clock:    s.Now,
	}
	rec := ctrl.Verifier.(*recordingVerifier)

	res, err := ctrl.Run(ctx, optimizer.RunInput{
		Authority: optimizer.Authority{
			RunID: runID, WorkflowID: p.WorkflowID, Actor: "improvement-run",
			CustomerID: p.TenantID, Constraints: p.Constraints(),
			// 🚫 The three merge prerequisites are deliberately NOT armed. P35 opens pull requests; it
			// does not merge (a stated non-goal), and delivery is `internal/forgedelivery`'s, downstream
			// of an approval this loop knows nothing about. Arming them here would give the loop a merge
			// path that bypasses the approval gate entirely.
			KillSwitchArmed: false, AuditArmed: false, RollbackArmed: false,
			GrantedAt: s.Now(),
		},
		Targets:           enum.Targets,
		BaselineSpecBytes: enum.BaselineSpecBytes,
		EvalSetCaseIDs:    enum.EvalSetCaseIDs,
	})
	if err != nil {
		return s.faulted(ctx, run, "the optimization loop could not start: "+err.Error())
	}

	run.Outcome = OutcomeOf(res, kill.Fired())
	// 🔴 THERE IS NO CAP-OVER-STOPPING-CONDITION OVERRIDE HERE, and its absence is a decision the
	// fence drill forced.
	//
	// One was written: *if the enumerator hit its cap and the loop reported a stopping condition,
	// report the cap instead* — on the reasoning that a truncated run read as converged is the one
	// direction that stops somebody raising the cap. `make p35-fence-redcheck` then showed it could not
	// be made to fail, because it is UNREACHABLE: `Plan.Constraints` maps `CandidateCap` onto
	// `MaxIterations` exactly, and the loop consumes exactly one candidate per iteration
	// (`TestOneIterationConsumesOneCandidate`), so the loop's own `max_iter` stop always fires first.
	//
	// It is deleted rather than kept as harmless, because if it ever DID become reachable it would be
	// wrong: reaching it would mean the loop exhausted candidates before its iteration bound, which is
	// a genuinely exhausted search — and relabelling that as "truncated by the cap" would send somebody
	// to raise a cap that was not the constraint. The cap arrives as `StateMaxIter` → `BoundCandidateCap`
	// through `OutcomeOf`, which is where it belongs.
	run.SpendUSD = res.CumulativeSpend

	// 🔴 One ledger entry per ADMITTED candidate, not a per-axis count. FR29 requires the ledger to
	// record the candidates, and `Service.Run` rebuilds the per-axis breakdown from these entries — a
	// run reconstructed from the record would otherwise show `generated: 0` beside `verified: 3`, which
	// reads as candidates that appeared from nowhere. A total cannot be replayed into a breakdown.
	for _, hash := range bounded.AdmittedHashes() {
		axis := bounded.AxisOf(hash)
		if _, err := s.Ledger.Append(ctx, Entry{
			RunID: runID, TenantID: p.TenantID, Kind: KindCandidateGenerated, PlanID: p.PlanID,
			ConfigHash: hash, SourceRevision: p.SourceRevision, Axis: assessment.Axis(axis),
			Actor: "run", AtMS: s.nowMS(),
		}); err != nil {
			return run, fmt.Errorf("improvementrun: a generated candidate could not be recorded: %w", err)
		}
		bump(run.PerAxis, assessment.Axis(axis), StageGenerated, 1)
	}

	for _, v := range rec.verified {
		proposalID, diffRef, diffStat, modelVersion, evalSet, err := s.Proposals.Record(ctx, p, runID, v.cand, v.result)
		if err != nil {
			// A candidate that passed the gate and could not be recorded is NOT surfaced: a card with no
			// diff asks somebody to approve a change whose bytes do not exist.
			s.rejected(ctx, run, v.cand, "verified, and its diff could not be compiled: "+err.Error())
			continue
		}
		vp, err := NewVerifiedProposal(runID, p, v.cand, v.result, proposalID, diffRef, diffStat, modelVersion, evalSet)
		if err != nil {
			s.rejected(ctx, run, v.cand, err.Error())
			continue
		}
		if _, lerr := s.Ledger.Append(ctx, Entry{
			RunID: runID, TenantID: p.TenantID, Kind: KindCandidateVerified,
			PlanID: p.PlanID, ProposalID: vp.ProposalID, ConfigHash: vp.ConfigHash,
			SourceRevision: vp.SourceRevision, Axis: vp.Axis, Actor: "run",
			Detail: vp.DeltaLabel(), AtMS: s.nowMS(),
		}); lerr != nil {
			return run, fmt.Errorf("improvementrun: a verified candidate could not be recorded, so it is "+
				"not surfaced: %w", lerr)
		}
		run.Proposals = append(run.Proposals, vp)
		bump(run.PerAxis, vp.Axis, StageVerified, 1)
		s.emit(eventname.RunCandidateVerified, map[string]any{
			"axis": vp.Axis.String(), "significant": vp.Significant, "held_out": vp.HeldOut,
		})
	}

	if len(run.Proposals) == 0 && run.Empty == nil {
		st := enum.State
		run.Empty = &st
	}
	run.FinishedAtMS = s.nowMS()
	if err := s.recordOutcome(ctx, run); err != nil {
		return run, err
	}
	if s.Metrics != nil {
		s.Metrics.RunFinished(run)
	}
	return run, nil
}

func (s *Service) runID(p Plan) string {
	if s.NewRunID != nil {
		return s.NewRunID(p)
	}
	return "run_" + p.PlanID
}

// faulted records a run that ended on a dependency failure. 🚫 Never as a bound.
func (s *Service) faulted(ctx context.Context, run Run, detail string) (Run, error) {
	run.Outcome = Outcome{Fault: detail, State: optimizer.StateStopped, Detail: detail}
	run.FinishedAtMS = s.nowMS()
	if _, err := s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: KindRunFaulted,
		PlanID: run.Plan.PlanID, Actor: "run", Detail: detail, AtMS: run.FinishedAtMS,
	}); err != nil {
		return run, fmt.Errorf("improvementrun: the run faulted (%s) and the fault could not be "+
			"recorded: %w", detail, err)
	}
	if s.Metrics != nil {
		s.Metrics.RunFinished(run)
	}
	return run, nil
}

// rejected records a candidate that was refused. A refusal is information — "the engine considered this
// and said no" — and is never silently dropped.
func (s *Service) rejected(ctx context.Context, run Run, cand optimizer.SearchCandidate, detail string) {
	_, _ = s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: KindCandidateRejected,
		PlanID: run.Plan.PlanID, ConfigHash: cand.ConfigHash, Axis: assessment.Axis(cand.Dimension),
		Actor: "run", Detail: detail, AtMS: s.nowMS(),
	})
}

func (s *Service) recordOutcome(ctx context.Context, run Run) error {
	kind := KindRunBounded
	switch {
	case run.Outcome.Faulted():
		kind = KindRunFaulted
	case run.Outcome.Bound == BoundKillSwitch:
		kind = KindRunCancelled
	}
	detail := run.Outcome.Bound.String()
	if run.Outcome.Faulted() {
		detail = run.Outcome.Fault
	}
	if _, err := s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: kind, PlanID: run.Plan.PlanID,
		Actor: "run", Detail: detail, SpendUSD: run.SpendUSD, AtMS: run.FinishedAtMS,
	}); err != nil {
		return fmt.Errorf("improvementrun: the run's outcome could not be recorded: %w", err)
	}
	return nil
}

// recordingVerifier wraps the shipped verifier so the run can see WHICH candidates passed, without
// re-deriving it from the loop's iterations.
//
// 🔴 It re-runs the typed-contract check FIRST, before delegating. That is not a duplicate of the
// composed verifier's own check — it is `RejectBeforeVerification` at the run's own boundary, and it is
// here because P35 is a NEW CALLER: the composed verifier's ordering is proven for the optimizer, and
// this asserts the conversational path reaches it. A candidate refused here never reaches the inner
// verifier, so it costs no provider call and produces no verdict row.
type recordingVerifier struct {
	inner    optimizer.Verifier
	contract optimizer.ContractChecker
	verified []verifiedCandidate
}

type verifiedCandidate struct {
	cand   optimizer.SearchCandidate
	result optimizer.VerifyResult
}

func (v *recordingVerifier) Verify(ctx context.Context, req optimizer.VerifyRequest) (optimizer.VerifyResult, error) {
	if err := RejectBeforeVerification(v.contract, req.Candidate); err != nil {
		// Reported as a contract failure on the RESULT rather than as an error: an error stops the whole
		// run (the loop treats a verifier error as "verification unavailable"), and one bad candidate
		// must not end a run over eight good ones.
		return optimizer.VerifyResult{ContractOK: false, ContractReason: err.Error()}, nil
	}
	res, err := v.inner.Verify(ctx, req)
	if err != nil {
		return res, err
	}
	if res.MergeReady() {
		v.verified = append(v.verified, verifiedCandidate{cand: req.Candidate, result: res})
	}
	return res, nil
}

// Acknowledge records a person's agreement to a plan above the disclosure threshold.
//
// 🔴 It is on the Service rather than on the store so the ONE call site that records consent is the one
// that also emits and counts it. A surface writing straight to the store would produce consent nobody
// observed, and "how many plans were shown and never acknowledged" is the product signal that says
// whether the threshold is set right.
func (s *Service) Acknowledge(ctx context.Context, a Acknowledgement) error {
	if s.Acks == nil {
		return fmt.Errorf("%w: no acknowledgement store is configured, so no plan above the disclosure "+
			"threshold can ever run", ErrNotConfigured)
	}
	return s.Acks.Record(ctx, a)
}

// Run reads a run back from the ledger.
//
// 🔴 RECONSTRUCTED from the append-only record rather than read from a second "runs" table, and that is
// `careful-table-creation`'s alternative taken rather than dismissed. The ledger already holds every
// fact a run view needs — the plan, the verified candidates with their axes, the decisions, the
// deliveries — and a second table would be a projection that can disagree with the record the
// reconciliation pass reads. When they disagree, the projection is the one the console shows.
//
// ⚠️ What it deliberately does NOT reconstruct is the verified DELTA and the diff reference: those live
// on the proposal rows `ProposalRecorder` wrote, and re-deriving them here would be a second decoder
// for the same bytes. A caller that needs them reads the proposal store. `run.Proposals` is therefore
// empty on a re-read, and `PerAxis` is not — which is the honest split, because the per-axis breakdown
// IS a ledger fact and the delta is not.
func (s *Service) Run(ctx context.Context, tenantID, runID string) (Run, bool, error) {
	if s.Ledger == nil {
		return Run{}, false, fmt.Errorf("%w: no run ledger", ErrNotConfigured)
	}
	entries, err := s.Ledger.Entries(ctx, runID)
	if err != nil {
		return Run{}, false, err
	}
	if len(entries) == 0 {
		return Run{}, false, nil
	}
	if entries[0].TenantID != tenantID {
		// 🚫 Reported as "no such run", never as "forbidden". A 403 discloses that the run exists, which
		// is the cross-tenant probe `internal/api`'s own fences refuse elsewhere.
		return Run{}, false, nil
	}

	run := Run{
		RunID: runID, TenantID: tenantID,
		Proposals: []VerifiedProposal{}, Decisions: map[string]Decision{},
		// The breakdown is rebuilt from the ledger's own `Axis` field rather than from the proposals,
		// because a run that was INTERRUPTED has entries and may have no readable proposals — and "which
		// axes did this run reach" must still be answerable. It is why `Entry` carries `Axis` at all.
		//
		// 🔴 Seeded with EVERY axis and replaced by the plan's real scope the moment the `plan_created`
		// entry is read (below). The seed exists only so a ledger with no plan entry — which `Validate`
		// makes impossible, but a hand-written row could produce — still has rows to bump rather than
		// dropping counts silently.
		PerAxis: axisIndex(Plan{Axes: assessment.Axes()}),
	}
	for _, e := range entries {
		if run.StartedAtMS == 0 || e.AtMS < run.StartedAtMS {
			run.StartedAtMS = e.AtMS
		}
		if e.AtMS > run.FinishedAtMS {
			run.FinishedAtMS = e.AtMS
		}
		run.SpendUSD += e.SpendUSD
		switch e.Kind {
		case KindRunBounded:
			run.Outcome.Bound = Bound(e.Detail)
		case KindRunCancelled:
			run.Outcome.Bound = BoundKillSwitch
		case KindRunFaulted:
			run.Outcome.Fault = e.Detail
		case KindPlanCreated:
			run.Plan.PlanID, run.Plan.SourceRevision = e.PlanID, e.SourceRevision
			run.Plan.TenantID = e.TenantID
			// 🔴 The workflow and the ORIGIN. Without them a reconstructed run delivers on the wrong
			// surface — see `Entry.Origin` for the defect this closes and how it was found.
			run.Plan.WorkflowID, run.Plan.Origin = e.WorkflowID, e.Origin
			// 🔴 The SCOPE, and the per-axis rows are rebuilt from it. Assuming all nine would make
			// every axis the plan excluded render as a measured zero — see `Entry.Axes`.
			run.Plan.Axes = append([]assessment.Axis(nil), e.Axes...)
			run.PerAxis = axisIndex(run.Plan)
		case KindCandidateGenerated:
			bump(run.PerAxis, e.Axis, StageGenerated, 1)
		case KindCandidateVerified:
			bump(run.PerAxis, e.Axis, StageVerified, 1)
			if s.ProposalReader != nil {
				p, ok, perr := s.ProposalReader.Proposal(ctx, tenantID, e.ProposalID)
				if perr != nil {
					return Run{}, false, fmt.Errorf("improvementrun: reading proposal %s: %w", e.ProposalID, perr)
				}
				if ok {
					run.Proposals = append(run.Proposals, p)
				}
			}
		case KindProposalApproved:
			bump(run.PerAxis, e.Axis, StageApproved, 1)
			run.setDecision(Decision{
				ProposalID: e.ProposalID, Axis: e.Axis, State: DecisionApproved, By: e.Actor, AtMS: e.AtMS,
				Binding: Binding{ConfigHash: e.ConfigHash, SourceRevision: e.SourceRevision},
			})
		case KindProposalDeclined:
			// 🔴 A `void` decision is also recorded under this kind — the ledger has no separate one,
			// because void and declined are both "this consent does not authorize anything" from the
			// record's point of view. The DETAIL distinguishes them, and the surface renders two
			// different sentences from it.
			state := DecisionDeclined
			if DecisionState(e.Detail) == DecisionVoid {
				state = DecisionVoid
			}
			run.setDecision(Decision{
				ProposalID: e.ProposalID, Axis: e.Axis, State: state, By: e.Actor, AtMS: e.AtMS,
				Binding: Binding{ConfigHash: e.ConfigHash, SourceRevision: e.SourceRevision},
			})
		case KindChangeWithdrawn:
			bump(run.PerAxis, e.Axis, StageWithdrawn, 1)
		case KindDeliveryOpened:
			bump(run.PerAxis, e.Axis, StageDelivered, 1)
		}
	}
	return run, true, nil
}

// ── §4 · approval and re-measurement ─────────────────────────────────────────────────────────────

// Approve records a person's approval of ONE proposal and then does the work approving it authorizes:
// apply, re-measure, and — when the change fails to reproduce — withdraw it before delivery.
//
// # 🚫 One proposal. There is no plural form, and that is design D4 rather than an API shape
//
// A bundle approval is one click that means several things, and the person will read the first item and
// accept the rest. The pressure to add "approve all" is the most predictable request in this phase, so
// the refusal is structural: this method takes one id and `TestNoBulkApprovalPathExists` asserts by
// reflection that nothing exported here takes a list of them.
//
// # The order, and why the binding is checked FIRST
//
//  1. the proposal was surfaced by THIS run          (an approval for something else is not an approval)
//  2. the binding still holds                        (FR13 — else void and re-request, spending nothing)
//  3. `internal/approval` records the consent        (the only approval path)
//  4. apply, then RE-MEASURE                         (FR15)
//  5. reconcile — withdraw, or hand to delivery      (FR16)
//
// Checking the binding before the consent is what makes a void approval cost nothing: the person is
// asked again before any provider call is made against a subject that has moved.
func (s *Service) Approve(ctx context.Context, run *Run, proposalID, approvedBy string) (Decision, error) {
	if err := s.checkApprove(); err != nil {
		return Decision{}, err
	}
	if approvedBy == "" {
		// 🔴 Refused rather than defaulted, before anything else. `approval.Approve` refuses it too, and
		// this refusal is not a duplicate of that one — it stops the binding check and the apply from
		// running at all for a decision that could never have been recorded.
		return Decision{}, errors.New("improvementrun: an approval must name the person who gave it; a " +
			"row that records a decision and cannot say who made it is worse than no row, because it is " +
			"believed")
	}
	p, ok := run.proposal(proposalID)
	if !ok {
		return Decision{}, fmt.Errorf("%w: %s", ErrNotSurfaced, proposalID)
	}

	want := Binding{ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision}
	current, err := s.currentBinding(ctx, run.Plan, p)
	if err != nil {
		return Decision{}, err
	}
	if !want.Equal(current) {
		d := Decision{
			ProposalID: proposalID, Axis: p.Axis, State: DecisionVoid, By: approvedBy,
			AtMS: s.nowMS(), Binding: want, VoidReason: voidReasonFor(want, current),
		}
		s.recordDecision(ctx, run, d, KindProposalDeclined)
		run.setDecision(d)
		return d, fmt.Errorf("%w: %s", ErrApprovalVoid, d.VoidReason)
	}

	gateID, err := s.Approvals.Submit(ctx, run.TenantID, p)
	if err != nil {
		return Decision{}, fmt.Errorf("improvementrun: this proposal could not be queued for approval: %w", err)
	}
	if err := s.Approvals.Approve(ctx, gateID, approvedBy); err != nil {
		return Decision{}, fmt.Errorf("improvementrun: the approval was not recorded: %w", err)
	}

	d := Decision{
		ProposalID: proposalID, Axis: p.Axis, State: DecisionApproved, By: approvedBy,
		AtMS: s.nowMS(), Binding: want,
	}
	if err := s.recordDecision(ctx, run, d, KindProposalApproved); err != nil {
		return Decision{}, err
	}
	run.setDecision(d)
	bump(run.PerAxis, p.Axis, StageApproved, 1)
	if s.Metrics != nil {
		s.Metrics.Approved(p.Axis)
	}

	// 🔴 The apply is recorded BEFORE it happens. That ordering is the whole of design D6's
	// reconciliation: a crash between applying and delivering leaves a `change_applied` entry with no
	// delivery beside it, which is exactly what `Ledger.Unresolved` looks for.
	if _, err := s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: KindChangeApplied, PlanID: run.Plan.PlanID,
		ProposalID: p.ProposalID, ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		Axis: p.Axis, Actor: approvedBy, AtMS: s.nowMS(),
	}); err != nil {
		return d, fmt.Errorf("improvementrun: the change could not be recorded before applying, so it "+
			"was not applied — an unrecorded apply is one the reconciliation pass cannot repair: %w", err)
	}

	if err := s.remeasureAndReconcile(ctx, run, p); err != nil {
		return d, err
	}
	return d, nil
}

// Decline records a refusal. 🔴 The run CONTINUES and the proposal STAYS VISIBLE (FR12): one no is not
// a cancel, and a proposal that vanished when it was declined looks like one that was never made.
func (s *Service) Decline(ctx context.Context, run *Run, proposalID, declinedBy string) (Decision, error) {
	if err := s.checkApprove(); err != nil {
		return Decision{}, err
	}
	if declinedBy == "" {
		return Decision{}, errors.New("improvementrun: a decline must name the person who gave it")
	}
	p, ok := run.proposal(proposalID)
	if !ok {
		return Decision{}, fmt.Errorf("%w: %s", ErrNotSurfaced, proposalID)
	}
	gateID, err := s.Approvals.Submit(ctx, run.TenantID, p)
	if err != nil {
		return Decision{}, fmt.Errorf("improvementrun: this proposal could not be queued for review: %w", err)
	}
	if err := s.Approvals.Decline(ctx, gateID, declinedBy); err != nil {
		return Decision{}, fmt.Errorf("improvementrun: the decline was not recorded: %w", err)
	}
	d := Decision{
		ProposalID: proposalID, Axis: p.Axis, State: DecisionDeclined, By: declinedBy,
		AtMS: s.nowMS(), Binding: Binding{ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision},
	}
	if err := s.recordDecision(ctx, run, d, KindProposalDeclined); err != nil {
		return Decision{}, err
	}
	run.setDecision(d)
	if s.Metrics != nil {
		s.Metrics.Declined(p.Axis)
	}
	return d, nil
}

// remeasureAndReconcile takes the second observation and decides whether the change may proceed.
func (s *Service) remeasureAndReconcile(ctx context.Context, run *Run, p VerifiedProposal) error {
	want := Binding{ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision}
	verified := Measurement{
		Delta: p.Delta, Significant: p.Significant,
		ProviderModelVersion: p.ProviderModelVersion,
		ResolvedConfigHash:   p.ConfigHash, SourceRevision: p.SourceRevision,
	}
	remeasured, rerr := s.Remeasure.Remeasure(ctx, p, want)
	w, err := Reconcile(p, verified, remeasured, rerr, s.nowMS())
	if err != nil {
		return err
	}
	if w == nil {
		// It reproduced. Delivery is §5's, and it is the caller's next step.
		return nil
	}
	// 🔴 The re-measurement's own spend, reported by the runner. Zero when it never ran, which is the
	// truth in that case rather than an estimate — decisions.md D-35.4 charges this against the run's
	// budget and bills none of it, and both halves need a measured number.
	w.SpendUSD = remeasured.SpendUSD
	run.WithdrawnSpendUSD += w.SpendUSD
	run.SpendUSD += w.SpendUSD
	if _, lerr := s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: KindChangeWithdrawn, PlanID: run.Plan.PlanID,
		ProposalID: p.ProposalID, ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		Axis: p.Axis, Actor: "run", Detail: string(w.Reason), AtMS: w.AtMS,
	}); lerr != nil {
		return fmt.Errorf("improvementrun: the withdrawal could not be recorded, and an unrecorded "+
			"withdrawal is one the reconciliation pass would try to deliver: %w", lerr)
	}
	run.Withdrawals = append(run.Withdrawals, *w)
	bump(run.PerAxis, p.Axis, StageWithdrawn, 1)
	if s.Metrics != nil {
		s.Metrics.Withdrawn(p.Axis)
	}
	s.emit(eventname.RunChangeWithdrawn, map[string]any{
		"axis": p.Axis.String(), "reason": w.Reason.String(),
		"about_the_change": w.Reason.AboutTheChange(),
	})
	return nil
}

// currentBinding resolves what the subject is NOW, so FR13's void check has something to compare to.
//
// 🔴 It reads the SUBJECT source rather than trusting the plan, because the plan was resolved when the
// question was asked and the whole point of the check is that time has passed since then.
func (s *Service) currentBinding(ctx context.Context, plan Plan, proposal VerifiedProposal) (Binding, error) {
	if s.Subject == nil {
		// 🚫 Not "assume it held". Without a way to ask what the subject is now, an approval could not be
		// voided when it moved — and the failure would be silent and in the dangerous direction.
		return Binding{}, fmt.Errorf("%w: no subject resolver, so an approval could never be voided when "+
			"its revision moves", ErrNotConfigured)
	}
	b, err := s.Subject(ctx, plan, proposal)
	if err != nil {
		return Binding{}, fmt.Errorf("improvementrun: resolving what this proposal's subject is now: %w", err)
	}
	if !b.Complete() {
		// 🔴 An incomplete answer is a REFUSAL, not a pass. A resolver that could not read the current
		// revision would otherwise return a zero binding, which never equals the proposal's — voiding
		// every approval — or, worse under a different implementation, be treated as a wildcard.
		return Binding{}, fmt.Errorf("improvementrun: the subject resolver returned an incomplete "+
			"binding (%+v), so whether this approval still holds cannot be answered", b)
	}
	return b, nil
}

func voidReasonFor(want, current Binding) string {
	switch {
	case want.SourceRevision != current.SourceRevision && want.ConfigHash != current.ConfigHash:
		return fmt.Sprintf("the source revision moved from %s to %s and the configuration was regenerated",
			shortHash(want.SourceRevision), shortHash(current.SourceRevision))
	case want.SourceRevision != current.SourceRevision:
		return fmt.Sprintf("the source revision moved from %s to %s",
			shortHash(want.SourceRevision), shortHash(current.SourceRevision))
	default:
		return "the configuration was regenerated since this proposal was made"
	}
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

func (s *Service) recordDecision(ctx context.Context, run *Run, d Decision, kind EntryKind) error {
	if _, err := s.Ledger.Append(ctx, Entry{
		RunID: run.RunID, TenantID: run.TenantID, Kind: kind, PlanID: run.Plan.PlanID,
		ProposalID: d.ProposalID, ConfigHash: d.Binding.ConfigHash,
		SourceRevision: d.Binding.SourceRevision, Axis: d.Axis, Actor: d.By,
		Detail: d.State.String(), AtMS: d.AtMS,
	}); err != nil {
		return fmt.Errorf("improvementrun: the decision could not be recorded, so it was not acted on: %w", err)
	}
	return nil
}

func (s *Service) checkApprove() error {
	if err := s.checkPlan(); err != nil {
		return err
	}
	var missing []string
	if s.Approvals == nil {
		missing = append(missing, "an approval gate (without it, there is no consent path at all)")
	}
	if s.Remeasure == nil {
		missing = append(missing, "a re-measurer (without it, a change is delivered on one observation, "+
			"and a gate that cannot be checked twice is a gate nobody downstream believes)")
	}
	if s.Subject == nil {
		missing = append(missing, "a subject resolver (without it, an approval could never be voided "+
			"when its revision moves)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrNotConfigured, missing)
	}
	return nil
}

// DecisionKind is what a caller asked for. A discriminator on ONE route rather than two routes, which
// is `careful-api-creation`'s enum-variant alternative taken rather than dismissed: approving and
// declining are the same act on the same resource with opposite values, and two endpoints would be two
// places the binding check could diverge.
type DecisionKind string

const (
	// DecideApprove authorizes the change. It applies, re-measures, and may withdraw.
	DecideApprove DecisionKind = "approve"
	// DecideDecline refuses it. The run continues and the proposal stays visible.
	DecideDecline DecisionKind = "decline"
)

// Valid reports membership. 🚫 There is deliberately no third value and no "approve_all".
func (k DecisionKind) Valid() bool { return k == DecideApprove || k == DecideDecline }

// Decide is the entry point a surface calls: it loads the run, applies ONE decision to ONE proposal,
// and returns the updated run.
//
// It returns the whole `Run` rather than only the `Decision` because the decision is not the only thing
// that changed: an approval applies and re-measures, so the run may have gained a withdrawal — and a
// surface that received only the decision would render "approved" over a change that was withdrawn
// three lines later. §9.4 asks for that sequence to be tellable, and the only way it is tellable is if
// the response carries it.
func (s *Service) Decide(ctx context.Context, tenantID, runID, proposalID string, kind DecisionKind, by string) (Run, Decision, error) {
	if !kind.Valid() {
		return Run{}, Decision{}, fmt.Errorf("improvementrun: %q is not a decision (approve, decline)", kind)
	}
	run, found, err := s.Run(ctx, tenantID, runID)
	if err != nil {
		return Run{}, Decision{}, err
	}
	if !found {
		return Run{}, Decision{}, fmt.Errorf("%w: no such run", ErrNotSurfaced)
	}
	if kind == DecideDecline {
		d, derr := s.Decline(ctx, &run, proposalID, by)
		return run, d, derr
	}

	d, err := s.Approve(ctx, &run, proposalID, by)
	if err != nil {
		return run, d, err
	}
	// 🔴 The approval CARRIES THROUGH to delivery, in one call, and that is the product rather than a
	// convenience. US5 is "I receive a pull request URL in the conversation, so the run ends where my
	// review starts"; a decision that stopped at "approved" would leave the person waiting for a step
	// nobody told them to take.
	//
	// 🚫 It does NOT carry through when the change was withdrawn. `Deliver` refuses that itself, and the
	// refusal is left there rather than short-circuited here: one place decides whether a change may be
	// delivered, and this is not it.
	if _, wd := run.WithdrawalFor(proposalID); wd {
		return run, d, nil
	}
	if s.Deliveries == nil || s.Routes == nil {
		// 🔴 A deployment that can approve but cannot deliver returns the approval and the run, with no
		// delivery on it. Not an error: the approval is real, the change was applied and re-measured, and
		// the surface renders "approved, delivery not configured" — which is a true state, unlike a 500.
		return run, d, nil
	}
	if _, derr := s.Deliver(ctx, &run, proposalID); derr != nil {
		// The decision stands and is returned WITH the error. A caller that dropped the decision would
		// re-render an unapproved card over a change that was approved, applied and re-measured.
		return run, d, derr
	}
	return run, d, nil
}
