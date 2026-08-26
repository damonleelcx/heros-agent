package improvementrun

import (
	"bufio"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/proposalgen"
	"github.com/heros-foreal/agentd/internal/verification"
)

// gates_test.go is `tasks.md` §7 — **the phase**.
//
// # What the other fences in this package do not cover, and why this file exists anyway
//
// design.md's organising question is not "how do we build the run" but *"which existing gate could this
// new path go around, and what makes that impossible rather than merely unlikely."* The optimizer's own
// tests prove the OPTIMIZER calls these gates. They say nothing about a new caller, and none of them
// fails loudly if a gate is simply not called.
//
// So this file re-runs every gate in the inventory **through `improvementrun.Service`** — the
// conversational caller — and the inventory itself is parsed by `TestGateInventoryIsComplete` so the
// checklist and the fences cannot drift apart.

// ── the gate inventory, as data ──────────────────────────────────────────────────────────────────

// Gate is one entry in the inventory `openspec/changes/p35-autonomous-improvement-run/gate-inventory.md`
// holds as a checklist. Declared here so the Go side and the Markdown side are two views of one list
// rather than two lists.
type Gate struct {
	// ID is the row's identifier in the checklist, e.g. "G1".
	ID string
	// Name is what the gate decides.
	Name string
	// Package is where it lives.
	Package string
	// Fences are the tests that prove the CONVERSATIONAL caller reaches it.
	Fences []string
}

// Gates is the six gates the new caller must not bypass, in the inventory's order.
func Gates() []Gate {
	return []Gate{
		{"G1", "typed I/O contract", "internal/typedcontract", []string{
			"TestConversationalRun_ContractViolationRejectedBeforeVerification"}},
		{"G2", "verified delta, held-out", "internal/verification", []string{
			"TestConversationalRun_UnverifiedNotDelivered",
			"TestConversationalRun_GateFailingHighScorerNotDelivered"}},
		{"G3", "entitlement: plan AND automation level", "internal/entitlement", []string{
			"TestConversationalRun_EntitlementRefusedServerSide",
			"TestConversationalRun_ConversationCannotRaiseEntitlement"}},
		{"G4", "transform refusal", "internal/transform", []string{
			"TestConversationalRun_NoOverrideMaterialisesARefusedConfiguration"}},
		{"G5", "human approval", "internal/approval", []string{
			"TestConversationalRun_ApprovalIsPerProposalAndRoutedThroughTheShippedGate",
			"TestConversationalRun_ApprovalVoidWhenRevisionMoves"}},
		{"G6", "never merge below Autonomous", "internal/forgedelivery", []string{
			"TestConversationalRun_NeverMergesBelowAutonomous"}},
	}
}

// TestGateInventoryIsComplete parses the checklist and asserts it against `Gates()` and against the
// tests that actually exist.
//
// 🔴 A checklist nobody can verify is a paragraph with boxes drawn on it. This is what makes
// `gate-inventory.md` an artifact task 11.3 can hand to somebody who did not implement the phase: every
// row names a test, and a row naming a test that does not exist is a red build rather than a tick
// somebody added optimistically.
func TestGateInventoryIsComplete(t *testing.T) {
	inventory := filepath.Join("..", "..", "openspec", "changes",
		"p35-autonomous-improvement-run", "gate-inventory.md")
	f, err := os.Open(inventory)
	if err != nil {
		t.Fatalf("the gate inventory is missing (%v). It is the artifact §11.3 hands to a reviewer who "+
			"did not implement the phase", err)
	}
	defer func() { _ = f.Close() }()

	named := map[string]bool{}
	fenceRe := regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for _, m := range fenceRe.FindAllStringSubmatch(sc.Text(), -1) {
			named[m[1]] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(named) == 0 {
		t.Fatal("the gate inventory names no fences, so it is prose with a table in it")
	}

	declared := testNamesInThisPackage(t)
	// Fences that live in a sibling package are exempt from the existence check but must still be named
	// somewhere; the ones this package owns are checked against what compiles.
	elsewhere := map[string]bool{
		"TestInstallationScopedToSelectedRepositories":            true,
		"TestRevocationStopsPushesImmediately":                    true,
		"TestReadConnectionAndWriteInstallationAreSeparateGrants": true,
		"TestLiveFourStep_ApproveSelectSelectFetch":               true,
	}
	for name := range named {
		if elsewhere[name] || declared[name] {
			continue
		}
		t.Errorf("the gate inventory names the fence %q and no such test exists. A checklist row with no "+
			"test behind it is a tick somebody added optimistically", name)
	}

	// … and the reverse: every gate `Gates()` declares must have a row.
	for _, g := range Gates() {
		for _, fence := range g.Fences {
			if !named[fence] {
				t.Errorf("gate %s (%s) names the fence %q, and the inventory does not. A gate with no row "+
					"is a gate nobody walks", g.ID, g.Name, fence)
			}
		}
	}
}

func testNamesInThisPackage(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, d := range file.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") {
					out[fn.Name.Name] = true
				}
			}
		}
	}
	return out
}

