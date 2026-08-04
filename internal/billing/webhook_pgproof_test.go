//go:build pgproof

// Live-Postgres proof for the two DURABLE webhook stores — the dedupe table and the state mirror.
//
// # Why the in-memory tests do not cover any of this
//
// `MemDeliveries.Claim` holds a mutex across check-and-insert, which is genuinely the same atomicity a
// PRIMARY KEY provides — inside one process. Everything that makes the durable store different is
// outside that boundary: a claim must survive a restart (Stripe redelivers for days), and two pods must
// not both win one delivery. Neither is expressible against a map, so asserting them there asserts a
// property of the map.
//
// The state mirror is stronger still: every guarantee it has is a CHECK in migration 0033, and a map
// refuses nothing.
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/billing/
package billing

import (
	"strings"
	"testing"
	"time"
)

// TestClaimIsWonExactlyOnce: the PRIMARY KEY, not the code, is what makes a redelivery a no-op.
//
// This is THE property the endpoint's exactly-once claim rests on, and the reason a delivery store is
// required before a webhook may be processed at all.
func TestClaimIsWonExactlyOnce(t *testing.T) {
	db := durableDB(t, "billing_webhook_claim")
	d, err := NewPGDeliveries(db)
	if err != nil {
		t.Fatalf("NewPGDeliveries: %v", err)
	}
	at := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	won, err := d.Claim("evt_1", WebhookInvoicePaid, at)
	if err != nil || !won {
		t.Fatalf("first claim: won=%v err=%v — want won, no error", won, err)
	}
	// The redelivery. Stripe sends this for days after the first attempt, and across restarts.
	won, err = d.Claim("evt_1", WebhookInvoicePaid, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("redelivery claim errored: %v — an error is NOT a lost claim and must not read as one", err)
	}
	if won {
		t.Fatal("the redelivery WON the claim — the effect would be applied twice")
	}
	if !d.Seen("evt_1") || d.Count() != 1 {
		t.Fatalf("seen=%v count=%d — want one recorded delivery", d.Seen("evt_1"), d.Count())
	}
}

// TestReleaseReopensAClaimWhoseEffectNeverLanded: the persist-then-ack gap, closed.
//
// The claim is taken BEFORE the effect is persisted — it has to be, or two concurrent redeliveries both
// proceed. That ordering opens exactly one gap: claim persists, effect does not, and the retry then
// finds the claim and applies nothing. This is the proof that releasing closes it.
func TestReleaseReopensAClaimWhoseEffectNeverLanded(t *testing.T) {
	db := durableDB(t, "billing_webhook_release")
	d, _ := NewPGDeliveries(db)
	at := time.Now().UTC()

	if won, _ := d.Claim("evt_2", WebhookCheckoutCompleted, at); !won {
		t.Fatal("first claim should be won")
	}
	if err := d.Release("evt_2"); err != nil {
		t.Fatalf("release: %v", err)
	}
	won, err := d.Claim("evt_2", WebhookCheckoutCompleted, at)
	if err != nil || !won {
		t.Fatalf("the retry did NOT re-win the released claim (won=%v err=%v) — the effect would be lost", won, err)
	}
	// A double release must not re-open a delivery that DID succeed.
	if err := d.Release("evt_never_claimed"); err != nil {
		t.Fatalf("releasing an unclaimed delivery must be a no-op, got: %v", err)
	}
}

// TestAnEmptyEventIDIsRefusedRatherThanStored: a delivery with no id cannot be deduped, so it cannot be
// processed exactly once. The PRIMARY KEY's `CHECK (provider_event_id <> ”)` says so too; this proves
// the store refuses before reaching it, with the error that names the reason.
func TestAnEmptyEventIDIsRefusedRatherThanStored(t *testing.T) {
	db := durableDB(t, "billing_webhook_noid")
	d, _ := NewPGDeliveries(db)
	won, err := d.Claim("", WebhookInvoicePaid, time.Now())
	if won || err == nil {
		t.Fatalf("an empty event id was accepted (won=%v err=%v)", won, err)
	}
	if d.Count() != 0 {
		t.Fatalf("count=%d — nothing should have been stored", d.Count())
	}
}

