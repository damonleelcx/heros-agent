package stripefake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Package stripefake is an in-process Stripe: the server the P21 tests and the P21 demo talk to.
//
// 🔴 It is NOT importable by the product. `internal/billing` does not depend on it, and
// fence_test.go asserts that only tests and cmd/ demos do — a fake reachable from a shipping code path
// is the "mock in production" failure, and the way to prevent it is a gate rather than a convention.
//
// It is NOT a mock of `StripeProvider`. It is an HTTP server that behaves the way Stripe's API behaves:
// form-encoded requests, JSON objects, bearer auth, and — the part everything else rests on — REAL
// IDEMPOTENCY. A repeated `Idempotency-Key` replays the stored response and creates nothing; a key
// presented for a DIFFERENT operation is refused the way Stripe refuses it.
//
// Why this shape rather than a `Provider` mock: a mock proves that stripe.go calls methods, which is
// not in doubt. What can actually be wrong is the HTTP conversation — a missing header, a wrong form
// key, a status misclassified as an outage — and only a server can test that. The never-double-charge
// claim in particular is meaningless against a mock: it is a claim about what the PROVIDER does with a
// repeated key, so the thing under test has to be something that can decline to create a second object.
//
// It also models the two failure shapes the design cares about, exactly as StubProvider does:
//
//   - SetDown: an outage. Every call answers 503, which stripe.go must classify as
//     ErrProviderUnavailable so the P7 outage buffer takes over.
//   - SetFailAfterRecord: the AMBIGUOUS failure. The object IS created and then the response is lost.
//     This is the nastiest case in billing and the only way to prove a retry does not double-charge is
//     to be able to produce it on demand.

// The platform metadata keys, spelled out as LITERALS rather than imported from the billing package.
//
// That is deliberate. A fake that shares its constants with the code under test cannot catch a rename:
// both sides move together and the wire contract silently changes. Written out here, this file is a
// second, independent statement of what the wire looks like — which is the only way the test can fail
// when the wire changes.
const (
	metaCustomerID = "platform_customer_id"
	metaPeriod     = "platform_period"
	metaKind       = "platform_kind"
	metaBasis      = "platform_basis"
	// stripeAPIVersion is the version the provider pins. The fake REFUSES anything else, so an unpinned
	// or drifted integration fails loudly here rather than against the real API.
	//
	// It is written out here rather than imported from `billing` on purpose, and this is the moment that
	// justified it: when the real account forced the pin forward, this constant had to move too, and the
	// suite went red until it did. A shared constant would have moved silently on both sides and proved
	// nothing about the wire.
	stripeAPIVersion = "2025-03-31.basil"
)

const (
	// TestKey and LiveKey are placeholders shaped like a Stripe key WITHOUT being one: the run after the
	// prefix contains underscores, so the repository's credential fence does not match them and this file
	// needs no exemption from it.
	TestKey = "sk_test_p21_fake_key_not_a_secret"
	LiveKey = "sk_live_p21_fake_key_not_a_secret"
)

// Server is an in-process Stripe.
type Server struct {
	mu  sync.Mutex
	srv *httptest.Server
	seq int

	customers  map[string]map[string]any // stripe customer id -> object
	byPlatform map[string]string         // platform customer id -> stripe customer id
	subs       map[string]*fakeSub
	items      map[string]*fakeItem // invoice item id -> item
	invoices   map[string]*fakeInvoice
	credits    map[string]map[string]any
	refunds    map[string]map[string]any
	// meters are the account's billing meters: event name -> meter id.
	meters map[string]string
	// meterEvents is the identifier dedupe set. Stripe refuses a repeated identifier outright, and
	// that refusal is what makes a retried usage report converge instead of accumulating.
	meterEvents map[string]bool
	// meterUsage is event name -> customer handle -> period key -> aggregated total.
	meterUsage map[string]map[string]map[string]float64
	// prices are the price references this account knows.
	prices map[string]fakePrice
	// owner maps a subscription to its customer. Beside the subs map rather than on fakeSub so the
	// object serializer stays a pure function of fakeSub.
	owner map[string]string

	// idem is Stripe's idempotency store: key -> the operation that claimed it and the response it
	// produced. Storing the RESPONSE (not just the id) is what makes a replay indistinguishable from the
	// original to the caller, which is the property the provider relies on.
	idem map[string]idemEntry

	// calls counts effect-producing requests, so a test can prove the provider genuinely asked more than
	// once and Stripe still recorded one thing.
	calls map[string]int

	down            bool
	failAfterRecord bool
	// requireLiveKey makes the fake behave like a LIVE Stripe account: a test key is refused.
	requireLiveKey bool
	// seenAuth records every Authorization header value, so a test can assert which key was presented.
	seenAuth []string
	// seenIdem records every Idempotency-Key header, so a test can assert the P7 key reached the wire.
	seenIdem []string
}

