package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stripewebhook_test.go is P21 section 4: the inbound path, verify → dedupe → persist → ack.
//
// The order is the whole design, so the tests are written to catch an order violation rather than a
// wrong answer: each one asserts what did NOT happen (no claim, no state, no ack) as well as what did.
// A handler that verifies after parsing still returns the right answer for a valid payload — the bug
// only shows on the forged one, and only if the test looks at the state that should not have moved.

const webhookSecret = "webhook-signing-secret-DO-NOT-LEAK-test"

// webhookHarness wires a Service with a delivery store and a state mirror that can both be taken down.
type webhookHarness struct {
	svc        *Service
	deliveries *MemDeliveries
	states     *MemStates
	now        time.Time
}

func newWebhookHarness(t *testing.T) *webhookHarness {
	t.Helper()
	h := newHarness(t, "team")
	deliveries, states := NewMemDeliveries(), NewMemStates()
	h.svc.WithDeliveries(deliveries).WithStates(states)
	return &webhookHarness{svc: h.svc, deliveries: deliveries, states: states, now: clockNow}
}

// stripeDelivery builds a delivery signed the way Stripe signs one.
func (h *webhookHarness) stripeDelivery(id string, typ WebhookType, customerID string, extra map[string]string) (body []byte, header string) {
	p := map[string]string{"id": id, "type": string(typ), "customer_id": customerID}
	for k, v := range extra {
		p[k] = v
	}
	body, _ = json.Marshal(p)
	return body, StripeSignatureFor(webhookSecret, h.now, body)
}

// TestStripeSignatureIsVerifiedBeforeAnySideEffect is task 4.1 / FR13.
func TestStripeSignatureIsVerifiedBeforeAnySideEffect(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()

	body, good := h.stripeDelivery("evt_1", WebhookInvoicePaymentFailed, "cus_acme", map[string]string{"invoice_ref": "in_1"})

	bad := map[string]string{
		"no signature at all":    "",
		"forged v1":              "t=" + unixOf(h.now) + ",v1=" + strings.Repeat("a", 64),
		"right shape, no secret": StripeSignatureFor("not-the-signing-secret", h.now, body),
		// The signature is valid for a DIFFERENT body — the classic mistake of verifying a re-serialized
		// payload instead of the bytes that were signed.
		"valid for another body": StripeSignatureFor(webhookSecret, h.now, []byte(`{"id":"evt_other"}`)),
	}
	for name, sig := range bad {
		ack := h.svc.HandleStripeWebhook(ctx, body, sig)
		if ack.Status == 200 {
			t.Errorf("%s: the endpoint ACKED a payload it could not verify", name)
		}
		if ack.Status != 400 {
			t.Errorf("%s: status = %d, want 400 — a forged delivery is not improved by a retry", name, ack.Status)
		}
		if ack.Applied {
			t.Errorf("%s: a side effect was applied", name)
		}
	}

	// 🔴 Nothing moved: no dedupe claim, no mirrored state. This is the assertion that catches a handler
	// which verifies too late — the answer above is already correct in that case.
	if n := h.deliveries.Count(); n != 0 {
		t.Errorf("%d delivery claims exist after only forged payloads — verification is not step one", n)
	}
	if st := h.svc.BillingState("cus_acme"); st.CustomerID != "" || st.PaymentFailed {
		t.Errorf("a forged payload moved the mirrored state: %+v", st)
	}

	// The genuine one works, so the guard is a guard rather than a wall.
	if ack := h.svc.HandleStripeWebhook(ctx, body, good); ack.Status != 200 || !ack.Applied {
		t.Fatalf("a correctly signed delivery was not applied: %+v", ack)
	}
	if !h.svc.BillingState("cus_acme").PaymentFailed {
		t.Error("the verified delivery did not reach the mirrored state")
	}
}

// TestStripeSignatureAcceptsARotatedSecret is the multi-`v1` case — what a secret rotation looks like
// on the wire. A verifier that read only the first signature would reject half the deliveries in the
// exact window where a rejection is hardest to diagnose.
func TestStripeSignatureAcceptsARotatedSecret(t *testing.T) {
	h := newWebhookHarness(t)
	body, _ := h.stripeDelivery("evt_rotate", WebhookInvoicePaid, "cus_acme", nil)

	ts := unixOf(h.now)
	old := strings.TrimPrefix(SignWebhook("the-previous-signing-secret", ts, body), "v1=")
	current := strings.TrimPrefix(SignWebhook(webhookSecret, ts, body), "v1=")

	// Stripe signs with BOTH during the overlap, old first.
	header := "t=" + ts + ",v1=" + old + ",v1=" + current
	if ack := h.svc.HandleStripeWebhook(context.Background(), body, header); ack.Status != 200 || !ack.Applied {
		t.Fatalf("a delivery signed with both the old and the current secret was rejected: %+v", ack)
	}
}

