package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/metering"
)

// gainshare.go raises a share-of-verified-savings charge — and, far more importantly, REFUSES to raise
// one for anything else (task 6.3 / design Decision 8 / FR12).
//
// ## The refusal is the feature
//
// Gainshare is the platform's sharpest trust edge: it bills for savings the platform itself claims to
// have produced. Every guard here exists so that claim cannot be self-serving:
//
//	1. CONSENT     — gainshare requires informed, recorded, revocable consent on the account.
//	2. LEDGER-ONLY — the billable figure comes from ComputeBillableSavings, which reads only the P5.5
//	                 verified-delta ledger for MERGED PRs.
//	3. RE-VERIFY   — every ref the figure claims is resolved back against the ledger AT CHARGE TIME. A
//	                 saving whose evidence is not there raises no charge, whatever the stored row says.
//	4. EVIDENCE    — the resulting billing_event carries the verified-delta refs and merge commits, and
//	                 both the ledger and the database reject a gainshare row without them.
//
// Guard 3 deserves its reason stated plainly: without it, the only thing standing between an estimate
// and an invoice would be the correctness of whatever wrote the `billable_savings` row. Re-resolving
// the evidence at charge time means a corrupted, stale, or hand-edited savings row still cannot produce
// a charge — the ledger is asked again, and it is the ledger that decides.

// Gainshare errors. Each names a distinct refusal so a support engineer can answer "why was this not
// billed" without reading code.
var (
	// ErrNoGainshareConsent: gainshare is a contract the customer opts into.
	ErrNoGainshareConsent = errors.New("billing: refusing a gainshare charge without recorded gainshare consent")
	// ErrSavingsNotVerified: the claimed saving is absent from, or not billable in, the verified-delta
	// ledger. THE load-bearing refusal (FR12).
	ErrSavingsNotVerified = errors.New("billing: refusing a gainshare charge for savings absent from the verified-delta ledger")
	// ErrNoGainsharePrice: the plan has no gainshare price reference in the config store.
	ErrNoGainsharePrice = errors.New("billing: the plan has no gainshare price reference")
)

// GainshareResult is a raised gainshare charge together with the evidence behind it.
type GainshareResult struct {
	Event   BillingEvent             `json:"event"`
	Savings metering.BillableSavings `json:"savings"`
}

// ChargeGainshare raises a period's gainshare charge from the customer's VERIFIED, MERGED savings.
//
// It returns ErrNoVerifiedSavings when the period has none — a normal outcome for most periods, and a
// silence-free one: the caller can say "nothing verified this period" rather than showing a zero of
// unknown provenance.
func (s *Service) ChargeGainshare(ctx context.Context, customerID string, p metering.Period,
	ledger metering.VerifiedDeltaLedger, savings metering.SavingsStore) (GainshareResult, error) {

	acct, plan, err := s.resolve(customerID)
	if err != nil {
		return GainshareResult{}, err
	}

	// ── 1. CONSENT ────────────────────────────────────────────────────────────
	if !acct.GainshareConsent {
		return GainshareResult{}, fmt.Errorf("%w (%s)", ErrNoGainshareConsent, customerID)
	}
	priceRef := plan.PriceRefs[string(KindGainshare)]
	if priceRef == "" {
		return GainshareResult{}, fmt.Errorf("%w: %s", ErrNoGainsharePrice, plan.PlanID)
	}

	// ── 2. LEDGER-ONLY ────────────────────────────────────────────────────────
	bs, err := metering.ComputeBillableSavings(ledger, customerID, p)
	if err != nil {
		return GainshareResult{}, err
	}
	if bs.Savings <= 0 || len(bs.VerifiedDeltaRefs) == 0 {
		return GainshareResult{Savings: bs}, fmt.Errorf("%w: %s/%s", metering.ErrNoVerifiedSavings, customerID, p.ID)
	}

	// ── 3. RE-VERIFY every ref against the ledger, at charge time ─────────────
	evidence := make([]string, 0, len(bs.VerifiedDeltaRefs)+len(bs.MergeCommits))
	for _, ref := range bs.VerifiedDeltaRefs {
		entry, ok := ledger.ByRef(ref)
		if !ok {
			return GainshareResult{Savings: bs}, fmt.Errorf("%w: ref %q is not in the ledger", ErrSavingsNotVerified, ref)
		}
		if billable, why := entry.Billable(); !billable {
			return GainshareResult{Savings: bs}, fmt.Errorf("%w: ref %q is in the ledger but not billable: %s", ErrSavingsNotVerified, ref, why)
		}
		evidence = append(evidence, "verified_delta:"+ref)
	}
	for _, commit := range bs.MergeCommits {
		if commit == "" {
			return GainshareResult{Savings: bs}, fmt.Errorf("%w: a contributing delta has no merge commit", ErrSavingsNotVerified)
		}
		evidence = append(evidence, "merge:"+commit)
	}

	// Persist the figure WITH its evidence before charging, so the invoice line and the audit row point
	// at a stored record rather than at a number recomputed differently later.
	if savings != nil {
		bs.UpdatedAt = s.now().UTC()
		if _, err := savings.Upsert(bs); err != nil {
			return GainshareResult{Savings: bs}, fmt.Errorf("billing: persist billable savings: %w", err)
		}
	}

	// ── 4. CHARGE, carrying the evidence ──────────────────────────────────────
	ev, err := s.charge(ctx, chargeSpec{
		CustomerID:     customerID,
		Period:         p,
		Kind:           KindGainshare,
		EventType:      TypeGainshareCharge,
		IdempotencyKey: GainshareIdempotencyKey(customerID, p.ID),
		Quantity:       bs.Savings,
		HasQuantity:    true,
		CausedBy:       fmt.Sprintf("billable_savings:%s/%s", customerID, p.ID),
		Description:    fmt.Sprintf("verified savings on %d merged optimization(s)", len(bs.MergeCommits)),
		Evidence:       evidence,
	})
	if err != nil {
		return GainshareResult{Savings: bs}, err
	}
	return GainshareResult{Event: ev, Savings: bs}, nil
}

// GainshareEvidence resolves a raised gainshare charge back to the ledger entries behind it — the
// auditability path (task 6.5). It returns the entries in the order the charge recorded them.
//
// This is the read side of "every billed saving traces to its evidence": given only a billing event,
// an auditor gets back the verified deltas, each carrying its fixed baseline + holdout methodology.
func GainshareEvidence(ev BillingEvent, ledger metering.VerifiedDeltaLedger) ([]metering.VerifiedDelta, []string, error) {
	if ev.Type != TypeGainshareCharge {
		return nil, nil, fmt.Errorf("billing: %s is a %s, not a gainshare charge", ev.EventID, ev.Type)
	}
	if len(ev.Evidence) == 0 {
		return nil, nil, fmt.Errorf("%w: gainshare event %s carries no evidence", ErrSavingsNotVerified, ev.EventID)
	}
	var deltas []metering.VerifiedDelta
	var merges []string
	for _, e := range ev.Evidence {
		switch {
		case len(e) > 15 && e[:15] == "verified_delta:":
			ref := e[15:]
			d, ok := ledger.ByRef(ref)
			if !ok {
				return nil, nil, fmt.Errorf("%w: evidence ref %q no longer resolves", ErrSavingsNotVerified, ref)
			}
			deltas = append(deltas, d)
		case len(e) > 6 && e[:6] == "merge:":
			merges = append(merges, e[6:])
		default:
			return nil, nil, fmt.Errorf("billing: unrecognized evidence entry %q on %s", e, ev.EventID)
		}
	}
	return deltas, merges, nil
}
