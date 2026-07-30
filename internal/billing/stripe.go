package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// stripe.go is P21: the REAL provider behind the P7 abstraction.
//
// It is a substitution, not a new API surface. `StripeProvider` satisfies the EXISTING
// [Provider] interface byte-for-byte — same methods, same request/result structs, same error
// semantics — with the network swapped in. Every caller (service.go's charge protocol, correction.go's
// credit path, the reconciler) runs unchanged, and a second processor stays expressible as a second
// implementation rather than a re-plumb (design Decision 1, ratified).
//
// The design and the specs call this "a `stripe.Provider`". It lives in package `billing` rather than a
// `billing/stripe` subpackage because the contract-parity suite must run the SAME tests against this and
// against [StubProvider], and because nothing here is reusable outside the billing capability. The type
// is `StripeProvider`; wherever the prose says `stripe.Provider`, it means this.
//
// ## What is load-bearing here
//
//   - **The idempotency key is the P7 key, on Stripe's `Idempotency-Key` header** (Decision 4). It is
//     never composed here; it arrives on the request struct, derived by ledger.go. Two layers then
//     refuse a duplicate: the ledger's UNIQUE key (no second row) and Stripe's own idempotency (no
//     second object).
//   - **No amount, ever** (P7 Decision 10). Prices are opaque `price_ref` handles; quantities are
//     quantities; `AmountRef` is a POINTER at where the amount lives in Stripe, not a number. There is
//     no arithmetic in this file that touches money.
//   - **Outage and rejection are different errors** (FR / task 2.6). An outage means BUFFER AND RETRY
//     (`ErrProviderUnavailable`, which the P7 outage buffer and `FlushPending` already understand); a
//     rejection means STOP (`ErrProviderRejected`). Collapsing them would either drop revenue or hammer
//     a processor that is deliberately refusing.
//   - **Corrections are additive** (Decision 8). `IssueCredit` creates a NEW Stripe credit note or
//     refund against a prior charge. Nothing here can reduce, void, or delete an original object — there
//     is no code path to it.
//
// ## Why a fake Stripe in tests rather than a mock provider
//
// A mock would prove that this file calls methods; it would prove nothing about the wire. The tests
// drive this code against an HTTP server that behaves the way Stripe's API behaves — form-encoded
// requests, JSON objects, real idempotency replay, real error envelopes — so what is under test is the
// HTTP conversation, which is the part that can actually be wrong.

// Stripe API constants.
const (
	// stripeBaseURL is Stripe's API root. Overridable ONLY through WithStripeBaseURL, which exists for
	// the fake-Stripe tests; there is no environment variable, because a base URL that can be moved by
	// the environment is a payment processor that can be moved by the environment.
	stripeBaseURL = "https://api.stripe.com"
	// stripeAPIVersion pins the API shape this file was written against. Pinned rather than floating: an
	// unpinned integration changes behaviour when someone else ships, which for money is the worst
	// possible way to find out.
	stripeAPIVersion = "2024-06-20"
	// stripeTimeout bounds one Stripe call. There is no bare http.Client here — an outbound client with
	// no timeout is a goroutine that never returns and a charge whose outcome is never learned.
	stripeTimeout = 30 * time.Second
)

// Metadata keys the platform stamps on the Stripe objects it creates. They are the join back to the
// platform's own records — the platform's period key, kind and basis are not concepts Stripe has, so
// without them a read-back invoice could not name what justified each line (Invoice.Validate).
const (
	metaCustomerID = "platform_customer_id"
	metaPeriod     = "platform_period"
	metaKind       = "platform_kind"
	metaBasis      = "platform_basis"
)

// Stripe-specific errors. They are in this file rather than provider.go because P21 does not change P7's
// files; they are still `billing.` errors, which is what callers match on.
var (
	// ErrProviderRejected is a DELIBERATE refusal by the provider — a bad request, a missing object, a
	// declined card. It is distinguishable from ErrProviderUnavailable on purpose: an outage is retried,
	// a rejection is not. Retrying a rejection is how an integration turns one bad request into a rate
	// limit.
	ErrProviderRejected = errors.New("billing: provider rejected the request")
	// ErrStripeKeyMode is the test/live guard (task 3.2). A live key resolving for a test surface is a
	// real-money incident waiting for its first test run, so it is refused at construction and at every
	// call, not documented as a deployment convention.
	ErrStripeKeyMode = errors.New("billing: stripe key does not match the configured billing mode")
)

// stripeCustomerCache is the platform-customer-id → Stripe customer handle memo. It is a cache, never a
// system of record: the account store holds the handle durably (account.ProviderCustomerHandle), and a
// cold cache costs one extra lookup, not a duplicate customer.
type stripeCustomerCache struct {
	mu sync.RWMutex
	by map[string]string
}

func (c *stripeCustomerCache) get(id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	h, ok := c.by[id]
	return h, ok
}

func (c *stripeCustomerCache) put(id, handle string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[id] = handle
}

// StripeProvider is the Stripe implementation of [Provider].
type StripeProvider struct {
	secrets Secrets
	mode    Mode
	now     Clock

	baseURL string
	http    *http.Client

	customers stripeCustomerCache

	// seen records, per idempotency key, the object id the FIRST call produced. It is how Duplicate is
	// reported honestly: on a repeat under the same key the call still goes to Stripe — that is the
	// point, Stripe's idempotency is the layer being exercised — and the result is marked a duplicate
	// when Stripe hands back the same object it handed back before, or when Stripe itself says it
	// replayed. Neither signal is invented here.
	seenMu sync.Mutex
	seen   map[string]string
}

// StripeOption configures the provider at construction.
type StripeOption func(*StripeProvider)

// WithStripeBaseURL points the provider at a different API root. It exists for the fake-Stripe tests.
func WithStripeBaseURL(u string) StripeOption {
	return func(p *StripeProvider) { p.baseURL = strings.TrimRight(u, "/") }
}

// WithStripeHTTPClient injects the HTTP client (a transport with a recorder, a test server's client).
func WithStripeHTTPClient(c *http.Client) StripeOption {
	return func(p *StripeProvider) {
		if c != nil {
			p.http = c
		}
	}
}

