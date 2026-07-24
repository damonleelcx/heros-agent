// Package p7e2e is the P7 end-to-end proof: one in-process commercial stack — config store, accounts,
// the P2.5 cost-event substrate, the meter, the entitlement gate, the P5.5 verified-delta ledger and a
// Stripe-style provider — with every claim in the M10 exit checklist (PRD §13) asserted against what
// it actually produces.
//
// The point of building it ONCE and asserting many times is not speed. A checklist where each item
// quietly gets its own favourable fixture proves nothing about the system: it proves that a fixture
// can be constructed for each claim. Here every assertion is made against the same customers, the same
// events, the same ledger and the same provider, so the claims have to be simultaneously true.
package p7e2e

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/telemetry"
	"github.com/heros-foreal/agentd/internal/verification"
)

// ── The fixture catalog (task 9.1) ───────────────────────────────────────────
//
// Four NAMED plans with limits and OPAQUE price references. There is no amount here — a price is a
// provider handle — which is exactly why a fixture catalog can be committed at all while a real one
// cannot (TestNoPlanCatalogInGitTrackedFile enforces the distinction).
const fixtureCatalog = `{
  "version": "cfg-fixture-1",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":10,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":100,"seats":5,"retention_days":30,"eval_compute":100},
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

// catalogV2 repoints Team's SUM band and its metered price reference — the config publish task 1.4 /
// 9.3 asserts takes effect with no code deploy.
const catalogV2 = `{
  "version": "cfg-fixture-2",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":10,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":100000,"seats":50,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered_v2"}},
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

// The fixture period and clock.
var (
	julyStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	july      = metering.MonthPeriod(julyStart)
	afterJuly = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
)

// The credentials the secrets manager holds. Their VALUES matter to exactly one test — the one that
// asserts they appear nowhere.
const (
	billingAPIKey     = "sk_test_p7e2e_provider_key_1a2b3c"
	webhookSigningKey = "whsec_p7e2e_signing_4d5e6f"
)

// planIDs is every named plan, so the matrix iterates the real catalog.
var planIDs = []string{"free", "team", "business", "enterprise"}

// stack is the whole P7 commercial stack, wired once.
type stack struct {
	source   *plancfg.MemSource
	plans    *plancfg.Resolver
	audit    *plancfg.MemAudit
	accounts *account.MemStore
	events   *metering.MemCostEvents
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	gate     *entitlement.Gate
	deltas   *metering.MemVerifiedDeltas
	savings  *metering.MemSavingsStore
	ledger   *billing.MemLedger
	provider *billing.StubProvider
	deliver  *billing.MemDeliveries
	rollout  *billing.Rollout
	sink     *recordingSink
	alerter  *metering.MemAlerter
	observer *metering.Observer
	svc      *billing.Service
	// customerOf maps a plan id to the synthetic customer on that plan.
	customerOf map[string]string
}

// recordingSink keeps every emitted metric so the observability and secrets assertions can read the
// ACTUAL events rather than the emitter's intent.
type recordingSink struct {
	events []metricevent.Event
	spans  []telemetry.Span
}

func (r *recordingSink) EmitMetric(_ context.Context, ev metricevent.Event) {
	r.events = append(r.events, ev)
}
func (r *recordingSink) EmitSpan(_ context.Context, sp telemetry.Span) { r.spans = append(r.spans, sp) }

// newStack builds the fixture: one synthetic customer per NAMED plan, a stream of P2.5 cost events for
// the period, a P5.5 verified-delta ledger holding both a merged verified saving AND an estimated /
// un-merged one, and a Stripe-style provider with subscriptions, metered usage, invoices and
// redeliverable webhooks (task 9.1).
func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()
	now := func() time.Time { return afterJuly }

	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(fixtureCatalog)); err != nil {
		t.Fatalf("publish catalog: %v", err)
	}
	audit := plancfg.NewMemAudit()
	plans := plancfg.NewResolver(src, audit)
	plans.SetClock(now)
	if _, err := plans.Reload("fixture"); err != nil {
		t.Fatalf("reload: %v", err)
	}

	provider := billing.NewStubProvider()
	accts := account.NewMemStore()
	events := metering.NewMemCostEvents()
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(events, usage)
	meter.SetClock(now)

	customerOf := map[string]string{}
	for _, plan := range planIDs {
		cus := "cus_" + plan
		customerOf[plan] = cus
		handle, err := provider.EnsureCustomer(ctx, cus)
		if err != nil {
			t.Fatalf("ensure customer %s: %v", cus, err)
		}
		if _, err := accts.Create(account.Account{
			CustomerID: cus, ProviderCustomerHandle: handle,
			ActivePlanID: plan, PlanConfigVersion: plans.Version(), CreatedAt: julyStart,
		}); err != nil {
			t.Fatalf("create account %s: %v", cus, err)
		}

		// A stream of REAL P2.5 cost events for the period. Every customer accrues the same spend, so a
		// difference in outcome below can only come from packaging, never from the fixture.
		runID := "run-" + plan
		events.Attribute(runID, cus)
		for i, usd := range fixtureSpend {
			events.Put(costEvent(runID, runID+"|router|"+string(rune('a'+i)), usd,
				julyStart.Add(time.Duration(i+1)*24*time.Hour)))
		}
		if _, _, err := meter.RecordSUM(cus, july); err != nil {
			t.Fatalf("record sum %s: %v", cus, err)
		}
		for _, m := range []struct {
			metric metering.Metric
			qty    float64
		}{{metering.MetricSeats, 4}, {metering.MetricRetention, 30}, {metering.MetricEvalCompute, 64}} {
			if _, _, err := meter.RecordUsage(cus, july, m.metric, m.qty, "fixture-"+string(m.metric)); err != nil {
				t.Fatalf("record %s: %v", m.metric, err)
			}
		}
	}

	// The P5.5 verified-delta ledger — for the Enterprise customer, the only one whose plan carries a
	// gainshare price reference. It holds BOTH shapes on purpose.
	deltas := metering.NewMemVerifiedDeltas()
	ent := customerOf["enterprise"]
	deltas.Put(verifiedDelta("vd-merged", ent, true, false, "mergecommit-aaa", 60, 35))    // billable: 25
	deltas.Put(verifiedDelta("vd-estimated", ent, true, true, "mergecommit-bbb", 900, 10)) // ESTIMATE
	deltas.Put(verifiedDelta("vd-unmerged", ent, false, false, "", 800, 20))               // never merged

	secrets, err := billing.NewManagedSecrets(providergateway.StaticSecrets{
		billing.SecretBillingAPIKey:         {APIKey: billingAPIKey},
		billing.SecretBillingWebhookSigning: {APIKey: webhookSigningKey},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	ledger := billing.NewMemLedger()
	svc, err := billing.NewService(provider, ledger, accts, plans, meter, secrets)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	svc.SetClock(func() time.Time { return afterJuly })
	deliver := billing.NewMemDeliveries()
	svc.WithDeliveries(deliver)

	rollout := billing.NewRollout()
	if err := rollout.Enable(billing.ModeTest); err != nil {
		t.Fatalf("rollout: %v", err)
	}
	if err := rollout.EnableGainshare(); err != nil {
		t.Fatalf("rollout gainshare: %v", err)
	}
	rollout.EnableAutoMergeEntitlement()

	sink := &recordingSink{}
	alerter := &metering.MemAlerter{}
	obs := metering.NewObserver(sink, alerter)
	obs.SetClock(now)
	svc.WithRollout(rollout).WithObserver(obs)

	// The gate is clocked INSIDE the period on purpose. It answers "may this action happen NOW", so it
	// reads the CURRENT period's meters — and a gate clocked after the period rolled over would read an
	// empty August and cheerfully allow everything a July over-limit should have denied. That is not a
	// test detail: it is the difference between a limit that binds and one that silently lapses at
	// midnight on the 1st.
	gate := entitlement.NewGate(accts, plans, usage)
	gate.SetClock(func() time.Time { return julyStart.Add(15 * 24 * time.Hour) })

	return &stack{source: src, plans: plans, audit: audit, accounts: accts, events: events,
		usage: usage, meter: meter, gate: gate, deltas: deltas, savings: metering.NewMemSavingsStore(),
		ledger: ledger, provider: provider, deliver: deliver, rollout: rollout, sink: sink,
		alerter: alerter, observer: obs, svc: svc, customerOf: customerOf}
}

// fixtureSpend is the per-customer cost-event stream. Sums to 63.75 — inside Team's fixture SUM band
// of 100 and well over Free's 10, so the over-limit case is real rather than contrived.
var fixtureSpend = []float64{12.5, 18.25, 9.75, 21.0, 2.25}

const fixtureSUM = 12.5 + 18.25 + 9.75 + 21.0 + 2.25

// costEvent builds a REAL P2.5 cost event — the same shape telemetry.MetricSet emits, carrying the
// seven tags and the `invocation_id` retry identity the gateway stamps.
func costEvent(runID, invocationID string, usd float64, ts time.Time) metricevent.Event {
	seed := int64(1)
	v := usd
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     "var-fixture", RunID: runID, NodeID: "router", CaseID: "case-1", Seed: &seed,
		Timestamp:  ts.UTC().Format(time.RFC3339Nano),
		ConfigHash: "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0",
		MetricName: telemetry.MetricCostUSD, Value: &v, Unit: telemetry.UnitUSD,
		Dimensions: map[string]any{telemetry.AttrInvocationID: invocationID},
	}
}

