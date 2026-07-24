package account

import (
	"errors"
	"testing"
	"time"
)

var at = time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

func newAcct(t *testing.T) (*MemStore, Account) {
	t.Helper()
	s := NewMemStore()
	a, err := s.Create(Account{
		CustomerID:             "cus_acme",
		ProviderCustomerHandle: "prov_cus_9f3a21",
		ActivePlanID:           "team",
		PlanConfigVersion:      "cfg-a",
		CreatedAt:              at,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return s, a
}

// TestAccountHoldsProviderHandleNotCardData is task 1.1's security invariant. The platform holds an
// OPAQUE handle; card data stays with the PCI-compliant provider. A comment cannot enforce that — this
// asserts the store actively REFUSES a card-shaped value, so the shape cannot arrive through a
// mis-wired integration.
func TestAccountHoldsProviderHandleNotCardData(t *testing.T) {
	s := NewMemStore()

	// Real, well-known TEST card numbers (never issued to anyone) — the exact shape that must never be
	// storable in a handle field.
	cardShaped := []string{
		"4242424242424242",    // 16-digit, Luhn-valid
		"4242 4242 4242 4242", // with the separators a paste carries
		"4242-4242-4242-4242",
		"378282246310005", // 15-digit Amex shape
	}
	for _, c := range cardShaped {
		if _, err := s.Create(Account{CustomerID: "cus_" + c, ProviderCustomerHandle: c}); !errors.Is(err, ErrCardData) {
			t.Errorf("handle %q was accepted (err=%v) — the platform must never store card data", c, err)
		}
	}

	// An opaque provider handle — including an all-digit one that is NOT Luhn-valid — is fine. The
	// fence rejects the card family, not every string with digits in it.
	for _, ok := range []string{"prov_cus_9f3a21", "cus_ABC123", "1234567890123"} {
		if _, err := s.Create(Account{CustomerID: "cus_ok_" + ok, ProviderCustomerHandle: ok}); err != nil {
			t.Errorf("legitimate handle %q was rejected: %v", ok, err)
		}
	}

	if _, err := s.Create(Account{CustomerID: "cus_x"}); !errors.Is(err, ErrEmptyHandle) {
		t.Errorf("an account with no provider handle must be refused, got %v", err)
	}
	if _, err := s.Create(Account{ProviderCustomerHandle: "h"}); !errors.Is(err, ErrEmptyCustomer) {
		t.Errorf("an account with no customer id must be refused, got %v", err)
	}
}

// TestPlanAndConfigVersionMoveTogether: an account pins BOTH the plan id and the config version it
// resolved under, so a closed period stays explainable after the next config publish.
func TestPlanAndConfigVersionMoveTogether(t *testing.T) {
	s, a := newAcct(t)
	if a.ActivePlanID != "team" || a.PlanConfigVersion != "cfg-a" {
		t.Fatalf("precondition: %+v", a)
	}

	if _, err := s.SetPlan("cus_acme", "enterprise", ""); err == nil {
		t.Error("a plan change with no plan_config_version must be refused — the pair is what makes a " +
			"closed period explainable")
	}

	got, err := s.SetPlan("cus_acme", "enterprise", "cfg-b")
	if err != nil {
		t.Fatalf("set plan: %v", err)
	}
	if got.ActivePlanID != "enterprise" || got.PlanConfigVersion != "cfg-b" {
		t.Errorf("after plan change: %+v", got)
	}

	// Downstream read path (not the setter's return value): the change is durable in the store.
	reread, err := s.Get("cus_acme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reread.ActivePlanID != "enterprise" || reread.PlanConfigVersion != "cfg-b" {
		t.Errorf("re-read from the store disagrees with the write: %+v", reread)
	}
}

// TestGainshareConsentIsRecordedAndRevocable: consent is an informed, recorded, REVOCABLE contract
// (task 7.2). Revocation must actually clear the timestamp, not leave a stale "consented_at" that reads
// as still-consented in an audit.
func TestGainshareConsentIsRecordedAndRevocable(t *testing.T) {
	s, _ := newAcct(t)

	a, err := s.SetGainshareConsent("cus_acme", true, at)
	if err != nil {
		t.Fatalf("consent: %v", err)
	}
	if !a.GainshareConsent || a.ConsentedAt == nil || !a.ConsentedAt.Equal(at) {
		t.Fatalf("consent not recorded with its timestamp: %+v", a)
	}

	a, err = s.SetGainshareConsent("cus_acme", false, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if a.GainshareConsent {
		t.Error("consent was not revoked")
	}
	if a.ConsentedAt != nil {
		t.Errorf("revocation left a stale consented_at (%v) — an audit would read it as still consented", a.ConsentedAt)
	}

	reread, _ := s.Get("cus_acme")
	if reread.GainshareConsent || reread.ConsentedAt != nil {
		t.Errorf("re-read disagrees with the revocation: %+v", reread)
	}
}

// TestStoreErrorsAreDistinguishable: a missing account and a duplicate are different problems and must
// be different errors — callers act differently on each.
func TestStoreErrorsAreDistinguishable(t *testing.T) {
	s, _ := newAcct(t)
	if _, err := s.Get("nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := s.Create(Account{CustomerID: "cus_acme", ProviderCustomerHandle: "h2"}); !errors.Is(err, ErrExists) {
		t.Errorf("want ErrExists, got %v", err)
	}
	if _, err := s.SetPlan("nobody", "team", "cfg-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := s.SetGainshareConsent("nobody", true, at); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
