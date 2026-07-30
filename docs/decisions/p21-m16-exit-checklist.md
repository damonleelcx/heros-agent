# P21 / M16 exit checklist — the verification record

- **Status:** Recorded (2026-07-30)
- **Checklist:** [`docs/prd/P21-stripe-payments.md`](../prd/P21-stripe-payments.md) §13.
- **Purpose:** say exactly what has been verified, against what, and what has not — so the decision to
  flip to live mode is made on evidence rather than on a green task list.

## The honest headline

Every item below is **green against an in-process Stripe** (`internal/stripefake`) that implements
Stripe's wire contract: form-encoded requests, JSON objects, bearer auth with a pinned API version, real
idempotent replay, the key-reuse refusal, an outage mode and the ambiguous recorded-then-lost failure.

🔴 **That is not the same as a real Stripe test account, and this record does not claim it is.** The
checklist's own words are *"against one Stripe test-mode stack"*, and a stack whose Stripe is in this
process has not exercised Stripe's real API version, its real error envelopes, its real idempotency
window, or its real webhook delivery.

What is left is **three artefacts** (PRD §10.1), not one command: a Stripe **test secret key**, a
**webhook signing secret** for that endpoint, and **real price objects** whose ids replace the
placeholder `price_ref_*` values, with metered prices denominated in the meter's integral unit. Two of
them are credentials this repository must never contain; the third is configuration only the Stripe
account owner can create.

With all three in hand it is one command, and it needs no code edit:

```bash
export STRIPE_API_KEY=<your Stripe TEST key>     # not a flag: a flag lands in shell history and in ps
go run ./cmd/p21hermes -repo /path/to/hermes-agent \
  -stripe-base https://api.stripe.com -plans ./your-catalog.json
```

The **preflight** runs first and tells you whether artefact (C) is right before anything charges: it
resolves every configured reference and names each failure by plan, kind and reference. `/readyz`
reports the same as `billing_provider.pricing`. Running it is the cheapest possible check that the
account is ready, and `-break-price` shows what it looks like when it is not.

Everything the platform side can prove is proven. What is left is the half only Stripe can answer.

## Item by item

| # | Checklist item | Status | Evidence |
|---|---|---|---|
| 1 | `stripe.Provider` implements `billing.Provider` without changing the interface; every caller runs unchanged against both | ✅ | `TestContractParity` runs one suite against `StubProvider` and `StripeProvider`; `var _ Provider = (*StripeProvider)(nil)` is the compile-time half |
| 2 | Every charge-bearing call carries the P7 key as Stripe's `Idempotency-Key`; a retry and a redelivery produce one object and one row | ✅ | `TestStripeCarriesTheP7IdempotencyKeyOnEveryChargeBearingCall`, `TestNeverDoubleChargeAcrossBothLayers` (ten retries after a recorded-then-lost failure) |
| 3 | A customer can attach a payment method via Checkout/Element; the card never touches the platform; a handle only is stored | ⚠️ | `TestCollectionFlowOverStripe` mints a session server-side and asserts no key in it; the browser half is verified in Chrome against `cmd/p21hermes`. **Not yet driven with a real Stripe test card**, which is item 3's remaining half |
| 4 | Subscribe / upgrade / downgrade by plan name; entitlement flips at the plan-change event; proration is Stripe's | ✅ | `TestCollectionFlowOverStripe`; verified in Chrome — a downgrade round-trips through the BFF and the page reports it |
| 5 | The billing page renders plan by name, SUM/usage, invoice breakdown, payment-method status, with loading / empty / past-due / payment-failed states; no Stripe secret and no price in the bundle | ✅ | `web/console/tests/billing.test.mjs` (15 tests); `npm run build` → `scan-bundle.mjs` reports no credential material, no Stripe secret, no priced literal |
| 6 | The webhook verifies the `Stripe-Signature` before any side effect, processes each event exactly once on Stripe's event id, and persists before it acks | ✅ | `TestStripeSignatureIsVerifiedBeforeAnySideEffect`, `TestRedeliveredStripeEventAppliesNothingAndReturns2xx`, `TestPersistThenAck` (load-bearing) |
| 7 | Subscription lifecycle drives entitlement — paid grants, canceled/failed degrades to Free at the boundary, paying restores; reversible, nothing deleted | ✅ | `TestPaidSubscriptionGrantsThePlanByAnAuditedChange`, `TestDunningGraceWindowKeepsTheEntitlement`, `TestCanceledSubscriptionDegradesToFreeAndDeletesNothing`, `TestDegradationIsReversible` (five transitions, five intact rows) |
| 8 | A billing error is corrected via credit/refund through the additive path; originals intact; net right; no data loss | ✅ | `TestReversibilityOverStripe` — six separate assertions, including that repeating a correction issues the same one |
| 9 | Gainshare bills only verified, merged savings; an estimated / un-merged saving raises no charge | ✅ | `TestGainshareOverStripeBillsOnlyVerifiedMergedSavings` (six ways to fail, each asserting no ledger row **and** no Stripe object) |
| 10 | Stripe key + signing secret from the Secrets seam; in no git/manifest/log/trace/bundle; test and live separated | ✅ | `TestStripeCredentialsComeFromTheSeamUnderTheP7ReservedNames`, `TestALiveKeyDoesNotResolveForATestSurface`, `TestNoStripeSecretInAGitTrackedFile` (whole git index, proven to fire), `scan-bundle.mjs` |
| 11 | Usage is reconcilable against Stripe; a seeded drift is surfaced; no invoice line resells provider tokens | ✅ | `TestReconciliationSurfacesDriftAgainstStripe`, `TestNoResoldTokenLineSurvivesTheStripeReadBack` |
| 13 | Every configured price reference resolves at the provider before anything charges, and a failure is named by plan / kind / reference | ✅ | `TestPreflightNamesEveryUnresolvedReference`, `TestPreflightCatchesAnArchivedPrice`, `TestPreflightDistinguishesAnOutageFromAMisconfiguration`, `TestPreflightIsReadOnly`; reported on `/readyz` as `billing_provider.pricing`, and rendered as a first-class misconfigured state in the console |
| 12 | The webhook endpoint is the one documented inbound path, and the platform is never in a customer's production request path | ✅ | [`p21-billing-webhook-ingress.md`](p21-billing-webhook-ingress.md); `TestBillingWebhookRouteIsUnmountedByDefault`, `TestBillingWebhookRouteBoundsTheBody` |

