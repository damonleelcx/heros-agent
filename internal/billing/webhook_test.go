package billing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
)

const testWebhookSecret = "whsec_test_signing_secret"

// signed builds a correctly-signed delivery for a payload — the shape the provider actually sends.
func signed(t *testing.T, p WebhookPayload, at time.Time) SignedWebhook {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stamp := at.UTC().Format(time.RFC3339)
	return SignedWebhook{Body: body, Timestamp: stamp, Signature: SignWebhook(testWebhookSecret, stamp, body)}
}

func withWebhooks(t *testing.T) (*harness, *MemDeliveries) {
	t.Helper()
	h := newHarness(t, "team")
	d := NewMemDeliveries()
	h.svc.WithDeliveries(d)
	return h, d
}

// TestRedeliveredWebhookIsProcessedOnce is task 4.1 / FR14: the provider redelivers a webhook the
// platform already processed; it must be recognized as a duplicate and produce NO second side effect.
func TestRedeliveredWebhookIsProcessedOnce(t *testing.T) {
	h, deliveries := withWebhooks(t)
	ctx := context.Background()

	p := WebhookPayload{ProviderEventID: "evt_001", Type: WebhookInvoicePaymentFailed,
		CustomerID: "cus_acme", Period: july.ID, InvoiceRef: "prov_inv_1"}
	in := signed(t, p, clockNow)

	first, err := h.svc.HandleWebhook(ctx, in)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if !first.Applied || first.Duplicate {
		t.Fatalf("first delivery must apply: %+v", first)
	}
	if !first.State.PaymentFailed || first.State.InvoiceStatus != "payment_failed" {
		t.Fatalf("the side effect did not happen: %+v", first.State)
	}

	// Five redeliveries of the byte-identical payload.
	for i := 0; i < 5; i++ {
		again, err := h.svc.HandleWebhook(ctx, in)
		if err != nil {
			t.Fatalf("redelivery %d: %v", i, err)
		}
		if !again.Duplicate {
			t.Errorf("redelivery %d was not recognized as a duplicate", i)
		}
		if again.Applied {
			t.Errorf("redelivery %d applied a SECOND side effect", i)
		}
	}
	if deliveries.Count() != 1 {
		t.Errorf("webhook_delivery rows = %d after 6 deliveries, want 1", deliveries.Count())
	}

	// Downstream read path: the mirrored state is the provider's, unchanged by the redeliveries.
	st := h.svc.BillingState("cus_acme")
	if !st.PaymentFailed || st.InvoiceStatus != "payment_failed" {
		t.Errorf("mirrored state drifted across redeliveries: %+v", st)
	}
}

// TestUnsignedWebhookIsRejectedBeforeAnySideEffect is task 9.6 / the billing spec's unsigned-webhook
// scenario. It asserts the REJECTION and, just as importantly, that NOTHING moved: no delivery row, no
// state change, no ledger row.
func TestUnsignedWebhookIsRejectedBeforeAnySideEffect(t *testing.T) {
	h, deliveries := withWebhooks(t)
	ctx := context.Background()

	p := WebhookPayload{ProviderEventID: "evt_forged", Type: WebhookInvoicePaymentFailed,
		CustomerID: "cus_acme", Period: july.ID}
	good := signed(t, p, clockNow)

	cases := map[string]struct {
		in   SignedWebhook
		want error
	}{
		"no signature at all": {SignedWebhook{Body: good.Body, Timestamp: good.Timestamp}, ErrNoSignature},
		"wrong signature": {SignedWebhook{Body: good.Body, Timestamp: good.Timestamp,
			Signature: "v1=deadbeef"}, ErrBadSignature},
		"signature for a DIFFERENT body": {func() SignedWebhook {
			other := signed(t, WebhookPayload{ProviderEventID: "evt_other", Type: WebhookInvoicePaid,
				CustomerID: "cus_acme"}, clockNow)
			return SignedWebhook{Body: good.Body, Timestamp: good.Timestamp, Signature: other.Signature}
		}(), ErrBadSignature},
		"tampered body, original signature": {func() SignedWebhook {
			tampered := append(append([]byte(nil), good.Body...), ' ')
			return SignedWebhook{Body: tampered, Timestamp: good.Timestamp, Signature: good.Signature}
		}(), ErrBadSignature},
		"replayed old delivery": {func() SignedWebhook {
			old := clockNow.Add(-2 * time.Hour)
			return signed(t, p, old)
		}(), ErrWebhookStaleStamp},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := h.svc.HandleWebhook(ctx, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v (result %+v)", tc.want, err, res)
			}
			if deliveries.Count() != 0 {
				t.Errorf("a rejected webhook was recorded as a delivery")
			}
			if st := h.svc.BillingState("cus_acme"); st.PaymentFailed || st.InvoiceStatus != "" {
				t.Errorf("a rejected webhook moved state: %+v", st)
			}
			if len(h.ledger.Events("cus_acme", "")) != 0 {
				t.Errorf("a rejected webhook wrote to the billing ledger")
			}
		})
	}

	// And the well-formed one still works, so the rejections above are about the signature and not
	// about the handler being broken.
	if _, err := h.svc.HandleWebhook(ctx, good); err != nil {
		t.Fatalf("the correctly-signed delivery was rejected too: %v", err)
	}
}

