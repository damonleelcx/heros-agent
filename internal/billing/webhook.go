package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// webhook.go handles inbound provider webhooks: **signature-verified before any side effect**, and
// **deduped by `provider_event_id`** so a redelivered webhook is processed exactly once (task 4.1 /
// FR14 / design Decision 5 and 10).
//
// ## Order is the whole design
//
//	1. VERIFY the signature — against the secret from the secrets manager.
//	2. DEDUPE by provider_event_id — claim the delivery, or recognize it as a redelivery.
//	3. THEN apply the side effect.
//
// Step 1 before everything: an unsigned or forged payload must not be able to move a single byte of
// state. A handler that parses first and verifies later has already trusted attacker-controlled input
// to choose a code path. Step 2 before step 3: the claim is what makes "exactly once" true; deduping
// AFTER applying would process the redelivery and then notice.
//
// ## Why the platform stores webhook state at all
//
// Provider webhooks carry the dunning story — `payment_failed`, `past_due`, `paid`. The platform does
// not RECOMPUTE any of it (that is the provider's, design Decision 1 of the billing spec); it records
// the latest provider-owned state so the UI can show a past-due banner without asking the provider on
// every page load.

// WebhookType is a provider event type the platform acts on. Unknown types are ACKNOWLEDGED and
// ignored rather than rejected: a provider adding an event type must not start failing our endpoint,
// but an unknown type must also never be guessed at.
type WebhookType string

const (
	WebhookInvoicePaid          WebhookType = "invoice.paid"
	WebhookInvoicePaymentFailed WebhookType = "invoice.payment_failed"
	WebhookInvoiceFinalized     WebhookType = "invoice.finalized"
	WebhookSubscriptionUpdated  WebhookType = "customer.subscription.updated"
	WebhookSubscriptionPastDue  WebhookType = "customer.subscription.past_due"
	WebhookSubscriptionCanceled WebhookType = "customer.subscription.deleted"
	WebhookChargeRefunded       WebhookType = "charge.refunded"
	WebhookChargeDisputeCreated WebhookType = "charge.dispute.created"
)

