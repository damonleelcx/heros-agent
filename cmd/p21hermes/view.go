package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// view.go builds the two read models the console reads: the P7 billing view and the P21 payment view.
//
// 🔴 Every figure here is READ from the meter, the plan configuration, or the provider. Nothing is
// computed for display: the console renders what the server was told, and the server was told by the
// systems that own each fact. That is the whole reason the page can carry no currency literal — there
// is no arithmetic anywhere between the meter and the screen.

func encodeJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
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
	if _, err := s.accounts.SetGainshareConsent(customer, consented, periods[0].Start); err != nil {
		return api.BillingView{}, err
	}
	v, _ := s.Billing(customer, "")
	return v, nil
}

func (s *state) Billing(customer, periodID string) (api.BillingView, bool) {
	if customer != customerID {
		return api.BillingView{}, false
	}
	p := periods[len(periods)-1]
	for _, cand := range periods {
		if cand.ID == periodID {
			p = cand
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
		SUMUnit: "USD", PlanID: plan.PlanID, PlanName: plan.DisplayName,
		PlanConfigVersion: acct.PlanConfigVersion,
	}

	// SUM and its trend, read from the usage store.
	if rec, err := s.usage.Get(metering.Key{CustomerID: customer, Period: p.ID, Metric: metering.MetricSUM}); err == nil {
		v.SUM = rec.Quantity
	} else {
		// 🔴 An absent record is EMPTY, not zero. They look identical as a number and mean opposite
		// things — "nothing has been recorded" and "we measured nothing".
		v.Empty = true
	}
	for _, cand := range periods {
		rec, err := s.usage.Get(metering.Key{CustomerID: customer, Period: cand.ID, Metric: metering.MetricSUM})
		if err != nil {
			continue
		}
		point := api.PeriodPoint{Period: cand.ID, SUM: rec.Quantity}
		for _, n := range s.nodes {
			point.Baseline += n.baselineSpend
			if n.merged {
				point.Optimized += n.optimizedSpend
			} else {
				point.Optimized += n.baselineSpend
			}
		}
		v.SUMTrend = append(v.SUMTrend, point)
	}

	// Meters against the plan's allowances. An UNSET allowance is UNLIMITED, never zero.
	for _, m := range metering.Metrics {
		rec, err := s.usage.Get(metering.Key{CustomerID: customer, Period: p.ID, Metric: m})
		if err != nil {
			continue
		}
		row := api.MeterView{
			Metric: string(m), Label: meterLabel(m), Value: rec.Quantity, Unit: meterUnit(m),
			ReportedToProvider: rec.ReportedToProvider, ProviderUsageRef: rec.ProviderUsageRef,
		}
		if allowed, ok := plan.Limit(limitFor(m)); ok {
			row.Allowed, row.Over = allowed, rec.Quantity > allowed
		} else {
			row.Unlimited = true
		}
		v.Meters = append(v.Meters, row)
	}

	// Entitlements: the same rows the gate reads.
	for _, f := range plancfg.Features {
		d, err := s.gate.CheckEntitlement(customer, f, entitlement.LevelAssisted)
		if err != nil {
			continue
		}
		row := api.EntitlementRow{Feature: string(f), Label: featureLabel(f), Included: d.Allowed}
		if !d.Allowed {
			row.Reason, row.UpgradePlan, row.UpgradePlanName = d.Reason, d.UpgradePlan, d.UpgradePlanName
		}
		v.Entitlements = append(v.Entitlements, row)
	}

	// The invoice, read back from the PROVIDER and validated on the way in.
	if inv, err := s.provider.Invoice(context.Background(), customer, p.ID); err == nil {
		v.Invoice = api.InvoiceView{InvoiceRef: inv.InvoiceRef, Status: inv.Status}
		totals := map[string]*api.TotalView{}
		for _, l := range inv.Lines {
			line := api.LineView{
				Kind: string(l.Kind), Label: string(l.Kind), Basis: l.Basis,
				AmountRef: l.AmountRef, Quantity: l.Quantity, ChargeRef: l.ChargeRef,
			}
			if l.Kind == billing.LineGainshare {
				// A gainshare line carries its EVIDENCE or it is a defect. The evidence is the merge the
				// saving attributes to, in the REAL repository.
				line.Evidence = s.gainshareEvidence()
			}
			v.Invoice.Lines = append(v.Invoice.Lines, line)
			t, ok := totals[string(l.Kind)]
			if !ok {
				t = &api.TotalView{Kind: string(l.Kind), Label: string(l.Kind)}
				totals[string(l.Kind)] = t
			}
			t.Lines++
			t.Quantity += l.Quantity
		}
		for _, t := range totals {
			v.Invoice.Totals = append(v.Invoice.Totals, *t)
		}
		sort.Slice(v.Invoice.Totals, func(i, j int) bool { return v.Invoice.Totals[i].Kind < v.Invoice.Totals[j].Kind })
	}

	// The provider's state, MIRRORED. Never computed here.
	st := s.svc.BillingState(customer)
	v.State = api.BillingStateView{
		InvoiceStatus: st.InvoiceStatus, SubscriptionStatus: st.SubscriptionStatus,
		PaymentFailed: st.PaymentFailed, PastDue: st.PastDue,
	}
	if st.PaymentFailed || st.PastDue {
		v.State.Guidance = "The payment provider could not take payment on the card on file. Update the payment method to restore the plan's features; the provider retries on its own schedule."
	}

	// Savings: what was billed, and — the part that makes the claim checkable — what was NOT.
	v.Savings = api.SavingsView{
		Consented: acct.GainshareConsent, ConsentAvailable: plan.PriceRefs["gainshare"] != "", Unit: "USD",
	}
	for _, n := range s.nodes {
		v.Savings.BaselineSUM += n.baselineSpend
		if n.merged {
			v.Savings.OptimizedSUM += n.optimizedSpend
			v.Savings.Verified += n.baselineSpend - n.optimizedSpend
			v.Savings.Billed = append(v.Savings.Billed, api.SavingRow{
				Ref: "vd:" + n.nodeID, BaselineSUM: n.baselineSpend, OptimizedSUM: n.optimizedSpend,
				Savings: n.baselineSpend - n.optimizedSpend, MergeCommit: s.headSHA,
			})
		} else {
			v.Savings.OptimizedSUM += n.baselineSpend
			v.Savings.Excluded = append(v.Savings.Excluded, api.ExcludedRow{
				Ref:    "vd:" + n.nodeID,
				Reason: "verified but NOT merged — a saving that is not in effect bills nothing",
				// Shown so the customer can see the size of what the platform declined to bill.
				WouldHaveBeen: n.baselineSpend - n.optimizedSpend,
			})
		}
	}
	v.Savings.NoneVerified = len(v.Savings.Billed) == 0
	return v, true
}

func (s *state) gainshareEvidence() []api.EvidenceView {
	out := make([]api.EvidenceView, 0, len(s.nodes))
	for _, n := range s.nodes {
		if !n.merged {
			continue
		}
		out = append(out, api.EvidenceView{
			Kind: "merge", Ref: s.headSHA,
			Label: fmt.Sprintf("%s in %s", n.symbol, n.file),
			Link:  "https://github.com/" + workflowID + "/commit/" + s.headSHA,
		})
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// api.PaymentsSource
// ─────────────────────────────────────────────────────────────────────────────

func (s *state) Payment(customer, periodID string) (api.PaymentView, bool) {
	b, ok := s.Billing(customer, periodID)
	if !ok {
		return api.PaymentView{}, false
	}
	v := api.PaymentView{Billing: b, CollectionAvailable: s.svc.CollectionAvailable()}

	for _, opt := range s.svc.PlanOptions(customer) {
		v.Plans = append(v.Plans, api.PlanOptionView{
			PlanID: opt.PlanID, Name: opt.Name, Rank: opt.Rank, Current: opt.Current,
			Direction: opt.Direction, Subscribable: opt.Subscribable,
		})
	}

	st := s.svc.BillingState(customer)
	v.PaymentMethod = api.PaymentMethodView{
		Present: st.PaymentMethodPresent, Brand: st.PaymentMethodBrand, Last4: st.PaymentMethodLast4,
	}
	switch {
	case st.PaymentFailed:
		// The provider's word, and a path back. A dunning state with no next step is an alarm.
		v.PaymentMethod.Status = "payment_failed"
		v.PaymentMethod.Reason = "The payment provider could not take payment on the card on file."
		v.PaymentMethod.RestorePath = "Add or update the payment method below. The provider retries on its own schedule, and a working card ends the retries."
	case st.PastDue:
		v.PaymentMethod.Status = "past_due"
		v.PaymentMethod.Reason = "The payment provider has this subscription past due while it retries payment."
		v.PaymentMethod.RestorePath = "Update the payment method below to end the retries before the grace window closes."
	case st.PaymentMethodPresent:
		v.PaymentMethod.Status = "ok"
	}
	return v, true
}

func (s *state) StartCheckout(ctx context.Context, customer, planName, successURL, cancelURL string) (api.CheckoutView, error) {
	session, err := s.svc.StartCheckout(ctx, customer, planName, successURL, cancelURL)
	if err != nil {
		return api.CheckoutView{}, err
	}
	out := api.CheckoutView{URL: session.URL, ClientSecret: session.ClientSecret, SessionRef: session.SessionRef}
	if !session.ExpiresAt.IsZero() {
		out.ExpiresAt = session.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return out, nil
}

func (s *state) ChangePlan(ctx context.Context, customer, planName string) (api.PlanChangeView, error) {
	res, err := s.svc.ChangePlan(ctx, customer, planName)
	if err != nil {
		return api.PlanChangeView{}, err
	}
	return api.PlanChangeView{
		PlanID: res.PlanID, PlanName: res.PlanName, Status: res.Status,
		Changed: res.Changed, CheckoutRequired: res.CheckoutRequired,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Labels — the console renders what the server names, so the names live here.
// ─────────────────────────────────────────────────────────────────────────────

// featureLabel names a gated surface for a reader. The gate names the plan and the reason; the surface's
// own label is the console's vocabulary, so it lives beside the meter labels rather than being inferred
// from the identifier.
func featureLabel(f plancfg.Feature) string {
	switch f {
	case plancfg.FeatureCLI:
		return "Command-line client"
	case plancfg.FeatureDiscovery:
		return "Repository discovery"
	case plancfg.FeatureAssistedPR:
		return "Verified optimization pull requests"
	case plancfg.FeatureDashboard:
		return "Web console"
	case plancfg.FeatureAutoMerge:
		return "Autonomous auto-merge"
	}
	return string(f)
}

func meterLabel(m metering.Metric) string {
	switch m {
	case metering.MetricSUM:
		return "Spend under management"
	case metering.MetricSeats:
		return "Seats"
	case metering.MetricRetention:
		return "Retention"
	case metering.MetricEvalCompute:
		return "Eval compute"
	}
	return strings.ToUpper(string(m))
}

func meterUnit(m metering.Metric) string {
	switch m {
	case metering.MetricSUM:
		return "USD"
	case metering.MetricRetention:
		return "days"
	case metering.MetricEvalCompute:
		return "units"
	}
	return "count"
}

func limitFor(m metering.Metric) plancfg.Limit {
	switch m {
	case metering.MetricSUM:
		return plancfg.LimitSUMBand
	case metering.MetricSeats:
		return plancfg.LimitSeats
	case metering.MetricRetention:
		return plancfg.LimitRetentionDays
	}
	return plancfg.LimitEvalCompute
}
