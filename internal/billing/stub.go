package billing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// stub.go is the Stripe-style provider stub (task 9.1): subscriptions, metered usage, invoices,
// redeliverable webhooks, and — the part that matters — PROVIDER-SIDE IDEMPOTENCY.
//
// It is not a mock that returns canned values. It behaves the way the real provider's idempotency
// contract behaves: a repeated idempotency key returns the ORIGINAL record and creates nothing. That is
// what makes the never-double-charge tests meaningful — they exercise both layers of the guarantee (the
// platform's UNIQUE key AND the provider's refusal), and the second layer is only real if the stub
// actually implements it.
//
// It also models the two failure shapes the design cares about:
//   - SetDown: an outage. Every call returns ErrProviderUnavailable, so usage buffers and the recovery
//     path can be tested (task 3.5).
//   - SetFailAfterRecord: an AMBIGUOUS failure — the provider records the charge, then the response is
//     lost. This is the nastiest case in billing, and the only way to prove the retry does not
//     double-charge is to be able to produce it on demand.

// StubProvider is an in-process Stripe-style billing provider.
type StubProvider struct {
	mu sync.Mutex

	customers     map[string]string // customer_id -> provider handle
	subscriptions map[string]SubscriptionResult
	// byKey is the provider's own idempotency index: key -> the operation and ref it originally
	// produced. The OPERATION is recorded alongside the ref because a provider's key namespace is
	// global per account: a key reused across two different operations would otherwise return the
	// first operation's record to the second caller — silently yielding, say, a usage ref where a
	// charge ref was expected, and no charge at all. Recording the operation turns that into a loud
	// refusal (see keyFor).
	byKey       map[string]keyedRef
	usage       map[string]RecordedUsage // usage ref -> record
	usageBy     map[string][]string      // customer_id -> usage refs
	charges     map[string]ChargeRequest // charge ref -> request
	chargeBy    map[string][]string      // customer_id -> charge refs
	credits     map[string]CreditRequest
	creditBy    map[string][]string
	handleOwner map[string]string // provider handle -> customer_id

	seq  int
	down bool
	// failAfterRecord makes the next charge/usage call record the effect and THEN fail — the ambiguous
	// failure a retry must survive without double-charging.
	failAfterRecord bool
	// calls counts effect-producing provider calls, so a test can assert the provider was really asked
	// more than once and still recorded one thing.
	calls map[string]int
}

// keyedRef is one idempotency-key entry: which operation claimed the key, and what it produced.
type keyedRef struct {
	op  string
	ref string
}

// ErrIdempotencyKeyReused is returned when one key is presented for two different operations. It is a
// PROGRAMMING error, not a retry, and a provider that quietly returned the wrong record instead would
// convert it into a missing charge nobody notices.
var ErrIdempotencyKeyReused = errors.New("billing: idempotency key reused across different provider operations")

// claim resolves an idempotency key for op. It returns the previously produced ref and true when this
// is a genuine retry, ("", false) when the key is new, and an error when the key belongs to another
// operation. Callers hold p.mu.
func (p *StubProvider) claim(op, key string) (string, bool, error) {
	prev, ok := p.byKey[key]
	if !ok {
		return "", false, nil
	}
	if prev.op != op {
		return "", false, fmt.Errorf("%w: %q was used for %s and is now presented for %s", ErrIdempotencyKeyReused, key, prev.op, op)
	}
	return prev.ref, true, nil
}

// NewStubProvider builds an empty provider.
func NewStubProvider() *StubProvider {
	return &StubProvider{
		customers: map[string]string{}, subscriptions: map[string]SubscriptionResult{},
		byKey: map[string]keyedRef{}, usage: map[string]RecordedUsage{}, usageBy: map[string][]string{},
		charges: map[string]ChargeRequest{}, chargeBy: map[string][]string{},
		credits: map[string]CreditRequest{}, creditBy: map[string][]string{},
		handleOwner: map[string]string{}, calls: map[string]int{},
	}
}

// SetDown simulates a provider outage.
func (p *StubProvider) SetDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

// SetFailAfterRecord makes the next effect-producing call record its effect and then return an error —
// the ambiguous failure.
func (p *StubProvider) SetFailAfterRecord(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failAfterRecord = v
}

// Calls reports how many times an operation was invoked (including duplicates).
func (p *StubProvider) Calls(op string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[op]
}

// ChargeCount is how many DISTINCT charges the provider actually recorded — the number a
// never-double-charge test asserts on.
func (p *StubProvider) ChargeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.charges)
}

// UsageCount is how many DISTINCT usage records the provider recorded.
func (p *StubProvider) UsageCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.usage)
}

// Describe names the provider.
func (p *StubProvider) Describe() string { return "stub:stripe-style(test-mode)" }

func (p *StubProvider) nextRef(prefix string) string {
	p.seq++
	return fmt.Sprintf("%s_%06d", prefix, p.seq)
}