// TestStateMirrorSurvivesAndUpserts: the mirror is per-customer current state, not a history, and a
// second webhook for the same customer replaces it rather than accumulating rows.
func TestStateMirrorSurvivesAndUpserts(t *testing.T) {
	db := durableDB(t, "billing_webhook_state")
	s, err := NewPGStates(db)
	if err != nil {
		t.Fatalf("NewPGStates: %v", err)
	}
	t0 := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)

	first := BillingState{
		CustomerID: "cus_s", InvoiceStatus: "open", SubscriptionStatus: "past_due",
		LastInvoiceRef: "in_123", PaymentFailed: true, PastDue: true,
		PaymentMethodPresent: true, PaymentMethodBrand: "visa", PaymentMethodLast4: "4242",
		UpdatedAt: t0,
	}
	if err := s.Put(first); err != nil {
		t.Fatalf("put: %v", err)
	}
	got := s.Get("cus_s")
	if !got.PaymentFailed || !got.PastDue || got.PaymentMethodLast4 != "4242" || got.SubscriptionStatus != "past_due" {
		t.Fatalf("mirrored state did not round-trip: %+v", got)
	}
	if !got.UpdatedAt.Equal(t0) {
		t.Fatalf("updated_at=%s want %s — a late webhook must not look newer than an on-time one", got.UpdatedAt, t0)
	}

	// The recovery event: payment succeeds. It must REPLACE, not accumulate.
	if err := s.Put(BillingState{
		CustomerID: "cus_s", InvoiceStatus: "paid", SubscriptionStatus: "active",
		LastInvoiceRef: "in_124", PaymentMethodPresent: true, PaymentMethodBrand: "visa",
		PaymentMethodLast4: "4242", UpdatedAt: t0.Add(time.Hour),
	}); err != nil {
		t.Fatalf("put recovery: %v", err)
	}
	got = s.Get("cus_s")
	if got.PaymentFailed || got.PastDue || got.InvoiceStatus != "paid" {
		t.Fatalf("the recovery did not clear the dunning state: %+v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM billing_state WHERE customer_id = 'cus_s'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("billing_state has %d rows for one customer — it is a mirror, not a history", n)
	}
}

// TestAnUnknownCustomerMirrorsAsZero: "never charged" and "no state" are the same answer, and it is the
// one that understates rather than invents a dunning state.
func TestAnUnknownCustomerMirrorsAsZero(t *testing.T) {
	db := durableDB(t, "billing_webhook_state_zero")
	s, _ := NewPGStates(db)
	got := s.Get("cus_nobody")
	if got != (BillingState{}) {
		t.Fatalf("an unknown customer returned %+v — want the zero value", got)
	}
}

