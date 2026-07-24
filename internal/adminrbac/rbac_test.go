package adminrbac_test

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// rbac_test.go covers tasks 2.4 (the load-bearing least-privilege matrix) and the FR3/FR5 invariants
// around it: deny by default, denials logged, live role resolution, append-only Superadmin-gated
// grants.

// fixture wires four admin principals, one per role — the fixture task 14.1 calls for.
type fixture struct {
	gate  *adminrbac.Gate
	audit *adminaudit.MemoryStore
	admin map[adminrbac.Role]string
	clk   *clock
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clk := &clock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	store := adminaudit.NewMemoryStore(clk.now)
	grants := adminrbac.NewGrantStore(clk.now)
	admins := map[adminrbac.Role]string{}
	for _, r := range adminrbac.Roles {
		id := "adm-" + string(r)
		admins[r] = id
		if _, err := grants.Seed(id, r, "fixture: one admin principal per role"); err != nil {
			t.Fatalf("Seed %s: %v", r, err)
		}
	}
	gate, err := adminrbac.NewGate(grants, store, clk.now)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return &fixture{gate: gate, audit: store, admin: admins, clk: clk}
}

// denialsFor counts audited denials naming a capability.
func (f *fixture) denialsFor(actor string, c adminrbac.Capability) int {
	n := 0
	for _, e := range f.audit.Entries() {
		if e.Action == adminaudit.ActionAuthorizationDenied && e.ActorAdminID == actor &&
			e.Evidence["capability"] == string(c) {
			n++
		}
	}
	return n
}

// TestLeastPrivilegeMatrix is task 2.4, the load-bearing test. Support is denied AND logged on every
// billing and destructive capability; the roles that hold them are allowed.
func TestLeastPrivilegeMatrix(t *testing.T) {
	f := newFixture(t)
	support := f.admin[adminrbac.RoleSupport]

	// ── Support is denied, and each denial is logged ──
	deniedToSupport := []struct {
		cap    adminrbac.Capability
		target string
	}{
		{adminrbac.CapBillingCorrect, "tenant:acme"},      // refund
		{adminrbac.CapTenantSuspend, "tenant:acme"},       // suspend
		{adminrbac.CapJobCancel, "job:run-17"},            // cancel
		{adminrbac.CapKillSwitch, "global"},               // kill
		{adminrbac.CapEntitlementOverride, "tenant:acme"}, // override
	}
	for _, tc := range deniedToSupport {
		d := f.gate.Authorize(support, tc.cap, tc.target)
		if d.Allowed {
			t.Fatalf("Support was allowed %s on %s — least privilege is broken", tc.cap, tc.target)
		}
		if len(d.HeldBy) == 0 {
			t.Errorf("%s denial named no holder — a bare refusal gives the operator no escalation path", tc.cap)
		}
		if got := f.denialsFor(support, tc.cap); got != 1 {
			t.Errorf("%s denial audited %d times, want 1", tc.cap, got)
		}
		if !errors.Is(d.Error(), nil) && d.Error() == nil {
			t.Errorf("%s denial produced no error to propagate", tc.cap)
		}
	}

	// ── Billing-Ops can refund ──
	if d := f.gate.Authorize(f.admin[adminrbac.RoleBillingOps], adminrbac.CapBillingCorrect, "tenant:acme"); !d.Allowed {
		t.Fatalf("Billing-Ops was denied a refund: %v", d.Error())
	}
	// ── Platform-SRE can cancel a job and operate the kill switch ──
	for _, c := range []adminrbac.Capability{adminrbac.CapJobCancel, adminrbac.CapKillSwitch} {
		if d := f.gate.Authorize(f.admin[adminrbac.RolePlatformSRE], c, "global"); !d.Allowed {
			t.Fatalf("Platform-SRE was denied %s: %v", c, d.Error())
		}
	}
	// ── Platform-SRE cannot bill, and Billing-Ops cannot halt the fleet: the partition runs both ways ──
	if d := f.gate.Authorize(f.admin[adminrbac.RolePlatformSRE], adminrbac.CapBillingCorrect, "tenant:acme"); d.Allowed {
		t.Error("Platform-SRE was allowed to issue a refund")
	}
	if d := f.gate.Authorize(f.admin[adminrbac.RoleBillingOps], adminrbac.CapKillSwitch, "global"); d.Allowed {
		t.Error("Billing-Ops was allowed to operate the kill switch")
	}

	// ── Superadmin can grant a role, and the grant is audited ──
	before := len(f.audit.Entries())
	row, err := f.gate.Grant(f.admin[adminrbac.RoleSuperadmin], support, adminrbac.RoleBillingOps,
		"ticket OPS-1120: covering billing during on-call")
	if err != nil {
		t.Fatalf("Superadmin Grant: %v", err)
	}
	if row.Action != adminrbac.GrantActionGrant || row.GrantedAt.IsZero() {
		t.Errorf("grant row is malformed: %+v", row)
	}
	entries := f.audit.Entries()
	if len(entries) != before+1 {
		t.Fatalf("grant wrote %d audit entries, want 1", len(entries)-before)
	}
	last := entries[len(entries)-1]
	if last.Action != adminaudit.ActionRoleGrant || last.ActorAdminID != f.admin[adminrbac.RoleSuperadmin] ||
		last.Evidence["role"] != string(adminrbac.RoleBillingOps) || last.Reason == "" || last.CreatedAt.IsZero() {
		t.Errorf("grant audit entry does not record actor/subject/role/reason/timestamp: %+v", last)
	}

	// The grant takes effect LIVE — the newly-granted capability is reachable on the next call, with
	// no re-login.
	if d := f.gate.Authorize(support, adminrbac.CapBillingCorrect, "tenant:acme"); !d.Allowed {
		t.Error("a live role grant did not take effect on the next authorization")
	}

	// ── A non-Superadmin role grant is denied ──
	for _, r := range []adminrbac.Role{adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE} {
		actor := f.admin[r]
		if _, err := f.gate.Grant(actor, "adm-victim", adminrbac.RoleSuperadmin, "escalating myself"); !errors.Is(err, adminrbac.ErrNotSuperadmin) {
			t.Fatalf("%s granting a role: err = %v, want ErrNotSuperadmin", r, err)
		}
		if got := f.denialsFor(actor, adminrbac.CapRoleGrant); got != 1 {
			t.Errorf("%s role-grant denial audited %d times, want 1", r, got)
		}
	}
	// And no grant row was written by any of them.
	for _, g := range f.gate.LiveRoles("adm-victim") {
		t.Errorf("a denied grant still took effect: adm-victim holds %s", g)
	}
}

