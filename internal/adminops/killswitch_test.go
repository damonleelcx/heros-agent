package adminops_test

import (
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// killswitch_test.go covers task 7.3 — the LOAD-BEARING kill-switch test (FR12):
//
//	arming GLOBAL halts EVERY tenant's autonomous merges, immediately, with no deploy
//	arming PER-TENANT halts ONLY that tenant; others continue
//	indeterminate state FAILS CLOSED to halt
//
// Every assertion is made through the same reader the P6 loop uses (Admission → KillStateReader), so
// a change that halted the console's view without halting the loop would fail here.

// TestGlobalKillSwitchHaltsEveryTenantImmediately is FR12's first scenario.
func TestGlobalKillSwitchHaltsEveryTenantImmediately(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	tenants := []string{tenantAcme, tenantBoreal, tenantCastle}

	for _, id := range tenants {
		if allowed, _, err := h.admission.AllowMerge(id); !allowed || err != nil {
			t.Fatalf("precondition: %s should be merging (%v, %v)", id, allowed, err)
		}
	}

	if _, err := h.kill.Arm(ctx, adminops.ScopeGlobal,
		"provider incident: every tenant's loop is producing regressions", adminops.Confirm()); err != nil {
		t.Fatalf("Arm(global): %v", err)
	}

	// Immediately: no restart, no deploy, no cache invalidation step — the very next question is
	// answered from the state the arm just wrote.
	for _, id := range tenants {
		allowed, why, err := h.admission.AllowMerge(id)
		if err != nil {
			t.Fatalf("AllowMerge(%s) errored after a global arm: %v", id, err)
		}
		if allowed {
			t.Fatalf("%s was still permitted to merge after the GLOBAL kill switch was armed", id)
		}
		if why == "" {
			t.Errorf("the halt for %s has no named reason", id)
		}
	}

	// Audited.
	entries := h.entriesFor(adminaudit.ActionKillSwitchArm)
	if len(entries) != 2 {
		t.Fatalf("arming wrote %d audit entries, want 2", len(entries))
	}
	if entries[1].Target != adminops.ScopeGlobal || entries[1].Reason == "" {
		t.Errorf("the arm entry does not record the scope and reason: %+v", entries[1])
	}
	h.assertChainIntact()
}

// TestPerTenantKillSwitchHaltsOnlyThatTenant is FR12's second scenario.
func TestPerTenantKillSwitchHaltsOnlyThatTenant(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.kill.Arm(ctx, adminops.TenantScope(tenantAcme),
		"this tenant's loop is thrashing a provider", adminops.Confirm()); err != nil {
		t.Fatalf("Arm(tenant): %v", err)
	}
	if allowed, _, err := h.admission.AllowMerge(tenantAcme); allowed || err != nil {
		t.Fatalf("the named tenant was not halted: allowed=%v err=%v", allowed, err)
	}
	for _, other := range []string{tenantBoreal, tenantCastle} {
		allowed, why, err := h.admission.AllowMerge(other)
		if err != nil {
			t.Fatalf("AllowMerge(%s): %v", other, err)
		}
		if !allowed {
			t.Fatalf("halting one tenant halted %s too: %s", other, why)
		}
	}

	// Disarming a per-tenant scope takes one operator — the friction is on the GLOBAL direction.
	if _, err := h.kill.Disarm(ctx, adminops.TenantScope(tenantAcme), "provider recovered", adminops.Confirm(), ""); err != nil {
		t.Fatalf("Disarm(tenant): %v", err)
	}
	if allowed, _, err := h.admission.AllowMerge(tenantAcme); !allowed || err != nil {
		t.Fatalf("disarming did not restore the tenant: allowed=%v err=%v", allowed, err)
	}
}

// TestIndeterminateKillStateFailsClosedToHalt is FR12's third scenario, and the whole reason the
// store interface returns an error rather than a bool.
func TestIndeterminateKillStateFailsClosedToHalt(t *testing.T) {
	h := newHarness(t)
	h.killStore.SetUnreachable(true)

	for _, id := range []string{tenantAcme, tenantBoreal, tenantCastle} {
		allowed, why, err := h.admission.AllowMerge(id)
		if allowed {
			t.Fatalf("%s was permitted to merge while the kill-switch state was unreadable", id)
		}
		if err == nil {
			t.Fatalf("an unreadable kill-switch state was reported as a plain denial for %s", id)
		}
		if why == "" {
			t.Errorf("the indeterminate halt for %s has no named reason", id)
		}
	}

	// And the console says so rather than showing the tenant as running.
	view, err := h.tenants.Get(h.ctx(adminrbac.RolePlatformSRE), tenantAcme)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !view.AutonomousMergesHalted {
		t.Fatal("the console showed a tenant as merging while the kill-switch state was unreadable")
	}
	if view.HaltReason == "" {
		t.Error("the console does not say WHY the tenant is halted")
	}

	h.killStore.SetUnreachable(false)
	if allowed, _, err := h.admission.AllowMerge(tenantAcme); !allowed || err != nil {
		t.Fatalf("merges did not resume once the state was readable again: %v %v", allowed, err)
	}
}

// TestGlobalDisarmRequiresASecondApprover: arming is one operator, resuming the fleet is two
// (design Open Q2, implemented as a settable policy default).
func TestGlobalDisarmRequiresASecondApprover(t *testing.T) {
	h := newHarness(t)
	sre := h.ctx(adminrbac.RolePlatformSRE)

	if !h.kill.Policy().GlobalDisarmRequiresTwoPerson {
		t.Fatal("the default policy does not require two-person global disarm")
	}
	if _, err := h.kill.Arm(sre, adminops.ScopeGlobal, "fleet-wide incident", adminops.Confirm()); err != nil {
		t.Fatalf("Arm: %v", err)
	}

	// No approver.
	if _, err := h.kill.Disarm(sre, adminops.ScopeGlobal, "incident resolved", adminops.Confirm(), ""); !errors.Is(err, adminops.ErrSecondApproverRequired) {
		t.Fatalf("global disarm with no approver: err = %v, want ErrSecondApproverRequired", err)
	}
	// Self-approval is not a second person.
	self := h.adminIDs[adminrbac.RolePlatformSRE]
	if _, err := h.kill.Disarm(sre, adminops.ScopeGlobal, "incident resolved", adminops.Confirm(), self); !errors.Is(err, adminops.ErrSecondApproverRequired) {
		t.Fatalf("self-approved global disarm: err = %v, want ErrSecondApproverRequired", err)
	}
	// An approver who does not hold the capability is not an approver.
	if _, err := h.kill.Disarm(sre, adminops.ScopeGlobal, "incident resolved", adminops.Confirm(),
		h.adminIDs[adminrbac.RoleSupport]); !errors.Is(err, adminops.ErrSecondApproverRequired) {
		t.Fatalf("disarm approved by a Support admin: err = %v, want ErrSecondApproverRequired", err)
	}
	// Still halted after every refusal.
	if allowed, _, _ := h.admission.AllowMerge(tenantAcme); allowed {
		t.Fatal("a refused global disarm resumed the fleet")
	}

	// A genuine second approver who holds the capability.
	if _, err := h.kill.Disarm(sre, adminops.ScopeGlobal, "incident resolved and verified",
		adminops.Confirm(), h.adminIDs[adminrbac.RoleSuperadmin]); err != nil {
		t.Fatalf("two-person global disarm: %v", err)
	}
	if allowed, why, err := h.admission.AllowMerge(tenantAcme); !allowed || err != nil {
		t.Fatalf("the fleet did not resume after a two-person disarm: %v %q %v", allowed, why, err)
	}
	// The approver is on the record.
	entries := h.entriesFor(adminaudit.ActionKillSwitchDisarm)
	if len(entries) == 0 || entries[len(entries)-1].Evidence["second_approver"] != h.adminIDs[adminrbac.RoleSuperadmin] {
		t.Error("the disarm entry does not record who approved it")
	}
}

// TestKillSwitchIsPermissionGatedAndReasonRequired: Support and Billing-Ops cannot touch the brake.
func TestKillSwitchIsPermissionGatedAndReasonRequired(t *testing.T) {
	h := newHarness(t)
	for _, role := range []adminrbac.Role{adminrbac.RoleSupport, adminrbac.RoleBillingOps} {
		ctx := h.ctx(role)
		if _, err := h.kill.Arm(ctx, adminops.ScopeGlobal, "because", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s arming the global kill switch: err = %v, want ErrDenied", role, err)
		}
		if _, err := h.kill.States(ctx); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s reading kill-switch state: err = %v, want ErrDenied", role, err)
		}
	}
	sre := h.ctx(adminrbac.RolePlatformSRE)
	if _, err := h.kill.Arm(sre, adminops.ScopeGlobal, "", adminops.Confirm()); !errors.Is(err, adminops.ErrNoReason) {
		t.Errorf("arming with no reason: err = %v, want ErrNoReason", err)
	}
	if _, err := h.kill.Arm(sre, adminops.ScopeGlobal, "incident", adminops.Confirmation{}); !errors.Is(err, adminops.ErrNotConfirmed) {
		t.Errorf("unconfirmed arm: err = %v, want ErrNotConfirmed", err)
	}
	if allowed, _, _ := h.admission.AllowMerge(tenantAcme); !allowed {
		t.Fatal("a refused arm halted the fleet anyway")
	}
}

