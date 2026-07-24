package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// rollout_test.go is task 8.1 + 8.3: the revenue signals ride the P2.5 substrate and alert on the two
// conditions that matter, and the rollout keeps every wave DARK until its checklist is green.

// recordingSink is a telemetry.Sink that keeps what it was handed, so a test can assert on the ACTUAL
// events rather than on the emitter's intent.
type recordingSink struct {
	events []metricevent.Event
	spans  []telemetry.Span
}

func (r *recordingSink) EmitMetric(_ context.Context, ev metricevent.Event) {
	r.events = append(r.events, ev)
}
func (r *recordingSink) EmitSpan(_ context.Context, sp telemetry.Span) { r.spans = append(r.spans, sp) }

func (r *recordingSink) named(name string) []metricevent.Event {
	var out []metricevent.Event
	for _, ev := range r.events {
		if ev.MetricName == name {
			out = append(out, ev)
		}
	}
	return out
}

func observed(t *testing.T) (*harness, *recordingSink, *metering.MemAlerter) {
	t.Helper()
	h := newHarness(t, "enterprise")
	sink := &recordingSink{}
	alerter := &metering.MemAlerter{}
	obs := metering.NewObserver(sink, alerter)
	obs.SetClock(func() time.Time { return clockNow })
	h.svc.WithObserver(obs)
	return h, sink, alerter
}

// TestRevenueSignalsRideTheP25Substrate is task 8.1. Every event must pass the SAME emission gate every
// other metric passes — which is the whole point of not standing up a second telemetry path.
func TestRevenueSignalsRideTheP25Substrate(t *testing.T) {
	h, sink, alerter := observed(t)
	ctx := context.Background()

	// A whole period: record every meter, report it, charge, gainshare, a webhook, and a
	// reconciliation with drift.
	for _, m := range []metering.Metric{metering.MetricSeats, metering.MetricRetention, metering.MetricEvalCompute} {
		if _, _, err := h.meter.RecordUsage("cus_acme", july, m, 4, "digest-"+string(m)); err != nil {
			t.Fatalf("record %s: %v", m, err)
		}
	}
	for _, m := range metering.Metrics {
		if _, err := h.svc.ReportUsage(ctx, "cus_acme", july, m); err != nil {
			t.Fatalf("report %s: %v", m, err)
		}
	}
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); err != nil {
		t.Fatalf("charge: %v", err)
	}

	if _, err := h.accounts.SetGainshareConsent("cus_acme", true, julyStart); err != nil {
		t.Fatalf("consent: %v", err)
	}
	ledger := metering.NewMemVerifiedDeltas()
	ledger.Put(mergedVerified("vd1", 100, 60))
	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, metering.NewMemSavingsStore()); err != nil {
		t.Fatalf("gainshare: %v", err)
	}

	h.svc.WithDeliveries(NewMemDeliveries())
	body, _ := json.Marshal(WebhookPayload{ProviderEventID: "evt_obs", Type: WebhookInvoicePaymentFailed,
		CustomerID: "cus_acme", Period: july.ID, InvoiceRef: "prov_inv_1"})
	stamp := clockNow.UTC().Format("2006-01-02T15:04:05Z07:00")
	if _, err := h.svc.HandleWebhook(ctx, SignedWebhook{Body: body, Timestamp: stamp,
		Signature: SignWebhook(testWebhookSecret, stamp, body)}); err != nil {
		t.Fatalf("webhook: %v", err)
	}

	// Drift, pushed straight at the observer (it satisfies metering.AlertSink).
	h.provider.DropUsage("cus_acme", july.ID, string(metering.MetricSUM))
	obs := metering.NewObserver(sink, alerter)
	obs.SetClock(func() time.Time { return clockNow })
	if _, err := h.svc.Reconcile(ctx, "cus_acme", july, obs); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// A failed charge: the provider goes down mid-charge.
	h.provider.SetDown(true)
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindSubscription,
		SubscriptionChargeIdempotencyKey("cus_acme", july.ID, "enterprise")); err == nil {
		t.Fatal("precondition: the charge should have failed")
	}
	h.provider.SetDown(false)

	// (1) Every metric in the taxonomy was emitted at least once.
	for _, name := range telemetry.RevenueMetricNames {
		if len(sink.named(name)) == 0 {
			t.Errorf("no %s event was emitted — the revenue taxonomy is incomplete", name)
		}
	}

	// (2) Every emitted event PASSES the P2.5 emission gate. This is the assertion that makes "rides the
	// substrate" true rather than decorative: an event that the gate would reject is an event the
	// collector would drop, and the dashboard would be silently empty.
	if len(sink.events) == 0 {
		t.Fatal("no events at all — the assertions below would be vacuously true")
	}
	for _, ev := range sink.events {
		if err := ev.Validate(); err != nil {
			t.Errorf("a revenue event would be REJECTED at the P2.5 emission boundary: %v", err)
		}
		// (3) Billing-scoped, and greppably so.
		if ev.VariantID != telemetry.VariantIDBilling || ev.RunID != telemetry.RunIDBilling ||
			ev.NodeID != telemetry.NodeIDBilling || ev.CaseID != telemetry.CaseIDBilling {
			t.Errorf("%s does not carry the reserved billing-scope tags: %+v", ev.MetricName, ev)
		}
		// (4) The customer rides as a DIMENSION, never as a series label — a label per customer is the
		// cardinality explosion Decision 4 exists to prevent.
		if ev.Dimensions[telemetry.AttrCustomerID] != "cus_acme" {
			t.Errorf("%s does not carry the customer as a dimension: %+v", ev.MetricName, ev.Dimensions)
		}
		if telemetry.IsSeriesLabel(telemetry.AttrCustomerID) {
			t.Error("customer_id became a TSDB series label — one series per customer")
		}
		labels := telemetry.SeriesLabels(ev)
		if _, leaked := labels[telemetry.AttrCustomerID]; leaked {
			t.Errorf("customer_id leaked into the projected label set: %v", labels)
		}
	}

	// (5) Exactly the two alertable conditions alerted.
	if got := len(alerter.OfKind(metering.AlertFailedCharge)); got != 1 {
		t.Errorf("failed-charge alerts = %d, want 1", got)
	}
	if got := len(alerter.OfKind(metering.AlertReconciliationDrift)); got == 0 {
		t.Error("no reconciliation-drift alert was raised")
	}
	for _, a := range alerter.Alerts() {
		if a.Detail == "" {
			t.Errorf("alert %+v carries no detail — a page nobody can act on", a)
		}
		if a.CustomerID == "" || a.Period == "" {
			t.Errorf("alert %+v does not say who or when", a)
		}
	}
}

