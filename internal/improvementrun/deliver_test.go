package improvementrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// deliver_test.go proves §5 through the CONVERSATIONAL caller. Every fence here re-runs a property
// `internal/forgedelivery` already holds for the optimizer, because a requirement holding for one
// caller says nothing about a new one.

// ── the delivery double ──────────────────────────────────────────────────────────────────────────

// recordingDeliverer records what the shipped core was ASKED to do and reproduces the two behaviours
// P35 depends on: idempotency, and never merging below Autonomous.
type recordingDeliverer struct {
	calls []forgedelivery.Proposal
	// byDelivery models the append-only record's idempotency: a second Deliver for the same
	// (config_hash, source_revision, target) returns the FIRST result with Created=false.
	byDelivery map[string]forgedelivery.Result
	err        error
	// merged counts merges. 🔴 It must stay zero at every level below Autonomous.
	merged int
}

func newRecordingDeliverer() *recordingDeliverer {
	return &recordingDeliverer{byDelivery: map[string]forgedelivery.Result{}}
}

func (d *recordingDeliverer) Deliver(_ context.Context, p forgedelivery.Proposal, route *forgedelivery.Route, _ forgedelivery.ForgeWriter) (forgedelivery.Result, error) {
	d.calls = append(d.calls, p)
	if d.err != nil {
		return forgedelivery.Result{}, d.err
	}
	id := forgedelivery.DeliveryID(p.ConfigHash, p.SourceRevision, route.Target.Key())
	if first, seen := d.byDelivery[id]; seen {
		first.Created = false
		return first, nil
	}
	res := forgedelivery.Result{
		DeliveryID: id, Mode: route.Mode, Created: true,
		PR: forgedelivery.PullRequest{
			Ref: route.Target.Key() + "#7", Number: 7, State: "open",
			URL: "https://github.com/" + route.Target.Key() + "/pull/7",
		},
	}
	if p.Level == entitlement.LevelAutonomous {
		d.merged++
		res.Merged = true
	}
	d.byDelivery[id] = res
	return res, nil
}

// routeStub resolves a route and records whether a WRITER was handed over — which is how "the CLI path
// gives the platform no forge credential" becomes observable.
type routeStub struct {
	missing bool
	mode    forgedelivery.Mode
	// forge is non-nil only for App mode, mirroring the real wiring.
	handedWriter bool
	branchCalls  []string
}

func (r *routeStub) Route(_ context.Context, _, workflowID string, mode forgedelivery.Mode) (
	*forgedelivery.Route, forgedelivery.ForgeWriter, bool, error) {
	if r.missing {
		return nil, nil, false, nil
	}
	r.mode = mode
	route := &forgedelivery.Route{
		Mode: mode, ForgeKind: forgedelivery.ForgeGitHub,
		Target: forgedelivery.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main"},
	}
	if mode == forgedelivery.ModeApp {
		r.handedWriter = true
		return route, &recordingForge{calls: &r.branchCalls}, true, nil
	}
	// 🔴 CI-mediated hands NO writer. The credential lives in the customer's CI runner, and the
	// platform-side absence is the property, not a policy.
	return route, nil, true, nil
}

// recordingForge records every branch push, which is what fence 7.10 asserts against.
type recordingForge struct{ calls *[]string }

func (f *recordingForge) EnsureBranch(_ context.Context, t forgedelivery.Target, head string) error {
	*f.calls = append(*f.calls, t.Key()+":"+head)
	return nil
}
func (f *recordingForge) OpenOrUpdatePR(context.Context, forgedelivery.OpenRequest) (forgedelivery.PullRequest, bool, error) {
	return forgedelivery.PullRequest{}, true, nil
}
func (f *recordingForge) ClosePR(context.Context, string, string) error   { return nil }
func (f *recordingForge) MergePR(context.Context, string) (string, error) { return "", nil }
func (f *recordingForge) OpenPRCount(context.Context, forgedelivery.Target) (int, error) {
	return 0, nil
}
func (f *recordingForge) Kind() forgedelivery.ForgeKind { return forgedelivery.ForgeGitHub }