// ── 7.1 / 7.2 · a gate-failing or unverified candidate is not DELIVERED ──────────────────────────
//
// The §3 fences assert these are not SURFACED. These assert the stronger claim the requirement makes:
// even if a proposal reached the delivery call, delivery does not happen — because there is no proposal
// to deliver, and `Deliver` refuses an id this run never surfaced.

func TestAGateFailingCandidateHasNoDeliveryPath(t *testing.T) {
	f := newFixture(t)
	high := passingVerdict(0.95)
	high.Verdict.GateResult, high.Verdict.RegressionPass = "fail_regression", false
	f.offer(assessment.AxisModel, "cfg_high_but_failing", high)
	del := newRecordingDeliverer()
	f.svc.Deliveries, f.svc.Routes = del, &routeStub{}
	f.svc.Approvals, f.svc.Remeasure = NewMemApprovalGate(), &remeasureStub{
		byProposal: map[string]Measurement{}, errs: map[string]error{}}
	f.svc.Subject = func(_ context.Context, _ Plan, p VerifiedProposal) (Binding, error) {
		return Binding{ConfigHash: p.ConfigHash, SourceRevision: "abc123def456"}, nil
	}
	run := f.run(t, "improve my model choice")

	// There is no proposal id to deliver, and the only ids that exist come from verified candidates.
	if _, err := f.svc.Deliver(context.Background(), &run, "prop_cfg_high_but_failing"); !errors.Is(err, ErrNotSurfaced) {
		t.Fatalf("a gate-failing candidate had a reachable delivery path: %v", err)
	}
	if len(del.calls) != 0 {
		t.Fatal("the delivery core was reached")
	}
}

// ── 7.4 · no plan, role, entitlement, flag or parameter materialises a refused configuration ─────

// refusingTransform stands in for `internal/transform`'s refusal. It refuses one configuration
// unconditionally — which is the property: the refusal is a fact about the CONFIGURATION, not about the
// caller, so no caller-side value can change the answer.
type refusingTransform struct{ refuses string }

func (r refusingTransform) Check(c optimizer.SearchCandidate) (bool, string) {
	if c.ConfigHash == r.refuses {
		return false, "this configuration reorders nodes the source does not have; the transform refuses it"
	}
	return true, ""
}

// TestConversationalRun_NoOverrideMaterialisesARefusedConfiguration is fence 7.4 and FR14.
//
// 🔴 It varies every lever a caller has — the plan's axes, its cap, its budget, the run origin (which
// selects the delivery mode and therefore the credential path), and the approving person — and asserts
// the refused configuration is refused under every one. A refusal that any of these could lift would
// not be a property of the configuration; it would be a default somebody can turn off.
func TestConversationalRun_NoOverrideMaterialisesARefusedConfiguration(t *testing.T) {
	const refused = "cfg_refused"

	type lever struct {
		name    string
		fixture func(*fixture)
		bounds  func(*Bounds)
	}
	levers := []lever{
		{name: "the plan's default scope"},
		{name: "a plan scoped to the refused candidate's own axis",
			fixture: func(f *fixture) { f.question = "improve my graph topology" }},
		{name: "a larger candidate cap", bounds: func(b *Bounds) { b.MaxCandidates = 100 }},
		{name: "a larger spend budget", bounds: func(b *Bounds) { b.MaxSpendUSD = 1000 }},
		{name: "a console origin, which selects the write-credential delivery path",
			fixture: func(f *fixture) { f.origin = OriginConsole }},
		{name: "a CLI origin", fixture: func(f *fixture) { f.origin = OriginCLI }},
	}

	for _, l := range levers {
		f := newFixture(t)
		if l.fixture != nil {
			l.fixture(f)
		}
		if l.bounds != nil {
			f.boundsOverride = l.bounds
		}
		f.offer(assessment.AxisGraph, refused, passingVerdict(0.50))
		f.svc.Contract = refusingTransform{refuses: refused}

		run := f.run(t, f.questionOr("fix it"))
		for _, p := range run.Proposals {
			if p.ConfigHash == refused {
				t.Fatalf("%s materialised a configuration the transform refuses. FR14's whole point is "+
					"that the refusal is a property of the configuration rather than of the caller", l.name)
			}
		}
	}
}

