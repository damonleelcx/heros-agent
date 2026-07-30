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
window, or its real webhook delivery. The remaining step is mechanical and needs a credential this
repository does not and must not contain:

```bash
go run ./cmd/p21hermes -repo /path/to/hermes-agent \
  -stripe-base https://api.stripe.com -api-key <a Stripe TEST key>
```

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

## Live-mode cutover (V2) — NOT taken

Gated on **both** (PRD Q5): this checklist green against a real Stripe test account, **and** one
reconciled test-mode billing period signed off by Finance. Neither has happened, so the rollout flag
stays at its zero value, which is test.

The sequence when both are satisfied is in
[`p21-billing-webhook-ingress.md`](p21-billing-webhook-ingress.md) §6.
