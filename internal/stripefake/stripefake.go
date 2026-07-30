package stripefake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	stripeAPIVersion = "2024-06-20"
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
	usage      map[string]map[string]float64 // subscription item id -> period key -> total
	credits    map[string]map[string]any
	refunds    map[string]map[string]any
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
	id       string
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
		usage: map[string]map[string]float64{}, credits: map[string]map[string]any{},
		refunds: map[string]map[string]any{}, idem: map[string]idemEntry{}, calls: map[string]int{},
		owner: map[string]string{},
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
func (f *Server) Usage(itemID, period string) (float64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.usage[itemID][period]
	return v, ok
}

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
		id: f.next("ii"), invoice: inv.id, customer: customerHandle, price: "price_ref_tokens",
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
	inv := &fakeInvoice{id: f.next("in"), customer: customer, status: "open"}
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
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/usage_records"):
		item := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/subscription_items/"), "/usage_records")
		return f.recordUsage(item, form)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/usage_record_summaries"):
		item := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/subscription_items/"), "/usage_record_summaries")
		return f.usageSummaries(item)
	case r.Method == http.MethodPost && path == "/v1/invoiceitems":
		return f.createInvoiceItem(form)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/invoiceitems/"):
		return f.getInvoiceItem(strings.TrimPrefix(path, "/v1/invoiceitems/"))
	case r.Method == http.MethodGet && path == "/v1/invoices":
		return f.listInvoices(form)
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

func (f *Server) recordUsage(item string, form url.Values) (int, []byte) {
	if !f.hasItem(item) {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such subscription item: "+item)
	}
	qty, err := strconv.ParseFloat(form.Get("quantity"), 64)
	if err != nil {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid_integer", "quantity must be an integer")
	}
	ts, _ := strconv.ParseInt(form.Get("timestamp"), 10, 64)
	period := time.Unix(ts, 0).UTC().Format("2006-01")
	if f.usage[item] == nil {
		f.usage[item] = map[string]float64{}
	}
	// `set` semantics: the platform's usage record is an upsert of "what was used", so a re-report
	// converges rather than accumulating.
	if form.Get("action") == "increment" {
		f.usage[item][period] += qty
	} else {
		f.usage[item][period] = qty
	}
	return okBody(map[string]any{"id": f.next("mbur"), "object": "usage_record", "quantity": qty})
}

func (f *Server) hasItem(item string) bool {
	for _, s := range f.subs {
		for _, it := range s.items {
			if it.id == item {
				return true
			}
		}
	}
	return false
}

func (f *Server) usageSummaries(item string) (int, []byte) {
	data := []any{}
	for period, total := range f.usage[item] {
		start, _ := time.ParseInLocation("2006-01", period, time.UTC)
		data = append(data, map[string]any{
			"id": "urs_" + item + "_" + period, "object": "usage_record_summary",
			"period":      map[string]any{"start": start.Unix(), "end": start.AddDate(0, 1, 0).Unix()},
			"total_usage": total, "subscription_item": item,
		})
	}
	return okBody(map[string]any{"object": "list", "data": data})
}

func (f *Server) createInvoiceItem(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	if _, ok := f.customers[cus]; !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such customer: "+cus)
	}
	if form.Get("price") == "" {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_missing", "price is required")
	}
	if _, err := strconv.ParseInt(form.Get("quantity"), 10, 64); err != nil {
		return errBody(http.StatusBadRequest, "invalid_request_error", "parameter_invalid_integer", "quantity must be an integer")
	}
	qty, _ := strconv.ParseFloat(form.Get("quantity"), 64)
	inv := f.invoiceFor(cus)
	it := &fakeItem{
		id: f.next("ii"), invoice: inv.id, customer: cus, price: form.Get("price"),
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
		"price": map[string]any{"id": it.price},
	}
}

func (f *Server) getInvoiceItem(id string) (int, []byte) {
	it, ok := f.items[id]
	if !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such invoiceitem: "+id)
	}
	return okBody(f.itemObject(it))
}

func (f *Server) listInvoices(form url.Values) (int, []byte) {
	cus := form.Get("customer")
	data := []any{}
	for _, inv := range f.invoices {
		if inv.customer != cus {
			continue
		}
		lines := []any{}
		for _, it := range f.items {
			if it.invoice != inv.id {
				continue
			}
			lines = append(lines, map[string]any{
				"id": it.id, "object": "line_item", "description": it.desc,
				"quantity": it.quantity, "metadata": it.metadata,
				"price": map[string]any{"id": it.price},
			})
		}
		for _, own := range inv.own {
			lines = append(lines, map[string]any{
				"id": own.id, "object": "line_item", "description": own.description,
				"quantity": own.quantity, "metadata": map[string]string{},
				"price":  map[string]any{"id": own.priceRef},
				"period": map[string]any{"start": own.periodStart, "end": own.periodStart},
			})
		}
		data = append(data, map[string]any{
			"id": inv.id, "object": "invoice", "status": inv.status, "customer": inv.customer,
			"lines": map[string]any{"object": "list", "data": lines},
		})
	}
	return okBody(map[string]any{"object": "list", "data": data})
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

// SeedMeteredItem attaches a metered item to an existing subscription and returns its id.
//
// This models STRIPE-ACCOUNT CONFIGURATION, not platform code: a metered subscription item exists
// because Finance configured a metered price and attached it, and the platform's job is only to report
// a quantity against it. Seeding it here rather than having the platform create it keeps that division
// honest — the `Provider` interface has one price reference per subscription and P21 does not widen it.
func (f *Server) SeedMeteredItem(subID, priceRef string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sub, ok := f.subs[subID]
	if !ok {
		return ""
	}
	item := fakeSubItem{id: f.next("si"), priceRef: priceRef, usageType: "metered"}
	sub.items = append(sub.items, item)
	return item.id
}

func (f *Server) createCreditNote(form url.Values) (int, []byte) {
	inv := form.Get("invoice")
	if _, ok := f.invoices[inv]; !ok {
		return errBody(http.StatusNotFound, "invalid_request_error", "resource_missing", "no such invoice: "+inv)
	}
	line := form.Get("lines[0][invoice_line_item]")
	if _, ok := f.items[line]; !ok {
		return errBody(http.StatusBadRequest, "invalid_request_error", "resource_missing", "no such invoice line item: "+line)
	}
	id := f.next("cn")
	f.credits[id] = map[string]any{"id": id, "object": "credit_note", "invoice": inv, "memo": form.Get("memo")}
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