// deliverable runs to an APPROVED, re-measured proposal ready to deliver.
func deliverable(t *testing.T, origin RunOrigin) (*fixture, *Run, *recordingDeliverer, *routeStub) {
	t.Helper()
	f, run, _, _ := approvable(t)
	run.Plan.Origin = origin

	del, routes := newRecordingDeliverer(), &routeStub{}
	f.svc.Deliveries, f.svc.Routes = del, routes

	if _, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	return f, run, del, routes
}

// ── 5.1 the surface-scoped default ───────────────────────────────────────────────────────────────

func TestConsoleRunsUseTheHostedAppAndCLIRunsStayCIMediated(t *testing.T) {
	for _, tc := range []struct {
		origin RunOrigin
		want   forgedelivery.Mode
		writer bool
	}{
		{OriginConsole, forgedelivery.ModeApp, true},
		{OriginCLI, forgedelivery.ModeCI, false},
		{OriginCI, forgedelivery.ModeCI, false},
	} {
		f, run, _, routes := deliverable(t, tc.origin)
		res, err := f.svc.Deliver(context.Background(), run, run.Proposals[0].ProposalID)
		if err != nil {
			t.Fatalf("%s: %v", tc.origin, err)
		}
		if routes.mode != tc.want {
			t.Fatalf("%s runs deliver in mode %q, want %q (R3: the console default is the App because a "+
				"console customer has no CI integration; the CLI keeps CI-mediated)",
				tc.origin, routes.mode, tc.want)
		}
		if res.Mode != string(tc.want) {
			t.Fatalf("%s: the result reports mode %q", tc.origin, res.Mode)
		}
		// 🔴 The credential-absence property, observable rather than asserted.
		if routes.handedWriter != tc.writer {
			t.Fatalf("%s: a platform-side forge writer was %s. ADR-005's load-bearing property is that "+
				"the CLI path gives the platform NO forge credential",
				tc.origin, map[bool]string{true: "handed over", false: "not handed over"}[routes.handedWriter])
		}
	}
}

// TestTheDefaultChangesTheModeNotTheScope is design D3's sentence, fenced. The failure it guards is the
// default quietly widening into "the platform has write access to your account".
func TestTheDefaultChangesTheModeNotTheScope(t *testing.T) {
	inst := forgedelivery.Installation{
		InstallationID: "i1", TenantID: "ten_1",
		Repositories: []string{"nousresearch/hermes-agent"},
		Permissions:  forgedelivery.LeastPrivilegePermissions(),
	}
	if err := inst.Validate(); err != nil {
		t.Fatalf("the least-privilege installation was refused: %v", err)
	}
	wide := inst
	wide.Repositories = nil
	if err := wide.Validate(); err == nil {
		t.Fatal("an installation selecting NO repositories validated. There must be no way to express " +
			"\"all repositories\", or the console default becomes an account-wide grant")
	}
	broader := inst
	broader.Permissions = forgedelivery.PermissionSet{"administration": "write"}
	if err := broader.Validate(); err == nil {
		t.Fatal("a broader-than-delivery permission set validated. Broadening the App's permissions is " +
			"a spec change, not a configuration choice")
	}
}

