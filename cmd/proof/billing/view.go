package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// view.go builds the api.BillingSource read model over the wired stack, and prints the headless report.

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
		PlanID: plan.PlanID, PlanName: plan.DisplayName, PlanConfigVersion: plan.Version,
		SUMUnit: telemetry.UnitUSD,
	}

	recs, _ := s.usage.Period(customer, p.ID)
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

	var denial *entitlement.Decision
	for _, surface := range entitlement.Surfaces {
		d, err := surface.Check(s.gate, customer)
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
		v.Entitlements = append(v.Entitlements, row)
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
	if res, err := s.svc.Reconcile(ctx, customer, p, s.observer); err == nil {
		for _, d := range res.Drift {
			v.Drift = append(v.Drift, api.DriftView{
				Kind: string(d.Kind), Metric: string(d.Metric), Detail: d.Detail,
				PlatformQuantity: d.PlatformQuantity, ProviderQuantity: d.ProviderQuantity,
			})
		}
	}
	v.Savings = s.savingsView(customer, p, acct.GainshareConsent, acct.ConsentedAt, plan.PriceRefs["gainshare"] != "")
	return v, true
}

func (s *state) invoiceView(ctx context.Context, customer string, p metering.Period) api.InvoiceView {
	out := api.InvoiceView{}
	if inv, err := s.provider.Invoice(ctx, customer, p.ID); err == nil {
		if verr := inv.Validate(); verr != nil {
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
			line.Evidence = s.evidenceView(ev)
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

// evidenceView resolves a gainshare charge back to the P5.5 ledger AND to the real git commit. The
// merge link is what makes "traces to its evidence" checkable with `git show` rather than by trust.
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
		out = append(out, api.EvidenceView{Kind: "merge", Ref: m,
			Label: fmt.Sprintf("merged into %s as %s", workflowID, shortSHA(m))})
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
			MergeCommit: shortSHA(d.MergeCommit),
			Method: &api.MethodView{ID: d.Baseline.ID, EvalSetHash: d.Baseline.EvalSetHash,
				HoldoutCases: len(d.Baseline.HoldoutCaseIDs), GeneratingCases: len(d.Baseline.GeneratingCaseIDs),
				Seeds: len(d.Baseline.Seeds)},
		})
	}
	for _, e := range bs.Excluded {
		out.Excluded = append(out.Excluded, api.ExcludedRow{Ref: e.Ref, Reason: e.Reason, WouldHaveBeen: e.WouldHaveBeen})
	}
	return out
}

// ── the headless report ──────────────────────────────────────────────────────

