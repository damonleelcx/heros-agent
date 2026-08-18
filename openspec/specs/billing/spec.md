# Billing — Spec (folded from P7)

Product rationale: [`../../../docs/prd/P7-billing-metering.md`](../../../docs/prd/P7-billing-metering.md)
§6 (FR9–FR14) and §7.

Covers the Stripe-style billing-provider integration (subscriptions + metered usage + invoicing);
**idempotent billing — never double-charge**; **reversible, auditable** credits and refunds
(corrections additive, no data loss); **gainshare billed as a share of VERIFIED savings only**;
**customers use their own provider keys — the platform never resells tokens**; and **idempotent
webhook handling + invoice reconciliation**. Consumes the `metering` capability's verified
billable-savings and reconciliation.

> No dollar amounts, percentages, or price bands appear in this spec. Plans are named
> (Free / Team / Business / Enterprise); limits + price references are configuration, not code, not
> in git. Card data stays with the PCI-compliant provider; the platform holds only opaque provider
> handles.

## Requirements

### Requirement: The platform SHALL integrate a billing provider for subscriptions and metered usage

The platform SHALL integrate a Stripe-style **billing provider** for **subscriptions + metered usage
+ invoicing**, delegating proration and dunning to the provider. The platform SHALL hold only the
provider's **customer and subscription handles** and SHALL NOT store raw card data — PCI scope stays
with the provider.

#### Scenario: Subscription and metered usage are billed through the provider

- **WHEN** a customer on a paid plan accrues subscription and metered usage over a period
- **THEN** the platform reports the subscription and metered usage to the provider, which invoices
  them
- **AND** the platform stores only provider handles/refs, never card data.

#### Scenario: Proration and dunning are the provider's

- **WHEN** a subscription changes mid-period or a charge fails
- **THEN** the platform reflects the provider's proration and dunning state
- **AND** does not reimplement proration or dunning itself.

### Requirement: Billing SHALL be idempotent and SHALL never double-charge

Every charge-bearing operation — subscription, metered, and gainshare — SHALL carry an **idempotency
key**, and the platform SHALL record **at most one** charge per key. A retried billing operation, or a
re-reported usage record for a `{customer, period, metric}`, SHALL NOT produce a second charge.

#### Scenario: A replayed period charges exactly once

- **WHEN** a metered-usage report for the same `{customer, period, metric}` is submitted more than
  once
- **THEN** the provider records exactly one charge for it
- **AND** no duplicate charge-bearing billing event is persisted.

#### Scenario: A retried charge does not double-charge

- **WHEN** a `Charge` is retried under the same idempotency key after an ambiguous failure
- **THEN** the customer is charged once
- **AND** the second attempt is recognized as a duplicate of the first.

### Requirement: A billing error SHALL be correctable via an additive, audited credit or refund without data loss

A billing error SHALL be correctable via a **credit or refund** issued as a **new, audited** billing
event, with the underlying usage and invoice records left **intact**. The correction SHALL be
**additive** — the platform SHALL NOT delete or mutate the original usage or invoice records to fix an
error. Every charge, credit, and refund SHALL be an append-only audit entry sufficient to reconstruct
the period.

#### Scenario: A wrong charge is corrected via credit with originals intact

- **WHEN** a customer is charged in error and the error is corrected with a credit
- **THEN** the credit is a new audited billing event
- **AND** the original charge and its usage/invoice records remain intact (not deleted or mutated)
- **AND** the net effect on the customer is correct.

#### Scenario: The audit trail reconstructs the period

- **WHEN** an auditor replays the billing events for a period after one or more corrections
- **THEN** the sequence of charges, credits, and refunds reconstructs what was charged, when, and why
- **AND** no prior record was overwritten to achieve the correction.

### Requirement: Gainshare SHALL be billed as a share of verified savings only

Gainshare SHALL be billed as a share of the metering capability's **verified billable savings** —
`baseline SUM − optimized SUM` from **merged** PRs in the **P5.5 verified-delta ledger** — and of
nothing else. The platform SHALL **refuse to raise a gainshare charge** for any savings not present in
the verified-delta ledger. Estimated, unverified, or un-merged savings SHALL NOT be billed.

#### Scenario: An unverified or estimated saving raises no gainshare charge

- **WHEN** a gainshare charge is attempted for a saving that is estimated, or absent from the
  verified-delta ledger
- **THEN** the platform refuses to raise the charge
- **AND** no gainshare billing event is created for it.

#### Scenario: A verified, merged saving is billed and traces to its evidence

- **WHEN** the metering capability reports verified billable savings for a merged, verified delta
- **THEN** a gainshare charge is raised as a share of that verified saving
- **AND** the gainshare billing event traces to the verified-delta ledger entries and merge commits
  behind it.

### Requirement: Customers SHALL use their own provider keys and the platform SHALL never resell tokens

Customers SHALL use their **own** LLM provider keys for optimization and eval runs, and the platform
SHALL **never resell or mark up provider tokens**. No invoice line item SHALL represent resold
provider tokens; the platform bills for its service and its verified savings, not for provider tokens.

#### Scenario: No invoice line resells provider tokens

- **WHEN** a customer's optimization and eval runs execute on the customer's own provider keys and are
  invoiced
- **THEN** no invoice line item represents resold or marked-up provider tokens
- **AND** the provider spend was incurred on the customer's keys, not the platform's.

### Requirement: Provider webhooks SHALL be handled idempotently and invoices reconciled

Provider **webhooks** SHALL be **signature-verified** before any side effect and **deduped** so a
redelivered webhook is processed **exactly once**. **Invoices** SHALL be **reconciled** against the
platform's usage records so a provider-side and platform-side view of a period agree, or the drift is
surfaced.

#### Scenario: A redelivered webhook is processed once

- **WHEN** the provider redelivers a webhook the platform already processed
- **THEN** the platform recognizes it as a duplicate and processes it exactly once
- **AND** no duplicate side effect (charge, credit, or state change) occurs.

#### Scenario: An unsigned webhook is rejected before any side effect

- **WHEN** a webhook arrives without a valid provider signature
- **THEN** it is rejected before any billing side effect is performed.

#### Scenario: An invoice is reconciled against platform usage

- **WHEN** the provider issues an invoice for a period
- **THEN** the platform reconciles it against its usage records for that period
- **AND** any drift between the invoice and the platform's usage is surfaced rather than silently
  accepted.
