package improvementrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/proposalgen"
)

// service_test.go drives the WHOLE propose phase through `Service`, which is the conversational
// caller. Every fence in this file therefore tests the new path, which is the only thing §7 asks for
// that the optimizer's own tests do not already prove.

// ── the test fixture ─────────────────────────────────────────────────────────────────────────────

type fixture struct {
	svc     *Service
	ledger  *MemLedger
	metrics *Metrics
	verify  map[string]optimizer.VerifyResult
	events  []eventname.Name
	enumErr error
	state   EmptyState
	targets []optimizer.Target
	cands   map[string][]optimizer.SearchCandidate
	// contractFails names config hashes the typed-contract check rejects.
	contractFails map[string]string
	// verifyOrder records the ORDER of calls, so "rejected before verification" is observable.
	verifyOrder []string
	// recorded names every candidate the ProposalRecorder was asked to record. 🔴 It is what makes the
	// collection guard in `recordingVerifier` provable: a gate-failing candidate must never produce a
	// proposal ROW, and `NewVerifiedProposal`'s own refusal happens too late to prevent one.
	recorded []string

	// question, origin and boundsOverride let a fence vary the levers a caller actually has, which is
	// what fence 7.4 needs: a refusal that any of them could lift would not be a property of the
	// configuration.
	question       string
	origin         RunOrigin
	boundsOverride func(*Bounds)
}

// questionOr returns the fixture's question, or a default.
func (f *fixture) questionOr(def string) string {
	if f.question != "" {
		return f.question
	}
	return def
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		ledger: NewMemLedger(), metrics: NewMetrics(),
		verify: map[string]optimizer.VerifyResult{}, cands: map[string][]optimizer.SearchCandidate{},
		contractFails: map[string]string{},
		state: EmptyState{
			State: proposalgen.StateNoBottleneck, Headline: "h", Detail: "d", Healthy: true,
		},
		targets: []optimizer.Target{{DiagnosisID: "d1", Node: "n1", Dimension: "model", Priority: 1}},
	}
	f.svc = &Service{
		Bounds:       fixtureBounds{f: f},
		Acks:         NewMemAckStore(),
		Enumerations: f,
		Proposals:    f,
		Ledger:       f.ledger,
		Metrics:      f.metrics,
		Verifier:     f,
		Repo:         optimizer.NewFakeRepo([]byte(`{"baseline":true}`)),
		ChangeLedger: optimizer.NewMemLedger(),
		Contract:     f,
		Now:          func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		Observe:      func(n eventname.Name, _ map[string]any) { f.events = append(f.events, n) },
	}
	return f
}

// fixtureBounds reads the FIXTURE's override, so a fence can widen a cap or a budget and assert the
// answer does not change.
type fixtureBounds struct{ f *fixture }

func (b fixtureBounds) BoundsFor(_ context.Context, tenantID string) (Bounds, error) {
	out := Bounds{
		TenantID: tenantID, WorkflowID: "wf_1", SourceRevision: "abc123def456",
		MaxCandidates: 8, MaxSpendUSD: 4.00,
	}
	if b.f != nil && b.f.boundsOverride != nil {
		b.f.boundsOverride(&out)
	}
	return out, nil
}

func (f *fixture) Enumerate(_ context.Context, p Plan) (Enumeration, error) {
	if f.enumErr != nil {
		return Enumeration{}, f.enumErr
	}
	if len(f.cands) == 0 {
		return Enumeration{State: f.state}, nil
	}
	return Enumeration{
		Enumerator: candidateSource{f}, Targets: f.targets, State: f.state,
		BaselineSpecBytes: []byte(`{}`), EvalSetCaseIDs: []string{"c1", "c2"},
	}, nil
}

// candidateSource is the delegate `optimizer.Enumerator`. It is a separate type from the fixture
// because the fixture already has an `Enumerate` with a different signature (EnumerationSource), and
// one type cannot satisfy both.
type candidateSource struct{ f *fixture }

func (c candidateSource) Enumerate(t optimizer.Target) []optimizer.SearchCandidate {
	return c.f.cands[t.Dimension]
}

