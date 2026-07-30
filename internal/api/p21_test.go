package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
)

// p21_test.go covers the P21 halves that live on the HTTP surface.
//
// Task 3.1's last clause — "confirm /readyz's secrets_source covers them" — is asserted here rather
// than assumed, because the failure it guards is a health endpoint being CONFIDENTLY WRONG: a
// deployment whose LLM credentials come from a manager while its billing credentials quietly come from
// somewhere else, with /readyz reporting one source for both.

// describeStub stands in for *billing.Service.Describe on the readiness surface. The api package takes
// a one-method interface rather than importing billing, so the test does too.
type describeStub map[string]string

func (d describeStub) Describe() map[string]string { return d }

// TestReadyz_NamesTheBillingProviderAndItsCredentialSource is P21 task 3.1.
func TestReadyz_NamesTheBillingProviderAndItsCredentialSource(t *testing.T) {
	const secret = "sk_test_p21_readyz_placeholder_not_a_secret"

	s := New(nil, config.Config{})
	s.SetBillingRollout(describeStub{
		"billing": "enabled", "provider_mode": "test", "gainshare": "disabled", "auto_merge_entitlement": "disabled",
	})
	// What billing.Service.Describe actually returns: the provider identity and the SOURCE its
	// credentials resolve from. Never a credential, never a credential's id.
	s.SetBillingCapability(describeStub{
		"provider": "stripe(test)", "secrets_source": "aws-secrets-manager", "secrets_detail": "region eu-west-1",
	})

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}

	prov, ok := body["billing_provider"].(map[string]any)
	if !ok {
		t.Fatalf("/readyz does not report billing_provider — an operator cannot check WHICH processor is "+
			"wired or WHERE its credentials come from on the box that is misbehaving: %v", body)
	}
	if prov["provider"] != "stripe(test)" {
		t.Errorf("billing_provider.provider = %v, want stripe(test) — the mode is part of the identity, "+
			"because 'is this box charging real money' is the operational question", prov["provider"])
	}
	if prov["secrets_source"] != "aws-secrets-manager" {
		t.Errorf("billing_provider.secrets_source = %v — the billing credential's source must be externally "+
			"readable, or the claim that it comes from the seam is unverifiable", prov["secrets_source"])
	}

	// The rollout is still reported separately: which gates are open and which processor is behind them
	// are different questions, and a single field would let one answer stand in for the other.
	roll, ok := body["billing_rollout"].(map[string]any)
	if !ok || roll["provider_mode"] != "test" {
		t.Errorf("billing_rollout = %v, want the gate state including provider_mode", body["billing_rollout"])
	}

	// 🔴 No credential, anywhere in the document.
	if strings.Contains(rr.Body.String(), secret) {
		t.Error("/readyz leaked a billing credential")
	}
	for _, shape := range []string{"sk_live_", "sk_test_", "whsec_"} {
		if strings.Contains(rr.Body.String(), shape) {
			t.Errorf("/readyz body contains %q — the readiness surface names the SOURCE, never a secret", shape)
		}
	}
}

// TestReadyz_OmitsTheBillingProviderWhenNoneIsWired asserts the absent-not-unknown rule the other two
// billing signals already follow: a deployment that wired no billing capability HAS none, and saying so
// by omission beats inventing a status a monitor would then alert on.
func TestReadyz_OmitsTheBillingProviderWhenNoneIsWired(t *testing.T) {
	s := New(nil, config.Config{})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if _, present := body["billing_provider"]; present {
		t.Errorf("billing_provider is present with nothing wired: %v", body["billing_provider"])
	}
}
