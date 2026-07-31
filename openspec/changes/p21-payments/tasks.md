# Tasks — P21: Stripe Payments

Ordered by workstream. P21 implements the P7 billing abstraction against Stripe and adds payment collection; it
consumes P7's interface, ledger, correction path, Secrets seam, plan model, and entitlement gate **without changing
them**. Each task is independently verifiable. Every PR carries the standard **edition/deployment-form impact
matrix**, and — because this is money — a note on which of the three correctness invariants (idempotent /
persist-then-ack / reversible) it touches.

## 1. Sales Operations + System Designer — Decide the one-way doors first (blocks everything else)
- [x] 1.1 Ratify **D1 (Stripe behind the existing `Provider` interface; interface does not widen)**, **D2
      (Checkout/Element — card never touches the platform)**, and **D7 (opaque price refs; no price in
      code/bundle)** in `design.md`; these are cheap now and expensive later.
- [x] 1.2 Confirm the plan↔`price_ref` mapping is **configuration** (Stripe price IDs in the config store / Stripe),
      not git; extend the P7 auto-discovering plan-config fence (`plancfg/gitfence_test.go`) to cover the payment UI
      / client bundle so a priced literal fails the build.
- [x] 1.3 Write the **commercial honesty** notes: plans by name only; dunning and refund behavior in UI/docs match
      Stripe's actual behavior; the word "风险可控" appears nowhere; no internal profile/bundle/script name in a
      customer-facing billing message.

## 2. Backend — The Stripe provider (implements the existing interface)
- [x] 2.1 Author `internal/billing/stripe.go`: a `stripe.Provider` satisfying `billing.Provider` byte-for-byte —
      `EnsureCustomer`, `CreateSubscription`, `Subscription`, `ReportUsage`, `RaiseCharge`, `IssueCredit`, `Invoice`,
      `RecordedUsage`, `Describe`. Construct it with the Secrets seam and the P7 rollout mode; **do not widen the
      interface**.
- [x] 2.2 Pass the P7-derived idempotency key (`ledger.go` `*IdempotencyKey` helpers) as Stripe's **`Idempotency-Key`**
      on every charge-bearing call; surface `Duplicate=true` when Stripe returns the original.
- [x] 2.3 `CreateSubscription` on the plan's **opaque `price_ref`**; proration Stripe's; **no amount** computed or
      stored. `ReportUsage` reports a **quantity** to a Stripe metered item (multiply nothing).
- [x] 2.4 `IssueCredit` issues an **additive** Stripe credit note / refund against a prior charge; never reduces or
      voids the original.
- [x] 2.5 `Invoice` read-back maps to `billing.Invoice` and passes `Invoice.Validate` (resold-token line rejected,
      every line names a basis); `RecordedUsage` returns Stripe's recorded metered usage for reconciliation.
- [x] 2.6 Map Stripe transport errors to the **outage vs. rejection** split: `ErrProviderUnavailable` on an outage
      (buffer + retry via `FlushPending`), a distinct error on a rejection (stop).

## 3. Backend + DevOps — Secret posture and test/live separation
- [x] 3.1 Wire the Stripe API key and webhook signing secret through the **Secrets seam** under the P7 reserved
      names `SecretBillingAPIKey` / `SecretBillingWebhookSigning`; resolve **fail-closed**; confirm `/readyz`'s
      `secrets_source` covers them.
- [x] 3.2 Separate **test mode and live mode** via the P7 rollout flag (`rollout.go`) whose zero value is **test**; a
      **live** key SHALL NOT resolve for a test surface and a **test** event SHALL move no real money.
- [x] 3.3 Assert **no Stripe secret** is in git / manifest / log / trace / client bundle (build-time scan), and the
      **console holds none** (only a server-minted Checkout session / client secret).

## 4. Backend — The inbound webhook path (verify → dedupe → persist → ack)
- [x] 4.1 Extend `internal/billing/webhook.go` to the real **`Stripe-Signature`** scheme; verify **before** any side
      effect and **before** parsing the body into a decision; reject unsigned / forged / stale-timestamp.
- [x] 4.2 Add the `POST /billing/webhook` route in `internal/api/server.go` (the **one** inbound-from-internet path);
      dedupe on Stripe's **event id** via `webhook_delivery`; a redelivery applies nothing and returns 2xx.
- [x] 4.3 **Persist-then-ack**: persist the dedupe claim and the effect **before** returning 2xx; a persistence
      failure returns **non-2xx** so Stripe retries; a detected gap is reconciled, never dropped. *(load-bearing)*
- [x] 4.4 Mirror subscription-lifecycle events into the provider-owned `BillingState` **verbatim** (no recomputed
      dunning); a `charge.refunded` / dispute webhook authors **no** ledger row (P7 rule preserved).

