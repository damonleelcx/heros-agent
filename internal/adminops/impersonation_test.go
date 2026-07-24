package adminops_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// impersonation_test.go covers task 8.3 — the LOAD-BEARING impersonation test (FR13):
//
//	no reason                       ⇒ denied, no session
//	a read-scoped session           ⇒ time-bounded, every action audited AS impersonation
//	a write in read scope           ⇒ denied until elevated + second-confirmed
//	the session                     ⇒ expires automatically

// TestImpersonationWithoutAReasonIsDenied is FR13's first scenario.
func TestImpersonationWithoutAReasonIsDenied(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	for _, reason := range []string{"", "  ", "\t"} {
		sess, _, err := h.impersonation.Start(ctx, tenantAcme, reason, 0, adminops.Confirm())
		if !errors.Is(err, adminops.ErrImpersonationNoReason) {
			t.Fatalf("Start with reason %q: err = %v, want ErrImpersonationNoReason", reason, err)
		}
		if sess.ID != "" {
			t.Fatal("a session was started without a reason")
		}
	}
	if n := len(h.entriesFor(adminaudit.ActionImpersonationStart)); n != 0 {
		t.Errorf("a refused impersonation wrote %d audit entries, want 0", n)
	}
}

// TestReadScopedSessionIsTimeBoundedAndAudited is FR13's second scenario.
func TestReadScopedSessionIsTimeBoundedAndAudited(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)
	const reason = "ticket SUP-4102: customer reports an eval run they cannot see"

	sess, receipt, err := h.impersonation.Start(ctx, tenantAcme, reason, 20*time.Minute, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.Scope != adminops.ScopeRead {
		t.Errorf("a new session's scope = %q, want read", sess.Scope)
	}
	if !sess.ExpiresAt.After(sess.StartedAt) {
		t.Error("the session has no time bound")
	}
	if got := sess.ExpiresAt.Sub(sess.StartedAt); got != 20*time.Minute {
		t.Errorf("session lifetime = %s, want the requested 20m", got)
	}
	if receipt.Result != adminops.ResultApplied {
		t.Errorf("receipt result = %q", receipt.Result)
	}

	// Start is audited with actor, tenant and reason.
	entries := h.entriesFor(adminaudit.ActionImpersonationStart)
	if len(entries) != 2 {
		t.Fatalf("start wrote %d audit entries, want 2", len(entries))
	}
	start := entries[1]
	if start.ActorAdminID != h.adminIDs[adminrbac.RoleSupport] || start.Target != adminops.TenantTarget(tenantAcme) ||
		start.Reason != reason {
		t.Errorf("the start entry does not record actor/tenant/reason: %+v", start)
	}

	// ── Every action during the session is logged AS impersonation ──
	impCtx := adminops.WithImpersonation(ctx, sess.ID)
	if err := h.impersonation.RecordRead(impCtx, sess.ID, "eval run board"); err != nil {
		t.Fatalf("RecordRead: %v", err)
	}
	acts := h.entriesFor(adminaudit.ActionImpersonatedAction)
	if len(acts) != 1 {
		t.Fatalf("an impersonated read wrote %d audit entries, want 1", len(acts))
	}
	act := acts[0]
	if act.ImpersonationID != sess.ID {
		t.Errorf("the impersonated action is not tied to its session: %+v", act)
	}
	if act.ActorAdminID != h.adminIDs[adminrbac.RoleSupport] {
		t.Errorf("the impersonated action was logged as actor %q — it must be logged as the ACTING ADMIN, "+
			"never as the tenant", act.ActorAdminID)
	}
	if act.Evidence["tenant_id"] != tenantAcme {
		t.Errorf("the impersonated action does not name the impersonated tenant: %v", act.Evidence)
	}

	// The banner names the tenant, the scope, the expiry and that actions are logged (FR25).
	banner := sess.BannerText(h.clk.now())
	for _, want := range []string{tenantAcme, "read-only", "expires in", "every action is logged"} {
		if !strings.Contains(banner, want) {
			t.Errorf("the impersonation banner is missing %q: %q", want, banner)
		}
	}
	if sess.RemainingSeconds(h.clk.now()) <= 0 {
		t.Error("the banner would count down from zero on a live session")
	}
	h.assertChainIntact()
}

