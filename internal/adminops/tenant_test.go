package adminops_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// tenant_test.go covers task 3.3 — destructive discipline and tenant lifecycle (FR6, FR7):
//
//	a destructive action records actor + target + reason + timestamp
//	an action WITHOUT a reason does not proceed and changes nothing
//	an irreversible action requires a SECOND confirmation
//	suspending a tenant HALTS its autonomous merges; reactivate restores them

// TestDestructiveActionRecordsActorTargetReasonTimestamp is FR6's first scenario.
func TestDestructiveActionRecordsActorTargetReasonTimestamp(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	const reason = "incident INC-204: tenant's loop is producing regressions"

	receipt, err := h.tenants.Suspend(ctx, tenantAcme, reason, adminops.Confirm())
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if receipt.Result != adminops.ResultApplied {
		t.Fatalf("receipt result = %q, want %q", receipt.Result, adminops.ResultApplied)
	}

	// Write-ahead first, outcome second — and the outcome links back to the write-ahead entry.
	entries := h.entriesFor(adminaudit.ActionTenantSuspend)
	if len(entries) != 2 {
		t.Fatalf("suspend wrote %d audit entries, want 2 (write-ahead + outcome)", len(entries))
	}
	ahead, outcome := entries[0], entries[1]
	if ahead.Result != adminops.ResultAttempted {
		t.Errorf("first entry result = %q, want %q — the write-ahead entry must precede the effect", ahead.Result, adminops.ResultAttempted)
	}
	if ahead.Seq >= outcome.Seq {
		t.Errorf("the outcome entry (seq %d) does not follow the write-ahead entry (seq %d)", outcome.Seq, ahead.Seq)
	}
	if outcome.Evidence["write_ahead_seq"] == "" {
		t.Error("the outcome entry does not reference its write-ahead entry")
	}
	for _, e := range entries {
		if e.ActorAdminID != h.adminIDs[adminrbac.RolePlatformSRE] {
			t.Errorf("entry actor = %q, want the acting admin", e.ActorAdminID)
		}
		if e.Target != adminops.TenantTarget(tenantAcme) {
			t.Errorf("entry target = %q, want %q", e.Target, adminops.TenantTarget(tenantAcme))
		}
		if e.Reason != reason {
			t.Errorf("entry reason = %q, want the operator's recorded reason", e.Reason)
		}
		if e.CreatedAt.IsZero() {
			t.Error("entry has no timestamp")
		}
	}
	h.assertChainIntact()
}

// TestActionWithoutAReasonDoesNotProceed is FR6's second scenario: no reason, no state change.
func TestActionWithoutAReasonDoesNotProceed(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	for _, reason := range []string{"", "   ", "\t\n"} {
		if _, err := h.tenants.Suspend(ctx, tenantAcme, reason, adminops.Confirm()); !errors.Is(err, adminops.ErrNoReason) {
			t.Fatalf("Suspend with reason %q: err = %v, want ErrNoReason", reason, err)
		}
	}
	// No state change.
	acct, err := h.accounts.Get(tenantAcme)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acct.Status.Suspended() {
		t.Fatal("a reasonless suspension took effect")
	}
	// And no suspend entry — not even a write-ahead one, because the friction check runs before it.
	if n := len(h.entriesFor(adminaudit.ActionTenantSuspend)); n != 0 {
		t.Errorf("a refused action wrote %d suspend audit entries, want 0", n)
	}
}

// TestUnconfirmedActionDoesNotProceed: confirmation is required as well as a reason (FR6).
func TestUnconfirmedActionDoesNotProceed(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.tenants.Suspend(ctx, tenantAcme, "non-payment", adminops.Confirmation{}); !errors.Is(err, adminops.ErrNotConfirmed) {
		t.Fatalf("Suspend unconfirmed: err = %v, want ErrNotConfirmed", err)
	}
	if acct, _ := h.accounts.Get(tenantAcme); acct.Status.Suspended() {
		t.Fatal("an unconfirmed suspension took effect")
	}
}

