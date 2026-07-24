package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
)

// p7_test.go covers the billing surface's HTTP boundary and — the part that is a REQUIREMENT rather
// than a nicety — the "no dollar figure is hardcoded in the client" fence (task 7.4 / 9.7).

// ── the no-hardcoded-money fence ─────────────────────────────────────────────

// currencyLiteral matches a currency symbol or code sitting next to a number, or a number formatted as
// money. These are the shapes a hardcoded price takes in a client.
var currencyLiteral = regexp.MustCompile(
	`\$\s?\d|\d\s?\$|` + // $49 / 49$
		`(?i)\b(usd|eur|gbp|jpy)\s?[\d]|[\d]\s?(usd|eur|gbp|jpy)\b|` + // USD 49 / 49 USD
		`[€£¥]\s?\d|` +
		`(?i)\bcurrency\s*[:=]\s*["'][a-z]{3}["']`) // currency: "USD"

// pricedAssignment matches a plan/price/limit value assigned in client code — the other way a price
// leaks into a client: not as a formatted string but as a constant it then renders.
var pricedAssignment = regexp.MustCompile(
	`(?i)\b(price|amount|rate|fee|monthly|annual|sum_band|seat_limit|gainshare_rate)[a-z_]*\s*[:=]\s*-?\d`)

// styleOrGeometry strips the things that legitimately contain numbers and are not money: CSS, SVG
// geometry, and the chart's own layout constants. Without this the fence would flag `width:120px` and
// be so noisy nobody would keep it.
var styleBlock = regexp.MustCompile(`(?s)<style>.*?</style>`)

// scrubNonMoney removes the constructs whose numbers are never money.
func scrubNonMoney(s string) string {
	s = styleBlock.ReplaceAllString(s, "")
	// SVG/layout constants and viewBox coordinates.
	s = regexp.MustCompile(`viewBox="[^"]*"`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?i)\b(W|H|padL|padR|padT|padB|plotW|plotH|groupW|barW|gap|max)\s*=\s*-?\d+`).ReplaceAllString(s, "")
	return s
}

// TestClientHardcodesNoMoney is task 7.4's load-bearing client rule: every amount, allowance and unit
// the page shows comes from the API. A currency literal or a priced constant in the client is a defect,
// because it is a number the server never agreed to and cannot correct by publishing config.
func TestClientHardcodesNoMoney(t *testing.T) {
	body := scrubNonMoney(string(p7HTML))
	if len(body) < 2000 {
		t.Fatalf("the embedded page is only %d bytes — the fence is not seeing it, so its assertions "+
			"would be vacuously true", len(body))
	}
	if m := currencyLiteral.FindString(body); m != "" {
		t.Errorf("the client contains a currency literal %q — every amount must come from the API", m)
	}
	if m := pricedAssignment.FindString(body); m != "" {
		t.Errorf("the client assigns a priced constant %q — limits and prices are configuration, not client code", m)
	}
	// And the positive half: the page must actually READ the server's unit rather than assuming one.
	if !strings.Contains(string(p7HTML), "sum_unit") {
		t.Error("the client never reads sum_unit — it is assuming a currency")
	}
}

// TestMoneyFenceGoesRed proves the fence can FAIL. A guard nobody has watched reject anything is
// decoration; these are the exact shapes a hardcoded price would take.
func TestMoneyFenceGoesRed(t *testing.T) {
	mustCatch := []string{
		`<div>$49 / month</div>`,
		`<span>49 USD</span>`,
		`const price = 49;`,
		`const monthlyUsd = 49.00;`,
		`sum_band: 1000`,
		`<div>€29</div>`,
		`const currency = "USD";`,
	}
	for _, s := range mustCatch {
		body := scrubNonMoney(s)
		if !currencyLiteral.MatchString(body) && !pricedAssignment.MatchString(body) {
			t.Errorf("the fence missed a hardcoded price: %q", s)
		}
	}
	mustIgnore := []string{
		`const W = 640, H = 190, padL = 46;`,
		`<rect x="12.5" y="40" width="34" height="90"/>`,
		`num(v.sum) + esc(v.sum_unit)`,
		`<td class="num">${num(l.quantity)}</td>`,
	}
	for _, s := range mustIgnore {
		body := scrubNonMoney(s)
		if currencyLiteral.MatchString(body) || pricedAssignment.MatchString(body) {
			t.Errorf("the fence false-positived on legitimate client code: %q", s)
		}
	}
}

// ── HTTP boundary ────────────────────────────────────────────────────────────

// fakeP7 is a canned P7Source.
type fakeP7 struct {
	view       BillingView
	found      bool
	consent    bool
	consentErr error
	asked      []string
}

func (f *fakeP7) Billing(customerID, period string) (BillingView, bool) {
	f.asked = append(f.asked, customerID+"/"+period)
	v := f.view
	v.CustomerID = customerID
	if period != "" {
		v.Period = period
	}
	return v, f.found
}

func (f *fakeP7) SetGainshareConsent(customerID string, consented bool) (BillingView, error) {
	if f.consentErr != nil {
		return BillingView{}, f.consentErr
	}
	f.consent = consented
	v := f.view
	v.CustomerID = customerID
	v.Savings.Consented = consented
	return v, nil
}

func (f *fakeP7) Periods(string) []string { return []string{"2026-07"} }

func newP7Server(src P7Source) *Server {
	s := New(nil, config.Config{})
	if src != nil {
		s.MountP7(src)
	}
	return s
}

func TestP7UnmountedReturns503(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountP7(nil) // routes registered, source absent — the deployment-without-billing shape
	for _, req := range []*http.Request{
		httptest.NewRequest("GET", "/api/p7/customers/cus_a/billing", nil),
		httptest.NewRequest("POST", "/api/p7/customers/cus_a/gainshare-consent", strings.NewReader(`{"consented":true}`)),
	} {
		w := httptest.NewRecorder()
		s.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", req.Method, req.URL.Path, w.Code)
		}
	}
}

func TestP7BillingRoundTrip(t *testing.T) {
	src := &fakeP7{found: true, view: BillingView{
		Period: "2026-07", SUM: 60, SUMUnit: "usd", PlanID: "team", PlanName: "Team",
		Invoice: InvoiceView{Lines: []LineView{{Kind: "metered", Basis: "usage_record:cus_a/2026-07/sum", Quantity: 60}}},
	}}
	s := newP7Server(src)

	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/p7/customers/cus_a/billing?period=2026-06", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got BillingView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CustomerID != "cus_a" || got.Period != "2026-06" {
		t.Errorf("the path/query were not passed through: %+v", got)
	}
	if len(src.asked) != 1 || src.asked[0] != "cus_a/2026-06" {
		t.Errorf("source asked %v", src.asked)
	}
	if got.SUMUnit != "usd" {
		t.Errorf("the unit did not survive the round trip: %q", got.SUMUnit)
	}

	// An unknown customer is a 404 with a reason, never an empty 200 that renders as "no usage".
	src.found = false
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/p7/customers/nobody/billing", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown customer = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "nobody") {
		t.Errorf("the 404 does not name what was missing: %s", w.Body.String())
	}
}

func TestP7ConsentRoundTrip(t *testing.T) {
	src := &fakeP7{found: true}
	s := newP7Server(src)

	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/p7/customers/cus_a/gainshare-consent",
		strings.NewReader(`{"consented":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !src.consent {
		t.Error("consent was not recorded")
	}
	var v BillingView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !v.Savings.Consented {
		t.Error("the refreshed view does not reflect the consent that was just recorded")
	}

	// Revocation is the same route with the opposite value — a revocable contract, not a one-way door.
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/p7/customers/cus_a/gainshare-consent",
		strings.NewReader(`{"consented":false}`)))
	if w.Code != http.StatusOK || src.consent {
		t.Errorf("revocation: status %d, consent %v", w.Code, src.consent)
	}

	// A malformed payload is a 400, and a rejected consent change is a 409 that names why.
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/p7/customers/cus_a/gainshare-consent",
		strings.NewReader(`not json`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed payload = %d, want 400", w.Code)
	}
	src.consentErr = errors.New("account is closed")
	w = httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/p7/customers/cus_a/gainshare-consent",
		strings.NewReader(`{"consented":true}`)))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "account is closed") {
		t.Errorf("rejected consent = %d %s", w.Code, w.Body.String())
	}
}