// TestWebhookWithNoDedupeKeyIsRefused: a delivery with no provider event id cannot be deduped, so it
// cannot be processed exactly once — and "process it anyway" would mean processing every redelivery.
func TestWebhookWithNoDedupeKeyIsRefused(t *testing.T) {
	h, deliveries := withWebhooks(t)
	in := signed(t, WebhookPayload{Type: WebhookInvoicePaid, CustomerID: "cus_acme"}, clockNow)
	if _, err := h.svc.HandleWebhook(context.Background(), in); !errors.Is(err, ErrWebhookNoEventID) {
		t.Errorf("want ErrWebhookNoEventID, got %v", err)
	}
	if deliveries.Count() != 0 {
		t.Error("the refused delivery was recorded")
	}
}

// TestWebhooksWithoutADeliveryStoreAreRefused: rather than processing un-deduped, a service with no
// dedupe table refuses. Silently accepting them would make every provider retry a fresh side effect.
func TestWebhooksWithoutADeliveryStoreAreRefused(t *testing.T) {
	h := newHarness(t, "team") // no WithDeliveries
	in := signed(t, WebhookPayload{ProviderEventID: "evt_x", Type: WebhookInvoicePaid, CustomerID: "cus_acme"}, clockNow)
	if _, err := h.svc.HandleWebhook(context.Background(), in); err == nil {
		t.Error("a service with no webhook dedupe table processed a webhook anyway")
	}
}

