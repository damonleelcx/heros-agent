package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/metering"
)

// correction.go is the ONLY way a billing error is fixed (task 4.3 / design Decision 6 / FR11).
//
// A correction is a NEW, audited `billing_event`. The underlying usage record, the original charge, and
// the provider's invoice all stay INTACT. Nothing in this file can delete or revise a prior row —
// the ledger's append-only contract and the Postgres trigger both refuse it, and neither is the only
// guard.
//
// ## Why additive rather than "fix the wrong row"
//
// Reversibility must not depend on anyone remembering what they mutated. An additive-only ledger means
// recovery is a FORWARD operation (issue a credit), every past state is reconstructable, and an auditor
// can replay the ledger to any period and get the number the customer actually saw. Editing the wrong
// charge would make the mistake — and the fact that there ever was one — disappear, which is precisely
// what an auditor needs to see.
//
// This is the same discipline P6 applies to a bad merge: the correct way to undo is to record a
// compensating action, never to erase history.

// Credit issues an additive balance credit against a prior charge.
//
// It requires the ORIGINAL charge's event id and a reason. Both are load-bearing: a credit that names
// no charge cannot be reconciled against anything, and a credit that explains nothing is
// indistinguishable from a mistake six months later.
func (s *Service) Credit(ctx context.Context, customerID, againstEventID, reason string) (BillingEvent, error) {
	return s.correct(ctx, customerID, againstEventID, reason, TypeCredit)
}

// Refund issues an additive money-back refund against a prior charge. Same shape as Credit; the
// provider decides what "refund" means for the payment instrument.
func (s *Service) Refund(ctx context.Context, customerID, againstEventID, reason string) (BillingEvent, error) {
	return s.correct(ctx, customerID, againstEventID, reason, TypeRefund)
}

func (s *Service) correct(ctx context.Context, customerID, againstEventID, reason string, typ EventType) (BillingEvent, error) {
	if strings.TrimSpace(reason) == "" {
		return BillingEvent{}, ErrNoReason
	}
	if strings.TrimSpace(againstEventID) == "" {
		return BillingEvent{}, errors.New("billing: a correction must name the charge it corrects")
	}
	acct, _, err := s.resolve(customerID)
	if err != nil {
		return BillingEvent{}, err
	}

	original, err := s.findEvent(customerID, againstEventID)
	if err != nil {
		return BillingEvent{}, err
	}
	if !original.Type.ChargeBearing() {
		return BillingEvent{}, fmt.Errorf("billing: %s is a %s, not a charge — there is nothing to correct", againstEventID, original.Type)
	}
	if original.Status != StatusRecorded || original.ProviderRef == "" {
		return BillingEvent{}, fmt.Errorf("billing: %s was never settled with the provider — an unsettled charge is "+
			"resolved by not settling it, not by crediting it", againstEventID)
	}

	key := CorrectionIdempotencyKey(typ, customerID, againstEventID, reason)

	// Write-ahead, exactly as a charge does: the correction is durable before the provider is asked, so
	// an ambiguous failure leaves a resumable record rather than an invisible maybe-credit.
	row, err := s.ledger.Append(BillingEvent{
		CustomerID: customerID, Period: original.Period, Type: typ,
		IdempotencyKey: key,
		CausedBy:       "billing_event:" + original.EventID,
		Reason:         reason,
		Quantity:       original.Quantity,
		Status:         StatusPending,
		CreatedAt:      s.now().UTC(),
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrDuplicateKey):
		if row.Status == StatusRecorded {
			return row, nil // this exact correction was already issued
		}
	default:
		return BillingEvent{}, fmt.Errorf("billing: write-ahead correction: %w", err)
	}

	res, perr := s.provider.IssueCredit(ctx, CreditRequest{
		ProviderCustomerHandle: acct.ProviderCustomerHandle,
		AgainstRef:             original.ProviderRef,
		Refund:                 typ == TypeRefund,
		Reason:                 reason,
		IdempotencyKey:         key,
	})
	if perr != nil {
		return row, fmt.Errorf("billing: correction buffered, provider call failed: %w", perr)
	}
	settled, err := s.ledger.Settle(key, res.CreditRef, res.AmountRef, s.now())
	if err != nil && !errors.Is(err, ErrAlreadySettled) {
		return row, fmt.Errorf("billing: settle correction: %w", err)
	}
	return settled, nil
}

// findEvent locates one of a customer's ledger rows by event id.
func (s *Service) findEvent(customerID, eventID string) (BillingEvent, error) {
	for _, ev := range s.ledger.Events(customerID, "") {
		if ev.EventID == eventID {
			return ev, nil
		}
	}
	return BillingEvent{}, fmt.Errorf("%w: %s", ErrEventNotFound, eventID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciliation (task 4.2)
// ─────────────────────────────────────────────────────────────────────────────

// providerUsageAdapter presents the billing provider as the meter's read-only ProviderUsageSource. It
// exists so the dependency runs one way: billing knows about metering, metering does not know about
// billing, and the reconciler is structurally incapable of writing to either ledger.
type providerUsageAdapter struct{ p Provider }

func (a providerUsageAdapter) RecordedUsage(ctx context.Context, customerID, period string) ([]metering.ProviderUsage, error) {
	recs, err := a.p.RecordedUsage(ctx, customerID, period)
	if err != nil {
		return nil, err
	}
	out := make([]metering.ProviderUsage, 0, len(recs))
	for _, r := range recs {
		out = append(out, metering.ProviderUsage{Metric: r.Metric, Period: r.Period, Quantity: r.Quantity, UsageRef: r.UsageRef})
	}
	return out, nil
}

// Reconciler builds a read-only reconciler over this service's provider and the meter's usage store.
func (s *Service) Reconciler(alerts metering.AlertSink) *metering.Reconciler {
	return metering.NewReconciler(s.meter.Usage(), providerUsageAdapter{s.provider}, alerts)
}

// Reconcile compares one customer-period's usage records against what the provider recorded, and
// surfaces any drift.
//
// It NEVER reconciles by overwrite. The permitted repairs are elsewhere and both additive: re-report
// missing platform→provider usage idempotently (RepairUnreported below), or issue an audited credit.
func (s *Service) Reconcile(ctx context.Context, customerID string, p metering.Period, alerts metering.AlertSink) (metering.ReconcileResult, error) {
	return s.Reconciler(alerts).Reconcile(ctx, customerID, p)
}

// RepairUnreported is the ONE auto-correction the design permits (PRD Q6): re-report usage the provider
// is missing, idempotently, under the usage report's own key. It adds; it never reduces.
//
// A reduction of a provider charge always goes through the audited Credit path instead — which is why
// this method refuses every drift kind except the two "the provider is missing our usage" ones.
func (s *Service) RepairUnreported(ctx context.Context, customerID string, p metering.Period, res metering.ReconcileResult) ([]metering.Metric, error) {
	var repaired []metering.Metric
	for _, d := range res.Drift {
		if d.Kind != metering.DriftUnreported && d.Kind != metering.DriftMissingAtProvider {
			// DriftMissingAtPlatform and DriftQuantityMismatch are NOT auto-repairable: reducing or
			// overwriting the provider's record is exactly the reconcile-by-overwrite this design forbids.
			continue
		}
		if _, err := s.ReportUsage(ctx, customerID, p, d.Metric); err != nil {
			return repaired, fmt.Errorf("billing: re-report %s: %w", d.Metric, err)
		}
		repaired = append(repaired, d.Metric)
	}
	return repaired, nil
}
