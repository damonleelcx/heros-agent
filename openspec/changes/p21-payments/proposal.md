## Why

P7 ([`p7-billing-metering`](../archive/2026-07-23-p7-billing-metering/)) built the billing **abstraction** and shipped it behind a
stub. It defined the [`billing.Provider`](../../../internal/billing/provider.go) interface (subscriptions,
metered usage, invoices, credits/refunds), the append-only `billing_event` ledger with its write-ahead and
idempotency discipline, the additive-correction path, reconciliation, the `providergateway.Secrets` seam for
billing credentials, the plan model with **opaque price references**, and the plan × automation-level entitlement
gate — all tested against an in-process [`StubProvider`](../../../internal/billing/stub.go) that faithfully models
a real processor's idempotency and failure shapes. What P7 deliberately did **not** do is talk to a real payment
processor, or **collect a payment method**: a `StubProvider` will happily invoice a customer who has no card on
file.

Three gaps are structural and block the first real dollar. **(1) There is no real processor** — `provider.go`
declares the interface and `stub.go` implements it in-process, but nothing implements it against Stripe, so the
platform cannot charge anyone. **(2) There is no payment collection** — `EnsureCustomer` returns a handle and
`CreateSubscription` places a customer on a `price_ref`, but no Stripe Checkout / Payment Element captures a card,
so a subscription has no instrument behind it. **(3) The webhook path is scaffolded, not wired to reality** —
[`webhook.go`](../../../internal/billing/webhook.go) verifies a signature, dedupes on `provider_event_id`, and
mirrors state, but with the *stub's* scheme and no inbound HTTP endpoint taking a real `Stripe-Signature`, and —
the load-bearing rule — **an HTTP 200 returned to Stripe is not proof the event was recorded**: a handler that
acks before it durably persists turns a redelivery into a *lost* event, because Stripe stops retrying what it
thinks succeeded. And nothing yet **syncs Stripe's subscription state to entitlements**, so a customer who stops
paying keeps what they bought and a customer who pays does not get it.

P21 delivers the concrete Stripe integration of the P7 abstraction — **the interface does not move; Stripe is one
implementation behind it** (八级法则 可演进, a second processor must be expressible without touching callers) —
plus the payment collection P7 left out. It is downstream of P7 (the abstraction), P5.5 (gainshare's only source,
which P21 does not loosen), P9 (the console the billing page lands in), P19 (the ingress the webhook endpoint sits
on), and ADR-002 (billing is platform-internal commerce; the platform is never in a customer's production request
path). Product rationale: [`../../../docs/prd/P21-stripe-payments.md`](../../../docs/prd/P21-stripe-payments.md).

## What Changes

- **New capability `stripe-billing-provider`.** A real `stripe.Provider` implements the **existing**
  `billing.Provider` interface without changing it (`EnsureCustomer`, `CreateSubscription`, `Subscription`,
  `ReportUsage`, `RaiseCharge`, `IssueCredit`, `Invoice`, `RecordedUsage`, `Describe`), so every existing caller —
  the charge protocol in `service.go`, the correction path, the reconciler — runs **unchanged**. Every
  charge-bearing call passes the P7-derived idempotency key as Stripe's **`Idempotency-Key`** header (never
  double-charge, two layers); subscriptions use the plan's **opaque `price_ref`** with **proration Stripe's**;
  metered usage reports a **quantity** to a Stripe metered item (the provider computes no amount); credits/refunds
  are **additive** Stripe credit notes, never a reduction of the original; `Invoice` read-back passes
  `Invoice.Validate` (a resold-token line is rejected); and the **outage vs. rejection** split is preserved so the
  P7 buffer and `FlushPending` recovery work unchanged. A **second processor** remains a second implementation of
  the same interface.
- **New capability `payment-collection`.** Stripe **Checkout / Payment Element** captures a payment method such
  that **card data goes from the browser to Stripe directly** and never through the platform (PCI scope stays with
  Stripe; the platform holds handles only). A self-serve **subscribe / upgrade / downgrade by plan name** flow
  flips the **entitlement at the plan-change event** while **money proration is Stripe's**. The console **billing
  page** renders plan **by name**, current SUM/usage, the invoice breakdown (subscription / metered / verified
  gainshare), and payment-method status, with first-class **loading / empty / past-due / payment-failed** states.
  The console holds **no Stripe secret** (only a server-minted Checkout session / client secret) and the bundle
  contains **no hardcoded price value** — the P7 plan-config git fence is extended to the payment UI.