func (f *fixture) Check(cand optimizer.SearchCandidate) (bool, string) {
	f.verifyOrder = append(f.verifyOrder, "contract:"+cand.ConfigHash)
	if reason, bad := f.contractFails[cand.ConfigHash]; bad {
		return false, reason
	}
	return true, ""
}

func (f *fixture) Verify(_ context.Context, req optimizer.VerifyRequest) (optimizer.VerifyResult, error) {
	f.verifyOrder = append(f.verifyOrder, "verify:"+req.Candidate.ConfigHash)
	if r, ok := f.verify[req.Candidate.ConfigHash]; ok {
		return r, nil
	}
	return optimizer.VerifyResult{ContractOK: true, Builds: true}, nil
}

func (f *fixture) Record(_ context.Context, _ Plan, _ string, cand optimizer.SearchCandidate, _ optimizer.VerifyResult) (
	string, string, string, string, *assessment.EvalSetReport, error) {
	f.recorded = append(f.recorded, cand.ConfigHash)
	return "prop_" + cand.ConfigHash, "diff_" + cand.ConfigHash, "1 file, +3 −1", "claude-opus-5-2026-05", nil, nil
}

// offer adds a candidate on an axis.
func (f *fixture) offer(axis assessment.Axis, hash string, vr optimizer.VerifyResult) {
	f.cands[string(axis)] = append(f.cands[string(axis)], optimizer.SearchCandidate{
		DiagnosisID: "d1", Node: "n1", Dimension: string(axis), ConfigHash: hash,
		Operator: "model_downgrade", Rationale: "cheaper published tier exists", SpecBytes: []byte(`{}`),
		ExpectedGain: 1,
	})
	f.verify[hash] = vr
	present := false
	for _, t := range f.targets {
		if t.Dimension == string(axis) {
			present = true
		}
	}
	if !present {
		f.targets = append(f.targets, optimizer.Target{DiagnosisID: "d1", Node: "n1", Dimension: string(axis), Priority: 1})
	}
}

// acknowledge records the person's agreement to a plan that needs one.
func (f *fixture) acknowledge(t *testing.T, p Plan) {
	t.Helper()
	if !p.RequiresAcknowledgement() {
		return
	}
	if err := f.svc.Acks.Record(context.Background(), Acknowledgement{
		PlanID: p.PlanID, TenantID: p.TenantID, By: "person@example.com",
		ProjectedSpendUSD: p.ProjectedSpendUSD, AtMS: 1,
	}); err != nil {
		t.Fatalf("acknowledging: %v", err)
	}
}

func (f *fixture) plan(t *testing.T, question string) Plan {
	t.Helper()
	origin := f.origin
	if origin == "" {
		origin = OriginConsole
	}
	p, err := f.svc.Plan(context.Background(), "ten_1", question, origin)
	if err != nil {
		t.Fatalf("planning %q: %v", question, err)
	}
	return p
}

// run plans, ACKNOWLEDGES if the plan is above the disclosure threshold, and executes.
//
// 🔴 The acknowledgement is here rather than switched off, because switching it off in the fixture is
// how a fence stops covering the path the product takes. The fixture's default bounds project above the
// threshold on purpose — that is the ordinary console case — and
// `TestAnUnacknowledgedPlanAboveTheThresholdRunsNothing` asserts the withholding directly.
func (f *fixture) run(t *testing.T, question string) Run {
	t.Helper()
	p := f.plan(t, question)
	f.acknowledge(t, p)
	r, err := f.svc.Propose(context.Background(), p)
	if err != nil {
		t.Fatalf("running %q: %v", question, err)
	}
	return r
}

// ── the propose phase, end to end through the conversational caller ──────────────────────────────

func TestConversationalRun_SurfacesOnlyVerifiedCandidates(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))
	f.offer(assessment.AxisModel, "cfg_unverified", func() optimizer.VerifyResult {
		r := passingVerdict(0.30)
		r.Verdict.GateResult, r.Verdict.Significant = "fail_significance", false
		return r
	}())

	run := f.run(t, "improve my model choice")
	if len(run.Proposals) != 1 {
		t.Fatalf("surfaced %d proposals, want 1 (the verified one)", len(run.Proposals))
	}
	if run.Proposals[0].ConfigHash != "cfg_good" {
		t.Fatalf("surfaced %q; the unverified candidate reached a card", run.Proposals[0].ConfigHash)
	}
}

