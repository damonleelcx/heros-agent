// Command billing serves the P7 billing/usage surface against the REAL 7a+7b stack, with the ONLY stub
// being the billing provider (a Stripe-style stub in provider test mode, exactly as p55demo stubs the
// provider fan-out).
//
// Everything the UI shows is the shipped code path: SUM is derived from real P2.5 cost events through
// metering.DeriveSUM and upserted as a keyed usage record; the plan comes from a config store on disk
// (written to a temp dir, never git); entitlements come from the real entitlement gate; the invoice is
// assembled from the append-only billing ledger and the provider's own invoice; and gainshare is
// computed from a P5.5 verified-delta ledger that deliberately contains an ESTIMATE and an UN-MERGED
// proposal alongside the two verified, merged deltas — so "unverified bills nothing" can be SEEN, not
// just asserted in a test.
//
// Not a shipped service — a demo harness (task 7.3–7.5 / 9.7).
//
//	go run ./cmd/demo/billing   # then open the printed URL
//
// Flags let each first-class state be driven without editing code:
//
//	-over-limit     put the customer past their plan's SUM band (over-limit-with-upgrade-path)
//	-payment-failed deliver a payment_failed webhook (dunning)
//	-drift          drop a usage record on the provider side (reconciliation drift)
//	-plan           which named plan the customer is on (free|team|business|enterprise)
//	-no-consent     start with gainshare consent NOT given
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/telemetry"
	"github.com/heros-foreal/agentd/internal/verification"
)

const customerID = "cus_acme"