// fakePrice is a seeded price. The TYPE is carried because it is the half of a price reference that
// a preflight cannot infer and that Stripe enforces at the moment of charge: an invoice item accepts
// only `one_time`, a subscription only `recurring`. A fake that modelled existence but not type would
// wave through the exact misconfiguration the preflight was extended to catch.
type fakePrice struct {
	active bool
	typ    string
}

type idemEntry struct {
	op     string
	status int
	body   []byte
}

type fakeSub struct {
	id     string
	status string
	items  []fakeSubItem
}

type fakeSubItem struct {
	id        string
	priceRef  string
	usageType string
}

type fakeItem struct {
	id string
	// lineID is the id of the INVOICE LINE this item produces, and it is deliberately a different
	// string from id. Stripe's are different too, and a fake that reused one id for both would let a
	// provider send the wrong one and still pass — which is exactly how the credit-note path stayed
	// broken until a real account refused it.
	lineID   string
	invoice  string
	customer string
	price    string
	quantity float64
	desc     string
	metadata map[string]string
}

type fakeInvoice struct {
	id       string
	customer string
	status   string
	// extra lines Stripe authored itself (the recurring subscription line), which carry no platform
	// metadata and exercise the read-back's basis fallback.
	own []fakeOwnLine
}

type fakeOwnLine struct {
	id          string
	priceRef    string
	quantity    float64
	description string
	periodStart int64
}

// New starts an in-process Stripe. Close it when done.
func New() *Server {
	f := &Server{
		customers: map[string]map[string]any{}, byPlatform: map[string]string{},
		subs: map[string]*fakeSub{}, items: map[string]*fakeItem{}, invoices: map[string]*fakeInvoice{},
		credits: map[string]map[string]any{},
		meters:  map[string]string{}, meterEvents: map[string]bool{},
		meterUsage: map[string]map[string]map[string]float64{},
		refunds:    map[string]map[string]any{}, idem: map[string]idemEntry{}, calls: map[string]int{},
		owner: map[string]string{}, prices: map[string]fakePrice{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// Close shuts the server down.
func (f *Server) Close() { f.srv.Close() }

func (f *Server) URL() string { return f.srv.URL }

func (f *Server) SetDown(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = v
}

func (f *Server) SetFailAfterRecord(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAfterRecord = v
}

func (f *Server) SetRequireLiveKey(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requireLiveKey = v
}

func (f *Server) Calls(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

// ItemCount is how many DISTINCT invoice items Stripe recorded — the number a never-double-charge test
// asserts on.
func (f *Server) ItemCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

func (f *Server) CreditCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.credits) + len(f.refunds)
}

func (f *Server) IdempotencyKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seenIdem...)
}

func (f *Server) AuthHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seenAuth...)
}

// Item is a recorded invoice item, as a caller outside this package sees it. The fields a test asserts
// on are exported; the internal bookkeeping is not.
type Item struct {
	ID       string
	Invoice  string
	Price    string
	Quantity float64
	Metadata map[string]string
}

// Item returns a recorded invoice item.
func (f *Server) Item(id string) (Item, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return Item{}, false
	}
	return Item{ID: it.id, Invoice: it.invoice, Price: it.price, Quantity: it.quantity, Metadata: it.metadata}, true
}

// Usage returns the total Stripe holds for a subscription item in a period.
func (f *Server) next(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_%06d", prefix, f.seq)
}

// SeedSubscription installs a subscription with a flat item and a metered item, as a Stripe account
// configured for this product would have. Test setup, not a code path.
func (f *Server) SeedSubscription(platformCustomerID, subPrice, meteredPrice string) (customerHandle, subRef, meteredItemID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cus := f.next("cus")
	f.customers[cus] = map[string]any{"id": cus, "object": "customer",
		"metadata": map[string]string{metaCustomerID: platformCustomerID}}
	f.byPlatform[platformCustomerID] = cus

	sub := &fakeSub{id: f.next("sub"), status: "active"}
	sub.items = append(sub.items, fakeSubItem{id: f.next("si"), priceRef: subPrice, usageType: "licensed"})
	mi := fakeSubItem{id: f.next("si"), priceRef: meteredPrice, usageType: "metered"}
	sub.items = append(sub.items, mi)
	f.subs[sub.id] = sub
	f.owner[sub.id] = cus
	return cus, sub.id, mi.id
}

