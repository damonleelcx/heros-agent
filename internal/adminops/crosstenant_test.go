package adminops_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// crosstenant_test.go covers task 10.3 — the LOAD-BEARING cross-tenant test (FR14):
//
//	an UNAUTHORIZED admin is denied
//	an AUTHORIZED view succeeds AND is logged

// TestUnauthorizedCrossTenantViewIsDeniedAndLogged is FR14's first scenario.
func TestUnauthorizedCrossTenantViewIsDeniedAndLogged(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	for _, agg := range adminops.Aggregates {
		if _, err := h.crossTenant.View(ctx, agg, h.period); !errors.Is(err, adminops.ErrDenied) {
			t.Fatalf("Support viewing %s: err = %v, want ErrDenied", agg, err)
		}
	}
	// A drill-down is the same permission, so it cannot be used as a side door.
	if _, err := h.crossTenant.DrillDown(ctx, tenantAcme, adminops.AggregateUsageSUM, h.period); !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support drilling down: err = %v, want ErrDenied", err)
	}

	denials := h.entriesFor(adminaudit.ActionAuthorizationDenied)
	if len(denials) == 0 {
		t.Fatal("cross-tenant denials were not logged")
	}
	var sawCrossTenant bool
	for _, d := range denials {
		if d.Evidence["capability"] == string(adminrbac.CapCrossTenantRead) {
			sawCrossTenant = true
			if d.ActorAdminID != h.adminIDs[adminrbac.RoleSupport] {
				t.Errorf("the denial names actor %q", d.ActorAdminID)
			}
		}
	}
	if !sawCrossTenant {
		t.Error("no denial names the cross-tenant read capability")
	}
	// And nothing was logged as a VIEW: a denied look is not a look.
	if n := len(h.entriesFor(adminaudit.ActionCrossTenantView)); n != 0 {
		t.Errorf("a denied request logged %d cross-tenant views, want 0", n)
	}
}

// TestAuthorizedCrossTenantViewSucceedsAndIsLogged is FR14's second scenario — the half that is easy
// to forget, because the view worked.
func TestAuthorizedCrossTenantViewSucceedsAndIsLogged(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantAcme)
	h.seedMeteredCharge(tenantBoreal)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	model, err := h.crossTenant.View(ctx, adminops.AggregateUsageSUM, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if model.Suppressed {
		t.Fatalf("a three-tenant fleet was suppressed at the default floor: %s", model.SuppressionReason)
	}
	if model.Cohort != 3 {
		t.Errorf("cohort = %d, want the 3 fixture tenants", model.Cohort)
	}
	if len(model.Rows) == 0 {
		t.Error("the usage aggregate has no rows despite two metered tenants")
	}
	if model.Source == "" {
		t.Error("the read model does not name the substrate it came from")
	}

	views := h.entriesFor(adminaudit.ActionCrossTenantView)
	if len(views) != 1 {
		t.Fatalf("an authorized view logged %d entries, want 1", len(views))
	}
	v := views[0]
	if v.ActorAdminID != h.adminIDs[adminrbac.RolePlatformSRE] {
		t.Errorf("the view log names viewer %q", v.ActorAdminID)
	}
	if v.Evidence["read_model"] != string(adminops.AggregateUsageSUM) {
		t.Errorf("the view log does not name the read model: %v", v.Evidence)
	}
	if v.CreatedAt.IsZero() {
		t.Error("the view log has no timestamp")
	}
	h.assertChainIntact()
}

// TestEveryAggregateIsLoggedOnEveryView: one entry per view, not one per session.
func TestEveryAggregateIsLoggedOnEveryView(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)
	for _, agg := range adminops.Aggregates {
		if _, err := h.crossTenant.View(ctx, agg, h.period); err != nil {
			t.Fatalf("View(%s): %v", agg, err)
		}
	}
	views := h.entriesFor(adminaudit.ActionCrossTenantView)
	if len(views) != len(adminops.Aggregates) {
		t.Fatalf("%d views logged %d entries", len(adminops.Aggregates), len(views))
	}
	// Repeating the same view logs again — an auditor counts looks, not distinct pages.
	if _, err := h.crossTenant.View(ctx, adminops.AggregateUsageSUM, h.period); err != nil {
		t.Fatalf("View: %v", err)
	}
	if n := len(h.entriesFor(adminaudit.ActionCrossTenantView)); n != len(adminops.Aggregates)+1 {
		t.Errorf("a repeated view logged %d entries in total, want %d", n, len(adminops.Aggregates)+1)
	}
}

// TestDrillDownIsLoggedAgainstTheTenant: FR14's "treat a single-tenant drill-down as a per-tenant
// view" — an auditor sees WHICH tenant was looked at, not just that a fleet page was opened.
func TestDrillDownIsLoggedAgainstTheTenant(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantAcme)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	model, err := h.crossTenant.DrillDown(ctx, tenantAcme, adminops.AggregateUsageSUM, h.period)
	if err != nil {
		t.Fatalf("DrillDown: %v", err)
	}
	if model.PerTenant != tenantAcme {
		t.Errorf("the drill-down does not mark itself per-tenant: %+v", model)
	}
	if model.Suppressed {
		t.Error("a per-tenant drill-down was suppressed by the cohort floor — the floor protects " +
			"aggregates, not explicitly per-tenant views the operator is authorized for")
	}
	views := h.entriesFor(adminaudit.ActionCrossTenantView)
	if len(views) != 1 {
		t.Fatalf("a drill-down logged %d entries, want 1", len(views))
	}
	if views[0].Target != adminops.TenantTarget(tenantAcme) {
		t.Errorf("the drill-down was logged against %q, not the tenant looked at", views[0].Target)
	}
}