// TestStaleStripeWebhookIsRejectedAsAPossibleReplay is task 4.1's replay-window scenario.
func TestStaleStripeWebhookIsRejectedAsAPossibleReplay(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()

	// Correctly signed, but signed an hour ago: a captured payload replayed later.
	stale := h.now.Add(-time.Hour)
	body, _ := json.Marshal(map[string]string{"id": "evt_replay", "type": string(WebhookInvoicePaid), "customer_id": "cus_acme"})
	header := StripeSignatureFor(webhookSecret, stale, body)

	ack := h.svc.HandleStripeWebhook(ctx, body, header)
	if ack.Status != 400 {
		t.Fatalf("a stale delivery returned %d, want 400", ack.Status)
	}
	if ack.Applied || h.deliveries.Count() != 0 {
		t.Error("a stale delivery moved state — the replay window is not bounding anything")
	}
	if !strings.Contains(ack.Reason, "window") {
		t.Errorf("the refusal does not name the replay window: %q", ack.Reason)
	}
}

// TestRedeliveredStripeEventAppliesNothingAndReturns2xx is task 4.2 / FR14.
func TestRedeliveredStripeEventAppliesNothingAndReturns2xx(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()
	body, sig := h.stripeDelivery("evt_paid_1", WebhookInvoicePaid, "cus_acme", map[string]string{"invoice_ref": "in_9"})

	first := h.svc.HandleStripeWebhook(ctx, body, sig)
	if first.Status != 200 || !first.Applied || first.Duplicate {
		t.Fatalf("first delivery: %+v", first)
	}

	for i := 0; i < 4; i++ {
		again := h.svc.HandleStripeWebhook(ctx, body, sig)
		if again.Status != 200 {
			t.Errorf("redelivery %d returned %d — Stripe would keep retrying an event that already applied", i, again.Status)
		}
		if !again.Duplicate {
			t.Errorf("redelivery %d was not recognized as a duplicate", i)
		}
		if again.Applied {
			t.Errorf("redelivery %d APPLIED the effect a second time", i)
		}
	}
	if n := h.deliveries.Count(); n != 1 {
		t.Errorf("%d dedupe rows for one event id, want 1", n)
	}
}

// TestPersistThenAck is task 4.3 — load-bearing.
//
// An HTTP 200 to Stripe is a promise that the event was recorded. Stripe stops retrying an event it
// thinks succeeded, so a handler that acks before it persists converts a redelivery into a LOST event
// that never comes back. This asserts the whole of that sentence: non-2xx on failure, the claim
// released so the retry is not deduped into nothing, and the retry actually applying.
func TestPersistThenAck(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()
	body, sig := h.stripeDelivery("evt_paid_2", WebhookInvoicePaid, "cus_acme", map[string]string{"invoice_ref": "in_10"})

	// The effect cannot be persisted.
	h.states.SetDown(true)
	ack := h.svc.HandleStripeWebhook(ctx, body, sig)
	if ack.Status == 200 {
		t.Fatal("🔴 the endpoint ACKED an event it did not record — Stripe will never send it again")
	}
	if ack.Status < 500 {
		t.Errorf("status = %d, want 5xx — a persistence failure must ASK for the retry, and a 4xx tells "+
			"Stripe the event is permanently unprocessable", ack.Status)
	}
	if ack.Applied {
		t.Error("the ack claims the effect was applied")
	}
	if st := h.svc.BillingState("cus_acme"); st.InvoiceStatus != "" {
		t.Errorf("state moved despite the persistence failure: %+v", st)
	}

	// 🔴 The claim was RELEASED. Without this the retry below would be deduped into a no-op, and the
	// event would be acked-but-unrecorded by the slower route.
	if h.deliveries.Seen("evt_paid_2") {
		t.Fatal("the dedupe claim survived a failed effect — Stripe's retry would apply nothing, which is " +
			"the acked-but-unrecorded failure arriving one delivery later")
	}

	// Stripe retries. Now it persists, and the event is not lost.
	h.states.SetDown(false)
	retry := h.svc.HandleStripeWebhook(ctx, body, sig)
	if retry.Status != 200 || !retry.Applied {
		t.Fatalf("the retry after recovery did not apply: %+v", retry)
	}
	if h.svc.BillingState("cus_acme").InvoiceStatus != "paid" {
		t.Error("the retried event did not reach the mirrored state")
	}

	// And no acked-but-unrecorded event exists: every 2xx this endpoint returned has a dedupe row.
	if n := h.deliveries.Count(); n != 1 {
		t.Errorf("%d dedupe rows, want exactly 1 for the one acked event", n)
	}
}

