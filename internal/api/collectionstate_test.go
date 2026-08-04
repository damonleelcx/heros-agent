package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
)

// collectionstate_test.go fences the difference between a record that is MISSING and a record this
// deployment can never have.
//
// Both are a 404 on `GET /api/v1/customers/{id}/billing`, and for a long time both said "no billing
// account for {id}". The console rendered that through its not-found panel — "the identifier does not
// resolve" — which is right for a typo'd tenant and wrong for an install with no payment provider,
// where an account is unreachable by construction: the only thing that creates one is checkout, and
// checkout is P21. An operator reading the first sentence goes looking for the record, or for the
// button that would create it. Neither exists.
//
// The distinction is made HERE because the server is the only party that knows whether this deployment
// mounts a payment provider. The console branches on the CODE, never the prose, so a copy edit cannot
// change behaviour — which is the other half of what this file pins.

// billingSourceStub answers "no such account" for everything, which is the state under test.
type billingSourceStub struct{}

func (billingSourceStub) Billing(string, string) (BillingView, bool) { return BillingView{}, false }

func (billingSourceStub) SetGainshareConsent(string, bool) (BillingView, error) {
	return BillingView{}, errors.New("not used by this fence")
}

func (billingSourceStub) Periods(string) []string { return nil }

// billingNotFoundBody performs the 404 and returns its decoded body. withPayments decides whether this
// deployment mounts a collection surface.
func billingNotFoundBody(t *testing.T, withPayments bool) map[string]string {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountBilling(billingSourceStub{})
	if withPayments {
		// Any non-nil source: this fence is about whether collection EXISTS, not what it answers.
		s.MountPayments(paymentsSourceStub{})
	}

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/customers/acme/billing", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for an absent account, got %d — this fence is about the 404's CONTENT", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the 404 body: %v", err)
	}
	return body
}

// TestNoProviderSaysCollectedNothingRatherThanNoSuchAccount is the defect.
func TestNoProviderSaysCollectedNothingRatherThanNoSuchAccount(t *testing.T) {
	body := billingNotFoundBody(t, false)

	if body["reason_code"] != ReasonCollectionNotConfigured {
		t.Errorf("a deployment with no payment provider must mark this 404 as a configured state "+
			"(reason_code %q), so the console can render it as one. Without the code the console has "+
			"only the prose to go on, and branching on prose puts the decision in two places.\ngot: %v",
			ReasonCollectionNotConfigured, body)
	}
	if !strings.Contains(body["error"], "collects no payments") {
		t.Errorf("the sentence does not say what is actually true — that this install collects no "+
			"payments, so no account is ever created.\ngot: %q", body["error"])
	}
	// 🔴 The old sentence, which sends an operator looking for a record that cannot exist.
	if strings.HasPrefix(body["error"], "no billing account for") {
		t.Errorf("the 404 still reads as a missing record on a deployment that can never have one: %q",
			body["error"])
	}
}

// TestWithAProviderItIsStillAPlainNotFound is the other direction, and it matters as much: where
// checkout DOES exist, an absent account really is just an absent account — the customer has not
// checked out yet — and dressing that up as "this deployment collects no payments" would be false.
func TestWithAProviderItIsStillAPlainNotFound(t *testing.T) {
	body := billingNotFoundBody(t, true)

	if body["reason_code"] == ReasonCollectionNotConfigured {
		t.Errorf("a deployment that DOES mount collection is reporting that it collects no payments; "+
			"an account is simply not created yet, and the remedy is checkout.\ngot: %v", body)
	}
	if !strings.Contains(body["error"], "no billing account for acme") {
		t.Errorf("the plain not-found sentence was lost: %q", body["error"])
	}
}

// paymentsSourceStub is a PaymentsSource that exists and answers nothing. Presence is the only
// property under test — this fence asks whether the deployment MOUNTS collection, not what it says.
type paymentsSourceStub struct{}

func (paymentsSourceStub) Payment(string, string) (PaymentView, bool) { return PaymentView{}, false }

func (paymentsSourceStub) StartCheckout(context.Context, string, string, string, string) (CheckoutView, error) {
	return CheckoutView{}, errors.New("not used by this fence")
}

func (paymentsSourceStub) ChangePlan(context.Context, string, string) (PlanChangeView, error) {
	return PlanChangeView{}, errors.New("not used by this fence")
}