// EnsureCustomer returns (creating if needed) the provider's opaque customer handle.
func (p *StubProvider) EnsureCustomer(_ context.Context, customerID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls["EnsureCustomer"]++
	if p.down {
		return "", ErrProviderUnavailable
	}
	if h, ok := p.customers[customerID]; ok {
		return h, nil
	}
	h := p.nextRef("prov_cus")
	p.customers[customerID] = h
	p.handleOwner[h] = customerID
	return h, nil
}

// CreateSubscription places a customer on a plan price. Idempotent by key.
func (p *StubProvider) CreateSubscription(_ context.Context, req SubscriptionRequest) (SubscriptionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls["CreateSubscription"]++
	if p.down {
		return SubscriptionResult{}, ErrProviderUnavailable
	}
	if _, ok := p.handleOwner[req.ProviderCustomerHandle]; !ok {
		return SubscriptionResult{}, fmt.Errorf("%w: %s", ErrNoSuchProviderCustomer, req.ProviderCustomerHandle)
	}
	if ref, dup, err := p.claim("CreateSubscription", req.IdempotencyKey); err != nil {
		return SubscriptionResult{}, err
	} else if dup {
		return p.subscriptions[ref], nil
	}
	ref := p.nextRef("prov_sub")
	res := SubscriptionResult{SubscriptionRef: ref, Status: "active"}
	p.subscriptions[ref] = res
	p.byKey[req.IdempotencyKey] = keyedRef{"CreateSubscription", ref}
	return res, nil
}

// Subscription reads a subscription's provider-owned state (dunning included).
func (p *StubProvider) Subscription(_ context.Context, ref string) (SubscriptionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return SubscriptionResult{}, ErrProviderUnavailable
	}
	s, ok := p.subscriptions[ref]
	if !ok {
		return SubscriptionResult{}, fmt.Errorf("billing: no such subscription %s", ref)
	}
	return s, nil
}

// SetSubscriptionStatus drives the provider-owned dunning state (past_due, etc.) that webhooks report
// and the UI reflects. The platform never computes this — it only mirrors it.
func (p *StubProvider) SetSubscriptionStatus(ref, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.subscriptions[ref]; ok {
		s.Status = status
		p.subscriptions[ref] = s
	}
}

// ReportUsage records metered usage. A repeated idempotency key returns the ORIGINAL record.
func (p *StubProvider) ReportUsage(_ context.Context, req UsageReport) (UsageResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls["ReportUsage"]++
	if p.down {
		return UsageResult{}, ErrProviderUnavailable
	}
	owner, ok := p.handleOwner[req.ProviderCustomerHandle]
	if !ok {
		return UsageResult{}, fmt.Errorf("%w: %s", ErrNoSuchProviderCustomer, req.ProviderCustomerHandle)
	}
	if ref, dup, err := p.claim("ReportUsage", req.IdempotencyKey); err != nil {
		return UsageResult{}, err
	} else if dup {
		return UsageResult{UsageRef: ref, Duplicate: true}, nil
	}
	ref := p.nextRef("prov_usage")
	p.usage[ref] = RecordedUsage{Metric: req.Metric, Period: req.Period, Quantity: req.Quantity, UsageRef: ref}
	p.usageBy[owner] = append(p.usageBy[owner], ref)
	p.byKey[req.IdempotencyKey] = keyedRef{"ReportUsage", ref}
	if p.failAfterRecord {
		p.failAfterRecord = false
		return UsageResult{}, fmt.Errorf("%w: response lost after the provider recorded the usage", ErrProviderUnavailable)
	}
	return UsageResult{UsageRef: ref}, nil
}

// RaiseCharge records one charge. A repeated idempotency key records NOTHING and returns the original.
func (p *StubProvider) RaiseCharge(_ context.Context, req ChargeRequest) (ChargeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls["RaiseCharge"]++
	if p.down {
		return ChargeResult{}, ErrProviderUnavailable
	}
	owner, ok := p.handleOwner[req.ProviderCustomerHandle]
	if !ok {
		return ChargeResult{}, fmt.Errorf("%w: %s", ErrNoSuchProviderCustomer, req.ProviderCustomerHandle)
	}
	if !KnownChargeKind(req.Kind) {
		return ChargeResult{}, fmt.Errorf("billing: provider refuses unknown charge kind %q", req.Kind)
	}
	if ref, dup, err := p.claim("RaiseCharge", req.IdempotencyKey); err != nil {
		return ChargeResult{}, err
	} else if dup {
		return ChargeResult{ChargeRef: ref, AmountRef: "prov_amt_" + ref, Duplicate: true}, nil
	}
	ref := p.nextRef("prov_ch")
	p.charges[ref] = req
	p.chargeBy[owner] = append(p.chargeBy[owner], ref)
	p.byKey[req.IdempotencyKey] = keyedRef{"RaiseCharge", ref}
	if p.failAfterRecord {
		p.failAfterRecord = false
		return ChargeResult{}, fmt.Errorf("%w: response lost after the provider recorded the charge", ErrProviderUnavailable)
	}
	return ChargeResult{ChargeRef: ref, AmountRef: "prov_amt_" + ref}, nil
}

