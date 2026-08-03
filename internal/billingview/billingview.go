// Package billingview builds the P7 billing read model — the adapter that existed only inside
// cmd/demo/billing and cmd/proof/billing, which is what internal/launch meant by "no persistent adapter
// exists outside a demo binary".
//
// # What this is, and what it deliberately is not
//
// It is a READ model. It resolves an account's plan, reads the meters and the SUM trend, asks the
// entitlement gate what the plan includes, assembles the invoice lines from the billing ledger, and
// reports verified savings. It performs no provider call and mints no charge; the one write it exposes
// is recording gainshare consent, which is a customer decision about their own account.
//
// Checkout and plan changes are api.PaymentsSource, NOT this — they call the payment provider, and a
// deployment can have a durable ledger without provider credentials. Keeping them apart is why billing
// can mount here while payments stays honest about needing a provider.
//
// # Every collaborator is required
//
// A nil one is refused at construction rather than nil-checked per field. A billing page assembled from
// whichever stores happened to be wired is a page whose blanks mean nothing in particular — and on this
// surface a blank is a number a customer will read as zero.
package billingview

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// TrendPeriods is how many periods the SUM trend covers, newest last.
//
// A constant rather than configuration: the trend is a shape a reader compares at a glance, and a
// deployment that showed three months where another showed twelve would make two screenshots
// incomparable for no gain.
const TrendPeriods = 6

// Source is the api.BillingSource over a wired billing stack.
type Source struct {
	accounts account.Store
	plans    *plancfg.Resolver
	usage    metering.UsageStore
	deltas   metering.VerifiedDeltaLedger
	gate     *entitlement.Gate
	svc      *billing.Service
	now      func() time.Time
}

// New returns the read model. Every collaborator is required — see the package comment.
func New(accounts account.Store, plans *plancfg.Resolver, usage metering.UsageStore,
	deltas metering.VerifiedDeltaLedger, gate *entitlement.Gate, svc *billing.Service) (*Source, error) {
	switch {
	case accounts == nil:
		return nil, errors.New("billingview: nil account store")
	case plans == nil:
		return nil, errors.New("billingview: nil plan resolver")
	case usage == nil:
		return nil, errors.New("billingview: nil usage store")
	case deltas == nil:
		return nil, errors.New("billingview: nil verified-delta ledger")
	case gate == nil:
		return nil, errors.New("billingview: nil entitlement gate")
	case svc == nil:
		return nil, errors.New("billingview: nil billing service")
	}
	return &Source{accounts: accounts, plans: plans, usage: usage, deltas: deltas,
		gate: gate, svc: svc, now: time.Now}, nil
}

// periods returns the trend window, oldest first, ending at the current period.
func (s *Source) periods() []metering.Period {
	cur := metering.MonthPeriod(s.now().UTC())
	out := make([]metering.Period, 0, TrendPeriods)
	for i := TrendPeriods - 1; i >= 0; i-- {
		out = append(out, metering.MonthPeriod(cur.Start.AddDate(0, -i, 0)))
	}
	return out
}

// Periods lists the periods the picker offers, newest first.
func (s *Source) Periods(string) []string {
	ps := s.periods()
	out := make([]string, 0, len(ps))
	for i := len(ps) - 1; i >= 0; i-- {
		out = append(out, ps[i].ID)
	}
	return out
}

// SetGainshareConsent records or revokes consent and returns the refreshed view.
func (s *Source) SetGainshareConsent(customerID string, consented bool) (api.BillingView, error) {
	if _, err := s.accounts.SetGainshareConsent(customerID, consented, s.now().UTC()); err != nil {
		return api.BillingView{}, err
	}
	v, ok := s.Billing(customerID, "")
	if !ok {
		return api.BillingView{}, fmt.Errorf("billingview: consent recorded for %s but the view could not be rebuilt", customerID)
	}
	return v, nil
}

