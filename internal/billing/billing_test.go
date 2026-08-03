package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// fixtureCatalog is the test config store: four NAMED plans with limits and OPAQUE price references.
// No amount appears here or anywhere else in the package — a price is a provider handle.
const fixtureCatalog = `{
  "version": "cfg-test-1",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":100,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":1000,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":10000,"seats":25,"retention_days":90,"eval_compute":1000},
     "price_refs":{"subscription":"price_ref_biz_sub","metered":"price_ref_biz_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

var (
	julyStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	july      = metering.MonthPeriod(julyStart)
	clockNow  = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
)

// harness is the whole 7a stack wired the way the service ships: a config-store plan resolver, an
// account store, the P2.5 cost-event substrate behind a real Meter, the append-only ledger, and the
// Stripe-style provider stub.
type harness struct {
	t        *testing.T
	plans    *plancfg.Resolver
	accounts *account.MemStore
	events   *metering.MemCostEvents
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	ledger   *MemLedger
	provider *StubProvider
	svc      *Service
}

func newHarness(t *testing.T, planID string) *harness {
	t.Helper()
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(fixtureCatalog)); err != nil {
		t.Fatalf("publish catalog: %v", err)
	}
	plans := plancfg.NewResolver(src, plancfg.NewMemAudit())
	plans.SetClock(func() time.Time { return clockNow })
	if _, err := plans.Reload("fixture"); err != nil {
		t.Fatalf("reload: %v", err)
	}

	prov := NewStubProvider()
	handle, err := prov.EnsureCustomer(context.Background(), "cus_acme")
	if err != nil {
		t.Fatalf("ensure customer: %v", err)
	}
	accts := account.NewMemStore()
	if _, err := accts.Create(account.Account{
		CustomerID: "cus_acme", ProviderCustomerHandle: handle,
		ActivePlanID: planID, PlanConfigVersion: plans.Version(), CreatedAt: julyStart,
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

	ledger := NewMemLedger()
	secrets, err := NewManagedSecrets(providergateway.StaticSecrets{
		SecretBillingAPIKey:         {APIKey: "billing-api-key-DO-NOT-LEAK-test"},
		SecretBillingWebhookSigning: {APIKey: "webhook-signing-secret-DO-NOT-LEAK-test"},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	svc, err := NewService(prov, ledger, accts, plans, meter, secrets)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	svc.SetClock(func() time.Time { return clockNow })

	return &harness{t: t, plans: plans, accounts: accts, events: events, usage: usage,
		meter: meter, ledger: ledger, provider: prov, svc: svc}
}

// costEvent builds a real P2.5 cost event.
func costEvent(runID, invocationID string, usd float64, ts time.Time) metricevent.Event {
	seed := int64(1)
	v := usd
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     "var-1", RunID: runID, NodeID: "router", CaseID: "case-1", Seed: &seed,
		Timestamp:  ts.UTC().Format(time.RFC3339Nano),
		ConfigHash: "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0",
		MetricName: telemetry.MetricCostUSD, Value: &v, Unit: telemetry.UnitUSD,
		Dimensions: map[string]any{telemetry.AttrInvocationID: invocationID},
	}
}

const wantSUM = 0.25 + 0.50 + 1.25

// TestSubscriptionAndMeteredUsageGoThroughTheProvider is task 3.1 / the billing spec's first scenario:
// the platform reports subscription and metered usage to the provider and stores only HANDLES.
func TestSubscriptionAndMeteredUsageGoThroughTheProvider(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()

	sub, err := h.svc.StartSubscription(ctx, "cus_acme")
	if err != nil {
		t.Fatalf("start subscription: %v", err)
	}
	if !strings.HasPrefix(sub.SubscriptionRef, "prov_sub_") || sub.Status != "active" {
		t.Errorf("subscription = %+v", sub)
	}

	res, err := h.svc.ReportUsage(ctx, "cus_acme", july, metering.MetricSUM)
	if err != nil {
		t.Fatalf("report usage: %v", err)
	}
	if res.UsageRef == "" {
		t.Error("no provider usage ref returned")
	}

	// Downstream read path: the usage record now carries the provider hand-off.
	rec, err := h.usage.Get(metering.Key{CustomerID: "cus_acme", Period: july.ID, Metric: metering.MetricSUM})
	if err != nil {
		t.Fatalf("read back usage: %v", err)
	}
	if !rec.ReportedToProvider || rec.ProviderUsageRef != res.UsageRef {
		t.Errorf("usage record did not record the hand-off: %+v", rec)
	}
	if rec.Quantity != wantSUM {
		t.Errorf("reported quantity = %v, want %v", rec.Quantity, wantSUM)
	}

	// The platform holds HANDLES, never card data — the account model refuses the latter outright.
	acct, _ := h.accounts.Get("cus_acme")
	if !strings.HasPrefix(acct.ProviderCustomerHandle, "prov_cus_") {
		t.Errorf("provider handle = %q", acct.ProviderCustomerHandle)
	}
}

// TestProrationAndDunningAreTheProviders is the billing spec's second scenario: the platform REFLECTS
// the provider's dunning state and does not reimplement it.
func TestProrationAndDunningAreTheProviders(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	if _, err := h.svc.StartSubscription(ctx, "cus_acme"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	h.provider.SetSubscriptionStatus(h.svc.SubscriptionRef("cus_acme"), "past_due")
	got, err := h.svc.SubscriptionState(ctx, "cus_acme")
	if err != nil {
		t.Fatalf("subscription state: %v", err)
	}
	if got.Status != "past_due" {
		t.Errorf("status = %q, want the provider's past_due — the platform must reflect, not recompute", got.Status)
	}
}

// TestRetriedMeteredReportYieldsOneProviderCharge is task 3.5 / FR10: a retried metered-usage report
// for the same {customer, period, metric} yields ONE provider charge.
//
// It asserts on three independent facts, because any one alone can be true for the wrong reason:
// the provider recorded one charge, the ledger holds one row, and the provider was genuinely CALLED
// more than once.
func TestRetriedMeteredReportYieldsOneProviderCharge(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()

	key := MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))
	const retries = 6
	var last BillingEvent
	for i := 0; i < retries; i++ {
		if _, err := h.svc.ReportUsage(ctx, "cus_acme", july, metering.MetricSUM); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
		ev, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered, key)
		if err != nil {
			t.Fatalf("charge %d: %v", i, err)
		}
		last = ev
	}

	if h.provider.Calls("ReportUsage") != retries {
		t.Fatalf("the provider saw %d usage reports for %d retries — the test did not actually retry",
			h.provider.Calls("ReportUsage"), retries)
	}
	if got := h.provider.UsageCount(); got != 1 {
		t.Errorf("the provider recorded %d usage records, want 1", got)
	}
	if got := h.provider.ChargeCount(); got != 1 {
		t.Errorf("the provider recorded %d charges, want exactly 1", got)
	}
	rows := testEvents(h.ledger, "cus_acme", july.ID)
	charges := 0
	for _, r := range rows {
		if r.Type == TypeCharge {
			charges++
		}
	}
	if charges != 1 {
		t.Errorf("the ledger holds %d charge rows, want 1", charges)
	}
	if last.Status != StatusRecorded || last.ProviderRef == "" {
		t.Errorf("the final charge is not settled: %+v", last)
	}
	if last.Quantity != wantSUM {
		t.Errorf("charged quantity = %v, want the period's SUM %v", last.Quantity, wantSUM)
	}
}

// TestAmbiguousProviderFailureDoesNotDoubleCharge is the nastiest retry case: the provider RECORDS the
// charge and then the response is lost. A naive retry raises a second charge; the shared idempotency
// key must make it a no-op.
func TestAmbiguousProviderFailureDoesNotDoubleCharge(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	key := MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))

	h.provider.SetFailAfterRecord(true)
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered, key); err == nil {
		t.Fatal("precondition: the ambiguous failure must surface as an error")
	}
	if h.provider.ChargeCount() != 1 {
		t.Fatalf("precondition: the provider should have recorded the charge before failing, got %d", h.provider.ChargeCount())
	}

	// The retry — same key.
	ev, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered, key)
	if err != nil {
		t.Fatalf("retry after ambiguous failure: %v", err)
	}
	if h.provider.ChargeCount() != 1 {
		t.Errorf("the provider recorded %d charges after the retry, want 1 — the customer was double-charged", h.provider.ChargeCount())
	}
	if ev.Status != StatusRecorded {
		t.Errorf("the retry did not settle the row: %+v", ev)
	}
	if h.provider.Calls("RaiseCharge") != 2 {
		t.Errorf("the provider was called %d times; the test must genuinely have retried", h.provider.Calls("RaiseCharge"))
	}
}

// TestProviderOutageBuffersAndBillsOnce is task 3.5's outage NFR: provider down → usage buffered →
// reported on recovery → billed ONCE.
func TestProviderOutageBuffersAndBillsOnce(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	key := MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))

	h.provider.SetDown(true)
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered, key); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("want ErrProviderUnavailable during the outage, got %v", err)
	}
	// Retry during the outage — still buffered, still one row.
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered, key); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("second attempt during the outage: %v", err)
	}
	pending := testPending(h.ledger)
	if len(pending) != 1 {
		t.Fatalf("buffered rows = %d, want exactly 1 (the usage is safe, the charge is deferred)", len(pending))
	}
	if h.provider.ChargeCount() != 0 {
		t.Fatalf("the provider recorded a charge during its own outage: %d", h.provider.ChargeCount())
	}

	// Recovery.
	h.provider.SetDown(false)
	settled, still, err := h.svc.FlushPending(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if len(settled) != 1 || len(still) != 0 {
		t.Fatalf("after recovery: settled=%d stillPending=%d, want 1 and 0", len(settled), len(still))
	}
	if h.provider.ChargeCount() != 1 {
		t.Errorf("the outage window was billed %d times, want exactly once", h.provider.ChargeCount())
	}

	// And a post-recovery retry of the original call still does not double-charge.
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered, key); err != nil {
		t.Fatalf("post-recovery retry: %v", err)
	}
	if h.provider.ChargeCount() != 1 {
		t.Errorf("a post-recovery retry double-charged: %d", h.provider.ChargeCount())
	}
}

// TestNoInvoiceLineResellsProviderTokens is task 3.4 / FR13. Three independent assertions, because
// no-resale must hold structurally rather than by inspection of one invoice:
//
//	(a) the charge KINDS are a closed set with no token member — a resold-token charge is unrepresentable;
//	(b) the provider refuses a charge of any other kind;
//	(c) every invoice line the platform reads back passes Invoice.Validate, which rejects the token
//	    shapes outright.
func TestNoInvoiceLineResellsProviderTokens(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()

	// (a) the closed set.
	for _, k := range ChargeKinds {
		if forbiddenLineKinds[LineKind(k)] {
			t.Errorf("charge kind %q is a resold-token kind and must not exist", k)
		}
	}
	if KnownChargeKind("provider_tokens") || KnownChargeKind("tokens") {
		t.Error("a token-passthrough charge kind is representable")
	}

	// (b) the service and the provider both refuse one.
	if _, err := h.svc.Charge(ctx, "cus_acme", july, ChargeKind("provider_tokens"), "k1"); err == nil {
		t.Error("the service raised a resold-token charge")
	}
	acct, _ := h.accounts.Get("cus_acme")
	if _, err := h.provider.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: acct.ProviderCustomerHandle, Kind: "provider_tokens",
		Period: july.ID, PriceRef: "p", IdempotencyKey: "k2",
	}); err == nil {
		t.Error("the provider accepted a resold-token charge")
	}

	// (c) a real invoice, built from real charges, has no token line.
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindSubscription,
		SubscriptionChargeIdempotencyKey("cus_acme", july.ID, "team")); err != nil {
		t.Fatalf("subscription charge: %v", err)
	}
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); err != nil {
		t.Fatalf("metered charge: %v", err)
	}
	inv, err := h.provider.Invoice(ctx, "cus_acme", july.ID)
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if err := inv.Validate(); err != nil {
		t.Errorf("the invoice failed the no-resale check: %v", err)
	}
	if len(inv.Lines) != 2 {
		t.Fatalf("invoice lines = %d, want 2 (subscription + metered)", len(inv.Lines))
	}
	for _, l := range inv.Lines {
		if l.Basis == "" {
			t.Errorf("line %+v names no basis — every line must trace to the usage that justified it", l)
		}
	}
}

// TestInvoiceValidateGoesRed proves the no-resale fence can FAIL: a fence that has never been seen to
// reject anything is decoration.
func TestInvoiceValidateGoesRed(t *testing.T) {
	for _, kind := range []LineKind{"provider_tokens", "resold_tokens", "llm_passthrough", "token_markup", "tokens"} {
		inv := Invoice{Lines: []InvoiceLine{{Kind: kind, Basis: "b"}}}
		if err := inv.Validate(); !errors.Is(err, ErrResoldTokens) {
			t.Errorf("a %q line was accepted (err=%v)", kind, err)
		}
	}
	if err := (Invoice{Lines: []InvoiceLine{{Kind: LineMetered}}}).Validate(); err == nil {
		t.Error("a line with no basis was accepted")
	}
	if err := (Invoice{Lines: []InvoiceLine{{Kind: "mystery", Basis: "b"}}}).Validate(); err == nil {
		t.Error("a line of unknown kind was accepted")
	}
	ok := Invoice{Lines: []InvoiceLine{
		{Kind: LineSubscription, Basis: "plan:team@cfg-test-1"},
		{Kind: LineMetered, Basis: "usage_record:cus_acme/2026-07/sum"},
		{Kind: LineGainshare, Basis: "billable_savings:cus_acme/2026-07"},
		{Kind: LineCredit, Basis: "billing_event:be_1"},
	}}
	if err := ok.Validate(); err != nil {
		t.Errorf("a legitimate invoice was rejected: %v", err)
	}
}

// TestBillingSecretsComeFromTheSecretsManagerAndNeverLeak is task 3.3 / 9.6.
//
// It asserts three things: the credentials come from the shared secrets SOURCE (not code, not config),
// the source is externally nameable for the health surface, and no secret VALUE appears in anything the
// package produces — the ledger rows, the Describe output, or an error message.
func TestBillingSecretsComeFromTheSecretsManagerAndNeverLeak(t *testing.T) {
	const apiKey = "billing-api-key-DO-NOT-LEAK-test"
	const webhookSecret = "webhook-signing-secret-DO-NOT-LEAK-test"
	h := newHarness(t, "team")
	ctx := context.Background()

	secrets, err := NewManagedSecrets(providergateway.StaticSecrets{
		SecretBillingAPIKey:         {APIKey: apiKey},
		SecretBillingWebhookSigning: {APIKey: webhookSecret},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	got, err := secrets.APIKey(ctx)
	if err != nil || got != apiKey {
		t.Fatalf("APIKey = %q, %v", got, err)
	}
	gotWH, err := secrets.WebhookSigningSecret(ctx)
	if err != nil || gotWH != webhookSecret {
		t.Fatalf("WebhookSigningSecret = %q, %v", gotWH, err)
	}

	// The source names itself (health signal) without naming what is behind it.
	info := secrets.Describe()
	if info.Kind == "" {
		t.Error("the secrets source does not name itself — /readyz cannot report which one is live")
	}
	if strings.Contains(info.Kind+info.Detail, apiKey) || strings.Contains(info.Kind+info.Detail, webhookSecret) {
		t.Error("Describe leaked a secret value")
	}

	// A missing credential FAILS CLOSED and names WHICH one, never its value.
	empty, _ := NewManagedSecrets(providergateway.StaticSecrets{})
	if _, err := empty.APIKey(ctx); !errors.Is(err, ErrSecretUnavailable) {
		t.Errorf("a missing billing key must fail closed, got %v", err)
	}

	// No secret reaches the ledger, the health description, or a charge row.
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); err != nil {
		t.Fatalf("charge: %v", err)
	}
	blob := describeBlob(t, h)
	for _, secret := range []string{apiKey, webhookSecret} {
		if strings.Contains(blob, secret) {
			t.Errorf("a secret value reached the billing output surface")
		}
	}
}

// describeBlob concatenates everything the billing capability exposes outward — the health
// description and every ledger row — so a single scan can assert no secret is anywhere in it.
func describeBlob(t *testing.T, h *harness) string {
	t.Helper()
	var sb strings.Builder
	for k, v := range h.svc.Describe() {
		sb.WriteString(k + "=" + v + "\n")
	}
	for _, ev := range testEvents(h.ledger, "cus_acme", "") {
		sb.WriteString(ev.EventID + "|" + ev.IdempotencyKey + "|" + ev.ProviderRef + "|" +
			ev.AmountRef + "|" + ev.CausedBy + "|" + ev.Reason + "|" + strings.Join(ev.Evidence, ",") + "\n")
	}
	return sb.String()
}

// TestLedgerRefusesUnsoundRows: each of these would be a charge nobody can explain at month-end.
func TestLedgerRefusesUnsoundRows(t *testing.T) {
	l := NewMemLedger()
	base := BillingEvent{CustomerID: "c", Type: TypeCharge, Kind: KindMetered,
		IdempotencyKey: "k", CausedBy: "usage_record:c/2026-07/sum", CreatedAt: clockNow}

	for name, mut := range map[string]func(BillingEvent) BillingEvent{
		"no customer":        func(e BillingEvent) BillingEvent { e.CustomerID = ""; return e },
		"no idempotency key": func(e BillingEvent) BillingEvent { e.IdempotencyKey = ""; return e },
		"unknown type":       func(e BillingEvent) BillingEvent { e.Type = "invoice_void"; return e },
		"unknown kind":       func(e BillingEvent) BillingEvent { e.Kind = "provider_tokens"; return e },
		"no cause":           func(e BillingEvent) BillingEvent { e.CausedBy = ""; return e },
		"credit with no reason": func(e BillingEvent) BillingEvent {
			e.Type, e.Reason = TypeCredit, ""
			return e
		},
	} {
		if _, err := l.Append(mut(base)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := len(testEvents(l, "c", "")); got != 0 {
		t.Errorf("a rejected row was stored anyway (%d rows)", got)
	}
}

// TestSettleIsCompletionNotRevision: the write-ahead row may be completed with the provider's receipt
// exactly once, and a second receipt may not overwrite the first.
func TestSettleIsCompletionNotRevision(t *testing.T) {
	l := NewMemLedger()
	ev, err := l.Append(BillingEvent{CustomerID: "c", Period: july.ID, Type: TypeCharge, Kind: KindMetered,
		IdempotencyKey: "k", CausedBy: "usage_record:c/2026-07/sum", Quantity: 3, CreatedAt: clockNow})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if ev.Status != StatusPending {
		t.Fatalf("a new charge must start pending, got %q", ev.Status)
	}

	done, err := l.Settle("k", "prov_ch_1", "prov_amt_1", clockNow)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if done.Status != StatusRecorded || done.SettledAt == nil {
		t.Fatalf("settle did not record: %+v", done)
	}
	// Nothing about the DECISION moved.
	if done.Quantity != 3 || done.CausedBy != ev.CausedBy || done.Kind != ev.Kind || done.EventID != ev.EventID {
		t.Errorf("settle revised the decision: %+v vs %+v", done, ev)
	}

	if _, err := l.Settle("k", "prov_ch_2", "prov_amt_2", clockNow); !errors.Is(err, ErrAlreadySettled) {
		t.Errorf("a second receipt was accepted, got %v", err)
	}
	again, _ := l.ByKey("k")
	if again.ProviderRef != "prov_ch_1" {
		t.Errorf("the second receipt overwrote the first: %q", again.ProviderRef)
	}
}

// TestIdempotencyKeyReuseAcrossOperationsIsRefused guards the bug class that a shared key namespace
// creates. A provider's idempotency keys are global per account, so presenting one key for two
// different operations makes the provider answer the second call with the FIRST operation's record.
// The symptom is not an error — it is a charge that silently never happened, with a usage ref standing
// in for a charge ref and a green test suite.
//
// The keys are namespaced by operation so this is unrepresentable in the shipped path; the provider
// refuses it outright so a future call site that hand-rolls a key fails loudly instead.
func TestIdempotencyKeyReuseAcrossOperationsIsRefused(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	acct, _ := h.accounts.Get("cus_acme")

	// The derived keys are distinct per operation — the structural half of the guarantee.
	report := UsageReportIdempotencyKey("cus_acme", july.ID, "sum")
	charge := MeteredChargeIdempotencyKey("cus_acme", july.ID, "sum")
	sub := SubscriptionChargeIdempotencyKey("cus_acme", july.ID, "team")
	gain := GainshareIdempotencyKey("cus_acme", july.ID)
	seen := map[string]bool{}
	for _, k := range []string{report, charge, sub, gain} {
		if seen[k] {
			t.Fatalf("two operations derive the same idempotency key: %q", k)
		}
		seen[k] = true
	}

	// And the provider refuses a hand-rolled collision rather than answering with the wrong record.
	shared := "hand_rolled_key"
	if _, err := h.provider.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: acct.ProviderCustomerHandle, Metric: "sum", Period: july.ID,
		Quantity: 1, IdempotencyKey: shared,
	}); err != nil {
		t.Fatalf("seed usage report: %v", err)
	}
	_, err := h.provider.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: acct.ProviderCustomerHandle, Kind: KindMetered, Period: july.ID,
		PriceRef: "price_ref_team_metered", Quantity: 1, IdempotencyKey: shared,
	})
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Errorf("a key reused across operations was accepted (err=%v) — the charge would silently never happen", err)
	}
	if h.provider.ChargeCount() != 0 {
		t.Errorf("the refused call recorded a charge anyway: %d", h.provider.ChargeCount())
	}
}

// recordingSecrets records every credential name asked for, so a test can assert WHICH credentials the
// billing capability touches — not merely that it works.
type recordingSecrets struct {
	inner providergateway.Secrets
	asked []string
}

func (r *recordingSecrets) Credential(ctx context.Context, name string) (providergateway.Credential, error) {
	r.asked = append(r.asked, name)
	return r.inner.Credential(ctx, name)
}
func (r *recordingSecrets) Describe() providergateway.SourceInfo { return r.inner.Describe() }

// TestBillingNeverTouchesCustomerProviderKeys is the other half of task 3.4 / FR13.
//
// The no-resale rule has two halves: no invoice line represents resold tokens (asserted above), and
// the provider spend was incurred on the CUSTOMER'S OWN keys. The second half is a statement about
// what the billing capability must NOT do — it must never reach for an LLM provider credential,
// because the only reason to hold one would be to run inference on the platform's account and bill it
// on. Asserting the absence is the only way to test a negative: this drives a whole billing period —
// subscribe, report usage, charge, invoice — and asserts the ONLY credentials ever requested were the
// two reserved billing names.
func TestBillingNeverTouchesCustomerProviderKeys(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()

	rec := &recordingSecrets{inner: providergateway.StaticSecrets{
		SecretBillingAPIKey:         {APIKey: "billing-api-key-DO-NOT-LEAK-test"},
		SecretBillingWebhookSigning: {APIKey: "webhook-signing-secret-DO-NOT-LEAK-test"},
		// Present but must never be asked for: if billing reached for these, it would be running
		// inference on the platform's account — the token-resale business this design refuses.
		"openai":    {APIKey: "openai-customer-key-DO-NOT-LEAK-test"},
		"anthropic": {APIKey: "anthropic-customer-key-DO-NOT-LEAK-test"},
		"bedrock":   {AWS: &providergateway.AWSCredential{AccessKeyID: "AKIA", SecretAccessKey: "s"}},
	}}
	secrets, err := NewManagedSecrets(rec)
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	svc, err := NewService(h.provider, h.ledger, h.accounts, h.plans, h.meter, secrets)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	svc.SetClock(func() time.Time { return clockNow })

	// A whole billing period, end to end.
	if _, err := svc.StartSubscription(ctx, "cus_acme"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := svc.ReportUsage(ctx, "cus_acme", july, metering.MetricSUM); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := secrets.APIKey(ctx); err != nil {
		t.Fatalf("api key: %v", err)
	}
	if _, err := secrets.WebhookSigningSecret(ctx); err != nil {
		t.Fatalf("webhook secret: %v", err)
	}
	inv, err := svc.Provider().Invoice(ctx, "cus_acme", july.ID)
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("invoice: %v", err)
	}

	allowed := map[string]bool{SecretBillingAPIKey: true, SecretBillingWebhookSigning: true}
	for _, name := range rec.asked {
		if !allowed[name] {
			t.Errorf("billing requested the %q credential — provider spend belongs on the CUSTOMER'S keys; "+
				"the platform never resells or marks up provider tokens", name)
		}
	}
	if len(rec.asked) == 0 {
		t.Error("no credential was requested at all — the assertion would be vacuously true")
	}
}