// IssueCredit records an additive credit or refund against a prior charge. It never reduces or removes
// the original — the provider keeps both, which is what makes the correction auditable.
func (p *StubProvider) IssueCredit(_ context.Context, req CreditRequest) (CreditResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls["IssueCredit"]++
	if p.down {
		return CreditResult{}, ErrProviderUnavailable
	}
	owner, ok := p.handleOwner[req.ProviderCustomerHandle]
	if !ok {
		return CreditResult{}, fmt.Errorf("%w: %s", ErrNoSuchProviderCustomer, req.ProviderCustomerHandle)
	}
	if _, ok := p.charges[req.AgainstRef]; !ok {
		return CreditResult{}, fmt.Errorf("billing: cannot credit unknown charge %s", req.AgainstRef)
	}
	if ref, dup, err := p.claim("IssueCredit", req.IdempotencyKey); err != nil {
		return CreditResult{}, err
	} else if dup {
		return CreditResult{CreditRef: ref, AmountRef: "prov_amt_" + ref, Duplicate: true}, nil
	}
	ref := p.nextRef("prov_cr")
	p.credits[ref] = req
	p.creditBy[owner] = append(p.creditBy[owner], ref)
	p.byKey[req.IdempotencyKey] = keyedRef{"IssueCredit", ref}
	return CreditResult{CreditRef: ref, AmountRef: "prov_amt_" + ref}, nil
}

// Invoice assembles the provider's invoice for a customer-period from what it recorded.
//
// Note the line kinds it can produce: subscription, metered, gainshare, credit, refund. There is no
// code path here that can emit a token line, which is the no-resale rule expressed as reachability
// rather than as a policy statement.
func (p *StubProvider) Invoice(_ context.Context, customerID, period string) (Invoice, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return Invoice{}, ErrProviderUnavailable
	}
	inv := Invoice{InvoiceRef: "prov_inv_" + customerID + "_" + period, CustomerID: customerID, Period: period, Status: "open"}
	refs := append([]string(nil), p.chargeBy[customerID]...)
	sort.Strings(refs)
	for _, ref := range refs {
		req := p.charges[ref]
		if req.Period != period {
			continue
		}
		inv.Lines = append(inv.Lines, InvoiceLine{
			Kind:        LineKind(req.Kind),
			Basis:       req.Description,
			AmountRef:   "prov_amt_" + ref,
			Quantity:    req.Quantity,
			Description: req.Description,
			ChargeRef:   ref,
		})
	}
	crefs := append([]string(nil), p.creditBy[customerID]...)
	sort.Strings(crefs)
	for _, ref := range crefs {
		req := p.credits[ref]
		against := p.charges[req.AgainstRef]
		if against.Period != period {
			continue
		}
		kind := LineCredit
		if req.Refund {
			kind = LineRefund
		}
		inv.Lines = append(inv.Lines, InvoiceLine{
			Kind:        kind,
			Basis:       "billing_event:" + req.AgainstRef,
			AmountRef:   "prov_amt_" + ref,
			Description: req.Reason,
			ChargeRef:   ref,
		})
	}
	return inv, nil
}

// RecordedUsage is what the provider believes it recorded for a period — the reconciler's right-hand
// side.
func (p *StubProvider) RecordedUsage(_ context.Context, customerID, period string) ([]RecordedUsage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return nil, ErrProviderUnavailable
	}
	var out []RecordedUsage
	for _, ref := range p.usageBy[customerID] {
		u := p.usage[ref]
		if u.Period == period {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metric < out[j].Metric })
	return out, nil
}

// DropUsage deletes a usage record the provider had — the seed for a "the provider is missing usage the
// platform reported" drift (task 4.5). Test-only surgery on the provider's side of the ledger; the
// platform's records are untouched, which is the whole point of the drift being detectable.
func (p *StubProvider) DropUsage(customerID, period, metric string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.usageBy[customerID][:0]
	for _, ref := range p.usageBy[customerID] {
		u := p.usage[ref]
		if u.Period == period && u.Metric == metric {
			delete(p.usage, ref)
			continue
		}
		kept = append(kept, ref)
	}
	p.usageBy[customerID] = kept
}

// InjectUsage adds a usage record the platform never reported — the seed for the opposite drift.
func (p *StubProvider) InjectUsage(customerID, period, metric string, qty float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ref := p.nextRef("prov_usage_ghost")
	p.usage[ref] = RecordedUsage{Metric: metric, Period: period, Quantity: qty, UsageRef: ref}
	p.usageBy[customerID] = append(p.usageBy[customerID], ref)
}
