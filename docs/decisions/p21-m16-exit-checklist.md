# P21 / M16 exit checklist — the verification record

- **Status:** Recorded (2026-07-30)
- **Checklist:** [`docs/prd/P21-stripe-payments.md`](../prd/P21-stripe-payments.md) §13.
- **Purpose:** say exactly what has been verified, against what, and what has not — so the decision to
  flip to live mode is made on evidence rather than on a green task list.

## The honest headline

Every item below is green against **a real Stripe test account** — `acct_1Ty5Ze…`, "Heros Agent
sandbox", US/USD, test mode — with the platform talking to `https://api.stripe.com` over the wire, on a
customer created by that run. The full flow is **20 steps, 0 failed**.

That was not true this morning. The earlier record said the same items were green against an in-process
Stripe and refused to call that the same thing, and it was right to: moving to the real wire found
**seven defects**, five of them in shipped code (§"What the real account changed"). Two of those would
have failed the first time a customer was charged.

What is still NOT claimed:

- 🔴 **A real test card has not been entered.** The Checkout session is minted server-side and the
  browser is sent to Stripe's own page; nobody has typed `4242…` into it. That is item 3's remaining
  half, and it is left for a human on purpose — it is the one step in this checklist that involves
  putting a card number into a form.
- 🔴 **Live mode is untaken (V2)**, and gated on Finance signing off one reconciled test-mode period.

## Item by item



| # | Checklist item | Status | Evidence |
|---|---|---|---|
| 1 | `stripe.Provider` implements `billing.Provider` without changing the interface; every caller runs unchanged against both | ✅ | `TestContractParity` runs one suite against `StubProvider` and `StripeProvider`; `var _ Provider = (*StripeProvider)(nil)` is the compile-time half |
| 2 | Every charge-bearing call carries the P7 key as Stripe's `Idempotency-Key`; a retry and a redelivery produce one object and one row | ✅ | `TestStripeCarriesTheP7IdempotencyKeyOnEveryChargeBearingCall`, `TestNeverDoubleChargeAcrossBothLayers` (ten retries after a recorded-then-lost failure) |
| 3 | A customer can attach a payment method via Checkout/Element; the card never touches the platform; a handle only is stored | ⚠️ | A **real** `checkout.stripe.com` session is minted server-side against the live test account and asserted to carry no key; the console renders the mirrored method (`visa •••• 4242`, brand and last four only). **Nobody has typed a test card into Stripe's page**, which is item 3's remaining half and the one step deliberately left to a human |
| 4 | Subscribe / upgrade / downgrade by plan name; entitlement flips at the plan-change event; proration is Stripe's | ✅ | `TestCollectionFlowOverStripe`; verified in Chrome — a downgrade round-trips through the BFF and the page reports it |
| 5 | The billing page renders plan by name, SUM/usage, invoice breakdown, payment-method status, with loading / empty / past-due / payment-failed states; no Stripe secret and no price in the bundle | ✅ | `web/console/tests/billing.test.mjs` (15 tests); `npm run build` → `scan-bundle.mjs` reports no credential material, no Stripe secret, no priced literal |
| 6 | The webhook verifies the `Stripe-Signature` before any side effect, processes each event exactly once on Stripe's event id, and persists before it acks | ✅ | `TestStripeSignatureIsVerifiedBeforeAnySideEffect`, `TestRedeliveredStripeEventAppliesNothingAndReturns2xx`, `TestPersistThenAck` (load-bearing) |
| 7 | Subscription lifecycle drives entitlement — paid grants, canceled/failed degrades to Free at the boundary, paying restores; reversible, nothing deleted | ✅ | `TestPaidSubscriptionGrantsThePlanByAnAuditedChange`, `TestDunningGraceWindowKeepsTheEntitlement`, `TestCanceledSubscriptionDegradesToFreeAndDeletesNothing`, `TestDegradationIsReversible` (five transitions, five intact rows) |
| 8 | A billing error is corrected via credit/refund through the additive path; originals intact; net right; no data loss | ✅ | `TestReversibilityOverStripe` — six separate assertions, including that repeating a correction issues the same one |
| 9 | Gainshare bills only verified, merged savings; an estimated / un-merged saving raises no charge | ✅ | `TestGainshareOverStripeBillsOnlyVerifiedMergedSavings` (six ways to fail, each asserting no ledger row **and** no Stripe object) |
| 10 | Stripe key + signing secret from the Secrets seam; in no git/manifest/log/trace/bundle; test and live separated | ✅ | `TestStripeCredentialsComeFromTheSeamUnderTheP7ReservedNames`, `TestALiveKeyDoesNotResolveForATestSurface`, `TestNoStripeSecretInAGitTrackedFile` (whole git index, proven to fire), `scan-bundle.mjs` |
| 11 | Usage is reconcilable against Stripe; a seeded drift is surfaced; no invoice line resells provider tokens | ✅ | Against the real account: platform **432** == Stripe's meter **432.0** for 2026-07, two independently authored figures, compared without writing to either ledger. `TestReconciliationSurfacesDriftAgainstStripe`, `TestNoResoldTokenLineSurvivesTheStripeReadBack`, `TestARederivedSUMDoesNotOverwriteWhatStripeAlreadyRecorded` |
| 13 | Every configured price reference resolves at the provider, is the right SHAPE for its charge kind, and a failure is named by plan / kind / reference | ✅ | Real account, both directions: 8 references resolve (`pricing: verified:8`); a catalog carrying a product id where a price id belongs reports `unresolved:1` on `/readyz`, names *plan Team / metered*, and says how to find the right id. `TestPreflightNamesEveryUnresolvedReference`, `TestPreflightCatchesAnArchivedPrice`, `TestPreflightRejectsAPriceOfTheWrongShape` (3 cases), `TestPreflightStillPassesWhenEveryPriceIsTheRightShape`, `TestPreflightIsReadOnly` |
| 12 | The webhook endpoint is the one documented inbound path, and the platform is never in a customer's production request path | ✅ | [`p21-billing-webhook-ingress.md`](p21-billing-webhook-ingress.md); `TestBillingWebhookRouteIsUnmountedByDefault`, `TestBillingWebhookRouteBoundsTheBody` |

