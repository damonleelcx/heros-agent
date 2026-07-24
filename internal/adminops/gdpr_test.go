package adminops_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// gdpr_test.go covers task 11.3 — the LOAD-BEARING GDPR test (FR17):
//
//	a deletion request is actionable ⇒ content removed/tombstoned
//	completion is VERIFIABLE, and the action is audited
//	the append-only chain stays intact via the non-PII tombstone reference

const subjectRef = "subject:person-7741"

// seedSubject puts real content in the store, so "removed" means content genuinely disappeared rather
// than a counter changing.
func (h *harness) seedSubject(t *testing.T) []string {
	t.Helper()
	ids := []string{"trace-1", "memory-4", "evalcase-9"}
	for i, id := range ids {
		h.subjects.Put(subjectRef, id, "content belonging to the data subject "+string(rune('a'+i)))
	}
	return ids
}

// TestGDPRDeletionIsActionableVerifiableAndAudited is FR17's first scenario.
func TestGDPRDeletionIsActionableVerifiableAndAudited(t *testing.T) {
	h := newHarness(t)
	ids := h.seedSubject(t)
	ctx := h.ctx(adminrbac.RoleSuperadmin)
	target := adminops.SubjectTarget(subjectRef)

	req, receipt, err := h.gdpr.Execute(ctx, subjectRef,
		"data-subject erasure request DSR-88, identity verified", adminops.ConfirmTyping(target))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if req.Status != adminops.GDPRCompleted || req.CompletedAt == nil {
		t.Fatalf("the request did not complete: %+v", req)
	}
	if req.RemovedCount != len(ids) {
		t.Errorf("removed %d records, want %d", req.RemovedCount, len(ids))
	}
	if receipt.Result != adminops.ResultApplied {
		t.Errorf("receipt result = %q", receipt.Result)
	}

	// ── The content is genuinely gone ──
	for _, id := range ids {
		body, ok := h.subjects.Body(subjectRef, id)
		if !ok {
			t.Errorf("record %s vanished entirely — a tombstone keeps the row so foreign keys still resolve", id)
		}
		if body != "" {
			t.Errorf("record %s still holds content after the erasure: %q", id, body)
		}
	}
	remaining, err := h.subjects.Remaining(context.Background(), subjectRef)
	if err != nil || remaining != 0 {
		t.Errorf("content remaining = %d, %v; want 0", remaining, err)
	}

	// ── Completion is VERIFIABLE — recomputed, not asserted ──
	report, err := h.gdpr.Verify(ctx, req.RequestID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.Verified() {
		t.Fatalf("the completion record does not verify: %+v", report)
	}
	if !report.VerificationRefMatches {
		t.Error("the stored verification reference does not recompute")
	}

	// ── Audited ──
	entries := h.entriesFor(adminaudit.ActionGDPRExecute)
	if len(entries) != 2 {
		t.Fatalf("the erasure wrote %d audit entries, want 2", len(entries))
	}
	outcome := entries[1]
	if outcome.ActorAdminID != h.adminIDs[adminrbac.RoleSuperadmin] || outcome.Reason == "" || outcome.CreatedAt.IsZero() {
		t.Errorf("the erasure entry does not record actor/reason/timestamp: %+v", outcome)
	}
	h.assertChainIntact()
}

// TestTheAuditChainStaysIntactAfterADeletion is FR17's second scenario.
func TestTheAuditChainStaysIntactAfterADeletion(t *testing.T) {
	h := newHarness(t)
	h.seedSubject(t)
	ctx := h.ctx(adminrbac.RoleSuperadmin)

	// Some ordinary history first, so "no entry is removed" is a claim about a chain with content.
	sre := h.ctx(adminrbac.RolePlatformSRE)
	if _, err := h.tenants.Suspend(sre, tenantAcme, "unrelated incident", adminops.Confirm()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	before := len(h.audit.Entries())

	req, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure request DSR-90",
		adminops.ConfirmTyping(adminops.SubjectTarget(subjectRef)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	after := h.audit.Entries()
	if len(after) <= before {
		t.Fatalf("the chain shrank or stood still across an erasure: %d → %d", before, len(after))
	}
	// No entry was removed: every pre-existing sequence number is still present, in order.
	for i := 0; i < before; i++ {
		if after[i].Seq != i+1 {
			t.Fatalf("entry %d is missing or reordered after the erasure", i+1)
		}
	}
	if v := h.audit.Verify(); !v.Intact {
		t.Fatalf("the chain does not verify after an erasure: break at %d (%s)", v.BreakAt, v.Detail)
	}

	// ── The tombstone reference is present, and it is NON-PII ──
	tombstones := h.entriesFor(adminaudit.ActionGDPRTombstone)
	if len(tombstones) != 1 {
		t.Fatalf("the erasure wrote %d tombstone entries, want 1", len(tombstones))
	}
	tomb := tombstones[0]
	if tomb.Target != req.TombstoneRef {
		t.Errorf("the tombstone entry's target = %q, want the tombstone reference", tomb.Target)
	}
	if tomb.Evidence["verification_ref"] != req.VerificationRef {
		t.Error("the tombstone entry does not carry the completion record")
	}
	// Nothing in the chain names the subject.
	for _, e := range h.audit.Entries() {
		if strings.Contains(e.Target, subjectRef) && e.Action == adminaudit.ActionGDPRTombstone {
			t.Errorf("the tombstone entry names the subject: %q", e.Target)
		}
		for k, v := range e.Evidence {
			if strings.Contains(v, "person-7741") {
				t.Errorf("audit entry %d evidence[%q] carries the subject reference", e.Seq, k)
			}
		}
	}
	// And an auditor holding the subject reference can still find the entry, by recomputing the ref.
	if adminops.TombstoneRef(req.RequestID, subjectRef) != req.TombstoneRef {
		t.Error("the tombstone reference is not recomputable from the request and the subject")
	}
}

// TestGDPRRequiresSuperadminAndASecondConfirmation: the two gates on a one-way door.
func TestGDPRRequiresSuperadminAndASecondConfirmation(t *testing.T) {
	h := newHarness(t)
	ids := h.seedSubject(t)
	target := adminops.SubjectTarget(subjectRef)

	// ── Not Superadmin ──
	for _, role := range []adminrbac.Role{adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE} {
		ctx := h.ctx(role)
		if _, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure", adminops.ConfirmTyping(target)); !errors.Is(err, adminops.ErrDenied) {
			t.Errorf("%s executing an erasure: err = %v, want ErrDenied", role, err)
		}
	}
	// ── Superadmin without the second confirmation ──
	ctx := h.ctx(adminrbac.RoleSuperadmin)
	if _, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure request DSR-1", adminops.Confirm()); !errors.Is(err, adminops.ErrSecondConfirmation) {
		t.Fatalf("erasure with one confirmation: err = %v, want ErrSecondConfirmation", err)
	}
	if _, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure request DSR-1", adminops.ConfirmTyping("subject:someone-else")); !errors.Is(err, adminops.ErrSecondConfirmation) {
		t.Fatalf("erasure with a mistyped target: err = %v, want ErrSecondConfirmation", err)
	}
	// ── No reason ──
	if _, _, err := h.gdpr.Execute(ctx, subjectRef, "", adminops.ConfirmTyping(target)); !errors.Is(err, adminops.ErrNoReason) {
		t.Fatalf("erasure with no reason: err = %v, want ErrNoReason", err)
	}

	// Nothing was deleted by any of those.
	for _, id := range ids {
		if body, _ := h.subjects.Body(subjectRef, id); body == "" {
			t.Fatalf("record %s was erased by a refused request", id)
		}
	}
	if n := len(h.entriesFor(adminaudit.ActionGDPRTombstone)); n != 0 {
		t.Errorf("a refused erasure wrote %d tombstone entries, want 0", n)
	}
}

