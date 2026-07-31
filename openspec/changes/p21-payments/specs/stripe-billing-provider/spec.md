# Stripe Billing Provider — Spec Delta (P21)

Product rationale: [`../../../../docs/prd/P21-stripe-payments.md`](../../../../docs/prd/P21-stripe-payments.md)
§6 (FR1–FR7) and §7. Architecture decisions: [`../../design.md`](../../design.md) Decisions 1, 4, 8, 9. Implements the
**existing** [`billing.Provider`](../../../../internal/billing/provider.go) interface built by
[`p7-billing-metering`](../../../p7-billing-metering/); reuses the P7 idempotency keys
([`ledger.go`](../../../../internal/billing/ledger.go)) and the additive-correction path
([`correction.go`](../../../../internal/billing/correction.go)).

Covers the **concrete Stripe implementation** of the P7 billing abstraction: a `stripe.Provider` that satisfies
`billing.Provider` without changing it, passing the P7-derived idempotency key to Stripe on every charge-bearing
call, placing customers on **opaque price references** with proration Stripe's, reporting metered **quantities**
(never amounts), issuing **additive** credits/refunds, reading invoices back through `Invoice.Validate`, and
preserving the **outage vs. rejection** split — and verifying, before any of it charges, that every
configured price reference actually resolves at the provider.

> This capability is a *substitution*, not a new API surface. The interface, the ledger, the charge protocol, and
> the correction path are P7's and do not change; P21 fills the `Provider` box with Stripe so a **second** processor
> stays expressible as a second implementation (八级法则 可演进). No requirement here widens the interface.

## ADDED Requirements

### Requirement: A Stripe provider SHALL implement the existing billing.Provider interface without changing it

A `stripe.Provider` SHALL satisfy the existing `billing.Provider` interface — `EnsureCustomer`,
`CreateSubscription`, `Subscription`, `ReportUsage`, `RaiseCharge`, `IssueCredit`, `Invoice`, `RecordedUsage`,
`Describe` — with identical signatures and error semantics, so every existing caller runs unchanged. The
interface SHALL NOT be widened to accommodate Stripe.

#### Scenario: Every existing caller runs unchanged against Stripe

- **WHEN** the platform is configured with the Stripe provider instead of the stub
- **THEN** the charge protocol in `service.go`, the correction path, and the reconciler run without modification
- **AND** the same billing test suite that passes against `StubProvider` passes against `stripe.Provider` in test
  mode (contract parity).

#### Scenario: The interface is not widened for Stripe

- **WHEN** the Stripe implementation is added
- **THEN** no Stripe-specific type appears in the `billing.Provider` interface or in any caller's signature
- **AND** a second processor could be added as a second implementation of the same interface, touching no caller.

### Requirement: Every charge-bearing Stripe call SHALL carry the P7-derived idempotency key so a retry or replay produces at most one Stripe object

`CreateSubscription`, `ReportUsage`, `RaiseCharge`, and `IssueCredit` SHALL pass the P7-derived idempotency key as
Stripe's `Idempotency-Key` header. A retried operation or a redelivered record SHALL produce **at most one** Stripe
object, and the result SHALL surface `Duplicate = true` when Stripe returned the original.

#### Scenario: A retried charge produces one Stripe object

- **WHEN** a charge for `{customer, period, metric}` is submitted twice under the same P7 idempotency key
- **THEN** Stripe records exactly one charge object and returns the original on the second call
- **AND** the ledger holds exactly one row for the key, so both layers refuse the duplicate.

#### Scenario: The ambiguous recorded-then-lost failure does not double-charge

- **WHEN** Stripe records a charge but the response is lost, and the operation is retried under the same key
- **THEN** the retry resolves the pending ledger row against the original Stripe object
- **AND** no second charge is created.

### Requirement: Subscriptions SHALL use an opaque price reference and the provider SHALL never compute an amount

`CreateSubscription` SHALL place the customer on the plan's opaque `price_ref` (a Stripe price ID); the provider
SHALL never compute, store, or return a money amount, and proration on a plan change SHALL be Stripe's.

#### Scenario: A subscription is created from a price reference with no amount in the platform

- **WHEN** a customer subscribes to a plan by name
- **THEN** the provider creates a Stripe subscription on the plan's `price_ref`
- **AND** no money amount is computed or stored by the platform — Stripe applies the price the reference names.

#### Scenario: Proration on a plan change is Stripe's

- **WHEN** a customer changes plan mid-period
- **THEN** the provider updates the Stripe subscription to the new `price_ref`
- **AND** the resulting proration is computed by Stripe, not by the platform.

### Requirement: Metered usage SHALL be reported to Stripe as a quantity, never an amount

`ReportUsage` SHALL report a metered **quantity** for a `{customer, period, metric}` to a Stripe metered
subscription item; the provider SHALL multiply nothing — Stripe applies the price the `price_ref` names.

#### Scenario: A metered quantity is reported without an amount

- **WHEN** the platform reports a period's metered usage quantity
- **THEN** Stripe records the quantity against the metered item under the report's idempotency key
- **AND** the platform computes no money value for it.

### Requirement: Credits and refunds SHALL be additive Stripe objects that never reduce the original

`IssueCredit` SHALL issue an additive Stripe credit note (or a refund when `Refund` is set) against a prior charge;
it SHALL never reduce, void, or delete the original Stripe object.

#### Scenario: A correction is additive

