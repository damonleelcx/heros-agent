//go:build pgproof

// The live half of P27 task 9.1: ownership does not reach the identity of a configuration or a run.
//
// internal/variantspec proves the same thing two other ways — recorded pre-P27 bytes, and a ban on the
// ownership vocabulary in the hashed type — but neither of those ever holds a tenant. This one does: it
// submits the SAME spec twice, under two organizations, through the whole production path, and reads
// what the platform derived.
package submit

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// dropQueued removes the queue items a test in this file enqueued.
//
// 🔴 Not tidiness — a correctness requirement this file learned by breaking something. Every harness in
// this package shares one `testDB`, so `run_queue` is file-global state, and
// TestPGSubmit_ResubmittingTheSameSpecIsIdempotent ends by asserting that a SECOND Dequeue finds the
// queue empty. That assertion reads as "a re-submit did not fork a run" and actually means "nothing
// anywhere in this package has left a dispatchable item" — true only because every test that enqueues
// happens to appear after it in source order.
//
// This file sorts before submit_pgproof_test.go, so its runs were dispatchable first and that test went
// red, on an ownership change that has nothing to do with queueing. Cleaning up here is the fix that
// belongs to this change; making that assertion independent of file order belongs to whoever owns it.
func dropQueued(t *testing.T, runID string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := testDB.Exec(`DELETE FROM run_queue WHERE run_id = $1`, runID); err != nil {
			t.Errorf("clean up the queue item for %s: %v", runID, err)
		}
	})
}

// TestPGSubmit_ConfigHashAndRunIDAreInvariantUnderTheSubmittingOrganization is 9.1's dynamic assertion.
//
// If ownership leaked into either derivation, this is what it would cost: every organization would file
// the same configuration under its own config_hash, so a board would only ever see its own tenant's
// measurements of a variant, and the cross-run history a config_hash exists to accumulate would fragment
// silently — no error, just less evidence than there should be.
func TestPGSubmit_ConfigHashAndRunIDAreInvariantUnderTheSubmittingOrganization(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	m := h.registerModel(t, "submit-owner", "anthropic", "claude-owner-5")
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}})
	base := Request{Spec: spec, VariantID: "v_owner", Label: "owner", Seed: 11}

	orgA := base
	orgA.TenantID = "org_alpha"
	first, err := h.svc.Submit(ctx, orgA)
	if err != nil {
		t.Fatalf("Submit as org_alpha: %v", err)
	}
	dropQueued(t, first.RunID)

	orgB := base
	orgB.TenantID = "org_beta"
	second, err := h.svc.Submit(ctx, orgB)
	if err != nil {
		t.Fatalf("Submit as org_beta: %v", err)
	}

	if second.ConfigHash != first.ConfigHash {
		t.Errorf("config_hash depends on who submitted: %s for org_alpha, %s for org_beta.\n"+
			"Every result cached under one organization's hash is now invisible to the other, and the two "+
			"are measuring the same configuration.", first.ConfigHash, second.ConfigHash)
	}
	if second.RunID != first.RunID {
		t.Errorf("run_id depends on who submitted: %s vs %s. run_id is a function of "+
			"(config_hash, source_revision, seed) and of nothing else — that is what makes a re-submit "+
			"collapse rather than fork.", first.RunID, second.RunID)
	}

	// And no third row: the derivation being stable is what makes the two submissions ONE experiment.
	if n := count(t, `SELECT count(*) FROM run WHERE config_hash=$1 AND seed=11`, first.ConfigHash); n != 1 {
		t.Errorf("run has %d rows for one configuration submitted by two organizations, want 1", n)
	}
}

// TestPGSubmit_TheSecondOrganizationsSubmitDoesNotAcquireTheRun records a DEFECT this task found. It
// is not section 9's to fix — 9.1 is about the measurement, and the measurement is intact — but it is
// the direct consequence of the invariance above and it must not be lost.
//
// 🔴 run_id is a pure function of (config_hash, source_revision, seed), so two organizations submitting
// the same spec derive the SAME run_id. executor.Store.Start is idempotent on that id: the second insert
// affects no rows, and the read-back that follows proves the existing row carries the same config_hash,
// source_revision and seed — it does NOT look at tenant_id. So Start returns success, and the run stays
// owned by whoever got there first.
//
// The second organization is therefore handed a run_id its own listing will never return and its own
// GET will 404 on. Submit reported success; the customer has nothing. Neither of the two plausible
// repairs belongs here — refusing the second submit tells one customer that another customer is running
// something, and putting the tenant into run_id would break exactly the invariance proved above — which
// is why this is written down rather than patched.
func TestPGSubmit_TheSecondOrganizationsSubmitDoesNotAcquireTheRun(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	m := h.registerModel(t, "submit-owner2", "anthropic", "claude-owner2-5")
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}})
	base := Request{Spec: spec, VariantID: "v_owner2", Label: "owner2", Seed: 12}

	orgA := base
	orgA.TenantID = "org_first"
	first, err := h.svc.Submit(ctx, orgA)
	if err != nil {
		t.Fatalf("Submit as org_first: %v", err)
	}
	dropQueued(t, first.RunID)
	orgB := base
	orgB.TenantID = "org_second"
	if _, err := h.svc.Submit(ctx, orgB); err != nil {
		t.Fatalf("Submit as org_second: %v", err)
	}

	var owner string
	if err := testDB.QueryRow(`SELECT coalesce(tenant_id,'') FROM run WHERE run_id=$1`, first.RunID).
		Scan(&owner); err != nil {
		t.Fatalf("read the run's owner: %v", err)
	}
	if owner != "org_first" {
		t.Fatalf("run %s is owned by %q. This test PINS a known defect: the second submitter does not "+
			"acquire the run, and gets an id it cannot read. If ownership now transfers or the second "+
			"submit is refused, that is a change in behaviour — delete this test and record the new one.",
			first.RunID, owner)
	}
	t.Logf("known defect, pinned: run %s was submitted by org_first and org_second and is owned by %q; "+
		"org_second holds a run_id its own listing will never return", first.RunID, owner)
}
