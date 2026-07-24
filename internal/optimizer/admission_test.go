package optimizer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// admission_test.go is the P6 side of P8's operator brake (P8 FR7/FR12, task 7.3).
//
// The kill switch is only real if the LOOP stops. These tests drive the actual Controller over a
// candidate that would otherwise merge, and assert that an armed admission gate — and, separately, an
// INDETERMINATE one — leaves the last-good Variant Spec live with nothing merged.

// fixedAdmission answers the loop's pre-merge question with a canned verdict.
type fixedAdmission struct {
	allowed bool
	reason  string
	err     error
	// asked records the tenants the loop asked about, proving the check happens per merge rather than
	// once at startup.
	asked []string
}

func (a *fixedAdmission) AllowMerge(customerID string) (bool, string, error) {
	a.asked = append(a.asked, customerID)
	return a.allowed, a.reason, a.err
}

// mergeableInput is a run whose single candidate passes verification and every gate, so anything that
// prevents the merge is the thing under test rather than an incidental failure.
func mergeableInput(t *testing.T) (*Controller, RunInput, *FakeRepo) {
	t.Helper()
	cand := mkCand(`{"v":"admission"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.70, 0.4)}}
	repo := NewFakeRepo([]byte(testBaseline))
	c := newController(verifier, repo, NewMemLedger(), NewKillSwitch(), cand)
	auth := testAuthority(true)
	auth.CustomerID = "tenant-acme"
	return c, baseInput(auth), repo
}

// TestAdmissionAllowedStillMerges is the control: with the brake off, this run DOES merge — so the
// tests below prove the brake, not a broken fixture.
func TestAdmissionAllowedStillMerges(t *testing.T) {
	c, in, _ := mergeableInput(t)
	adm := &fixedAdmission{allowed: true}
	c.Admission = adm

	res, err := c.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Merges) != 1 {
		t.Fatalf("the control run merged %d candidates, want 1", len(res.Merges))
	}
	if len(adm.asked) == 0 || adm.asked[0] != "tenant-acme" {
		t.Errorf("the loop did not ask admission about the run's tenant: %v", adm.asked)
	}
}

// TestArmedAdmissionHaltsTheMerge: the operator console armed the switch, so nothing merges and the
// last-good spec stays live.
func TestArmedAdmissionHaltsTheMerge(t *testing.T) {
	c, in, repo := mergeableInput(t)
	headBefore, _ := repo.Head(context.Background())
	c.Admission = &fixedAdmission{allowed: false, reason: "global kill switch armed: provider incident"}

	res, err := c.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Merges) != 0 {
		t.Fatalf("an armed kill switch did not stop the merge: %d merges", len(res.Merges))
	}
	if res.State != StateStopped {
		t.Errorf("run state = %s, want %s", res.State, StateStopped)
	}
	if !strings.Contains(res.StopReason, "operator console") {
		t.Errorf("stop reason = %q, want it to name the operator halt", res.StopReason)
	}
	if res.MergeEnabled {
		t.Error("merge was left armed after an operator halt")
	}
	if head, _ := repo.Head(context.Background()); head != headBefore {
		t.Error("the repository head moved despite the halt — the last-good spec is not live")
	}
}

// TestIndeterminateAdmissionFailsClosedToHalt is the load-bearing half: when the kill-switch state
// cannot be read, no merge proceeds.
func TestIndeterminateAdmissionFailsClosedToHalt(t *testing.T) {
	c, in, repo := mergeableInput(t)
	headBefore, _ := repo.Head(context.Background())
	c.Admission = &fixedAdmission{allowed: true, err: errors.New("kill-switch store unreachable")}

	res, err := c.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Merges) != 0 {
		t.Fatalf("an INDETERMINATE kill-switch state did not stop the merge: %d merges", len(res.Merges))
	}
	if res.State != StateStopped {
		t.Errorf("run state = %s, want %s", res.State, StateStopped)
	}
	if !strings.Contains(res.StopReason, "indeterminate") {
		t.Errorf("stop reason = %q, want it to name the indeterminate state", res.StopReason)
	}
	if head, _ := repo.Head(context.Background()); head != headBefore {
		t.Error("the repository head moved on an indeterminate kill-switch read")
	}
	// Note the fixture returns allowed=true alongside the error. A reader that trusted the boolean and
	// ignored the error would merge here — which is exactly the mistake this test exists to catch.
}

// TestNilAdmissionLeavesTheLoopUnchanged: a self-hosted deployment with no operator console still
// runs, because the brake is a seam rather than a requirement.
func TestNilAdmissionLeavesTheLoopUnchanged(t *testing.T) {
	c, in, _ := mergeableInput(t)
	c.Admission = nil
	res, err := c.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Merges) != 1 {
		t.Fatalf("a deployment with no operator console merged %d candidates, want 1", len(res.Merges))
	}
}
