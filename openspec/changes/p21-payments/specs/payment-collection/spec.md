# Payment Collection — Spec Delta (P21)

Product rationale: [`../../../../docs/prd/P21-stripe-payments.md`](../../../../docs/prd/P21-stripe-payments.md)
§6 (FR8–FR12) and §7. Architecture decisions: [`../../design.md`](../../design.md) Decisions 2, 6, 7. Inherits
[ADR-006](../../../../docs/adr/ADR-006-console-deploy-packaging.md) (console deploy) and
[ADR-008](../../../../docs/adr/ADR-008-console-tenant-identity-seam.md) (console tenant-identity seam). Reuses the P7
opaque price model ([`plancfg.PriceRefs`](../../../../internal/plancfg/plancfg.go)), the plan-config git fence
([`plancfg/gitfence_test.go`](../../../../internal/plancfg/gitfence_test.go)), the handle-only account model
([`account.NewHandle`](../../../../internal/account/account.go)), and the entitlement gate.

Covers the **P7 gap**: capturing a payment method (Stripe Checkout / Payment Element, card browser→Stripe
directly), the self-serve **subscribe / upgrade / downgrade by plan name** flow, and the customer-console **billing
page** — with **no Stripe secret in the client**, **no price value in the bundle**, and first-class **unhappy
states**.

> This capability is the collection surface P7 left out. It captures a payment method and drives the P7 subscription
> API; it introduces no billing concept and no price. The card never touches the platform, and no figure on the
> page is anything but a value read back from the billing/metering API.

## ADDED Requirements

### Requirement: A payment method SHALL be captured such that card data goes from the browser to Stripe directly

The platform SHALL capture a payment method via Stripe Checkout (or the Payment Element) such that **card data
goes from the browser to Stripe directly and never through the platform**. The platform SHALL store only the
resulting Stripe customer / payment-method **handle**, never card data.

#### Scenario: The card never touches the platform

- **WHEN** a customer enters a card to attach a payment method
- **THEN** the card data is submitted from the browser to Stripe (via Checkout or the Payment Element), not to the
  platform
- **AND** the platform stores only the Stripe handle, and no request or log on the platform contains card data.

#### Scenario: The stored value can never be a card number

- **WHEN** a payment-method or customer handle is stored on the account
- **THEN** `account.NewHandle` refuses any value that is a Luhn-valid PAN
- **AND** the account holds a provider handle only.

### Requirement: A customer SHALL be able to subscribe, upgrade, and downgrade by plan name, with the entitlement flipping at the plan-change event

A customer SHALL be able to subscribe / upgrade / downgrade **by plan name** from the console. On a plan change the
**entitlement SHALL flip at the plan-change event** (audited) and the **money proration SHALL be Stripe's**.

#### Scenario: An upgrade flips the entitlement at the event

- **WHEN** a customer upgrades to a plan by name
- **THEN** the platform updates the Stripe subscription to the new plan's `price_ref` and records an audited plan
  change
- **AND** the entitlement gate reflects the new plan from the plan-change event, while proration is Stripe's.

#### Scenario: A downgrade takes effect without deleting history

- **WHEN** a customer downgrades to a lower plan by name
- **THEN** the entitlement flips at the plan-change event and the change is recorded as a new audited ledger row
- **AND** no prior account, usage, or billing record is deleted.

### Requirement: The console SHALL hold no Stripe secret and the bundle SHALL contain no hardcoded price value

The console SHALL hold **no Stripe secret key** — only a short-lived, server-minted Checkout session URL / client
secret — and the client bundle SHALL contain **no hardcoded price value**. The P7 auto-discovering plan-config
fence SHALL extend to the payment UI.

#### Scenario: No Stripe secret reaches the client

- **WHEN** the billing page initiates checkout
- **THEN** the BFF mints the Checkout session / client secret server-side and the client receives only that
  short-lived value
- **AND** no Stripe API key appears in the client bundle or in any client-visible response.

#### Scenario: No price value is in the bundle

- **WHEN** the client bundle and the payment UI are scanned by the plan-config fence
- **THEN** no priced literal (amount, rate, price band) is found
- **AND** every price shown on the page was read back from the billing API, which reads Stripe, not a constant.

### Requirement: The billing page SHALL render plan by name, usage/SUM, invoices, and payment method, each figure from the API

The console billing page SHALL render the plan **by name**, the current period **SUM and metered usage**, the
**invoice breakdown** (subscription / metered / verified gainshare), and the **payment-method status** — each figure
sourced from the billing/metering API.

#### Scenario: The invoice breakdown is legible and traceable

- **WHEN** a customer opens the billing page for a period
- **THEN** it shows the plan name and the invoice separated into subscription / metered / verified gainshare lines
- **AND** each line names the basis that justified it (the usage record or the verified-delta evidence), with no
  hardcoded figure.

### Requirement: A past-due or payment-failed state SHALL render a named reason and a restore path, driven by the mirrored provider state

A `past_due` or `payment_failed` state SHALL render a **named reason and a restore path** ("update payment to
restore <feature>"), never a bare error, and SHALL be driven by the **mirrored provider state**, not recomputed on
the client.

#### Scenario: Past-due renders a restore path, not a bare error

- **WHEN** the mirrored provider state for a customer is `past_due` or `payment_failed`
- **THEN** the billing page renders a named reason and a path to update payment and restore the affected feature
- **AND** the state shown is the provider's mirrored state, not a value the client computed.

#### Scenario: Loading, empty, and billing-unavailable states are explicit

- **WHEN** the billing data is loading, empty, or the billing provider is temporarily unavailable
- **THEN** the page renders an explicit loading / empty / "billing temporarily unavailable" state
- **AND** it never blocks the rest of the product or renders a blank error.

### Requirement: A payment awaiting customer authentication SHALL be its own state, distinct from a failure

When a payment requires the cardholder to authenticate (SCA / 3-D Secure), the resulting subscription state SHALL
be **mirrored verbatim** from the provider and rendered as its **own** state — carrying the provider's own action
link — distinct from `past_due` and from `payment_failed`. The platform SHALL NOT retry such a payment
automatically, and SHALL NOT describe it as a declined card.

#### Scenario: A card requiring authentication renders a waiting state, not a failure

- **WHEN** a subscription is incomplete because the payment requires customer authentication
- **THEN** the billing page renders a named "your bank needs to confirm this payment" state with the provider's
  action link
- **AND** it is visually and textually distinct from `payment_failed`, and keyboard-reachable.

#### Scenario: An authentication-pending payment is not retried automatically

- **WHEN** a payment is waiting on the cardholder to authenticate
- **THEN** the platform performs no automatic retry of that payment
- **AND** the next action shown belongs to the cardholder, not to the platform.

#### Scenario: A declined card and an authentication-pending payment give different instructions

- **WHEN** the two states are compared
- **THEN** the declined card asks the customer to update their payment method
- **AND** the authentication-pending payment asks the customer to complete authentication — the two messages are
  never merged into one.