// WebhookPayload is the provider's event body.
type WebhookPayload struct {
	// ProviderEventID is the provider's own event id — the DEDUPE key.
	ProviderEventID string      `json:"id"`
	Type            WebhookType `json:"type"`
	CustomerID      string      `json:"customer_id"`
	Period          string      `json:"period,omitempty"`
	SubscriptionRef string      `json:"subscription_ref,omitempty"`
	InvoiceRef      string      `json:"invoice_ref,omitempty"`
	ChargeRef       string      `json:"charge_ref,omitempty"`
	// Status is the provider-owned state this event reports (paid / past_due / canceled / ...). The
	// platform mirrors it; it never derives it.
	Status    string `json:"status,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// SignedWebhook is a raw inbound delivery: the bytes as received and the signature header.
//
// The RAW BYTES are kept, not a parsed struct: a signature covers the bytes the provider sent, and
// re-serializing a parsed payload to verify it would compare a signature against something the provider
// never signed.
type SignedWebhook struct {
	Body      []byte
	Signature string
	// Timestamp is the provider's signing timestamp, part of the signed material so a captured payload
	// cannot be replayed indefinitely.
	Timestamp string
}

// Webhook errors. Each is distinguishable because the operational response differs: a bad signature is
// a security event, a duplicate is normal, an unparseable body is a contract break.
var (
	ErrBadSignature      = errors.New("billing: webhook signature verification failed — rejected before any side effect")
	ErrNoSignature       = errors.New("billing: webhook carries no signature — rejected before any side effect")
	ErrWebhookMalformed  = errors.New("billing: webhook payload is malformed")
	ErrWebhookNoEventID  = errors.New("billing: webhook carries no provider event id — it cannot be deduped, so it cannot be processed exactly once")
	ErrWebhookStaleStamp = errors.New("billing: webhook timestamp is outside the accepted window — a captured payload may not be replayed indefinitely")
)

// SignWebhook produces the signature the platform verifies: HMAC-SHA256 over "<timestamp>.<body>".
//
// The timestamp is inside the signed material on purpose. Signing the body alone would make every
// captured delivery valid forever, so an attacker who once observed a legitimate payload could replay
// it at will — the signature would check out every time.
func SignWebhook(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// WebhookDelivery is one processed delivery — the `webhook_delivery` row.
type WebhookDelivery struct {
	ProviderEventID string      `json:"provider_event_id"`
	Type            WebhookType `json:"type"`
	ProcessedAt     time.Time   `json:"processed_at"`
}

// DeliveryStore is the webhook dedupe table.
type DeliveryStore interface {
	// Claim records the delivery and reports whether THIS caller won the claim. A redelivery returns
	// false — and it must do so atomically, because two concurrent redeliveries that both "check then
	// insert" would both proceed.
	Claim(providerEventID string, typ WebhookType, at time.Time) (won bool, err error)
	Seen(providerEventID string) bool
	Count() int
}

// MemDeliveries is the in-memory `webhook_delivery` table. Claim holds the lock across
// check-and-insert, which is the same atomicity the Postgres PRIMARY KEY provides.
type MemDeliveries struct {
	mu   sync.Mutex
	rows map[string]WebhookDelivery
	down bool
}

// NewMemDeliveries builds an empty delivery store.
func NewMemDeliveries() *MemDeliveries { return &MemDeliveries{rows: map[string]WebhookDelivery{}} }

// SetDown flips the store between available and unavailable.
func (d *MemDeliveries) SetDown(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.down = v
}

// Claim atomically records the delivery, or reports it as already seen.
func (d *MemDeliveries) Claim(id string, typ WebhookType, at time.Time) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.down {
		return false, errors.New("billing: webhook delivery store unavailable")
	}
	if _, ok := d.rows[id]; ok {
		return false, nil
	}
	d.rows[id] = WebhookDelivery{ProviderEventID: id, Type: typ, ProcessedAt: at.UTC()}
	return true, nil
}

// Seen reports whether a delivery was already processed.
func (d *MemDeliveries) Seen(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.rows[id]
	return ok
}

// Count is how many distinct deliveries were processed.
func (d *MemDeliveries) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.rows)
}

// List returns every processed delivery in event-id order.
func (d *MemDeliveries) List() []WebhookDelivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]WebhookDelivery, 0, len(d.rows))
	for _, v := range d.rows {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderEventID < out[j].ProviderEventID })
	return out
}

// BillingState is the provider-owned state the platform mirrors for a customer — what the UI's
// payment-failed / past-due / dunning states render from.
type BillingState struct {
	CustomerID string `json:"customer_id"`
	// InvoiceStatus / SubscriptionStatus are the PROVIDER's words, carried verbatim. The platform does
	// not translate them into its own vocabulary: a state the provider owns and the platform renames is
	// a state two systems will eventually disagree about.
	InvoiceStatus      string    `json:"invoice_status,omitempty"`
	SubscriptionStatus string    `json:"subscription_status,omitempty"`
	LastInvoiceRef     string    `json:"last_invoice_ref,omitempty"`
	PaymentFailed      bool      `json:"payment_failed"`
	PastDue            bool      `json:"past_due"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// WebhookResult reports what one delivery did.
type WebhookResult struct {
	ProviderEventID string      `json:"provider_event_id"`
	Type            WebhookType `json:"type"`
	// Duplicate is true when this delivery was already processed. It is a SUCCESS: the provider gets a
	// 2xx and stops retrying, and nothing happened twice.
	Duplicate bool `json:"duplicate"`
	// Applied is true when a side effect was performed.
	Applied bool         `json:"applied"`
	State   BillingState `json:"state"`
}

// WebhookMaxSkew bounds how old a signed timestamp may be. Five minutes is the provider convention and
// is long enough for a slow retry, short enough that a captured payload is not a permanent key.
const WebhookMaxSkew = 5 * time.Minute

// HandleWebhook verifies, dedupes, and then applies one provider delivery.
//
// It returns Duplicate=true (and applies nothing) for a redelivery. It returns an error — having
// touched nothing — for an unsigned or badly-signed payload.
func (s *Service) HandleWebhook(ctx context.Context, in SignedWebhook) (WebhookResult, error) {
	if s.deliveries == nil {
		return WebhookResult{}, errors.New("billing: no webhook delivery store — a webhook that cannot be deduped must not be processed")
	}

	// ── 1. VERIFY, before anything is parsed into a decision ──────────────────
	if strings.TrimSpace(in.Signature) == "" {
		return WebhookResult{}, ErrNoSignature
	}
	if s.secrets == nil {
		return WebhookResult{}, fmt.Errorf("%w: no webhook signing secret is configured", ErrSecretUnavailable)
	}
	secret, err := s.secrets.WebhookSigningSecret(ctx)
	if err != nil {
		// FAIL CLOSED: with no secret we cannot verify, and an unverifiable webhook must not be trusted.
		return WebhookResult{}, err
	}
	want := SignWebhook(secret, in.Timestamp, in.Body)
	if !hmac.Equal([]byte(want), []byte(in.Signature)) {
		return WebhookResult{}, ErrBadSignature
	}
	if err := s.checkSkew(in.Timestamp); err != nil {
		return WebhookResult{}, err
	}

	var p WebhookPayload
	if err := json.Unmarshal(in.Body, &p); err != nil {
		return WebhookResult{}, fmt.Errorf("%w: %v", ErrWebhookMalformed, err)
	}
	if p.ProviderEventID == "" {
		return WebhookResult{}, ErrWebhookNoEventID
	}

	// ── 2. DEDUPE — claim the delivery ────────────────────────────────────────
	won, err := s.deliveries.Claim(p.ProviderEventID, p.Type, s.now())
	if err != nil {
		return WebhookResult{}, fmt.Errorf("billing: claim webhook delivery: %w", err)
	}
	if !won {
		return WebhookResult{ProviderEventID: p.ProviderEventID, Type: p.Type, Duplicate: true,
			State: s.BillingState(p.CustomerID)}, nil
	}

	// ── 3. APPLY the side effect ──────────────────────────────────────────────
	state := s.applyWebhook(p)
	// The provider's invoice/subscription state is a revenue signal in its own right: it is how the
	// dunning funnel becomes visible before a customer calls about it.
	if st := firstNonEmpty(state.InvoiceStatus, state.SubscriptionStatus); st != "" {
		s.observe.InvoiceState(p.CustomerID, p.Period, st)
	}
	return WebhookResult{ProviderEventID: p.ProviderEventID, Type: p.Type, Applied: true, State: state}, nil
}

func (s *Service) checkSkew(stamp string) error {
	if stamp == "" {
		return fmt.Errorf("%w: no timestamp", ErrWebhookStaleStamp)
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return fmt.Errorf("%w: unparseable timestamp %q", ErrWebhookStaleStamp, stamp)
	}
	d := s.now().Sub(t)
	if d < 0 {
		d = -d
	}
	if d > WebhookMaxSkew {
		return fmt.Errorf("%w: %s off", ErrWebhookStaleStamp, d.Round(time.Second))
	}
	return nil
}

// applyWebhook mirrors the provider's state. It computes nothing: each branch copies what the provider
// said. An unknown type updates nothing and is acknowledged — a provider adding an event must not break
// the endpoint, and guessing at an unknown event is worse than ignoring it.
func (s *Service) applyWebhook(p WebhookPayload) BillingState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	st := s.states[p.CustomerID]
	st.CustomerID = p.CustomerID
	st.UpdatedAt = s.now().UTC()

	switch p.Type {
	case WebhookInvoicePaid:
		st.InvoiceStatus, st.LastInvoiceRef = "paid", p.InvoiceRef
		st.PaymentFailed, st.PastDue = false, false
	case WebhookInvoicePaymentFailed:
		st.InvoiceStatus, st.LastInvoiceRef = "payment_failed", p.InvoiceRef
		st.PaymentFailed = true
	case WebhookInvoiceFinalized:
		st.InvoiceStatus, st.LastInvoiceRef = "open", p.InvoiceRef
	case WebhookSubscriptionPastDue:
		st.SubscriptionStatus, st.PastDue = "past_due", true
	case WebhookSubscriptionUpdated:
		st.SubscriptionStatus = p.Status
		st.PastDue = p.Status == "past_due"
	case WebhookSubscriptionCanceled:
		st.SubscriptionStatus = "canceled"
	case WebhookChargeRefunded, WebhookChargeDisputeCreated:
		// The money movement itself is the provider's; the platform records the correction through the
		// audited Credit/Refund path, never by inventing a ledger row from a webhook. A webhook is a
		// notification, not an authorization to write to the billing ledger.
	}
	if s.states == nil {
		s.states = map[string]BillingState{}
	}
	s.states[p.CustomerID] = st
	return st
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// BillingState returns the provider-owned state mirrored for a customer.
func (s *Service) BillingState(customerID string) BillingState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.states[customerID]
}