// TestMinimumCohortFloorSuppressesASmallFleet: the re-identification guard, and it is not rendered as
// an empty result (FR26).
func TestMinimumCohortFloorSuppressesASmallFleet(t *testing.T) {
	h := newHarness(t)
	small := account.NewMemStore()
	if _, err := small.Create(account.Account{
		CustomerID: "tenant-solo", ProviderCustomerHandle: "prov_cus_solo", ActivePlanID: "team",
		PlanConfigVersion: h.plans.Version(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	svc, err := adminops.NewCrossTenantService(h.exec, adminops.CrossTenantConfig{
		Accounts: small, Meter: h.meter, Ledger: h.billing.Ledger(),
	})
	if err != nil {
		t.Fatalf("NewCrossTenantService: %v", err)
	}
	model, err := svc.View(h.ctx(adminrbac.RolePlatformSRE), adminops.AggregateTopConsumers, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if !model.Suppressed {
		t.Fatal("a one-tenant aggregate was reported, which is that tenant's data with a different label")
	}
	if len(model.Rows) != 0 {
		t.Error("a suppressed model still returned rows")
	}
	if model.SuppressionReason == "" {
		t.Error("a suppressed model does not say why — it would render as an empty result")
	}
	if model.Cohort != 1 {
		t.Errorf("cohort = %d, want 1", model.Cohort)
	}
	// The view is still logged: the operator asked to look, and that is what the log records.
	if n := len(h.entriesFor(adminaudit.ActionCrossTenantView)); n != 1 {
		t.Errorf("a suppressed view logged %d entries, want 1", n)
	}
}

// TestTheFloorCannotBeLowered: a deployment may raise the floor, never lower it.
func TestTheFloorCannotBeLowered(t *testing.T) {
	h := newHarness(t)
	if _, err := adminops.NewCrossTenantService(h.exec, adminops.CrossTenantConfig{
		Accounts: h.accounts, MinimumCohort: 1,
	}); err == nil {
		t.Fatal("a minimum-cohort floor of 1 was accepted")
	}
	svc, err := adminops.NewCrossTenantService(h.exec, adminops.CrossTenantConfig{
		Accounts: h.accounts, MinimumCohort: 25,
	})
	if err != nil {
		t.Fatalf("raising the floor: %v", err)
	}
	if svc.MinimumCohort() != 25 {
		t.Errorf("MinimumCohort = %d, want 25", svc.MinimumCohort())
	}
	model, err := svc.View(h.ctx(adminrbac.RolePlatformSRE), adminops.AggregateUsageSUM, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if !model.Suppressed {
		t.Error("raising the floor did not suppress the three-tenant fleet")
	}
}

// TestUnloggableViewDoesNotHappen: a look at tenant data that cannot be recorded is refused, the same
// fail-closed rule the command path applies to writes.
func TestUnloggableViewDoesNotHappen(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	h.audit.SetUnavailable(true)
	defer h.audit.SetUnavailable(false)

	if _, err := h.crossTenant.View(ctx, adminops.AggregateUsageSUM, h.period); !errors.Is(err, adminaudit.ErrStoreUnavailable) {
		t.Fatalf("a view with the audit store down: err = %v, want ErrStoreUnavailable", err)
	}
	if _, err := h.crossTenant.DrillDown(ctx, tenantAcme, adminops.AggregateUsageSUM, h.period); !errors.Is(err, adminaudit.ErrStoreUnavailable) {
		t.Fatalf("a drill-down with the audit store down: err = %v, want ErrStoreUnavailable", err)
	}
}

// TestAnomaliesNameTheirCause: an anomaly count with no cause is a number nobody can act on.
func TestAnomaliesNameTheirCause(t *testing.T) {
	h := newHarness(t)
	sre := h.ctx(adminrbac.RolePlatformSRE)
	if _, err := h.tenants.Suspend(sre, tenantCastle, "non-payment after dunning", adminops.Confirm()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if _, err := h.kill.Arm(sre, adminops.TenantScope(tenantBoreal), "loop is thrashing", adminops.Confirm()); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	model, err := h.crossTenant.View(sre, adminops.AggregateAnomalies, h.period)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	found := map[string]string{}
	for _, r := range model.Rows {
		if r.Detail == "" {
			t.Errorf("anomaly row %q has no cause", r.Label)
		}
		found[r.Label] = r.Detail
	}
	if !strings.Contains(found[tenantCastle], "suspended") {
		t.Errorf("the suspended tenant's anomaly reads %q", found[tenantCastle])
	}
	if !strings.Contains(found[tenantBoreal], "halted") {
		t.Errorf("the halted tenant's anomaly reads %q", found[tenantBoreal])
	}
}

// TestUnknownAggregateIsRefused: a typo must not produce an empty aggregate that reads as "no data".
func TestUnknownAggregateIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	if _, err := h.crossTenant.View(ctx, adminops.Aggregate("made_up"), h.period); err == nil {
		t.Fatal("an unknown aggregate was served")
	}
	if n := len(h.entriesFor(adminaudit.ActionCrossTenantView)); n != 0 {
		t.Error("an unknown aggregate was logged as a view")
	}
}
