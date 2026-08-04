package billing

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// webhook_pg.go is the durable half of the webhook path: the dedupe table and the mirrored provider
// state, over Postgres.
//
// # Why this is what unblocks collection, rather than a hardening pass
//
// `HandleWebhook` REFUSES outright when no delivery store is attached — "a webhook that cannot be
// deduped must not be processed" — and `internal/launch` attached none. So on every real deployment the
// endpoint was mounted, publicly reachable, and answered 400 to every delivery Stripe made. The shape of
// that failure is the bad one: checkout completes, the customer's card is charged, Stripe reports the
// subscription active, and the platform never learns. Nothing in either console shows an error, because
// from the platform's side nothing happened at all.
//
// It could not be fixed by attaching `MemDeliveries` instead. The dedupe table is what makes a delivery
// exactly-once, and a map gives that guarantee only for the lifetime of one process: a redelivery after
// a restart re-applies an effect that was already applied, and Stripe redelivers for days. The same
// applies to the state mirror, where the loss is silent rather than duplicated — see 0033's header.
//
// # Two stores, two different reasons the database is the arbiter
//
//   - `webhook_delivery.provider_event_id` is the PRIMARY KEY (migration 0013). Claim is therefore an
//     `INSERT … ON CONFLICT DO NOTHING` and a zero row count IS the "somebody else won" signal. It does
//     not check-then-insert: a prior SELECT would lose exactly the race the claim exists to catch, and
//     two pods handling the same redelivery would both proceed.
//   - `billing_state` (migration 0033) is a MIRROR, so Put is an upsert keyed on the customer. There is
//     no history here on purpose; the history is `billing_event`, which is append-only.

// PGDeliveries is the durable webhook dedupe table, over `webhook_delivery`.
type PGDeliveries struct{ db *sql.DB }

// NewPGDeliveries returns a durable DeliveryStore over an open Postgres handle.
func NewPGDeliveries(db *sql.DB) (*PGDeliveries, error) {
	if db == nil {
		return nil, errors.New("billing: nil database")
	}
	return &PGDeliveries{db: db}, nil
}

func storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// Claim records the delivery and reports whether THIS caller won it.
//
// 🔴 An error is NOT a lost claim, and the two must not collapse into one return. `won=false, err=nil`
// means "already processed — ack it and do nothing", while an error means "we do not know", and the
// caller must return a non-2xx so the provider retries. Returning false on a database blip would ack an
// event whose effect was never applied, and the provider never sends it again.
func (p *PGDeliveries) Claim(providerEventID string, typ WebhookType, at time.Time) (bool, error) {
	if providerEventID == "" {
		return false, ErrWebhookNoEventID
	}
	if at.IsZero() {
		at = time.Now()
	}
	ctx, cancel := storeCtx()
	defer cancel()
	res, err := p.db.ExecContext(ctx, `
		INSERT INTO webhook_delivery (provider_event_id, type, processed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider_event_id) DO NOTHING`,
		providerEventID, string(typ), at.UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Release withdraws a claim this caller won but could not complete.
//
// Deleting the row is the whole point: the claim is taken BEFORE the effect is persisted, so a failure
// between the two leaves a delivery marked processed whose effect never landed, and the provider's retry
// would find the claim and apply nothing. Releasing a claim that was never won deletes no row and is a
// no-op, so a double release cannot re-open a delivery that did succeed.
func (p *PGDeliveries) Release(providerEventID string) error {
	if providerEventID == "" {
		return nil
	}
	ctx, cancel := storeCtx()
	defer cancel()
	_, err := p.db.ExecContext(ctx, `DELETE FROM webhook_delivery WHERE provider_event_id = $1`, providerEventID)
	return err
}

// Seen reports whether a delivery has been processed.
//
// ⚠️ It returns a bool with no error, because DeliveryStore says so — and that means a database failure
// is indistinguishable here from "not seen". That is safe ONLY because nothing decides whether to
// process a delivery from this method: Claim does, and Claim can fail loudly. Seen is a read-side
// convenience (tests, and the operator's delivery view). Do not route a dedupe decision through it.
func (p *PGDeliveries) Seen(providerEventID string) bool {
	ctx, cancel := storeCtx()
	defer cancel()
	var n int
	if err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_delivery WHERE provider_event_id = $1`, providerEventID).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// Count is how many deliveries have been processed. Same caveat as Seen: a failure reads as zero, and
// nothing may decide anything from it.
func (p *PGDeliveries) Count() int {
	ctx, cancel := storeCtx()
	defer cancel()
	var n int
	if err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_delivery`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// PGStates is the durable mirror of provider-owned billing state, over `billing_state`.
type PGStates struct{ db *sql.DB }

// NewPGStates returns a durable StateStore over an open Postgres handle.
func NewPGStates(db *sql.DB) (*PGStates, error) {
	if db == nil {
		return nil, errors.New("billing: nil database")
	}
	return &PGStates{db: db}, nil
}

// Put persists one customer's mirrored state. An error means NOT PERSISTED, and the webhook path turns
// that into a non-2xx so the provider retries — the persist-then-ack ordering P21 task 4.3 requires.
func (p *PGStates) Put(st BillingState) error {
	if st.CustomerID == "" {
		return errors.New("billing: a mirrored billing state needs a customer id")
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now()
	}
	// 🔴 Mirror the CARD-DISPLAY constraint before the database does. 0033 refuses a brand or last4
	// without payment_method_present, and the honest reading of "no card present" is that there is
	// nothing to display — so the display fields are cleared rather than the write being failed. A
	// failure here would return a non-2xx for a webhook whose content is perfectly valid, and Stripe
	// would retry it forever.
	if !st.PaymentMethodPresent {
		st.PaymentMethodBrand, st.PaymentMethodLast4 = "", ""
	}
	ctx, cancel := storeCtx()
	defer cancel()
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO billing_state (
			customer_id, invoice_status, subscription_status, last_invoice_ref,
			payment_failed, past_due,
			payment_method_present, payment_method_brand, payment_method_last4, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (customer_id) DO UPDATE SET
			invoice_status         = EXCLUDED.invoice_status,
			subscription_status    = EXCLUDED.subscription_status,
			last_invoice_ref       = EXCLUDED.last_invoice_ref,
			payment_failed         = EXCLUDED.payment_failed,
			past_due               = EXCLUDED.past_due,
			payment_method_present = EXCLUDED.payment_method_present,
			payment_method_brand   = EXCLUDED.payment_method_brand,
			payment_method_last4   = EXCLUDED.payment_method_last4,
			updated_at             = EXCLUDED.updated_at`,
		st.CustomerID, st.InvoiceStatus, st.SubscriptionStatus, st.LastInvoiceRef,
		st.PaymentFailed, st.PastDue,
		st.PaymentMethodPresent, st.PaymentMethodBrand, st.PaymentMethodLast4, st.UpdatedAt.UTC())
	return err
}

// Get returns the mirrored state, or the zero value for a customer with none.
//
// ⚠️ StateStore.Get returns no error, so a database failure reads here as "this customer has no
// mirrored state" — the same value a customer who has never been charged produces. That is the
// interface's shape, and it is tolerable only because of which way it fails: the zero value renders as
// "no payment problem, no card on file", so an outage understates a dunning state rather than inventing
// one. It must never become the input to a decision that CHARGES or SUSPENDS anything.
func (p *PGStates) Get(customerID string) BillingState {
	ctx, cancel := storeCtx()
	defer cancel()
	var st BillingState
	err := p.db.QueryRowContext(ctx, `
		SELECT customer_id, invoice_status, subscription_status, last_invoice_ref,
		       payment_failed, past_due,
		       payment_method_present, payment_method_brand, payment_method_last4, updated_at
		  FROM billing_state WHERE customer_id = $1`, customerID).
		Scan(&st.CustomerID, &st.InvoiceStatus, &st.SubscriptionStatus, &st.LastInvoiceRef,
			&st.PaymentFailed, &st.PastDue,
			&st.PaymentMethodPresent, &st.PaymentMethodBrand, &st.PaymentMethodLast4, &st.UpdatedAt)
	if err != nil {
		return BillingState{}
	}
	return st
}