- **New capability `billing-webhooks`.** The one inbound-from-internet endpoint verifies the **`Stripe-Signature`**
  header against the signing secret from the Secrets seam **before any side effect and before parsing the body
  into a decision**; processes each event **exactly once** keyed on **Stripe's event id**; and **persists the
  dedupe claim and the effect before it returns 2xx** — a persistence failure returns non-2xx so Stripe
  **retries**, and a gap is reconciled, never silently dropped. Subscription-lifecycle events are **mirrored
  verbatim** into the provider-owned state the UI renders, and drive **entitlements**: an `active`/paid
  subscription **grants** the plan; a `canceled`/failed one **degrades to Free at the period boundary** by an
  **audited plan change** (`account.SetPlan` + a `TypePlanChange` ledger row), **never** by deleting data —
  reversible, so paying restores the plan. A `charge.refunded` / dispute webhook authors **no** ledger row on its
  own (a webhook is a notification, not an authorization to write the ledger).

- **Real-account operation, added to all three capabilities after the integration went green against a real
  Stripe *test* account (2026-07-30).** Five behaviours only a real account produces, and one property only a
  real account has. **(a) Mode is a property of every artefact, not a flag on one** — key, webhook endpoint,
  signing secret, customer handle and **price id** are separate objects per mode; the preflight runs in the
  mode the deployment is running in, and every readiness line naming Stripe names its mode. **(b) Customer
  authentication (SCA / 3-D Secure) is a state, not a failure** — a `requires_action` subscription is mirrored
  verbatim and rendered as its own waiting-on-your-bank state with Stripe's action link, never folded into
  `payment_failed` and never auto-retried. **(c) A 429 is an outage, a decline is a rejection** — rate limits
  and lock contention buffer and retry; declines stop. **(d) The webhook endpoint is registered per mode with
  an enumerated event set**, and an event type the platform does not handle is acked as understood-and-ignored
  rather than 5xx'd. **(e) A dispute moves money the platform did not author** — the webhook still writes no
  ledger row, and reconciliation surfaces the movement as a **named divergence** for a human to close through
  the audited credit path. And the property: **the live cutover is one-way for money** — the rollout flag is
  reversible, a charge is not, so the documented rollback for money already moved is an additive correction.

**No breaking changes to the P7 abstraction.** The `Provider` interface, the ledger, the correction path, the
entitlement gate, the Secrets seam, and the plan model are **consumed, not modified**. The stub remains for tests
and the demo. P21 slots a concrete implementation behind the interface and adds a collection surface in front and
an endpoint behind.

## Impact

- **Affected capabilities:** new — `stripe-billing-provider`, `payment-collection`, `billing-webhooks`. Consumes
  (does not modify) `billing` (P7 `Provider`/ledger/correction/reconcile), `entitlements`, `metering`, the P7
  plan model + Secrets seam, `web-console`/`console-bff` (P9), and the P19 ingress.
- **Affected code / systems:** `internal/billing/` (new `stripe.go` implementing `Provider`; the webhook endpoint
  wired to a real `Stripe-Signature`; the entitlement-sync path on the webhook result); `internal/api/server.go`
  (the `POST /billing/webhook` route + `secrets_source` already covers the billing secrets); the console BFF (a
  `POST /billing/checkout-session` and `POST /billing/plan`, holding the Stripe key server-side); the customer
  console (a billing page under the authenticated app area); the plan↔`price_ref` mapping as configuration; the
  deploy runbook (the one inbound path, test/live wiring). For the real-account increment: the preflight and the
  readiness line gain a **mode**; the transport error mapping gains the rate-limit vs. decline split; the billing
  page gains an **authentication-required** state; the webhook route gains an enumerated event set and an
  ack-and-ignore path; reconciliation gains a named **dispute** divergence; and the ingress runbook gains the
  ordered live-cutover sequence with its one-way note.
- **Dependencies:** upstream — **P7** (the abstraction P21 implements — hard dependency), **P5.5** (gainshare's
  only source, not loosened), **P9** (console), **P19** (ingress + test/live secret wiring), **ADR-002**
  (platform never in a customer path). Unblocks — **M16: real payments live** (the subscription + metered +
  gainshare revenue P7 specified can now actually move through Stripe, idempotently and reversibly).
- **Explicitly out of scope (owned elsewhere):** metering/SUM derivation (P7); gainshare policy + verified-delta
  computation (P7/P5.5); plan definitions/limits/prices (config + Stripe, not git); tax law + ASC 606 + dunning
  *policy copy* (Stripe + Finance); a **second** payment processor (the seam stays open, but building it is future
  work); storing raw card numbers / a custom processor (never); reselling provider tokens (never — the closed
  `ChargeKind` enum and `Invoice.Validate` already forbid it).