// getPrice resolves a price reference. Only SEEDED prices resolve — a fake that resolved anything would
// make the preflight vacuous, which is the one thing a configuration check must never be.
func (f *Server) getPrice(id string) (int, []byte) {
	pr, known := f.prices[id]
	if !known {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such price: "+id)
	}
	return okBody(map[string]any{"id": id, "object": "price", "active": pr.active, "type": pr.typ})
}

// SeedPrice registers a price the account knows, with no declared type. `active=false` models an
// ARCHIVED price: it resolves and cannot be charged on, which is exactly the case a local shape check
// would wave through.
func (f *Server) SeedPrice(id string, active bool) {
	f.SeedPriceOfType(id, active, "")
}

// SeedPriceOfType registers a price WITH its Stripe type — `one_time` or `recurring`.
//
// Separate from SeedPrice rather than a wider signature on it, because the untyped form models a real
// state as well: a price whose type this test does not care about. What the two must never collapse
// into is a fake that assumes a type, since the assumption would be right exactly as often as the
// configuration it is supposed to be checking.
func (f *Server) SeedPriceOfType(id string, active bool, typ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prices[id] = fakePrice{active: active, typ: typ}
}

// SeedCustomerHandle registers a customer under a handle the PLATFORM already holds.
//
// It models the ordinary case rather than a convenience: an account created before this process
// started already carries a provider handle, and a test that had to mint a fresh one would be testing
// a path only a brand-new customer takes.
func (f *Server) SeedCustomerHandle(handle, platformCustomerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customers[handle] = map[string]any{"id": handle, "object": "customer",
		"metadata": map[string]string{metaCustomerID: platformCustomerID}}
	f.byPlatform[platformCustomerID] = handle
}

// SeedSubscriptionFor attaches a subscription with a flat and a metered item to an existing customer.
func (f *Server) SeedSubscriptionFor(handle, subPrice, meteredPrice string) (subRef, meteredItemID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sub := &fakeSub{id: f.next("sub"), status: "active"}
	sub.items = append(sub.items, fakeSubItem{id: f.next("si"), priceRef: subPrice, usageType: "licensed"})
	mi := fakeSubItem{id: f.next("si"), priceRef: meteredPrice, usageType: "metered"}
	sub.items = append(sub.items, mi)
	f.subs[sub.id] = sub
	f.owner[sub.id] = handle
	return sub.id, mi.id
}

// SeedOwnInvoiceLine adds a line Stripe authored itself — the recurring subscription line, which carries
// no platform metadata. It exists so the read-back's "every line names a basis" fallback is exercised by
// a line the platform did not create, which is the only kind that can reach it.
func (f *Server) SeedOwnInvoiceLine(customerHandle, period, priceRef, description string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv := f.invoiceFor(customerHandle)
	start, _ := time.ParseInLocation("2006-01", period, time.UTC)
	inv.own = append(inv.own, fakeOwnLine{
		id: f.next("il"), priceRef: priceRef, quantity: 1, description: description, periodStart: start.Unix(),
	})
}

// SeedTokenLine seeds the misconfiguration the no-resale rule exists for: a Stripe line whose kind is a
// resold-token shape. The platform's own enum cannot produce one — that is the point — so the only way
// to test the read path's refusal is to author it on Stripe's side.
func (f *Server) SeedTokenLine(customerHandle, period, kind, description string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv := f.invoiceFor(customerHandle)
	it := &fakeItem{
		id: f.next("ii"), lineID: f.next("il"), invoice: inv.id, customer: customerHandle, price: "price_ref_tokens",
		quantity: 1, desc: description,
		metadata: map[string]string{metaKind: kind, metaPeriod: period, metaBasis: description},
	}
	f.items[it.id] = it
}

// invoiceFor returns the customer's open invoice, creating it on first use. Callers hold f.mu.
func (f *Server) invoiceFor(customer string) *fakeInvoice {
	for _, inv := range f.invoices {
		if inv.customer == customer {
			return inv
		}
	}
	// A new invoice is a DRAFT, as Stripe's is. Starting it `open` would skip the finalize step
	// entirely on this side and leave the provider's draft handling unexercised — which is how the
	// credit-note path passed here while a real Stripe refused it.
	inv := &fakeInvoice{id: f.next("in"), customer: customer, status: "draft"}
	f.invoices[inv.id] = inv
	return inv
}

// ── the HTTP surface ────────────────────────────────────────────────────────

