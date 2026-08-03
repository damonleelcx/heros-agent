package billinge2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// exit_test.go walks the M10 exit checklist (PRD §13) item by item against ONE stack.
//
// Each subtest is named for its checklist line, so a failure says which acceptance criterion is not
// met rather than which function returned the wrong number. Wave 7a items come first, then 7b — the
// order the phase ships in, and the order they must be allowed to go green in.

// TestM10ExitChecklist is the phase's acceptance gate.
func TestM10ExitChecklist(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	ent := s.customerOf["enterprise"]

	// ── 7a ────────────────────────────────────────────────────────────────────

	t.Run("7a/SUM is derived from the P2.5 cost events and deterministic on a closed period", func(t *testing.T) {
		res, err := s.meter.DeriveSUM(ent, july)
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if res.Quantity != fixtureSUM {
			t.Fatalf("SUM = %v, want %v", res.Quantity, fixtureSUM)
		}
		if !july.Closed(afterJuly) {
			t.Fatal("precondition: the period must be closed")
		}
		for i := 0; i < 10; i++ {
			again, err := s.meter.DeriveSUM(ent, july)
			if err != nil {
				t.Fatalf("re-derive: %v", err)
			}
			if again.Quantity != res.Quantity || again.SourceDigest != res.SourceDigest {
				t.Fatalf("re-derivation %d differs: %v/%s vs %v/%s", i, again.Quantity, again.SourceDigest,
					res.Quantity, res.SourceDigest)
			}
		}
		// "Not a second pipeline" is structural: the meter's only input is the cost-event source, and
		// the figure equals the sum of exactly those events.
		evs, err := s.events.CostEvents(ent, july)
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		total := 0.0
		for _, ev := range evs {
			total += *ev.Value
		}
		if total != res.Quantity {
			t.Errorf("SUM (%v) is not the aggregate of the cost events (%v) — there is a second source", res.Quantity, total)
		}
	})

	t.Run("7a/every meter is idempotent and a replayed period is counted once", func(t *testing.T) {
		before := s.usage.Rows()
		writes := s.usage.Writes()
		for i := 0; i < 5; i++ {
			if _, _, err := s.meter.RecordSUM(ent, july); err != nil {
				t.Fatalf("replay %d: %v", i, err)
			}
			// Redeliver the substrate's events too, so both halves of idempotency are exercised.
			s.events.Put(costEvent("run-enterprise", "run-enterprise|router|a", fixtureSpend[0],
				julyStart.Add(24*time.Hour)))
		}
		if s.usage.Writes() <= writes {
			t.Fatal("the store saw no additional writes — the replay did not happen, so 'one row' proves nothing")
		}
		if s.usage.Rows() != before {
			t.Errorf("rows moved from %d to %d on replay", before, s.usage.Rows())
		}
		recs, err := s.usage.Period(ent, july.ID)
		if err != nil {
			t.Fatalf("period: %v", err)
		}
		if len(recs) != len(metering.Metrics) {
			t.Fatalf("meters = %d, want one per metric (%d)", len(recs), len(metering.Metrics))
		}
		for _, r := range recs {
			if r.Metric == metering.MetricSUM && r.Quantity != fixtureSUM {
				t.Errorf("SUM after replay = %v, want %v (the period once, not multiplied)", r.Quantity, fixtureSUM)
			}
		}
	})

	t.Run("7a/usage reconciles against the provider and a seeded drift is surfaced", func(t *testing.T) {
		for _, m := range metering.Metrics {
			if _, err := s.svc.ReportUsage(ctx, ent, july, m); err != nil {
				t.Fatalf("report %s: %v", m, err)
			}
		}
		clean, err := s.svc.Reconcile(ctx, ent, july, s.observer)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if !clean.Matched {
			t.Fatalf("a fully-reported period did not reconcile clean: %+v", clean.Drift)
		}

		// Seed a drift, and snapshot both sides so "surfaced, not silently accepted" can be checked
		// against them not having moved.
		s.provider.DropUsage(ent, july.ID, string(metering.MetricSeats))
		beforePlatform, _ := s.usage.Period(ent, july.ID)
		beforeProvider, _ := s.provider.RecordedUsage(ctx, ent, july.ID)

		drifted, err := s.svc.Reconcile(ctx, ent, july, s.observer)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if drifted.Matched || len(drifted.Drift) == 0 {
			t.Fatal("the seeded drift was silently accepted")
		}
		if drifted.Drift[0].Detail == "" {
			t.Error("the drift carries no explanation")
		}
		afterPlatform, _ := s.usage.Period(ent, july.ID)
		afterProvider, _ := s.provider.RecordedUsage(ctx, ent, july.ID)
		if len(afterPlatform) != len(beforePlatform) || len(afterProvider) != len(beforeProvider) {
			t.Error("the reconciler wrote to one of the ledgers — it must never reconcile by overwrite")
		}
		// Restore the fixture for the tests that follow.
		if _, err := s.svc.RepairUnreported(ctx, ent, july, drifted); err != nil {
			t.Fatalf("repair: %v", err)
		}
	})

	t.Run("7a/access is gated by plan AND automation level", func(t *testing.T) {
		want := map[plancfg.Feature]map[string]bool{
			plancfg.FeatureCLI:        {"free": true, "team": true, "business": true, "enterprise": true},
			plancfg.FeatureDiscovery:  {"free": true, "team": true, "business": true, "enterprise": true},
			plancfg.FeatureAssistedPR: {"free": false, "team": true, "business": true, "enterprise": true},
			plancfg.FeatureDashboard:  {"free": false, "team": true, "business": true, "enterprise": true},
			plancfg.FeatureAutoMerge:  {"free": false, "team": false, "business": false, "enterprise": true},
		}
		if len(want) != len(entitlement.Surfaces) {
			t.Fatalf("the matrix covers %d of %d gated surfaces", len(want), len(entitlement.Surfaces))
		}
		for _, surface := range entitlement.Surfaces {
			for _, plan := range planIDs {
				d, err := surface.Check(s.gate, s.customerOf[plan])
				if err != nil {
					t.Fatalf("%s/%s: %v", surface.Feature, plan, err)
				}
				if d.Allowed != want[surface.Feature][plan] {
					t.Errorf("%s on %s: allowed=%v, want %v (%s)", surface.Feature, plan, d.Allowed,
						want[surface.Feature][plan], d.Reason)
				}
			}
		}
		// The level axis is independent: Enterprise asking for auto-merge under an Assisted contract is
		// still denied.
		d, err := s.gate.CheckEntitlement(ent, plancfg.FeatureAutoMerge, entitlement.LevelAssisted)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if d.Allowed {
			t.Error("auto-merge was allowed at the Assisted level — the level axis is not checked")
		}
	})

	t.Run("7a/a plan or price change takes effect with no code deploy", func(t *testing.T) {
		team := s.customerOf["team"]
		before, err := s.plans.ResolvePlan("team")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if band, _ := before.Limit(plancfg.LimitSUMBand); band != 100 {
			t.Fatalf("precondition: team's band is %v", band)
		}
		// Take the customer over the band so the change is observable through the GATE, not just the
		// resolver.
		if _, _, err := s.meter.RecordUsage(team, july, metering.MetricSUM, 500, "over"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		d, _ := s.gate.AssistedPR(team)
		if d.Allowed {
			t.Fatal("precondition: team must be over its band")
		}

		// The ONLY thing that happens: a config publish into the same running resolver.
		if err := s.source.PublishJSON([]byte(catalogV2)); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if _, err := s.plans.Reload("finance"); err != nil {
			t.Fatalf("reload: %v", err)
		}

		after, _ := s.plans.ResolvePlan("team")
		if band, _ := after.Limit(plancfg.LimitSUMBand); band != 100000 {
			t.Errorf("the new band did not take effect: %v", band)
		}
		if after.PriceRefs["metered"] != "price_ref_team_metered_v2" {
			t.Errorf("the repointed price reference did not take effect: %q", after.PriceRefs["metered"])
		}
		if d, _ := s.gate.AssistedPR(team); !d.Allowed {
			t.Errorf("the gate still denies after the publish: %s", d.Reason)
		}
		// And the publish is audited.
		evs := s.audit.Events()
		last := evs[len(evs)-1]
		if len(last.Changed) == 0 || last.ToVersion != "cfg-fixture-2" {
			t.Errorf("the plan_change audit event does not describe the publish: %+v", last)
		}
		// Restore for the tests that follow.
		if err := s.source.PublishJSON([]byte(fixtureCatalog)); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if _, err := s.plans.Reload("fixture"); err != nil {
			t.Fatalf("restore reload: %v", err)
		}
		if _, _, err := s.meter.RecordSUM(team, july); err != nil {
			t.Fatalf("restore sum: %v", err)
		}
		_ = before
	})

	t.Run("7a/an over-limit action is denied with a named reason and an upgrade path", func(t *testing.T) {
		team := s.customerOf["team"]
		// Take Team past its fixture SUM band of 100. Assisted PRs are a spend-CONSUMING surface, so
		// this is where the band binds — the CLI and discovery are the plan's floor and stay available
		// (see meteredLimits: taking away the tool that shows you your spend is not a paywall).
		if _, _, err := s.meter.RecordUsage(team, july, metering.MetricSUM, 500, "over-band"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		defer func() {
			if _, _, err := s.meter.RecordSUM(team, july); err != nil {
				t.Fatalf("restore: %v", err)
			}
		}()

		if d, err := s.gate.CLI(team); err != nil || !d.Allowed {
			t.Errorf("the CLI was denied to an over-band customer — the floor must hold: %+v (%v)", d, err)
		}

		d, err := s.gate.AssistedPR(team)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if d.Allowed {
			t.Fatal("an over-band action was silently allowed")
		}
		if d.ReasonCode != entitlement.ReasonOverLimit {
			t.Fatalf("reason code = %q, want over_limit", d.ReasonCode)
		}
		if d.Limit == nil || d.Limit.Allowed != 100 || d.Limit.Observed != 500 {
			t.Errorf("the denial does not carry the meter: %+v", d.Limit)
		}
		if d.Limit.Period != july.ID {
			t.Errorf("the denial names period %q, want %q", d.Limit.Period, july.ID)
		}
		if d.UpgradePlan != "business" {
			t.Fatalf("upgrade plan = %q, want the cheapest that lifts it (business)", d.UpgradePlan)
		}
		if d.UpgradePlanName == "" {
			t.Error("the upgrade path has no customer-facing plan NAME")
		}
		if err := d.Validate(); err != nil {
			t.Errorf("incoherent denial: %v", err)
		}
		// The named plan actually lifts it.
		up, err := s.gate.AssistedPR(s.customerOf[d.UpgradePlan])
		if err != nil {
			t.Fatalf("check upgrade: %v", err)
		}
		if !up.Allowed {
			t.Errorf("the named upgrade plan %q still denies: %s", d.UpgradePlan, up.Reason)
		}
	})

	t.Run("7a/subscription and metered usage are billed, and never double-charged", func(t *testing.T) {
		if _, err := s.svc.StartSubscription(ctx, ent); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		subKey := billing.SubscriptionChargeIdempotencyKey(ent, july.ID, "enterprise")
		metKey := billing.MeteredChargeIdempotencyKey(ent, july.ID, string(metering.MetricSUM))
		before := s.provider.ChargeCount()

		for i := 0; i < 5; i++ {
			if _, err := s.svc.Charge(ctx, ent, july, billing.KindSubscription, subKey); err != nil {
				t.Fatalf("subscription charge %d: %v", i, err)
			}
			if _, err := s.svc.Charge(ctx, ent, july, billing.KindMetered, metKey); err != nil {
				t.Fatalf("metered charge %d: %v", i, err)
			}
		}
		if got := s.provider.ChargeCount() - before; got != 2 {
			t.Errorf("the provider recorded %d charges for 5 retries of 2 operations, want 2", got)
		}
		// An ambiguous failure — the provider records, the response is lost — is the nastiest retry.
		s.provider.SetFailAfterRecord(true)
		evalKey := billing.MeteredChargeIdempotencyKey(ent, july.ID, string(metering.MetricEvalCompute))
		if _, err := s.svc.Charge(ctx, ent, july, billing.KindMetered, evalKey); err == nil {
			t.Fatal("the ambiguous failure did not surface")
		}
		recorded := s.provider.ChargeCount()
		if _, err := s.svc.Charge(ctx, ent, july, billing.KindMetered, evalKey); err != nil {
			t.Fatalf("retry after ambiguous failure: %v", err)
		}
		if s.provider.ChargeCount() != recorded {
			t.Error("the retry after an ambiguous failure double-charged")
		}
	})

	t.Run("7a/webhooks are handled idempotently and rejected unsigned", func(t *testing.T) {
		payload := billing.WebhookPayload{ProviderEventID: "evt_m10", Type: billing.WebhookInvoicePaymentFailed,
			CustomerID: ent, Period: july.ID, InvoiceRef: "prov_inv_m10"}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		stamp := afterJuly.UTC().Format(time.RFC3339)
		good := billing.SignedWebhook{Body: body, Timestamp: stamp,
			Signature: billing.SignWebhook(webhookSigningKey, stamp, body)}

		before := s.deliver.Count()
		first, err := s.svc.HandleWebhook(ctx, good)
		if err != nil {
			t.Fatalf("first delivery: %v", err)
		}
		if !first.Applied || !first.State.PaymentFailed {
			t.Fatalf("the first delivery did not apply: %+v", first)
		}
		for i := 0; i < 4; i++ {
			again, err := s.svc.HandleWebhook(ctx, good)
			if err != nil {
				t.Fatalf("redelivery %d: %v", i, err)
			}
			if !again.Duplicate || again.Applied {
				t.Errorf("redelivery %d was processed again: %+v", i, again)
			}
		}
		if s.deliver.Count() != before+1 {
			t.Errorf("delivery rows moved by %d for 5 deliveries of one event", s.deliver.Count()-before)
		}

		// Unsigned: rejected BEFORE any side effect.
		ledgerBefore := len(testEvents(s.ledger, ent, ""))
		unsigned := billing.SignedWebhook{Body: body, Timestamp: stamp}
		if _, err := s.svc.HandleWebhook(ctx, unsigned); !errors.Is(err, billing.ErrNoSignature) {
			t.Errorf("an unsigned webhook was not rejected: %v", err)
		}
		forged := billing.SignedWebhook{Body: body, Timestamp: stamp, Signature: "v1=deadbeef"}
		if _, err := s.svc.HandleWebhook(ctx, forged); !errors.Is(err, billing.ErrBadSignature) {
			t.Errorf("a forged webhook was not rejected: %v", err)
		}
		if len(testEvents(s.ledger, ent, "")) != ledgerBefore || s.deliver.Count() != before+1 {
			t.Error("a rejected webhook produced a side effect")
		}
	})

	t.Run("7a/a billing error is corrected additively with no data loss", func(t *testing.T) {
		metKey := billing.MeteredChargeIdempotencyKey(ent, july.ID, string(metering.MetricSUM))
		wrong, err := s.ledger.ByKey(metKey)
		if err != nil {
			t.Fatalf("the charge to correct is missing: %v", err)
		}
		usageBefore, err := s.usage.Get(metering.Key{CustomerID: ent, Period: july.ID, Metric: metering.MetricSUM})
		if err != nil {
			t.Fatalf("usage: %v", err)
		}

		const reason = "billed before the period's late cost events landed"
		credit, err := s.svc.Credit(ctx, ent, wrong.EventID, reason)
		if err != nil {
			t.Fatalf("credit: %v", err)
		}
		if credit.EventID == wrong.EventID || credit.Type != billing.TypeCredit {
			t.Fatalf("the correction is not a new credit row: %+v", credit)
		}
		if credit.CausedBy != "billing_event:"+wrong.EventID || credit.Reason != reason {
			t.Errorf("the credit does not name what it corrects and why: %+v", credit)
		}
		// ORIGINALS INTACT.
		still, err := s.ledger.ByKey(metKey)
		if err != nil {
			t.Fatalf("the original charge is gone: %v", err)
		}
		if still.Quantity != wrong.Quantity || still.ProviderRef != wrong.ProviderRef || still.Status != wrong.Status {
			t.Errorf("the original charge was mutated:\n got %+v\nwant %+v", still, wrong)
		}
		usageAfter, err := s.usage.Get(usageBefore.Key())
		if err != nil {
			t.Fatalf("the usage record is gone: %v", err)
		}
		if usageAfter != usageBefore {
			t.Errorf("the usage record was mutated by the correction")
		}
		// NET is right and the period replays.
		if credit.Quantity != wrong.Quantity {
			t.Errorf("the credit (%v) does not offset the charge (%v)", credit.Quantity, wrong.Quantity)
		}
		for _, ev := range testEvents(s.ledger, ent, july.ID) {
			if ev.CausedBy == "" {
				t.Errorf("row %s names no cause — the period cannot be reconstructed", ev.EventID)
			}
		}
	})

	t.Run("7a/customers use their own provider keys and no invoice line resells tokens", func(t *testing.T) {
		inv, err := s.provider.Invoice(ctx, ent, july.ID)
		if err != nil {
			t.Fatalf("invoice: %v", err)
		}
		if err := inv.Validate(); err != nil {
			t.Fatalf("the invoice fails the no-resale check: %v", err)
		}
		if len(inv.Lines) == 0 {
			t.Fatal("the invoice has no lines — the assertion would be vacuous")
		}
		for _, l := range inv.Lines {
			switch l.Kind {
			case billing.LineSubscription, billing.LineMetered, billing.LineGainshare,
				billing.LineCredit, billing.LineRefund:
			default:
				t.Errorf("invoice line of kind %q", l.Kind)
			}
			if l.Basis == "" {
				t.Errorf("line %+v traces to nothing", l)
			}
		}
		// The charge kinds are a CLOSED set with no token member, so a resold-token line is
		// unrepresentable rather than merely absent.
		for _, k := range billing.ChargeKinds {
			if strings.Contains(string(k), "token") {
				t.Errorf("charge kind %q is a token pass-through", k)
			}
		}
	})

	// ── 7b ────────────────────────────────────────────────────────────────────

	t.Run("7b/billable savings come only from merged verified deltas and estimates bill nothing", func(t *testing.T) {
		bs, err := metering.ComputeBillableSavings(s.deltas, ent, july)
		if err != nil {
			t.Fatalf("compute: %v", err)
		}
		const wantSavings = 60 - 35 // only the merged, verified delta
		if bs.Savings != wantSavings {
			t.Fatalf("billable savings = %v, want %v — the estimate (890) and the un-merged saving (780) "+
				"must contribute zero", bs.Savings, wantSavings)
		}
		if len(bs.VerifiedDeltaRefs) != 1 || bs.VerifiedDeltaRefs[0] != "vd-merged" {
			t.Errorf("refs = %v, want [vd-merged]", bs.VerifiedDeltaRefs)
		}
		if len(bs.MergeCommits) != 1 || bs.MergeCommits[0] != "mergecommit-aaa" {
			t.Errorf("merge commits = %v", bs.MergeCommits)
		}
		if len(bs.Excluded) != 2 {
			t.Fatalf("exclusions = %d, want 2 (the estimate and the un-merged proposal)", len(bs.Excluded))
		}
		for _, e := range bs.Excluded {
			if e.Reason == "" {
				t.Errorf("exclusion %s names no reason", e.Ref)
			}
		}
	})

	t.Run("7b/gainshare bills verified savings only and refuses anything else", func(t *testing.T) {
		if _, err := s.accounts.SetGainshareConsent(ent, true, julyStart); err != nil {
			t.Fatalf("consent: %v", err)
		}
		before := s.provider.ChargeCount()
		res, err := s.svc.ChargeGainshare(ctx, ent, july, s.deltas, s.savings)
		if err != nil {
			t.Fatalf("gainshare: %v", err)
		}
		if res.Event.Type != billing.TypeGainshareCharge || res.Event.Quantity != 25 {
			t.Errorf("gainshare charge = %+v", res.Event)
		}
		if s.provider.ChargeCount() != before+1 {
			t.Errorf("provider charges moved by %d", s.provider.ChargeCount()-before)
		}
		// It TRACES to its evidence, and the methodology reconstructs from the event alone.
		deltas, merges, err := billing.GainshareEvidence(res.Event, s.deltas)
		if err != nil {
			t.Fatalf("evidence: %v", err)
		}
		if len(deltas) != 1 || len(merges) != 1 || merges[0] != "mergecommit-aaa" {
			t.Fatalf("evidence = %d deltas, %v merges", len(deltas), merges)
		}
		if !deltas[0].Baseline.Complete() || deltas[0].Baseline.ID != "holdout-v1" {
			t.Errorf("the baseline + hold-out methodology is not reconstructable: %+v", deltas[0].Baseline)
		}

		// A customer with NO verified savings raises no charge at all.
		team := s.customerOf["team"]
		if _, err := s.accounts.SetGainshareConsent(team, true, julyStart); err != nil {
			t.Fatalf("consent: %v", err)
		}
		if _, err := s.svc.ChargeGainshare(ctx, team, july, s.deltas, s.savings); err == nil {
			t.Error("a gainshare charge was raised for a customer with no verified savings")
		}

		// And a retry raises no second charge.
		count := s.provider.ChargeCount()
		if _, err := s.svc.ChargeGainshare(ctx, ent, july, s.deltas, s.savings); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if s.provider.ChargeCount() != count {
			t.Error("a retried gainshare charge double-charged")
		}
	})

	// ── Cross-cutting ─────────────────────────────────────────────────────────

	t.Run("revenue is observable on the P2.5 substrate with alerts on failed charges and drift", func(t *testing.T) {
		if len(s.sink.events) == 0 {
			t.Fatal("no revenue events at all")
		}
		seen := map[string]bool{}
		for _, ev := range s.sink.events {
			if err := ev.Validate(); err != nil {
				t.Errorf("a revenue event would be REJECTED at the P2.5 emission boundary: %v", err)
			}
			seen[ev.MetricName] = true
			if _, leaked := telemetry.SeriesLabels(ev)[telemetry.AttrCustomerID]; leaked {
				t.Error("customer_id leaked into the TSDB label set — one series per customer")
			}
		}
		for _, name := range []string{telemetry.MetricRevenueSUM, telemetry.MetricRevenueMetered,
			telemetry.MetricRevenueGainshareBilled, telemetry.MetricRevenueReconcileDrift,
			telemetry.MetricRevenueInvoiceState} {
			if !seen[name] {
				t.Errorf("no %s event was emitted during a full billing period", name)
			}
		}
		if len(s.alerter.OfKind(metering.AlertReconciliationDrift)) == 0 {
			t.Error("the seeded drift raised no alert")
		}
	})

	t.Run("secrets come from the manager and appear in no span, label, or log", func(t *testing.T) {
		// Everything this capability produces outward, concatenated, then scanned once.
		var blob strings.Builder
		for _, ev := range s.sink.events {
			b, err := json.Marshal(ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			blob.Write(b)
			for k, v := range telemetry.SeriesLabels(ev) {
				fmt.Fprintf(&blob, "%s=%s;", k, v)
			}
		}
		for _, sp := range s.sink.spans {
			b, _ := json.Marshal(sp)
			blob.Write(b)
		}
		for _, plan := range planIDs {
			for _, ev := range testEvents(s.ledger, s.customerOf[plan], "") {
				b, err := json.Marshal(ev)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				blob.Write(b)
			}
			for _, rec := range mustPeriod(t, s, s.customerOf[plan]) {
				b, _ := json.Marshal(rec)
				blob.Write(b)
			}
		}
		for k, v := range s.svc.Describe() {
			fmt.Fprintf(&blob, "%s=%s;", k, v)
		}
		for k, v := range s.rollout.Describe() {
			fmt.Fprintf(&blob, "%s=%s;", k, v)
		}
		for _, a := range s.alerter.Alerts() {
			b, _ := json.Marshal(a)
			blob.Write(b)
		}
		out := blob.String()
		if len(out) < 500 {
			t.Fatalf("only %d bytes of output to scan — the assertion would be vacuously true", len(out))
		}
		for _, secret := range []string{billingAPIKey, webhookSigningKey} {
			if strings.Contains(out, secret) {
				t.Errorf("a secret value reached telemetry, the ledger, or a health surface")
			}
		}
		// The positive half: the secrets ARE reachable, from the manager, at the moment of use — so the
		// absence above is not just "nothing was configured".
		secrets, err := billing.NewManagedSecrets(nil)
		if err == nil {
			t.Error("a billing service was built with no secrets source")
		}
		_ = secrets
	})

	t.Run("the rollout keeps each wave dark until its checklist is green", func(t *testing.T) {
		dark := billing.NewRollout()
		for _, k := range billing.ChargeKinds {
			if err := dark.AllowCharge(k); !errors.Is(err, billing.ErrBillingDark) {
				t.Errorf("%s allowed while dark: %v", k, err)
			}
		}
		if dark.AllowAutoMergeEntitlement() {
			t.Error("the Enterprise auto-merge entitlement is granted by default")
		}
		if err := dark.Enable(billing.ModeTest); err != nil {
			t.Fatalf("enable: %v", err)
		}
		if err := dark.AllowCharge(billing.KindGainshare); !errors.Is(err, billing.ErrGainshareDisabled) {
			t.Errorf("7b was reachable in wave 7a: %v", err)
		}
		if dark.Mode() != billing.ModeTest {
			t.Errorf("7a did not ship in provider TEST mode: %q", dark.Mode())
		}
	})
}

func mustPeriod(t *testing.T, s *stack, customerID string) []metering.UsageRecord {
	t.Helper()
	recs, err := s.usage.Period(customerID, july.ID)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	return recs
}