// NewStripeProvider builds the Stripe provider.
//
// Every dependency is required and resolved at construction EXCEPT the API key, which is resolved at the
// moment of use from the Secrets seam — that is the whole point of the seam (P7 secrets.go): the only
// way to obtain a credential is to ask for it when it is needed, so there is no field holding one that a
// formatter, a logger or a panic dump could reach.
//
// The mode's zero value is test (Mode's zero value is ""; it is normalized here), so a deployment that
// forgets to configure a mode charges nothing real.
func NewStripeProvider(secrets Secrets, mode Mode, clock Clock, opts ...StripeOption) (*StripeProvider, error) {
	if secrets == nil {
		return nil, errors.New("billing: the Stripe provider requires a secrets source — a billing credential is never read from code, config, or the environment")
	}
	switch mode {
	case "":
		mode = ModeTest
	case ModeTest, ModeLive:
	default:
		return nil, fmt.Errorf("billing: unknown billing mode %q (test|live)", mode)
	}
	if clock == nil {
		clock = time.Now
	}
	p := &StripeProvider{
		secrets:   secrets,
		mode:      mode,
		now:       clock,
		baseURL:   stripeBaseURL,
		http:      &http.Client{Timeout: stripeTimeout},
		customers: stripeCustomerCache{by: map[string]string{}},
		seen:      map[string]string{},
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// NewStripeProviderForRollout builds the provider in the mode the P7 rollout flag is in (task 3.2).
//
// This is how a DEPLOYMENT constructs it, and the reason it exists rather than a caller reading
// `rollout.Mode()` and passing it along: there must be exactly one answer to "is this process moving
// real money", and two places that derive it are two places that can disagree. A nil rollout is the
// fully dark zero value — test mode — because a deployment that forgot to wire the flag must charge
// nothing real rather than inherit whatever the last caller passed.
func NewStripeProviderForRollout(secrets Secrets, rollout *Rollout, clock Clock, opts ...StripeOption) (*StripeProvider, error) {
	mode := ModeTest
	if rollout != nil {
		mode = rollout.Mode()
	}
	return NewStripeProvider(secrets, mode, clock, opts...)
}

// Describe names the provider and its MODE for the readiness surface — the identity, never a credential.
//
// The mode is part of the identity on purpose: "which processor" and "is it moving real money" are the
// same question operationally, and a health surface that answered only the first would let a live
// deployment look identical to a test one.
func (p *StripeProvider) Describe() string { return "stripe(" + string(p.mode) + ")" }

// Mode is the provider's billing mode.
func (p *StripeProvider) Mode() Mode { return p.mode }

// ─────────────────────────────────────────────────────────────────────────────
// Transport
// ─────────────────────────────────────────────────────────────────────────────

// stripeResponse is one decoded Stripe reply plus the two facts the callers need that are not in the
// body: whether Stripe replayed an idempotent request, and the raw bytes for a typed decode.
type stripeResponse struct {
	body     []byte
	replayed bool
}

// apiKey resolves the Stripe secret key from the seam and asserts it matches the configured mode.
//
// The mode check is here — on the resolution path — rather than only at construction, because the seam
// resolves at the moment of use: a secret rotated to a live key under a test deployment must be refused
// at the next call, not at the next restart. Stripe's key prefixes (`sk_test_` / `sk_live_`,
// `rk_test_` / `rk_live_` for restricted keys) make the assertion checkable without ever inspecting
// the secret's value beyond its prefix.
func (p *StripeProvider) apiKey(ctx context.Context) (string, error) {
	key, err := p.secrets.APIKey(ctx)
	if err != nil {
		return "", err // already fails closed and names WHICH credential, never its value
	}
	live, known := stripeKeyIsLive(key)
	if !known {
		// An unrecognized prefix is refused rather than assumed. Assuming "probably test" is how a live
		// key ends up on a test surface, and the failure of that assumption is a real charge.
		return "", fmt.Errorf("%w: the resolved key has no recognizable stripe test/live prefix, so its mode cannot be established", ErrStripeKeyMode)
	}
	if live && p.mode != ModeLive {
		return "", fmt.Errorf("%w: a LIVE key resolved for a %s surface — refusing, because a live key on a test surface is a real-money incident", ErrStripeKeyMode, p.mode)
	}
	if !live && p.mode == ModeLive {
		return "", fmt.Errorf("%w: a TEST key resolved for a live surface — refusing, because live traffic would silently move no money", ErrStripeKeyMode)
	}
	return key, nil
}

// stripeKeyIsLive reports whether a Stripe key is a live key, and whether its mode could be established
// at all. It reads a PREFIX; it never logs, returns, or otherwise surfaces the key.
func stripeKeyIsLive(key string) (live bool, known bool) {
	k := strings.TrimSpace(key)
	switch {
	case strings.HasPrefix(k, "sk_live_"), strings.HasPrefix(k, "rk_live_"):
		return true, true
	case strings.HasPrefix(k, "sk_test_"), strings.HasPrefix(k, "rk_test_"):
		return false, true
	}
	return false, false
}

// do performs one Stripe API call.
//
// `idempotencyKey` is sent as Stripe's `Idempotency-Key` header when non-empty. It is only ever the
// caller's P7-derived key — this function never composes one, because a key composed at a call site is
// a double-charge with a green test suite (ledger.go).
func (p *StripeProvider) do(ctx context.Context, method, path string, form url.Values, idempotencyKey string) (stripeResponse, error) {
	key, err := p.apiKey(ctx)
	if err != nil {
		return stripeResponse{}, err
	}

	var body io.Reader
	endpoint := p.baseURL + path
	if method == http.MethodGet {
		if len(form) > 0 {
			endpoint += "?" + form.Encode()
		}
	} else if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return stripeResponse{}, fmt.Errorf("%w: build request: %v", ErrProviderRejected, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Stripe-Version", stripeAPIVersion)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	res, err := p.http.Do(req)
	if err != nil {
		// A transport failure is an OUTAGE: the request may or may not have been recorded, which is
		// exactly the ambiguous case the write-ahead ledger row and the idempotency key exist for. The
		// caller buffers and retries under the same key.
		return stripeResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, redactURL(err))
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return stripeResponse{}, fmt.Errorf("%w: reading the response failed after the request was accepted: %v", ErrProviderUnavailable, err)
	}

	out := stripeResponse{
		body: raw,
		// Stripe sets this when it serves a stored response for a repeated idempotency key. It is the
		// provider's own statement that it did not create a second object.
		replayed: strings.EqualFold(res.Header.Get("Idempotent-Replayed"), "true"),
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return out, nil
	}
	return out, classifyStripeError(res.StatusCode, raw)
}

// stripeErrorEnvelope is Stripe's error body.
type stripeErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
	} `json:"error"`
}