func (f *Server) handle(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	form := r.PostForm
	if r.Method == http.MethodGet {
		form = r.URL.Query()
	}

	f.mu.Lock()
	f.seenAuth = append(f.seenAuth, r.Header.Get("Authorization"))
	if k := r.Header.Get("Idempotency-Key"); k != "" {
		f.seenIdem = append(f.seenIdem, k)
	}
	down, requireLive := f.down, f.requireLiveKey
	f.mu.Unlock()

	if down {
		// A 503 is an OUTAGE: retryable, and the provider must classify it as one.
		writeStripeError(w, http.StatusServiceUnavailable, "api_error", "lock_timeout", "stripe is temporarily unavailable")
		return
	}

	auth := r.Header.Get("Authorization")
	switch {
	case !strings.HasPrefix(auth, "Bearer "):
		writeStripeError(w, http.StatusUnauthorized, "invalid_request_error", "", "no api key provided")
		return
	case requireLive && !strings.Contains(auth, "sk_live_"):
		writeStripeError(w, http.StatusUnauthorized, "invalid_request_error", "", "this account is in live mode and a test key was supplied")
		return
	case !requireLive && strings.Contains(auth, "sk_live_"):
		writeStripeError(w, http.StatusUnauthorized, "invalid_request_error", "", "a live key was supplied to a test account")
		return
	}
	if got := r.Header.Get("Stripe-Version"); got != stripeAPIVersion {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "", "unpinned or wrong api version: "+got)
		return
	}

	op := r.Method + " " + routeOf(r.URL.Path)
	idemKey := r.Header.Get("Idempotency-Key")

	f.mu.Lock()
	f.calls[op]++
	if idemKey != "" {
		if prev, ok := f.idem[idemKey]; ok {
			if prev.op != op {
				// Stripe's own refusal: one key, one operation. Without it, a key reused across two
				// operations silently returns the first operation's object — a missing charge nobody sees.
				f.mu.Unlock()
				writeStripeError(w, http.StatusBadRequest, "idempotency_error", "idempotency_key_in_use",
					"the idempotency key was already used for a different operation")
				return
			}
			f.mu.Unlock()
			w.Header().Set("Idempotent-Replayed", "true")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(prev.status)
			_, _ = w.Write(prev.body)
			return
		}
	}
	f.mu.Unlock()

	status, body := f.route(r, form)

	// Store the response under the idempotency key BEFORE answering, so a replay is served even when the
	// original response never made it back to the caller — which is the whole point of the mechanism.
	if idemKey != "" && status >= 200 && status < 300 {
		f.mu.Lock()
		f.idem[idemKey] = idemEntry{op: op, status: status, body: body}
		f.mu.Unlock()
	}

	// The ambiguous failure: the effect IS recorded (above) and the response is lost.
	f.mu.Lock()
	fail := f.failAfterRecord && status >= 200 && status < 300 && r.Method == http.MethodPost
	if fail {
		f.failAfterRecord = false
	}
	f.mu.Unlock()
	if fail {
		writeStripeError(w, http.StatusInternalServerError, "api_error", "",
			"the response was lost after stripe recorded the object")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// routeOf collapses a path with an id in it to a stable operation name, so the idempotency store keys on
// the OPERATION rather than on the URL.
func routeOf(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if strings.Contains(p, "_") && i > 1 {
			parts[i] = "{id}"
		}
	}
	return "/" + strings.Join(parts, "/")
}

