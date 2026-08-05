package billing

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/account"
)

// collection_test.go covers the two P27 additions to the collection path: the provider customer that a
// Free account does not have until its first upgrade, and the downgrade that would leave an organization
// over its new seat allowance.
//
// It reuses `newParityStack` — the same stack `gate_test.go` drives — because a checkout proven against a
// second, simpler harness is a checkout proven against a stack no deployment runs.

// TestFirstCheckoutMintsAndPERSISTSTheProviderCustomer is 6.4.
//
// 🔴 `EnsureCustomer` was already called here before P27, and its answer was thrown away. That was
// invisible while every account was hand-created with a handle already in it. A Free account starts with
// none, so this is the first moment one exists — and if it is not stored, the next checkout mints again,
// the platform never learns which provider customer is theirs, and `SetPlan(…, charges: true)` fails the
// invariant the database now holds.
func TestFirstCheckoutMintsAndPersistsTheProviderCustomer(t *testing.T) {
	h := newParityStack(t, "stripe")

	// A Free account, exactly as sign-up creates one: no provider handle at all.
	if _, err := h.accounts.Create(account.Account{
		CustomerID: "cus_free", ActivePlanID: "free", PlanConfigVersion: "cfg-a", CreatedAt: clockNow,
	}); err != nil {
		t.Fatalf("seed free account: %v", err)
	}

	if _, err := h.svc.StartCheckout(t.Context(), "cus_free", "Team", "https://ok", "https://no"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	stored, err := h.accounts.Get("cus_free")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.ProviderCustomerHandle == "" {
		t.Fatal("the provider customer was created and not recorded — a retry would create a second one, " +
			"and the platform would never learn which one is this customer's")
	}
	first := stored.ProviderCustomerHandle

	// A second checkout — the impatient double-click — must reuse it rather than mint again.
	if _, err := h.svc.StartCheckout(t.Context(), "cus_free", "Team", "https://ok", "https://no"); err != nil {
		t.Fatalf("second checkout: %v", err)
	}
	after, _ := h.accounts.Get("cus_free")
	if after.ProviderCustomerHandle != first {
		t.Errorf("the handle changed between checkouts: %q then %q", first, after.ProviderCustomerHandle)
	}
}

// heldSeats is a seat count, for the downgrade check.
type heldSeats int

func (h heldSeats) SeatsHeld(string) (int, error) { return int(h), nil }

// TestADowngradeBelowTheHeldSeatsIsRefusedWithBothNumbers is 6.5.
func TestADowngradeBelowTheHeldSeatsIsRefusedWithBothNumbers(t *testing.T) {
	h := newParityStack(t, "stripe")
	if _, err := h.accounts.Create(account.Account{
		CustomerID: "cus_big", ProviderCustomerHandle: "prov_cus_1",
		ActivePlanID: "business", PlanConfigVersion: "cfg-a", PlanCharges: true, CreatedAt: clockNow,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Business allows 25; Team allows 5. The organization holds 9.
	h.svc.WithSeatCounter(heldSeats(9))

	_, err := h.svc.ChangePlan(t.Context(), "cus_big", "Team")
	if !errors.Is(err, ErrSeatsExceedPlan) {
		t.Fatalf("a downgrade below the held seat count was accepted (err=%v).\n"+
			"Accepting it puts the organization over an allowance it just bought, with the first symptom "+
			"being an unrelated action failing.", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "5") || !strings.Contains(msg, "9") {
		t.Errorf("the refusal does not name BOTH numbers: %q", msg)
	}
	if !strings.Contains(msg, "remove a member") {
		t.Errorf("the refusal does not name the remedy: %q", msg)
	}

	// Below the allowance, the same downgrade goes through.
	h.svc.WithSeatCounter(heldSeats(3))
	if _, err := h.svc.ChangePlan(t.Context(), "cus_big", "Team"); err != nil && errors.Is(err, ErrSeatsExceedPlan) {
		t.Fatalf("a downgrade within the allowance was refused: %v", err)
	}
}

// TestAnOperatorSeatOverrideMakesRoomForADowngrade — the override REPLACES the plan's allowance for that
// one limit, unchanged from P7, and a downgrade an operator has already made room for is not refused.
func TestAnOperatorSeatOverrideMakesRoomForADowngrade(t *testing.T) {
	h := newParityStack(t, "stripe")
	if _, err := h.accounts.Create(account.Account{
		CustomerID: "cus_over", ProviderCustomerHandle: "prov_cus_2",
		ActivePlanID: "business", PlanConfigVersion: "cfg-a", PlanCharges: true, CreatedAt: clockNow,
		QuotaOverrides: map[string]float64{"seats": 50},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h.svc.WithSeatCounter(heldSeats(9))
	if _, err := h.svc.ChangePlan(t.Context(), "cus_over", "Team"); err != nil && errors.Is(err, ErrSeatsExceedPlan) {
		t.Fatal("an operator-set seat override did not make room for the downgrade")
	}
}

// TestAnUnmeasurableSeatCountDoesNotBlockAPlanChange: a membership-store outage must not stop a customer
// changing a plan they are entitled to change.
func TestAnUnmeasurableSeatCountDoesNotBlockAPlanChange(t *testing.T) {
	h := newParityStack(t, "stripe")
	if _, err := h.accounts.Create(account.Account{
		CustomerID: "cus_blind", ProviderCustomerHandle: "prov_cus_3",
		ActivePlanID: "business", PlanConfigVersion: "cfg-a", PlanCharges: true, CreatedAt: clockNow,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h.svc.WithSeatCounter(brokenSeats{})
	if _, err := h.svc.ChangePlan(t.Context(), "cus_blind", "Team"); errors.Is(err, ErrSeatsExceedPlan) {
		t.Fatal("a seat-store outage refused a plan change")
	}
}

type brokenSeats struct{}

func (brokenSeats) SeatsHeld(string) (int, error) { return 0, errors.New("membership store is down") }
