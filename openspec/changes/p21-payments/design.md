# Design — P21: Stripe Payments

Product rationale: [`../../../docs/prd/P21-stripe-payments.md`](../../../docs/prd/P21-stripe-payments.md).
Implements the P7 billing abstraction ([`p7-billing-metering`](../p7-billing-metering/)) against Stripe. Inherits
[ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (the platform is never in a
customer's production request path — billing is unrelated internal commerce),
[ADR-006](../../../docs/adr/ADR-006-console-deploy-packaging.md) (console deploy packaging), and
[ADR-008](../../../docs/adr/ADR-008-console-tenant-identity-seam.md) (console tenant-identity seam). Reuses the P7
code surface verbatim: [`provider.go`](../../../internal/billing/provider.go) (the `Provider` interface),
[`ledger.go`](../../../internal/billing/ledger.go) (append-only + derived idempotency keys),
[`webhook.go`](../../../internal/billing/webhook.go) (verify → dedupe → apply),
[`secrets.go`](../../../internal/billing/secrets.go) (`SecretBillingAPIKey` / `SecretBillingWebhookSigning`),
[`correction.go`](../../../internal/billing/correction.go) (additive credit/refund),
[`entitlement.go`](../../../internal/entitlement/entitlement.go), and
[`plancfg.go`](../../../internal/plancfg/plancfg.go) (`PriceRefs`).

Every decision below is arbitrated on the **八级法则** — the single trade-off law this project uses:

> **安全 > 稳定 > UX > 运维 > 可演进 > 可扩展 > 维护 > 实现**

with its three iron laws: (L1) a higher level's degradation is never traded for a lower level's convenience;
(L2) decide at the highest level that separates the options and do not fall back down for a lower-level
convenience; (L3) 实现 (single-shot implementation cost) is always the floor and never outranks anything above it.

## Context

P21 adds **no new billing concept**. P7 already drew every box: the `Provider` interface, the append-only ledger,
the idempotent webhook handler, the entitlement gate, the plan model with opaque price references, and the rollout
flag whose zero value is test mode. What P7 shipped behind that abstraction is a `StubProvider` — faithful to a
real processor's idempotency and failure contract, but moving no money — and it left one hole: **nothing captures
a payment method.** P21 fills the `Provider` box with Stripe, adds the collection surface in front of it, and
stands up the inbound webhook endpoint behind it.

Three properties from the rulebook are non-negotiable and shape every decision: **money is idempotent — a retry or
replay never double-charges**; **an HTTP 200 to a provider is not proof the effect was recorded — persist before
you ack**; and **corrections are additive — an error is fixed by a credit/refund with audit, never a delete.**
Two more come from P7 and are preserved, not re-opened: **the platform holds provider handles only, never card
data**, and **gainshare bills only verified, merged savings.**

## Ratification — the three one-way doors, decided before any code (task 1.1)

Three of the eight decisions below are **one-way doors**: cheap to hold now, expensive-to-impossible to
recover later. They are ratified here, jointly by Sales Operations (who carries the commercial
consequence) and the System Designer (who carries the architectural one), *before* implementation
starts — which is the only moment ratification is worth anything.

| Door | Ratified as | What "later" costs if it is not held now |
|---|---|---|
| **D1** — Stripe behind the existing `Provider` interface; **the interface does not widen** | **Ratified.** `stripe.Provider` satisfies `billing.Provider` byte-for-byte. A second processor is a second implementation, never a re-plumb. | A Stripe-typed billing package makes "add a second/regional processor" a rewrite of every call site. Procurement asks for this *after* the contract is signed, when the rewrite is least affordable. |
| **D2** — Checkout / Payment Element; **the card never touches the platform** | **Ratified.** Card data goes browser→Stripe. The platform stores a handle, and `account.NewHandle` refuses a Luhn-valid PAN. | A PAN that has been in a log, a span, or a heap dump cannot be un-leaked. PCI scope, once entered, is left by audit, not by a patch. |
| **D7** — **Opaque price refs**; no price value in code, manifest, or bundle | **Ratified.** A concrete price lives in Stripe; the platform holds `plancfg.PlanConfig.PriceRefs` only. | A price in git is in git forever, every environment becomes a place the number drifts, and every price change becomes a deploy. |

**Why these three and not the other five.** D3/D4/D5/D6/D8 are all *correctness* decisions — get one
wrong and a test goes red, or an incident teaches it, and the fix is local. These three are *shape*
decisions: getting one wrong is not caught by a test at all, it is discovered the day someone asks for
the thing the shape forbids. 八级法则 L2 says decide at the highest level that separates the options and
do not fall back down for a lower-level convenience; L2 is exactly what a one-way door demands, so they
are ratified up front rather than deferred to the first PR that trips over them.

Each is enforced structurally, not by review habit — the point of ratifying is that the ratification has
teeth:

- **D1** — a contract-parity suite runs every caller against both `StubProvider` and `stripe.Provider`,
  plus an interface-shape test asserting no Stripe type is reachable from `billing.Provider`.
- **D2** — the BFF mints a Checkout session server-side and no platform route accepts a card field;
  `account.NewHandle`'s PAN refusal is the second, independent guard.
- **D7** — the auto-discovering plan-config fence, extended below to the payment UI (task 1.2).

## Plan ↔ `price_ref` is configuration, not git (task 1.2)

The mapping from a plan to its Stripe price is **configuration**: the Stripe price IDs live in the config
store (and the prices themselves in Stripe, administered by Finance), reached through
[`plancfg.PlanConfig.PriceRefs`](../../../internal/plancfg/plancfg.go). Nothing in git holds a plan
catalog or a priced value, and that claim is a *test*, not a paragraph:
[`plancfg/gitfence_test.go`](../../../internal/plancfg/gitfence_test.go) enumerates the whole git index
every run.

P21 adds one surface the P7 fence could not have anticipated — **the payment UI**, where a price is most
tempting to hardcode "just for display". The fence is therefore extended to cover git-tracked payment/
billing UI sources (`TestNoPricedLiteralInPaymentUI`), and the console keeps its build-time
`scan-bundle.mjs` price scan over the JavaScript the browser actually downloads. Two layers, because they
fail differently: the Go fence catches the literal at commit time in any file, the bundle scan catches
whatever survives a build — including a value that arrived through a dependency.

Both fences are **proven to go red** (`TestPaymentUIPriceDetectorGoesRed`), for the reason P7 states: a
guard that has never been shown to fire is decoration.

## Commercial honesty (task 1.3)

Money is where a product's claims are cashed, so the billing surface carries the strictest honesty rules
in the system. Four, each with the failure it prevents:

1. **Plans are named, never priced, in anything the platform ships.** Free / Team / Business / Enterprise.
   The number is Stripe's and Finance's. A number in a screen, a doc, or a bundle outlives the moment it
   was true and ships anyway.
2. **Dunning and refund behavior described in the UI and the docs match Stripe's actual behavior.** The
   console renders the *mirrored* provider state (D5) — if Stripe says `past_due`, the page says
   past-due, on Stripe's schedule, with Stripe's grace window. The platform does not describe a dunning
   policy it does not implement, and it does not recompute one it does not own.
3. **The phrase "risk is controlled" (风险可控) appears nowhere** — not in the UI, not in the docs, not in
   a support reply. The honest form is *risk is observable*: what the system can actually promise is that
   every charge is idempotent, every correction is additive and audited, and every figure traces to the
   record that justified it. Claiming control over a payment processor's outcomes is a promise made with
   someone else's system.
4. **No internal profile, bundle, script, or component name appears in a customer-facing billing
   message.** A customer reads "your payment could not be processed — update your card to restore
   auto-merge", never a mechanism name. An internal identifier in a billing message is a support ticket
   nobody can answer and a hint nobody needed.

The first is machine-enforced (the fences above). The second is structural (the UI renders mirrored
state and has no branch that computes dunning). The third is machine-enforced too — the console's
`scan-claims.mjs` fails the build on the banned phrase in any shipped file, and it is proven to fire —
because a rule that can be written as a guard should not be written only as a document. The fourth stays
a review responsibility: "no internal mechanism name" is not a fixed string, and a scan that tried to
enumerate one would be a scan somebody disables. It is stated here so a reviewer has something to point
at rather than an opinion to defend, with the checklist in
[`docs/sales/P21-billing-copy.md`](../../../docs/sales/P21-billing-copy.md) §8.

## Decision 1 — Stripe behind the existing `Provider` interface; the interface does not widen

**Chosen:** a `stripe.Provider` that satisfies the **existing** [`billing.Provider`](../../../internal/billing/provider.go)
interface byte-for-byte — same methods, same request/result structs, same error semantics — with the network
swapped in. Every existing caller (`service.go`'s charge protocol, `correction.go`'s credit path, the reconciler)
runs unchanged. A **second** processor is a **second** implementation of the same interface.

**Why (L5 可演进 over L8 实现).** The cheapest first write (L8) would be a Stripe-typed billing package —
`stripeSubscription`, `stripeCharge` — threaded through the callers. L3 says that cannot decide it, and L5 says
why it must not: the property that matters is that a **second processor is future work, not a rewrite**. The stub
was written to be faithful to the `Provider` contract *precisely so that* the real integration is a substitution,
not a re-plumb — the code under test is the shipped code path with the network swapped. Coupling callers to Stripe
would erase that, turning "add a processor" into "touch every call site," which is the one-way door this decision
refuses to walk through. The seam is the interface; P21 fills it and does not widen it.

**Rejected — a Stripe-typed billing package.** Marginally cheaper to write once, catastrophically expensive the
day a customer's procurement requires a second processor or a regional one. Reopens only if a real requirement
proves the interface cannot express a needed operation — and the answer then is to evolve the *interface* (with
the stub updated in lockstep), never to bypass it at a call site.

## Decision 2 — Collection is Stripe Checkout / Payment Element; the card never touches the platform

**Chosen:** a payment method is captured through Stripe **Checkout** (hosted) or the **Payment Element** (embedded),
so **card data goes from the browser to Stripe directly**. The BFF mints a Checkout session / client secret
server-side; the browser is redirected or renders Stripe's own iframe; the platform stores only the resulting
Stripe customer / payment-method **handle**.

**Why (L1 安全).** Card data in the platform's scope is a PCI liability and an irreversible one-way door the moment
it is logged, traced, or persisted. Keeping the card on the browser→Stripe path keeps PCI scope entirely with
Stripe and makes "the platform never stores a card" a *structural* fact, not a policy — the same posture
[`account.NewHandle`](../../../internal/account/account.go) already enforces by refusing a Luhn-valid PAN. The safety
rule and the design agree: the platform does not enter or store financial credentials, and here it cannot, because
the collection surface routes them around it.

**Rejected — collecting the card on a platform form and forwarding it to Stripe.** Pulls PAN into platform scope
for the duration of the request — logs, traces, memory — which is exactly the PCI exposure Checkout exists to
avoid. Non-conformant regardless of "we don't store it."

## Decision 3 — Webhook order is verify → dedupe → persist → ack; an HTTP 200 is not proof

**Chosen:** the inbound endpoint (1) **verifies** the `Stripe-Signature` against the signing secret before a byte
is parsed into a decision, (2) **claims** the delivery in `webhook_delivery` keyed on Stripe's event id, (3)
**persists** the effect, and only then (4) **returns 2xx**. A persistence failure returns **non-2xx** so Stripe
retries. A redelivery is a 2xx that applies nothing.

**Why (L1 安全 / L2 稳定).** Two failure modes converge here and both are silent-money failures. Security: a handler
that parses before verifying has already trusted attacker-controlled bytes to choose a code path, so verification
is step one, before anything (the P7 `HandleWebhook` already does this). Stability: **an HTTP 200 returned to
Stripe is a promise that the event was recorded** — Stripe stops retrying an event it thinks succeeded, so a
handler that acks before it persists converts a redelivery into a *lost* event that will never come back. The only
safe order is persist-then-ack; a persistence failure must surface as a non-2xx *so that Stripe retries*, which is
the whole point of an at-least-once delivery contract. This is the "HTTP 200 ≠ 成功入库" rule at its most
expensive, and the dedupe claim being durable *before* the effect is what makes "exactly once" true rather than
"processed, then noticed it was a duplicate."

**Rejected — ack fast, process asynchronously off a queue.** Tempting for latency (L3/L8), but it separates the ack
from the record: the moment Stripe gets its 200, the durability of the event is the platform's problem alone, and a
queue drop is an invisible lost charge. If async processing is ever needed for throughput, the **claim must still
be persisted synchronously before the ack** — the ack may only ever mean "durably ours now."

## Decision 4 — The idempotency key is the P7-derived key, passed to Stripe

**Chosen:** every charge-bearing Stripe call (`CreateSubscription`, `ReportUsage`, `RaiseCharge`, `IssueCredit`)
carries the P7-derived idempotency key ([`ledger.go`](../../../internal/billing/ledger.go) `*IdempotencyKey`
helpers) as Stripe's **`Idempotency-Key`** header. Two layers refuse the duplicate: the ledger's UNIQUE key (no
second row) and Stripe's own idempotency (no second object).

**Why (L2 稳定).** Never-double-charge must hold under arbitrary retry and the nastiest real case — Stripe recorded
the charge and the response was lost. With the key on both layers, that case resolves to a `pending` ledger row a
retry resumes, and the retry cannot double-charge because Stripe returns the original object for the same key. The
keys are **derived, never typed at a call site**, and carry an operation prefix — because two call sites spelling a
key differently is a double-charge with a green test suite, and because Stripe's idempotency-key namespace is
global per account (a key reused across operations returns the first operation's object, silently). The P7 keys
already encode both properties; P21 forwards them and adds nothing.

**Rejected — a fresh idempotency key per attempt, or an application-only dedupe.** A fresh key defeats the entire
mechanism; an application-only dedupe leaves the check-then-insert race open, which is the race the two-layer
guarantee closes.

## Decision 5 — Subscription state drives entitlement by audited plan change, reversibly

**Chosen:** the webhook sync moves the account's `active_plan_id` in response to Stripe's subscription lifecycle:
`active`/`invoice.paid` **grants** the plan; `canceled`/`invoice.payment_failed` **degrades to Free at the period
boundary** (or per the dunning schedule). Every move is an **audited plan change** — `account.SetPlan` (pinning the
plan config version) plus a `TypePlanChange` ledger row — **never** a delete of the account or its history. The
change is **reversible**: paying again is another plan change that restores the plan.

**Why (L2 稳定 / L4 运维).** What a customer can do must follow what they pay for, in both directions, and the
transition must be reconstructable. A degrade-by-delete (drop the account, revoke the row) is irreversible and
unauditable — the two things money state may never be. A plan change at a boundary, audited, is reversible by
construction and leaves the "why did this tenant lose auto-merge" question answerable from the ledger. The
boundary matters: entitlement flips at the plan-change event (P7 Q4) while money proration is Stripe's, so the two
systems each own their fact and neither guesses the other's. Stripe's dunning schedule is mirrored, not recomputed
— the platform reflects `past_due`/`canceled` verbatim (P7's `webhook.go` already does this) and degrades at the
boundary Stripe's schedule defines (PRD Q3).

**Rejected — degrade immediately on the first `payment_failed`, or degrade by deleting the account.** Immediate
degradation fights Stripe's dunning retries (a transient failure would yank access mid-grace-window); delete-based
degradation is the irreversible, unauditable path L2 forbids.

## Decision 6 — Stripe secret from the Secrets seam; test/live separated; the console holds none

**Chosen:** the Stripe API key and webhook signing secret come from the `providergateway.Secrets` seam under the
P7 reserved names `SecretBillingAPIKey` / `SecretBillingWebhookSigning`, resolved **fail-closed**. **Test mode and
live mode are separated** by the P7 rollout flag ([`rollout.go`](../../../internal/billing/rollout.go)) whose zero
value is **test**. The console holds **no** Stripe secret — it receives a short-lived, server-minted Checkout
session URL / client secret only.

**Why (L1 安全).** A billing secret is a credential like any other, and the platform already has exactly one answer
to "where do credentials come from at call time": the Secrets seam, with its manager, caching, fail-closed factory,
and the `Describe()` that puts the live source on `/readyz`. A second mechanism for the Stripe key would split that
truth and invite the specific ugly failure the P7 `secrets.go` comment names — LLM credentials from a manager while
billing credentials quietly come from an env var, with a health endpoint confidently wrong about both. Test/live
separation is a安全/稳定 property: the zero value being **test** means a deployment that forgets to configure the
mode charges nothing real, and a **live** key must never resolve for a test surface (a leaked live key in a test
account is a real-money incident). The console holding no secret is the same law as "no key in a browser" from
every other console surface.

**Rejected — a `STRIPE_SECRET_KEY` env var read directly, or a Stripe key shipped to the console for client-side
calls.** The first splits the credential truth and dodges the health signal; the second is a live secret in a
bundle, which is the one place a secret may never be.

## Decision 7 — Opaque price references; no price value in code, manifest, or bundle

**Chosen:** a concrete price lives **in Stripe** as a price object and is referenced from the platform only by its
opaque `price_ref` (Stripe price ID), exactly as [`plancfg.PlanConfig.PriceRefs`](../../../internal/plancfg/plancfg.go)
already models. No price value exists in code, a manifest, a client bundle, or git — enforced by the P7
auto-discovering fence ([`plancfg/gitfence_test.go`](../../../internal/plancfg/gitfence_test.go)) extended to the
payment UI.

**Why (L5 可演进 / commercial honesty).** Pricing is a business lever pulled without an engineering release; a
price baked into a screen or a manifest makes every price change a deploy and every environment a place the number
can drift. Referencing the Stripe price by id keeps the amount where it is administered (Stripe, by Finance) and
the plan named where it is shown (the UI). The fence is auto-discovering rather than an allowlist because an
allowlist protects only the files someone remembered to add; enumerating the whole git index catches a priced
literal the first time it appears, in any file, including a React bundle.

**Rejected — a price constant in the billing UI "for display," or a price map in a config file in git.** Both put a
number in git, both make a price change a code change, both fail the fence.

## Decision 8 — Reversibility is additive only; corrections are Stripe credit notes / refunds

**Chosen:** a billing error is corrected through the **existing** additive `Credit`/`Refund` path
([`correction.go`](../../../internal/billing/correction.go)) — a Stripe credit note or refund plus a new audited
`billing_event` row. No path deletes or edits a prior usage, invoice, or ledger record; the original Stripe object
is never reduced or removed.

**Why (L2 稳定).** Reversibility must not depend on anyone remembering what they mutated. An additive-only ledger
makes recovery a **forward** operation, every past state reconstructable, and an auditor able to replay the ledger
to any period and get the number the customer actually saw. Editing the wrong charge would make the mistake — and
the fact that there ever was one — disappear, which is exactly what an auditor needs to see. P7 already enforces
this (the ledger is append-only, `Settle` is completion not revision, a webhook authors no ledger row); P21
preserves it against a live processor and adds nothing that can delete.

**Rejected — "fix the wrong charge" by editing or voiding the original.** Destroys the audit trail and the
reconstructability that make the bill trustworthy.

## Decision 9 — the provider account's configuration is verified before it is charged against, not by charging against it

**Chosen:** a **preflight** resolves every `price_ref` in the published plan configuration against the
provider — a read, never a write — and names each one that does not resolve by plan, charge kind and
reference. It runs at configuration time and its result is on the readiness surface. A wrong reference is
therefore a *stated* condition rather than a rejected charge in the middle of a period.

**Why (L2 稳定 / L4 运维).** Decision 7 makes a price an opaque reference the platform cannot validate by
inspection — which is the right trade, and it has a consequence: the only thing that knows whether
`price_ref_team_sub` means anything is the provider. Without a preflight, the first code that finds out
is `RaiseCharge`, at the first charge of the period, and the answer arrives as `ErrProviderRejected` —
correct, unretryable, and maximally badly timed. The customer's period has already accrued; the charge
that should close it cannot be raised; and the operator learns about it from a failed charge rather than
from a configuration check they could have run at deploy.

The same reasoning is why it is **read-only**. A preflight that created a probe subscription to prove a
price works would be a preflight that moves money to check whether money can move, and the first time it
half-failed it would leave an artefact nobody expected on a customer's account.

It also answers a question the platform is otherwise unable to answer honestly: *is this deployment
configured to bill at all?* "The billing service is up" and "the billing service can charge" are
different claims, and a readiness surface that reported only the first would be confidently wrong in the
one case that matters.

**Rejected — validate the reference's shape locally.** Tempting (a Stripe price id starts `price_`), and
worthless: a well-shaped id for a price that was archived, or that belongs to the other mode's account,
passes a shape check and fails a charge. The only authority on whether a reference resolves is the thing
that resolves it.

**Rejected — let the first charge find out.** That is the status quo this decision exists to remove. It
converts a five-second configuration check into a billing incident.

## Decision 10 — mode is a property of every artefact, not a flag on one of them

**Chosen:** the API key, the webhook endpoint, its signing secret, the customer handles and the **price
ids** are each treated as **per-mode objects that live mode does not inherit**. The preflight (Decision 9)
runs in the mode the deployment is running in, against that mode's catalog, and every readiness or
verification line that names the provider **names the mode it observed**.

**Why (L1 安全 / L2 稳定).** In Stripe, test and live are two disjoint object graphs sharing an API
surface. Everything about the integration invites the opposite belief: one key name in the seam, one
catalog shape, one endpoint route, one boolean-sounding flag. So the natural sentence — *"Stripe is
configured"* — is exactly the sentence under which a test price id ends up in a live catalog, and the
first evidence is a rejected charge against a real customer.

The mode stamp on the record is the cheap half and the load-bearing one. A reader who sees "preflight:
8 references verified" supplies the missing word themselves, and supplies it optimistically. A reader
who sees "test mode: 8 references verified" cannot.

**Rejected — treat the mode as a single deployment flag and assume artefacts follow.** They do not
follow; nothing propagates from test to live, and the assumption is invisible until money is real.

**Rejected — warn on a mode mismatch instead of refusing.** A warning on the path that charges people is
a warning nobody reads twice. The key-prefix check refuses in **both** directions for the same reason:
the dangerous case is not a test key on a live surface (which charges nothing), it is a live key
resolving somewhere it was not meant to.

## Decision 11 — customer authentication is a state to render, not an error to swallow

**Chosen:** a subscription sitting `incomplete` with a `requires_action` payment intent is **mirrored
verbatim** and rendered as its own state, carrying **Stripe's own** action link. It is not folded into
`payment_failed`, and it is never retried automatically.

**Why (L3 UX / L2 稳定).** A card that needs 3-D Secure is not a card that was refused. The customer has
done nothing wrong and their next action — approve a prompt in their banking app — has no relationship
to the action `payment_failed` asks for, which is "use a different card". Telling them the wrong one
costs a real person a real afternoon, and a percentage of them churn instead.

The automatic retry is worse than useless: a payment waiting on a human does not become a payment that
succeeds because it was retried. It becomes a rate-limited retry loop, and (with a real issuer at the
other end) a fraud signal.

This is the repository's own rule about discriminating power applied to money: a status that covers both
"your bank needs to confirm this" and "your card was refused" carries **no** information, because the
recipient cannot act differently on it.

**Rejected — one `payment_failed` state with softer copy.** Softer copy does not tell the customer to go
to their banking app. The two states need two different actions and two different links.

## Decision 12 — a 429 is an outage; a decline is a rejection

**Chosen:** Stripe HTTP **429** and lock-contention errors map to `ErrProviderUnavailable` — the P7
buffer holds the work and `FlushPending` drains it exactly once. Card declines and invalid-request
errors map to a **rejection** that stops.

**Why (L2 稳定).** P7 already depends on this split; a real account is simply the first place both sides
of it occur. Getting it backwards is expensive in both directions and the two costs are different:
treating a 429 as a rejection **discards billable usage** (silently, and the customer is under-billed in
a way reconciliation later surfaces as drift), while treating a decline as an outage **hammers a card
that will never clear**, which annoys an issuer and eventually the customer.

**Rejected — one error class for "Stripe said no".** It is one class only from the transport's point of
view. From the caller's point of view they are opposite instructions: hold this and try again, versus
stop and tell somebody.

## Interfaces sketch

```
internal/billing/
  stripe.go                 # NEW: stripe.Provider implements billing.Provider (same interface, real network)
                            #   - EnsureCustomer / CreateSubscription / Subscription / ReportUsage /
                            #     RaiseCharge / IssueCredit / Invoice / RecordedUsage / Describe
                            #   - every charge-bearing call sets Stripe Idempotency-Key = the P7 derived key
                            #   - ErrProviderUnavailable on outage (buffer+retry) vs. a rejection (stop)
  provider.go               # UNCHANGED — the interface P21 fills
  service.go                # UNCHANGED — write-ahead → provider → settle, now with a real provider
  webhook.go                # extended: real Stripe-Signature scheme; verify → dedupe → persist → ack
  correction.go / ledger.go / reconcile / secrets.go / rollout.go   # UNCHANGED — consumed

internal/api/server.go
  POST /billing/webhook     # the ONE inbound-from-internet path; HandleWebhook then entitlement sync

console BFF (web/console, server side — holds the Stripe key, never the client)
  POST /billing/checkout-session  -> { checkout_url }     # mint a Stripe Checkout session server-side
  POST /billing/plan { plan_name } -> { status }          # subscribe/upgrade/downgrade; entitlement flips at event
  GET  /billing/summary            -> { plan_name, sum, usage[], invoice_lines[], payment_method_status }

web/console/src/app/app/billing    # the billing page: plan by NAME, SUM/usage, invoices, payment method
                                    # NO Stripe secret, NO hardcoded price; states: loading/empty/past_due/failed
```

`stripe.Provider` construction (mode from the P7 rollout flag; secret from the seam):

```go
// same interface, real network — every caller runs unchanged
func New(secrets billing.Secrets, mode billing.Mode, clock billing.Clock) (billing.Provider, error)
// mode zero value = test; a live key never resolves for a test surface (Decision 6)
```

Webhook → entitlement sync (Decision 5), after `HandleWebhook` returns `Applied=true`:

```
invoice.paid / subscription.updated(active)  -> SetPlan(customer, paid_plan, cfg_version) + TypePlanChange row
invoice.payment_failed / subscription.past_due -> mirror state; keep entitlement through the grace window
subscription.deleted / grace-end             -> SetPlan(customer, "free", cfg_version) + TypePlanChange row
charge.refunded / charge.dispute.created     -> mirror only; NO ledger row from the webhook (P7 rule)
```

## Risks

- **Stripe type leaks into a caller** → `stripe.Provider` satisfies the existing interface byte-for-byte (D1); a
  contract-parity suite runs every caller against both stub and Stripe; the interface is not widened.
- **Double-charge on retry/replay/ambiguous failure** → the P7 idempotency key on Stripe's `Idempotency-Key` (D4);
  two layers refuse the duplicate; replay + recorded-then-lost tests assert one object, one row.
- **Webhook acked before recorded → lost event** → verify → dedupe → persist → ack (D3); persistence failure returns
  non-2xx so Stripe retries; failure-injection test is load-bearing.
- **Stopped-paying customer keeps a paid entitlement, or paying one doesn't get it** → subscription state drives
  entitlement by audited, reversible plan change (D5); tested both directions with the `TypePlanChange` rows intact.
- **Card or Stripe secret in platform scope / browser / bundle** → Checkout/Element keeps the card browser→Stripe
  (D2); `NewHandle` refuses a PAN; secret from the seam, console holds none, bundle scan fails on a secret (D6).
- **A price value in code/manifest/bundle** → opaque `price_ref` only (D7); the P7 auto-discovering fence extended
  to the payment UI fails the build on a priced literal.
- **Test event moves real money / live key on a test surface** → test/live separated by the rollout flag whose zero
  value is test (D6); asserted in tests.
- **Stripe outage blocks the product or bills the window twice** → product keeps running; usage buffered, reported
  idempotently on recovery, window billed once (preserved from P7).
- **Gainshare bills an unverified saving once money flows** → gainshare reads only the P5.5 verified-delta ledger
  (preserved, not loosened); estimated/un-merged saving raises no charge.
- **The webhook endpoint is a soft attack surface** → one documented inbound path, signature-gated before any side
  effect, timestamp-bounded replay window, rate-aware (D3, mirror of P19's egress allowlist).
- **A test artefact is carried into live** (a test price id, a shared signing secret, a customer handle assumed to
  resolve) → every artefact is mode-scoped and the preflight runs in the running mode; readiness lines are
  mode-stamped (D10). "Stripe is configured" is not a fact a deployment gets to hold as one bit.
- **A 3-D Secure prompt is reported to the customer as a declined card** → `requires_action` is its own state with
  Stripe's own action link, mirrored verbatim, never auto-retried (D11).
- **A rate limit discards billable usage, or a decline is retried forever** → 429 and lock contention are outages
  that buffer; declines are rejections that stop (D12); both directions asserted.
- **A chargeback moves money and the ledger never hears about it** → the dispute webhook still writes no ledger row
  (the P7 rule), and reconciliation surfaces the movement as a **named divergence** a human closes through the
  audited credit path. The requirement is that the disagreement is loud, not that it cannot happen.
- **The live cutover is planned as if the flag were a rollback** → it is not: the flag is reversible and a charge is
  not. Runbook, PRD and console copy all state that the way back for money already moved is an additive correction.

## Where this landed

| What | Where |
|---|---|
| The Stripe provider | [`internal/billing/stripe.go`](../../../internal/billing/stripe.go) |
| Collection (checkout, plan change by name) | [`internal/billing/collection.go`](../../../internal/billing/collection.go) |
| The entitlement sync | [`internal/billing/entitlementsync.go`](../../../internal/billing/entitlementsync.go) |
| The inbound endpoint | [`internal/api/p21.go`](../../../internal/api/p21.go) → `POST /billing/webhook` |
| The billing page + its BFF | [`web/console/src/app/app/billing`](../../../web/console/src/app/app/billing) |
| The in-process Stripe (tests + demo only) | [`internal/stripefake`](../../../internal/stripefake) — a fence fails the build if a shipping package reaches it |
| Run it against a real repository | [`cmd/proof/payments`](../../../cmd/proof/payments) |
| The ingress runbook | [`docs/decisions/p21-billing-webhook-ingress.md`](../../../docs/decisions/p21-billing-webhook-ingress.md) |
| The customer-facing copy | [`docs/sales/P21-billing-copy.md`](../../../docs/sales/P21-billing-copy.md) |
| The M16 verification record | [`docs/decisions/p21-m16-exit-checklist.md`](../../../docs/decisions/p21-m16-exit-checklist.md) |

The three capabilities are folded into the live spec set:
[`stripe-billing-provider`](../../specs/stripe-billing-provider/spec.md),
[`payment-collection`](../../specs/payment-collection/spec.md),
[`billing-webhooks`](../../specs/billing-webhooks/spec.md).