// TestAPartialErasureIsNotReportedAsComplete: reporting a partial erasure as done is the compliance
// failure that matters, so the request stays open and the caller gets an error.
func TestAPartialErasureIsNotReportedAsComplete(t *testing.T) {
	h := newHarness(t)
	h.seedSubject(t)
	stubborn := &stubbornSubjectStore{inner: h.subjects}
	svc, err := adminops.NewGDPRService(h.exec, stubborn)
	if err != nil {
		t.Fatalf("NewGDPRService: %v", err)
	}
	ctx := h.ctx(adminrbac.RoleSuperadmin)
	req, _, err := svc.Execute(ctx, subjectRef, "erasure request DSR-2",
		adminops.ConfirmTyping(adminops.SubjectTarget(subjectRef)))
	if !errors.Is(err, adminops.ErrErasureIncomplete) {
		t.Fatalf("a partial erasure: err = %v, want ErrErasureIncomplete", err)
	}
	if req.Status == adminops.GDPRCompleted {
		t.Error("a partial erasure was marked completed")
	}
	if n := len(h.entriesFor(adminaudit.ActionGDPRTombstone)); n != 0 {
		t.Error("a partial erasure wrote a tombstone claiming completion")
	}
	// The attempt is still on the record, as a FAILED outcome.
	entries := h.entriesFor(adminaudit.ActionGDPRExecute)
	if len(entries) != 2 || entries[1].Result != adminops.ResultFailed {
		t.Errorf("a partial erasure is not recorded as a failed attempt: %+v", entries)
	}
}