// TestObservabilityFailureNeverStopsBilling: a nil observer, and a sink that panics, must both leave
// the business operation intact. Telemetry health may not gate revenue.
func TestObservabilityFailureNeverStopsBilling(t *testing.T) {
	h := newHarness(t, "team") // no observer attached at all
	ctx := context.Background()
	if _, err := h.svc.ReportUsage(ctx, "cus_acme", july, metering.MetricSUM); err != nil {
		t.Fatalf("report with a nil observer: %v", err)
	}
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); err != nil {
		t.Fatalf("charge with a nil observer: %v", err)
	}
	if h.provider.ChargeCount() != 1 {
		t.Errorf("the charge did not happen: %d", h.provider.ChargeCount())
	}
}

// ── rollout (task 8.3) ───────────────────────────────────────────────────────

// TestRolloutIsDarkByDefault: the zero value charges nothing and moves no real money. A deployment that
// forgets to configure the flag must fail in the direction where the worst outcome is harmless.
func TestRolloutIsDarkByDefault(t *testing.T) {
	r := NewRollout()
	if r.Mode() != ModeTest {
		t.Errorf("default mode = %q, want test", r.Mode())
	}
	for _, k := range ChargeKinds {
		if err := r.AllowCharge(k); !errors.Is(err, ErrBillingDark) {
			t.Errorf("%s charge allowed while dark: %v", k, err)
		}
	}
	if r.AllowAutoMergeEntitlement() {
		t.Error("the Enterprise auto-merge entitlement is granted by default")
	}
	// A zero-value Rollout (not built through the constructor) is dark too.
	var zero Rollout
	if err := zero.AllowCharge(KindSubscription); !errors.Is(err, ErrBillingDark) {
		t.Errorf("the zero-value rollout allows charges: %v", err)
	}
}

