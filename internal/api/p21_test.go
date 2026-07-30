package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/billing"
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

// ─────────────────────────────────────────────────────────────────────────────
// Task 4.2 — the one inbound-from-internet path
// ─────────────────────────────────────────────────────────────────────────────

// recordingSink stands in for the billing capability. It records what the handler passed it and
// returns whatever ack the test wants, so the assertions are about the HANDLER's behaviour: does it
// hand over the raw body and the Stripe header, and does it write the status back verbatim.
type recordingSink struct {
	ack       billing.WebhookAck
	gotBody   []byte
	gotHeader string
	calls     int
}

func (r *recordingSink) HandleStripeWebhook(_ context.Context, body []byte, header string) billing.WebhookAck {
	r.calls++
	r.gotBody, r.gotHeader = body, header
	return r.ack
}

// TestBillingWebhookRouteWritesTheStatusTheBillingCapabilityDecided is P21 task 4.2 + 4.3 at the HTTP
// boundary.
//
// The status is the contract, and the handler's only job is to write it down. A handler that upgraded
// a 500 to a 200 ("the request was well-formed, after all") would tell Stripe an unrecorded event
// succeeded — which is the one thing this endpoint may never do.
func TestBillingWebhookRouteWritesTheStatusTheBillingCapabilityDecided(t *testing.T) {
	cases := map[string]billing.WebhookAck{
		"applied":         {ProviderEventID: "evt_1", Applied: true, Status: 200},
		"redelivery":      {ProviderEventID: "evt_1", Duplicate: true, Status: 200},
		"forged":          {Status: 400, Reason: "signature verification failed"},
		"not persisted":   {Status: 500, Reason: "the delivery was NOT durably recorded"},
		"secret unusable": {Status: 500, Reason: "credential unavailable"},
	}
	for name, ack := range cases {
		sink := &recordingSink{ack: ack}
		s := New(nil, config.Config{})
		s.MountBillingWebhook(sink)

		req := httptest.NewRequest(http.MethodPost, "/billing/webhook", strings.NewReader(`{"id":"evt_1"}`))
		req.Header.Set(billing.StripeSignatureHeader, "t=1,v1=abc")
		rr := httptest.NewRecorder()
		s.Handler.ServeHTTP(rr, req)

		if rr.Code != ack.Status {
			t.Errorf("%s: status = %d, want %d — the handler must write the billing capability's decision "+
				"verbatim, because only that layer can see whether the effect persisted", name, rr.Code, ack.Status)
		}
		if sink.calls != 1 {
			t.Errorf("%s: the sink was called %d times", name, sink.calls)
		}
		if string(sink.gotBody) != `{"id":"evt_1"}` {
			t.Errorf("%s: the handler passed %q — the RAW bytes must reach the verifier, because a signature "+
				"covers the bytes Stripe sent", name, sink.gotBody)
		}
		if sink.gotHeader != "t=1,v1=abc" {
			t.Errorf("%s: the Stripe-Signature header did not reach the verifier (%q)", name, sink.gotHeader)
		}
	}
}

// TestBillingWebhookRouteIsUnmountedByDefault: the endpoint exists only where a deployment exposes it.
// A route that answers on every deployment is inbound surface area nobody chose.
func TestBillingWebhookRouteIsUnmountedByDefault(t *testing.T) {
	s := New(nil, config.Config{})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/billing/webhook", strings.NewReader("{}")))
	if rr.Code == http.StatusOK {
		t.Fatal("an unmounted webhook endpoint answered 200")
	}
}

// TestBillingWebhookRouteBoundsTheBody: an internet-facing endpoint reads a bounded body or it is a
// memory-exhaustion primitive with a signature check bolted on.
func TestBillingWebhookRouteBoundsTheBody(t *testing.T) {
	sink := &recordingSink{ack: billing.WebhookAck{Status: 200, Applied: true}}
	s := New(nil, config.Config{})
	s.MountBillingWebhook(sink)

	huge := strings.NewReader(`{"pad":"` + strings.Repeat("x", billingWebhookMaxBody+1024) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/billing/webhook", huge)
	req.Header.Set(billing.StripeSignatureHeader, "t=1,v1=abc")
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("an oversized body returned %d, want 400", rr.Code)
	}
	if sink.calls != 0 {
		t.Error("an oversized body reached the verifier — it must be refused before any side effect")
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