## One constraint the implementation surfaced, and did not paper over

Stripe records **whole-unit quantities**. `billing.stripeQuantity` refuses a fractional one rather than
rounding it, because rounding silently changes what a customer is billed and is the hardest kind of
billing bug to find — every individual number looks plausible. Scaling was rejected too: multiplying a
quantity to reach a price is the platform computing money, which the whole design refuses to do.

The remedy is Stripe-side configuration, not code: denominate the price in the meter's integral unit so
the reported quantity is a whole number of those units. **This is a live-mode prerequisite** — Finance
must confirm each metered price is denominated that way before the cutover, or the first fractional SUM
of the period will be refused (loudly, which is the intended failure, but it will be refused).

`cmd/p21hermes` demonstrates the refusal on purpose: a constraint nobody has seen fire is a constraint
nobody believes.

## The run against a real repository

`cmd/p21hermes` drives the whole path against a real checkout of
[nousresearch/hermes-agent](https://github.com/nousresearch/hermes-agent).

```
checkout:  nousresearch/hermes-agent @ 937222f4e
provider:  stripe(test)  (in-process Stripe — no credential, no real money)
rollout:   billing=enabled provider_mode=test gainshare=enabled auto_merge_entitlement=enabled
result:    18 step(s), 0 failed
```

Real in that run: the repository (its files are checked and its HEAD is what the gainshare evidence
points at — deleting one of the named source files makes the demo report it), and the entire P21 code
path end to end. Stubbed: the per-node spend figures, which the demo says in its own output rather than
letting a reader assume otherwise.

What the run demonstrates, in order: a Stripe customer handle (never a card) → a server-minted checkout
session → the payment method mirrored from a webhook (brand and last four only) → `invoice.paid`
granting the plan by an audited change → a redelivery applying nothing → a forged delivery rejected
before any parse → a subscription on the plan's opaque price reference → the period's SUM reported as a
quantity → a metered charge that stays one charge across a retry → a fractional quantity **refused**
rather than rounded → an additive credit with the original intact → an invoice read back with every line
naming its basis → reconciliation against Stripe's recorded usage → the dunning grace window keeping the
entitlement → the boundary degrading to Free without deleting anything → paying restoring it → and the
gainshare invariant, where **the larger verified saving is the one that bills nothing** because it was
never merged.

The console renders it: the billing page against this run shows the plan by name, the period's SUM and
usage, the invoice line with its basis, the payment method, and the verified-savings table in which
`handle_max_iterations` carries the period's largest saving and the words *not billed*.

## Against a REAL Stripe test account (2026-07-30)

Run against Stripe's own API with a test key and a catalog carrying that account's real price ids
(`acct_1Ty5Ze…`, "Heros Agent sandbox", US/USD, test mode). **14 of 17 steps green.** What it changed
about this record:

| What the real account taught | Outcome |
|---|---|
| The pinned API version was too old — Checkout refused under `2024-06-20` | **Product bug, fixed.** Pin moved to `2025-03-31.basil` on both the provider and the fake, which state the wire independently, so the suite went red until both moved. An in-process Stripe answers whatever version it is told; only the wire can invalidate a pin. |
| Stripe **replays cached ERRORS** for a repeated idempotency key | **Operational finding, documented** in the ingress runbook. A call that failed for a since-fixed reason keeps failing until the key rotates; the key carries the plan config version, so republishing the catalog rotates it. |
| The configuration held **product** ids where price ids belong | **Diagnostic added.** The preflight names it and says how to find the right id, instead of relaying Stripe's "no such price". |
| Two demo steps reported **vacuous passes** — "0 invoice lines, every one names its basis" | **Fixed.** Both now report NOTHING TO CHECK, which is a fail, because a claim about an empty set is not evidence. |
| A paid subscription cannot be created for a customer with no payment method | **Correct behaviour on both sides**, now reported as a stated condition: in the real flow Checkout creates the subscription, after the card is entered on Stripe's page. |

Still blocked on the account's configuration, not on code: **no metered price and no gainshare price
exist**, so metered reporting, the metered charge, invoice read-back and reconciliation have nothing to
run against. They report NOTHING TO CHECK rather than passing. Creating those prices needs the Q7 unit
decision — the platform reports a whole-unit quantity and refuses to round one.

## Live-mode cutover (V2) — NOT taken

Gated on **both** (PRD Q5): this checklist green against a real Stripe test account, **and** one
reconciled test-mode billing period signed off by Finance. Neither has happened, so the rollout flag
stays at its zero value, which is test.

The sequence when both are satisfied is in
[`p21-billing-webhook-ingress.md`](p21-billing-webhook-ingress.md) §6.