func (f *Server) route(r *http.Request, form url.Values) (int, []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/v1/customers":
		return f.createCustomer(form)
	case r.Method == http.MethodGet && path == "/v1/customers/search":
		return f.searchCustomers(form)
	case r.Method == http.MethodPost && path == "/v1/subscriptions":
		return f.createSubscription(form)
	case r.Method == http.MethodGet && path == "/v1/subscriptions":
		return f.listSubscriptions(form)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/subscriptions/"):
		return f.getSubscription(strings.TrimPrefix(path, "/v1/subscriptions/"))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/subscriptions/"):
		return f.updateSubscription(strings.TrimPrefix(path, "/v1/subscriptions/"), form)
	// 🔴 The legacy /v1/subscription_items/{id}/usage_records pair is GONE, not merely unused. Under
	// the pinned API version a metered price must be meter-backed, so the subscription item those
	// endpoints reported against cannot be created — and a fake that still answered them would let a
	// provider that never moved off the dead path stay green forever.
	case r.Method == http.MethodPost && path == "/v1/billing/meter_events":
		return f.createMeterEvent(form)
	case r.Method == http.MethodGet && path == "/v1/billing/meters":
		return f.listMeters()
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/event_summaries"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/billing/meters/"), "/event_summaries")
		return f.meterEventSummaries(id, form)
	case r.Method == http.MethodPost && path == "/v1/invoiceitems":
		return f.createInvoiceItem(form)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/invoiceitems/"):
		return f.getInvoiceItem(strings.TrimPrefix(path, "/v1/invoiceitems/"))
	case r.Method == http.MethodGet && path == "/v1/invoices":
		return f.listInvoices(form)
	case r.Method == http.MethodPost && path == "/v1/invoices":
		return f.issueInvoice(form)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/finalize"):
		return f.finalizeInvoice(strings.TrimSuffix(strings.TrimPrefix(path, "/v1/invoices/"), "/finalize"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/invoices/"):
		return f.getInvoice(strings.TrimPrefix(path, "/v1/invoices/"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/prices/"):
		return f.getPrice(strings.TrimPrefix(path, "/v1/prices/"))
	case r.Method == http.MethodPost && path == "/v1/checkout/sessions":
		return f.createCheckoutSession(form)
	case r.Method == http.MethodPost && path == "/v1/credit_notes":
		return f.createCreditNote(form)
	case r.Method == http.MethodPost && path == "/v1/refunds":
		return f.createRefund(form)
	}
	return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "unrecognized request url: "+path)
}

func (f *Server) createCustomer(form url.Values) (int, []byte) {
	platform := form.Get("metadata[" + metaCustomerID + "]")
	id := f.next("cus")
	obj := map[string]any{"id": id, "object": "customer",
		"metadata": map[string]string{metaCustomerID: platform}}
	f.customers[id] = obj
	if platform != "" {
		f.byPlatform[platform] = id
	}
	return okBody(obj)
}

func (f *Server) searchCustomers(form url.Values) (int, []byte) {
	q := form.Get("query")
	// The query shape stripe.go builds: metadata['platform_customer_id']:'cus_acme'
	platform := ""
	if i := strings.LastIndex(q, ":'"); i >= 0 {
		platform = strings.TrimSuffix(q[i+2:], "'")
	}
	data := []any{}
	if id, ok := f.byPlatform[platform]; ok {
		data = append(data, f.customers[id])
	}
	return okBody(map[string]any{"object": "search_result", "data": data})
}

func (f *Server) createSubscription(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	if _, ok := f.customers[cus]; !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such customer: "+cus)
	}
	price := form.Get("items[0][price]")
	if price == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "items[0][price] is required")
	}
	sub := &fakeSub{id: f.next("sub"), status: "active"}
	sub.items = append(sub.items, fakeSubItem{id: f.next("si"), priceRef: price, usageType: "licensed"})
	f.subs[sub.id] = sub
	f.owner[sub.id] = cus
	return okBody(f.subObject(sub))
}

func (f *Server) subObject(s *fakeSub) map[string]any {
	items := make([]any, 0, len(s.items))
	for _, it := range s.items {
		items = append(items, map[string]any{
			"id": it.id,
			"price": map[string]any{
				"id":        it.priceRef,
				"recurring": map[string]any{"usage_type": it.usageType},
			},
		})
	}
	return map[string]any{
		"id": s.id, "object": "subscription", "status": s.status,
		"items": map[string]any{"object": "list", "data": items},
	}
}

func (f *Server) getSubscription(id string) (int, []byte) {
	s, ok := f.subs[id]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such subscription: "+id)
	}
	return okBody(f.subObject(s))
}

// updateSubscription repoints an item at a different price — the plan change.
//
// It REPRICES the named item and leaves every other item alone, which is the behaviour the platform
// depends on: repricing a metered item would silently re-rate the period's usage, and a fake that
// quietly repriced everything would hide exactly that bug.
func (f *Server) updateSubscription(id string, form url.Values) (int, []byte) {
	sub, ok := f.subs[id]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such subscription: "+id)
	}
	itemID, price := form.Get("items[0][id]"), form.Get("items[0][price]")
	if itemID == "" || price == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "items[0][id] and items[0][price] are required")
	}
	found := false
	for i, it := range sub.items {
		if it.id == itemID {
			sub.items[i].priceRef = price
			found = true
		}
	}
	if !found {
		return errBody(http.StatusBadRequest, "invalid_request_error", "resource_missing", "no such subscription item: "+itemID)
	}
	return okBody(f.subObject(sub))
}

func (f *Server) listSubscriptions(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	data := []any{}
	for id, s := range f.subs {
		if f.owner[id] == cus || cus == "" {
			data = append(data, f.subObject(s))
		}
	}
	return okBody(map[string]any{"object": "list", "data": data})
}

// SeedMeter registers a billing meter the account knows, and returns its id. Test setup: a meter is
// created by the ACCOUNT OWNER in Stripe, never by this platform, so there is no code path that makes
// one and no test may pretend otherwise.
func (f *Server) SeedMeter(eventName string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.meters[eventName]; ok {
		return id
	}
	id := f.next("mtr")
	f.meters[eventName] = id
	return id
}

