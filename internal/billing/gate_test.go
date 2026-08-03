package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/stripefake"
)

// p21gate_test.go is P21 section 8: the money invariants as machine assertions.
//
// ## What is here, and what is asserted elsewhere
//
// The load-bearing property of this file is CONTRACT PARITY (8.1): the same suite, over the same
// callers, against both `StubProvider` and `StripeProvider`. Everything else in section 8 that is not
// already asserted by the section that built it lives here too:
//
//	8.1 contract parity            THIS FILE — the parity suite below, run against both providers
//	8.2 never-double-charge        THIS FILE (service level) + stripe_test.go (provider level)
//	8.3 persist-then-ack           stripewebhook_test.go TestPersistThenAck (load-bearing)
//	8.4 signature / replay         stripewebhook_test.go — verified before any side effect, stale rejected
//	8.5 entitlement sync           entitlementsync_test.go — both directions, rows intact
//	8.6 reversibility              THIS FILE — wrong charge, credit, originals intact, net right
//	8.7 secret / test-live         stripemode_test.go + secretfence_test.go + scan-bundle.mjs
//	8.8 collection / UI            THIS FILE (server side) + web/console/tests/billing.test.mjs
//	8.9 reconciliation / no-resale THIS FILE — seeded drift surfaced, no resold-token line
//
// That map is written out because a task list that says "done" with a pointer to a test is not
// evidence unless the pointer resolves. Each entry above names a test that exists in this package or
// in the console's suite, and each one fails if the property does.

// ─────────────────────────────────────────────────────────────────────────────
// 8.1 — Contract parity
// ─────────────────────────────────────────────────────────────────────────────

// parityStack is one wired billing stack: the same callers, a different provider underneath.
type parityStack struct {
	svc      *Service
	accounts *account.MemStore
	usage    *metering.MemUsageStore
	// chargeCount is how many DISTINCT charge objects the PROVIDER recorded. The number a
	// never-double-charge assertion is about.
	chargeCount func() int
	// setDown drives the provider into an outage.
	setDown func(bool)
	// failureInjector is the fake behind a stripe stack, for the two tests that legitimately need to
	// drive a failure. It is deliberately not the provider: the parity suite must not be able to reach
	// past the interface, or it would stop being a parity suite.
	failureInjector any
}

