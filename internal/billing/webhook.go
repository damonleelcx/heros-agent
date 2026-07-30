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
	"strconv"
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
	// Release withdraws a claim this caller won but could not complete (P21 task 4.3).
	//
	// It exists because the claim is taken BEFORE the effect is persisted — it has to be, or two
	// concurrent redeliveries both proceed — and that ordering opens exactly one gap: the claim
	// persists, the effect does not, and the provider's retry then finds the claim and applies nothing.
	// That is an event acked-but-unrecorded by a slower route, which is the failure persist-then-ack
	// exists to prevent. Releasing the claim before returning a non-2xx closes it: the retry re-claims
	// and re-applies. Releasing a claim that was never won is a no-op, so a double release cannot
	// re-open a delivery that did succeed.
	Release(providerEventID string) error
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

// Release withdraws a claim. It is deliberately tolerant of an unknown id: a release for a delivery
// that was never claimed must not be an error, or the failure path would have its own failure path.
func (d *MemDeliveries) Release(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.down {
		return errors.New("billing: webhook delivery store unavailable — the claim could not be released, so the effect must be reconciled rather than retried")
	}
	delete(d.rows, id)
	return nil
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

// StateStore is the durable mirror of the provider-owned billing state (P21 task 4.3).
//
// It is an interface rather than a map field because "the effect was persisted" has to be a claim the
// endpoint can FAIL to make. With a map, a persistence failure is unrepresentable, and the
// persist-then-ack test would be asserting against a code path that cannot go wrong — which is not a
// test, it is a restatement of the implementation.
type StateStore interface {
	// Put persists a customer's mirrored provider state. An error means NOT PERSISTED: the caller must
	// return a non-2xx so the provider retries.
	Put(BillingState) error
	// Get returns the mirrored state, or the zero value for a customer with none.
	Get(customerID string) BillingState
}

// MemStates is the in-memory mirror.
type MemStates struct {
	mu   sync.Mutex
	rows map[string]BillingState
	down bool
}

// NewMemStates builds an empty state mirror.
func NewMemStates() *MemStates { return &MemStates{rows: map[string]BillingState{}} }

// SetDown flips the mirror between available and unavailable — the persistence-failure seam.
func (m *MemStates) SetDown(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.down = v
}

// Put persists one customer's mirrored state.
func (m *MemStates) Put(st BillingState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return errors.New("billing: billing state store unavailable — the mirrored provider state was NOT persisted")
	}
	m.rows[st.CustomerID] = st
	return nil
}

// Get returns a customer's mirrored state.
func (m *MemStates) Get(customerID string) BillingState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[customerID]
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
	if err := s.verifySignature(ctx, in); err != nil {
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

	// ── 3. PERSIST the side effect ────────────────────────────────────────────
	state, err := s.applyWebhook(p)
	if err != nil {
		// The claim was won but the effect did not persist. Withdraw the claim so the provider's retry
		// re-applies rather than being deduped into nothing — see DeliveryStore.Release.
		if rerr := s.deliveries.Release(p.ProviderEventID); rerr != nil {
			// Neither the effect nor the release persisted. This is the one case that cannot be fixed by
			// a retry, so it is reported as a GAP to reconcile rather than swallowed: the delivery is
			// claimed, nothing was applied, and a redelivery would apply nothing.
			return WebhookResult{}, fmt.Errorf("%w: the delivery claim for %s could not be released after the "+
				"effect failed to persist — this event must be RECONCILED against the provider, not waited for: %v",
				ErrWebhookNotPersisted, p.ProviderEventID, rerr)
		}
		return WebhookResult{}, fmt.Errorf("%w: %v", ErrWebhookNotPersisted, err)
	}
	// The provider's invoice/subscription state is a revenue signal in its own right: it is how the
	// dunning funnel becomes visible before a customer calls about it.
	if st := firstNonEmpty(state.InvoiceStatus, state.SubscriptionStatus); st != "" {
		s.observe.InvoiceState(p.CustomerID, p.Period, st)
	}
	return WebhookResult{ProviderEventID: p.ProviderEventID, Type: p.Type, Applied: true, State: state}, nil
}