// MeterUsage is what the meter aggregated for one customer-period. The reconciler's right-hand side,
// exposed so a test can assert the number Stripe holds rather than the number the platform sent.
func (f *Server) MeterUsage(eventName, customerHandle, period string) (float64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.meterUsage[eventName][customerHandle][period]
	return v, ok
}

// createMeterEvent is POST /v1/billing/meter_events.
//
// 🔴 A repeated `identifier` is REFUSED, which is Stripe's real behaviour and the property the whole
// convergence argument rests on. Note what the refusal does NOT carry: Stripe sends no error `code`
// for it, only this sentence, so the provider has to match the message. That is stated here rather
// than smoothed over, because a fake that invented a tidy code would let the provider match on one
// that does not exist on the wire.
func (f *Server) createMeterEvent(form url.Values) (int, []byte) {
	name := form.Get("event_name")
	if _, ok := f.meters[name]; !ok {
		return errBody(http.StatusBadRequest, "invalid_request_error", "",
			"Invalid event_name: "+name+". No active meter found with this event name.")
	}
	id := form.Get("identifier")
	if id == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "identifier is required")
	}
	if f.meterEvents[id] {
		return errBody(http.StatusBadRequest, "invalid_request_error", "",
			"An event already exists with identifier "+id+".")
	}
	handle := form.Get("payload[stripe_customer_id]")
	if _, ok := f.customers[handle]; !ok {
		return errBody(http.StatusBadRequest, "invalid_request_error", "",
			"Invalid payload: no such customer "+handle)
	}
	val, err := strconv.ParseFloat(form.Get("payload[value]"), 64)
	if err != nil {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid_integer", "payload[value] must be a number")
	}
	ts, _ := strconv.ParseInt(form.Get("timestamp"), 10, 64)
	period := time.Unix(ts, 0).UTC().Format("2006-01")

	// 🔴 The 35-day backfill window Stripe enforces is deliberately NOT modelled. This fake has no
	// clock, and a time-dependent fake would make the suite pass in July and fail in September for
	// reasons no one could reproduce. The provider's own refusal is what is tested, against an
	// injected clock, in TestReportUsageRefusesAPeriodOutsideStripesBackfillWindow.
	f.meterEvents[id] = true
	if f.meterUsage[name] == nil {
		f.meterUsage[name] = map[string]map[string]float64{}
	}
	if f.meterUsage[name][handle] == nil {
		f.meterUsage[name][handle] = map[string]float64{}
	}
	// AGGREGATES, it does not set. That is the whole reason the identifier has to be deterministic.
	f.meterUsage[name][handle][period] += val
	return okBody(map[string]any{
		"object": "billing.meter_event", "event_name": name, "identifier": id,
		"timestamp": ts, "payload": map[string]any{"stripe_customer_id": handle, "value": form.Get("payload[value]")},
	})
}

func (f *Server) listMeters() (int, []byte) {
	data := []any{}
	names := make([]string, 0, len(f.meters))
	for n := range f.meters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data = append(data, map[string]any{
			"id": f.meters[n], "object": "billing.meter", "event_name": n, "status": "active",
		})
	}
	return okBody(map[string]any{"object": "list", "data": data})
}

func (f *Server) meterEventSummaries(meterID string, form url.Values) (int, []byte) {
	name := ""
	for n, id := range f.meters {
		if id == meterID {
			name = n
			break
		}
	}
	if name == "" {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such meter: "+meterID)
	}
	handle := form.Get("customer")
	start, _ := strconv.ParseInt(form.Get("start_time"), 10, 64)
	end, _ := strconv.ParseInt(form.Get("end_time"), 10, 64)

	data := []any{}
	periods := make([]string, 0, len(f.meterUsage[name][handle]))
	for period := range f.meterUsage[name][handle] {
		periods = append(periods, period)
	}
	sort.Strings(periods)
	for _, period := range periods {
		t, _ := time.ParseInLocation("2006-01", period, time.UTC)
		if t.Unix() < start || t.Unix() >= end {
			continue
		}
		data = append(data, map[string]any{
			"id": "mtrusg_" + meterID + "_" + period, "object": "billing.meter_event_summary",
			"aggregated_value": f.meterUsage[name][handle][period],
			"start_time":       t.Unix(), "end_time": t.AddDate(0, 1, 0).Unix(),
		})
	}
	return okBody(map[string]any{"object": "list", "data": data})
}