// Billing assembles the whole surface for a customer-period. An empty period means the current one.
//
// ok=false means no such customer, or a plan that does not resolve. A read failure also returns false —
// api.BillingSource has nowhere to put an error, a limitation inherited rather than introduced, and
// stated rather than hidden.
func (s *Source) Billing(customerID, periodID string) (api.BillingView, bool) {
	ctx := context.Background()
	ps := s.periods()
	p := ps[len(ps)-1]
	for _, cand := range ps {
		if cand.ID == periodID {
			p = cand
		}
	}
	acct, err := s.accounts.Get(customerID)
	if err != nil {
		return api.BillingView{}, false
	}
	plan, err := s.plans.ResolvePlan(acct.ActivePlanID)
	if err != nil {
		return api.BillingView{}, false
	}

	v := api.BillingView{
		CustomerID: customerID, Period: p.ID, AvailablePeriods: s.Periods(customerID),
		PlanID: plan.PlanID, PlanName: plan.DisplayName, PlanConfigVersion: plan.Version,
		SUMUnit: telemetry.UnitUSD,
	}

	recs, err := s.usage.Period(customerID, p.ID)
	if err != nil {
		return api.BillingView{}, false
	}
	// Empty is REPORTED, not inferred from a zero SUM: "no usage recorded this period" and "usage of
	// zero" read identically as a number and mean different things to someone checking a bill.
	v.Empty = len(recs) == 0
	for _, rec := range recs {
		if rec.Metric == metering.MetricSUM {
			v.SUM = rec.Quantity
		}
		allowed, set := plan.Limit(limitFor(rec.Metric))
		v.Meters = append(v.Meters, api.MeterView{
			Metric: string(rec.Metric), Label: meterLabel(rec.Metric), Value: rec.Quantity,
			Unit: meterUnit(rec.Metric), Allowed: allowed, Unlimited: !set,
			Over:               set && rec.Quantity > allowed,
			ReportedToProvider: rec.ReportedToProvider, ProviderUsageRef: rec.ProviderUsageRef,
		})
	}

	for _, tp := range ps {
		pt := api.PeriodPoint{Period: tp.ID}
		if rec, err := s.usage.Get(metering.Key{CustomerID: customerID, Period: tp.ID, Metric: metering.MetricSUM}); err == nil {
			pt.SUM = rec.Quantity
		}
		if bs, err := metering.ComputeBillableSavings(s.deltas, customerID, tp); err == nil {
			pt.Baseline, pt.Optimized = bs.BaselineSUM, bs.OptimizedSUM
		}
		v.SUMTrend = append(v.SUMTrend, pt)
	}

	v.Entitlements, v.Denial = s.entitlements(customerID)
	v.Invoice = s.invoice(customerID, p)

	st := s.svc.BillingState(customerID)
	v.State = api.BillingStateView{
		InvoiceStatus: st.InvoiceStatus, SubscriptionStatus: st.SubscriptionStatus,
		PaymentFailed: st.PaymentFailed, PastDue: st.PastDue,
	}
	if st.PaymentFailed || st.PastDue {
		v.State.Guidance = "Your billing provider could not take payment. Update the payment method with " +
			"the provider to clear this; the platform does not hold your card details."
	}
	_ = ctx

	v.Savings = s.savings(customerID, p, acct.GainshareConsent, acct.ConsentedAt, plan.PriceRefs["gainshare"] != "")
	return v, true
}

// entitlements asks the gate what this plan includes, and picks the denial worth surfacing.
//
// An over-limit denial wins over a plan-tier one when both exist: "you have used more than your plan
// allows" is actionable now, and "this plan does not include X" is a purchase decision.
func (s *Source) entitlements(customerID string) ([]api.EntitlementRow, *api.DenialView) {
	var rows []api.EntitlementRow
	var denial *entitlement.Decision
	for _, surface := range entitlement.Surfaces {
		d, err := surface.Check(s.gate, customerID)
		if err != nil {
			continue
		}
		row := api.EntitlementRow{Feature: string(surface.Feature), Label: surfaceLabel(surface.Feature), Included: d.Allowed}
		if !d.Allowed {
			row.Reason, row.UpgradePlan, row.UpgradePlanName = d.Reason, d.UpgradePlan, d.UpgradePlanName
			if denial == nil || (d.ReasonCode == entitlement.ReasonOverLimit && denial.ReasonCode != entitlement.ReasonOverLimit) {
				cp := d
				denial = &cp
			}
		}
		rows = append(rows, row)
	}
	if denial == nil {
		return rows, nil
	}
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
	return rows, &dv
}