- **WHEN** a billing error is corrected via `IssueCredit` against a prior charge
- **THEN** Stripe records a new credit note / refund object referencing the original charge
- **AND** the original charge object is unchanged.

### Requirement: Invoices read back SHALL pass Invoice.Validate and no line SHALL represent resold provider tokens

`Invoice` SHALL read back a Stripe invoice as `billing.Invoice`, and every line SHALL pass `Invoice.Validate`: a
line whose kind is a resold-token shape SHALL be **rejected**, and every line SHALL name a basis. `RecordedUsage`
SHALL return Stripe's recorded metered usage for reconciliation.

#### Scenario: A resold-token line is rejected on read-back

- **WHEN** a Stripe invoice is read back and a line carries a resold-token kind
- **THEN** `Invoice.Validate` returns an error and the invoice is not rendered as if it were understood
- **AND** the platform surfaces the misconfiguration rather than showing a token line to a customer.

#### Scenario: Recorded usage is available for reconciliation

- **WHEN** reconciliation reads Stripe's recorded usage for a customer-period
- **THEN** `RecordedUsage` returns the metered quantities Stripe recorded
- **AND** they can be compared against the platform's usage records without a write to either ledger.

### Requirement: Every configured price reference SHALL be verified at the provider before anything charges against it

The platform SHALL provide a **preflight** that resolves **every** `price_ref` in the published plan
configuration against the provider and reports, for each one that does not resolve, **which plan, which charge
kind, and which reference** failed. The preflight SHALL be side-effect-free — it reads, it never creates — and its
result SHALL be externally readable on the readiness surface. A price reference that does not resolve SHALL NOT be
discovered by a rejected charge during a billing period.

#### Scenario: A placeholder price reference is named before it can reject a charge

- **WHEN** a plan carries a `price_ref` that does not exist in the provider account
- **THEN** the preflight reports that plan, that charge kind, and that reference as unresolved
- **AND** the failure is visible before a period's first charge rather than as a rejection during one.

#### Scenario: A fully configured account preflights clean

- **WHEN** every plan's price references resolve at the provider
- **THEN** the preflight reports no unresolved reference, and it names how many it checked
- **AND** a preflight that checked nothing is reported as unverified rather than as clean.

#### Scenario: The preflight creates nothing

- **WHEN** the preflight runs
- **THEN** it performs read operations only and creates no customer, subscription, charge, or price
- **AND** running it repeatedly changes nothing at the provider.

### Requirement: The Stripe provider SHALL distinguish an outage from a rejection so the P7 outage buffer works unchanged

The provider SHALL return `ErrProviderUnavailable` for an outage (the caller buffers and retries) and a distinct
error for a rejection (the caller stops), so the P7 outage buffer and `FlushPending` recovery drain the window
exactly once.

#### Scenario: A Stripe outage buffers rather than drops

- **WHEN** Stripe is unreachable during a charge
- **THEN** the provider returns `ErrProviderUnavailable` and the ledger row stays pending
- **AND** the usage is already safe, and `FlushPending` bills the window exactly once on recovery.

#### Scenario: A rejection stops rather than retries

- **WHEN** Stripe rejects a request (not an outage)
- **THEN** the provider returns a distinct, non-outage error
- **AND** the caller does not hammer Stripe with retries of a deliberately refused call.

### Requirement: Stripe artefacts SHALL be mode-scoped, and the preflight SHALL run in the mode the deployment is running in

The API key, the webhook endpoint and its signing secret, the customer handles, and the **price references** SHALL
be treated as **per-mode** artefacts that live mode does not inherit from test mode. The preflight SHALL resolve
the catalog **in the mode the deployment is running in**, and every readiness line or verification record naming
the provider SHALL **name that mode**. A key whose mode does not match the surface SHALL be **refused** — in both
directions — not warned about.

#### Scenario: A clean test-mode preflight is not reported as a statement about live

- **WHEN** the preflight resolves every reference in test mode
- **THEN** the readiness line records the result **together with the mode it was observed in**
- **AND** no surface reports the live catalog as verified on the strength of a test-mode result.

#### Scenario: A live catalog is preflighted in live mode before the first live charge

- **WHEN** the deployment is configured for live mode
- **THEN** the preflight resolves the **live** catalog's references at the provider before anything charges
- **AND** a reference that resolves in test but not in live is named by plan, kind and reference.

#### Scenario: A key whose mode does not match the surface is refused

- **WHEN** a test-mode key is resolved for a live surface, or a live-mode key for a test surface
- **THEN** the provider refuses to construct rather than proceeding with a warning
- **AND** an unrecognised key prefix is refused outright rather than assumed to be test.

### Requirement: A rate limit SHALL be an outage and a decline SHALL be a rejection

Provider **HTTP 429** responses and lock-contention errors SHALL map to `ErrProviderUnavailable`, so the P7 outage
buffer holds the work and `FlushPending` drains the window exactly once. **Card declines** and invalid-request
errors SHALL map to a distinct rejection error that stops. Both mappings SHALL be asserted.

#### Scenario: A rate-limited window is billed once, not lost

- **WHEN** the provider returns 429 during a charge-bearing call
- **THEN** the provider returns `ErrProviderUnavailable` and the ledger row stays pending
- **AND** `FlushPending` bills the window exactly once on recovery, with no usage discarded.

#### Scenario: A declined card stops instead of retrying

- **WHEN** the provider declines a payment
- **THEN** the provider returns a rejection error distinct from an outage
- **AND** the caller does not retry a card that a retry cannot make succeed.
