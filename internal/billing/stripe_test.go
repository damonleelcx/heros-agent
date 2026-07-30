package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// stripe_test.go is P21 section 2: the Stripe provider, exercised over the wire against the fake Stripe.
//
// Every assertion here is about the HTTP conversation rather than about Go call shapes, because the
// conversation is what can be wrong: a header that never reached the wire, a form key Stripe would have
// ignored, a status class that turns a deliberate refusal into an infinite retry.

const (
	stripeSubPrice     = "price_ref_team_sub"
	stripeMeteredPrice = "price_ref_team_metered"
	stripeJuly         = "2026-07"
)

var stripeClock = func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }

// stripeSecrets builds a Secrets seam holding a TEST key.
func stripeSecrets(t *testing.T, key string) Secrets {
	t.Helper()
	s, err := NewManagedSecrets(providergateway.StaticSecrets{
		SecretBillingAPIKey:         {APIKey: key},
		SecretBillingWebhookSigning: {APIKey: "webhook-signing-secret-DO-NOT-LEAK-test"},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	return s
}

// newStripe wires the provider at a fake Stripe in test mode.
func newStripe(t *testing.T, f *fakeStripe) *StripeProvider {
	t.Helper()
	p, err := NewStripeProvider(stripeSecrets(t, testStripeKey), ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("new stripe provider: %v", err)
	}
	return p
}

// TestStripeProviderImplementsTheP7InterfaceUnchanged is task 2.1 / FR1.
//
// The compile-time `var _ Provider` in stripe.go proves the method set. What this adds is the RUNTIME
// half of the same claim: every method answers with the P7 result structs, so a caller that holds a
// `Provider` cannot tell which implementation it has except by asking Describe.
func TestStripeProviderImplementsTheP7InterfaceUnchanged(t *testing.T) {
	f := newFakeStripe(t)
	ctx := context.Background()

	// Two implementations, one interface variable. If P21 had widened the interface, this would not
	// compile — which is the conversation design Decision 1 wants forced rather than avoided.
	impls := map[string]Provider{
		"stub":   NewStubProvider(),
		"stripe": newStripe(t, f),
	}
	for name, p := range impls {
		if p.Describe() == "" {
			t.Errorf("%s: Describe returned nothing — the readiness surface cannot name the live provider", name)
		}
		handle, err := p.EnsureCustomer(ctx, "cus_acme")
		if err != nil {
			t.Fatalf("%s: EnsureCustomer: %v", name, err)
		}
		if handle == "" {
			t.Errorf("%s: EnsureCustomer returned an empty handle", name)
		}
		// The SAME call twice returns the SAME handle: a second customer would be a split billing history.
		again, err := p.EnsureCustomer(ctx, "cus_acme")
		if err != nil || again != handle {
			t.Errorf("%s: EnsureCustomer is not stable: %q then %q (%v)", name, handle, again, err)
		}
	}

	if got := impls["stripe"].Describe(); got != "stripe(test)" {
		t.Errorf("Describe = %q, want stripe(test) — the mode is part of the identity", got)
	}
}

// TestStripeCarriesTheP7IdempotencyKeyOnEveryChargeBearingCall is task 2.2 / FR2.
//
// It asserts the key reached the WIRE, on Stripe's own header, for each of the four charge-bearing
// operations — not that a struct field was populated. The header is where the guarantee lives.
func TestStripeCarriesTheP7IdempotencyKeyOnEveryChargeBearingCall(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, subRef, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	usageKey := UsageReportIdempotencyKey("cus_acme", stripeJuly, "sum")
	chargeKey := MeteredChargeIdempotencyKey("cus_acme", stripeJuly, "sum")

	if _, err := p.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: handle, SubscriptionRef: subRef, Metric: "sum", Period: stripeJuly,
		Quantity: 2, PriceRef: stripeMeteredPrice, IdempotencyKey: usageKey,
	}); err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}
	charge, err := p.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 2, IdempotencyKey: chargeKey,
		Description: "usage_record:cus_acme/2026-07/sum",
	})
	if err != nil {
		t.Fatalf("RaiseCharge: %v", err)
	}
	creditKey := CorrectionIdempotencyKey(TypeCredit, "be_000001", "over-charged")
	if _, err := p.IssueCredit(ctx, CreditRequest{
		ProviderCustomerHandle: handle, AgainstRef: charge.ChargeRef, Reason: "over-charged",
		IdempotencyKey: creditKey,
	}); err != nil {
		t.Fatalf("IssueCredit: %v", err)
	}
	subKey := "subscribe:cus_acme:team:v1"
	if _, err := p.CreateSubscription(ctx, SubscriptionRequest{
		ProviderCustomerHandle: handle, PriceRef: stripeSubPrice, PlanID: "team", PlanName: "Team",
		IdempotencyKey: subKey,
	}); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	seen := strings.Join(f.IdempotencyKeys(), "\n")
	for _, want := range []string{usageKey, chargeKey, creditKey, subKey} {
		if !strings.Contains(seen, want) {
			t.Errorf("the P7 idempotency key %q never reached Stripe's Idempotency-Key header", want)
		}
	}
	// The keys are DERIVED, and each carries its operation prefix — the property that stops one key
	// from being reused across two operations and silently returning the first operation's object.
	for _, k := range []string{usageKey, chargeKey} {
		if !strings.Contains(k, ":") {
			t.Errorf("key %q carries no operation prefix", k)
		}
	}
}