// TestPersistThenAckReportsAnIrreconcilableGapRatherThanSwallowingIt covers the one failure a retry
// cannot fix: the effect did not persist AND the claim could not be released.
func TestPersistThenAckReportsAnIrreconcilableGapRatherThanSwallowingIt(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()
	body, sig := h.stripeDelivery("evt_gap", WebhookInvoicePaid, "cus_acme", nil)

	// The claim succeeds, the effect fails, and by then the delivery store is gone too.
	h.states.SetDown(true)
	failingRelease := &releaseFailingDeliveries{MemDeliveries: h.deliveries}
	h.svc.WithDeliveries(failingRelease)

	ack := h.svc.HandleStripeWebhook(ctx, body, sig)
	if ack.Status < 500 {
		t.Fatalf("status = %d, want 5xx", ack.Status)
	}
	// The reason must say RECONCILE rather than implying a retry will fix it, because it will not: the
	// claim is standing and the redelivery would be deduped into nothing.
	if !strings.Contains(ack.Reason, "RECONCILED") {
		t.Errorf("an irreconcilable gap was reported as an ordinary retry: %q", ack.Reason)
	}
}

// releaseFailingDeliveries claims normally and cannot release — the second half of the double failure.
type releaseFailingDeliveries struct{ *MemDeliveries }

func (d *releaseFailingDeliveries) Release(string) error {
	return errors.New("the delivery store went away")
}

// TestSubscriptionLifecycleIsMirroredVerbatim is task 4.4 / the mirroring requirement.
//
// The platform reflects Stripe's words. It does not translate them into its own vocabulary, because a
// state the provider owns and the platform renames is a state two systems will eventually disagree
// about — and the disagreement surfaces as a customer being told they are fine while Stripe is dunning
// them.
func TestSubscriptionLifecycleIsMirroredVerbatim(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()

	steps := []struct {
		id     string
		typ    WebhookType
		extra  map[string]string
		assert func(BillingState) string
	}{
		{"evt_a", WebhookInvoiceFinalized, map[string]string{"invoice_ref": "in_1"}, func(st BillingState) string {
			if st.InvoiceStatus != "open" || st.LastInvoiceRef != "in_1" {
				return "finalized did not mirror open/in_1"
			}
			return ""
		}},
		{"evt_b", WebhookInvoicePaymentFailed, map[string]string{"invoice_ref": "in_1"}, func(st BillingState) string {
			if !st.PaymentFailed || st.InvoiceStatus != "payment_failed" {
				return "payment_failed did not mirror"
			}
			return ""
		}},
		{"evt_c", WebhookSubscriptionPastDue, nil, func(st BillingState) string {
			if !st.PastDue || st.SubscriptionStatus != "past_due" {
				return "past_due did not mirror"
			}
			return ""
		}},
		{"evt_d", WebhookInvoicePaid, map[string]string{"invoice_ref": "in_1"}, func(st BillingState) string {
			if st.InvoiceStatus != "paid" || st.PaymentFailed || st.PastDue {
				return "paid did not clear the dunning flags"
			}
			return ""
		}},
		{"evt_e", WebhookSubscriptionCanceled, nil, func(st BillingState) string {
			if st.SubscriptionStatus != "canceled" {
				return "canceled did not mirror"
			}
			return ""
		}},
	}
	for _, s := range steps {
		body, sig := h.stripeDelivery(s.id, s.typ, "cus_acme", s.extra)
		if ack := h.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 || !ack.Applied {
			t.Fatalf("%s: %+v", s.typ, ack)
		}
		if msg := s.assert(h.svc.BillingState("cus_acme")); msg != "" {
			t.Errorf("%s: %s (state %+v)", s.typ, msg, h.svc.BillingState("cus_acme"))
		}
	}

	// An UNKNOWN event type is acknowledged and changes nothing. A provider adding an event must not
	// start failing the endpoint, and guessing at an unknown event is worse than ignoring it.
	before := h.svc.BillingState("cus_acme")
	body, sig := h.stripeDelivery("evt_new", WebhookType("customer.subscription.paused"), "cus_acme", nil)
	ack := h.svc.HandleStripeWebhook(ctx, body, sig)
	if ack.Status != 200 {
		t.Errorf("an unknown event type returned %d — a new Stripe event must not break the endpoint", ack.Status)
	}
	after := h.svc.BillingState("cus_acme")
	if after.InvoiceStatus != before.InvoiceStatus || after.SubscriptionStatus != before.SubscriptionStatus {
		t.Errorf("an unknown event type was GUESSED at: %+v -> %+v", before, after)
	}
}