// TestWriteInReadScopeIsDeniedUntilElevated is FR13's third scenario and the load-bearing half.
func TestWriteInReadScopeIsDeniedUntilElevated(t *testing.T) {
	h := newHarness(t)
	// Platform-SRE, because Support holds no elevation capability at all — that half is asserted
	// separately below.
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	sess, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-51: reproducing a stuck job", 0, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	impCtx := adminops.WithImpersonation(ctx, sess.ID)

	// A write under the read-scoped session is refused — and the refusal is about the SESSION, not
	// about the operator's permissions, which they do hold.
	h.seedJobs()
	if _, err := h.jobs.Cancel(impCtx, "run-running", "stopping it for the customer", adminops.Confirm()); !errors.Is(err, adminops.ErrImpersonatedWrite) {
		t.Fatalf("write in read scope: err = %v, want ErrImpersonatedWrite", err)
	}
	if j, _ := h.jobQueue.get("run-running"); j.State != "leased" {
		t.Fatal("a write refused by impersonation scope took effect anyway")
	}
	// Reads still work.
	if _, err := h.jobs.List(impCtx, 10); err != nil {
		t.Fatalf("a read under a read-scoped session was refused: %v", err)
	}

	// ── Elevation needs a SECOND confirmation (type the target) ──
	target := adminops.TenantTarget(tenantAcme)
	if _, _, err := h.impersonation.Elevate(ctx, sess.ID, "customer asked us to cancel the run", adminops.Confirm()); !errors.Is(err, adminops.ErrSecondConfirmation) {
		t.Fatalf("elevation with one confirmation: err = %v, want ErrSecondConfirmation", err)
	}
	if _, _, err := h.impersonation.Elevate(ctx, sess.ID, "customer asked", adminops.ConfirmTyping("tenant:wrong")); !errors.Is(err, adminops.ErrSecondConfirmation) {
		t.Fatalf("elevation with a mistyped target: err = %v, want ErrSecondConfirmation", err)
	}
	// Still read-scoped after both refusals.
	if _, err := h.jobs.Cancel(impCtx, "run-running", "stopping it", adminops.Confirm()); !errors.Is(err, adminops.ErrImpersonatedWrite) {
		t.Fatal("a refused elevation nonetheless permitted a write")
	}

	elevated, _, err := h.impersonation.Elevate(ctx, sess.ID,
		"customer explicitly asked us to cancel run-running on their behalf", adminops.ConfirmTyping(target))
	if err != nil {
		t.Fatalf("Elevate: %v", err)
	}
	if elevated.Scope != adminops.ScopeElevatedWrite {
		t.Fatalf("after elevation the scope is %q", elevated.Scope)
	}
	// The elevation is itself audited.
	if n := len(h.entriesFor(adminaudit.ActionImpersonationElevate)); n != 2 {
		t.Errorf("elevation wrote %d audit entries, want 2", n)
	}

	// Now the write proceeds — and is audited AS impersonation.
	if _, err := h.jobs.Cancel(impCtx, "run-running", "customer asked us to cancel it", adminops.Confirm()); err != nil {
		t.Fatalf("write after elevation: %v", err)
	}
	cancels := h.entriesFor(adminaudit.ActionJobCancel)
	if len(cancels) == 0 {
		t.Fatal("the elevated write was not audited")
	}
	for _, e := range cancels {
		if e.ImpersonationID != sess.ID {
			t.Errorf("an impersonated write was not tagged with its impersonation session: %+v", e)
		}
	}
	h.assertChainIntact()
}

// TestSupportCannotElevate is design Open Q3's proposal in force: Support can SEE but never ACT AS.
func TestSupportCannotElevate(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	sess, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-9: looking at the customer's board", 0, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, _, err = h.impersonation.Elevate(ctx, sess.ID, "the customer asked",
		adminops.ConfirmTyping(adminops.TenantTarget(tenantAcme)))
	if !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support elevating: err = %v, want ErrDenied", err)
	}
	if got, _ := h.impersonation.Session(sess.ID); got.Scope != adminops.ScopeRead {
		t.Fatal("a denied elevation changed the session's scope")
	}
}