// TestStripeRetriedChargeCreatesOneStripeObject is task 2.2 / FR2 — never double-charge, at the
// provider layer.
func TestStripeRetriedChargeCreatesOneStripeObject(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, _, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	key := MeteredChargeIdempotencyKey("cus_acme", stripeJuly, "sum")
	req := ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 2, IdempotencyKey: key,
		Description: "usage_record:cus_acme/2026-07/sum",
	}

	first, err := p.RaiseCharge(ctx, req)
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if first.Duplicate {
		t.Error("the FIRST charge reported itself as a duplicate")
	}
	for i := 0; i < 4; i++ {
		again, err := p.RaiseCharge(ctx, req)
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if again.ChargeRef != first.ChargeRef {
			t.Fatalf("retry %d produced a DIFFERENT charge %s (first was %s) — that is a double charge",
				i, again.ChargeRef, first.ChargeRef)
		}
		if !again.Duplicate {
			t.Errorf("retry %d did not surface Duplicate=true — the evidence that idempotency worked is hidden", i)
		}
	}
	if n := f.ItemCount(); n != 1 {
		t.Errorf("stripe recorded %d charge objects, want exactly 1", n)
	}
	// The provider was genuinely asked more than once: the guarantee is Stripe's refusal, not a local
	// short-circuit that never made the call.
	if n := f.Calls("POST /v1/invoiceitems"); n != 5 {
		t.Errorf("stripe was called %d times, want 5 — a local cache that skipped the call would not "+
			"exercise the provider's own idempotency", n)
	}
}

// TestStripeAmbiguousRecordedThenLostFailureDoesNotDoubleCharge is task 2.2's second scenario — the
// nastiest real case: Stripe records the charge and the response never comes back.
func TestStripeAmbiguousRecordedThenLostFailureDoesNotDoubleCharge(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, _, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	req := ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 2,
		IdempotencyKey: MeteredChargeIdempotencyKey("cus_acme", stripeJuly, "sum"),
		Description:    "usage_record:cus_acme/2026-07/sum",
	}

	f.SetFailAfterRecord(true)
	if _, err := p.RaiseCharge(ctx, req); err == nil {
		t.Fatal("the ambiguous failure did not surface as an error — an invisible maybe-charge is the worst outcome")
	} else if !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("a lost response must classify as an OUTAGE so the row stays pending and is retried; got %v", err)
	}

	// The retry resolves against the ORIGINAL object rather than creating a second one.
	res, err := p.RaiseCharge(ctx, req)
	if err != nil {
		t.Fatalf("retry after the ambiguous failure: %v", err)
	}
	if !res.Duplicate {
		t.Error("the retry did not recognize Stripe's replay — it must, or the ledger records a second charge")
	}
	if n := f.ItemCount(); n != 1 {
		t.Errorf("stripe holds %d charge objects after a recorded-then-lost failure and a retry, want 1", n)
	}
}