// stubbornSubjectStore tombstones nothing — the shape of a store whose erasure silently did not take.
type stubbornSubjectStore struct{ inner *adminops.MemSubjectStore }

func (s *stubbornSubjectStore) Tombstone(context.Context, string) (int, error) { return 0, nil }
func (s *stubbornSubjectStore) Remaining(ctx context.Context, ref string) (int, error) {
	return s.inner.Remaining(ctx, ref)
}
func (s *stubbornSubjectStore) Describe() string { return "stubborn:subject-content" }

// TestErasureIsIdempotent: a re-run after a partial failure completes rather than failing because
// some records are already gone.
func TestErasureIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.seedSubject(t)
	ctx := h.ctx(adminrbac.RoleSuperadmin)
	target := adminops.SubjectTarget(subjectRef)

	first, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure request DSR-3", adminops.ConfirmTyping(target))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	second, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure request DSR-3 re-run", adminops.ConfirmTyping(target))
	if err != nil {
		t.Fatalf("re-running the erasure: %v", err)
	}
	if second.RemovedCount != 0 {
		t.Errorf("the re-run removed %d records, want 0 — everything was already tombstoned", second.RemovedCount)
	}
	if second.Status != adminops.GDPRCompleted {
		t.Error("the re-run did not complete")
	}
	if first.RequestID == second.RequestID {
		t.Error("the re-run reused the completed request rather than filing a new one")
	}
	if v := h.audit.Verify(); !v.Intact {
		t.Errorf("the chain does not verify after two erasures: %+v", v)
	}
}

// TestVerifyOfAnUnknownRequestIsAnError: a verification that quietly returns "fine" for a request
// nobody filed is worse than no verification.
func TestVerifyOfAnUnknownRequestIsAnError(t *testing.T) {
	h := newHarness(t)
	if _, err := h.gdpr.Verify(h.ctx(adminrbac.RoleSuperadmin), "dsr-9999"); !errors.Is(err, adminops.ErrNoSuchGDPRRequest) {
		t.Fatalf("Verify of an unknown request: err = %v, want ErrNoSuchGDPRRequest", err)
	}
}

// TestVerificationCatchesATamperedChain: the report is a real check, so it fails when it should.
func TestVerificationCatchesATamperedChain(t *testing.T) {
	h := newHarness(t)
	h.seedSubject(t)
	ctx := h.ctx(adminrbac.RoleSuperadmin)
	req, _, err := h.gdpr.Execute(ctx, subjectRef, "erasure request DSR-4",
		adminops.ConfirmTyping(adminops.SubjectTarget(subjectRef)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := h.audit.SimulateOutOfBandTamper(1, func(e *adminaudit.Entry) { e.Reason = "rewritten" }); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	report, err := h.gdpr.Verify(ctx, req.RequestID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.ChainIntact {
		t.Fatal("the verification reported an intact chain after it was tampered with")
	}
	if report.Verified() {
		t.Fatal("an erasure verified against a broken chain")
	}
}