// demoCatalog is the fixture plan catalog. It is written to a TEMP DIR at startup and read back through
// the real FileSource — the config store, not git. Prices are opaque provider references; there is no
// amount in this file or anywhere else in the repository.
const demoCatalog = `{
  "version": "cfg-demo-1",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":10,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":120,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":5000,"seats":25,"retention_days":90,"eval_compute":1000},
     "price_refs":{"subscription":"price_ref_biz_sub","metered":"price_ref_biz_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

var (
	planID        = flag.String("plan", "enterprise", "named plan: free|team|business|enterprise")
	overLimit     = flag.Bool("over-limit", false, "push SUM past the plan's band so the over-limit denial renders")
	paymentFailed = flag.Bool("payment-failed", false, "deliver a payment_failed webhook so the dunning state renders")
	withDrift     = flag.Bool("drift", false, "drop a provider-side usage record so reconciliation drift renders")
	noConsent     = flag.Bool("no-consent", false, "start with gainshare consent NOT given")
	dark          = flag.Bool("dark", false, "leave the billing feature flag OFF (the default rollout state)")
	addr          = flag.String("addr", "127.0.0.1:8097", "listen address")
)

// The demo's periods. Three closed months so both charts have a trend to draw.
var periods = []metering.Period{
	metering.MonthPeriod(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)),
	metering.MonthPeriod(time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)),
	metering.MonthPeriod(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)),
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	st, err := build()
	if err != nil {
		log.Fatalf("p7demo: %v", err)
	}

	srv := api.New(nil, config.Config{})
	srv.MountP7(st)
	// The rollout state is a HEALTH SIGNAL on /readyz, not only a log line: "is this box charging real
	// money" has to be checkable now, from the box.
	srv.SetBillingRollout(st.rollout)
	log.Printf("P7 billing surface: http://%s/billing?customer=%s  (plan=%s over-limit=%v payment-failed=%v drift=%v)",
		*addr, customerID, *planID, *overLimit, *paymentFailed, *withDrift)
	log.Printf("rollout: %s   (readiness: http://%s/readyz)", st.rollout, *addr)
	for _, a := range st.alerts.Alerts() {
		log.Printf("ALERT %s customer=%s period=%s qty=%v — %s", a.Kind, a.CustomerID, a.Period, a.Quantity, a.Detail)
	}
	if err := http.ListenAndServe(*addr, srv.Handler); err != nil {
		log.Fatal(err)
	}
}

// state is the whole wired stack plus the api.P7Source implementation.
type state struct {
	mu       sync.Mutex
	plans    *plancfg.Resolver
	accounts *account.MemStore
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	gate     *entitlement.Gate
	svc      *billing.Service
	provider *billing.StubProvider
	deltas   *metering.MemVerifiedDeltas
	savings  *metering.MemSavingsStore
	alerts   *metering.MemAlerter
	rollout  *billing.Rollout
	observer *metering.Observer
}

func build() (*state, error) {
	ctx := context.Background()
	now := func() time.Time { return periods[len(periods)-1].End.Add(-time.Hour) }

	// ── plan config, from a config store on disk (never git) ──────────────────
	dir, err := os.MkdirTemp("", "p7demo-config-*")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "plans.json")
	if err := os.WriteFile(path, []byte(demoCatalog), 0o600); err != nil {
		return nil, err
	}
	log.Printf("plan configuration published to %s (config store, not git)", path)
	plans := plancfg.NewResolver(plancfg.NewFileSource(path), plancfg.NewMemAudit())
	plans.SetClock(now)
	if _, err := plans.Reload("p7demo"); err != nil {
		return nil, err
	}

	// ── provider (the ONLY stub) + account ────────────────────────────────────
	provider := billing.NewStubProvider()
	handle, err := provider.EnsureCustomer(ctx, customerID)
	if err != nil {
		return nil, err
	}
	accts := account.NewMemStore()
	if _, err := accts.Create(account.Account{
		CustomerID: customerID, ProviderCustomerHandle: handle,
		ActivePlanID: plancfg.NormalizePlanID(*planID), PlanConfigVersion: plans.Version(),
		CreatedAt: periods[0].Start,
	}); err != nil {
		return nil, err
	}
	if !*noConsent {
		if _, err := accts.SetGainshareConsent(customerID, true, periods[0].Start); err != nil {
			return nil, err
		}
	}

	// ── the P2.5 cost-event substrate: REAL cost events, SUM derived from them ─
	events := metering.NewMemCostEvents()
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(events, usage)
	meter.SetClock(now)

	// Per-period spend, shaped so the trend tells the optimization story: spend rises, then the two
	// merged optimizations pull it down.
	spend := map[string][]float64{
		periods[0].ID: {12.5, 18.25, 9.75, 21.0, 14.5},
		periods[1].ID: {22.0, 19.5, 26.25, 17.75, 20.5},
		periods[2].ID: {14.0, 11.25, 13.5, 9.0, 12.25},
	}
	if *overLimit {
		// Push the last period well past the Team band so the denial has real magnitude behind it.
		spend[periods[2].ID] = append(spend[periods[2].ID], 400.0)
	}
	for _, p := range periods {
		runID := "run-" + p.ID
		events.Attribute(runID, customerID)
		for i, usd := range spend[p.ID] {
			events.Put(costEvent(runID, fmt.Sprintf("%s|router|%d", runID, i), usd,
				p.Start.Add(time.Duration(i+1)*24*time.Hour)))
		}
		if _, _, err := meter.RecordSUM(customerID, p); err != nil {
			return nil, err
		}
		for _, m := range []struct {
			metric metering.Metric
			qty    float64
		}{{metering.MetricSeats, 4}, {metering.MetricRetention, 30}, {metering.MetricEvalCompute, 128}} {
			if _, _, err := meter.RecordUsage(customerID, p, m.metric, m.qty, "demo-"+string(m.metric)+"-"+p.ID); err != nil {
				return nil, err
			}
		}
	}

	// ── billing service ───────────────────────────────────────────────────────
	secrets, err := billing.NewManagedSecrets(providergateway.StaticSecrets{
		billing.SecretBillingAPIKey:         {APIKey: "billing-api-key-DO-NOT-LEAK-demo"},
		billing.SecretBillingWebhookSigning: {APIKey: "webhook-signing-secret-DO-NOT-LEAK-demo"},
	})
	if err != nil {
		return nil, err
	}
	svc, err := billing.NewService(provider, billing.NewMemLedger(), accts, plans, meter, secrets)
	if err != nil {
		return nil, err
	}
	svc.SetClock(now)
	svc.WithDeliveries(billing.NewMemDeliveries())

	// The rollout starts DARK, exactly as a deployment's does. The demo then opens the gates in wave
	// order — 7a in provider TEST mode, then 7b — so the ordering is exercised rather than asserted.
	rollout := billing.NewRollout()
	if !*dark {
		if err := rollout.Enable(billing.ModeTest); err != nil {
			return nil, err
		}
		if err := rollout.EnableGainshare(); err != nil {
			return nil, err
		}
		rollout.EnableAutoMergeEntitlement()
	}
	alerts := &metering.MemAlerter{}
	observer := metering.NewObserver(billing.LogSink{}, alerts)
	observer.SetClock(now)
	svc.WithRollout(rollout).WithObserver(observer)

	plan, err := plans.ResolvePlan(plancfg.NormalizePlanID(*planID))
	if err != nil {
		return nil, err
	}
	if plan.PriceRefs["subscription"] != "" {
		if _, err := svc.StartSubscription(ctx, customerID); err != nil {
			return nil, err
		}
	}
	// Report + charge every period, so the invoice has real subscription and metered lines.
	for _, p := range periods {
		for _, m := range metering.Metrics {
			if _, err := svc.ReportUsage(ctx, customerID, p, m); err != nil {
				log.Printf("report %s/%s: %v", p.ID, m, err)
			}
		}
		if plan.PriceRefs["subscription"] != "" {
			if _, err := svc.Charge(ctx, customerID, p, billing.KindSubscription,
				billing.SubscriptionChargeIdempotencyKey(customerID, p.ID, plan.PlanID)); err != nil {
				log.Printf("subscription charge %s: %v", p.ID, err)
			}
		}
		if plan.PriceRefs["metered"] != "" {
			if _, err := svc.Charge(ctx, customerID, p, billing.KindMetered,
				billing.MeteredChargeIdempotencyKey(customerID, p.ID, string(metering.MetricSUM))); err != nil {
				log.Printf("metered charge %s: %v", p.ID, err)
			}
		}
	}

	// ── the P5.5 verified-delta ledger ────────────────────────────────────────
	//
	// Deliberately mixed: two VERIFIED, MERGED deltas (billable) plus an ESTIMATE and an UN-MERGED
	// proposal worth far more (never billable). The whole point of the surface is that the second pair
	// is visible and marked NOT BILLED.
	deltas := metering.NewMemVerifiedDeltas()
	last := periods[len(periods)-1]
	deltas.Put(verifiedDelta("vd-router-model", last.ID, 62.0, 41.0, true, false, "a1b2c3d4e5f6"))
	deltas.Put(verifiedDelta("vd-rag-rerank", last.ID, 38.0, 29.0, true, false, "f6e5d4c3b2a1"))
	deltas.Put(verifiedDelta("vd-cache-estimate", last.ID, 300.0, 60.0, false, true, ""))   // ESTIMATE
	deltas.Put(verifiedDelta("vd-prompt-unmerged", last.ID, 180.0, 40.0, false, false, "")) // verified, NOT merged
	savings := metering.NewMemSavingsStore()

	acct, _ := accts.Get(customerID)
	if acct.GainshareConsent && plan.PriceRefs["gainshare"] != "" {
		if _, err := svc.ChargeGainshare(ctx, customerID, last, deltas, savings); err != nil {
			log.Printf("gainshare: %v", err)
		}
	}

	// ── the optional first-class states ───────────────────────────────────────
	if *paymentFailed {
		body, _ := json.Marshal(billing.WebhookPayload{
			ProviderEventID: "evt_demo_failed", Type: billing.WebhookInvoicePaymentFailed,
			CustomerID: customerID, Period: last.ID, InvoiceRef: "prov_inv_demo",
		})
		stamp := now().UTC().Format(time.RFC3339)
		if _, err := svc.HandleWebhook(ctx, billing.SignedWebhook{
			Body: body, Timestamp: stamp, Signature: billing.SignWebhook("webhook-signing-secret-DO-NOT-LEAK-demo", stamp, body),
		}); err != nil {
			log.Printf("webhook: %v", err)
		}
	}
	if *withDrift {
		provider.DropUsage(customerID, last.ID, string(metering.MetricSUM))
	}

	gate := entitlement.NewGate(accts, plans, usage)
	gate.SetClock(now)

	return &state{plans: plans, accounts: accts, usage: usage, meter: meter, gate: gate,
		svc: svc, provider: provider, deltas: deltas, savings: savings, alerts: alerts,
		rollout: rollout, observer: observer}, nil
}

// costEvent builds a real P2.5 cost event — the same shape telemetry.MetricSet emits.
func costEvent(runID, invocationID string, usd float64, ts time.Time) metricevent.Event {
	seed := int64(1)
	v := usd
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     "var-demo", RunID: runID, NodeID: "router", CaseID: "case-1", Seed: &seed,
		Timestamp:  ts.UTC().Format(time.RFC3339Nano),
		ConfigHash: "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0",
		MetricName: telemetry.MetricCostUSD, Value: &v, Unit: telemetry.UnitUSD,
		Dimensions: map[string]any{telemetry.AttrInvocationID: invocationID},
	}
}

func verifiedDelta(ref, period string, baseline, optimized float64, merged, estimated bool, commit string) metering.VerifiedDelta {
	return metering.VerifiedDelta{
		Ref: ref, ProposalID: "prop-" + ref, CustomerID: customerID, Period: period,
		Verdict: verification.Verdict{
			GateResult: verification.GatePass, HeldOut: true, Significant: true, RegressionPass: true,
			Delta: evalstats.Interval{Mean: 0.34, Low: 0.21, High: 0.47},
		},
		Merged: merged, MergeCommit: commit, Estimated: estimated,
		BaselineSUM: baseline, OptimizedSUM: optimized,
		Baseline: metering.BaselineMethod{
			ID: "holdout-v1", EvalSetHash: "es_9f3a21c4",
			HoldoutCaseIDs:     []string{"c4", "c5", "c6", "c7"},
			GeneratingCaseIDs:  []string{"c1", "c2", "c3"},
			Seeds:              []int64{1, 2, 3, 4, 5},
			BaselineConfigHash: "cfg_baseline_" + ref, CandidateConfigHash: "cfg_candidate_" + ref,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// api.P7Source
// ─────────────────────────────────────────────────────────────────────────────

func (s *state) Periods(string) []string {
	out := make([]string, 0, len(periods))
	for i := len(periods) - 1; i >= 0; i-- {
		out = append(out, periods[i].ID)
	}
	return out
}

func (s *state) SetGainshareConsent(customer string, consented bool) (api.BillingView, error) {
	s.mu.Lock()
	if _, err := s.accounts.SetGainshareConsent(customer, consented, periods[0].Start); err != nil {
		s.mu.Unlock()
		return api.BillingView{}, err
	}
	s.mu.Unlock()
	v, _ := s.Billing(customer, "")
	return v, nil
}

func (s *state) Billing(customer, periodID string) (api.BillingView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()

	p := periods[len(periods)-1]
	if periodID != "" {
		for _, cand := range periods {
			if cand.ID == periodID {
				p = cand
			}
		}
	}
	acct, err := s.accounts.Get(customer)
	if err != nil {
		return api.BillingView{}, false
	}
	plan, err := s.plans.ResolvePlan(acct.ActivePlanID)
	if err != nil {
		return api.BillingView{}, false
	}

	v := api.BillingView{
		CustomerID: customer, Period: p.ID, AvailablePeriods: s.Periods(customer),
		PlanID: plan.PlanID, PlanName: plan.DisplayName, PlanConfigVersion: plan.Version,
		SUMUnit: telemetry.UnitUSD,
	}

	// ── 1. SUM + meters ───────────────────────────────────────────────────────
	recs, _ := s.usage.Period(customer, p.ID)
	v.Empty = len(recs) == 0
	for _, rec := range recs {
		if rec.Metric == metering.MetricSUM {
			v.SUM = rec.Quantity
		}
		lim, metric := limitFor(rec.Metric)
		allowed, set := plan.Limit(lim)
		v.Meters = append(v.Meters, api.MeterView{
			Metric: string(rec.Metric), Label: meterLabel(rec.Metric), Value: rec.Quantity,
			Unit: meterUnit(rec.Metric), Allowed: allowed, Unlimited: !set,
			Over:               set && rec.Quantity > allowed,
			ReportedToProvider: rec.ReportedToProvider, ProviderUsageRef: rec.ProviderUsageRef,
		})
		_ = metric
	}

	// ── trend ─────────────────────────────────────────────────────────────────
	for _, tp := range periods {
		pt := api.PeriodPoint{Period: tp.ID}
		if rec, err := s.usage.Get(metering.Key{CustomerID: customer, Period: tp.ID, Metric: metering.MetricSUM}); err == nil {
			pt.SUM = rec.Quantity
		}
		if bs, err := metering.ComputeBillableSavings(s.deltas, customer, tp); err == nil {
			pt.Baseline, pt.Optimized = bs.BaselineSUM, bs.OptimizedSUM
		}
		v.SUMTrend = append(v.SUMTrend, pt)
	}

	// ── 2. entitlements (the real gate) ───────────────────────────────────────
	for _, surface := range entitlement.Surfaces {
		d, err := surface.Check(s.gate, customer)
		if err != nil {
			continue
		}
		row := api.EntitlementRow{Feature: string(surface.Feature), Label: surfaceLabel(surface.Feature), Included: d.Allowed}
		if !d.Allowed {
			row.Reason, row.UpgradePlan, row.UpgradePlanName = d.Reason, d.UpgradePlan, d.UpgradePlanName
		}
		v.Entitlements = append(v.Entitlements, row)
	}

	// The denial banner shows the FIRST surface the customer cannot reach — the paywall, in context.
	// Over-limit outranks not-entitled, because it is the one the customer just hit.
	var denial *entitlement.Decision
	for _, surface := range entitlement.Surfaces {
		d, err := surface.Check(s.gate, customer)
		if err != nil || d.Allowed {
			continue
		}
		if denial == nil || (d.ReasonCode == entitlement.ReasonOverLimit && denial.ReasonCode != entitlement.ReasonOverLimit) {
			cp := d
			denial = &cp
		}
	}
	if denial != nil {
		dv := api.DenialView{
			Feature: string(denial.Feature), FeatureLabel: surfaceLabel(denial.Feature),
			Reason: denial.Reason, ReasonCode: string(denial.ReasonCode),
			UpgradePlan: denial.UpgradePlan, UpgradePlanName: denial.UpgradePlanName,
		}
		if denial.Limit != nil {
			dv.Limit = &api.DenialLimitView{
				Limit: string(denial.Limit.Limit), Label: limitLabel(denial.Limit.Limit),
				Allowed: denial.Limit.Allowed, Observed: denial.Limit.Observed, Period: denial.Limit.Period,
			}
		}
		v.Denial = &dv
	}

	// ── 3. invoice + provider state ───────────────────────────────────────────
	v.Invoice = s.invoiceView(ctx, customer, p)
	st := s.svc.BillingState(customer)
	v.State = api.BillingStateView{
		InvoiceStatus: st.InvoiceStatus, SubscriptionStatus: st.SubscriptionStatus,
		PaymentFailed: st.PaymentFailed, PastDue: st.PastDue,
	}
	if st.PaymentFailed || st.PastDue {
		v.State.Guidance = "Your billing provider could not take payment. Update the payment method with " +
			"the provider to clear this; the platform does not hold your card details."
	}

	// ── drift (reconciliation, read-only) ─────────────────────────────────────
	if res, err := s.svc.Reconcile(ctx, customer, p, s.observer); err == nil {
		for _, d := range res.Drift {
			v.Drift = append(v.Drift, api.DriftView{
				Kind: string(d.Kind), Metric: string(d.Metric), Detail: d.Detail,
				PlatformQuantity: d.PlatformQuantity, ProviderQuantity: d.ProviderQuantity,
			})
		}
	}

	// ── 4. savings ────────────────────────────────────────────────────────────
	v.Savings = s.savingsView(customer, p, acct.GainshareConsent, acct.ConsentedAt, plan.PriceRefs["gainshare"] != "")
	return v, true
}

func (s *state) invoiceView(ctx context.Context, customer string, p metering.Period) api.InvoiceView {
	out := api.InvoiceView{}
	inv, err := s.provider.Invoice(ctx, customer, p.ID)
	if err == nil {
		if verr := inv.Validate(); verr != nil {
			// A provider invoice that fails the no-resale check is a defect, surfaced rather than
			// rendered as if it were understood.
			log.Printf("invoice validation: %v", verr)
			return out
		}
		out.InvoiceRef, out.Status = inv.InvoiceRef, inv.Status
	}

	byKind := map[string]*api.TotalView{}
	for _, ev := range s.svc.Ledger().Events(customer, p.ID) {
		if !ev.Type.ChargeBearing() {
			continue
		}
		kind := string(ev.Kind)
		if kind == "" {
			kind = string(ev.Type) // credit / refund carry no charge kind
		}
		line := api.LineView{
			Kind: kind, Label: lineLabel(ev), Basis: ev.CausedBy,
			AmountRef: ev.AmountRef, Quantity: ev.Quantity, Unit: telemetry.UnitUSD,
			ChargeRef: ev.ProviderRef,
		}
		if ev.Type == billing.TypeCredit || ev.Type == billing.TypeRefund {
			line.Corrects = ev.CausedBy
		}
		if ev.Type == billing.TypeGainshareCharge {
			line.Evidence = s.evidenceView(ev)
		}
		out.Lines = append(out.Lines, line)

		t := byKind[kind]
		if t == nil {
			t = &api.TotalView{Kind: kind, Label: strings.Title(kind) + " subtotal"} //nolint:staticcheck // ASCII kind names
			byKind[kind] = t
		}
		t.Lines++
		t.Quantity += ev.Quantity
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		out.Totals = append(out.Totals, *byKind[k])
	}
	return out
}

// evidenceView resolves a gainshare charge's evidence back to the P5.5 ledger — the auditability path,
// rendered. A gainshare line whose evidence does not resolve shows nothing rather than a claim.
func (s *state) evidenceView(ev billing.BillingEvent) []api.EvidenceView {
	deltas, merges, err := billing.GainshareEvidence(ev, s.deltas)
	if err != nil {
		log.Printf("gainshare evidence: %v", err)
		return nil
	}
	var out []api.EvidenceView
	for _, d := range deltas {
		out = append(out, api.EvidenceView{
			Kind: "verified_delta", Ref: d.Ref,
			Label: fmt.Sprintf("verified delta %s (held-out, significant)", d.Ref),
			Link:  "/recommendations#" + d.ProposalID,
			Method: &api.MethodView{
				ID: d.Baseline.ID, EvalSetHash: d.Baseline.EvalSetHash,
				HoldoutCases: len(d.Baseline.HoldoutCaseIDs), GeneratingCases: len(d.Baseline.GeneratingCaseIDs),
				Seeds: len(d.Baseline.Seeds), BaselineConfig: d.Baseline.BaselineConfigHash,
				CandidateConfig:  d.Baseline.CandidateConfigHash,
				SignificanceNote: fmt.Sprintf("delta %.3f [%.3f, %.3f]", d.Verdict.Delta.Mean, d.Verdict.Delta.Low, d.Verdict.Delta.High),
			},
		})
	}
	for _, m := range merges {
		out = append(out, api.EvidenceView{Kind: "merge", Ref: m, Label: "merged as " + m,
			Link: "/optimizer#" + m})
	}
	return out
}

func (s *state) savingsView(customer string, p metering.Period, consent bool, at *time.Time, priced bool) api.SavingsView {
	out := api.SavingsView{Consented: consent, ConsentAvailable: priced, Unit: telemetry.UnitUSD}
	if at != nil {
		out.ConsentedAt = at.Format("2006-01-02")
	}
	bs, err := metering.ComputeBillableSavings(s.deltas, customer, p)
	if err != nil {
		return out
	}
	out.BaselineSUM, out.OptimizedSUM, out.Verified = bs.BaselineSUM, bs.OptimizedSUM, bs.Savings
	out.NoneVerified = bs.Savings <= 0

	for _, ref := range bs.VerifiedDeltaRefs {
		d, ok := s.deltas.ByRef(ref)
		if !ok {
			continue
		}
		out.Billed = append(out.Billed, api.SavingRow{
			Ref: d.Ref, BaselineSUM: d.BaselineSUM, OptimizedSUM: d.OptimizedSUM, Savings: d.Savings(),
			MergeCommit: d.MergeCommit,
			Method: &api.MethodView{
				ID: d.Baseline.ID, EvalSetHash: d.Baseline.EvalSetHash,
				HoldoutCases: len(d.Baseline.HoldoutCaseIDs), GeneratingCases: len(d.Baseline.GeneratingCaseIDs),
				Seeds: len(d.Baseline.Seeds),
			},
		})
	}
	for _, e := range bs.Excluded {
		out.Excluded = append(out.Excluded, api.ExcludedRow{Ref: e.Ref, Reason: e.Reason, WouldHaveBeen: e.WouldHaveBeen})
	}
	return out
}

// ── labels ───────────────────────────────────────────────────────────────────

func limitFor(m metering.Metric) (plancfg.Limit, metering.Metric) {
	switch m {
	case metering.MetricSUM:
		return plancfg.LimitSUMBand, m
	case metering.MetricSeats:
		return plancfg.LimitSeats, m
	case metering.MetricRetention:
		return plancfg.LimitRetentionDays, m
	default:
		return plancfg.LimitEvalCompute, m
	}
}

func meterLabel(m metering.Metric) string {
	switch m {
	case metering.MetricSUM:
		return "Spend under management"
	case metering.MetricSeats:
		return "Dashboard seats"
	case metering.MetricRetention:
		return "Trace & metric retention"
	default:
		return "Cloud eval compute"
	}
}

func meterUnit(m metering.Metric) string {
	switch m {
	case metering.MetricSUM:
		return telemetry.UnitUSD
	case metering.MetricRetention:
		return "days"
	default:
		return ""
	}
}

func limitLabel(l plancfg.Limit) string {
	switch l {
	case plancfg.LimitSUMBand:
		return "Spend under management"
	case plancfg.LimitSeats:
		return "Seats"
	case plancfg.LimitRetentionDays:
		return "Retention"
	default:
		return "Eval compute"
	}
}

func surfaceLabel(f plancfg.Feature) string {
	switch f {
	case plancfg.FeatureCLI:
		return "Command-line client"
	case plancfg.FeatureDiscovery:
		return "Repository discovery"
	case plancfg.FeatureAssistedPR:
		return "Assisted verified pull requests"
	case plancfg.FeatureDashboard:
		return "Web dashboard"
	default:
		return "Autonomous auto-merge"
	}
}

func lineLabel(ev billing.BillingEvent) string {
	switch ev.Type {
	case billing.TypeGainshareCharge:
		return "Verified savings (gainshare)"
	case billing.TypeCredit:
		return "Credit — " + ev.Reason
	case billing.TypeRefund:
		return "Refund — " + ev.Reason
	}
	switch ev.Kind {
	case billing.KindSubscription:
		return "Plan subscription"
	case billing.KindMetered:
		return "Metered spend under management"
	}
	return string(ev.Type)
}