// TestRefundAndDisputeWebhooksAuthorNoLedgerRow is task 4.4's preserved P7 rule.
//
// A webhook is a notification, not an authorization to write the billing ledger. The money movement is
// recorded through the audited Credit/Refund path, where it carries a reason and names what it corrects.
func TestRefundAndDisputeWebhooksAuthorNoLedgerRow(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()
	before := len(testEvents(h.svc.Ledger(), "cus_acme", ""))

	for _, typ := range []WebhookType{WebhookChargeRefunded, WebhookChargeDisputeCreated} {
		body, sig := h.stripeDelivery("evt_"+string(typ), typ, "cus_acme", map[string]string{"charge_ref": "ch_1"})
		if ack := h.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
			t.Fatalf("%s: %+v", typ, ack)
		}
	}

	if after := len(testEvents(h.svc.Ledger(), "cus_acme", "")); after != before {
		t.Errorf("a refund/dispute webhook authored %d ledger row(s) — a webhook may notify, never write "+
			"the billing ledger", after-before)
	}
}

// TestAWebhookWithNoEventIDIsRefused: without the provider's event id the delivery cannot be deduped,
// so it cannot be processed exactly once — and processing it anyway would be processing it an unknown
// number of times.
func TestAWebhookWithNoEventIDIsRefused(t *testing.T) {
	h := newWebhookHarness(t)
	body, _ := json.Marshal(map[string]string{"type": string(WebhookInvoicePaid), "customer_id": "cus_acme"})
	sig := StripeSignatureFor(webhookSecret, h.now, body)

	ack := h.svc.HandleStripeWebhook(context.Background(), body, sig)
	if ack.Status != 400 {
		t.Fatalf("status = %d, want 400", ack.Status)
	}
	if h.deliveries.Count() != 0 || h.svc.BillingState("cus_acme").CustomerID != "" {
		t.Error("an un-dedupable delivery moved state")
	}
}

// TestWebhookAckLeaksNothing: a rejected webhook's diagnostics must not become a second leak.
func TestWebhookAckLeaksNothing(t *testing.T) {
	h := newWebhookHarness(t)
	body, sig := h.stripeDelivery("evt_leak", WebhookInvoicePaid, "cus_acme", nil)

	for _, ack := range []WebhookAck{
		h.svc.HandleStripeWebhook(context.Background(), body, sig),
		h.svc.HandleStripeWebhook(context.Background(), body, "t=1,v1=deadbeef"),
	} {
		blob, _ := json.Marshal(ack)
		for _, forbidden := range []string{webhookSecret, sig, string(body)} {
			if forbidden != "" && strings.Contains(string(blob), forbidden) {
				t.Errorf("the ack carries material it must not: %s", blob)
			}
		}
	}
}

// unixOf is the timestamp encoding Stripe signs with.
func unixOf(t time.Time) string { return strconv.FormatInt(t.UTC().Unix(), 10) }

// TestRealCheckoutInvoicePaidGrantsThePlanFromTheSubscriptionsMetadata is the defect a real self-serve
// checkout exposed, written against the payload Stripe actually sent.
//
// 🔴 The shape is the whole point, so it is reproduced rather than simplified: on a real `invoice.paid`
// for a subscription CHECKOUT created, the invoice carries `metadata: {}` and NO top-level
// `subscription`. The plan the customer just paid for exists only at
// `parent.subscription_details.metadata`. Reading the flat fields alone yields no plan and no
// subscription ref, the entitlement sync has nothing to act on, and the customer is charged without
// being granted anything — silently, with a 200 on the webhook and an "applied" in the log.
//
// The demo could never have caught this: it authors P7-flat events that carry `plan_id` directly.
func TestRealCheckoutInvoicePaidGrantsThePlanFromTheSubscriptionsMetadata(t *testing.T) {
	h := newHarness(t, "free")

	// Verbatim shape of a Stripe 2025-03-31.basil invoice.paid for a Checkout-created subscription.
	body := []byte(`{
	  "id": "evt_real_checkout",
	  "type": "invoice.paid",
	  "data": { "object": {
	    "id": "in_real",
	    "object": "invoice",
	    "customer": "cus_stripe_handle",
	    "status": "paid",
	    "metadata": {},
	    "parent": {
	      "type": "subscription_details",
	      "subscription_details": {
	        "subscription": "sub_real",
	        "metadata": {
	          "platform_customer_id": "cus_acme",
	          "platform_plan_id": "business"
	        }
	      }
	    }
	  }}
	}`)

	p, err := h.svc.decodeWebhook(body)
	if err != nil {
		t.Fatalf("decodeWebhook: %v", err)
	}
	if p.PlanID != "business" {
		t.Errorf("PlanID = %q, want \"business\" — read from parent.subscription_details.metadata, "+
			"because a real invoice carries none of its own", p.PlanID)
	}
	if p.SubscriptionRef != "sub_real" {
		t.Errorf("SubscriptionRef = %q, want \"sub_real\" — the flat `subscription` field is gone in this API version", p.SubscriptionRef)
	}
	if p.CustomerID != "cus_acme" {
		t.Errorf("CustomerID = %q, want \"cus_acme\" — resolvable without an account-store lookup because "+
			"the platform stamped it on the subscription at checkout", p.CustomerID)
	}
}

