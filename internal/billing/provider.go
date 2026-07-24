// Package billing integrates the Stripe-style billing provider and owns the append-only billing
// ledger: subscriptions, metered usage, invoices, additive credits/refunds, and idempotent webhook
// handling.
//
// Three invariants shape every line of it, because money is the least-reversible thing the platform
// does:
//
//   - **NEVER DOUBLE-CHARGE** (design Decision 5). Every charge-bearing operation carries an
//     idempotency key. The key is UNIQUE on `billing_event` *and* is passed to the provider, so a
//     duplicate is refused at both layers: the row cannot persist, and the provider itself declines it.
//     That closes the check-then-insert race a purely application-level dedupe leaves open.
//   - **CORRECTIONS ARE ADDITIVE** (design Decision 6). A credit or refund is a NEW audited row; no
//     path deletes or mutates a prior record, so recovery is a forward operation and an auditor can
//     replay the ledger to any period.
//   - **NO CARD DATA, NO AMOUNTS** (design Decision 10). The platform stores provider HANDLES only.
//     There is deliberately no money field anywhere in this package: an amount the platform never holds
//     is an amount that cannot leak into a span, a metric label, or a log.
//
// And one commercial invariant: the platform **never resells provider tokens** (design Decision 9).
// Customers run on their own LLM provider keys. ChargeKind has exactly three values, none of which is
// a token pass-through, and Invoice.Validate rejects any line that claims to be one.
package billing

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ChargeKind is what a charge is FOR. Exactly three, and none of them is provider tokens — the
// no-resale rule is a closed enum, not a policy document (design Decision 9 / FR13).
type ChargeKind string

const (
	// KindSubscription is the recurring plan charge. Proration is the provider's.
	KindSubscription ChargeKind = "subscription"
	// KindMetered is usage-based billing against a reported usage record.
	KindMetered ChargeKind = "metered"
	// KindGainshare is a share of VERIFIED, MERGED savings — and of nothing else (design Decision 8).
	KindGainshare ChargeKind = "gainshare"
)

// ChargeKinds is every kind a charge may take.
var ChargeKinds = []ChargeKind{KindSubscription, KindMetered, KindGainshare}

// KnownChargeKind reports whether k is billable at all.
func KnownChargeKind(k ChargeKind) bool {
	for _, x := range ChargeKinds {
		if x == k {
			return true
		}
	}
	return false
}

// ErrProviderUnavailable is what the provider returns when it cannot be reached. It is distinguishable
// on purpose: an outage means BUFFER AND RETRY (the usage is safe, the charge is deferred), whereas a
// rejection means STOP. Collapsing the two would either drop revenue or hammer a provider that is
// deliberately refusing.
var ErrProviderUnavailable = errors.New("billing: provider unavailable")

// ErrNoSuchProviderCustomer is returned for an unknown provider handle.
var ErrNoSuchProviderCustomer = errors.New("billing: no such provider customer")

// SubscriptionRequest asks the provider to place a customer on a plan's subscription price.
type SubscriptionRequest struct {
	ProviderCustomerHandle string
	// PriceRef is the OPAQUE provider price handle from the plan config. Never an amount.
	PriceRef string
	// PlanID / PlanName are carried for the provider's own record; the platform's plan model stays
	// authoritative.
	PlanID         string
	PlanName       string
	IdempotencyKey string
}

// SubscriptionResult is the provider's subscription handle plus its lifecycle state.
type SubscriptionResult struct {
	// SubscriptionRef is the opaque provider subscription handle.
	SubscriptionRef string
	// Status is the provider's own state (active / past_due / canceled). The platform REFLECTS it and
	// never recomputes it — dunning is the provider's (billing spec: "proration and dunning are the
	// provider's").
	Status string
}

// UsageReport hands a metered quantity to the provider for a period.
type UsageReport struct {
	ProviderCustomerHandle string
	SubscriptionRef        string
	// Metric is the meter's name; Period is the billing period key.
	Metric   string
	Period   string
	Quantity float64
	PriceRef string
	// IdempotencyKey is derived from {customer, period, metric} — so a retried report for the same
	// tuple is recognized as the SAME report, not a second one (FR10).
	IdempotencyKey string
}

// UsageResult is the provider's usage-record handle.
type UsageResult struct {
	UsageRef string
	// Duplicate is true when the provider recognized the idempotency key and returned the ORIGINAL
	// record rather than creating a second one. Surfaced rather than hidden: it is the evidence that
	// idempotency worked.
	Duplicate bool
}

// ChargeRequest raises one charge.
type ChargeRequest struct {
	ProviderCustomerHandle string
	Kind                   ChargeKind
	Period                 string
	PriceRef               string
	// Quantity is the billable quantity (usage or verified savings). It is a QUANTITY, not an amount:
	// the provider multiplies it by the price the PriceRef names, so the platform never computes money.
	Quantity       float64
	IdempotencyKey string
	// Description is a short, non-sensitive label for the invoice line.
	Description string
}

// ChargeResult is the provider's charge handle.
type ChargeResult struct {
	ChargeRef string
	// AmountRef is the OPAQUE provider handle for the resulting amount. Deliberately not a number: the
	// platform must be able to point AT an amount without ever holding one.
	AmountRef string
	Duplicate bool
}