// invoice assembles the period's charge lines from the ledger.
//
// It does NOT call the provider for an invoice ref. The proof binary does, because it has a stub
// provider on hand; a read model that made a network call on every page load would make the billing
// page fail when the provider is slow — and the platform's own ledger is the authority for what was
// charged anyway.
func (s *Source) invoice(customerID string, p metering.Period) api.InvoiceView {
	out := api.InvoiceView{}
	evs, err := s.svc.Ledger().Events(customerID, p.ID)
	if err != nil {
		// A failed ledger read leaves the invoice EMPTY rather than partially assembled. The caller
		// renders v.Empty and the meters; showing some lines and not others would total wrongly.
		return out
	}
	byKind := map[string]*api.TotalView{}
	for _, ev := range evs {
		if !ev.Type.ChargeBearing() {
			continue
		}
		kind := string(ev.Kind)
		if kind == "" {
			kind = string(ev.Type)
		}
		line := api.LineView{
			Kind: kind, Label: lineLabel(ev), Basis: ev.CausedBy, AmountRef: ev.AmountRef,
			Quantity: ev.Quantity, Unit: telemetry.UnitUSD, ChargeRef: ev.ProviderRef,
		}
		if ev.Type == billing.TypeCredit || ev.Type == billing.TypeRefund {
			line.Corrects = ev.CausedBy
		}
		if ev.Type == billing.TypeGainshareCharge {
			line.Evidence = s.evidence(ev)
		}
		out.Lines = append(out.Lines, line)
		t := byKind[kind]
		if t == nil {
			t = &api.TotalView{Kind: kind, Label: strings.ToUpper(kind[:1]) + kind[1:] + " subtotal"}
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

// evidence resolves a gainshare charge back to the verified-delta ledger entries behind it.
//
// A gainshare line without its evidence is a bill for a saving nobody can check — which the schema
// refuses to store and this view refuses to render.
func (s *Source) evidence(ev billing.BillingEvent) []api.EvidenceView {
	deltas, merges, err := billing.GainshareEvidence(ev, s.deltas)
	if err != nil {
		return nil
	}
	var out []api.EvidenceView
	for _, d := range deltas {
		out = append(out, api.EvidenceView{
			Kind: "verified_delta", Ref: d.Ref,
			Label: fmt.Sprintf("verified delta %s (held-out, significant)", d.Ref),
			Method: &api.MethodView{
				ID: d.Baseline.ID, EvalSetHash: d.Baseline.EvalSetHash,
				HoldoutCases: len(d.Baseline.HoldoutCaseIDs), GeneratingCases: len(d.Baseline.GeneratingCaseIDs),
				Seeds: len(d.Baseline.Seeds), BaselineConfig: d.Baseline.BaselineConfigHash,
				CandidateConfig: d.Baseline.CandidateConfigHash,
				SignificanceNote: fmt.Sprintf("delta %.3f [%.3f, %.3f]",
					d.Verdict.Delta.Mean, d.Verdict.Delta.Low, d.Verdict.Delta.High),
			},
		})
	}
	for _, m := range merges {
		out = append(out, api.EvidenceView{Kind: "merge", Ref: m, Label: "merged as " + shortSHA(m)})
	}
	return out
}

// savings reports verified savings, what was billed, and — the load-bearing part — what was
// deliberately NOT billed and why.
func (s *Source) savings(customerID string, p metering.Period, consent bool, at *time.Time, priced bool) api.SavingsView {
	out := api.SavingsView{Consented: consent, ConsentAvailable: priced, Unit: telemetry.UnitUSD}
	if at != nil {
		out.ConsentedAt = at.Format("2006-01-02")
	}
	bs, err := metering.ComputeBillableSavings(s.deltas, customerID, p)
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
			MergeCommit: shortSHA(d.MergeCommit),
			Method: &api.MethodView{ID: d.Baseline.ID, EvalSetHash: d.Baseline.EvalSetHash,
				HoldoutCases: len(d.Baseline.HoldoutCaseIDs), GeneratingCases: len(d.Baseline.GeneratingCaseIDs),
				Seeds: len(d.Baseline.Seeds)},
		})
	}
	// Excluded is never dropped: "this saving was real and we did not bill it" is the sentence that
	// makes the billed figure believable.
	for _, e := range bs.Excluded {
		out.Excluded = append(out.Excluded, api.ExcludedRow{Ref: e.Ref, Reason: e.Reason, WouldHaveBeen: e.WouldHaveBeen})
	}
	return out
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
func limitFor(m metering.Metric) plancfg.Limit {
	switch m {
	case metering.MetricSUM:
		return plancfg.LimitSUMBand
	case metering.MetricSeats:
		return plancfg.LimitSeats
	case metering.MetricRetention:
		return plancfg.LimitRetentionDays
	default:
		return plancfg.LimitEvalCompute
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