## 5. Backend — Subscription → entitlement sync (reversible, audited)
- [x] 5.1 On `invoice.paid` / `subscription.updated(active)`: set the account's active plan via `account.SetPlan`
      (pinning the plan config version) + a `TypePlanChange` ledger row; the entitlement gate reflects it.
- [x] 5.2 On `subscription.deleted` / dunning grace-end: degrade to **Free at the period boundary** by an audited
      plan change; **delete nothing**; keep the entitlement through the grace window Stripe is retrying in.
- [x] 5.3 Assert the degradation is **reversible**: a subsequent paid subscription restores the plan by another
      audited plan change, with all `TypePlanChange` rows intact.

## 6. Frontend — Payment collection + the console billing page
- [x] 6.1 BFF (server-side): `POST /billing/checkout-session` mints a **Stripe Checkout** session / client secret;
      the browser is redirected / renders Stripe's Element so the **card goes browser→Stripe directly**; the BFF holds
      the Stripe key, the client never does.
- [x] 6.2 BFF: `POST /billing/plan { plan_name }` for **subscribe / upgrade / downgrade**; the entitlement flips at
      the plan-change event; proration is Stripe's.
- [x] 6.3 Console **billing page** (`web/console/src/app/app/billing`): plan **by name**, current SUM/usage, invoice
      breakdown (subscription / metered / verified gainshare, each with its basis), payment-method status — every
      figure from the billing/metering API, **no** hardcoded price.
- [x] 6.4 First-class **unhappy states**: `past_due` / `payment_failed` render a named reason + restore path (from the
      **mirrored** provider state, not recomputed); explicit loading / empty / billing-unavailable; SUM/usage charts
      follow the **dataviz** skill; all states keyboard-reachable.

## 7. AI Engineer — Gainshare charging preserves the verified-only invariant
- [x] 7.1 A gainshare charge via Stripe SHALL read **only** the P5.5 verified-delta ledger for merged PRs (P7 FR3/
      FR12 preserved); an estimated / un-merged saving raises **no** charge — P21 does not loosen this.
- [x] 7.2 Every gainshare Stripe charge traces through the ledger `evidence` to the verifying delta refs + merge
      commits; keep it gated behind the P7 gainshare flag (P5.5 live + estimated-saving-bills-nothing green).

## 8. QA — The correctness gate (money invariants as machine assertions)
- [x] 8.1 **Contract parity**: the P7 billing suite passes against `stripe.Provider` in test mode exactly as against
      `StubProvider`; every caller compiles and runs against both. *(FR1)*
- [x] 8.2 **Never-double-charge**: retry a charge / redeliver a webhook N times against Stripe test mode → **one**
      Stripe object + **one** ledger row; the recorded-then-lost failure does not double-charge. *(FR2, FR7, FR14)*
- [x] 8.3 **Persist-then-ack** *(load-bearing)*: inject a persistence failure → endpoint returns **non-2xx** so
      Stripe retries; **no** acked-but-unrecorded event exists. *(FR15)*
- [x] 8.4 **Signature / replay**: unsigned, forged, and stale-timestamp webhooks each rejected **before** any side
      effect; a valid redelivery is a 2xx applying nothing. *(FR13, FR14)*
- [x] 8.5 **Entitlement sync**: paid grants the plan; canceled/failed degrades to Free at the boundary by an audited
      plan change; paying restores it — both directions, ledger rows intact. *(FR17)*
- [x] 8.6 **Reversibility** *(load-bearing)*: inject a wrong charge → correct via credit → originals intact, net
      right, no data loss. *(FR5, NFR5)*
- [x] 8.7 **Secret / test-live**: Stripe key + signing secret from the seam, in **no** span/label/log/bundle; a
      **live** key does not resolve for a test surface; default mode moves no real money. *(NFR3, NFR4)*
- [x] 8.8 **Collection / UI**: Stripe Checkout with a test card → subscription active → billing page renders (plan
      name / SUM / invoice breakdown / payment method) → upgrade → downgrade → past-due; card never posts to the
      platform, **no** secret in the bundle, **no** hardcoded price. *(FR8–FR12)*
- [x] 8.9 **Reconciliation / no-resale**: a seeded drift between platform usage and Stripe's recorded usage is
      **surfaced**; **no** invoice line represents resold provider tokens. *(NFR6, `Invoice.Validate`)*

## 9. Product Designer + DevOps — The billing runbook and the inbound-path posture
- [x] 9.1 Document the webhook endpoint as the **one inbound-from-internet path** for the P19 ingress model:
      signature-gated, timestamp-bounded replay window, rate-aware; state the test/live secret wiring.
