package adminops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/metering"
)

// billing.go is the Billing-Ops oversight surface (FR9): invoices, dunning, reconciliation, additive
// credits/refunds, and gainshare oversight.
//
// # Why a credit here is P7's credit, not a second one
//
// Every correction routes through billing.Service.Credit / .Refund, which append a NEW audited
// billing event and leave the original charge, its usage record and the provider's invoice intact.
// P8 adds the operator gate and the admin audit entry on top; it does not add a second way to move
// money. A console with its own correction path would be a second ledger, and the two would disagree
// exactly when it mattered.
//
// # Why an unevidenced gainshare charge is an EXCEPTION rather than a hidden row
//
// Gainshare bills a share of savings the platform claims to have produced. The claim is only as good
// as its evidence — verified deltas and the merge commits behind them. A charge whose evidence no
// longer resolves is not "probably fine": it is the exact shape of a billing error the customer will
// find first. So the oversight view surfaces it as an exception, loudly, rather than rendering it
// alongside the valid ones.

// InvoiceLine is one billing event as the operator sees it. Quantities and provider REFERENCES, never
// amounts — the amount lives with the PCI-compliant provider and the console never holds one.
type InvoiceLine struct {
	EventID string `json:"event_id"`
	Period  string `json:"period"`
	Type    string `json:"type"`
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status"`
	// Quantity is the metered quantity the event carries (a SUM quantity, a seat count). It is a
	// measured quantity, not money.
	Quantity float64 `json:"quantity"`
	// ProviderRef is the opaque provider handle for the recorded charge, empty until settled.
	ProviderRef string `json:"provider_ref,omitempty"`
	// CausedBy names the platform record that justified the row, which is what makes a period
	// reconstructable without the provider.
	CausedBy  string `json:"caused_by"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt string `json:"created_at"`
}

// DriftLine is one reconciliation disagreement.
type DriftLine struct {
	Kind             string  `json:"kind"`
	Metric           string  `json:"metric"`
	Period           string  `json:"period"`
	PlatformQuantity float64 `json:"platform_quantity"`
	ProviderQuantity float64 `json:"provider_quantity"`
	Detail           string  `json:"detail"`
}

// GainshareLine is one gainshare charge with the evidence behind it — or the exception that says the
// evidence does not hold.
type GainshareLine struct {
	EventID string `json:"event_id"`
	Period  string `json:"period"`
	// VerifiedDeltaRefs and MergeCommits are the evidence: a billed saving traces to entries in the
	// P5.5 verified-delta ledger and to git merge commits that actually exist.
	VerifiedDeltaRefs []string `json:"verified_delta_refs,omitempty"`
	MergeCommits      []string `json:"merge_commits,omitempty"`
	// Exception is non-empty when the charge does NOT trace to verified, merged evidence. A charge
	// with an exception is not shown as valid.
	Exception string `json:"exception,omitempty"`
}

// Valid reports whether the charge traces to verified, merged evidence.
func (g GainshareLine) Valid() bool { return g.Exception == "" }

// BillingOversight is the Billing-Ops read model for one tenant-period.
type BillingOversight struct {
	TenantID string `json:"tenant_id"`
	Period   string `json:"period"`
	// Invoices is every recorded billing event for the period.
	Invoices []InvoiceLine `json:"invoices"`
	// Dunning is the events still awaiting provider confirmation — the buffer a provider outage fills
	// and the queue a failed payment sits in. Named separately because "pending" and "recorded" mean
	// different things to an operator answering a customer.
	Dunning []InvoiceLine `json:"dunning"`
	// ReconciliationMatched and Drift are the platform-vs-provider comparison. Drift is surfaced, never
	// reconciled by overwrite.
	ReconciliationMatched bool        `json:"reconciliation_matched"`
	Drift                 []DriftLine `json:"drift,omitempty"`
	// ReconciliationDegraded is set when the comparison could not be made (the provider is
	// unreachable). It is distinct from "matched: false" — a transport failure is not absence of drift
	// (FR26), and rendering it as "matched" would be the console asserting something it does not know.
	ReconciliationDegraded bool   `json:"reconciliation_degraded"`
	ReconciliationDetail   string `json:"reconciliation_detail,omitempty"`
	// Gainshare is the period's gainshare charges with their evidence.
	Gainshare []GainshareLine `json:"gainshare,omitempty"`
	// Exceptions counts the gainshare charges whose evidence does not hold — the number an operator
	// triages first.
	Exceptions int `json:"exceptions"`
	// PlanHistory is every audited plan change for this tenant, newest first, across ALL periods.
	//
	// 🔴 Deliberately not period-scoped, and that is the whole reason it exists. A plan change is not a
	// periodic event — the ledger writes it with no period at all — so the period-filtered invoice list
	// above can never contain one. Before this field, an operator asking the most ordinary question
	// there is ("why is this tenant on Enterprise, and who moved them there?") had no surface that
	// answered it: the audited trail P7 is careful to keep was visible nowhere in the console.
	PlanHistory []PlanChangeLine `json:"plan_history,omitempty"`
}

// PlanChangeLine is one audited move between plans.
//
// It carries no quantity and no provider reference because a plan change is neither a charge nor a
// measurement: it is a statement that entitlement moved, when, and on the strength of what.
type PlanChangeLine struct {
	EventID string `json:"event_id"`
	// Status is the ledger status, carried rather than assumed. A plan change is recorded, not settled,
	// so anything else here is worth an operator's attention.
	Status string `json:"status"`
	// CausedBy names the platform record that justified the move — a subscription lifecycle event, an
	// operator override — which is what makes the trail auditable without asking the provider.
	CausedBy string `json:"caused_by"`
	// Reason is the human sentence the ledger stored, e.g. "invoice paid: plan free -> Team".
	Reason    string `json:"reason,omitempty"`
	CreatedAt string `json:"created_at"`
}

// BillingService is the operator's billing oversight and correction surface.
type BillingService struct {
	exec    *Executor
	billing *billing.Service
	deltas  metering.VerifiedDeltaLedger
}

// NewBillingService wires the service. The verified-delta ledger may be nil in a deployment with no
// gainshare; the oversight view then says so by returning no gainshare lines rather than by claiming
// there are none.
func NewBillingService(exec *Executor, svc *billing.Service, deltas metering.VerifiedDeltaLedger) (*BillingService, error) {
	if exec == nil || svc == nil {
		return nil, errors.New("adminops: the billing oversight service needs the command path and the P7 billing service")
	}
	return &BillingService{exec: exec, billing: svc, deltas: deltas}, nil
}

// collectingAlerts captures reconciliation alerts instead of paging somebody. The console shows drift
// on screen; the paging sink stays wired where it belongs, in the P2.5 alerting path — a read of the
// oversight page must not raise a page.
type collectingAlerts struct{ results []metering.ReconcileResult }

// DriftDetected implements metering.AlertSink.
func (c *collectingAlerts) DriftDetected(res metering.ReconcileResult) {
	c.results = append(c.results, res)
}

// Oversight returns the Billing-Ops read model for one tenant-period. Permission-gated to
// Billing-Ops and above; Support is denied.
func (s *BillingService) Oversight(ctx context.Context, tenantID string, period metering.Period) (BillingOversight, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapBillingRead, TenantTarget(tenantID)); err != nil {
		return BillingOversight{}, err
	}
	out := BillingOversight{TenantID: tenantID, Period: period.ID}

	for _, ev := range s.billing.Ledger().Events(tenantID, period.ID) {
		line := InvoiceLine{
			EventID: ev.EventID, Period: ev.Period, Type: string(ev.Type), Kind: string(ev.Kind),
			Status: string(ev.Status), Quantity: ev.Quantity, ProviderRef: ev.ProviderRef,
			CausedBy: ev.CausedBy, Reason: ev.Reason, CreatedAt: ev.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		out.Invoices = append(out.Invoices, line)
		if ev.Status == billing.StatusPending {
			out.Dunning = append(out.Dunning, line)
		}
		if ev.Type == billing.TypeGainshareCharge {
			out.Gainshare = append(out.Gainshare, s.gainshareLine(ev))
		}
	}
	for _, g := range out.Gainshare {
		if !g.Valid() {
			out.Exceptions++
		}
	}

	// The plan trail, read with an EMPTY period so the ledger's period filter does not drop it. Newest
	// first: an operator opening this page is answering a question about now, and reads downwards into
	// history.
	for _, ev := range s.billing.Ledger().Events(tenantID, "") {
		if ev.Type != billing.TypePlanChange {
			continue
		}
		out.PlanHistory = append([]PlanChangeLine{{
			EventID:   ev.EventID,
			Status:    string(ev.Status),
			CausedBy:  ev.CausedBy,
			Reason:    ev.Reason,
			CreatedAt: ev.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}}, out.PlanHistory...)
	}

	alerts := &collectingAlerts{}
	res, err := s.billing.Reconcile(ctx, tenantID, period, alerts)
	if err != nil {
		// DEGRADED, not "matched". The provider being unreachable tells us nothing about drift, and
		// saying "matched" would be the console asserting a fact it does not have.
		out.ReconciliationDegraded = true
		out.ReconciliationDetail = err.Error()
		return out, nil
	}
	out.ReconciliationMatched = res.Matched
	for _, d := range res.Drift {
		out.Drift = append(out.Drift, DriftLine{
			Kind: string(d.Kind), Metric: string(d.Metric), Period: d.Period,
			PlatformQuantity: d.PlatformQuantity, ProviderQuantity: d.ProviderQuantity, Detail: d.Detail,
		})
	}
	return out, nil
}

// gainshareLine resolves one gainshare charge back to its evidence, or reports why it cannot.
func (s *BillingService) gainshareLine(ev billing.BillingEvent) GainshareLine {
	line := GainshareLine{EventID: ev.EventID, Period: ev.Period}
	if s.deltas == nil {
		line.Exception = "no verified-delta ledger is wired — this charge's evidence cannot be checked"
		return line
	}
	deltas, merges, err := billing.GainshareEvidence(ev, s.deltas)
	if err != nil {
		line.Exception = err.Error()
		return line
	}
	for _, d := range deltas {
		line.VerifiedDeltaRefs = append(line.VerifiedDeltaRefs, d.Ref)
		if !d.Merged {
			line.Exception = fmt.Sprintf("verified delta %s is not merged — an unmerged saving has not happened", d.Ref)
		}
		if d.Estimated {
			line.Exception = fmt.Sprintf("verified delta %s is an estimate, which is structurally unbillable", d.Ref)
		}
	}
	line.MergeCommits = merges
	if len(merges) == 0 && line.Exception == "" {
		line.Exception = "this charge names no merge commit — there is no git fact behind the billed saving"
	}
	return line
}

// IssueCredit issues an additive, audited credit against a prior charge (FR9).
func (s *BillingService) IssueCredit(ctx context.Context, tenantID, againstEventID, reason string, confirm Confirmation) (Receipt, error) {
	return s.correct(ctx, tenantID, againstEventID, reason, confirm, adminaudit.ActionBillingCredit, s.billing.Credit)
}

// IssueRefund issues an additive, audited refund against a prior charge (FR9).
func (s *BillingService) IssueRefund(ctx context.Context, tenantID, againstEventID, reason string, confirm Confirmation) (Receipt, error) {
	return s.correct(ctx, tenantID, againstEventID, reason, confirm, adminaudit.ActionBillingRefund, s.billing.Refund)
}

type correctionFn func(ctx context.Context, customerID, againstEventID, reason string) (billing.BillingEvent, error)

func (s *BillingService) correct(ctx context.Context, tenantID, againstEventID, reason string,
	confirm Confirmation, action adminaudit.Action, apply correctionFn) (Receipt, error) {

	if strings.TrimSpace(againstEventID) == "" {
		return Receipt{}, errors.New("adminops: a correction must name the charge it corrects")
	}
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapBillingCorrect,
		Action:     action,
		Target:     TenantTarget(tenantID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{tenantID, againstEventID},
		Evidence:   map[string]string{"tenant_id": tenantID, "against_event_id": againstEventID},
	}, func(ctx context.Context) (map[string]string, error) {
		ev, err := apply(ctx, tenantID, againstEventID, reason)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"billing_event_id": ev.EventID, "billing_event_type": string(ev.Type),
			"caused_by": ev.CausedBy, "idempotency_key": ev.IdempotencyKey,
		}, nil
	})
}