// verifySignature is step 1 in isolation: the signature is checked against the signing secret from the
// seam BEFORE a byte of the body is parsed into a decision.
//
// It accepts two encodings of the same HMAC, and the reason is worth stating rather than leaving as a
// surprise: the material signed is identical (`"<timestamp>.<body>"`, HMAC-SHA256), only the transport
// differs. Stripe packs the timestamp and one-or-more signatures into a SINGLE header
// (`t=…,v1=…,v1=…`); P7's own form carries them in separate fields. Supporting both means the P21
// endpoint speaks Stripe's scheme without P7's existing callers changing, and there is exactly one
// verification implementation rather than two that can drift.
//
// Multiple `v1=` values are accepted because that is what a SECRET ROTATION looks like on the wire:
// Stripe signs with both the old and the new secret during the overlap window. A verifier that read
// only the first would reject half of the deliveries in the exact window where a rejection is most
// expensive to diagnose.
func (s *Service) verifySignature(ctx context.Context, in SignedWebhook) error {
	if strings.TrimSpace(in.Signature) == "" {
		return ErrNoSignature
	}
	if s.secrets == nil {
		return fmt.Errorf("%w: no webhook signing secret is configured", ErrSecretUnavailable)
	}
	secret, err := s.secrets.WebhookSigningSecret(ctx)
	if err != nil {
		// FAIL CLOSED: with no secret we cannot verify, and an unverifiable webhook must not be trusted.
		return err
	}

	stamp, candidates := parseStripeSignature(in.Signature)
	if stamp == "" {
		// P7's form: the timestamp travels beside the signature rather than inside the header.
		stamp, candidates = in.Timestamp, []string{in.Signature}
	}

	want := SignWebhook(secret, stamp, in.Body)
	ok := false
	for _, got := range candidates {
		// hmac.Equal, not ==: a byte-by-byte comparison that returns early leaks, through timing, how
		// much of a forged signature was correct.
		if hmac.Equal([]byte(want), []byte(got)) {
			ok = true
		}
	}
	if !ok {
		return ErrBadSignature
	}
	// The timestamp is checked AFTER the signature, and only then is it trustworthy: it is inside the
	// signed material, so a verified timestamp is one the sender committed to and an attacker cannot
	// move. Checking it first would be checking an attacker-supplied number.
	return s.checkSkew(stamp)
}

// StripeSignatureHeader is the header Stripe signs its deliveries with.
const StripeSignatureHeader = "Stripe-Signature"

// parseStripeSignature splits a `Stripe-Signature` header into its timestamp and its `v1` signatures.
//
// It returns an empty timestamp when the value is not in that form, which is how verifySignature tells
// the two encodings apart without a mode flag threaded through the call.
func parseStripeSignature(header string) (timestamp string, signatures []string) {
	if !strings.Contains(header, "t=") {
		return "", nil
	}
	for _, part := range strings.Split(header, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch k {
		case "t":
			timestamp = v
		case "v1":
			// Re-prefixed to the form SignWebhook produces, so there is one canonical shape being
			// compared rather than a comparison that strips a prefix on one side only.
			signatures = append(signatures, "v1="+v)
		}
	}
	if timestamp == "" || len(signatures) == 0 {
		return "", nil
	}
	return timestamp, signatures
}

// StripeSignatureFor builds the `Stripe-Signature` header value for a body — the exact bytes Stripe
// would send. It is the signing half of the verifier above, kept beside it so the two cannot drift, and
// it is what the endpoint's tests and the demo sign with.
func StripeSignatureFor(secret string, at time.Time, body []byte) string {
	ts := strconv.FormatInt(at.UTC().Unix(), 10)
	sig := strings.TrimPrefix(SignWebhook(secret, ts, body), "v1=")
	return "t=" + ts + ",v1=" + sig
}