// newParityStack wires the whole billing stack around a provider the caller chooses.
//
// It deliberately mirrors newHarness rather than reusing it: the harness builds the stub internally,
// and the whole point here is that the provider is the variable and everything else is identical.
func newParityStack(t *testing.T, providerName string) *parityStack {
	t.Helper()
	ctx := context.Background()

	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(fixtureCatalog)); err != nil {
		t.Fatalf("publish catalog: %v", err)
	}
	plans := plancfg.NewResolver(src, plancfg.NewMemAudit())
	plans.SetClock(func() time.Time { return clockNow })
	if _, err := plans.Reload("fixture"); err != nil {
		t.Fatalf("reload: %v", err)
	}

	secrets, err := NewManagedSecrets(providergateway.StaticSecrets{
		SecretBillingAPIKey:         {APIKey: stripefake.TestKey},
		SecretBillingWebhookSigning: {APIKey: webhookSecret},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	stack := &parityStack{setDown: func(bool) {}}
	var provider Provider
	switch providerName {
	case "stub":
		p := NewStubProvider()
		provider = p
		stack.chargeCount = p.ChargeCount
		stack.setDown = p.SetDown
	case "stripe":
		f := newFakeStripe(t)
		p, perr := NewStripeProvider(secrets, ModeTest, func() time.Time { return clockNow }, WithStripeBaseURL(f.URL()))
		if perr != nil {
			t.Fatalf("stripe provider: %v", perr)
		}
		provider = p
		stack.chargeCount = f.ItemCount
		stack.setDown = f.SetDown
		stack.failureInjector = f
	default:
		t.Fatalf("unknown provider %q", providerName)
	}

	// The SAME call on both: the platform asks the provider for a handle and stores what it gets.
	handle, err := provider.EnsureCustomer(ctx, "cus_acme")
	if err != nil {
		t.Fatalf("%s: EnsureCustomer: %v", providerName, err)
	}
	accts := account.NewMemStore()
	if _, err := accts.Create(account.Account{
		CustomerID: "cus_acme", ProviderCustomerHandle: handle,
		ActivePlanID: "team", PlanConfigVersion: plans.Version(), CreatedAt: julyStart,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	events := metering.NewMemCostEvents()
	events.Attribute("run-a", "cus_acme")
	for i, usd := range []float64{0.25, 0.50, 1.25} {
		events.Put(costEvent("run-a", "run-a|router|"+string(rune('1'+i)), usd, julyStart.Add(time.Duration(i+1)*time.Hour)))
	}
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(events, usage)
	meter.SetClock(func() time.Time { return clockNow })
	if _, _, err := meter.RecordSUM("cus_acme", july); err != nil {
		t.Fatalf("record sum: %v", err)
	}

	svc, err := NewService(provider, NewMemLedger(), accts, plans, meter, secrets)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	svc.SetClock(func() time.Time { return clockNow })
	svc.WithDeliveries(NewMemDeliveries()).WithStates(NewMemStates())
	rollout := NewRollout()
	if err := rollout.Enable(ModeTest); err != nil {
		t.Fatalf("rollout: %v", err)
	}
	svc.WithRollout(rollout)

	stack.svc, stack.accounts, stack.usage = svc, accts, usage
	return stack
}

// TestContractParity is task 8.1 / FR1.
//
// 🔴 One suite, two providers, ZERO caller changes. If P21 had widened the interface or leaked a Stripe
// type into a caller, the second sub-test would not compile — which is the point of running it rather
// than asserting it in a document.
func TestContractParity(t *testing.T) {
	for _, name := range []string{"stub", "stripe"} {
		t.Run(name, func(t *testing.T) {
			s := newParityStack(t, name)
			ctx := context.Background()

			// ── Subscribe on the plan's opaque price reference ────────────────
			sub, err := s.svc.StartSubscription(ctx, "cus_acme")
			if err != nil {
				t.Fatalf("StartSubscription: %v", err)
			}
			if sub.SubscriptionRef == "" || sub.Status == "" {
				t.Fatalf("subscription = %+v, want a handle and the provider's own status", sub)
			}

			// The change is AUDITED even though it moves no money yet.
			if rows := testEvents(s.svc.Ledger(), "cus_acme", ""); len(rows) != 1 || rows[0].Type != TypeSubscriptionChange {
				t.Fatalf("expected one subscription_change row, got %+v", rows)
			}

			// ── Report the period's metered usage ─────────────────────────────
			res, err := s.svc.ReportUsage(ctx, "cus_acme", july, metering.MetricSUM)
			if err != nil {
				t.Fatalf("ReportUsage: %v", err)
			}
			if res.UsageRef == "" {
				t.Fatal("no usage handle")
			}
			// The DOWNSTREAM read path, not the return value: the usage record carries the hand-off.
			rec, err := s.usage.Get(metering.Key{CustomerID: "cus_acme", Period: july.ID, Metric: metering.MetricSUM})
			if err != nil {
				t.Fatalf("read back usage: %v", err)
			}
			if !rec.ReportedToProvider || rec.ProviderUsageRef != res.UsageRef {
				t.Errorf("the usage record did not record the hand-off: %+v", rec)
			}

			// ── Charge, and charge again under the same key ───────────────────
			key := MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))
			first, err := s.svc.Charge(ctx, "cus_acme", july, KindMetered, key)
			if err != nil {
				t.Fatalf("Charge: %v", err)
			}
			if first.Status != StatusRecorded || first.ProviderRef == "" || first.AmountRef == "" {
				t.Fatalf("the charge did not settle: %+v", first)
			}
			if first.Quantity != 2 {
				t.Errorf("charged quantity = %v, want the period's SUM of 2", first.Quantity)
			}
			again, err := s.svc.Charge(ctx, "cus_acme", july, KindMetered, key)
			if err != nil {
				t.Fatalf("retry: %v", err)
			}
			if again.EventID != first.EventID {
				t.Fatalf("the retry wrote a SECOND ledger row %s (first %s)", again.EventID, first.EventID)
			}
			if n := s.chargeCount(); n != 1 {
				t.Errorf("the provider recorded %d charge objects, want 1", n)
			}

			// ── Correct it, additively ────────────────────────────────────────
			issuePeriodInvoice(t, s.svc.Provider(), "cus_acme", july.ID)
			credit, err := s.svc.Credit(ctx, "cus_acme", first.EventID, "parity: corrected")
			if err != nil {
				t.Fatalf("Credit: %v", err)
			}
			if credit.Type != TypeCredit || credit.CausedBy != "billing_event:"+first.EventID {
				t.Errorf("the credit does not name what it corrects: %+v", credit)
			}
			original, ok := findRow(s.svc, first.EventID)
			if !ok || original.Status != StatusRecorded {
				t.Errorf("the ORIGINAL charge was removed or altered by the correction: %+v", original)
			}

			// ── Read the invoice back ─────────────────────────────────────────
			inv, err := s.svc.Provider().Invoice(ctx, "cus_acme", july.ID)
			if err != nil {
				t.Fatalf("Invoice: %v", err)
			}
			if err := inv.Validate(); err != nil {
				t.Errorf("the read-back invoice does not validate: %v", err)
			}
			if len(inv.Lines) == 0 {
				t.Error("the invoice read back with no lines")
			}
			for _, l := range inv.Lines {
				if l.Basis == "" {
					t.Errorf("line %+v names no basis", l)
				}
			}

			// ── Recorded usage is readable for reconciliation ─────────────────
			recorded, err := s.svc.Provider().RecordedUsage(ctx, "cus_acme", july.ID)
			if err != nil {
				t.Fatalf("RecordedUsage: %v", err)
			}
			if len(recorded) == 0 {
				t.Error("the provider reports no recorded usage, so reconciliation has nothing to compare")
			}

			// ── The outage story, identical on both ───────────────────────────
			s.setDown(true)
			subKey := SubscriptionChargeIdempotencyKey("cus_acme", july.ID, "team")
			buffered, err := s.svc.Charge(ctx, "cus_acme", july, KindSubscription, subKey)
			if !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("a provider outage returned %v, want ErrProviderUnavailable so the charge buffers", err)
			}
			if buffered.Status != StatusPending {
				t.Errorf("the buffered row is %s, want pending", buffered.Status)
			}
			s.setDown(false)
			settled, stillPending, err := s.svc.FlushPending(ctx)
			if err != nil {
				t.Fatalf("FlushPending: %v", err)
			}
			if len(settled) != 1 || len(stillPending) != 0 {
				t.Fatalf("recovery settled %d and left %d pending, want 1 and 0", len(settled), len(stillPending))
			}
			if n := s.chargeCount(); n != 2 {
				t.Errorf("the outage window produced %d charge objects, want 2 (the metered one and the subscription one, each once)", n)
			}
		})
	}
}