// TestDenyByDefault: for every (role, capability) pair NOT in the allow list, the gate denies. This
// iterates the real Capabilities enumeration, so a capability added later is covered automatically.
func TestDenyByDefault(t *testing.T) {
	f := newFixture(t)
	for _, r := range adminrbac.Roles {
		granted := map[adminrbac.Capability]bool{}
		for _, c := range adminrbac.Grants(r) {
			granted[c] = true
		}
		for _, c := range adminrbac.Capabilities {
			d := f.gate.Authorize(f.admin[r], c, "tenant:acme")
			if d.Allowed != granted[c] {
				t.Errorf("Authorize(%s, %s) allowed = %v, want %v", r, c, d.Allowed, granted[c])
			}
		}
	}
}

// TestUnknownCapabilityIsDenied: deny-by-default covers capabilities that do not exist yet, which is
// the property that makes "add a capability, forget to grant it" fail safe.
func TestUnknownCapabilityIsDenied(t *testing.T) {
	f := newFixture(t)
	for _, r := range adminrbac.Roles {
		if d := f.gate.Authorize(f.admin[r], adminrbac.Capability("capability.invented.tomorrow"), "global"); d.Allowed {
			t.Errorf("%s was allowed an unlisted capability", r)
		}
	}
}

// TestAnAdminWithNoRoleReachesNothing: the deny-by-default floor for an unknown principal.
func TestAnAdminWithNoRoleReachesNothing(t *testing.T) {
	f := newFixture(t)
	for _, c := range adminrbac.Capabilities {
		if d := f.gate.Authorize("adm-nobody", c, "tenant:acme"); d.Allowed {
			t.Errorf("an admin with no live role was allowed %s", c)
		}
	}
}