// classifyStripeError performs task 2.6's split: outage vs. rejection.
//
// The line is drawn where the OPERATIONAL RESPONSE differs, not where Stripe's taxonomy does:
//
//	429 / 5xx / api_error / lock_timeout  → OUTAGE. Retryable. The usage is safe; the charge is buffered.
//	every other 4xx                        → REJECTION. Not retryable. Retrying is how one bad request
//	                                         becomes a rate limit, and the row must not be re-attempted
//	                                         until whatever is wrong with it is fixed.
func classifyStripeError(status int, raw []byte) error {
	var env stripeErrorEnvelope
	_ = json.Unmarshal(raw, &env.Error)
	_ = json.Unmarshal(raw, &env)

	detail := strings.TrimSpace(env.Error.Message)
	code := strings.TrimSpace(firstNonEmpty(env.Error.Code, env.Error.Type))
	if detail == "" {
		detail = "no error message in the response body"
	}

	switch {
	case status == http.StatusTooManyRequests,
		status >= 500,
		env.Error.Type == "api_error",
		env.Error.Code == "lock_timeout":
		return fmt.Errorf("%w: stripe %d %s: %s", ErrProviderUnavailable, status, code, detail)
	}
	return fmt.Errorf("%w: stripe %d %s: %s", ErrProviderRejected, status, code, detail)
}

// redactURL strips any query string from a transport error before it is wrapped. A Stripe error never
// carries a credential in a URL, but an error string is the one place a future parameter would leak
// without anyone noticing, and the URL adds nothing a caller can act on.
func redactURL(err error) string {
	s := err.Error()
	if i := strings.Index(s, "?"); i >= 0 {
		if j := strings.Index(s[i:], " "); j >= 0 {
			return s[:i] + "?<redacted>" + s[i+j:]
		}
		return s[:i] + "?<redacted>"
	}
	return s
}

// decode unmarshals a Stripe object, turning a shape surprise into a REJECTION rather than a panic three
// frames later.
func decode(res stripeResponse, into any) error {
	if err := json.Unmarshal(res.body, into); err != nil {
		return fmt.Errorf("%w: stripe returned a body this integration cannot read: %v", ErrProviderRejected, err)
	}
	return nil
}

// markSeen records the object an idempotency key produced and reports whether this call is a repeat that
// resolved to the SAME object. Together with Stripe's own replay signal it is what `Duplicate` means.
func (p *StripeProvider) markSeen(key, objectID string, replayed bool) bool {
	if key == "" {
		return replayed
	}
	p.seenMu.Lock()
	defer p.seenMu.Unlock()
	prev, ok := p.seen[key]
	if !ok {
		p.seen[key] = objectID
		return replayed
	}
	return replayed || prev == objectID
}

// ─────────────────────────────────────────────────────────────────────────────
// Quantities
// ─────────────────────────────────────────────────────────────────────────────

// stripeQuantity converts a platform quantity to the integer Stripe records.
//
// 🔴 It REFUSES a non-integral quantity instead of rounding it. Two rejected alternatives, and why:
//
//   - Rounding silently changes what a customer is billed. A billing bug that rounds is the hardest kind
//     to find, because every individual number looks plausible.
//   - Scaling by a factor would mean the platform multiplying a quantity to reach a price — which is the
//     platform computing money, the one thing the whole design refuses to do (P7 Decision 10).
//
// The remedy is a STRIPE-SIDE configuration: denominate the price in the meter's integral unit, so the
// quantity the platform reports is already a whole number of those units. That is Finance administering
// a price, not an engineer changing a number — which is exactly where the money-in-git rule wants it.
func stripeQuantity(q float64) (int64, error) {
	if math.IsNaN(q) || math.IsInf(q, 0) {
		return 0, fmt.Errorf("%w: quantity %v is not a number", ErrProviderRejected, q)
	}
	if q < 0 {
		return 0, fmt.Errorf("%w: refusing a negative quantity (%v) — a reduction is issued as an additive credit, never as a negative charge", ErrProviderRejected, q)
	}
	if q != math.Trunc(q) {
		return 0, fmt.Errorf("%w: stripe records whole-unit quantities and this one is %v — round-tripping it would change what the customer is billed. "+
			"Denominate the price in the meter's integral unit in Stripe so the reported quantity is a whole number of those units", ErrProviderRejected, q)
	}
	return int64(q), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Customers
// ─────────────────────────────────────────────────────────────────────────────

type stripeCustomer struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata"`
	Deleted  bool              `json:"deleted"`
}

type stripeSearchResult struct {
	Data []stripeCustomer `json:"data"`
}

// EnsureCustomer returns the Stripe customer handle for a platform customer, creating it if needed.
//
// ## Why a search AND an idempotency key
//
// Stripe's idempotency window is finite (24h). A search on the platform's own metadata is durable but
// eventually consistent (Stripe's search index lags a creation by up to a minute). Each covers exactly
// the other's gap: inside the first minute the idempotency key prevents a second customer; after the
// window expires the search finds the first one. Relying on either alone leaves a duplicate-customer
// hole, and a duplicate customer is a split billing history nobody notices until reconciliation.
func (p *StripeProvider) EnsureCustomer(ctx context.Context, customerID string) (string, error) {
	if strings.TrimSpace(customerID) == "" {
		return "", fmt.Errorf("%w: a customer handle cannot be established without a customer id", ErrProviderRejected)
	}
	if h, ok := p.customers.get(customerID); ok {
		return h, nil
	}
	if h, found, err := p.lookupCustomer(ctx, customerID); err != nil {
		return "", err
	} else if found {
		p.customers.put(customerID, h)
		return h, nil
	}

	form := url.Values{}
	form.Set("description", "platform customer "+customerID)
	form.Set("metadata["+metaCustomerID+"]", customerID)
	res, err := p.do(ctx, http.MethodPost, "/v1/customers", form, "ensure_customer:"+customerID)
	if err != nil {
		return "", err
	}
	var cus stripeCustomer
	if err := decode(res, &cus); err != nil {
		return "", err
	}
	if cus.ID == "" {
		return "", fmt.Errorf("%w: stripe created a customer with no id", ErrProviderRejected)
	}
	p.customers.put(customerID, cus.ID)
	return cus.ID, nil
}