// TestStripeSubscriptionUsesTheOpaquePriceRefAndComputesNoAmount is task 2.3 / FR3.
func TestStripeSubscriptionUsesTheOpaquePriceRefAndComputesNoAmount(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()

	handle, err := p.EnsureCustomer(ctx, "cus_acme")
	if err != nil {
		t.Fatalf("EnsureCustomer: %v", err)
	}
	res, err := p.CreateSubscription(ctx, SubscriptionRequest{
		ProviderCustomerHandle: handle, PriceRef: stripeSubPrice, PlanID: "team", PlanName: "Team",
		IdempotencyKey: "subscribe:cus_acme:team:v1",
	})
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if res.SubscriptionRef == "" {
		t.Fatal("no subscription handle")
	}
	// The status is Stripe's word, carried verbatim — the platform reflects dunning, never computes it.
	if res.Status != "active" {
		t.Errorf("status = %q, want stripe's own %q", res.Status, "active")
	}

	// A subscription with no price reference is REFUSED rather than defaulted: a default price is a
	// charge nobody configured.
	if _, err := p.CreateSubscription(ctx, SubscriptionRequest{
		ProviderCustomerHandle: handle, IdempotencyKey: "subscribe:cus_acme:noprice",
	}); !errors.Is(err, ErrProviderRejected) {
		t.Errorf("a subscription with no price_ref must be rejected, got %v", err)
	}

	// Read-back is Stripe's state, unchanged.
	back, err := p.Subscription(ctx, res.SubscriptionRef)
	if err != nil || back.Status != "active" {
		t.Errorf("Subscription read-back = %+v, %v", back, err)
	}
}

// TestStripeReportUsageSendsAQuantityAndMultipliesNothing is task 2.3 / FR4.
func TestStripeReportUsageSendsAQuantityAndMultipliesNothing(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, subRef, meteredItem := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	res, err := p.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: handle, SubscriptionRef: subRef, Metric: "sum", Period: stripeJuly,
		Quantity: 7, PriceRef: stripeMeteredPrice,
		IdempotencyKey: UsageReportIdempotencyKey("cus_acme", stripeJuly, "sum"),
	})
	if err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}
	if res.UsageRef == "" {
		t.Fatal("no usage handle")
	}

	// Stripe holds the QUANTITY the platform reported — unscaled, unmultiplied, attributed to the period
	// the platform named rather than to the wall clock at report time.
	got, ok := f.Usage(meteredItem, stripeJuly)
	if !ok {
		t.Fatalf("stripe recorded no usage for %s in %s — the report landed in the wrong period", meteredItem, stripeJuly)
	}
	if got != 7 {
		t.Errorf("stripe recorded quantity %v, want the reported 7 — the platform multiplies nothing", got)
	}

	// A re-report of the same period CONVERGES rather than accumulating (`action=set`).
	if _, err := p.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: handle, SubscriptionRef: subRef, Metric: "sum", Period: stripeJuly,
		Quantity: 9, PriceRef: stripeMeteredPrice,
		IdempotencyKey: UsageReportIdempotencyKey("cus_acme", stripeJuly, "sum") + ":corrected",
	}); err != nil {
		t.Fatalf("re-report: %v", err)
	}
	if got, _ := f.Usage(meteredItem, stripeJuly); got != 9 {
		t.Errorf("a re-report accumulated to %v instead of converging on 9 — that is a double count", got)
	}
}