func TestOnlyTheConsoleSurfaceReachesForACredential(t *testing.T) {
	for _, s := range forgedelivery.Surfaces() {
		mode, err := forgedelivery.DefaultModeFor(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if (mode == forgedelivery.ModeApp) != s.HoldsPlatformCredential() {
			t.Fatalf("surface %q defaults to mode %q but reports HoldsPlatformCredential()=%v. A fourth "+
				"surface must decide this explicitly rather than inherit it", s, mode, s.HoldsPlatformCredential())
		}
	}
	mode, err := forgedelivery.DefaultModeFor("invented")
	if err == nil {
		t.Fatal("an unknown surface was classified silently. The safe fallback and the loud complaint " +
			"are not alternatives")
	}
	if mode != forgedelivery.ModeCI {
		t.Fatalf("an unknown surface fell back to %q; the fallback must be the credential-free mode", mode)
	}
}

// ── 5.2 a console run with no installation ───────────────────────────────────────────────────────

func TestAConsoleRunWithNoInstallationIsWithheldAndKeepsTheDiff(t *testing.T) {
	f, run, del, _ := deliverable(t, OriginConsole)
	del.err = forgedelivery.ErrNoInstallation

	res, err := f.svc.Deliver(context.Background(), run, run.Proposals[0].ProposalID)
	if err != nil {
		t.Fatalf("a missing installation was reported as a failure rather than a condition: %v", err)
	}
	if res.Withheld == nil {
		t.Fatal("delivery was not withheld and no pull request exists — a silent nothing")
	}
	if res.Withheld.Kind != forgedelivery.WithheldNoInstallation {
		t.Fatalf("the condition is %q. `no_route` would tell somebody to configure a route they "+
			"already have", res.Withheld.Kind)
	}
	if res.Withheld.NextAction == "" || !strings.Contains(res.Withheld.NextAction, "Install") {
		t.Fatalf("the condition names no next action: %q", res.Withheld.NextAction)
	}
	// 🔴 The verified diff stays available.
	p, ok := run.proposal(run.Proposals[0].ProposalID)
	if !ok || p.DiffRef == "" {
		t.Fatal("the verified diff was discarded when delivery was withheld")
	}
	if res.Delivered() {
		t.Fatal("a withheld delivery reports a pull request")
	}
	// 🚫 And it did NOT silently fall back to CI-mediated, which a console customer cannot use.
	if res.Mode != string(forgedelivery.ModeApp) {
		t.Fatalf("the withheld delivery reports mode %q; falling back to CI-mediated would replace a "+
			"stated condition with a silent one that never completes", res.Mode)
	}
}

// ── 5.3 idempotency ──────────────────────────────────────────────────────────────────────────────

// TestConversationalRun_DeliveryIsIdempotent is FR20, and the assertion that matters is the SECOND
// clause: the second call must return the FIRST delivery, not create a second one and not error.
func TestConversationalRun_DeliveryIsIdempotent(t *testing.T) {
	f, run, del, _ := deliverable(t, OriginConsole)
	id := run.Proposals[0].ProposalID

	first, err := f.svc.Deliver(context.Background(), run, id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.Deliver(context.Background(), run, id)
	if err != nil {
		t.Fatalf("the second delivery ERRORED. FR20 requires it to return the first, not to fail: %v", err)
	}
	if second.PullRequestRef != first.PullRequestRef {
		t.Fatalf("two deliveries of one change produced two pull requests: %q and %q",
			first.PullRequestRef, second.PullRequestRef)
	}
	if !second.Deduplicated {
		t.Fatal("the second delivery does not report itself as deduplicated. That is the observable " +
			"half of idempotency, and a deduplication rate of zero means the path is never exercised")
	}
	if len(del.byDelivery) != 1 {
		t.Fatalf("the delivery core holds %d records for one change", len(del.byDelivery))
	}
	if f.metrics.Health().DeliveriesDeduplicated != 1 {
		t.Fatal("the health document counts no deduplication")
	}
}

// ── 5.4 never merge below Autonomous ─────────────────────────────────────────────────────────────

// TestConversationalRun_NeverMergesBelowAutonomous asserts the STRUCTURE rather than the policy: this
// caller requests `LevelAssisted`, so the merge branch inside `forgedelivery.Prepare` is unreachable
// from here — which is the difference between a rule and a structure.
func TestConversationalRun_NeverMergesBelowAutonomous(t *testing.T) {
	for _, origin := range []RunOrigin{OriginConsole, OriginCLI, OriginCI} {
		f, run, del, _ := deliverable(t, origin)
		if _, err := f.svc.Deliver(context.Background(), run, run.Proposals[0].ProposalID); err != nil {
			t.Fatalf("%s: %v", origin, err)
		}
		if del.merged != 0 {
			t.Fatalf("%s: the platform merged %d pull request(s). P35 opens pull requests; auto-merge is "+
				"P6's Autonomous level and Enterprise-only", origin, del.merged)
		}
		if len(del.calls) == 0 || del.calls[0].Level != entitlement.LevelAssisted {
			t.Fatalf("%s: delivery was requested at level %q. Requesting anything above Assisted makes "+
				"the merge branch reachable from a phase whose non-goal is merging",
				origin, del.calls[0].Level)
		}
	}
}

// ── the origin gate: a scheduled run never delivers ──────────────────────────────────────────────

func TestAScheduledRunNeverDeliversAtAnyLevel(t *testing.T) {
	f, run, del, _ := deliverable(t, OriginScheduled)
	_, err := f.svc.Deliver(context.Background(), run, run.Proposals[0].ProposalID)
	if !errors.Is(err, ErrScheduledRunMayNotDeliver) {
		t.Fatalf("a scheduled run delivered: %v", err)
	}
	if len(del.calls) != 0 {
		t.Fatal("the delivery core was reached by a scheduled run. The refusal must come BEFORE " +
			"entitlement, or \"may this deliver\" becomes answerable by buying a plan")
	}
}

// ── delivery is downstream of approval and of re-measurement ─────────────────────────────────────

func TestAnUnapprovedProposalIsNotDelivered(t *testing.T) {
	f, run, _, _ := approvable(t)
	f.svc.Deliveries, f.svc.Routes = newRecordingDeliverer(), &routeStub{}
	if _, err := f.svc.Deliver(context.Background(), run, run.Proposals[0].ProposalID); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("a proposal nobody approved was delivered: %v", err)
	}
}

func TestAWithdrawnChangeIsNotDelivered(t *testing.T) {
	f, run, rem, binding := approvable(t)
	del := newRecordingDeliverer()
	f.svc.Deliveries, f.svc.Routes = del, &routeStub{}
	p := run.Proposals[0]
	rem.byProposal[p.ProposalID] = Measurement{
		Delta:                evalstats.Interval{Mean: 0.01, Low: -0.03, High: 0.005, NSeeds: 5, NCases: 40},
		ProviderModelVersion: p.ProviderModelVersion,
		ResolvedConfigHash:   binding.ConfigHash, SourceRevision: binding.SourceRevision,
	}
	if _, err := f.svc.Approve(context.Background(), run, p.ProposalID, "person@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Deliver(context.Background(), run, p.ProposalID); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("a withdrawn change was delivered: %v", err)
	}
	if len(del.calls) != 0 {
		t.Fatal("the delivery core was reached by a withdrawn change")
	}
}

// ── 5.6 the pull request URL is on the record and comes FROM the forge ───────────────────────────

func TestThePullRequestURLIsRecordedAndNeverComposed(t *testing.T) {
	f, run, _, _ := deliverable(t, OriginConsole)
	res, err := f.svc.Deliver(context.Background(), run, run.Proposals[0].ProposalID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PullRequestURL == "" {
		t.Fatal("the delivery carries no pull request URL, so the run ends nowhere a review can start")
	}
	entries, _ := f.ledger.Entries(context.Background(), run.RunID)
	var found bool
	for _, e := range entries {
		if e.Kind == KindDeliveryOpened {
			found = true
			if e.DeliveryID == "" {
				t.Fatal("the delivery entry carries no delivery id, which is what the reconciliation " +
					"pass joins on")
			}
			if e.Detail != res.PullRequestURL {
				t.Fatalf("the record carries %q and the result carries %q", e.Detail, res.PullRequestURL)
			}
		}
	}
	if !found {
		t.Fatal("no delivery entry was appended, so an interrupted run could not be reconciled")
	}
}

// ── 5.7 the pull request body carries its evidence ───────────────────────────────────────────────

func TestConversationalRun_PullRequestBodyCarriesItsEvidence(t *testing.T) {
	body := forgedelivery.RenderPRBody(forgedelivery.Evidence{
		Title:      "model_downgrade on extract",
		Level:      "assisted",
		Verdict:    passingVerdict(0.08).Verdict,
		ConfigHash: "cfg", SourceRevision: "rev",
		Axis: string(assessment.AxisModel), Node: "extract", Operator: "model_downgrade",
		EvalSetCases: 40, EvalSetIndecisive: 2,
		RevertRef: "abc1234",
	})
	for _, want := range []string{
		"## What changed", "model", "extract", "model_downgrade",
		"## How decisive the eval set is", "Cases behind this number", "**40**",
		"## How to revert this", "git revert abc1234",
		"## Verified delta", "confidence interval",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the pull request body does not carry %q. A reviewer's first question is which part "+
				"of their agent this is, and the reason the whole phase is acceptable is that the change "+
				"is one `git revert` away:\n%s", want, body)
		}
	}
}