// lookupCustomer finds an existing Stripe customer by the platform's own metadata.
func (p *StripeProvider) lookupCustomer(ctx context.Context, customerID string) (string, bool, error) {
	form := url.Values{}
	form.Set("query", fmt.Sprintf("metadata['%s']:'%s'", metaCustomerID, customerID))
	form.Set("limit", "1")
	res, err := p.do(ctx, http.MethodGet, "/v1/customers/search", form, "")
	if err != nil {
		return "", false, err
	}
	var out stripeSearchResult
	if err := decode(res, &out); err != nil {
		return "", false, err
	}
	for _, c := range out.Data {
		if c.ID != "" && !c.Deleted {
			return c.ID, true, nil
		}
	}
	return "", false, nil
}

// resolveCustomer maps a platform customer id to its Stripe handle for the READ paths, without ever
// creating one. A read that created a customer as a side effect would be a write pretending to be a
// read, and the reconciler runs on the read paths.
func (p *StripeProvider) resolveCustomer(ctx context.Context, customerID string) (string, error) {
	if h, ok := p.customers.get(customerID); ok {
		return h, nil
	}
	h, found, err := p.lookupCustomer(ctx, customerID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: %s", ErrNoSuchProviderCustomer, customerID)
	}
	p.customers.put(customerID, h)
	return h, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Subscriptions
// ─────────────────────────────────────────────────────────────────────────────

type stripeSubscription struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Items  struct {
		Data []stripeSubscriptionItem `json:"data"`
	} `json:"items"`
}

type stripeSubscriptionItem struct {
	ID    string `json:"id"`
	Price struct {
		ID        string `json:"id"`
		Recurring struct {
			UsageType string `json:"usage_type"`
		} `json:"recurring"`
	} `json:"price"`
}

// CreateSubscription places the customer on the plan's OPAQUE price reference.
//
// No amount is computed, stored, or returned: `PriceRef` is a Stripe price ID, Stripe applies whatever
// that price names, and proration on a later change is Stripe's (design Decision 7 and the billing
// spec's "proration and dunning are the provider's").
func (p *StripeProvider) CreateSubscription(ctx context.Context, req SubscriptionRequest) (SubscriptionResult, error) {
	if req.ProviderCustomerHandle == "" {
		return SubscriptionResult{}, fmt.Errorf("%w: a subscription needs a provider customer handle", ErrProviderRejected)
	}
	if req.PriceRef == "" {
		return SubscriptionResult{}, fmt.Errorf("%w: a subscription needs the plan's opaque price reference — the platform never sends an amount", ErrProviderRejected)
	}

	form := url.Values{}
	form.Set("customer", req.ProviderCustomerHandle)
	form.Set("items[0][price]", req.PriceRef)
	// The plan id and name are carried for Stripe's own record only. The platform's plan model stays
	// authoritative (SubscriptionRequest's comment); nothing reads these back to decide anything.
	if req.PlanID != "" {
		form.Set("metadata[platform_plan_id]", req.PlanID)
	}
	if req.PlanName != "" {
		form.Set("metadata[platform_plan_name]", req.PlanName)
	}

	res, err := p.do(ctx, http.MethodPost, "/v1/subscriptions", form, req.IdempotencyKey)
	if err != nil {
		return SubscriptionResult{}, err
	}
	var sub stripeSubscription
	if err := decode(res, &sub); err != nil {
		return SubscriptionResult{}, err
	}
	if sub.ID == "" {
		return SubscriptionResult{}, fmt.Errorf("%w: stripe returned a subscription with no id", ErrProviderRejected)
	}
	p.markSeen(req.IdempotencyKey, sub.ID, res.replayed)
	// Status is Stripe's word, carried verbatim. The platform reflects it and never recomputes it.
	return SubscriptionResult{SubscriptionRef: sub.ID, Status: sub.Status}, nil
}

// Subscription reads Stripe's own subscription state, dunning included.
func (p *StripeProvider) Subscription(ctx context.Context, subscriptionRef string) (SubscriptionResult, error) {
	if subscriptionRef == "" {
		return SubscriptionResult{}, fmt.Errorf("%w: no subscription reference", ErrProviderRejected)
	}
	sub, err := p.subscription(ctx, subscriptionRef)
	if err != nil {
		return SubscriptionResult{}, err
	}
	return SubscriptionResult{SubscriptionRef: sub.ID, Status: sub.Status}, nil
}

func (p *StripeProvider) subscription(ctx context.Context, ref string) (stripeSubscription, error) {
	res, err := p.do(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(ref), nil, "")
	if err != nil {
		return stripeSubscription{}, err
	}
	var sub stripeSubscription
	if err := decode(res, &sub); err != nil {
		return stripeSubscription{}, err
	}
	if sub.ID == "" {
		return stripeSubscription{}, fmt.Errorf("%w: no such subscription %s", ErrProviderRejected, ref)
	}
	return sub, nil
}

// meteredItem finds the subscription item a metered report belongs on.
//
// It matches on the plan's `price_ref` when one is given, and falls back to the single metered item when
// the subscription has exactly one. It does NOT guess between several: reporting usage against the wrong
// item bills the right quantity at the wrong price, which reconciliation would show as agreement.
func meteredItem(sub stripeSubscription, priceRef string) (string, error) {
	if priceRef != "" {
		for _, it := range sub.Items.Data {
			if it.Price.ID == priceRef {
				return it.ID, nil
			}
		}
	}
	var metered []string
	for _, it := range sub.Items.Data {
		if it.Price.Recurring.UsageType == "metered" {
			metered = append(metered, it.ID)
		}
	}
	if len(metered) == 1 {
		return metered[0], nil
	}
	if priceRef != "" {
		return "", fmt.Errorf("%w: subscription %s has no item on price %s", ErrProviderRejected, sub.ID, priceRef)
	}
	return "", fmt.Errorf("%w: subscription %s has %d metered items and the report names no price reference — refusing to guess which one to bill",
		ErrProviderRejected, sub.ID, len(metered))
}