func (f *Server) createInvoiceItem(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	if _, ok := f.customers[cus]; !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such customer: "+cus)
	}
	// 🔴 The flat `price` parameter is GONE under the pinned API version. Stripe's own reply to it is
	// the one reproduced here, question mark and all — a fake that quietly accepted the old spelling
	// would let the provider keep sending a parameter the wire ignores, and the only symptom would be
	// an invoice item with no price on it.
	if form.Get("price") != "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_unknown",
			"Received unknown parameter: price. Did you mean pricing?")
	}
	ref := form.Get("pricing[price]")
	if ref == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "pricing[price] is required")
	}
	// An invoice item accepts a ONE-TIME price only. This is the refusal that makes the metered and
	// gainshare price references structurally different objects from the subscription one, and the
	// reason a single `price_refs` entry could never have served both.
	if pr, known := f.prices[ref]; known && pr.typ != "" && pr.typ != "one_time" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid",
			"The price specified is set to `type="+pr.typ+"` but this field only accepts prices with `type=one_time`.")
	}
	if _, err := strconv.ParseInt(form.Get("quantity"), 10, 64); err != nil {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid_integer", "quantity must be an integer")
	}
	qty, _ := strconv.ParseFloat(form.Get("quantity"), 64)
	inv := f.invoiceFor(cus)
	it := &fakeItem{
		id: f.next("ii"), lineID: f.next("il"), invoice: inv.id, customer: cus, price: ref,
		quantity: qty, desc: form.Get("description"), metadata: map[string]string{},
	}
	for _, k := range []string{metaKind, metaPeriod, metaBasis} {
		if v := form.Get("metadata[" + k + "]"); v != "" {
			it.metadata[k] = v
		}
	}
	f.items[it.id] = it
	return okBody(f.itemObject(it))
}

func (f *Server) itemObject(it *fakeItem) map[string]any {
	return map[string]any{
		"id": it.id, "object": "invoiceitem", "invoice": it.invoice, "customer": it.customer,
		"quantity": it.quantity, "description": it.desc, "metadata": it.metadata,
		"pricing": basilPricing(it.price),
	}
}

// basilPricing is the `pricing` sub-object the pinned API version nests a price reference inside.
func basilPricing(priceRef string) map[string]any {
	return map[string]any{
		"type":          "price_details",
		"price_details": map[string]any{"price": priceRef},
	}
}

func (f *Server) getInvoiceItem(id string) (int, []byte) {
	it, ok := f.items[id]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such invoiceitem: "+id)
	}
	return okBody(f.itemObject(it))
}

// issueInvoice is POST /v1/invoices.
//
// This fake attaches an invoice item to an invoice the moment it is created (invoiceFor), so there is
// never a pending item to sweep and this reduces to returning the invoice that already holds them. It
// is NOT a no-op stub: it exists so the provider's IssueInvoice is exercised against a wire on both
// sides, and so the demo takes the same branch here as it does against a real account.
func (f *Server) issueInvoice(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	if _, ok := f.customers[cus]; !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such customer: "+cus)
	}
	inv := f.invoiceFor(cus)
	return okBody(map[string]any{"id": inv.id, "object": "invoice", "status": inv.status, "customer": cus})
}

// getInvoice is GET /v1/invoices/{id}. It shares the object builder with the list route, so the two
// cannot drift into describing the same invoice differently.
// finalizeInvoice is POST /v1/invoices/{id}/finalize. A draft becomes `open`: real, creditable, and
// with no collection attempted.
func (f *Server) finalizeInvoice(id string) (int, []byte) {
	inv, ok := f.invoices[id]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such invoice: "+id)
	}
	inv.status = "open"
	return okBody(f.invoiceObject(inv))
}

func (f *Server) getInvoice(id string) (int, []byte) {
	inv, ok := f.invoices[id]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such invoice: "+id)
	}
	return okBody(f.invoiceObject(inv))
}

func (f *Server) listInvoices(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	data := []any{}
	for _, inv := range f.invoices {
		if inv.customer != cus {
			continue
		}
		data = append(data, f.invoiceObject(inv))
	}
	return okBody(map[string]any{"object": "list", "data": data})
}

// invoiceObject serializes one invoice with its lines. Shared by the list and retrieve routes so the
// two cannot drift into describing the same invoice differently — which would let a provider read a
// field on one path that does not exist on the other.
func (f *Server) invoiceObject(inv *fakeInvoice) map[string]any {
	lines := []any{}
	for _, it := range f.items {
		if it.invoice != inv.id {
			continue
		}
		lines = append(lines, map[string]any{
			"id": it.lineID, "object": "line_item", "description": it.desc,
			"quantity": it.quantity, "metadata": it.metadata,
			// The line points back at the item that produced it. Their ids differ, so this linkage is
			// the ONLY way a correction holding an item id can find the line to credit.
			"parent": map[string]any{
				"type":                 "invoice_item_details",
				"invoice_item_details": map[string]any{"invoice_item": it.id},
			},
			// 🔴 `pricing.price_details.price`, which is where the pinned API version puts it. The
			// pre-Basil `price.id` is not emitted at all: leaving it in "for safety" would let a
			// provider that still reads the old field keep passing here while losing the price on the
			// real wire — precisely the decay a version pin exists to make loud.
			"pricing": basilPricing(it.price),
		})
	}
	for _, own := range inv.own {
		lines = append(lines, map[string]any{
			"id": own.id, "object": "line_item", "description": own.description,
			"quantity": own.quantity, "metadata": map[string]string{},
			"pricing": basilPricing(own.priceRef),
			"period":  map[string]any{"start": own.periodStart, "end": own.periodStart},
		})
	}
	return map[string]any{
		"id": inv.id, "object": "invoice", "status": inv.status, "customer": inv.customer,
		"lines": map[string]any{"object": "list", "data": lines},
	}
}