// TestIrreversibleActionRequiresASecondConfirmation is FR6's third scenario, exercised through the
// command path's classification rather than through any one feature.
func TestIrreversibleActionRequiresASecondConfirmation(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSuperadmin)
	target := adminops.SubjectTarget("subject-7741")
	ran := false
	irreversible := adminops.Command{
		Capability: adminrbac.CapGDPRExecute,
		Action:     adminaudit.ActionGDPRExecute,
		Target:     target,
		Reason:     "data-subject erasure request DSR-88",
		Confirm:    adminops.Confirm(), // first confirmation only
	}
	effect := func(context.Context) (map[string]string, error) { ran = true; return nil, nil }

	if _, err := h.exec.Execute(ctx, irreversible, effect); !errors.Is(err, adminops.ErrSecondConfirmation) {
		t.Fatalf("irreversible action with one confirmation: err = %v, want ErrSecondConfirmation", err)
	}
	if ran {
		t.Fatal("the irreversible effect ran without a second confirmation")
	}

	// A typed target that does not MATCH is not a second confirmation either.
	irreversible.Confirm = adminops.ConfirmTyping("subject:wrong-one")
	if _, err := h.exec.Execute(ctx, irreversible, effect); !errors.Is(err, adminops.ErrSecondConfirmation) {
		t.Fatalf("irreversible action with a mistyped target: err = %v, want ErrSecondConfirmation", err)
	}
	if ran {
		t.Fatal("the irreversible effect ran on a mistyped target")
	}

	// Typing the target proceeds.
	irreversible.Confirm = adminops.ConfirmTyping(target)
	if _, err := h.exec.Execute(ctx, irreversible, effect); err != nil {
		t.Fatalf("irreversible action with a typed target: %v", err)
	}
	if !ran {
		t.Fatal("the effect did not run after the second confirmation")
	}
}

// TestSuspendingATenantHaltsItsAutonomousMerges is FR7's load-bearing scenario, asserted through the
// SAME admission gate the P6 loop consults — and through the loop itself.
func TestSuspendingATenantHaltsItsAutonomousMerges(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	// The admission gate satisfies the loop's interface — a compile-time assertion, so a signature
	// change cannot silently unwire the brake.
	var _ optimizer.MergeAdmission = h.admission

	if allowed, why, err := h.admission.AllowMerge(tenantAcme); !allowed || err != nil {
		t.Fatalf("before suspension AllowMerge = %v, %q, %v; want allowed", allowed, why, err)
	}
	if _, err := h.tenants.Suspend(ctx, tenantAcme, "incident INC-204", adminops.Confirm()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	allowed, why, err := h.admission.AllowMerge(tenantAcme)
	if err != nil {
		t.Fatalf("AllowMerge after suspension returned an error: %v", err)
	}
	if allowed {
		t.Fatal("a suspended tenant's autonomous merges were still permitted")
	}
	if why == "" {
		t.Error("the halt has no named reason")
	}

	// Other tenants are unaffected: suspension is per tenant.
	if allowed, _, err := h.admission.AllowMerge(tenantBoreal); !allowed || err != nil {
		t.Fatalf("an unrelated tenant was halted by another tenant's suspension: %v %v", allowed, err)
	}

	// The console's tenant view reports the halt through the same gate, so screen and gate agree.
	view, err := h.tenants.Get(ctx, tenantAcme)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !view.AutonomousMergesHalted || view.HaltReason == "" {
		t.Errorf("tenant view does not report the halt: %+v", view)
	}
	if view.Status != string(account.StatusSuspended) {
		t.Errorf("tenant view status = %q, want suspended", view.Status)
	}

	// ── Reactivation restores ──
	if _, err := h.tenants.Reactivate(ctx, tenantAcme, "incident INC-204 resolved", adminops.Confirm()); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}
	if allowed, _, err := h.admission.AllowMerge(tenantAcme); !allowed || err != nil {
		t.Fatalf("reactivation did not restore autonomous merges: %v %v", allowed, err)
	}
	if n := len(h.entriesFor(adminaudit.ActionTenantReactivate)); n != 2 {
		t.Errorf("reactivate wrote %d audit entries, want 2", n)
	}
	h.assertChainIntact()
}

// TestAnUnreadableTenantFailsClosed: admission never answers "allowed" when it could not read the
// state — failing open would let a store outage resume the fleet.
func TestAnUnreadableTenantFailsClosed(t *testing.T) {
	h := newHarness(t)
	allowed, _, err := h.admission.AllowMerge("tenant-that-does-not-exist")
	if allowed {
		t.Fatal("admission allowed a merge for a tenant it could not read")
	}
	if err == nil {
		t.Fatal("admission reported an unreadable tenant as a plain denial rather than indeterminate")
	}
}