// TestInvalidScopeIsRefused: a typo halts nothing silently.
func TestInvalidScopeIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)
	for _, scope := range []string{"", "globl", "tenant", "customer:acme"} {
		if _, err := h.kill.Arm(ctx, scope, "incident", adminops.Confirm()); err == nil {
			t.Errorf("scope %q was accepted as a kill-switch scope", scope)
		}
	}
}

// TestSuspensionAndKillSwitchAreBothConsulted: the two halt reasons are independent, and either one
// stops the merge. A tenant that is suspended AND globally halted stays halted after one is lifted.
func TestSuspensionAndKillSwitchAreBothConsulted(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	if _, err := h.tenants.Suspend(ctx, tenantAcme, "non-payment", adminops.Confirm()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if _, err := h.kill.Arm(ctx, adminops.ScopeGlobal, "fleet incident", adminops.Confirm()); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	if allowed, _, _ := h.admission.AllowMerge(tenantAcme); allowed {
		t.Fatal("a suspended, globally-halted tenant was permitted to merge")
	}
	// Lift the global halt: the tenant is still suspended.
	if _, err := h.kill.Disarm(ctx, adminops.ScopeGlobal, "incident resolved", adminops.Confirm(),
		h.adminIDs[adminrbac.RoleSuperadmin]); err != nil {
		t.Fatalf("Disarm: %v", err)
	}
	allowed, why, err := h.admission.AllowMerge(tenantAcme)
	if err != nil {
		t.Fatalf("AllowMerge: %v", err)
	}
	if allowed {
		t.Fatal("lifting the global halt resumed a SUSPENDED tenant")
	}
	if why == "" {
		t.Error("the remaining halt has no named reason")
	}
}

// TestArmedAtStartupPolicy: a deployment can boot halted, so a pod restart mid-incident does not
// quietly resume the fleet.
func TestArmedAtStartupPolicy(t *testing.T) {
	h := newHarness(t)
	store := adminops.NewMemKillSwitchStore()
	svc, err := adminops.NewKillSwitchService(h.exec, store, adminops.KillSwitchPolicy{
		GlobalDisarmRequiresTwoPerson: true,
		ArmedAtStartup:                []string{adminops.ScopeGlobal},
	})
	if err != nil {
		t.Fatalf("NewKillSwitchService: %v", err)
	}
	halted, why, err := svc.HaltsMerge(tenantAcme)
	if err != nil {
		t.Fatalf("HaltsMerge: %v", err)
	}
	if !halted {
		t.Fatal("a deployment configured to boot halted came up merging")
	}
	if why == "" {
		t.Error("the startup halt has no named reason")
	}
}