// TestAnEvalSetThatCannotFailSaysSoInThePullRequest is the sharpest sentence in the body and the one
// most likely to be dropped as unhelpful.
func TestAnEvalSetThatCannotFailSaysSoInThePullRequest(t *testing.T) {
	body := forgedelivery.RenderPRBody(forgedelivery.Evidence{
		Verdict: passingVerdict(0.08).Verdict, EvalSetCases: 8, EvalSetIndecisive: 8,
		EvalSetCannotFail: true,
	})
	if !strings.Contains(body, "passes whatever the agent does") {
		t.Fatalf("a set that cannot fail is rendered as ordinary evidence. That is the difference "+
			"between \"this scored 0.94\" and \"this scored 0.94 on a set where every case passes\":\n%s", body)
	}
}

// ── 5.8 the reconciliation pass ──────────────────────────────────────────────────────────────────

// TestReconciliationCompletesAnInterruptedDeliveryWithNoHumanStep is §7.4.
func TestReconciliationCompletesAnInterruptedDeliveryWithNoHumanStep(t *testing.T) {
	f, run, del, _ := deliverable(t, OriginConsole)
	f.svc.ProposalReader = staticProposals{run.Proposals}
	// The run died between applying and delivering: `change_applied` is on the ledger, no delivery is.
	if len(del.calls) != 0 {
		t.Fatal("the fixture already delivered")
	}
	res, err := f.svc.Reconcile(context.Background(), run.TenantID)
	if err != nil {
		t.Fatalf("the reconciliation pass failed: %v", err)
	}
	if res.Examined == 0 {
		t.Fatal("the pass examined nothing, so the interrupted delivery is invisible to it")
	}
	if res.Resolved != 1 {
		t.Fatalf("the pass resolved %d of %d (%v)", res.Resolved, res.Examined, res.Details)
	}
	if len(del.calls) != 1 {
		t.Fatalf("the pass delivered %d change(s)", len(del.calls))
	}
}

