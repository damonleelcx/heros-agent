package adminops_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/optimizer"
)

// mergeaudit_test.go covers task 9.3's second and third assertions (FR16):
//
//	EVERY autonomous merge appears in the audit chain with its diagnosis, verified delta and commit
//	an action while the audit store is unavailable does NOT take effect
//
// The merges here are produced by the wrapped P6 change ledger on the same code path the loop uses,
// so a merge that reached the repository by some route the wrapper does not see would be missing here.

// applyEvent is one P6 apply event, shaped as the loop writes it.
func applyEvent(runID, diagnosis, from, to, pr, summary string) optimizer.LedgerEvent {
	return optimizer.LedgerEvent{
		RunID: runID, Type: optimizer.EventApply, Actor: "optimizer",
		DiagnosisID: diagnosis, FromConfigHash: from, ToConfigHash: to, PRRef: pr, Summary: summary,
	}
}

// TestEveryAutonomousMergeAppearsInTheAuditChain is FR16's first scenario.
func TestEveryAutonomousMergeAppearsInTheAuditChain(t *testing.T) {
	h := newHarness(t)
	inner := optimizer.NewMemLedger()
	ledger, err := adminops.NewAuditingLedger(inner, h.audit, func(runID string) string {
		// A single-tenant run mapping, exactly as a deployment supplies: run → the tenant that owns it.
		return map[string]string{"run-1": tenantAcme, "run-2": tenantBoreal}[runID]
	})
	if err != nil {
		t.Fatalf("NewAuditingLedger: %v", err)
	}
	// It satisfies the loop's own ledger contract, so this is the ledger the loop would run with.
	var _ optimizer.ChangeLedger = ledger

	const summary = "merge model_upgrade at node3: held-out +0.420 [0.370,0.470], cost -0.0031, latency -85"
	seq, err := ledger.Append(applyEvent("run-1", "diag-17", "cfg-old", "cfg-new", "optimizer/abc123", summary))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := ledger.Backfill("run-1", seq, "9f3c1a2deadbeef"); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	entries := h.entriesFor(adminaudit.ActionAutonomousMerge)
	if len(entries) != 2 {
		t.Fatalf("one merge wrote %d audit entries, want 2 (write-ahead + completion)", len(entries))
	}
	complete := entries[1]
	if complete.Result != adminops.ResultApplied {
		t.Errorf("the completion entry result = %q", complete.Result)
	}
	if complete.ActorAdminID != adminaudit.ActorSystem {
		t.Errorf("the merge is attributed to %q, want the autonomous loop", complete.ActorAdminID)
	}
	if complete.Target != adminops.TenantTarget(tenantAcme) {
		t.Errorf("the merge is not reconstructable to its tenant: target = %q", complete.Target)
	}
	// Diagnosis, verified delta and merge commit — all three, in one entry.
	for key, want := range map[string]string{
		"diagnosis_id": "diag-17",
		"merge_commit": "9f3c1a2deadbeef",
		"pr_ref":       "optimizer/abc123",
	} {
		if complete.Evidence[key] != want {
			t.Errorf("merge entry evidence[%q] = %q, want %q", key, complete.Evidence[key], want)
		}
	}
	if !strings.Contains(complete.Evidence["verified_delta"], "held-out +0.420") {
		t.Errorf("the merge entry does not carry the verified delta: %q", complete.Evidence["verified_delta"])
	}
	if complete.CreatedAt.IsZero() {
		t.Error("the merge entry has no timestamp")
	}

	// A second tenant's merge lands under ITS tenant, so the chain is per-tenant reconstructable.
	seq2, err := ledger.Append(applyEvent("run-2", "diag-4", "c1", "c2", "optimizer/def456", "held-out +0.110"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := ledger.Backfill("run-2", seq2, "aa11bb22"); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	all := h.entriesFor(adminaudit.ActionAutonomousMerge)
	if len(all) != 4 {
		t.Fatalf("two merges wrote %d audit entries, want 4", len(all))
	}
	if all[3].Target != adminops.TenantTarget(tenantBoreal) {
		t.Errorf("the second tenant's merge is attributed to %q", all[3].Target)
	}
	h.assertChainIntact()
}

// TestNonMergeLedgerEventsAreNotMirrored: the audit chain records merges, not the loop's whole
// deliberation. The change ledger stays the place to read "why did it consider that candidate".
func TestNonMergeLedgerEventsAreNotMirrored(t *testing.T) {
	h := newHarness(t)
	ledger, err := adminops.NewAuditingLedger(optimizer.NewMemLedger(), h.audit, nil)
	if err != nil {
		t.Fatalf("NewAuditingLedger: %v", err)
	}
	for _, typ := range []optimizer.EventType{
		optimizer.EventGrant, optimizer.EventConsider, optimizer.EventVerify, optimizer.EventHalt, optimizer.EventStop,
	} {
		if _, err := ledger.Append(optimizer.LedgerEvent{RunID: "run-1", Type: typ, Actor: "optimizer"}); err != nil {
			t.Fatalf("Append %s: %v", typ, err)
		}
	}
	if n := len(h.entriesFor(adminaudit.ActionAutonomousMerge)); n != 0 {
		t.Errorf("non-merge ledger events produced %d merge audit entries, want 0", n)
	}
	if len(ledger.Events("run-1")) != 5 {
		t.Error("the wrapper dropped events from the change ledger")
	}
}

// TestAnUnauditableMergeDoesNotHappen is FR16's second scenario, at the most consequential actor: with
// the audit chain down, the write-ahead ledger append fails as ErrLedgerUnavailable, which is exactly
// what the P6 loop already fails closed on.
func TestAnUnauditableMergeDoesNotHappen(t *testing.T) {
	h := newHarness(t)
	inner := optimizer.NewMemLedger()
	ledger, err := adminops.NewAuditingLedger(inner, h.audit, nil)
	if err != nil {
		t.Fatalf("NewAuditingLedger: %v", err)
	}
	h.audit.SetUnavailable(true)

	_, err = ledger.Append(applyEvent("run-1", "diag-1", "a", "b", "pr", "held-out +0.3"))
	if !errors.Is(err, optimizer.ErrLedgerUnavailable) {
		t.Fatalf("Append with the audit chain down: err = %v, want ErrLedgerUnavailable — the loop only "+
			"fails closed on the error it knows", err)
	}
	h.audit.SetUnavailable(false)
	// And nothing reached the P6 ledger either, so the merge has no write-ahead record claiming it
	// happened.
	if n := len(inner.Events("run-1")); n != 0 {
		t.Errorf("the P6 ledger recorded %d events for a merge that could not be audited, want 0", n)
	}
	if n := len(h.entriesFor(adminaudit.ActionAutonomousMerge)); n != 0 {
		t.Errorf("the audit chain holds %d merge entries after the failure, want 0", n)
	}
}

// TestAuditingLedgerRequiresBothStores: a wrapper built without one of them would silently audit
// nothing.
func TestAuditingLedgerRequiresBothStores(t *testing.T) {
	h := newHarness(t)
	if _, err := adminops.NewAuditingLedger(nil, h.audit, nil); err == nil {
		t.Error("an auditing ledger was built with no P6 ledger")
	}
	if _, err := adminops.NewAuditingLedger(optimizer.NewMemLedger(), nil, nil); err == nil {
		t.Error("an auditing ledger was built with no audit store")
	}
}

// TestAuditViewerIsPermissionGated: the chain records actions across every tenant, so reading it is a
// cross-tenant read — Support is denied with an escalation path.
func TestAuditViewerIsPermissionGated(t *testing.T) {
	h := newHarness(t)
	if _, err := h.auditView.Entries(h.ctx(adminrbac.RoleSupport), adminaudit.Filter{}, 50); !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support reading the audit log: err = %v, want ErrDenied", err)
	}
	view, err := h.auditView.Entries(h.ctx(adminrbac.RolePlatformSRE), adminaudit.Filter{}, 50)
	if err != nil {
		t.Fatalf("Platform-SRE reading the audit log: %v", err)
	}
	if !view.Verification.Intact {
		t.Errorf("the audit viewer reports the chain broken: %+v", view.Verification)
	}
}