// ─────────────────────────────────────────────────────────────────────────────
// Collection (P21 tasks 6.1, 6.2) — the capability P7 left out
// ─────────────────────────────────────────────────────────────────────────────

type stripeCheckoutSession struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	ClientSecret string `json:"client_secret"`
	ExpiresAt    int64  `json:"expires_at"`
	Status       string `json:"status"`
}

// CreateCheckoutSession mints a Stripe Checkout session server-side.
//
// 🔴 This is design Decision 2 in one method. What comes back is a URL (hosted Checkout) or a client
// secret (the embedded Payment Element) — never a form the platform renders and never a field the
// platform reads. The card travels browser→Stripe, so "the platform never stores a card" is a
// STRUCTURAL fact rather than a policy: there is no code path here that could receive one.
//
// The session is short-lived and single-purpose. `customer` binds it to a Stripe customer the platform
// already holds a handle for, so a session cannot be re-pointed at somebody else's account.
func (p *StripeProvider) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (CheckoutSession, error) {
	if req.ProviderCustomerHandle == "" {
		return CheckoutSession{}, fmt.Errorf("%w: a checkout session needs a provider customer handle", ErrProviderRejected)
	}
	if req.PriceRef == "" {
		return CheckoutSession{}, fmt.Errorf("%w: a checkout session needs the plan's opaque price reference — the platform never sends an amount", ErrProviderRejected)
	}
	if req.SuccessURL == "" || req.CancelURL == "" {
		return CheckoutSession{}, fmt.Errorf("%w: a checkout session needs both a success and a cancel return URL — a customer who abandons checkout must land somewhere the product chose", ErrProviderRejected)
	}

	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("customer", req.ProviderCustomerHandle)
	form.Set("line_items[0][price]", req.PriceRef)
	form.Set("line_items[0][quantity]", "1")
	form.Set("success_url", req.SuccessURL)
	form.Set("cancel_url", req.CancelURL)
	// The plan travels on the SUBSCRIPTION the session creates, which is what the entitlement sync reads
	// off the lifecycle event later. Without it, `invoice.paid` would arrive naming no plan.
	if req.PlanID != "" {
		form.Set("subscription_data[metadata][platform_plan_id]", req.PlanID)
		form.Set("metadata[platform_plan_id]", req.PlanID)
	}
	if req.CustomerID != "" {
		form.Set("subscription_data[metadata]["+metaCustomerID+"]", req.CustomerID)
		form.Set("metadata["+metaCustomerID+"]", req.CustomerID)
	}

	res, err := p.do(ctx, http.MethodPost, "/v1/checkout/sessions", form, req.IdempotencyKey)
	if err != nil {
		return CheckoutSession{}, err
	}
	var s stripeCheckoutSession
	if err := decode(res, &s); err != nil {
		return CheckoutSession{}, err
	}
	if s.ID == "" || (s.URL == "" && s.ClientSecret == "") {
		return CheckoutSession{}, fmt.Errorf("%w: stripe returned a checkout session with nowhere to send the browser", ErrProviderRejected)
	}
	p.markSeen(req.IdempotencyKey, s.ID, res.replayed)

	out := CheckoutSession{SessionRef: s.ID, URL: s.URL, ClientSecret: s.ClientSecret}
	if s.ExpiresAt > 0 {
		out.ExpiresAt = time.Unix(s.ExpiresAt, 0).UTC()
	}
	return out, nil
}

// UpdateSubscriptionPrice moves an existing subscription onto a different plan's price reference.
//
// Proration is STRIPE'S. `proration_behavior=create_prorations` asks Stripe to do what it does; the
// platform neither computes nor stores the result, which is why there is no amount anywhere in this
// method or in what it returns.
func (p *StripeProvider) UpdateSubscriptionPrice(ctx context.Context, req UpdateSubscriptionRequest) (SubscriptionResult, error) {
	if req.SubscriptionRef == "" || req.PriceRef == "" {
		return SubscriptionResult{}, fmt.Errorf("%w: a plan change needs the subscription reference and the new plan's price reference", ErrProviderRejected)
	}
	sub, err := p.subscription(ctx, req.SubscriptionRef)
	if err != nil {
		return SubscriptionResult{}, err
	}
	// The item being repriced is the recurring one. Repricing a METERED item would silently re-rate the
	// period's usage, so the licensed item is selected explicitly rather than by position.
	item := ""
	for _, it := range sub.Items.Data {
		if it.Price.Recurring.UsageType != "metered" {
			item = it.ID
			break
		}
	}
	if item == "" {
		return SubscriptionResult{}, fmt.Errorf("%w: subscription %s has no non-metered item to reprice", ErrProviderRejected, sub.ID)
	}

	form := url.Values{}
	form.Set("items[0][id]", item)
	form.Set("items[0][price]", req.PriceRef)
	form.Set("proration_behavior", "create_prorations")
	if req.PlanID != "" {
		form.Set("metadata[platform_plan_id]", req.PlanID)
	}
	if req.PlanName != "" {
		form.Set("metadata[platform_plan_name]", req.PlanName)
	}

	res, err := p.do(ctx, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(req.SubscriptionRef), form, req.IdempotencyKey)
	if err != nil {
		return SubscriptionResult{}, err
	}
	var updated stripeSubscription
	if err := decode(res, &updated); err != nil {
		return SubscriptionResult{}, err
	}
	p.markSeen(req.IdempotencyKey, updated.ID, res.replayed)
	return SubscriptionResult{SubscriptionRef: updated.ID, Status: updated.Status}, nil
}

// Compile-time proof that the Stripe provider carries the optional collection capability.
var _ CollectionProvider = (*StripeProvider)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// Metered usage
// ─────────────────────────────────────────────────────────────────────────────

type stripeUsageRecord struct {
	ID string `json:"id"`
}