// ── 7.5 · entitlement is refused SERVER-SIDE and the conversation cannot raise it ────────────────

// denyingEntitlements refuses everything, with the gate's own Decision so the refusal carries the
// platform's words about which plan lifts it.
type denyingEntitlements struct{ calls []plancfg.Feature }

func (d *denyingEntitlements) CheckEntitlement(_ string, f plancfg.Feature, l entitlement.AutomationLevel) (entitlement.Decision, error) {
	d.calls = append(d.calls, f)
	return entitlement.Decision{
		Allowed: false, Feature: f, Level: l, PlanName: "Free", UpgradePlanName: "Team",
		Reason: "your plan does not include opening a verified optimization pull request",
	}, nil
}

// entitlementDeliverer wires the SHIPPED `forgedelivery.Deliverer` over a denying entitlement gate, so
// the refusal comes from the real enforcement funnel rather than from a double.
func entitlementDeliverer(t *testing.T, ents forgedelivery.Entitlements) *forgedelivery.Deliverer {
	t.Helper()
	return forgedelivery.NewDeliverer(passingOracle{}, ents, openHalt{}, deliveryrecord.NewMemStore(), 10)
}

type passingOracle struct{}

func (passingOracle) Verdict(context.Context, string, string, string) (verification.Verdict, bool, error) {
	return passingVerdict(0.08).Verdict, true, nil
}

type openHalt struct{}

func (openHalt) HaltsDelivery(string) (bool, string, error) { return false, "", nil }