// TestSessionExpiresAutomatically is FR13's fourth scenario: the session ends at its bound with no
// operator action and no sweeper.
func TestSessionExpiresAutomatically(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RolePlatformSRE)

	sess, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-77", 15*time.Minute, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := h.impersonation.Session(sess.ID); err != nil {
		t.Fatalf("the session should be live before its bound: %v", err)
	}

	h.clk.advance(15 * time.Minute)

	if _, err := h.impersonation.Session(sess.ID); !errors.Is(err, adminops.ErrImpersonationExpired) {
		t.Fatalf("Session at the bound: err = %v, want ErrImpersonationExpired", err)
	}
	if active, err := h.impersonation.Active(ctx); err != nil || len(active) != 0 {
		t.Errorf("an expired session is still active: %v %v", active, err)
	}
	// An expired session authorizes nothing — not even a read.
	impCtx := adminops.WithImpersonation(ctx, sess.ID)
	if err := h.impersonation.RecordRead(impCtx, sess.ID, "board"); !errors.Is(err, adminops.ErrImpersonationExpired) {
		t.Errorf("a read on an expired session: err = %v, want ErrImpersonationExpired", err)
	}
	h.seedJobs()
	if _, err := h.jobs.Cancel(impCtx, "run-running", "too late", adminops.Confirm()); !errors.Is(err, adminops.ErrImpersonatedWrite) {
		t.Errorf("a write on an expired session: err = %v, want ErrImpersonatedWrite", err)
	}
}

// TestEndingASessionIsAlwaysAvailable: the console's always-visible End control (FR25).
func TestEndingASessionIsAlwaysAvailable(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	sess, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-12", 0, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := h.impersonation.End(ctx, sess.ID, "finished looking"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if active, _ := h.impersonation.Active(ctx); len(active) != 0 {
		t.Error("an ended session is still active")
	}
	if n := len(h.entriesFor(adminaudit.ActionImpersonationEnd)); n != 2 {
		t.Errorf("ending wrote %d audit entries, want 2", n)
	}
}

// TestSessionTTLIsBounded: "time-bounded" a caller can set to a week is not time-bounded.
func TestImpersonationTTLIsBounded(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)
	if _, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-1", 7*24*time.Hour, adminops.Confirm()); err == nil {
		t.Fatal("a week-long impersonation session was accepted")
	}
	// The default is applied when the caller does not say.
	sess, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-1", 0, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := sess.ExpiresAt.Sub(sess.StartedAt); got != adminops.DefaultImpersonationTTL {
		t.Errorf("default session lifetime = %s, want %s", got, adminops.DefaultImpersonationTTL)
	}
}

// TestAnotherAdminCannotElevateYourSession: a session belongs to the admin who opened it.
func TestAnotherAdminCannotElevateYourSession(t *testing.T) {
	h := newHarness(t)
	sess, _, err := h.impersonation.Start(h.ctx(adminrbac.RolePlatformSRE), tenantAcme, "ticket SUP-3", 0, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, _, err = h.impersonation.Elevate(h.ctx(adminrbac.RoleSuperadmin), sess.ID, "helping out",
		adminops.ConfirmTyping(adminops.TenantTarget(tenantAcme)))
	if !errors.Is(err, adminops.ErrNotYourSession) {
		t.Fatalf("elevating another admin's session: err = %v, want ErrNotYourSession", err)
	}
}

// TestImpersonatedReadFailsClosedWhenUnauditable: an unaudited look at tenant data is the exact thing
// impersonation exists to prevent, so it does not happen.
func TestImpersonatedReadFailsClosedWhenUnauditable(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)
	sess, _, err := h.impersonation.Start(ctx, tenantAcme, "ticket SUP-8", 0, adminops.Confirm())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.audit.SetUnavailable(true)
	defer h.audit.SetUnavailable(false)
	if err := h.impersonation.RecordRead(adminops.WithImpersonation(ctx, sess.ID), sess.ID, "board"); !errors.Is(err, adminaudit.ErrStoreUnavailable) {
		t.Fatalf("an impersonated read with the audit store down: err = %v, want ErrStoreUnavailable", err)
	}
}