// createCheckoutSession mints the hosted collection surface.
//
// It answers with a URL, exactly as Stripe does, and it deliberately has NO field that could carry a
// card: the whole point of the object is that the card is entered on the provider's page. A fake that
// accepted a card here would be modelling a Stripe that does not exist and letting a platform bug hide.
func (f *Server) createCheckoutSession(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	if _, ok := f.customers[cus]; !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such customer: "+cus)
	}
	if form.Get("line_items[0][price]") == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "line_items[0][price] is required")
	}
	if form.Get("success_url") == "" || form.Get("cancel_url") == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "success_url and cancel_url are required")
	}
	id := f.next("cs")
	return okBody(map[string]any{
		"id": id, "object": "checkout.session", "status": "open",
		"url":        f.srv.URL + "/hosted-checkout/" + id,
		"expires_at": 4102444800,
	})
}

func (f *Server) createCreditNote(form url.Values) (int, []byte) {
	inv := form.Get("invoice")
	invObj, ok := f.invoices[inv]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such invoice: "+inv)
	}
	// 🔴 Stripe's refusal, reproduced. A draft is not an issued invoice, and crediting one is
	// meaningless — there is nothing yet to take back.
	if invObj.status == "draft" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid",
			"You cannot create a credit note for a draft invoice.")
	}
	line := form.Get("lines[0][invoice_line_item]")
	var it *fakeItem
	for _, cand := range f.items {
		if cand.lineID == line {
			it = cand
			break
		}
		if cand.id == line {
			// 🔴 Stripe's own refusal, reproduced. The invoice ITEM id is a real id of the wrong kind
			// here, and accepting it would make this fake more forgiving than the wire.
			return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid",
				"Old `id` values cannot be used to specify an invoice line item in this API version. "+
					"Please use the current value of the `id` field of the invoice line item object.")
		}
	}
	if it == nil {
		return errBody(http.StatusBadRequest, "invalid_request_error", "resource_missing", "no such invoice line item: "+line)
	}
	// 🔴 Stripe's own requirement, reproduced verbatim. A fake that credited a line without one would
	// hide the single most important question a correction asks — HOW MUCH is being reversed — and the
	// provider would have shipped a credit note Stripe refuses.
	qty := form.Get("lines[0][quantity]")
	if qty == "" && form.Get("lines[0][amount]") == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing",
			"`quantity` or `amount` is required when crediting an invoice line item.")
	}
	if qty != "" {
		n, err := strconv.ParseFloat(qty, 64)
		if err != nil || n <= 0 {
			return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid_integer",
				"lines[0][quantity] must be a positive integer")
		}
		// A credit for MORE units than were charged is not a correction, it is a gift. Stripe refuses
		// it and so does this.
		if n > it.quantity {
			return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid",
				fmt.Sprintf("Cannot credit %v units against a line of %v.", n, it.quantity))
		}
	}
	id := f.next("cn")
	f.credits[id] = map[string]any{"id": id, "object": "credit_note", "invoice": inv,
		"memo": form.Get("memo"), "quantity": qty}
	return okBody(f.credits[id])
}

func (f *Server) createRefund(form url.Values) (int, []byte) {
	charge := form.Get("charge")
	if _, ok := f.items[charge]; !ok {
		return errBody(http.StatusBadRequest, "invalid_request_error", "resource_missing", "no such charge: "+charge)
	}
	id := f.next("re")
	f.refunds[id] = map[string]any{"id": id, "object": "refund", "charge": charge}
	return okBody(f.refunds[id])
}

func okBody(v any) (int, []byte) {
	b, _ := json.Marshal(v)
	return http.StatusOK, b
}

func errBody(status int, typ, code, msg string) (int, []byte) {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"type": typ, "code": code, "message": msg}})
	return status, b
}

func writeStripeError(w http.ResponseWriter, status int, typ, code, msg string) {
	s, b := errBody(status, typ, code, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_, _ = w.Write(b)
}