// TestConversationalRun_GateFailingHighScorerNotDelivered is `TestLoop_GateFailingHighScorerNotMerged`
// re-run through the NEW caller. The optimizer's own test proves the optimizer calls the gate; this
// proves the conversation does.
func TestConversationalRun_GateFailingHighScorerNotDelivered(t *testing.T) {
	f := newFixture(t)
	high := passingVerdict(0.95)
	high.Verdict.GateResult, high.Verdict.RegressionPass = "fail_regression", false
	f.offer(assessment.AxisModel, "cfg_high_but_failing", high)

	run := f.run(t, "improve my model choice")
	if len(run.Proposals) != 0 {
		t.Fatalf("a candidate with a +0.95 delta that FAILED a gate was surfaced through the "+
			"conversation. However high the composite is the exact phrasing of FR9: %+v", run.Proposals[0])
	}
	if run.Empty == nil || run.Empty.State == "" {
		t.Fatal("the run surfaced nothing and named no state, which is the P30 defect through a new door")
	}
	// 🔴 And no proposal ROW was written for it. This is the assertion the collection guard — and only
	// the collection guard — satisfies: `NewVerifiedProposal` refuses the candidate, but it refuses it
	// AFTER `ProposalRecorder.Record` has already compiled a diff and written a row. A row for a
	// gate-failing candidate is a row P12's delivery surface can later offer to deliver.
	for _, h := range f.recorded {
		if h == "cfg_high_but_failing" {
			t.Fatalf("a proposal row was recorded for a gate-failing candidate (recorded: %v). The "+
				"refusal downstream of it comes too late — the diff is compiled and the row exists",
				f.recorded)
		}
	}
}

func TestConversationalRun_UnverifiedNotDelivered(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_unrun", optimizer.VerifyResult{ContractOK: true, Builds: true})
	run := f.run(t, "improve my model choice")
	if len(run.Proposals) != 0 {
		t.Fatal("a candidate with no verdict at all was surfaced through the conversation")
	}
}

// TestConversationalRun_ContractViolationRejectedBeforeVerification asserts the ORDER through the new
// path: the contract check runs and the verifier is never reached.
func TestConversationalRun_ContractViolationRejectedBeforeVerification(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_bad_contract", passingVerdict(0.20))
	f.contractFails["cfg_bad_contract"] = "node n1 emits a type the next node cannot read"

	run := f.run(t, "improve my model choice")
	if len(run.Proposals) != 0 {
		t.Fatal("a contract-violating candidate was surfaced")
	}
	for _, call := range f.verifyOrder {
		if call == "verify:cfg_bad_contract" {
			t.Fatalf("the verifier was called for a contract-violating candidate (order: %v). It then "+
				"costs a provider call and produces a verdict row, and the delivery oracle reads that row",
				f.verifyOrder)
		}
	}
	if len(f.verifyOrder) == 0 || !strings.HasPrefix(f.verifyOrder[0], "contract:") {
		t.Fatalf("the first call was %v, not the contract check", f.verifyOrder)
	}
}

// ── the plan is an artifact obtainable WITHOUT running anything ──────────────────────────────────

func TestPlanningSpendsNothingAndRecordsItself(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))
	p := f.plan(t, "improve my model choice")

	if len(f.verifyOrder) != 0 {
		t.Fatalf("planning called the verifier (%v). The plan is the artifact a person may DECLINE; "+
			"producing it must cost nothing", f.verifyOrder)
	}
	entries, _ := f.ledger.Entries(context.Background(), "plan:"+p.PlanID)
	if len(entries) != 1 || entries[0].Kind != KindPlanCreated {
		t.Fatalf("the plan was not recorded before anything spent: %+v", entries)
	}
	if f.metrics.Health().PlansCreated != 1 {
		t.Fatal("the health document counts no plan. \"How many were planned\" and \"how many ran\" " +
			"must be separately answerable, or the disclosure threshold's effect is invisible")
	}
}