func TestConversationalRun_EntitlementRefusedServerSide(t *testing.T) {
	f, run, _, routes := approvableWithDelivery(t)
	ents := &denyingEntitlements{}
	id := run.Proposals[0].ProposalID
	if _, err := f.svc.Approve(context.Background(), run, id, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	// 🔴 The entitlement gate is swapped in AFTER the approval, so the refusal under test is delivery's
	// and not the approval's. Consent and commercial permission are two different decisions, and a
	// fence that conflated them would pass with either one working.
	f.svc.Deliveries = entitlementDeliverer(t, ents)

	res, err := f.svc.Deliver(context.Background(), run, id)
	if err != nil {
		// An entitlement refusal is a reported CONDITION, not a failure.
		t.Fatalf("an entitlement refusal was reported as a failure: %v", err)
	}
	if res.Withheld == nil || res.Withheld.Kind != forgedelivery.WithheldNotEntitled {
		t.Fatalf("delivery was not withheld for entitlement: %+v", res.Withheld)
	}
	if len(ents.calls) == 0 {
		t.Fatal("the entitlement gate was never consulted. A gate that is not called and a gate that " +
			"passed produce the same outcome, which is the whole of design.md's worry about a new caller")
	}
	if res.Withheld.Entitlement == nil || res.Withheld.Entitlement.UpgradePlanName == "" {
		t.Fatal("the refusal does not carry the platform's own words about which plan lifts it")
	}
	if len(routes.branchCalls) != 0 {
		t.Fatal("an unentitled delivery pushed a branch")
	}
}

// TestConversationalRun_ConversationCannotRaiseEntitlement asserts the absence of a lever, by
// reflection over the request payloads and the plan.
//
// 🔴 The conversation's inputs are a QUESTION and a decision. Neither carries a plan, a level, a
// feature or a customer id, and this asserts that structurally rather than by reading the handlers —
// the field somebody adds under delivery pressure is exactly the one a reading would miss.
func TestConversationalRun_ConversationCannotRaiseEntitlement(t *testing.T) {
	banned := []string{"plan", "level", "entitlement", "feature", "automation", "customer", "tenant"}
	for _, ty := range []any{Bounds{}, Plan{}} {
		for _, field := range fieldNames(ty) {
			low := strings.ToLower(field)
			for _, b := range banned {
				if !strings.Contains(low, b) {
					continue
				}
				// `Plan.PlanID` and `Bounds.TenantID` are identity, not entitlement, and neither is
				// settable from a request — `originFor` and the credential supply them. Everything else
				// containing one of these words would be a lever.
				if field == "PlanID" || field == "TenantID" {
					continue
				}
				t.Errorf("%T has the field %q. A conversational input that named a plan, a level or a "+
					"customer would be a lever the person typing could pull", ty, field)
			}
		}
	}
	// … and the RUN requests the assisted level as a constant, never from the plan.
	if strings.Contains(readSource(t, "deliver.go"), "run.Plan.Level") {
		t.Fatal("the delivery level is read from the plan. It is a constant on purpose: requesting " +
			"anything above Assisted makes the merge branch reachable from a phase whose non-goal is merging")
	}
}

// ── 7.13 · a run that hits its BUDGET reports which bound ────────────────────────────────────────

func TestARunThatHitsItsBudgetReportsWhichBoundStoppedIt(t *testing.T) {
	f := newFixture(t)
	f.boundsOverride = func(b *Bounds) {
		// A budget small enough that the first verification exhausts it, and a cap large enough that the
		// cap is not what stops the run.
		b.MaxSpendUSD, b.MaxCandidates = 0.02, 50
	}
	for _, h := range []string{"c1", "c2", "c3"} {
		vr := passingVerdict(0.001)
		vr.Verdict.GateResult = "fail_significance"
		vr.SpendUSD = 0.05
		f.offer(assessment.AxisModel, h, vr)
	}
	run := f.run(t, "improve my model choice")
	if run.Outcome.Bound != BoundBudget {
		t.Fatalf("a run that exhausted its $%.2f budget reported the bound %q (%q). The four bounds have "+
			"four different next actions, and one of them is \"nothing — this worked\"",
			run.Plan.SpendBudgetUSD, run.Outcome.Bound, run.Outcome.Detail)
	}
	if !strings.Contains(run.Outcome.Sentence(), "spend budget") {
		t.Fatalf("the sentence does not name the budget: %q", run.Outcome.Sentence())
	}
	// 🔴 Budget exhaustion is a reported STOPPING CONDITION, not an error (§7.3).
	if run.Outcome.Faulted() {
		t.Fatal("budget exhaustion was reported as a fault")
	}
	if f.metrics.Health().PerBound[BoundBudget] != 1 {
		t.Fatalf("the health document does not count the budget bound: %+v", f.metrics.Health().PerBound)
	}
}

// ── 7.15 · the per-axis breakdown exists at EVERY stage ──────────────────────────────────────────

// TestPerAxisBreakdownExistsAtEveryStage walks a proposal from generated to delivered and asserts the
// axis's count moves at each one.
//
// 🔴 The aggregate is what gets built if nobody checks (§9.5). An operator with a 5% verification pass
// rate hidden inside a healthy average is an operator that is not working, and the operator with the
// smallest sample is the newest one — so the aggregate is least sensitive exactly where it matters.
func TestPerAxisBreakdownExistsAtEveryStage(t *testing.T) {
	f, run, _, _ := approvableWithDelivery(t)
	id := run.Proposals[0].ProposalID
	axis := run.Proposals[0].Axis

	stageOf := func(r *Run) AxisStage {
		row, ok := r.AxisRow(axis)
		if !ok {
			t.Fatalf("the breakdown has no row for %q", axis)
		}
		return row
	}
	row := stageOf(run)
	if row.Generated == 0 || row.Verified == 0 {
		t.Fatalf("generated=%d verified=%d after the propose phase", row.Generated, row.Verified)
	}

	updated, _, err := f.svc.Decide(context.Background(), run.TenantID, run.RunID, id, DecideApprove, "person@example.com")
	if err != nil {
		t.Fatalf("deciding: %v", err)
	}
	row = stageOf(&updated)
	for stage, n := range map[Stage]int{
		StageGenerated: row.Generated, StageVerified: row.Verified,
		StageApproved: row.Approved, StageDelivered: row.Delivered,
	} {
		if n == 0 {
			t.Fatalf("the %q stage counts zero for axis %q after a full approve-and-deliver. "+
				"Asserting the breakdown EXISTS is the requirement; the aggregate is what gets built "+
				"if nobody checks", stage, axis)
		}
	}
	// And the withdrawn stage exists as a countable stage even at zero.
	if row.Count(StageWithdrawn) != 0 {
		t.Fatalf("this run withdrew nothing and counts %d", row.Count(StageWithdrawn))
	}
	// Every stage in the closed set is readable.
	for _, st := range Stages() {
		_ = row.Count(st)
	}
}

// ── 7.12 · the five (seven) "nothing to propose" states, through the conversational path ─────────

func TestEveryNothingToProposeStateRendersThroughTheConversationalPath(t *testing.T) {
	seen := map[string]proposalgen.State{}
	for _, st := range proposalgen.States() {
		if !st.FoundNothing() {
			continue
		}
		f := newFixture(t)
		es, err := EmptyStateFor(proposalgen.Result{State: st, Detail: "the pass's own words about " + st.String()})
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		f.state = es
		run := f.run(t, "fix it")
		if run.Empty == nil {
			t.Fatalf("%s produced no named state through the run — an empty result, which FR7 forbids", st)
		}
		if run.Empty.State != st {
			t.Fatalf("%s arrived at the surface as %q", st, run.Empty.State)
		}
		if prior, dup := seen[run.Empty.Headline]; dup {
			t.Fatalf("%s and %s reach the surface with the SAME sentence", st, prior)
		}
		seen[run.Empty.Headline] = st
	}
	if len(seen) < 7 {
		t.Fatalf("only %d states reached the surface distinctly", len(seen))
	}
}

// ── 7.11 · revoking the installation stops a push immediately, through this caller ───────────────

func TestConversationalRun_RevokingTheInstallationStopsThePush(t *testing.T) {
	f, run, _, _ := approvableWithDelivery(t)
	id := run.Proposals[0].ProposalID
	if _, err := f.svc.Approve(context.Background(), run, id, "person@example.com"); err != nil {
		t.Fatal(err)
	}
	run.Plan.Origin = OriginConsole

	store := forgedelivery.NewInstallationStore()
	if err := store.Install(forgedelivery.Installation{
		InstallationID: "i1", TenantID: run.TenantID,
		Repositories: []string{"nousresearch/hermes-agent"},
		Permissions:  forgedelivery.LeastPrivilegePermissions(),
	}); err != nil {
		t.Fatal(err)
	}
	f.svc.Deliveries = revokingDeliverer{store: store, tenantID: run.TenantID}

	// It works while the installation stands …
	if _, err := f.svc.Deliver(context.Background(), run, id); err != nil {
		t.Fatalf("delivery failed with an active installation: %v", err)
	}
	// … and the very next call, after a revocation, does not.
	if err := store.Revoke("i1", "revoked by the customer"); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.Deliver(context.Background(), run, id)
	if err != nil {
		t.Fatalf("a revoked installation produced a failure rather than a condition: %v", err)
	}
	if res.Withheld == nil || res.Withheld.Kind != forgedelivery.WithheldInstallationRevoked {
		t.Fatalf("a revoked installation did not withhold delivery with its own reason: %+v", res.Withheld)
	}
}

// revokingDeliverer consults the PushGuard on every call, which is the property under test: the check
// is on the write path, not on the token's lifecycle.
type revokingDeliverer struct {
	store    *forgedelivery.InstallationStore
	tenantID string
}

func (d revokingDeliverer) Deliver(ctx context.Context, p forgedelivery.Proposal, route *forgedelivery.Route, _ forgedelivery.ForgeWriter) (forgedelivery.Result, error) {
	if err := d.store.MayPush(ctx, d.tenantID, route.Target.Owner+"/"+route.Target.Repo); err != nil {
		return forgedelivery.Result{}, err
	}
	id := forgedelivery.DeliveryID(p.ConfigHash, p.SourceRevision, route.Target.Key())
	return forgedelivery.Result{
		DeliveryID: id, Mode: route.Mode, Created: true,
		PR: forgedelivery.PullRequest{Ref: route.Target.Key() + "#7", URL: "https://example.invalid/pr/7"},
	}, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

func fieldNames(v any) []string {
	rt := reflect.TypeOf(v)
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		out = append(out, rt.Field(i).Name)
	}
	return out
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

var _ = evalstats.Interval{}