// report prints the whole run: what the loop merged into the real repo, what the entitlement gate
// refused, what the meter derived, what was billed, and — the load-bearing part — what was deliberately
// NOT billed and how much it was worth.
func (s *state) report() {
	last := periods[len(periods)-1]
	line := strings.Repeat("─", 78)

	fmt.Printf("\n%s\nP7 · %s\n%s\n", line, workflowID, line)
	fmt.Printf("checkout        %s\n", s.repoDir)
	fmt.Printf("plan            %s\n", *planID)
	fmt.Printf("rollout         %s\n", s.rollout)
	fmt.Printf("loop state      %s\n", s.loopState)

	fmt.Printf("\n%s\nP6 — WHAT THE LOOP DID TO THE REAL REPOSITORY\n%s\n", line, line)
	if len(s.merges) == 0 {
		fmt.Println("  (nothing merged)")
	}
	for _, m := range s.merges {
		fmt.Printf("  MERGED  %s\n          node %s · operator %s · verified delta %+.3f\n          %s\n",
			shortSHA(m.MergeCommit), m.Node, m.Operator, m.VerifiedDelta, m.PRRef)
	}
	for _, d := range s.mergeDenials {
		fmt.Printf("  GATED   entitlement denied a merge — %s\n", d.Summary)
	}
	for _, n := range s.clobbered {
		fmt.Printf("  STALE   %s was merged but a later merge overwrote it — the merge is a real git\n"+
			"          fact, the saving is NOT in effect, so it bills nothing\n", n)
	}

	fmt.Printf("\n%s\nMETERING — SUM DERIVED FROM THE P2.5 COST EVENTS\n%s\n", line, line)
	for _, p := range periods {
		rec, err := s.usage.Get(metering.Key{CustomerID: customerID, Period: p.ID, Metric: metering.MetricSUM})
		if err != nil {
			continue
		}
		mark := ""
		if p.ID == last.ID && len(s.merges) > 0 {
			mark = "   <- the merged optimizations are live"
		}
		fmt.Printf("  %s   SUM %9.2f %s   reported=%v%s\n", p.ID, rec.Quantity, telemetry.UnitUSD,
			rec.ReportedToProvider, mark)
	}
	// Determinism, stated as a fact rather than a claim.
	a, _ := s.meter.DeriveSUM(customerID, last)
	b, _ := s.meter.DeriveSUM(customerID, last)
	fmt.Printf("  re-derivation of the closed period: %.2f == %.2f, digest %s\n",
		a.Quantity, b.Quantity, a.SourceDigest[:16])

	fmt.Printf("\n%s\nENTITLEMENTS — PLAN × AUTOMATION LEVEL\n%s\n", line, line)
	for _, surface := range entitlement.Surfaces {
		d, err := surface.Check(s.gate, customerID)
		if err != nil {
			continue
		}
		if d.Allowed {
			fmt.Printf("  ✓ %-34s included\n", surfaceLabel(surface.Feature))
			continue
		}
		up := ""
		if d.UpgradePlanName != "" {
			up = "  → upgrade to " + d.UpgradePlanName
		}
		fmt.Printf("  ⃠ %-34s %s%s\n", surfaceLabel(surface.Feature), d.Reason, up)
	}

	fmt.Printf("\n%s\nINVOICE — %s\n%s\n", line, last.ID, line)
	inv := s.invoiceView(context.Background(), customerID, last)
	for _, l := range inv.Lines {
		fmt.Printf("  %-13s %-42s qty %9.2f  %s\n", strings.ToUpper(l.Kind), l.Basis, l.Quantity, l.AmountRef)
		for _, e := range l.Evidence {
			fmt.Printf("                  ↳ %s\n", e.Label)
		}
	}
	if len(inv.Lines) == 0 {
		fmt.Println("  (no charges — the billing feature flag is off)")
	}

	fmt.Printf("\n%s\nGAINSHARE — VERIFIED, MERGED SAVINGS ONLY\n%s\n", line, line)
	bs, err := metering.ComputeBillableSavings(s.deltas, customerID, last)
	if err != nil {
		fmt.Printf("  compute: %v\n", err)
		return
	}
	fmt.Printf("  BILLED   baseline %.2f − optimized %.2f = %.2f %s\n",
		bs.BaselineSUM, bs.OptimizedSUM, bs.Savings, telemetry.UnitUSD)
	for i, ref := range bs.VerifiedDeltaRefs {
		d, _ := s.deltas.ByRef(ref)
		fmt.Printf("           %s  saves %.2f  merge %s  (%s, %d held-out cases, %d seeds)\n",
			ref, d.Savings(), shortSHA(bs.MergeCommits[i]), d.Baseline.ID,
			len(d.Baseline.HoldoutCaseIDs), len(d.Baseline.Seeds))
	}
	var declined float64
	for _, e := range bs.Excluded {
		declined += e.WouldHaveBeen
	}
	fmt.Printf("\n  NOT BILLED — %.2f %s of savings the platform declined to charge for:\n", declined, telemetry.UnitUSD)
	stale := map[string]bool{}
	for _, n := range s.clobbered {
		stale["vd:"+n] = true
	}
	for _, e := range bs.Excluded {
		reason := e.Reason
		if stale[e.Ref] {
			// The generic ledger reason ("never merged") is the right BILLING outcome but the wrong
			// story for this one: it was merged, and then a later merge overwrote it. Saying so is the
			// difference between an explanation and a shrug.
			reason = "merged, then overwritten by a later merge — the saving is not in effect"
		}
		fmt.Printf("           %-46s %8.2f   %s\n", e.Ref, e.WouldHaveBeen, reason)
	}

	fmt.Printf("\n%s\nALERTS\n%s\n", line, line)
	if len(s.alerts.Alerts()) == 0 {
		fmt.Println("  (none)")
	}
	for _, a := range s.alerts.Alerts() {
		fmt.Printf("  %-24s %s/%s  qty %+.2f  %s\n", a.Kind, a.CustomerID, a.Period, a.Quantity, a.Detail)
	}

	fmt.Printf("\n%s\ngit log — the loop's real merges in %s\n%s\n", line, s.repoDir, line)
	cmd := exec.Command("git", "log", "--oneline", "-10")
	cmd.Dir = s.repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("  git log: %v\n", err)
		return
	}
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		fmt.Printf("  %s\n", l)
	}
	fmt.Println()
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ── labels ───────────────────────────────────────────────────────────────────

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