// TestCardDisplayCannotOutliveTheCard proves 0033's CHECK is real AND that Put satisfies it by clearing
// the display fields rather than by failing the write.
//
// 🔴 Both halves matter. Failing the write would return a non-2xx for a webhook whose content is
// perfectly valid ("the card was removed"), and Stripe would retry it forever. Writing the brand anyway
// would render a card on file for a customer who has none, on the page where that claim matters most.
func TestCardDisplayCannotOutliveTheCard(t *testing.T) {
	db := durableDB(t, "billing_webhook_card")
	s, _ := NewPGStates(db)

	if err := s.Put(BillingState{
		CustomerID: "cus_c", PaymentMethodPresent: true,
		PaymentMethodBrand: "visa", PaymentMethodLast4: "4242", UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("put with card: %v", err)
	}
	// The card is removed. Brand and last4 arrive stale from the caller; Put must clear them.
	if err := s.Put(BillingState{
		CustomerID: "cus_c", PaymentMethodPresent: false,
		PaymentMethodBrand: "visa", PaymentMethodLast4: "4242", UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("put without card was REFUSED (%v) — a valid webhook would retry forever", err)
	}
	got := s.Get("cus_c")
	if got.PaymentMethodBrand != "" || got.PaymentMethodLast4 != "" {
		t.Fatalf("card display outlived the card: %+v", got)
	}

	// And the CHECK itself is real — the database refuses the row this code declines to write.
	_, err := db.Exec(`INSERT INTO billing_state (customer_id, payment_method_present, payment_method_brand)
	                   VALUES ('cus_direct', FALSE, 'visa')`)
	if err == nil {
		t.Fatal("0033's billing_state_card_display_needs_a_card did not fire — the constraint is not guarding")
	}
	if !strings.Contains(err.Error(), "billing_state_card_display_needs_a_card") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	// last4 is four digits or nothing, so the column cannot quietly become somewhere a longer number fits.
	_, err = db.Exec(`INSERT INTO billing_state (customer_id, payment_method_present, payment_method_last4)
	                  VALUES ('cus_long', TRUE, '4111111111111111')`)
	if err == nil || !strings.Contains(err.Error(), "billing_state_last4_is_four_digits") {
		t.Fatalf("a full card number fit in payment_method_last4: %v", err)
	}
}

// TestAnAuditRowIsRecordedWithoutAReceipt is migration 0034.
//
// 🔴 This is the defect that made a paying deployment unable to complete a self-serve upgrade, and no
// in-memory test can reach it: the rule is a CHECK, and `MemLedger` is a map. The P21 run against a REAL
// Stripe test account did not catch it either — that run is real in every respect except the ledger it
// writes to.
//
// The path: `invoice.paid` → mirror state → sync entitlement → append the audited `plan_change` row.
// 0013's biconditional demanded a provider receipt on every `recorded` row, an audit row has none
// because no money moved, so the append failed with 23514 and the webhook retried forever.
func TestAnAuditRowIsRecordedWithoutAReceipt(t *testing.T) {
	db := durableDB(t, "billing_audit_rows")
	seedAccount(t, db, "cus_audit")
	l, err := NewPGLedger(db)
	if err != nil {
		t.Fatalf("NewPGLedger: %v", err)
	}
	for _, typ := range []EventType{TypePlanChange, TypeSubscriptionChange} {
		ev := BillingEvent{
			CustomerID: "cus_audit", Type: typ, Status: StatusRecorded,
			IdempotencyKey: string(typ) + ":cus_audit:team:webhook:evt_1",
			CausedBy:       "webhook:evt_1", Reason: "upgrade: plan Free -> Team",
			CreatedAt: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
		}
		if _, err := l.Append(ev); err != nil {
			t.Fatalf("%s: a completed audit row with no provider receipt was REFUSED: %v", typ, err)
		}
	}

	// And the money rule is UNCHANGED — this is a repair, not a relaxation. A charge claiming to be
	// recorded without the provider's receipt must still be impossible.
	_, err = db.Exec(`INSERT INTO billing_event
	    (event_id, customer_id, period, type, kind, idempotency_key, caused_by, status)
	    VALUES ('be_bad','cus_audit','2026-08','charge','metered','k_bad','usage_record:x','recorded')`)
	if err == nil || !strings.Contains(err.Error(), "billing_event_settled_has_refs") {
		t.Fatalf("a 'recorded' CHARGE with no provider receipt was accepted — the money rule was relaxed: %v", err)
	}

	// Nor may an audit row carry a receipt it never had: provider_ref must keep meaning "the provider
	// acknowledged this" everywhere in the table.
	_, err = db.Exec(`INSERT INTO billing_event
	    (event_id, customer_id, period, type, kind, idempotency_key, caused_by, status, provider_ref, settled_at)
	    VALUES ('be_bad2','cus_audit','2026-08','plan_change',NULL,'k_bad2','webhook:x','recorded','pi_fake', now())`)
	if err == nil || !strings.Contains(err.Error(), "billing_event_settled_has_refs") {
		t.Fatalf("an audit row carried a provider receipt: %v", err)
	}
}