func TestP7PageIsServed(t *testing.T) {
	s := newP7Server(&fakeP7{found: true})
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/p7/billing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type = %q", ct)
	}
	body := w.Body.String()
	// The four questions the page exists to answer must all have a section.
	for _, want := range []string{"Spend under management", "Plan &amp; entitlements", "Invoice breakdown",
		"Verified savings"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page has no %q section", want)
		}
	}
	// And the states that must be first-class.
	for _, want := range []string{"aria-live", "No usage recorded", "Nothing verified and merged",
		"never billed", "Revoke consent"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing the %q state/copy", want)
		}
	}
}

// TestGainshareLineCarriesEvidence: the view model has nowhere to put a gainshare line WITHOUT its
// evidence, and this asserts the shape a renderer can rely on.
func TestGainshareLineCarriesEvidence(t *testing.T) {
	line := LineView{Kind: "gainshare", Basis: "billable_savings:cus_a/2026-07", Quantity: 30,
		Evidence: []EvidenceView{
			{Kind: "verified_delta", Ref: "vd1", Label: "verified delta vd1",
				Method: &MethodView{ID: "holdout-v1", HoldoutCases: 4, GeneratingCases: 3, Seeds: 5}},
			{Kind: "merge", Ref: "abc123", Label: "merged as abc123"},
		}}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"kind":"gainshare"`, `"evidence"`, `"verified_delta"`, `"merge"`, `"holdout-v1"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the gainshare line loses %s in transit: %s", want, b)
		}
	}
	// A line with no amount does NOT emit a zero amount field — the platform holds no amount, and a
	// zero would render as "this cost nothing".
	if strings.Contains(string(b), `"amount_ref"`) {
		t.Error("an absent amount ref was serialized")
	}
}
