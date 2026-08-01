package adminops_test

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/linkingest"
)

// honesty_test.go defends the three corrections P26 wave 26a made to figures that were ALREADY
// SHIPPING on the operator console (tasks 2.2, 2.6, 2.7, and their supporting 2.3 and 2.4).
//
// These are not new-feature tests. Each one names the requirement it defends, so a later change that
// removes the correction fails with an explanation rather than with a diff nobody can interpret. That
// matters more here than usual: all three defects survived fourteen phases precisely because nothing
// asserted them, and a correction with no assertion is a correction with a half-life.

// TestLinkCoverageIsDisplayedBesideEverySUMDerivedFigure defends P26 task 2.2 and the requirement
// "Link coverage SHALL be displayed beside every SUM-derived figure on the operator console".
//
// 🔴 If this fails, the operator billing surface has gone back to showing a SUM figure that nothing
// qualifies. `openspec/project.md` states the rule once: metering counts only what it observed, and
// link coverage is displayed wherever a derived figure is shown. A billing operator issuing a credit
// against a SUM figure with 31% coverage is acting on a number wrong by an unknown factor.
func TestLinkCoverageIsDisplayedBesideEverySUMDerivedFigure(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantAcme)

	// What the platform OBSERVED: ten runs reported, four linked. The figure below reflects four.
	h.links.ObserveRunsReported(tenantAcme, 10)
	for i := 0; i < 4; i++ {
		if _, err := h.links.Record(linkingest.LinkedRun{
			RunID: "run-acme-" + string(rune('a'+i)), TenantID: tenantAcme, LinkedAt: h.clk.now(),
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	view, err := h.bills.Oversight(h.ctx(adminrbac.RoleBillingOps), tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}

	if !view.LinkCoverage.Known {
		t.Fatal("link coverage is not known — the surface would show a SUM figure it cannot qualify")
	}
	if got := view.LinkCoverage.Percent; got < 39 || got > 41 {
		t.Fatalf("link coverage = %.1f%%, want 40%% (4 of 10 runs linked)", got)
	}
	if view.MeteredSUM == nil {
		t.Fatal("no SUM-derived figure was produced with known coverage")
	}
	// 🔴 The pairing is in the TYPE. This is the assertion that fails if somebody adds a derived figure
	// without its coverage, because there is no constructor that produces one.
	if view.MeteredSUM.Coverage == nil {
		t.Fatal("the SUM-derived figure carries no coverage — the pairing that makes this correction " +
			"structural has been broken; see adminops.DerivedFigure")
	}
	if *view.MeteredSUM.Coverage != view.LinkCoverage.Percent {
		t.Fatalf("the figure's coverage (%.1f) disagrees with the surface's (%.1f) — two sources for one claim",
			*view.MeteredSUM.Coverage, view.LinkCoverage.Percent)
	}
	// The coverage must be a statement in the same view, not a number the reader has to interpret.
	if !strings.Contains(view.LinkCoverage.Statement, "LINKED runs only") {
		t.Fatalf("the coverage statement does not say what the figure counts: %q", view.LinkCoverage.Statement)
	}
	if !strings.Contains(view.LinkCoverage.Statement, "never inferred") {
		t.Fatalf("the coverage statement does not refuse extrapolation: %q", view.LinkCoverage.Statement)
	}
	// The rows the coverage qualifies are marked, so a percentage beside the table does not appear to
	// qualify a seat count or a plan change.
	var sumDerived int
	for _, line := range view.Invoices {
		if line.SUMDerived {
			sumDerived++
		}
	}
	if sumDerived == 0 {
		t.Fatal("no invoice row is marked SUM-derived — the coverage would appear to qualify every row")
	}
}

// TestAFigureWithUnknownCoverageIsWithheld defends P26 task 2.3 and the requirement "A figure whose
// coverage is unknown SHALL NOT be rendered".
//
// The distinction under test is the one that is invisible on screen: 0% coverage and unknown coverage
// look identical as a number and mean opposite things.
func TestAFigureWithUnknownCoverageIsWithheld(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantBoreal)
	// Nothing reported, nothing linked: coverage is UNKNOWN, not zero.

	view, err := h.bills.Oversight(h.ctx(adminrbac.RoleBillingOps), tenantBoreal, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}
	if view.LinkCoverage.Known {
		t.Fatal("coverage reported as known with nothing linked and nothing reported")
	}
	if view.MeteredSUM != nil {
		t.Fatalf("a SUM-derived figure was produced with unknown coverage: %+v — the figure must be "+
			"withheld, because a wrong number gets acted on and a missing one prompts a question", *view.MeteredSUM)
	}
	if view.GainshareSavings != nil {
		t.Fatal("a gainshare figure was produced with unknown coverage")
	}
	if !strings.Contains(view.LinkCoverage.Statement, "unknown") {
		t.Fatalf("the surface does not state that coverage is unknown: %q", view.LinkCoverage.Statement)
	}
	// And it must not be filled with an estimate.
	for _, word := range []string{"estimate", "approximately", "projected"} {
		if strings.Contains(strings.ToLower(view.LinkCoverage.Statement), word) {
			t.Fatalf("the unknown-coverage statement offers an estimate (%q): %q", word, view.LinkCoverage.Statement)
		}
	}
}

// TestAGainshareFigureNamesTheVerifiedDeltaLedger defends P26 task 2.4 and the requirement "A
// gainshare or verified-savings figure SHALL name the verified-delta ledger it drew on".
//
// Provenance on the SURFACE, not in a document: an operator has to be able to tell a verified figure
// from an unverified estimate without leaving the page.
func TestAGainshareFigureNamesTheVerifiedDeltaLedger(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantAcme)
	h.links.ObserveRunsReported(tenantAcme, 2)
	for i := 0; i < 2; i++ {
		if _, err := h.links.Record(linkingest.LinkedRun{
			RunID: "run-acme-g" + string(rune('a'+i)), TenantID: tenantAcme, LinkedAt: h.clk.now(),
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	view, err := h.bills.Oversight(h.ctx(adminrbac.RoleBillingOps), tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}
	if view.GainshareSavings == nil {
		t.Fatal("no gainshare figure was produced with complete coverage")
	}
	if view.GainshareSavings.Source != adminops.SourceVerifiedDeltaLedger {
		t.Fatalf("the gainshare figure names %q as its source, want %q — a savings figure drawing on "+
			"anything else is an unverified estimate wearing a billable figure's clothes",
			view.GainshareSavings.Source, adminops.SourceVerifiedDeltaLedger)
	}
	if !strings.Contains(view.GainshareSavings.Basis, "Unverified authored changes are excluded") {
		t.Fatalf("the gainshare figure does not state the exclusion: %q", view.GainshareSavings.Basis)
	}
	// And the SUM figure must NOT claim the verified-delta ledger — two figures, two provenances.
	if view.MeteredSUM != nil && view.MeteredSUM.Source == adminops.SourceVerifiedDeltaLedger {
		t.Fatal("the metered SUM figure claims the verified-delta ledger as its source")
	}
}

// TestASeededUnverifiedAuthoredChangeContributesExactlyZero defends P26 tasks 2.5 and 2.6 and the
// requirement "Every aggregate improvement, savings or quality figure SHALL exclude unverified
// authored changes".
//
// 🔴 It seeds one and reads the FIGURES. Asserting that a `WHERE` clause exists is asserting that we
// wrote a `WHERE` clause; this asserts the number does not move. The difference is the whole point —
// this repository has already found a green suite sitting on top of a fake written to match it.
func TestASeededUnverifiedAuthoredChangeContributesExactlyZero(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	before, err := h.crossTenant.View(ctx, adminops.AggregateAuthoredImprovement, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	baseline := figuresOf(t, before)

	// One authored change, applied and never measured by the harness.
	if err := h.authored.Append(context.Background(), authoring.Entry{
		ChangeID: "chg-unverified-1", Action: authoring.ActionSubmitted, TenantID: tenantAcme,
		ActorID: "user-1", WorkflowID: "wf-1", ConfigHash: "cfg-unverified",
		Axis: "prompt", Origin: "studio", VerificationState: authoring.StateUnverified, At: h.clk.now(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	after, err := h.crossTenant.View(ctx, adminops.AggregateAuthoredImprovement, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	got := figuresOf(t, after)

	for _, label := range adminops.ImprovementFigures {
		if got[label] != baseline[label] {
			t.Fatalf("%s moved from %v to %v after seeding ONE UNVERIFIED authored change. "+
				"An applied-but-unmeasured change must contribute exactly zero to every improvement, "+
				"savings and quality figure (P26 §2.5; authoring.CountableAggregate is the one filter).",
				label, baseline[label], got[label])
		}
	}
	// And the exclusion must be VISIBLE — an invisible exclusion is indistinguishable from an oversight.
	if got[adminops.RowExcludedUnverified] != baseline[adminops.RowExcludedUnverified]+1 {
		t.Fatalf("%s did not count the seeded change (%v → %v) — the surface must be able to say "+
			"'we looked at this and did not count it'", adminops.RowExcludedUnverified,
			baseline[adminops.RowExcludedUnverified], got[adminops.RowExcludedUnverified])
	}
	if !strings.Contains(after.Note, "Unverified authored changes are excluded") {
		t.Fatalf("the aggregate does not state the exclusion where the figures appear: %q", after.Note)
	}

	// The control half: a VERIFIED change DOES move the improvement figure. Without this, a filter that
	// excluded everything would pass the assertion above and the test would prove nothing.
	if err := h.authored.Append(context.Background(), authoring.Entry{
		ChangeID: "chg-verified-1", Action: authoring.ActionSubmitted, TenantID: tenantAcme,
		ActorID: "user-1", WorkflowID: "wf-1", ConfigHash: "cfg-verified",
		Axis: "prompt", Origin: "studio", VerificationState: authoring.StateVerified, At: h.clk.now(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	verified, err := h.crossTenant.View(ctx, adminops.AggregateAuthoredImprovement, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if figuresOf(t, verified)[adminops.ImprovementFigures[0]] != baseline[adminops.ImprovementFigures[0]]+1 {
		t.Fatal("a VERIFIED authored change did not move the improvement figure — the exclusion is " +
			"excluding everything, and the assertion above would pass on a broken aggregate")
	}
}

// TestTheAuditSurfaceStatesWhichMergePathsTheChainCovers defends P26 task 2.7 and the requirement
// "The audit chain's merge coverage SHALL be stated honestly".
//
// The chain mirrors P6 autonomous merges. It does not, and structurally cannot, mirror P12
// customer-CI-mediated deliveries — those merge in the customer's CI under a credential the platform
// does not hold. An audit log described as the record of "every merge" now covers one of two paths,
// and an auditor concluding "no record, so it did not happen" would be drawing a false conclusion
// from a true page.
func TestTheAuditSurfaceStatesWhichMergePathsTheChainCovers(t *testing.T) {
	h := newHarness(t)

	view, err := h.auditView.Entries(h.ctx(adminrbac.RolePlatformSRE), adminaudit.Filter{}, 10)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	cov := view.MergeCoverage

	if len(cov.Covered) == 0 {
		t.Fatal("the audit surface names no covered merge path")
	}
	if len(cov.NotCovered) == 0 {
		t.Fatal("the audit surface names no UNCOVERED merge path — which is the whole correction: " +
			"before P26 it implied it covered every merge")
	}
	var sawAutonomous, sawCIMediated bool
	for _, p := range cov.Covered {
		if p.ID == "p6-autonomous-merge" {
			sawAutonomous = true
			if !strings.Contains(p.Mechanism, "mergeaudit.go") {
				t.Fatalf("the covered path does not name the code that records it: %q", p.Mechanism)
			}
		}
	}
	for _, p := range cov.NotCovered {
		if p.ID == "p12-ci-mediated-delivery" {
			sawCIMediated = true
			// A gap with a destination is a different thing from a gap.
			if p.ReadableAt != "/delivery" {
				t.Fatalf("the uncovered path does not name where it IS readable: %q", p.ReadableAt)
			}
		}
	}
	if !sawAutonomous || !sawCIMediated {
		t.Fatalf("merge-path coverage is incomplete: autonomous=%v ci-mediated=%v", sawAutonomous, sawCIMediated)
	}
	if !strings.Contains(cov.Statement, "does not") && !strings.Contains(cov.Statement, "NOT") {
		t.Fatalf("the statement does not say what the chain omits: %q", cov.Statement)
	}
	if !strings.Contains(cov.Statement, "not evidence that it did not happen") {
		t.Fatalf("the statement does not warn against reading absence as evidence: %q", cov.Statement)
	}
}

// figuresOf indexes an aggregate's rows by label, failing on a duplicate — two rows with one label
// would make every assertion above depend on iteration order.
func figuresOf(t *testing.T, m adminops.ReadModel) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, r := range m.Rows {
		if _, dup := out[r.Label]; dup {
			t.Fatalf("aggregate %s has two rows labelled %q", m.Aggregate, r.Label)
		}
		out[r.Label] = r.Value
	}
	return out
}