// ReportUsage reports a metered QUANTITY for {customer, period, metric} to Stripe's metered item.
//
// It multiplies nothing. Stripe applies the price the `price_ref` names; the platform hands over a
// count and holds no amount at any point in this method.
//
// `action=set` rather than `increment`: the P7 usage record is an UPSERT of "what was used this period",
// so a re-report of the same {customer, period, metric} must converge on the same recorded total rather
// than accumulate. With `increment`, a retry after an ambiguous failure would double the usage — the
// idempotency key covers the retry Stripe sees, and `set` covers the one it does not.
func (p *StripeProvider) ReportUsage(ctx context.Context, req UsageReport) (UsageResult, error) {
	if req.SubscriptionRef == "" {
		return UsageResult{}, fmt.Errorf("%w: metered usage is reported against a subscription item, and no subscription reference was given", ErrProviderRejected)
	}
	qty, err := stripeQuantity(req.Quantity)
	if err != nil {
		return UsageResult{}, err
	}
	sub, err := p.subscription(ctx, req.SubscriptionRef)
	if err != nil {
		return UsageResult{}, err
	}
	item, err := meteredItem(sub, req.PriceRef)
	if err != nil {
		return UsageResult{}, err
	}

	// The timestamp lands inside the period being reported, so Stripe attributes the usage to the same
	// period the platform's usage record does. `p.now()` would attribute a late report to the wrong
	// period — the exact drift the reconciler would then surface as a mystery.
	ts := periodTimestamp(req.Period, p.now())

	form := url.Values{}
	form.Set("quantity", strconv.FormatInt(qty, 10))
	form.Set("timestamp", strconv.FormatInt(ts.Unix(), 10))
	form.Set("action", "set")

	res, err := p.do(ctx, http.MethodPost, "/v1/subscription_items/"+url.PathEscape(item)+"/usage_records", form, req.IdempotencyKey)
	if err != nil {
		return UsageResult{}, err
	}
	var rec stripeUsageRecord
	if err := decode(res, &rec); err != nil {
		return UsageResult{}, err
	}
	if rec.ID == "" {
		return UsageResult{}, fmt.Errorf("%w: stripe returned a usage record with no id", ErrProviderRejected)
	}
	dup := p.markSeen(req.IdempotencyKey, rec.ID, res.replayed)
	return UsageResult{UsageRef: rec.ID, Duplicate: dup}, nil
}

// periodTimestamp returns a timestamp inside the named period, or `fallback` when the period is not a
// parseable month key. It is deliberately the period's START: any instant in the period attributes the
// usage correctly, and the start is the only one that cannot land after the period closed.
func periodTimestamp(period string, fallback time.Time) time.Time {
	t, err := time.ParseInLocation("2006-01", period, time.UTC)
	if err != nil {
		return fallback.UTC()
	}
	return t
}

// periodKey maps a Stripe timestamp back to the platform's period key. It mirrors
// metering.MonthPeriod's calendar-month-in-UTC convention, which is what every usage record is keyed by.
func periodKey(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01")
}

// ─────────────────────────────────────────────────────────────────────────────
// Charges
// ─────────────────────────────────────────────────────────────────────────────

type stripeInvoiceItem struct {
	ID       string            `json:"id"`
	Invoice  string            `json:"invoice"`
	Metadata map[string]string `json:"metadata"`
}

// RaiseCharge raises one charge as a Stripe invoice item on the customer's next invoice.
//
// An invoice item rather than a direct charge, because every charge in this system belongs to a
// customer-period invoice a customer can read line by line — a bare charge would be money with no line
// to explain it, which is the opposite of what P7's invoice surface promises.
//
// `AmountRef` is a POINTER at where the amount lives in Stripe, never a value: the platform must be able
// to point AT an amount without holding one (ChargeResult's comment).
func (p *StripeProvider) RaiseCharge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	if req.ProviderCustomerHandle == "" {
		return ChargeResult{}, fmt.Errorf("%w: a charge needs a provider customer handle", ErrProviderRejected)
	}
	if !KnownChargeKind(req.Kind) {
		return ChargeResult{}, fmt.Errorf("%w: refusing a charge of unknown kind %q — the kinds are a closed set so a resold-token line cannot be invented at a call site", ErrProviderRejected, req.Kind)
	}
	if req.PriceRef == "" {
		return ChargeResult{}, fmt.Errorf("%w: a charge needs the plan's opaque price reference — the platform never sends an amount", ErrProviderRejected)
	}
	qty, err := stripeQuantity(req.Quantity)
	if err != nil {
		return ChargeResult{}, err
	}

	form := url.Values{}
	form.Set("customer", req.ProviderCustomerHandle)
	form.Set("price", req.PriceRef)
	form.Set("quantity", strconv.FormatInt(qty, 10))
	if req.Description != "" {
		form.Set("description", req.Description)
	}
	// The platform's own facts, stamped on the Stripe object. They are what makes the read-back invoice
	// able to name a basis for every line (Invoice.Validate), and what a reconciliation joins on.
	form.Set("metadata["+metaKind+"]", string(req.Kind))
	form.Set("metadata["+metaPeriod+"]", req.Period)
	if req.Description != "" {
		form.Set("metadata["+metaBasis+"]", req.Description)
	}

	res, err := p.do(ctx, http.MethodPost, "/v1/invoiceitems", form, req.IdempotencyKey)
	if err != nil {
		return ChargeResult{}, err
	}
	var item stripeInvoiceItem
	if err := decode(res, &item); err != nil {
		return ChargeResult{}, err
	}
	if item.ID == "" {
		return ChargeResult{}, fmt.Errorf("%w: stripe returned an invoice item with no id", ErrProviderRejected)
	}
	dup := p.markSeen(req.IdempotencyKey, item.ID, res.replayed)
	return ChargeResult{ChargeRef: item.ID, AmountRef: amountRef(item.ID), Duplicate: dup}, nil
}

// amountRef builds the opaque handle that POINTS AT an amount in Stripe. It is a reference by
// construction — there is no code path here that could put a number in it.
func amountRef(objectID string) string { return "stripe:amount:" + objectID }