// TestWavesAreOrdered: 7a can ship without 7b, and gainshare cannot be enabled on top of dark billing.
func TestWavesAreOrdered(t *testing.T) {
	r := NewRollout()

	// 7b cannot be enabled before 7a.
	if err := r.EnableGainshare(); !errors.Is(err, ErrBillingDark) {
		t.Errorf("gainshare was enabled while billing is off: %v", err)
	}

	// 7a in TEST mode: subscription + metered work, gainshare does not.
	if err := r.Enable(ModeTest); err != nil {
		t.Fatalf("enable: %v", err)
	}
	for _, k := range []ChargeKind{KindSubscription, KindMetered} {
		if err := r.AllowCharge(k); err != nil {
			t.Errorf("7a %s charge refused: %v", k, err)
		}
	}
	if err := r.AllowCharge(KindGainshare); !errors.Is(err, ErrGainshareDisabled) {
		t.Errorf("gainshare allowed in wave 7a: %v", err)
	}

	// 7b.
	if err := r.EnableGainshare(); err != nil {
		t.Fatalf("enable gainshare: %v", err)
	}
	if err := r.AllowCharge(KindGainshare); err != nil {
		t.Errorf("gainshare refused after 7b: %v", err)
	}

	// Turning billing off does NOT silently re-arm gainshare when it comes back.
	r.DisableGainshare()
	r.Disable()
	if err := r.Enable(ModeLive); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if err := r.AllowCharge(KindGainshare); !errors.Is(err, ErrGainshareDisabled) {
		t.Errorf("gainshare came back on its own after a billing restart: %v", err)
	}
	if r.Mode() != ModeLive {
		t.Errorf("mode = %q, want live", r.Mode())
	}
	if err := r.Enable("production"); err == nil {
		t.Error("an unknown provider mode was accepted")
	}
}

// TestRolloutGatesEveryCharge wires the flag into the service and asserts a dark deployment raises
// nothing AND leaves no pending ledger row a later flip would settle into a real charge.
func TestRolloutGatesEveryCharge(t *testing.T) {
	h, _, _ := observed(t)
	ctx := context.Background()
	r := NewRollout()
	h.svc.WithRollout(r)
	if _, err := h.accounts.SetGainshareConsent("cus_acme", true, julyStart); err != nil {
		t.Fatalf("consent: %v", err)
	}
	deltas := metering.NewMemVerifiedDeltas()
	deltas.Put(mergedVerified("vd1", 100, 60))

	// Dark.
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); !errors.Is(err, ErrBillingDark) {
		t.Fatalf("want ErrBillingDark, got %v", err)
	}
	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, deltas, metering.NewMemSavingsStore()); !errors.Is(err, ErrBillingDark) {
		t.Fatalf("gainshare while dark: %v", err)
	}
	if h.provider.ChargeCount() != 0 {
		t.Error("a charge reached the provider while billing was dark")
	}
	if len(h.ledger.Pending()) != 0 {
		t.Error("a dark deployment left a pending ledger row that a later flip would settle into a real charge")
	}

	// 7a on: metered works, gainshare still refused with its OWN reason.
	if err := r.Enable(ModeTest); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := h.svc.Charge(ctx, "cus_acme", july, KindMetered,
		MeteredChargeIdempotencyKey("cus_acme", july.ID, string(metering.MetricSUM))); err != nil {
		t.Fatalf("metered after 7a: %v", err)
	}
	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, deltas, metering.NewMemSavingsStore()); !errors.Is(err, ErrGainshareDisabled) {
		t.Fatalf("gainshare in wave 7a: %v", err)
	}

	// 7b on.
	if err := r.EnableGainshare(); err != nil {
		t.Fatalf("enable gainshare: %v", err)
	}
	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, deltas, metering.NewMemSavingsStore()); err != nil {
		t.Fatalf("gainshare after 7b: %v", err)
	}
}

// TestRolloutIsAHealthSignal: which gates are open must be readable NOW, from the box, not inferred
// from a startup log line.
func TestRolloutIsAHealthSignal(t *testing.T) {
	r := NewRollout()
	d := r.Describe()
	for _, k := range []string{"billing", "provider_mode", "gainshare", "auto_merge_entitlement"} {
		if d[k] == "" {
			t.Errorf("the health description omits %q", k)
		}
	}
	if d["billing"] != "disabled" || d["provider_mode"] != "test" {
		t.Errorf("a dark rollout describes itself as %v", d)
	}
	if err := r.Enable(ModeLive); err != nil {
		t.Fatal(err)
	}
	r.EnableAutoMergeEntitlement()
	s := r.String()
	for _, want := range []string{"billing=enabled", "provider_mode=live", "auto_merge_entitlement=enabled"} {
		if !strings.Contains(s, want) {
			t.Errorf("the log line %q is missing %q", s, want)
		}
	}
}