func findRow(svc *Service, eventID string) (BillingEvent, bool) {
	for _, ev := range testEvents(svc.Ledger(), "cus_acme", "") {
		if ev.EventID == eventID {
			return ev, true
		}
	}
	return BillingEvent{}, false
}

// ─────────────────────────────────────────────────────────────────────────────
// 8.2 — Never double-charge, at the SERVICE level
// ─────────────────────────────────────────────────────────────────────────────

// TestNeverDoubleChargeAcrossBothLayers is task 8.2 / FR2, FR7, FR14.
//
// The provider-level version is in stripe_test.go. This is the one that matters operationally: the
// LEDGER and STRIPE together, under the nastiest real failure — the provider records the charge and
// the response is lost.
func TestNeverDoubleChargeAcrossBothLayers(t *testing.T) {
	s := newParityStack(t, "stripe")
	ctx := context.Background()
	key := MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))

	// The recorded-then-lost failure. The row is written ahead, the provider records, the response dies.
	fake := currentFake(t, s)
	fake.SetFailAfterRecord(true)
	buffered, err := s.svc.Charge(ctx, "cus_acme", july, KindMetered, key)
	if err == nil {
		t.Fatal("the ambiguous failure did not surface — an invisible maybe-charge is the worst outcome")
	}
	if buffered.Status != StatusPending {
		t.Fatalf("the write-ahead row is %s, want pending so a retry can resume it", buffered.Status)
	}

	// Ten retries under the SAME key.
	for i := 0; i < 10; i++ {
		row, rerr := s.svc.Charge(ctx, "cus_acme", july, KindMetered, key)
		if rerr != nil {
			t.Fatalf("retry %d: %v", i, rerr)
		}
		if row.EventID != buffered.EventID {
			t.Fatalf("retry %d wrote a SECOND ledger row %s", i, row.EventID)
		}
	}

	// 🔴 One Stripe object, one ledger row. Both layers refused the duplicate.
	if n := s.chargeCount(); n != 1 {
		t.Errorf("stripe holds %d charge objects after a recorded-then-lost failure and ten retries, want 1", n)
	}
	rows := 0
	for _, ev := range testEvents(s.svc.Ledger(), "cus_acme", july.ID) {
		if ev.IdempotencyKey == key {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("%d ledger rows carry the key, want 1", rows)
	}
	settled, _ := findRow(s.svc, buffered.EventID)
	if settled.Status != StatusRecorded || settled.ProviderRef == "" {
		t.Errorf("the resumed row never settled against the original Stripe object: %+v", settled)
	}
}