## One constraint the implementation surfaced — now with a unit behind it

Stripe records **whole-unit quantities**. `billing.stripeQuantity` refuses a fractional one rather than
rounding it, because rounding silently changes what a customer is billed and is the hardest kind of
billing bug to find — every individual number looks plausible. Scaling was rejected too: multiplying a
quantity to reach a price is the platform computing money, which the whole design refuses to do.

The remedy was always "denominate the price in the meter's integral unit", and that unit is now
**decided and configured**: one US dollar of spend under management, priced at 3¢ / 2¢ / 1¢ per unit
across Team / Business / Enterprise, and one US dollar of verified merged saving at 20¢ for gainshare.
The reasoning, the rejected alternatives, and what the decision costs are in
[`p21-metered-unit-and-pricing.md`](p21-metered-unit-and-pricing.md).

`cmd/p21hermes` still demonstrates the refusal on purpose: a constraint nobody has seen fire is a
constraint nobody believes.

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

### Round two (2026-07-30, later) — the Q7 unit decided, and the last three steps unblocked

With the unit decided the three metered prices and the gainshare price were created, and the run went
from 14/17 to **20 steps, 0 failed**. Getting there found five more defects, every one of them invisible
to an in-process Stripe because the fake had been written to match what the provider already did:

| What the real account taught | Outcome |
|---|---|
| Under the pinned version a metered price **must be meter-backed**, so the legacy `usage_records` pair the provider used cannot have a subscription item to report against | **Product bug, fixed.** `ReportUsage` now posts a **meter event**, `RecordedUsage` reads **meter event summaries**. `action=set` is gone; convergence comes from a deterministic `identifier`, and the one way that is weaker is pinned by `TestARederivedSUMDoesNotOverwriteWhatStripeAlreadyRecorded` rather than left in a comment. |
| `/v1/invoiceitems` no longer takes `price` — *"Received unknown parameter: price. Did you mean pricing?"* — and accepts **only** `type=one_time` | **Product bug, fixed.** `pricing[price]`, and the preflight now checks price **shape** per charge kind, so a recurring price configured as metered fails at configuration time rather than at the period's first charge. |
| A credit note needs a **quantity** and the **invoice LINE id** — the item id is refused outright, and the two ids are unrelated | **Product bug, fixed.** The quantity is read back from Stripe's own line (never the platform's belief about it), and the line is found through `parent.invoice_item_details`. |
| A credit note cannot be issued against a **draft** invoice | **Product bug, fixed.** `IssueInvoice` finalizes with `auto_advance=false` — the invoice becomes real, and nothing is collected. Making the fake start invoices as drafts turned **four green tests red**, which is the whole argument for a faithful fake. |
| Two runs under **different customers** produced the **same correction idempotency key** | 🔴 **The worst of the seven, fixed.** `CorrectionIdempotencyKey` was the only charge-bearing key that was not customer-scoped; it rested entirely on ledger event ids being unique deployment-wide. Where they are not, one customer's credit note is returned for another customer's correction and neither ledger looks wrong afterwards. Stripe's refusal is what surfaced it. `TestEveryChargeBearingIdempotencyKeyIsCustomerScoped` now lines all five keys up so the odd one out cannot hide again. |
| Meter events are refused more than **35 days** after their timestamp | **Operational limit, refused not worked around.** `ErrUsagePeriodTooOld`: restamping to `now` would attribute an old period's usage to this month and nothing downstream would look wrong. A period must be reported during it, or within ~4 days of its close. |
| Stripe aggregates meter events **asynchronously** (~40–50s observed) | **Handled visibly.** The demo waits, bounded, and reports how long it waited; if the summary never arrives the step **fails** rather than reporting an empty read as agreement. |