// TestStripeRefusesAQuantityItCannotRecordExactly is the quantity contract, stated as a red test.
//
// It is here rather than in a comment because the alternative — rounding — is the failure mode that
// looks fine in every individual number and is wrong in the total.
func TestStripeRefusesAQuantityItCannotRecordExactly(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, subRef, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	_, err := p.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: handle, SubscriptionRef: subRef, Metric: "sum", Period: stripeJuly,
		Quantity: 2.5, PriceRef: stripeMeteredPrice, IdempotencyKey: "usage_report:cus_acme:2026-07:sum",
	})
	if !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("a fractional quantity must be REJECTED, not rounded; got %v", err)
	}
	if !strings.Contains(err.Error(), "integral unit") {
		t.Errorf("the refusal must name the remedy (denominate the price in the meter's integral unit); got %q", err)
	}
	// A rejection, not an outage: retrying it forever would not make it representable.
	if errors.Is(err, ErrProviderUnavailable) {
		t.Error("a quantity Stripe cannot represent is a REJECTION — classifying it as an outage would retry it forever")
	}

	if _, err := p.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: -1, IdempotencyKey: "charge_metered:neg",
	}); !errors.Is(err, ErrProviderRejected) {
		t.Errorf("a negative charge quantity must be rejected — a reduction is an additive credit; got %v", err)
	}
}

// TestStripeCreditIsAdditiveAndLeavesTheOriginalIntact is task 2.4 / FR5.
func TestStripeCreditIsAdditiveAndLeavesTheOriginalIntact(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, _, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	charge, err := p.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 4, IdempotencyKey: "charge_metered:cus_acme:2026-07:sum",
		Description: "usage_record:cus_acme/2026-07/sum",
	})
	if err != nil {
		t.Fatalf("RaiseCharge: %v", err)
	}
	before, _ := f.Item(charge.ChargeRef)

	credit, err := p.IssueCredit(ctx, CreditRequest{
		ProviderCustomerHandle: handle, AgainstRef: charge.ChargeRef,
		Reason: "billed against the wrong period", IdempotencyKey: "credit:be_000001:wrong-period",
	})
	if err != nil {
		t.Fatalf("IssueCredit: %v", err)
	}
	if credit.CreditRef == "" {
		t.Fatal("no credit handle")
	}

	// 🔴 The ORIGINAL is untouched. An auditor must be able to see both the mistake and its correction.
	after, ok := f.Item(charge.ChargeRef)
	if !ok {
		t.Fatal("the original charge object was REMOVED by the correction — corrections are additive")
	}
	if after.quantity != before.quantity || after.price != before.price {
		t.Errorf("the original charge was MODIFIED by the correction: %+v -> %+v", before, after)
	}
	if n := f.CreditCount(); n != 1 {
		t.Errorf("stripe holds %d correction objects, want 1", n)
	}

	// A refund is the same shape against the payment instrument.
	if _, err := p.IssueCredit(ctx, CreditRequest{
		ProviderCustomerHandle: handle, AgainstRef: charge.ChargeRef, Refund: true,
		Reason: "refunded to card", IdempotencyKey: "refund:be_000001:refunded-to-card",
	}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if _, ok := f.Item(charge.ChargeRef); !ok {
		t.Error("the refund removed the original charge")
	}

	// A correction with no reason, or naming nothing, is refused: an unexplained credit is
	// indistinguishable from a mistake six months later.
	for name, req := range map[string]CreditRequest{
		"no reason": {ProviderCustomerHandle: handle, AgainstRef: charge.ChargeRef, IdempotencyKey: "k1"},
		"no target": {ProviderCustomerHandle: handle, Reason: "why", IdempotencyKey: "k2"},
	} {
		if _, err := p.IssueCredit(ctx, req); !errors.Is(err, ErrProviderRejected) {
			t.Errorf("%s: expected a rejection, got %v", name, err)
		}
	}
}

