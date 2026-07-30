package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/stripefake"
)

// preflight_test.go is P21 task 11.3/11.6: the price-reference preflight, and the three ways it must not
// lie.
//
// The failure it prevents is specific and expensive: a placeholder `price_ref_*` that was never replaced
// with a real Stripe price id, discovered by a rejected charge at the first charge of a billing period.
// So the tests are written around what the report must SAY, not merely whether it returns an error — a
// preflight whose answer an operator cannot act on has moved the discovery earlier and helped nobody.

// fixturePrices are the references the fixture catalog carries, across all four plans.
var fixturePrices = []string{
	"price_ref_team_sub", "price_ref_team_metered",
	"price_ref_biz_sub", "price_ref_biz_metered",
	"price_ref_ent_sub", "price_ref_ent_metered", "price_ref_ent_gainshare",
}

// TestPreflightPassesWhenEveryReferenceResolves is the green path — and it asserts the COUNT, because a
// preflight that checked nothing and said "fine" is the vacuous pass this repository fences against
// everywhere else.
func TestPreflightPassesWhenEveryReferenceResolves(t *testing.T) {
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	for _, ref := range fixturePrices {
		f.SeedPrice(ref, true)
	}

	rep, err := s.svc.PreflightPricing(context.Background())
	if err != nil {
		t.Fatalf("a fully configured account did not preflight clean: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("report = %+v, want OK", rep)
	}
	if rep.Checked != len(fixturePrices) {
		t.Errorf("checked %d reference(s), want %d — a preflight that checked fewer than the catalog "+
			"carries has skipped one, and the one it skipped is the one that will fail", rep.Checked, len(fixturePrices))
	}
	if rep.RanAt.IsZero() {
		t.Error("the report carries no timestamp, so nobody can tell whether it is stale")
	}
	if got := rep.Summary(); got != "verified:7" {
		t.Errorf("summary = %q, want verified:7", got)
	}
}

// TestPreflightNamesEveryUnresolvedReference is the red path — the whole point of the section.
func TestPreflightNamesEveryUnresolvedReference(t *testing.T) {
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	// Everything resolves EXCEPT the Team plan's two references — the placeholder nobody replaced.
	for _, ref := range fixturePrices {
		if strings.Contains(ref, "team") {
			continue
		}
		f.SeedPrice(ref, true)
	}

	rep, err := s.svc.PreflightPricing(context.Background())
	if !errors.Is(err, ErrPricingMisconfigured) {
		t.Fatalf("an unresolvable reference returned %v, want ErrPricingMisconfigured", err)
	}
	if rep.OK() {
		t.Fatal("the report says OK with unresolved references in it")
	}
	if len(rep.Unresolved) != 2 {
		t.Fatalf("%d unresolved, want 2: %+v", len(rep.Unresolved), rep.Unresolved)
	}

	// 🔴 Each row names the three things needed to FIX it. A report that named only the reference would
	// send someone grepping a config store for a string.
	for _, u := range rep.Unresolved {
		if u.PlanID == "" || u.PlanName == "" {
			t.Errorf("unresolved row names no plan: %+v", u)
		}
		if u.Kind == "" {
			t.Errorf("unresolved row names no charge kind: %+v", u)
		}
		if u.PriceRef == "" {
			t.Errorf("unresolved row names no reference: %+v", u)
		}
		if u.Reason == "" {
			t.Errorf("unresolved row carries no reason: %+v", u)
		}
		if u.PlanID != "team" {
			t.Errorf("unexpected plan %q in the unresolved set", u.PlanID)
		}
	}
	// The error itself names the first one, so a deploy log line is actionable without the report.
	if !strings.Contains(err.Error(), "Team") {
		t.Errorf("the error does not name the failing plan: %v", err)
	}
	if got := rep.Summary(); got != "unresolved:2" {
		t.Errorf("summary = %q, want unresolved:2", got)
	}
}

// TestPreflightCatchesAnArchivedPrice is the case a local shape check waves through.
//
// An archived price id is well-formed and RESOLVES. It also cannot be charged on. That is exactly why
// Decision 9 rejected validating the reference's shape locally.
func TestPreflightCatchesAnArchivedPrice(t *testing.T) {
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	for _, ref := range fixturePrices {
		f.SeedPrice(ref, ref != "price_ref_ent_gainshare")
	}

	rep, err := s.svc.PreflightPricing(context.Background())
	if !errors.Is(err, ErrPricingMisconfigured) {
		t.Fatalf("an archived price returned %v, want ErrPricingMisconfigured", err)
	}
	if len(rep.Unresolved) != 1 || rep.Unresolved[0].PriceRef != "price_ref_ent_gainshare" {
		t.Fatalf("unresolved = %+v, want the archived gainshare price", rep.Unresolved)
	}
	if !strings.Contains(rep.Unresolved[0].Reason, "ARCHIVED") {
		t.Errorf("the reason does not say the price is archived, which is a different fix from a wrong id: %q",
			rep.Unresolved[0].Reason)
	}
}

// TestPreflightNamesAProductIdMistakenForAPriceId is the mistake this integration actually made.
//
// A Stripe PRODUCT id is a real object of the wrong kind, and Stripe's own 404 for it says "no such
// price" — true, and it sends the reader looking for a price that was never the problem. The fix is to
// look up the product's prices, so the message says that.
func TestPreflightNamesAProductIdMistakenForAPriceId(t *testing.T) {
	f := newFakeStripe(t)
	p, err := NewStripeProvider(stripeSecrets(t, stripefake.TestKey), ModeTest, stripeClock, WithStripeBaseURL(f.URL()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	err = p.ResolvePrice(context.Background(), "prod_Uyq6ubPcNC0pB1")
	if !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("a product id must be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "PRODUCT id") {
		t.Errorf("the refusal does not say it is a product id: %q", err)
	}
	if !strings.Contains(err.Error(), "/v1/prices?product=") {
		t.Errorf("the refusal does not say how to find the right id: %q", err)
	}
	// It is refused WITHOUT a round trip — the shape is unambiguous and the diagnosis is better than
	// Stripe's own.
	if n := f.Calls("GET /v1/prices/{id}"); n != 0 {
		t.Errorf("a product id cost %d Stripe call(s); the shape alone is conclusive here", n)
	}
}

// TestPreflightDistinguishesAnOutageFromAMisconfiguration.
//
// Reporting an outage as a misconfiguration would send an operator to edit a config store while the
// provider is simply down — and, worse, would produce a list of "unresolved" references that are all
// perfectly fine.
func TestPreflightDistinguishesAnOutageFromAMisconfiguration(t *testing.T) {
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	for _, ref := range fixturePrices {
		f.SeedPrice(ref, true)
	}
	f.SetDown(true)

	rep, err := s.svc.PreflightPricing(context.Background())
	if err != nil {
		t.Fatalf("an outage must not be reported as a misconfiguration error: %v", err)
	}
	if rep.Verified {
		t.Error("the report claims it verified the configuration while the provider was unreachable")
	}
	if len(rep.Unresolved) != 0 {
		t.Errorf("an outage produced %d 'unresolved' references, all of which are fine: %+v",
			len(rep.Unresolved), rep.Unresolved)
	}
	if !strings.Contains(rep.Detail, "unreachable") {
		t.Errorf("the detail does not say the provider was unreachable: %q", rep.Detail)
	}
	if got := rep.Summary(); got != "unverified" {
		t.Errorf("summary = %q, want unverified", got)
	}
}

// TestPreflightReportsUnverifiedWhenTheProviderCannotResolvePrices.
//
// The stub cannot look a price up. That must read as UNVERIFIED, never as a pass — "we did not check"
// and "it is fine" are different facts, and passing off the first as the second is how a deployment
// goes live on a configuration nothing has looked at.
func TestPreflightReportsUnverifiedWhenTheProviderCannotResolvePrices(t *testing.T) {
	s := newParityStack(t, "stub")

	rep, err := s.svc.PreflightPricing(context.Background())
	if err != nil {
		t.Fatalf("a provider that cannot verify is not an error: %v", err)
	}
	if rep.Verified || rep.OK() {
		t.Fatal("a provider that cannot resolve prices reported a clean configuration")
	}
	if rep.Checked != 0 {
		t.Errorf("checked %d with no verifier", rep.Checked)
	}
	if !strings.Contains(rep.Detail, "NOT been checked") {
		t.Errorf("the detail does not say the configuration was not checked: %q", rep.Detail)
	}
}

// TestPreflightIsReadOnly: it resolves prices and creates nothing. A preflight that created a probe
// subscription to prove a price works would be moving money to check whether money can move.
func TestPreflightIsReadOnly(t *testing.T) {
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	for _, ref := range fixturePrices {
		f.SeedPrice(ref, true)
	}

	// Deltas, not totals: the stack's own setup already created a customer, and counting from zero would
	// attribute that to the preflight.
	writes := func() int {
		return f.Calls("POST /v1/subscriptions") + f.Calls("POST /v1/customers") +
			f.Calls("POST /v1/invoiceitems") + f.Calls("POST /v1/checkout/sessions") +
			f.Calls("POST /v1/credit_notes") + f.Calls("POST /v1/refunds")
	}
	before, writesBefore := f.ItemCount()+f.CreditCount(), writes()
	for i := 0; i < 3; i++ {
		if _, err := s.svc.PreflightPricing(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if after := f.ItemCount() + f.CreditCount(); after != before {
		t.Errorf("the preflight created %d object(s) at the provider", after-before)
	}
	if n := writes() - writesBefore; n != 0 {
		t.Errorf("the preflight made %d write call(s) — it resolves prices, it creates nothing", n)
	}
}

// TestPricingStatusDistinguishesNeverRanFromClean is the anti-vacuity rule on the health signal.
func TestPricingStatusDistinguishesNeverRanFromClean(t *testing.T) {
	s := newParityStack(t, "stripe")

	if _, ran := s.svc.PricingStatus(); ran {
		t.Fatal("a preflight that never ran reports as having run")
	}
	if got := s.svc.Describe()["pricing"]; got != "not_run" {
		t.Errorf("Describe pricing = %q, want not_run — a health surface that said 'verified' here would "+
			"tell an operator their pricing is fine when nothing has checked it", got)
	}

	f := currentFake(t, s)
	for _, ref := range fixturePrices {
		f.SeedPrice(ref, true)
	}
	if _, err := s.svc.PreflightPricing(context.Background()); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	rep, ran := s.svc.PricingStatus()
	if !ran || !rep.OK() {
		t.Fatalf("after a clean run: ran=%v report=%+v", ran, rep)
	}
	if got := s.svc.Describe()["pricing"]; got != "verified:7" {
		t.Errorf("Describe pricing = %q, want verified:7", got)
	}
}

// TestPreflightDoesNotGateCharges.
//
// A stale preflight must not block a charge that would succeed, and a clean one must not excuse a charge
// that would fail. The provider stays the authority at charge time; the preflight's job is to make the
// answer available EARLIER, not to become a second opinion that can disagree.
func TestPreflightDoesNotGateCharges(t *testing.T) {
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	// Deliberately seed NOTHING, so the preflight reports every reference unresolved.
	if _, err := s.svc.PreflightPricing(context.Background()); !errors.Is(err, ErrPricingMisconfigured) {
		t.Fatalf("setup: %v", err)
	}

	// The charge still runs, and still succeeds, because the provider — not the cached report — decides.
	sub, err := s.svc.StartSubscription(context.Background(), "cus_acme")
	if err != nil {
		t.Fatalf("a failed preflight blocked a subscription the provider would have accepted: %v", err)
	}
	s.seedMeteredItem(sub.SubscriptionRef)
	if _, err := s.svc.Charge(context.Background(), "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, "sum")); err != nil {
		t.Fatalf("a failed preflight blocked a charge the provider would have accepted: %v", err)
	}
	if n := f.ItemCount(); n != 1 {
		t.Errorf("%d charge objects, want 1", n)
	}
}