// TestSubscriptionCreatedMirrorsStatusAndGrantsThePlan is the second half of the self-serve gap.
//
// Stripe sends `customer.subscription.created` for a subscription Checkout just made, and does NOT
// follow it with an `.updated`. A platform handling only `.updated` mirrors no status for exactly the
// customers who have just paid — so the console's provider-status chip, and every unhappy state derived
// from it, has nothing to render from until something unrelated happens to that subscription.
func TestSubscriptionCreatedMirrorsStatusAndGrantsThePlan(t *testing.T) {
	h := newHarness(t, "free")

	body := []byte(`{
	  "id": "evt_sub_created",
	  "type": "customer.subscription.created",
	  "data": { "object": {
	    "id": "sub_real",
	    "object": "subscription",
	    "customer": "cus_stripe_handle",
	    "status": "active",
	    "metadata": { "platform_customer_id": "cus_acme", "platform_plan_id": "business" }
	  }}
	}`)

	p, err := h.svc.decodeWebhook(body)
	if err != nil {
		t.Fatalf("decodeWebhook: %v", err)
	}
	if p.Type != WebhookSubscriptionCreated {
		t.Fatalf("type = %q", p.Type)
	}
	st, err := h.svc.applyWebhook(p)
	if err != nil {
		t.Fatalf("applyWebhook: %v", err)
	}
	// 🔴 The provider's own word, carried verbatim. Empty here is the defect: a paid customer whose
	// status the console cannot show.
	if st.SubscriptionStatus != "active" {
		t.Errorf("mirrored SubscriptionStatus = %q, want \"active\" — a brand-new paid subscription "+
			"leaves the console with nothing to render", st.SubscriptionStatus)
	}
	if st.PastDue {
		t.Error("a newly created active subscription must not mirror as past due")
	}
}

// TestCheckoutCreatedSubscriptionIsRememberedSoTheNextChangeRepointsIt is the double-billing defect.
//
// 🔴 The failure it fences is not a wrong number on a screen — it is a customer paying twice. In the
// real self-serve flow the platform does not create the subscription; Checkout does, on Stripe's page.
// If the platform does not capture the reference from the lifecycle event, the next plan change finds
// no subscription to repoint, correctly concludes "this must be a checkout", and starts another one —
// leaving two ACTIVE subscriptions on the customer, each individually correct.
func TestCheckoutCreatedSubscriptionIsRememberedSoTheNextChangeRepointsIt(t *testing.T) {
	h := newHarness(t, "free")

	// Precondition: the platform created nothing, so it knows of no subscription.
	if ref := h.svc.SubscriptionRef("cus_acme"); ref != "" {
		t.Fatalf("precondition: expected no subscription ref, got %q", ref)
	}

	body := []byte(`{
	  "id": "evt_checkout_sub",
	  "type": "customer.subscription.created",
	  "data": { "object": {
	    "id": "sub_from_checkout",
	    "object": "subscription",
	    "customer": "cus_stripe_handle",
	    "status": "active",
	    "metadata": { "platform_customer_id": "cus_acme", "platform_plan_id": "business" }
	  }}
	}`)
	p, err := h.svc.decodeWebhook(body)
	if err != nil {
		t.Fatalf("decodeWebhook: %v", err)
	}
	if _, err := h.svc.applyWebhook(p); err != nil {
		t.Fatalf("applyWebhook: %v", err)
	}

	if ref := h.svc.SubscriptionRef("cus_acme"); ref != "sub_from_checkout" {
		t.Fatalf("SubscriptionRef = %q, want \"sub_from_checkout\" — without it the next upgrade opens a "+
			"second checkout and the customer ends up subscribed twice", ref)
	}
}