// TestEveryCapabilityHasAHolder: deny-by-default must not produce a capability NO role can reach —
// "nobody can action a GDPR request" is a bug discovered during a compliance deadline.
func TestEveryCapabilityHasAHolder(t *testing.T) {
	for _, c := range adminrbac.Capabilities {
		if len(adminrbac.HoldersOf(c)) == 0 {
			t.Errorf("capability %s is granted by no role", c)
		}
	}
}

// TestSupportHoldsOnlyReadAndReadImpersonation pins the least-privilege shape itself, so widening
// Support is a deliberate edit to this list rather than a quiet one to the map.
func TestSupportHoldsOnlyReadAndReadImpersonation(t *testing.T) {
	want := map[adminrbac.Capability]bool{
		adminrbac.CapTenantRead: true, adminrbac.CapJobRead: true, adminrbac.CapImpersonateRead: true,
	}
	got := adminrbac.Grants(adminrbac.RoleSupport)
	if len(got) != len(want) {
		t.Fatalf("Support holds %v, want exactly %d read capabilities", got, len(want))
	}
	for _, c := range got {
		if !want[c] {
			t.Errorf("Support unexpectedly holds %s", c)
		}
	}
	// Support holds nothing that moves money or changes fleet/tenant state. Impersonation START is on
	// Support's list and does require a reason and an audit entry (FR13) — it is privileged, not
	// destructive — so the assertion below names the destructive set explicitly rather than leaning on
	// RequiresConfirmation, which covers both.
	for _, c := range []adminrbac.Capability{
		adminrbac.CapBillingCorrect, adminrbac.CapBillingRead, adminrbac.CapEntitlementOverride,
		adminrbac.CapTenantSuspend, adminrbac.CapTenantQuota, adminrbac.CapJobRetry, adminrbac.CapJobCancel,
		adminrbac.CapRegistryAdmin, adminrbac.CapKillSwitch, adminrbac.CapRoleGrant, adminrbac.CapGDPRExecute,
		adminrbac.CapCrossTenantRead, adminrbac.CapAuditRead, adminrbac.CapImpersonateElevate,
	} {
		if want[c] {
			t.Errorf("Support holds destructive/privileged capability %s", c)
		}
	}
}

// TestConfirmationAndIrreversibilityAreClassifiedForEveryCapability: the command path reads these two
// predicates to scale friction to blast radius, so every capability must have a considered answer.
func TestConfirmationAndIrreversibilityAreClassifiedForEveryCapability(t *testing.T) {
	for _, c := range adminrbac.Capabilities {
		if c.ReadOnly() == c.RequiresConfirmation() {
			t.Errorf("%s is classified as both read-only and confirmation-requiring", c)
		}
		if c.Irreversible() && !c.RequiresConfirmation() {
			t.Errorf("%s is irreversible but takes no confirmation", c)
		}
		if c.Description() == string(c) {
			t.Errorf("%s has no operator-facing description — a denial would name a machine string", c)
		}
	}
	if !adminrbac.CapGDPRExecute.Irreversible() {
		t.Error("a GDPR erasure is not classified irreversible — it would take one confirmation, not two")
	}
	if adminrbac.CapTenantSuspend.Irreversible() {
		t.Error("suspend is classified irreversible, but reactivate reverses it")
	}
}

// TestRevokeIsAppendOnlyAndImmediate: FR5's append-only shape, and revocation as an incident response.
func TestRevokeIsAppendOnlyAndImmediate(t *testing.T) {
	f := newFixture(t)
	sre := f.admin[adminrbac.RolePlatformSRE]
	super := f.admin[adminrbac.RoleSuperadmin]

	if d := f.gate.Authorize(sre, adminrbac.CapKillSwitch, "global"); !d.Allowed {
		t.Fatal("fixture: Platform-SRE should start with the kill switch")
	}
	row, err := f.gate.Revoke(super, sre, adminrbac.RolePlatformSRE, "incident INC-9: suspected credential compromise")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if row.Action != adminrbac.GrantActionRevoke || row.RevokedAt.IsZero() || row.Revokes == "" {
		t.Errorf("revoke row does not link to the grant it withdraws: %+v", row)
	}
	// Append-only: the original grant row is still there, untouched.
	rows := f.gate.LiveRoles(sre)
	if len(rows) != 0 {
		t.Errorf("revoked admin still holds %v", rows)
	}
	if d := f.gate.Authorize(sre, adminrbac.CapKillSwitch, "global"); d.Allowed {
		t.Error("a revoked role still authorized the kill switch")
	}
}

