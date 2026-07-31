# Billing Webhooks — Spec Delta (P21)

Product rationale: [`../../../../docs/prd/P21-stripe-payments.md`](../../../../docs/prd/P21-stripe-payments.md)
§6 (FR13–FR18) and §7. Architecture decisions: [`../../design.md`](../../design.md) Decisions 3, 5. Inherits
[ADR-002](../../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (the platform is never in a
customer's production request path). Extends the P7 webhook handler
([`webhook.go`](../../../../internal/billing/webhook.go)) — verify → dedupe → apply — to a real `Stripe-Signature`
scheme and wires the result to the entitlement gate via the audited plan-change path
([`account.SetPlan`](../../../../internal/account/account.go), `TypePlanChange` in
[`ledger.go`](../../../../internal/billing/ledger.go)).

Covers the **real inbound path**: Stripe signature verification before any side effect, exactly-once processing on
Stripe's event id, **persist-then-ack** (an HTTP 200 is not proof of record), verbatim state mirroring, and the
subscription-lifecycle → **entitlement** sync — reversible, audited, never a delete.

> This is the one inbound-from-internet path — the mirror of P19's egress allowlist. It moves state only after it
> has verified the signature and durably claimed the delivery; an HTTP 200 to Stripe means "durably ours", never
> "received". A webhook is a notification, not an authorization to write the billing ledger.

## ADDED Requirements

### Requirement: The webhook endpoint SHALL verify the Stripe signature before any side effect and before parsing the body into a decision

The inbound endpoint SHALL verify the `Stripe-Signature` header against the signing secret from the Secrets seam
**before any side effect** and **before parsing the body into a decision**. An unsigned, forged, or
stale-timestamp payload SHALL be **rejected before it moves one byte of state**.

#### Scenario: An unsigned or forged payload is rejected before any side effect

- **WHEN** a webhook arrives with no signature or a signature that does not verify against the signing secret
- **THEN** the endpoint rejects it before parsing the body into a decision and before any state change
- **AND** no ledger row, no dedupe claim, and no state mirror is written.

#### Scenario: A stale-timestamp payload is rejected as a possible replay

- **WHEN** a webhook's signed timestamp is outside the accepted skew window
- **THEN** the endpoint rejects it so a captured payload cannot be replayed indefinitely
- **AND** no side effect occurs.

#### Scenario: The signing secret is unresolvable

- **WHEN** the signing secret cannot be sourced from the Secrets seam
- **THEN** the endpoint fails closed and rejects the webhook rather than trusting an unverifiable payload
- **AND** the failure is loud, not a silent accept.

### Requirement: Each event SHALL be processed exactly once keyed on Stripe's event id

Each event SHALL be processed **exactly once** keyed on Stripe's **event id**: the endpoint claims the delivery in
the `webhook_delivery` dedupe table, and a **redelivery SHALL apply nothing** and return 2xx.

#### Scenario: A redelivered event applies nothing

- **WHEN** an event with an already-claimed Stripe event id is redelivered
- **THEN** the endpoint recognizes it as a duplicate, applies no side effect, and returns 2xx
- **AND** Stripe stops retrying because it received a success.

#### Scenario: A first delivery is claimed before it is applied

- **WHEN** an event's Stripe event id is seen for the first time
- **THEN** the endpoint claims the delivery in `webhook_delivery` before applying the effect
- **AND** two concurrent redeliveries cannot both proceed (the claim is atomic).

### Requirement: The endpoint SHALL persist the dedupe claim and the effect before returning 2xx — an HTTP 200 is not proof of record

The endpoint SHALL **persist the dedupe claim and the effect before it returns 2xx**. An HTTP 200 SHALL NOT be
returned for an event that was not durably recorded; a failure to persist SHALL return a **non-2xx** so Stripe
retries, and a detected gap SHALL be reconciled, never silently dropped.

#### Scenario: A persistence failure returns non-2xx so Stripe retries

- **WHEN** the endpoint cannot durably persist the dedupe claim or the effect
- **THEN** it returns a non-2xx status
- **AND** Stripe retries the event later, so no event acked-but-unrecorded is lost.

#### Scenario: A 2xx is returned only after the event is durably recorded

- **WHEN** the endpoint returns 2xx for an event
- **THEN** the dedupe claim and the applied effect are already durable
- **AND** a redelivery of that event finds the claim and applies nothing.

### Requirement: Subscription-lifecycle events SHALL be mirrored verbatim into the provider-owned state the UI renders

Subscription-lifecycle events (`customer.subscription.updated/deleted`, `invoice.paid`,
`invoice.payment_failed`, `customer.subscription.past_due`) SHALL be mirrored into the provider-owned
`BillingState` the UI renders, **verbatim** — the platform reflects Stripe's words and never recomputes dunning.

#### Scenario: Provider state is mirrored, not recomputed

- **WHEN** an `invoice.payment_failed` or `past_due` event is applied
- **THEN** the platform records the provider's status verbatim in the mirrored `BillingState`
- **AND** the platform does not derive its own dunning state or translate the provider's status into a different
  vocabulary.

### Requirement: Subscription lifecycle SHALL drive entitlements by an audited, reversible plan change, never by deleting data

An `active`/paid subscription SHALL **grant** its plan's entitlements; a `canceled`/failed one SHALL **degrade to
Free at the period boundary** (or per the dunning schedule) by an **audited plan change** (`account.SetPlan` plus a
`TypePlanChange` ledger row) — **never** by deleting the account or its history. The change SHALL be **reversible**:
paying restores the plan.

#### Scenario: A paid subscription grants the plan's entitlements

- **WHEN** an `invoice.paid` / `subscription.updated(active)` event is applied for a plan
- **THEN** the account's active plan is set to that plan via an audited plan change
- **AND** the entitlement gate reflects the plan's entitlements.

#### Scenario: A canceled subscription degrades to Free at the boundary, reversibly

- **WHEN** a `subscription.deleted` event (or the dunning grace-window end) is applied
- **THEN** the account is moved to the Free tier by an audited plan change at the period boundary, and no account,
  usage, or billing record is deleted
- **AND** a subsequent paid subscription restores the plan by another audited plan change.

### Requirement: A refund or dispute webhook SHALL NOT author a ledger row on its own

A `charge.refunded` / `charge.dispute.created` webhook SHALL **not** author a billing-ledger row on its own — a
webhook is a notification, not an authorization to write the ledger. The money movement SHALL be recorded through
the audited `Credit`/`Refund` path only.

#### Scenario: A refund webhook mirrors state but writes no ledger row

- **WHEN** a `charge.refunded` or `charge.dispute.created` webhook is applied
- **THEN** the platform mirrors the resulting provider state
- **AND** it authors no billing-ledger row from the webhook — any correction is recorded through the audited
  `Credit`/`Refund` path.

### Requirement: The webhook endpoint SHALL be the one inbound path and SHALL NOT place the platform in a customer's production request path

The Stripe webhook endpoint SHALL be the single documented inbound-from-internet path — signature-gated,
timestamp-bounded, and rate-aware — and no artifact SHALL place the platform (or Stripe) in a customer's production
request path (ADR-002); billing is platform-internal commerce.

#### Scenario: The billing path does not depend on, or sit in, a customer's production path

- **WHEN** the billing/webhook path is unavailable
- **THEN** a customer's transformed program continues calling its own providers unaffected
- **AND** no deploy artifact routes customer production traffic through the platform's billing path.

### Requirement: The webhook endpoint SHALL be registered per mode with an enumerated event set, and an unhandled event type SHALL be acked rather than failed

Each mode SHALL have its **own** registered endpoint with **its own** signing secret and an **explicit, enumerated
event-type subscription**, recorded in the ingress runbook with its URL and mode. An event whose type the platform
does not handle SHALL be **acked as understood-and-ignored** — a 2xx that applies nothing — rather than returning
a 5xx that fills the provider's retry queue.

#### Scenario: A test endpoint's signing secret does not verify a live delivery

- **WHEN** two endpoints exist, one per mode
- **THEN** each has its own signing secret, and a delivery signed for one does not verify against the other
- **AND** no deployment shares one signing secret across modes.

#### Scenario: An unhandled event type is acked, not failed

- **WHEN** the provider delivers an event whose type the platform does not handle
- **THEN** the endpoint returns 2xx and applies nothing
- **AND** no retry queue accumulates for events nobody asked for.

### Requirement: A dispute SHALL author no ledger row and SHALL surface as a named divergence

A dispute or chargeback moves money the platform did not author. The webhook SHALL mirror the state and SHALL
author **no** billing-ledger row (the P7 rule stands). The movement SHALL surface through reconciliation as a
**named** divergence, so it is closed by a human through the audited credit/refund path rather than absorbed
silently.

#### Scenario: A dispute webhook writes no ledger row

- **WHEN** a `charge.dispute.created` event is delivered and applied
- **THEN** the provider-owned state is mirrored
- **AND** no billing-ledger row is authored by the webhook.

#### Scenario: The resulting disagreement is named, not absorbed

- **WHEN** reconciliation compares the platform's records against the provider's after a dispute
- **THEN** the divergence is surfaced with the customer, period and amount it concerns
- **AND** it is never resolved by overwriting either record.