// TestWebhookNeverWritesTheBillingLedger: a webhook is a NOTIFICATION, not an authorization to write a
// charge. A refund webhook mirrors provider state; the money movement is recorded through the audited
// Credit/Refund path only.
func TestWebhookNeverWritesTheBillingLedger(t *testing.T) {
	h, _ := withWebhooks(t)
	ctx := context.Background()

	for i, typ := range []WebhookType{WebhookChargeRefunded, WebhookChargeDisputeCreated,
		WebhookInvoicePaid, WebhookSubscriptionPastDue, WebhookType("some.future.event")} {
		in := signed(t, WebhookPayload{ProviderEventID: "evt_led_" + string(rune('a'+i)), Type: typ,
			CustomerID: "cus_acme", Period: july.ID, ChargeRef: "prov_ch_1"}, clockNow)
		if _, err := h.svc.HandleWebhook(ctx, in); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if got := len(h.ledger.Events("cus_acme", "")); got != 0 {
		t.Errorf("webhooks wrote %d billing-ledger rows; a notification must not author a charge", got)
	}

	// An unknown type is acknowledged, not guessed at: state is untouched by it.
	st := h.svc.BillingState("cus_acme")
	if st.SubscriptionStatus != "past_due" {
		t.Errorf("the known events did not mirror provider state: %+v", st)
	}
}

// TestDunningStateIsMirroredNotComputed: the platform carries the PROVIDER's words. paid clears the
// failure; past_due sets it. Nothing here recomputes what the customer owes.
func TestDunningStateIsMirroredNotComputed(t *testing.T) {
	h, _ := withWebhooks(t)
	ctx := context.Background()

	seq := []struct {
		id            string
		typ           WebhookType
		wantFailed    bool
		wantPastDue   bool
		wantInvoiceSt string
	}{
		{"evt_d1", WebhookInvoiceFinalized, false, false, "open"},
		{"evt_d2", WebhookInvoicePaymentFailed, true, false, "payment_failed"},
		{"evt_d3", WebhookSubscriptionPastDue, true, true, "payment_failed"},
		{"evt_d4", WebhookInvoicePaid, false, false, "paid"},
	}
	for _, step := range seq {
		in := signed(t, WebhookPayload{ProviderEventID: step.id, Type: step.typ,
			CustomerID: "cus_acme", Period: july.ID, InvoiceRef: "prov_inv_1"}, clockNow)
		res, err := h.svc.HandleWebhook(ctx, in)
		if err != nil {
			t.Fatalf("%s: %v", step.typ, err)
		}
		if res.State.PaymentFailed != step.wantFailed || res.State.PastDue != step.wantPastDue ||
			res.State.InvoiceStatus != step.wantInvoiceSt {
			t.Errorf("after %s: %+v, want failed=%v pastDue=%v invoice=%q",
				step.typ, res.State, step.wantFailed, step.wantPastDue, step.wantInvoiceSt)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reversibility (task 4.4 / FR11)
// ─────────────────────────────────────────────────────────────────────────────

// TestWrongChargeIsCorrectedByCreditWithNoDataLoss is task 4.4, the load-bearing reversibility test.
//
// It injects a wrong charge, corrects it with a credit, and then asserts the three things FR11
// actually requires: the ORIGINALS are intact (not deleted, not mutated), the correction is a NEW
// audited row, and the net effect is right.
func TestWrongChargeIsCorrectedByCreditWithNoDataLoss(t *testing.T) {
	h, _ := withWebhooks(t)
	ctx := context.Background()

	// The wrong charge.
	wrong, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM)))
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	usageBefore, err := h.usage.Get(metering.Key{CustomerID: "cus_acme", Period: july.ID, Metric: metering.MetricSUM})
	if err != nil {
		t.Fatalf("usage: %v", err)
	}

	// The correction.
	const reason = "billed the un-reconciled period before the late cost events landed"
	credit, err := h.svc.Credit(ctx, "cus_acme", wrong.EventID, reason)
	if err != nil {
		t.Fatalf("credit: %v", err)
	}

	// (1) The correction is a NEW audited row that names what it corrects and why.
	if credit.EventID == wrong.EventID {
		t.Fatal("the correction reused the original row — a correction must be a new row")
	}
	if credit.Type != TypeCredit {
		t.Errorf("correction type = %q, want credit", credit.Type)
	}
	if credit.CausedBy != "billing_event:"+wrong.EventID {
		t.Errorf("the credit does not name the charge it corrects: %q", credit.CausedBy)
	}
	if credit.Reason != reason {
		t.Errorf("the credit lost its reason: %q", credit.Reason)
	}
	if credit.Status != StatusRecorded || credit.ProviderRef == "" {
		t.Errorf("the credit was not settled with the provider: %+v", credit)
	}

	// (2) The ORIGINALS are intact — the charge row, and the usage that justified it.
	orig, err := h.svc.findEvent("cus_acme", wrong.EventID)
	if err != nil {
		t.Fatalf("the original charge is GONE: %v", err)
	}
	if orig.Quantity != wrong.Quantity || orig.ProviderRef != wrong.ProviderRef ||
		orig.Status != wrong.Status || orig.CausedBy != wrong.CausedBy || orig.Kind != wrong.Kind {
		t.Errorf("the original charge was mutated by the correction:\n got %+v\nwant %+v", orig, wrong)
	}
	usageAfter, err := h.usage.Get(usageBefore.Key())
	if err != nil {
		t.Fatalf("the usage record is GONE: %v", err)
	}
	if usageAfter != usageBefore {
		t.Errorf("the usage record was mutated by the correction:\n got %+v\nwant %+v", usageAfter, usageBefore)
	}

	// (3) The net is right: the period holds exactly one charge and one offsetting credit for the same
	// quantity, and the ledger replays to that.
	rows := h.ledger.Events("cus_acme", july.ID)
	var charges, credits int
	for _, r := range rows {
		switch r.Type {
		case TypeCharge, TypeGainshareCharge:
			charges++
		case TypeCredit, TypeRefund:
			credits++
		}
	}
	if charges != 1 || credits != 1 {
		t.Errorf("period ledger = %d charges + %d credits, want 1 and 1", charges, credits)
	}
	if credit.Quantity != wrong.Quantity {
		t.Errorf("the credit's quantity (%v) does not offset the charge (%v)", credit.Quantity, wrong.Quantity)
	}

	// The provider kept BOTH too — the correction is additive on its side as well.
	inv, err := h.provider.Invoice(ctx, "cus_acme", july.ID)
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	var sawCharge, sawCredit bool
	for _, l := range inv.Lines {
		if l.Kind == LineMetered {
			sawCharge = true
		}
		if l.Kind == LineCredit {
			sawCredit = true
		}
	}
	if !sawCharge || !sawCredit {
		t.Errorf("the invoice does not show both the charge and its credit: %+v", inv.Lines)
	}

	// A retried correction is recognized, not doubled.
	again, err := h.svc.Credit(ctx, "cus_acme", wrong.EventID, reason)
	if err != nil {
		t.Fatalf("retried credit: %v", err)
	}
	if again.EventID != credit.EventID {
		t.Errorf("a retried identical correction created a SECOND credit (%s vs %s)", again.EventID, credit.EventID)
	}
}

// TestTheAuditTrailReconstructsThePeriod is the billing spec's audit scenario: replaying the events
// reconstructs what was charged, when, and why — after corrections — with nothing overwritten.
func TestTheAuditTrailReconstructsThePeriod(t *testing.T) {
	h, _ := withWebhooks(t)
	ctx := context.Background()

	if _, err := h.svc.StartSubscription(ctx, "cus_acme"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	sub, err := h.svc.Charge(ctx, "cus_acme", july, KindSubscription,
		SubscriptionChargeIdempotencyKey("cus_acme", july.ID, "team"))
	if err != nil {
		t.Fatalf("subscription charge: %v", err)
	}
	met, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM)))
	if err != nil {
		t.Fatalf("metered charge: %v", err)
	}
	if _, err := h.svc.Refund(ctx, "cus_acme", met.EventID, "duplicate metered charge reported by finance"); err != nil {
		t.Fatalf("refund: %v", err)
	}

	rows := h.ledger.Events("cus_acme", july.ID)
	if len(rows) != 3 {
		t.Fatalf("period rows = %d, want 3 (subscription charge, metered charge, refund)", len(rows))
	}
	for i, r := range rows {
		if r.CausedBy == "" {
			t.Errorf("row %d (%s) names no cause — the period cannot be reconstructed from it", i, r.Type)
		}
		if r.Type.ChargeBearing() && r.Status != StatusRecorded {
			t.Errorf("row %d (%s) is charge-bearing but unsettled: %+v", i, r.Type, r)
		}
	}
	if rows[0].EventID != sub.EventID || rows[1].EventID != met.EventID {
		t.Errorf("the ledger is not in append order: %s, %s", rows[0].EventID, rows[1].EventID)
	}
	if rows[2].Type != TypeRefund || rows[2].CausedBy != "billing_event:"+met.EventID {
		t.Errorf("the refund does not point at what it corrects: %+v", rows[2])
	}
}