// TestAnUnacknowledgedPlanAboveTheThresholdRunsNothing is FR2 through the conversational caller, and
// the assertion that matters is that NOTHING was touched: a refusal that had already enumerated or
// verified would have spent the money the disclosure exists to put in front of somebody first.
func TestAnUnacknowledgedPlanAboveTheThresholdRunsNothing(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))
	p := f.plan(t, "improve my model choice")
	if !p.RequiresAcknowledgement() {
		t.Fatalf("this fixture's plan projects $%.2f, which is not above the $%.2f threshold; the test "+
			"below would prove nothing", p.ProjectedSpendUSD, DisclosureThresholdUSD)
	}
	_, err := f.svc.Propose(context.Background(), p)
	if !errors.Is(err, ErrAwaitingAcknowledgement) {
		t.Fatalf("an unacknowledged plan above the threshold ran: %v", err)
	}
	if len(f.verifyOrder) != 0 {
		t.Fatalf("the refused run still called %v. A refusal that spends is not a refusal", f.verifyOrder)
	}
	if f.metrics.Health().RunsStarted != 0 {
		t.Fatal("the health document counts a started run for a plan that was withheld")
	}
}

func TestAnUnrecordablePlanIsNotOffered(t *testing.T) {
	f := newFixture(t)
	f.ledger.SetDown(true)
	_, err := f.svc.Plan(context.Background(), "ten_1", "fix it", OriginConsole)
	if err == nil {
		t.Fatal("a plan was offered while the ledger was down. An unrecorded plan is one an " +
			"acknowledgement can never be matched to, and the acknowledgement is the consent")
	}
}

// ── which bound stopped it, through the conversational path ──────────────────────────────────────

func TestConversationalRun_ReportsWhichBoundStoppedIt(t *testing.T) {
	f := newFixture(t)
	for i, h := range []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9", "c10"} {
		vr := passingVerdict(0.01)
		if i%2 == 0 {
			vr.Verdict.GateResult = "fail_significance"
		}
		f.offer(assessment.AxisModel, h, vr)
	}
	run := f.run(t, "improve my model choice")
	if run.Outcome.Bound != BoundCandidateCap {
		t.Fatalf("ten candidates under a cap of eight stopped on %q (%q). A truncated run reported as "+
			"converged is the one direction that stops somebody raising the cap",
			run.Outcome.Bound, run.Outcome.Detail)
	}
	if run.Outcome.Sentence() == "" {
		t.Fatal("the bound renders no sentence")
	}
}