// ─────────────────────────────────────────────────────────────────────────────
// Additive corrections
// ─────────────────────────────────────────────────────────────────────────────

type stripeRefund struct {
	ID string `json:"id"`
}

type stripeCreditNote struct {
	ID string `json:"id"`
}

// IssueCredit issues an ADDITIVE Stripe credit note, or a refund when `Refund` is set.
//
// 🔴 Nothing here reduces, voids, or deletes the original object, and there is no branch that could: the
// only Stripe calls this method can make are `POST /v1/credit_notes` and `POST /v1/refunds`, both of
// which CREATE a new object referencing the original. That is design Decision 8 expressed as
// reachability rather than as a policy sentence — an auditor replaying the ledger sees both the mistake
// and its correction, which is precisely what they need to see.
func (p *StripeProvider) IssueCredit(ctx context.Context, req CreditRequest) (CreditResult, error) {
	if req.ProviderCustomerHandle == "" {
		return CreditResult{}, fmt.Errorf("%w: a correction needs a provider customer handle", ErrProviderRejected)
	}
	if req.AgainstRef == "" {
		return CreditResult{}, fmt.Errorf("%w: a correction must name the charge it corrects — a credit that names nothing cannot be reconciled against anything", ErrProviderRejected)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return CreditResult{}, fmt.Errorf("%w: a correction must carry a reason", ErrProviderRejected)
	}

	if req.Refund {
		form := url.Values{}
		form.Set("charge", req.AgainstRef)
		form.Set("metadata["+metaBasis+"]", req.Reason)
		res, err := p.do(ctx, http.MethodPost, "/v1/refunds", form, req.IdempotencyKey)
		if err != nil {
			return CreditResult{}, err
		}
		var r stripeRefund
		if err := decode(res, &r); err != nil {
			return CreditResult{}, err
		}
		if r.ID == "" {
			return CreditResult{}, fmt.Errorf("%w: stripe returned a refund with no id", ErrProviderRejected)
		}
		dup := p.markSeen(req.IdempotencyKey, r.ID, res.replayed)
		return CreditResult{CreditRef: r.ID, AmountRef: amountRef(r.ID), Duplicate: dup}, nil
	}

	// A credit note is issued against the INVOICE the corrected line sits on, and names that line. The
	// invoice is resolved from the item rather than passed in, so a caller cannot credit the wrong
	// invoice by supplying one.
	invoiceRef, err := p.invoiceOfItem(ctx, req.AgainstRef)
	if err != nil {
		return CreditResult{}, err
	}
	form := url.Values{}
	form.Set("invoice", invoiceRef)
	form.Set("lines[0][type]", "invoice_line_item")
	form.Set("lines[0][invoice_line_item]", req.AgainstRef)
	form.Set("memo", req.Reason)
	form.Set("metadata["+metaBasis+"]", req.Reason)
	res, err := p.do(ctx, http.MethodPost, "/v1/credit_notes", form, req.IdempotencyKey)
	if err != nil {
		return CreditResult{}, err
	}
	var cn stripeCreditNote
	if err := decode(res, &cn); err != nil {
		return CreditResult{}, err
	}
	if cn.ID == "" {
		return CreditResult{}, fmt.Errorf("%w: stripe returned a credit note with no id", ErrProviderRejected)
	}
	dup := p.markSeen(req.IdempotencyKey, cn.ID, res.replayed)
	return CreditResult{CreditRef: cn.ID, AmountRef: amountRef(cn.ID), Duplicate: dup}, nil
}

// invoiceOfItem resolves the invoice a charge sits on.
func (p *StripeProvider) invoiceOfItem(ctx context.Context, itemRef string) (string, error) {
	res, err := p.do(ctx, http.MethodGet, "/v1/invoiceitems/"+url.PathEscape(itemRef), nil, "")
	if err != nil {
		return "", err
	}
	var item stripeInvoiceItem
	if err := decode(res, &item); err != nil {
		return "", err
	}
	if item.ID == "" {
		return "", fmt.Errorf("%w: cannot credit unknown charge %s", ErrProviderRejected, itemRef)
	}
	if item.Invoice == "" {
		return "", fmt.Errorf("%w: charge %s is not on an invoice yet — an uninvoiced item is resolved by not invoicing it, not by crediting it", ErrProviderRejected, itemRef)
	}
	return item.Invoice, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Read-back: invoices and recorded usage
// ─────────────────────────────────────────────────────────────────────────────

type stripeInvoiceList struct {
	Data []stripeInvoiceObject `json:"data"`
}

type stripeInvoiceObject struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Lines  struct {
		Data []stripeInvoiceLine `json:"data"`
	} `json:"lines"`
}