// verifiedDelta builds one P5.5 ledger entry. The verdict is a REAL verification.Verdict, so the
// billable predicate reads the same fields the P5.5 gate writes.
func verifiedDelta(ref, customerID string, merged, estimated bool, commit string, baseline, optimized float64) metering.VerifiedDelta {
	return metering.VerifiedDelta{
		Ref: ref, ProposalID: "prop-" + ref, CustomerID: customerID, Period: july.ID,
		Verdict: verification.Verdict{
			GateResult: verification.GatePass, HeldOut: true, Significant: true, RegressionPass: true,
			Delta: evalstats.Interval{Mean: 0.34, Low: 0.21, High: 0.47},
		},
		Merged: merged, MergeCommit: commit, Estimated: estimated,
		BaselineSUM: baseline, OptimizedSUM: optimized,
		Baseline: metering.BaselineMethod{
			ID: "holdout-v1", EvalSetHash: "es_fixture",
			HoldoutCaseIDs:      []string{"c4", "c5", "c6"},
			GeneratingCaseIDs:   []string{"c1", "c2", "c3"},
			Seeds:               []int64{1, 2, 3, 4, 5},
			BaselineConfigHash:  "cfg_base_" + ref,
			CandidateConfigHash: "cfg_cand_" + ref,
		},
	}
}