func TestConversationalRun_AFaultIsNotReportedAsABound(t *testing.T) {
	f := newFixture(t)
	f.enumErr = errors.New("the discovered-graph store is unreachable")
	p := f.plan(t, "fix it")
	f.acknowledge(t, p)
	run, err := f.svc.Propose(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if run.Outcome.Stopped() {
		t.Fatalf("an unreachable store was reported as the bound %q", run.Outcome.Bound)
	}
	if !run.Outcome.Faulted() {
		t.Fatal("the run reported neither a bound nor a fault, so it renders as having simply finished")
	}
	if f.metrics.Health().RunsFaulted != 1 || f.metrics.Health().RunsBoundedOut != 0 {
		t.Fatalf("the health document filed a fault as a bound: %+v", f.metrics.Health())
	}
}

// ── the named empty state travels through the conversational path ────────────────────────────────

func TestConversationalRun_NamesWhichNothingToProposeStateApplies(t *testing.T) {
	f := newFixture(t)
	f.state = EmptyState{
		State: proposalgen.StateNoRuns, Headline: "No runs have been linked for this workflow yet.",
		Detail: "the pass's own words", NextAction: "Run an eval and link it.",
	}
	run := f.run(t, "fix it")
	if run.Empty == nil {
		t.Fatal("the run produced nothing and named no state — an empty result, which is exactly what " +
			"FR7 forbids")
	}
	if run.Empty.State != proposalgen.StateNoRuns {
		t.Fatalf("the state was reported as %q, want %q", run.Empty.State, proposalgen.StateNoRuns)
	}
	if run.Empty.NextAction == "" {
		t.Fatal("the empty state names no next action, so a customer who needs to link runs is told nothing")
	}
}

// ── the per-axis breakdown exists at every stage ─────────────────────────────────────────────────

func TestConversationalRun_PerAxisBreakdownExistsAtEveryStage(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_m", passingVerdict(0.08))
	f.offer(assessment.AxisMemory, "cfg_x", func() optimizer.VerifyResult {
		r := passingVerdict(0.40)
		r.Verdict.GateResult = "fail_significance"
		return r
	}())

	run := f.run(t, "fix it")
	if len(run.PerAxis) != len(assessment.Axes()) {
		t.Fatalf("the breakdown has %d rows and there are %d axes. Listing only the scoped axes makes "+
			"\"we did not look\" and \"it produced nothing\" render identically",
			len(run.PerAxis), len(assessment.Axes()))
	}
	model, _ := run.AxisRow(assessment.AxisModel)
	memory, _ := run.AxisRow(assessment.AxisMemory)
	if model.Generated == 0 || model.Verified == 0 {
		t.Fatalf("the model axis generated %d and verified %d", model.Generated, model.Verified)
	}
	if memory.Generated == 0 {
		t.Fatal("the memory axis generated nothing, so its 0% verification rate has no denominator")
	}
	if memory.Verified != 0 {
		t.Fatalf("the memory axis verified %d; its only candidate failed the gate", memory.Verified)
	}
	// 🔴 The point of the breakdown: a healthy aggregate over a broken axis.
	h := f.metrics.Health()
	found := map[string]bool{}
	for _, cell := range h.PerAxis {
		found[cell.Axis.String()+"/"+cell.Stage.String()] = true
	}
	for _, want := range []string{"model/generated", "model/verified", "memory/generated"} {
		if !found[want] {
			t.Fatalf("the health document has no %q cell; an operator cannot see a per-axis failure", want)
		}
	}
}

// ── the loop is the shipped one, and it is not armed to merge ────────────────────────────────────

// TestTheRunNeverArmsTheLoopsMergePath is the structural half of "delivery is downstream of an
// approval". The loop CAN merge when its three prerequisites are armed; P35 never arms them, so there
// is no path from the loop to a customer's repository that does not go through `internal/approval` and
// `internal/forgedelivery`.
func TestTheRunNeverArmsTheLoopsMergePath(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.50))
	repo := optimizer.NewFakeRepo([]byte(`{"baseline":true}`))
	f.svc.Repo = repo

	f.run(t, "fix it")
	if repo.Merged() != 0 {
		t.Fatalf("the loop merged %d change(s) during a propose phase. Delivery is downstream of an "+
			"approval nobody has given yet", repo.Merged())
	}
}

func TestAServiceMissingACollaboratorRefusesRatherThanPanics(t *testing.T) {
	_, err := (&Service{}).Plan(context.Background(), "t", "fix it", OriginConsole)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v, want ErrNotConfigured. A misconfiguration that panics takes down every other "+
			"tenant's turn too", err)
	}
	for _, want := range []string{"bounds source", "run ledger", "clock"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name the missing %s: %v", want, err)
		}
	}
}

// TestADeploymentThatCannotVerifyCanStillProduceAPlan is PRD §12 stage 1, made reachable.
//
// 🔴 The alternative — one readiness check for both phases — makes the stage designed to run WITHOUT
// spending require every collaborator that spends. A deployment that cannot verify would then refuse to
// show a person what a run would cost, which is the difference between "this feature is at stage 1" and
// "this feature is off".
func TestADeploymentThatCannotVerifyCanStillProduceAPlan(t *testing.T) {
	f := newFixture(t)
	planOnly := &Service{Bounds: f.svc.Bounds, Acks: f.svc.Acks, Ledger: f.ledger, Now: f.svc.Now}

	p, err := planOnly.Plan(context.Background(), "ten_1", "fix it", OriginConsole)
	if err != nil {
		t.Fatalf("a plan-only deployment could not produce a plan: %v", err)
	}
	if p.SpendBudgetUSD <= 0 || p.CandidateCap <= 0 {
		t.Fatal("the plan carries no bounds, so it shows a person nothing they can decline")
	}
	_, err = planOnly.Propose(context.Background(), p)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("a plan-only deployment executed a plan: %v", err)
	}
	if !strings.Contains(err.Error(), "stage 1") {
		t.Fatalf("the refusal reads as a broken deployment rather than as a rollout stage: %v", err)
	}
}