type stripeInvoiceLine struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Quantity    float64           `json:"quantity"`
	Metadata    map[string]string `json:"metadata"`
	Type        string            `json:"type"`
	Price       struct {
		ID string `json:"id"`
	} `json:"price"`
	Period struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"period"`
	Proration bool `json:"proration"`
}

// Invoice reads a Stripe invoice back as a billing.Invoice.
//
// Two properties matter more than the mapping itself, and both are asserted by the caller through
// [Invoice.Validate]:
//
//  1. **Every line names a basis.** The platform's own basis is read from the object's metadata where
//     the platform created it; a line Stripe generated itself (the recurring subscription line) is given
//     a basis that names the Stripe object behind it. Nothing renders as an unexplained figure.
//  2. **A resold-token line is REJECTED, not rendered.** The kind comes from metadata, and a metadata
//     value that is a token-passthrough shape fails Validate — so a Stripe-side misconfiguration
//     surfaces as an error rather than as a line a customer is shown as if it were understood.
func (p *StripeProvider) Invoice(ctx context.Context, customerID, period string) (Invoice, error) {
	handle, err := p.resolveCustomer(ctx, customerID)
	if err != nil {
		return Invoice{}, err
	}
	form := url.Values{}
	form.Set("customer", handle)
	form.Set("limit", "100")
	res, err := p.do(ctx, http.MethodGet, "/v1/invoices", form, "")
	if err != nil {
		return Invoice{}, err
	}
	var list stripeInvoiceList
	if err := decode(res, &list); err != nil {
		return Invoice{}, err
	}

	out := Invoice{CustomerID: customerID, Period: period}
	for _, inv := range list.Data {
		lines := make([]InvoiceLine, 0, len(inv.Lines.Data))
		for _, l := range inv.Lines.Data {
			if linePeriod(l) != period {
				continue
			}
			lines = append(lines, mapInvoiceLine(inv.ID, l))
		}
		if len(lines) == 0 {
			continue
		}
		// The first invoice carrying lines for this period IS the period's invoice; a later one is
		// merged into it rather than replacing it, so a period split across two Stripe invoices (a
		// mid-period plan change does this) still reads back as one period.
		if out.InvoiceRef == "" {
			out.InvoiceRef, out.Status = inv.ID, inv.Status
		}
		out.Lines = append(out.Lines, lines...)
	}
	sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].ChargeRef < out.Lines[j].ChargeRef })

	// Validate on the READ path, not only at the render: a token line must never get far enough to be
	// rendered as if it were understood (spec: "a resold-token line is rejected on read-back").
	if err := out.Validate(); err != nil {
		return Invoice{}, err
	}
	return out, nil
}

// linePeriod is the platform period a Stripe line belongs to: the platform's own stamp where it exists,
// otherwise the calendar month of Stripe's line period. The stamp wins because it is the platform's
// fact; the derivation covers the lines Stripe authored itself.
func linePeriod(l stripeInvoiceLine) string {
	if v := l.Metadata[metaPeriod]; v != "" {
		return v
	}
	if l.Period.Start > 0 {
		return periodKey(l.Period.Start)
	}
	return ""
}

// mapInvoiceLine maps one Stripe line to the platform's view of it.
func mapInvoiceLine(invoiceID string, l stripeInvoiceLine) InvoiceLine {
	kind := LineKind(l.Metadata[metaKind])
	if kind == "" {
		// A line the platform did not create is Stripe's own recurring subscription line. Naming it
		// `subscription` is a mapping, not a guess: it is the only line Stripe authors on its own for
		// this integration, and calling it anything else would be inventing a kind.
		kind = LineSubscription
	}
	basis := l.Metadata[metaBasis]
	if basis == "" {
		// 🔴 Never empty. Invoice.Validate rejects a line that names no basis, and it is right to: a
		// figure a customer cannot trace to anything is the figure a billing dispute is made of. Where
		// the platform stamped none, the basis is the Stripe object itself, which is a true and
		// checkable answer to "why is this line here".
		basis = "stripe_invoice_line:" + invoiceID + "/" + l.ID
		if l.Price.ID != "" {
			basis += "@" + l.Price.ID
		}
	}
	desc := l.Description
	if desc == "" {
		desc = basis
	}
	return InvoiceLine{
		Kind:        kind,
		Basis:       basis,
		AmountRef:   amountRef(l.ID),
		Quantity:    l.Quantity,
		Description: desc,
		ChargeRef:   l.ID,
	}
}

type stripeUsageSummaryList struct {
	Data []stripeUsageSummary `json:"data"`
}

type stripeUsageSummary struct {
	ID     string `json:"id"`
	Period struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"period"`
	TotalUsage float64 `json:"total_usage"`
	Item       string  `json:"subscription_item"`
}

// RecordedUsage returns what STRIPE says it recorded for a customer-period — the reconciler's right-hand
// side.
//
// The provider is the system of record for "what was CHARGED"; `usage_record` is the platform's system
// of record for "what was USED" (P7 design Decision 7). This method is deliberately read-only and takes
// no write path: the reconciler is structurally incapable of repairing by overwrite.
func (p *StripeProvider) RecordedUsage(ctx context.Context, customerID, period string) ([]RecordedUsage, error) {
	handle, err := p.resolveCustomer(ctx, customerID)
	if err != nil {
		return nil, err
	}
	subs, err := p.customerSubscriptions(ctx, handle)
	if err != nil {
		return nil, err
	}

	var out []RecordedUsage
	for _, sub := range subs {
		for _, it := range sub.Items.Data {
			if it.Price.Recurring.UsageType != "metered" {
				continue
			}
			form := url.Values{}
			form.Set("limit", "100")
			res, err := p.do(ctx, http.MethodGet, "/v1/subscription_items/"+url.PathEscape(it.ID)+"/usage_record_summaries", form, "")
			if err != nil {
				return nil, err
			}
			var list stripeUsageSummaryList
			if err := decode(res, &list); err != nil {
				return nil, err
			}
			for _, s := range list.Data {
				if periodKey(s.Period.Start) != period {
					continue
				}
				out = append(out, RecordedUsage{
					// The metric is the platform's, and the price reference is what ties a Stripe item to
					// the plan's metered meter. Reporting the price ref as the metric would be reporting
					// Stripe's vocabulary as the platform's.
					Metric:   meterNameFor(it.Price.ID),
					Period:   period,
					Quantity: s.TotalUsage,
					UsageRef: s.ID,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metric < out[j].Metric })
	return out, nil
}

// meterNameFor maps a Stripe price reference to the platform meter it backs.
//
// The platform reports exactly one metered meter to Stripe today — SUM — so this is a statement of that
// fact rather than a lookup table waiting to be filled in. When a second metered meter is configured,
// this becomes a plan-config read, and the reconciler is the test that catches it if it is not.
func meterNameFor(string) string { return "sum" }

func (p *StripeProvider) customerSubscriptions(ctx context.Context, handle string) ([]stripeSubscription, error) {
	form := url.Values{}
	form.Set("customer", handle)
	form.Set("limit", "100")
	// `all` rather than the default: a canceled subscription still carries the usage of the period it
	// was canceled in, and a reconciliation that could not see it would report a drift that is not one.
	form.Set("status", "all")
	res, err := p.do(ctx, http.MethodGet, "/v1/subscriptions", form, "")
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []stripeSubscription `json:"data"`
	}
	if err := decode(res, &list); err != nil {
		return nil, err
	}
	return list.Data, nil
}

// Compile-time proof of design Decision 1: the Stripe implementation satisfies the EXISTING interface.
// If Stripe ever needed a method the interface does not have, this line would fail — which is the
// conversation the decision wants to force, rather than a widened interface nobody reviewed.
var _ Provider = (*StripeProvider)(nil)