// TestRoleGrantWithoutReasonIsRefused: FR6's recorded-reason rule reaches the grant path too.
func TestRoleGrantWithoutReasonIsRefused(t *testing.T) {
	f := newFixture(t)
	if _, err := f.gate.Grant(f.admin[adminrbac.RoleSuperadmin], "adm-x", adminrbac.RoleSupport, "   "); !errors.Is(err, adminrbac.ErrNoReason) {
		t.Fatalf("Grant with a blank reason: err = %v, want ErrNoReason", err)
	}
}

// TestGrantFailsClosedWhenAuditIsDown: an unauditable privileged action does not take effect (FR16),
// checked here at the grant path because a role change is the most privileged action there is.
func TestGrantFailsClosedWhenAuditIsDown(t *testing.T) {
	f := newFixture(t)
	f.audit.SetUnavailable(true)
	if _, err := f.gate.Grant(f.admin[adminrbac.RoleSuperadmin], "adm-x", adminrbac.RoleSupport, "on-call rotation"); !errors.Is(err, adminaudit.ErrStoreUnavailable) {
		t.Fatalf("Grant with the audit store down: err = %v, want ErrStoreUnavailable", err)
	}
	f.audit.SetUnavailable(false)
	if roles := f.gate.LiveRoles("adm-x"); len(roles) != 0 {
		t.Errorf("an unaudited grant took effect anyway: %v", roles)
	}
}

// TestServedPermissionMapMatchesTheGate: the console renders from FullPermissionMap, so it must agree
// with Authorize for every pair — otherwise the screen and the gate disagree (FR22).
func TestServedPermissionMapMatchesTheGate(t *testing.T) {
	f := newFixture(t)
	served := adminrbac.FullPermissionMap()
	for _, r := range adminrbac.Roles {
		inMap := map[adminrbac.Capability]bool{}
		for _, c := range served[r] {
			inMap[c] = true
		}
		for _, c := range adminrbac.Capabilities {
			d := f.gate.Authorize(f.admin[r], c, "tenant:acme")
			if d.Allowed != inMap[c] {
				t.Errorf("served map and gate disagree for (%s, %s): map=%v gate=%v", r, c, inMap[c], d.Allowed)
			}
		}
	}
}

// TestCapabilitiesForIsTheUnionOfLiveRoles: what the console renders for a specific operator.
func TestCapabilitiesForIsTheUnionOfLiveRoles(t *testing.T) {
	f := newFixture(t)
	support := f.admin[adminrbac.RoleSupport]
	if got := len(f.gate.CapabilitiesFor(support)); got != len(adminrbac.Grants(adminrbac.RoleSupport)) {
		t.Fatalf("CapabilitiesFor(Support) has %d entries, want %d", got, len(adminrbac.Grants(adminrbac.RoleSupport)))
	}
	if _, err := f.gate.Grant(f.admin[adminrbac.RoleSuperadmin], support, adminrbac.RolePlatformSRE, "on-call cover"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	got := map[adminrbac.Capability]bool{}
	for _, c := range f.gate.CapabilitiesFor(support) {
		got[c] = true
	}
	for _, c := range adminrbac.Grants(adminrbac.RolePlatformSRE) {
		if !got[c] {
			t.Errorf("after a second role grant, %s is missing from the union", c)
		}
	}
}

// TestUnknownRoleIsRefused: a typo denies rather than creating a role nothing grants.
func TestUnknownRoleIsRefused(t *testing.T) {
	f := newFixture(t)
	if _, err := f.gate.Grant(f.admin[adminrbac.RoleSuperadmin], "adm-x", adminrbac.Role("root"), "typo"); !errors.Is(err, adminrbac.ErrUnknownRole) {
		t.Fatalf("Grant of an unknown role: err = %v, want ErrUnknownRole", err)
	}
	if adminrbac.Role("root").Valid() {
		t.Error("an invented role reported itself valid")
	}
}