// TestCorrectionsRefuseUnsoundInput: each of these would produce a credit nobody can reconcile.
func TestCorrectionsRefuseUnsoundInput(t *testing.T) {
	h, _ := withWebhooks(t)
	ctx := context.Background()
	ch, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM)))
	if err != nil {
		t.Fatalf("charge: %v", err)
	}

	if _, err := h.svc.Credit(ctx, "cus_acme", ch.EventID, "   "); !errors.Is(err, ErrNoReason) {
		t.Errorf("a credit with no reason was accepted: %v", err)
	}
	if _, err := h.svc.Credit(ctx, "cus_acme", "", "some reason"); err == nil {
		t.Error("a credit naming no charge was accepted")
	}
	if _, err := h.svc.Credit(ctx, "cus_acme", "be_nonexistent", "some reason"); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("a credit against an unknown charge was accepted: %v", err)
	}

	// Crediting an UNSETTLED charge is refused: an unsettled charge is resolved by not settling it.
	h.provider.SetDown(true)
	buffered, _ := h.svc.Charge(ctx, "cus_acme", july, KindSubscription,
		SubscriptionChargeIdempotencyKey("cus_acme", july.ID, "team"))
	h.provider.SetDown(false)
	if buffered.Status != StatusPending {
		t.Fatalf("precondition: expected a buffered charge, got %+v", buffered)
	}
	if _, err := h.svc.Credit(ctx, "cus_acme", buffered.EventID, "reason"); err == nil {
		t.Error("an unsettled charge was credited")
	}
}