- [x] 9.2 Write the customer-facing billing/dunning copy honestly: past-due → restore path, downgrade → "takes effect
      at period end", gainshare → "verified and merged" with evidence links; no internal mechanism leaks.

## 10. Documentation & fold-in
- [x] 10.1 Cross-link the PRD, this change, P7, P5.5, P9, P19, and ADR-002; add the P21 row to `docs/prd/README.md`.
- [x] 10.2 On deploy, fold the three delta specs into `openspec/specs/` (drop the `## ADDED` headers).

## 11. Sales Operations + DevOps + Backend — Stripe account prerequisites (what stands between "the code is right" and V1)

V1 is unticked not because the platform is unfinished but because **three artefacts do not exist yet**, and two
of them cannot be produced by this repository. Naming them as a section makes the remaining distance visible
instead of leaving it as a footnote on an unticked box.

- [x] 11.1 **PRD**: document the three prerequisites — a Stripe **test secret key**, a **per-endpoint webhook
      signing secret** (one per mode), and **real Stripe price objects** whose ids replace the placeholder
      `price_ref_*` values, with metered prices denominated in the meter's **integral unit** — as a precondition
      block on §13, an NFR, a risk, and an open question naming who owns creating them.
- [x] 11.2 **OpenSpec**: add the **preflight** requirement to the `stripe-billing-provider` delta and a design
      decision for it; re-fold the capability into `openspec/specs/`.
- [x] 11.3 **Backend**: implement the preflight — resolve **every** configured `price_ref` at the provider and
      name the ones that do not resolve, **before** anything charges; surface the result on the readiness
      signal. A wrong price id must fail at configuration time, not mid-period at the first charge.
- [x] 11.4 **Backend + DevOps**: make a real-account run possible **without a code edit** — `cmd/p21hermes`
      takes `-plans <path>` so a catalog carrying real Stripe price ids can be published from a config store,
      and the API key comes from the **environment or stdin** rather than a command-line flag (a flag puts a
      credential in shell history and in `ps` output).
- [x] 11.5 **Frontend**: a **misconfigured-billing** state on the console billing page — distinct from
      unavailable, empty, and past-due — naming which plan's price reference does not resolve and whose job it
      is to fix it; keyboard-reachable, browser-verified.
- [x] 11.6 **QA + Docs**: the preflight goes red on a bad reference and green on good ones; the env/stdin key
      path is asserted and the flag path refuses; the ingress runbook §4 and the M16 record carry the
      preconditions.

## Verification record

Recorded in [`docs/decisions/p21-m16-exit-checklist.md`](../../../docs/decisions/p21-m16-exit-checklist.md),
item by item, with what each claim rests on.

- [x] V1 M16 exit checklist (PRD §13) green against **one** Stripe test-mode stack (claims simultaneously true).
      **CLAIMED, 2026-07-30**, against `acct_1Ty5Ze…` ("Heros Agent sandbox", US/USD, test mode) over
      `https://api.stripe.com`: `go run ./cmd/p21hermes -repo <hermes> -stripe-base https://api.stripe.com
      -plans <catalog> -customer <fresh>` → **20 steps, 0 failed**, on a customer created by that run so
      no object in it came from an earlier one. The three artefacts §11 named now all exist: a test key,
      a signing secret, and real price objects — the last of which needed the **Q7 unit decision**,
      recorded in [`p21-metered-unit-and-pricing.md`](../../../docs/decisions/p21-metered-unit-and-pricing.md).
      One item is green with a **stated remainder**: nobody has typed a test card into Stripe's own
      Checkout page. The session is real and minted server-side; entering the card is deliberately left
      to a human.
      🔴 Reaching green found **seven defects**, five in shipped code — including a correction
      idempotency key that was not customer-scoped, which could have returned one customer's credit note
      for another customer's correction. All are listed, with what each rests on, in
      [`p21-m16-exit-checklist.md`](../../../docs/decisions/p21-m16-exit-checklist.md).
- [ ] V2 Live-mode cutover gated on the M16 checklist **and** one reconciled test-mode period signed off by Finance
      (PRD Q5); the rollout flag flips to live only then.
      **NOT TAKEN**, and correctly so. The FIRST gate is now satisfied — V1 is green against a real
      Stripe test account — but the second is not: no reconciled test-mode period has been signed off by
      Finance, and the rates configured in the sandbox are a working default chosen to satisfy the unit
      constraint, not a commercial decision anyone has made. The rollout flag stays at its zero value,
      which is test. The cutover sequence is in the ingress runbook §6.
- [x] V3 Edition/deployment-form impact matrix attached to every P21 PR.
      Every commit in this change carries a three-row Personal / Trial / Production matrix with a reason
      on each row, including the `N` rows — an unexplained `N` is the half of the rule that gets skipped.