func (s *Service) checkSkew(stamp string) error {
	if stamp == "" {
		return fmt.Errorf("%w: no timestamp", ErrWebhookStaleStamp)
	}
	// Two encodings, one meaning: Stripe signs Unix seconds, P7's own form carries RFC3339. Both are
	// accepted here for the same reason verifySignature accepts both headers — the signed material is
	// identical and a second skew implementation is a second thing to get wrong.
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		secs, nerr := strconv.ParseInt(stamp, 10, 64)
		if nerr != nil {
			return fmt.Errorf("%w: unparseable timestamp %q", ErrWebhookStaleStamp, stamp)
		}
		t = time.Unix(secs, 0).UTC()
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
func (s *Service) applyWebhook(p WebhookPayload) (BillingState, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	st := s.states.Get(p.CustomerID)
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
	if err := s.states.Put(st); err != nil {
		return BillingState{}, err
	}
	return st, nil
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
	return s.states.Get(customerID)
}

// ─────────────────────────────────────────────────────────────────────────────
// P21 task 4.2/4.3 — the endpoint's whole body
// ─────────────────────────────────────────────────────────────────────────────

// ErrWebhookNotPersisted is returned when the delivery could not be durably recorded. It is the one
// error that MUST become a non-2xx: an HTTP 200 to Stripe is a promise the event was recorded, and
// Stripe stops retrying an event it thinks succeeded.
var ErrWebhookNotPersisted = errors.New("billing: the webhook delivery was NOT durably recorded")

// WebhookAck is one delivery's outcome, including the HTTP status the endpoint must return.
//
// The status is decided HERE rather than at the HTTP boundary because it encodes a money decision — a
// non-2xx is a request for Stripe to retry — and that decision must not be re-derived by a handler that
// cannot see whether the effect was persisted. The handler's job is to write the number down.
type WebhookAck struct {
	ProviderEventID string      `json:"provider_event_id,omitempty"`
	Type            WebhookType `json:"type,omitempty"`
	Duplicate       bool        `json:"duplicate"`
	Applied         bool        `json:"applied"`
	// Status is the HTTP status the endpoint returns.
	Status int `json:"-"`
	// Reason is a short, non-sensitive explanation for a non-2xx. It never carries the payload, the
	// signature, or the secret — a rejected webhook's diagnostics must not become a second leak.
	Reason string `json:"reason,omitempty"`
}

// HandleStripeWebhook is the inbound endpoint's whole body: verify → dedupe → persist → ack.
//
// ## The status codes are the contract, not a formatting choice
//
//	200  applied, or a redelivery that applied nothing. Both mean DURABLY OURS. Stripe stops retrying.
//	400  the delivery is not from Stripe, or cannot be processed at all: unsigned, forged, stale, no
//	     event id, unparseable. Retrying it would produce the same answer forever.
//	500  the delivery IS from Stripe and was NOT recorded. This is the important one: it is how the
//	     platform asks for the retry that at-least-once delivery exists to provide.
//
// The 400/500 split is the whole design. Answering 400 for a persistence failure would tell Stripe the
// event is permanently unprocessable and it would eventually stop — turning a transient database
// problem into a silently lost subscription change. Answering 200 would do it immediately.
func (s *Service) HandleStripeWebhook(ctx context.Context, body []byte, signatureHeader string) WebhookAck {
	res, err := s.HandleWebhook(ctx, SignedWebhook{Body: body, Signature: signatureHeader})
	switch {
	case err == nil:
		return WebhookAck{
			ProviderEventID: res.ProviderEventID, Type: res.Type,
			Duplicate: res.Duplicate, Applied: res.Applied, Status: 200,
		}
	case errors.Is(err, ErrWebhookNotPersisted),
		errors.Is(err, ErrSecretUnavailable):
		// Not recorded (or not verifiable because the secret is unreachable). Either way the platform
		// must NOT ack: it wants the redelivery.
		return WebhookAck{Status: 500, Reason: err.Error()}
	default:
		// Unsigned, forged, stale, unparseable, no event id, or no delivery store. None of these
		// improves on a retry.
		return WebhookAck{Status: 400, Reason: err.Error()}
	}
}