// TestSetQuotaChangesTheEnforcedAllowance: SetQuota is real — the override is what the entitlement
// gate enforces afterwards, not a value stored and never read.
func TestSetQuotaChangesTheEnforcedAllowance(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	if _, err := h.tenants.SetQuota(ctx, tenantCastle, plancfg.LimitSeats, 12,
		"ticket ACC-51: temporary seat increase during migration", adminops.Confirm()); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	acct, err := h.accounts.Get(tenantCastle)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v, ok := acct.QuotaOverride(string(plancfg.LimitSeats)); !ok || v != 12 {
		t.Fatalf("quota override = %v, %v; want 12, true", v, ok)
	}
	// Clearing returns the tenant to the plan's published allowance.
	if _, err := h.tenants.SetQuota(ctx, tenantCastle, plancfg.LimitSeats, math.NaN(),
		"migration complete", adminops.Confirm()); err != nil {
		t.Fatalf("SetQuota clear: %v", err)
	}
	acct, _ = h.accounts.Get(tenantCastle)
	if _, ok := acct.QuotaOverride(string(plancfg.LimitSeats)); ok {
		t.Error("clearing the override left it set")
	}
	if n := len(h.entriesFor(adminaudit.ActionTenantSetQuota)); n != 4 {
		t.Errorf("two quota commands wrote %d audit entries, want 4", n)
	}
}

// TestUnknownQuotaLimitIsRefused: a typo becomes an error rather than an override nothing reads.
func TestUnknownQuotaLimitIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)
	if _, err := h.tenants.SetQuota(ctx, tenantCastle, plancfg.Limit("seets"), 12, "typo", adminops.Confirm()); err == nil {
		t.Fatal("an unmetered limit name was accepted as a quota override")
	}
}

// TestSupportCannotRunTenantLifecycle: the least-privilege matrix reaching the command path, with the
// denial audited and no state change.
func TestSupportCannotRunTenantLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	if _, err := h.tenants.Suspend(ctx, tenantAcme, "because", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support suspending a tenant: err = %v, want ErrDenied", err)
	}
	if acct, _ := h.accounts.Get(tenantAcme); acct.Status.Suspended() {
		t.Fatal("a denied suspension took effect")
	}
	if n := len(h.entriesFor(adminaudit.ActionAuthorizationDenied)); n == 0 {
		t.Error("the denial was not audited")
	}
	// Support CAN read tenants — the denial is scoped to the capability, not to the surface.
	if _, err := h.tenants.List(ctx); err != nil {
		t.Fatalf("Support listing tenants: %v", err)
	}
}

// TestCommandWithoutAnAdminSessionIsRefused: the identity step comes first, so an unauthenticated
// request never reaches the gate.
func TestCommandWithoutAnAdminSessionIsRefused(t *testing.T) {
	h := newHarness(t)
	if _, err := h.tenants.Suspend(context.Background(), tenantAcme, "r", adminops.Confirm()); err == nil {
		t.Fatal("a command ran with no admin session")
	}
	if _, err := h.tenants.List(context.Background()); err == nil {
		t.Fatal("a read ran with no admin session")
	}
}

// TestSearchFindsTenantsByIDAndPlanName: the "search/view tenants" half of FR7.
func TestSearchFindsTenantsByIDAndPlanName(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	byID, err := h.tenants.Search(ctx, "ACME")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(byID) != 1 || byID[0].TenantID != tenantAcme {
		t.Fatalf("Search by id returned %+v", byID)
	}
	byPlan, err := h.tenants.Search(ctx, "enterprise")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(byPlan) != 1 || byPlan[0].PlanName != "Enterprise" {
		t.Fatalf("Search by plan name returned %+v", byPlan)
	}
	// The view carries plan NAMES, never prices.
	for _, v := range byPlan {
		if v.PlanName == "" {
			t.Error("a tenant view has no plan name")
		}
	}
}

// TestUnauditableCommandDoesNotTakeEffect is FR16 at the command path: with the audit store down, the
// write-ahead append fails and the effect never runs.
func TestUnauditableCommandDoesNotTakeEffect(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	h.audit.SetUnavailable(true)

	_, err := h.tenants.Suspend(ctx, tenantAcme, "incident INC-9", adminops.Confirm())
	if !errors.Is(err, adminaudit.ErrStoreUnavailable) {
		t.Fatalf("Suspend with the audit store down: err = %v, want ErrStoreUnavailable", err)
	}
	h.audit.SetUnavailable(false)
	if acct, _ := h.accounts.Get(tenantAcme); acct.Status.Suspended() {
		t.Fatal("an unauditable suspension took effect")
	}
	if allowed, _, _ := h.admission.AllowMerge(tenantAcme); !allowed {
		t.Fatal("an unauditable suspension halted the tenant's merges anyway")
	}
}