// currentFake recovers the fake behind a stripe parity stack. It exists because parityStack
// deliberately hides the provider — the parity suite must not be able to reach past the interface —
// and this one test legitimately needs the failure injector.
func currentFake(t *testing.T, s *parityStack) *stripefake.Server {
	t.Helper()
	f, ok := s.failureInjector.(*stripefake.Server)
	if !ok {
		t.Fatal("this stack has no failure injector")
	}
	return f
}

// ─────────────────────────────────────────────────────────────────────────────
// 8.6 — Reversibility (load-bearing)
// ─────────────────────────────────────────────────────────────────────────────

// TestReversibilityOverStripe is task 8.6 / FR5, NFR5.
//
// A wrong charge is corrected FORWARD. Nothing is deleted, nothing is edited, and the net is right —
// which is the only definition of "reversible" that survives an auditor asking what happened.
func TestReversibilityOverStripe(t *testing.T) {
	s := newParityStack(t, "stripe")
	ctx := context.Background()

	if _, err := s.svc.StartSubscription(ctx, "cus_acme"); err != nil {
		t.Fatalf("StartSubscription: %v", err)
	}

	wrong, err := s.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM)))
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	before := len(testEvents(s.svc.Ledger(), "cus_acme", ""))

	issuePeriodInvoice(t, s.svc.Provider(), "cus_acme", july.ID)
	credit, err := s.svc.Credit(ctx, "cus_acme", wrong.EventID, "billed against the wrong period")
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}

	// 1. The ledger GREW. A correction is a new row.
	after := testEvents(s.svc.Ledger(), "cus_acme", "")
	if len(after) != before+1 {
		t.Fatalf("the ledger went from %d to %d rows — a correction adds exactly one", before, len(after))
	}

	// 2. The ORIGINAL is byte-identical apart from nothing. It is still recorded, still names its cause,
	//    still carries its provider refs.
	original, ok := findRow(s.svc, wrong.EventID)
	if !ok {
		t.Fatal("the original charge is GONE — corrections are additive")
	}
	if original.Status != StatusRecorded || original.ProviderRef != wrong.ProviderRef || original.Quantity != wrong.Quantity {
		t.Errorf("the original was MODIFIED: %+v -> %+v", wrong, original)
	}

	// 3. The correction NAMES what it corrects and WHY. A credit that explains nothing is
	//    indistinguishable from a mistake six months later.
	if credit.CausedBy != "billing_event:"+wrong.EventID {
		t.Errorf("the credit does not name the charge it corrects: %q", credit.CausedBy)
	}
	if credit.Reason == "" {
		t.Error("the credit carries no reason")
	}

	// 4. The NET is right: one charge of q, one credit of q, so the customer nets zero for it.
	var charged, credited float64
	for _, ev := range after {
		switch ev.Type {
		case TypeCharge, TypeGainshareCharge:
			charged += ev.Quantity
		case TypeCredit, TypeRefund:
			credited += ev.Quantity
		}
	}
	if charged != credited {
		t.Errorf("charged %v against credited %v — the correction did not net out", charged, credited)
	}

	// 5. Stripe holds BOTH objects. An auditor sees the mistake and its correction.
	if s.chargeCount() != 1 {
		t.Errorf("stripe holds %d charge objects, want the original still there", s.chargeCount())
	}
	f := currentFake(t, s)
	if f.CreditCount() != 1 {
		t.Errorf("stripe holds %d correction objects, want 1", f.CreditCount())
	}

	// 6. A repeated correction is the SAME correction, not a second one.
	repeat, err := s.svc.Credit(ctx, "cus_acme", wrong.EventID, "billed against the wrong period")
	if err != nil {
		t.Fatalf("repeat credit: %v", err)
	}
	if repeat.EventID != credit.EventID {
		t.Errorf("repeating a correction issued a SECOND one: %s then %s", credit.EventID, repeat.EventID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 8.8 — Collection, server side
// ─────────────────────────────────────────────────────────────────────────────

// TestCollectionFlowOverStripe is task 8.8's server half: checkout → subscription → upgrade →
// downgrade → past due, with no card and no secret anywhere in the platform's hands.
//
// The browser half — the page renders each state, the card never posts to the platform, the bundle
// carries no secret and no price — is web/console/tests/billing.test.mjs plus the build-time bundle
// scan.
func TestCollectionFlowOverStripe(t *testing.T) {
	s := newParityStack(t, "stripe")
	ctx := context.Background()

	// ── Checkout, minted server-side ──────────────────────────────────────────
	session, err := s.svc.StartCheckout(ctx, "cus_acme", "Team",
		"https://console.example/app/billing?checkout=done", "https://console.example/app/billing?checkout=canceled")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	if session.URL == "" && session.ClientSecret == "" {
		t.Fatal("the session points nowhere — the browser has no way to reach the provider")
	}
	// 🔴 What comes back is a pointer at the provider's page. There is no field on it that could carry
	// a card, and no Stripe key in it.
	if strings.Contains(session.URL, "sk_test_") || strings.Contains(session.URL, "sk_live_") {
		t.Error("the checkout session carries a Stripe key")
	}

	// ── The plan name is the only identifier the console sends ────────────────
	if _, err := s.svc.StartCheckout(ctx, "cus_acme", "NoSuchPlan", "https://a/", "https://b/"); !errors.Is(err, ErrUnknownPlanName) {
		t.Errorf("an unknown plan name returned %v, want ErrUnknownPlanName", err)
	}

	// ── With no subscription yet, a plan change is a CHECKOUT, not a failure ──
	pending, err := s.svc.ChangePlan(ctx, "cus_acme", "Business")
	if err != nil {
		t.Fatalf("ChangePlan before any subscription: %v", err)
	}
	if !pending.CheckoutRequired {
		t.Error("a plan change with no payment method must report CheckoutRequired — 'you have not paid yet' is a step, not an error")
	}

	if _, err := s.svc.StartSubscription(ctx, "cus_acme"); err != nil {
		t.Fatalf("StartSubscription: %v", err)
	}

	// ── Upgrade, then downgrade — each an audited plan change ─────────────────
	up, err := s.svc.ChangePlan(ctx, "cus_acme", "Business")
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if !up.Changed || up.PlanID != "business" {
		t.Fatalf("upgrade = %+v", up)
	}
	acct, _ := s.accounts.Get("cus_acme")
	if acct.ActivePlanID != "business" {
		t.Errorf("the entitlement did not flip at the plan-change event: plan is %s", acct.ActivePlanID)
	}

	down, err := s.svc.ChangePlan(ctx, "cus_acme", "Team")
	if err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if !down.Changed || down.PlanID != "team" {
		t.Fatalf("downgrade = %+v", down)
	}

	// Two audited rows, nothing deleted.
	changes := 0
	for _, ev := range testEvents(s.svc.Ledger(), "cus_acme", "") {
		if ev.Type == TypePlanChange {
			changes++
		}
	}
	if changes != 2 {
		t.Errorf("%d plan-change rows for an upgrade and a downgrade, want 2", changes)
	}

	// ── Past due arrives as mirrored provider state, and changes no plan ──────
	body := []byte(`{"id":"evt_pastdue","type":"invoice.payment_failed","customer_id":"cus_acme","invoice_ref":"in_1"}`)
	ack := s.svc.HandleStripeWebhook(ctx, body, StripeSignatureFor(webhookSecret, clockNow, body))
	if ack.Status != 200 {
		t.Fatalf("payment_failed webhook: %+v", ack)
	}
	if st := s.svc.BillingState("cus_acme"); !st.PaymentFailed {
		t.Error("the dunning state was not mirrored, so the console has nothing to render")
	}
	acct, _ = s.accounts.Get("cus_acme")
	if acct.ActivePlanID != "team" {
		t.Errorf("the grace window moved the plan to %s — dunning is the provider's schedule", acct.ActivePlanID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 8.9 — Reconciliation and no-resale
// ─────────────────────────────────────────────────────────────────────────────

// TestReconciliationSurfacesDriftAgainstStripe is task 8.9 / NFR6.
//
// The customer finds out from the platform, not from their statement. A drift that is silently accepted
// is a billing dispute the platform has chosen to lose later.
func TestReconciliationSurfacesDriftAgainstStripe(t *testing.T) {
	s := newParityStack(t, "stripe")
	ctx := context.Background()

	if _, err := s.svc.StartSubscription(ctx, "cus_acme"); err != nil {
		t.Fatalf("StartSubscription: %v", err)
	}

	// The platform recorded the period's usage and never reported it. Stripe therefore has none — the
	// "the provider is missing our usage" drift.
	alerts := &metering.MemAlerter{}
	res, err := s.svc.Reconcile(ctx, "cus_acme", july, alerts)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Drift) == 0 {
		t.Fatal("a period the platform metered and never reported reconciled CLEAN — the drift is invisible")
	}
	named := false
	for _, d := range res.Drift {
		if d.Metric == metering.MetricSUM {
			named = true
		}
		if d.Detail == "" {
			t.Errorf("drift %+v carries no detail — an alert with no subject sends an operator to read dashboards", d)
		}
	}
	if !named {
		t.Errorf("the drift does not name the SUM meter: %+v", res.Drift)
	}

	// 🔴 The permitted repair is ADDITIVE: re-report the usage the provider is missing, under its own
	// key. Nothing reduces or overwrites the provider's record.
	repaired, err := s.svc.RepairUnreported(ctx, "cus_acme", july, res)
	if err != nil {
		t.Fatalf("RepairUnreported: %v", err)
	}
	if len(repaired) == 0 {
		t.Error("the repair reported nothing")
	}
	after, err := s.svc.Reconcile(ctx, "cus_acme", july, alerts)
	if err != nil {
		t.Fatalf("re-reconcile: %v", err)
	}
	if len(after.Drift) != 0 {
		t.Errorf("the drift survived an idempotent re-report: %+v", after.Drift)
	}
}

// TestNoResoldTokenLineSurvivesTheStripeReadBack is task 8.9's second half / FR13.
//
// The platform bills for its service and its verified savings. Provider spend is the customer's, on the
// customer's keys. P7 already asserts the rule over an in-memory invoice
// (TestNoInvoiceLineResellsProviderTokens); what this adds is the LIVE path — a Stripe invoice that
// actually carries such a line fails the read, so it never reaches a customer.
func TestNoResoldTokenLineSurvivesTheStripeReadBack(t *testing.T) {
	// The platform cannot produce one: the charge kinds are a closed set.
	for _, k := range []ChargeKind{"provider_tokens", "resold_tokens", "llm_passthrough", "token_markup", "tokens"} {
		if KnownChargeKind(k) {
			t.Errorf("%q is a known charge kind — the no-resale rule is not a closed enum", k)
		}
	}

	// And a provider that produced one is REJECTED on the way in, before anything renders it.
	for _, kind := range []LineKind{"provider_tokens", "resold_tokens", "llm_passthrough", "token_markup", "tokens"} {
		inv := Invoice{InvoiceRef: "in_1", CustomerID: "cus_acme", Period: july.ID,
			Lines: []InvoiceLine{{Kind: kind, Basis: "b", AmountRef: "a"}}}
		if err := inv.Validate(); !errors.Is(err, ErrResoldTokens) {
			t.Errorf("a %q line validated: %v", kind, err)
		}
	}

	// The live path: a Stripe invoice carrying one fails the read, so it never reaches a customer.
	s := newParityStack(t, "stripe")
	f := currentFake(t, s)
	acct, _ := s.accounts.Get("cus_acme")
	f.SeedTokenLine(acct.ProviderCustomerHandle, july.ID, "provider_tokens", "LLM tokens resold")
	if _, err := s.svc.Provider().Invoice(context.Background(), "cus_acme", july.ID); !errors.Is(err, ErrResoldTokens) {
		t.Errorf("a resold-token line survived the read-back: %v", err)
	}
}

// issuePeriodInvoice makes the period's invoice FINAL where the provider can do that, so a correction
// has something to credit.
//
// Guarded rather than asserted, and the guard is the honest part: Stripe refuses a credit note against
// a draft invoice, while the stub provider has no invoice lifecycle at all. Requiring the capability
// would fail the stub for lacking a concept it does not need; skipping the step for Stripe would fail
// the correction. Parity means both run the same test, not that both take the same wire steps.
func issuePeriodInvoice(t *testing.T, p Provider, customerID, period string) {
	t.Helper()
	issuer, ok := p.(InvoiceIssuer)
	if !ok {
		return
	}
	if _, err := issuer.IssueInvoice(context.Background(), customerID, period); err != nil {
		t.Fatalf("IssueInvoice: %v", err)
	}
}