// TestStripeInvoiceReadBackPassesValidateAndNamesEveryBasis is task 2.5 / FR6.
func TestStripeInvoiceReadBackPassesValidateAndNamesEveryBasis(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, _, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	if _, err := p.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 4, IdempotencyKey: "charge_metered:cus_acme:2026-07:sum",
		Description: "usage_record:cus_acme/2026-07/sum",
	}); err != nil {
		t.Fatalf("RaiseCharge: %v", err)
	}
	// A line STRIPE authored — the recurring subscription line — carries no platform metadata and is the
	// only kind that can reach the basis fallback.
	f.SeedOwnInvoiceLine(handle, stripeJuly, stripeSubPrice, "Team plan")

	inv, err := p.Invoice(ctx, "cus_acme", stripeJuly)
	if err != nil {
		t.Fatalf("Invoice: %v", err)
	}
	if inv.InvoiceRef == "" || inv.Status == "" {
		t.Errorf("invoice %+v has no reference or status", inv)
	}
	if len(inv.Lines) != 2 {
		t.Fatalf("read back %d lines, want 2 (the platform's charge and Stripe's own subscription line)", len(inv.Lines))
	}
	if err := inv.Validate(); err != nil {
		t.Errorf("the read-back invoice does not pass Validate: %v", err)
	}
	kinds := map[LineKind]bool{}
	for _, l := range inv.Lines {
		kinds[l.Kind] = true
		if l.Basis == "" {
			t.Errorf("line %+v names no basis — a figure a customer cannot trace is what a dispute is made of", l)
		}
		// 🔴 No amount, anywhere. AmountRef POINTS at the amount; it never is one.
		if l.AmountRef == "" || !strings.HasPrefix(l.AmountRef, "stripe:amount:") {
			t.Errorf("line %+v has no opaque amount handle", l)
		}
	}
	if !kinds[LineMetered] || !kinds[LineSubscription] {
		t.Errorf("read back kinds %v, want both metered and subscription", kinds)
	}

	// A different period reads back empty rather than borrowing another period's lines.
	other, err := p.Invoice(ctx, "cus_acme", "2026-06")
	if err != nil {
		t.Fatalf("Invoice(2026-06): %v", err)
	}
	if len(other.Lines) != 0 {
		t.Errorf("a period with no charges read back %d lines", len(other.Lines))
	}
}

// TestStripeInvoiceRejectsAResoldTokenLine is task 2.5's no-resale scenario.
//
// It seeds the misconfiguration on STRIPE's side — a line kind the platform's closed enum cannot
// produce — and asserts the read path refuses it instead of rendering it as if it were understood.
func TestStripeInvoiceRejectsAResoldTokenLine(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, _, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	f.SeedTokenLine(handle, stripeJuly, "provider_tokens", "LLM tokens resold")

	_, err := p.Invoice(ctx, "cus_acme", stripeJuly)
	if !errors.Is(err, ErrResoldTokens) {
		t.Fatalf("a resold-token line must be REJECTED on read-back, got %v", err)
	}
}

// TestStripeRecordedUsageIsAvailableForReconciliation is task 2.5's reconciliation scenario.
func TestStripeRecordedUsageIsAvailableForReconciliation(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, subRef, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	if _, err := p.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: handle, SubscriptionRef: subRef, Metric: "sum", Period: stripeJuly,
		Quantity: 6, PriceRef: stripeMeteredPrice, IdempotencyKey: "usage_report:cus_acme:2026-07:sum",
	}); err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}

	recs, err := p.RecordedUsage(ctx, "cus_acme", stripeJuly)
	if err != nil {
		t.Fatalf("RecordedUsage: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("read back %d usage records, want 1: %+v", len(recs), recs)
	}
	if recs[0].Quantity != 6 || recs[0].Period != stripeJuly || recs[0].Metric != "sum" {
		t.Errorf("recorded usage = %+v, want the reported 6 for sum/%s", recs[0], stripeJuly)
	}
	if recs[0].UsageRef == "" {
		t.Error("the recorded usage carries no handle — reconciliation cannot point at what it compared")
	}
}

