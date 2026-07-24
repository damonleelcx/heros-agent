package optimizer

import (
	"context"
	"strings"
	"testing"
)

// entitlement_test.go is P7 task 5.2 / FR8, tested where it actually bites: inside the loop, at the
// merge decision.
//
// The requirement has two halves and BOTH are load-bearing. Without the Enterprise entitlement the
// loop must (a) not merge, and (b) not silently drop the candidate either — it opens a pull request for
// a human, which is the Assisted contract the customer does have. A gate that denies by dropping is a
// gate that makes the product mysteriously stop working.

// stubEntitlement is a canned MergeEntitlement. It records who it was asked about, so a test can prove
// the loop CONSULTED the gate rather than happening to agree with it.
type stubEntitlement struct {
	allow   bool
	reason  string
	upgrade string
	asked   []string
}

func (s *stubEntitlement) AllowAutoMerge(customerID string) (bool, string, string) {
	s.asked = append(s.asked, customerID)
	if s.allow {
		return true, "", ""
	}
	return false, s.reason, s.upgrade
}

// TestLoop_EntitlementDenied_FallsBackToOpenPR is the entitlements spec's fallback scenario.
func TestLoop_EntitlementDenied_FallsBackToOpenPR(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	repo := NewFakeRepo([]byte(testBaseline))
	ledger := NewMemLedger()
	c := newController(StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.4)}}, repo, ledger, NewKillSwitch(), cand)
	gate := &stubEntitlement{allow: false,
		reason:  "Autonomous auto-merge is not included in the Team plan",
		upgrade: "enterprise"}
	c.Entitlement = gate

	auth := testAuthority(true) // every TECHNICAL prerequisite armed — only the commercial gate denies
	auth.CustomerID = "cus_team"
	res, err := c.Run(context.Background(), baseInput(auth))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The gate was actually consulted, for the right customer.
	if len(gate.asked) == 0 {
		t.Fatal("the loop never consulted the entitlement gate before merging")
	}
	if gate.asked[0] != "cus_team" {
		t.Errorf("the gate was asked about %q, want cus_team", gate.asked[0])
	}

	// (a) NOTHING was merged, and the last-good spec is still live.
	if len(res.Merges) != 0 {
		t.Errorf("merges = %d, want 0 — an un-entitled customer reached auto-merge", len(res.Merges))
	}
	if repo.Merged() != 0 {
		t.Errorf("the repository saw %d merges", repo.Merged())
	}

	// (b) A pull request WAS opened for a human — not a draft, because the change is verified and
	// reviewable; the customer is entitled to Assisted, just not to Autonomous.
	if repo.Opened() == 0 {
		t.Fatal("the candidate was silently dropped — no pull request was opened for a human")
	}
	if repo.LastPRWasDraft() {
		t.Error("the fallback PR was opened as a DRAFT; the change is verified and meant to be reviewed")
	}

	// The denial is AUDITED with its named reason and upgrade path — a commercial decision that changed
	// what the loop did to the repository must survive in the trail.
	var found *LedgerEvent
	for i, ev := range ledger.Events(auth.RunID) {
		if ev.Type == EventEntitlementDenied {
			found = &ledger.Events(auth.RunID)[i]
		}
	}
	if found == nil {
		t.Fatal("no entitlement_denied event in the change ledger — the fallback is unaudited")
	}
	if !strings.Contains(found.Summary, "Team plan") {
		t.Errorf("the audit row does not carry the named reason: %q", found.Summary)
	}
	if !strings.Contains(found.Summary, "enterprise") {
		t.Errorf("the audit row does not carry the upgrade path: %q", found.Summary)
	}
	if !strings.Contains(found.Summary, "pull request") {
		t.Errorf("the audit row does not say what the loop did instead: %q", found.Summary)
	}
	if found.PRRef == "" {
		t.Error("the audit row does not reference the PR the loop opened instead")
	}
}

// TestLoop_EntitlementAllowed_Merges is the other half of the spec scenario: under identical
// conditions, an entitled customer's merge proceeds. Without this, the test above would pass for a
// loop that never merges at all.
func TestLoop_EntitlementAllowed_Merges(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	repo := NewFakeRepo([]byte(testBaseline))
	c := newController(StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.4)}}, repo, NewMemLedger(), NewKillSwitch(), cand)
	gate := &stubEntitlement{allow: true}
	c.Entitlement = gate

	auth := testAuthority(true)
	auth.CustomerID = "cus_enterprise"
	res, err := c.Run(context.Background(), baseInput(auth))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(gate.asked) == 0 {
		t.Fatal("the loop never consulted the gate")
	}
	if len(res.Merges) != 1 {
		t.Fatalf("merges = %d, want 1 — the entitled customer's merge did not happen", len(res.Merges))
	}
	if repo.Merged() != 1 {
		t.Errorf("the repository saw %d merges, want 1", repo.Merged())
	}
}

// TestLoop_NoEntitlementGate_IsUnchanged: a deployment with no billing stack has nothing to enforce,
// and P7 must not change how such a deployment behaves.
func TestLoop_NoEntitlementGate_IsUnchanged(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	repo := NewFakeRepo([]byte(testBaseline))
	c := newController(StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.4)}}, repo, NewMemLedger(), NewKillSwitch(), cand)
	// c.Entitlement stays nil.

	res, err := c.Run(context.Background(), baseInput(testAuthority(true)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Merges) != 1 {
		t.Errorf("merges = %d, want 1 — wiring the gate changed a deployment that has no billing", len(res.Merges))
	}
}

// TestLoop_EntitlementDenied_FailsClosedWhenUnauditable: if the fallback itself cannot be audited, the
// loop stops rather than quietly opening a PR nobody will be able to explain. This is the same
// fail-closed rule a merge follows, applied to the commercial decision.
func TestLoop_EntitlementDenied_FailsClosedWhenUnauditable(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	repo := NewFakeRepo([]byte(testBaseline))
	ledger := NewMemLedger()
	c := newController(StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.4)}}, repo, ledger, NewKillSwitch(), cand)
	c.Entitlement = &stubEntitlement{allow: false, reason: "not included in the Team plan", upgrade: "enterprise"}

	auth := testAuthority(true)
	auth.CustomerID = "cus_team"

	// The ledger goes down AFTER the grant is recorded, so the run starts and reaches the gate.
	c.OnIteration = func(RunResult) { ledger.SetDown(true) }

	res, err := c.Run(context.Background(), baseInput(auth))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Merges) != 0 {
		t.Errorf("merges = %d, want 0", len(res.Merges))
	}
	if res.State != StateStopped {
		t.Errorf("state = %q, want stopped — an unauditable fallback must fail closed", res.State)
	}
	if res.MergeEnabled {
		t.Error("merge stayed enabled after failing closed")
	}
}