// CreditRequest issues an additive correction. It never deletes or reduces a prior record — the
// provider records a new credit against the customer.
type CreditRequest struct {
	ProviderCustomerHandle string
	// AgainstRef is the provider charge ref the credit corrects. Required: a credit that names nothing
	// is a correction nobody can reconstruct.
	AgainstRef string
	// Refund makes this a money-back refund rather than a balance credit. Both are additive.
	Refund         bool
	Reason         string
	IdempotencyKey string
}

// CreditResult is the provider's credit handle.
type CreditResult struct {
	CreditRef string
	AmountRef string
	Duplicate bool
}

// LineKind names what an invoice line is for. It mirrors ChargeKind plus the correction kinds; there is
// no token-passthrough member, by construction.
type LineKind string

const (
	LineSubscription LineKind = "subscription"
	LineMetered      LineKind = "metered"
	LineGainshare    LineKind = "gainshare"
	LineCredit       LineKind = "credit"
	LineRefund       LineKind = "refund"
)

// forbiddenLineKinds are the shapes a resold-token line would take. The platform bills for its service
// and its verified savings; provider spend is the customer's, on the customer's keys (FR13). This list
// exists so the rule is checkable rather than merely stated.
var forbiddenLineKinds = map[LineKind]bool{
	"provider_tokens": true,
	"resold_tokens":   true,
	"llm_passthrough": true,
	"token_markup":    true,
	"tokens":          true,
}

// InvoiceLine is one line of a provider invoice as the platform reads it back.
type InvoiceLine struct {
	Kind LineKind `json:"kind"`
	// Basis names the platform record that justified the line ("usage_record:cus_a/2026-07/sum",
	// "billable_savings:cus_a/2026-07"), so every line is traceable to the usage behind it.
	Basis string `json:"basis"`
	// AmountRef is the provider's opaque amount handle. No value.
	AmountRef   string  `json:"amount_ref"`
	Quantity    float64 `json:"quantity"`
	Description string  `json:"description"`
	ChargeRef   string  `json:"charge_ref"`
}

// Invoice is a provider invoice for one customer-period.
type Invoice struct {
	InvoiceRef string        `json:"invoice_ref"`
	CustomerID string        `json:"customer_id"`
	Period     string        `json:"period"`
	Status     string        `json:"status"` // provider-owned: draft / open / paid / past_due / uncollectible
	Lines      []InvoiceLine `json:"lines"`
}

// ErrResoldTokens is returned when an invoice carries a line that represents resold provider tokens.
var ErrResoldTokens = errors.New("billing: invoice line represents resold provider tokens — the platform never resells or marks up provider tokens")

// Validate asserts the no-resale rule over a whole invoice (task 3.4 / FR13). It is called on every
// invoice the platform reads back, so a provider-side misconfiguration cannot introduce a token line
// the platform then renders as if it were legitimate.
func (inv Invoice) Validate() error {
	for i, l := range inv.Lines {
		if forbiddenLineKinds[l.Kind] {
			return fmt.Errorf("%w (line %d, kind %q)", ErrResoldTokens, i, l.Kind)
		}
		switch l.Kind {
		case LineSubscription, LineMetered, LineGainshare, LineCredit, LineRefund:
		default:
			return fmt.Errorf("billing: invoice line %d has unknown kind %q — an unrecognized line cannot be shown to a customer as if it were understood", i, l.Kind)
		}
		if l.Basis == "" {
			return fmt.Errorf("billing: invoice line %d (%s) names no basis — every line must trace to the usage that justified it", i, l.Kind)
		}
	}
	return nil
}

// RecordedUsage is what the provider says it recorded for a period — the input to reconciliation. The
// provider is the system of record for "what was CHARGED"; `usage_record` is the platform's system of
// record for "what was USED" (design Decision 7).
type RecordedUsage struct {
	Metric   string  `json:"metric"`
	Period   string  `json:"period"`
	Quantity float64 `json:"quantity"`
	UsageRef string  `json:"usage_ref"`
}

// Provider is the Stripe-style billing provider, seen from the platform.
//
// It is an interface so the stub used by tests and the demo satisfies exactly the contract a real
// Stripe integration does — the code under test is the shipped code path, with the network swapped out.
// Note what is NOT here: no proration, no dunning, no tax. Those are the provider's, and reimplementing
// them would create a second, disagreeing opinion about what a customer owes.
type Provider interface {
	// EnsureCustomer returns the provider's opaque customer handle, creating it if needed.
	EnsureCustomer(ctx context.Context, customerID string) (string, error)
	CreateSubscription(ctx context.Context, req SubscriptionRequest) (SubscriptionResult, error)
	Subscription(ctx context.Context, subscriptionRef string) (SubscriptionResult, error)
	ReportUsage(ctx context.Context, req UsageReport) (UsageResult, error)
	RaiseCharge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	IssueCredit(ctx context.Context, req CreditRequest) (CreditResult, error)
	Invoice(ctx context.Context, customerID, period string) (Invoice, error)
	RecordedUsage(ctx context.Context, customerID, period string) ([]RecordedUsage, error)
	// Describe names the provider for the readiness surface — the identity, never a credential.
	Describe() string
}

// Clock is the injected time source. Billing rows are evidence; a wall clock that cannot be pinned
// makes a test's assertions about ordering unfalsifiable.
type Clock func() time.Time