// TestStripeOutageAndRejectionAreDifferentErrors is task 2.6 / the outage-vs-rejection split.
//
// This is the split the P7 outage buffer depends on: an outage means the row stays pending and
// FlushPending drains it; a rejection means stop. Collapsing them either drops revenue or hammers a
// processor that is deliberately refusing.
func TestStripeOutageAndRejectionAreDifferentErrors(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, _, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	req := ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 3, IdempotencyKey: "charge_metered:cus_acme:2026-07:sum",
		Description: "usage_record:cus_acme/2026-07/sum",
	}

	// OUTAGE — Stripe unreachable / 5xx: buffer and retry.
	f.SetDown(true)
	_, err := p.RaiseCharge(ctx, req)
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("a 5xx from Stripe must be an OUTAGE so the charge buffers; got %v", err)
	}
	if errors.Is(err, ErrProviderRejected) {
		t.Error("an outage must not also read as a rejection — the caller would stop instead of retrying")
	}
	f.SetDown(false)

	// REJECTION — a deliberate 4xx: stop.
	bad := req
	bad.PriceRef = ""
	if _, err := p.RaiseCharge(ctx, bad); !errors.Is(err, ErrProviderRejected) {
		t.Errorf("a request Stripe would refuse must be a REJECTION; got %v", err)
	}
	unknownCustomer := req
	unknownCustomer.ProviderCustomerHandle = "cus_does_not_exist"
	unknownCustomer.IdempotencyKey = "charge_metered:unknown"
	_, err = p.RaiseCharge(ctx, unknownCustomer)
	if !errors.Is(err, ErrProviderRejected) {
		t.Errorf("stripe's 404 for an unknown customer must be a REJECTION, not an outage; got %v", err)
	}
	if errors.Is(err, ErrProviderUnavailable) {
		t.Error("a rejection must not read as an outage — the caller would retry a call Stripe is refusing")
	}

	// After the outage cleared, the ORIGINAL key still bills exactly once.
	if _, err := p.RaiseCharge(ctx, req); err != nil {
		t.Fatalf("recovery charge: %v", err)
	}
	if n := f.ItemCount(); n != 1 {
		t.Errorf("the outage window produced %d charge objects, want exactly 1", n)
	}
}

// TestStripeRefusesAnIdempotencyKeyReusedAcrossOperations proves the provider surfaces Stripe's own
// refusal rather than silently returning the first operation's object.
//
// A billing provider's key namespace is global per account: reusing one key across two operations makes
// the provider return the FIRST operation's record for the second call — silently yielding, say, a usage
// ref where a charge ref was expected, and no charge at all.
func TestStripeRefusesAnIdempotencyKeyReusedAcrossOperations(t *testing.T) {
	f := newFakeStripe(t)
	p := newStripe(t, f)
	ctx := context.Background()
	handle, subRef, _ := f.SeedSubscription("cus_acme", stripeSubPrice, stripeMeteredPrice)

	const shared = "same-key-two-operations"
	if _, err := p.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: handle, SubscriptionRef: subRef, Metric: "sum", Period: stripeJuly,
		Quantity: 1, PriceRef: stripeMeteredPrice, IdempotencyKey: shared,
	}); err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}
	_, err := p.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: handle, Kind: KindMetered, Period: stripeJuly,
		PriceRef: stripeMeteredPrice, Quantity: 1, IdempotencyKey: shared, Description: "d",
	})
	if !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("a key reused across operations must fail LOUDLY, got %v", err)
	}
	if !strings.Contains(err.Error(), "idempotency") {
		t.Errorf("the refusal does not name idempotency: %q", err)
	}
}