// TestReconciliationRunsEveryCycleAndReportsZeroAsASuccess is design D6's requirement: a repair path
// that only runs after failures is a path that is never exercised until it is needed.
func TestReconciliationRunsEveryCycleAndReportsZeroAsASuccess(t *testing.T) {
	f := newFixture(t)
	f.svc.Deliveries, f.svc.Routes = newRecordingDeliverer(), &routeStub{}
	res, err := f.svc.Reconcile(context.Background(), "ten_1")
	if err != nil {
		t.Fatalf("a pass with nothing to do failed: %v", err)
	}
	if res.Examined != 0 || res.Resolved != 0 {
		t.Fatalf("a pass over an empty ledger found work: %+v", res)
	}
	h := f.metrics.Health()
	if h.ReconcileLastSuccessMS == 0 {
		t.Fatal("a pass that resolved nothing recorded no last-success timestamp. Zero deletions is the " +
			"NORMAL result, so \"did it do anything\" cannot be the signal — \"when did it last " +
			"complete\" is, and only the pass can publish it")
	}
}

// TestAnUnreadableLedgerFailsTheReconciliationPassRatherThanReportingSuccess — recording a fresh
// timestamp over a pass that examined nothing would make the staleness signal lie.
func TestAnUnreadableLedgerFailsTheReconciliationPassRatherThanReportingSuccess(t *testing.T) {
	f := newFixture(t)
	f.svc.Deliveries, f.svc.Routes = newRecordingDeliverer(), &routeStub{}
	f.ledger.SetDown(true)
	if _, err := f.svc.Reconcile(context.Background(), "ten_1"); err == nil {
		t.Fatal("a pass over an unreadable ledger reported success")
	}
	if f.metrics.Health().ReconcileLastSuccessMS != 0 {
		t.Fatal("a failed pass recorded a last-success timestamp, so an operator sees a fresh " +
			"timestamp over a pass that examined nothing")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

type staticProposals struct{ ps []VerifiedProposal }

func (s staticProposals) Proposal(_ context.Context, _, id string) (VerifiedProposal, bool, error) {
	for _, p := range s.ps {
		if p.ProposalID == id {
			return p, true, nil
		}
	}
	return VerifiedProposal{}, false, nil
}

// TestApprovingCarriesThroughToAPullRequestURL is US5 end to end through the conversational caller:
// "the run ends where my review starts."
func TestApprovingCarriesThroughToAPullRequestURL(t *testing.T) {
	f, run, del, _ := approvableWithDelivery(t)
	updated, d, err := f.svc.Decide(context.Background(), run.TenantID, run.RunID,
		run.Proposals[0].ProposalID, DecideApprove, "person@example.com")
	if err != nil {
		t.Fatalf("deciding: %v", err)
	}
	if d.State != DecisionApproved {
		t.Fatalf("the decision is %q", d.State)
	}
	if len(updated.Deliveries) != 1 || updated.Deliveries[0].PullRequestURL == "" {
		t.Fatalf("approving did not produce a pull request URL (%+v). A decision that stopped at "+
			"\"approved\" leaves the person waiting for a step nobody told them to take", updated.Deliveries)
	}
	if len(del.calls) != 1 {
		t.Fatalf("the delivery core was called %d times", len(del.calls))
	}
}

// TestDecliningNeverReachesDelivery — one no is not a cancel, and it is certainly not a push.
func TestDecliningNeverReachesDelivery(t *testing.T) {
	f, run, del, _ := approvableWithDelivery(t)
	if _, _, err := f.svc.Decide(context.Background(), run.TenantID, run.RunID,
		run.Proposals[0].ProposalID, DecideDecline, "person@example.com"); err != nil {
		t.Fatalf("declining: %v", err)
	}
	if len(del.calls) != 0 {
		t.Fatal("a declined proposal reached the delivery core")
	}
}

// TestConversationalRun_CancelPushesNothing is fence 7.10 and decisions.md D-35.6.
//
// 🔴 It asserts the forge received NO `EnsureBranch` call, which is a stronger and more directly
// observable claim than "no branch remains" — and it is the only claim available, because P12 forbids
// the platform from ever deleting a branch and enforces that by `ForgeWriter` having no delete method.
// The two rules are satisfied together by making the push the LAST step and putting the cancellation
// check immediately before it.
func TestConversationalRun_CancelPushesNothing(t *testing.T) {
	f, run, del, routes := approvableWithDelivery(t)
	id := run.Proposals[0].ProposalID
	// Approved, applied and re-measured — everything up to the push has happened.
	if _, err := f.svc.Approve(context.Background(), run, id, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	// … and NOW the person cancels, in the window the requirement is about.
	f.svc.Cancelled = func(string) bool { return true }

	_, err := f.svc.Deliver(context.Background(), run, id)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("a cancelled run delivered: %v", err)
	}
	if len(routes.branchCalls) != 0 {
		t.Fatalf("the forge was asked to create %v. A cancelled run must never have pushed — there is no "+
			"deletion capability to undo it with, and adding one would put a destructive method on the "+
			"interface whose safety comes from not having one", routes.branchCalls)
	}
	if len(del.calls) != 0 {
		t.Fatal("the delivery core was reached by a cancelled run")
	}
}

// approvableWithDelivery is `deliverable` without the approval having happened yet, so a test can drive
// the whole decision path itself.
func approvableWithDelivery(t *testing.T) (*fixture, *Run, *recordingDeliverer, *routeStub) {
	t.Helper()
	f, run, _, _ := approvable(t)
	f.svc.ProposalReader = staticProposals{run.Proposals}
	del, routes := newRecordingDeliverer(), &routeStub{}
	f.svc.Deliveries, f.svc.Routes = del, routes
	return f, run, del, routes
}

// TestAReconstructedRunKeepsItsOriginAndSubject is the fence for a defect the browser check found.
//
// 🔴 A run rebuilt from the ledger had neither a workflow nor an ORIGIN, so `Deliver` resolved the
// surface as the safe CLI default and every CONSOLE run completed from the record silently became
// CI-mediated. It failed loudly in the browser only by luck — CI-mediated needs no platform writer, so
// there was nothing to deliver with. On a deployment configured for both modes, the same defect would
// have delivered a console customer's change down a path they have no CI integration for, and reported
// success.
//
// The rule it makes concrete: a ledger entry carries what the REPAIR path needs, not what the writing
// path happened to have in scope.
func TestAReconstructedRunKeepsItsOriginAndSubject(t *testing.T) {
	f, run, _, _ := approvableWithDelivery(t)
	if run.Plan.Origin != OriginConsole {
		t.Fatalf("the fixture's run is %q, not a console run; this test would prove nothing", run.Plan.Origin)
	}

	rebuilt, found, err := f.svc.Run(context.Background(), run.TenantID, run.RunID)
	if err != nil || !found {
		t.Fatalf("the run could not be read back: found=%v err=%v", found, err)
	}
	if rebuilt.Plan.Origin != run.Plan.Origin {
		t.Fatalf("a run reconstructed from the ledger has origin %q, want %q. The origin decides the "+
			"delivery MODE and whether the run may deliver at all, and neither is re-derivable",
			rebuilt.Plan.Origin, run.Plan.Origin)
	}
	if rebuilt.Plan.WorkflowID != run.Plan.WorkflowID {
		t.Fatalf("a reconstructed run has workflow %q, want %q — there is nothing to resolve a route "+
			"against", rebuilt.Plan.WorkflowID, run.Plan.WorkflowID)
	}
	if rebuilt.Plan.SourceRevision != run.Plan.SourceRevision {
		t.Fatalf("a reconstructed run lost its source revision")
	}
}

// TestAReconstructedRunKeepsItsScope is the second half of the same defect family, and it fails in a
// quieter way than the origin one did.
//
// 🔴 A run rebuilt without its axes had to ASSUME a scope, and the only available assumption is all
// nine — so every axis the plan excluded rendered as a measured zero. "The plan did not look at this
// axis" and "this axis produced nothing" are opposite findings, and `AxisStage.InScope` exists exactly
// so a bare zero cannot conflate them. Assuming the scope re-created the conflation the field removes.
func TestAReconstructedRunKeepsItsScope(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisMemory, "cfg_mem", passingVerdict(0.08))
	rem := &remeasureStub{byProposal: map[string]Measurement{}, errs: map[string]error{}}
	f.svc.Approvals, f.svc.Remeasure = NewMemApprovalGate(), rem
	f.svc.Subject = func(_ context.Context, _ Plan, p VerifiedProposal) (Binding, error) {
		return Binding{ConfigHash: p.ConfigHash, SourceRevision: "abc123def456"}, nil
	}
	// A question naming ONE axis.
	run := f.run(t, "improve my memory strategy")
	if len(run.Plan.Axes) != 1 {
		t.Fatalf("the plan's scope is %v; this test needs a single-axis plan to prove anything", run.Plan.Axes)
	}

	rebuilt, found, err := f.svc.Run(context.Background(), run.TenantID, run.RunID)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	inScope := 0
	for _, row := range rebuilt.PerAxis {
		if row.InScope {
			inScope++
		}
	}
	if inScope != 1 {
		t.Fatalf("a reconstructed run reports %d axes in scope and the plan named 1. Every axis the "+
			"plan excluded then renders as a measured zero — the exact conflation InScope removes",
			inScope)
	}
	if row, _ := rebuilt.AxisRow(assessment.AxisMemory); !row.InScope {
		t.Fatal("the axis the plan actually named is reported out of scope")
	}
}

// TestAPlanEntryWithoutAnOriginIsRefused makes the requirement structural rather than a convention the
// next writer has to remember.
func TestAPlanEntryWithoutAnOriginIsRefused(t *testing.T) {
	for _, e := range []Entry{
		{RunID: "r", TenantID: "t", Kind: KindPlanCreated, WorkflowID: "wf"},      // no origin
		{RunID: "r", TenantID: "t", Kind: KindPlanCreated, Origin: OriginConsole}, // no workflow
	} {
		if err := e.Validate(); err == nil {
			t.Fatalf("a plan entry missing the workflow or the origin validated (%+v). A run "+
				"reconstructed from it delivers on the wrong surface, which changes which credential "+
				"is used", e)
		}
	}
}