What the run leaves in the account, all created by that run and all verifiable:

```
invoice      in_1TytwdL7abkWCmrInJgdhIYr   status=paid  total=1296 usd
  line       il_1TytwdL7abkWCmrI77svsxQ4   qty=432  amount=1296
             price=price_1TytR8L7abkWCmrIIVcnDXx8   (Team metered, 3c per USD of SUM)
             metadata platform_kind=metered platform_period=2026-07
                      platform_basis=usage_record:cus_nousresearch_v8/2026-07/sum
credit note  cn_1TytwgL7abkWCmrIr4cjwErh   invoice=in_1Tytwd…  total=1296  (original intact)
meter        heros_sum / 2026-07           aggregated_value=432.0
```

432 USD of spend under management × 3¢ = **$12.96**, and the platform sent the number 432 and nothing
else. Screenshots of the console rendering it, the two run transcripts, and the raw Stripe read-back are
in `~/Downloads/p21-stripe-verification-20260730/`.

The **misconfigured** path was exercised against the same real account with a catalog carrying a product
id where a price id belongs: `/readyz` reports `pricing: unresolved:1`, the preflight names *plan Team /
metered* and how to find the right id, the metered charge then fails at Stripe with "No such price" —
proving the preflight caught earlier exactly what would otherwise have surfaced at period close — and
the console renders a first-class misconfigured state that names no internal mechanism.

## Live-mode cutover (V2) — NOT taken

Gated on **both** (PRD Q5): this checklist green against a real Stripe test account, **and** one
reconciled test-mode billing period signed off by Finance. Neither has happened, so the rollout flag
stays at its zero value, which is test.

The sequence when both are satisfied is in
[`p21-billing-webhook-ingress.md`](p21-billing-webhook-ingress.md) §6.
