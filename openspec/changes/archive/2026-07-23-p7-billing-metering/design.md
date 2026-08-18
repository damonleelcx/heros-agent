# Design — P7: Billing, Metering & Entitlements (Commercialization)

Cross-reference: product rationale in
[`../../../docs/prd/P7-billing-metering.md`](../../../docs/prd/P7-billing-metering.md).

> **No dollar amounts, percentages, or price bands appear in this document.** Plans are named
> (Free / Team / Business / Enterprise); every limit and price reference is **configuration, not
> code, not in git**.

## Context

P7 is the commercialization phase: it turns a built product into a business. Three forces shape every
decision. First, **money is the least-reversible thing the platform does** — a double-charge, a charge
for a saving that never materialized, or an untraceable charge is an incident, not a bug — so
**idempotency, reversibility, and auditability** are correctness invariants enforced by construction,
not features. Second, the platform already has **one rich source of usage truth** (the P2.5
cost-event substrate); standing up a second usage pipeline would create two disagreeing ledgers, so
the value metric (**SUM**) must be a **derivation** of that substrate, not a new collector. Third, the
most differentiated pricing — **gainshare**, a share of savings — collides head-on with the founding
law *analysis without verification is confident guessing*: billing a customer for a guessed saving is
that failure at its most expensive, so gainshare reads **only** the P5.5 **verified-delta ledger**
(held-out, significant, merged) and bills **nothing** it cannot prove. The phase reuses machinery
already built — the P2.5 cost events (SUM's raw material), the P5.5 verified-delta ledger (gainshare's
only input), the P2/P2.5 idempotency discipline (never double-charge), ADR-001's merge-as-git-fact
(what a saving attributes to) — and adds a customer/account model, a metering derivation, an
entitlement gate, and a Stripe-style provider integration. It ships in two waves: **7a** (metering +
entitlements + subscription billing; sellable once P4 exists) and **7b** (gainshare; depends on P5.5).

## Decision 1 — SUM is a derivation of the P2.5 cost events, never a second pipeline

**Decision.** **Spend under management (SUM)** for a customer in a billing period is
`DeriveSUM(customer, period)` — an **aggregation of the P2.5 cost events** (tokens × the model's
price) attributed to that customer over that period. It reuses the telemetry substrate; it collects
nothing new. Re-deriving a **closed** period from the same events yields the **same** figure
(deterministic).

**Why.** P2.5 already emits `cost` per provider call (tokens × price), fully tagged and keyed by
`config_hash`, idempotent under retries. The number-one way usage systems drift is **two collectors
disagreeing** about what a customer used. Making SUM a pure function of the existing events means
there is **one source of truth** for usage, and "the meter" is a query, not a service that can fall
out of sync. Determinism on a closed period is what makes reconciliation and audit possible.

**Alternative rejected.** A dedicated usage-metering pipeline emitting its own events next to the
telemetry — duplication, permanent reconciliation burden, and a second definition of "spend" that can
diverge from the one the product already shows.

## Decision 2 — Every meter is an idempotent record keyed `{customer, period, metric}`

**Decision.** Each meter — **SUM, seats, retention, cloud eval compute** — is a `usage_record` with
**PRIMARY KEY `{customer_id, period, metric}`**, written by an **upsert** carrying a `source_digest`.
Re-reporting or re-deriving the same period's usage **updates the one row in place**; it never appends
a second charge-bearing record. The **same period's usage is therefore never double-counted**, no
matter how many times events are replayed, reconciled, or re-derived.

**Why.** Idempotency has to be a **schema property**, not application-level hope: the primary key makes
a duplicate physically impossible, and the `source_digest` makes a re-derivation from identical inputs
a no-op. This inherits the P2/P2.5 retry-idempotent discipline (a redelivered cost event does not
double-count SUM) rather than inventing a new one. "Same period usage is never double-counted" is a
first-class tested requirement, not a footnote.

**Trade-off.** A late-arriving cost event after a period closes must reconcile against an existing row
rather than create a new one — handled as a bounded-grace **additive correction** (PRD Q1), never a
silent rewrite.

## Decision 3 — Plans are configuration; a plan/price change needs no code deploy

**Decision.** A plan definition — its **limits, SUM band, seat/retention allowances, and price
references** — is **configuration** resolved at runtime by `ResolvePlan(plan_id) → PlanConfig` from a
**config store**, versioned and **hot-reloadable**. The database holds only a `plan_id` +
`plan_config_version` the account points at. **No plan definition or price is in git.** A plan/price
change, or introducing a new plan, is a **config publish + audit event** — it takes effect with **no
code deploy and no migration**.

**Why.** Pricing is a business lever pulled on a business cadence, not an engineering release. Baking
limits or prices into code (or git-tracked config) would couple every packaging change to a deploy
and would leak business-sensitive numbers into version control. Making plans data the platform *reads*
means Finance owns packaging and engineering owns the mechanism. CI asserts no priced value is in a
git-tracked file.

**Alternative rejected.** Plans/prices as constants or a git-committed config file — every price
change becomes a PR + deploy, and prices end up in history (the exact thing the money-in-git rule
forbids).

## Decision 4 — Entitlements gate by plan AND automation level; deny is explicit

**Decision.** `CheckEntitlement(customer, feature, automation_level) → {allowed, reason?,
upgrade_plan?}` gates every surface by the **active plan AND the automation level**:

| Surface | Free | Team | Business | Enterprise |
|---|---|---|---|---|
| CLI + discovery | ✓ | ✓ | ✓ | ✓ |
| Assisted verified PRs | — | ✓ | ✓ | ✓ |
| Web dashboard (seats / retention / SUM band **per plan**) | — | ✓ | ✓ | ✓ |
| **Autonomous auto-merge** | — | — | — | ✓ |

An action outside the customer's plan-and-level entitlement is **denied with a named reason and an
upgrade path** — never silently dropped, degraded, or allowed. The **P6 auto-merge loop consults the
gate before a merge** and, absent the Enterprise entitlement, **falls back to opening a PR** for a
human rather than merging.

**Why.** Packaging *is* the product boundary. Gating on **both** plan and automation level is what
keeps the platform's highest-authority actor (P6 Autonomous auto-merge, which edits and merges user
code) reachable **only** by the tier that contracted for it — the entitlement gate is the same
discipline as P6's own prerequisite gate, applied commercially. Making a denial **explicit with an
upgrade path** turns the paywall into an invitation and prevents the two silent-failure modes that
erode trust: a silently-allowed over-limit action (revenue leak) and a silently-dropped one
(mysterious breakage).

## Decision 5 — Billing is idempotent: never double-charge

**Decision.** Every charge-bearing operation — subscription, metered, gainshare — carries an
**idempotency key**; `billing_event.idempotency_key` is **UNIQUE** and the key is passed to the
provider, so the provider records **at most one** charge per key. A re-reported
`{customer, period, metric}`, a retried `Charge`, or a redelivered webhook produces **no** second
charge. Provider **webhooks** are **signature-verified** and **deduped** by `provider_event_id` in
`webhook_delivery` (processed exactly once).

**Why.** "Never double-charge" is the billing correctness invariant. It is enforced at two layers —
the UNIQUE constraint (a duplicate charge row cannot persist) and the provider idempotency key (the
provider itself refuses the duplicate) — so it holds under arbitrary retry and provider redelivery. A
provider outage does not block the product: usage is **buffered** and reported idempotently on
recovery, so the outage window is billed **once**.

**Alternative rejected.** Best-effort dedupe in application code (a race between check and insert can
double-charge). The UNIQUE key + provider idempotency key close that race by construction.

## Decision 6 — Corrections are additive: reversible and auditable, never destructive

**Decision.** A billing error is corrected via an **additive** `Credit`/`Refund` — a **new**, audited
`billing_event` row — with the underlying usage and invoice records left **intact**. No correction
path deletes or mutates a prior record. `billing_event` is **append-only**; "what was charged, when,
and why" survives every correction.

**Why.** Reversibility must not depend on anyone remembering what they mutated. An additive-only
ledger means recovery is a forward operation (issue a credit), every state is reconstructable, and an
auditor can replay the ledger to any period. This mirrors P6's write-ahead-audit / git-revert
reversibility discipline: the correct way to undo is to record a compensating action, not to erase
history. Tested by injecting a wrong charge, correcting it via credit, and asserting the originals are
intact and the net is right — **no data loss**.

## Decision 7 — Two systems of record, one reconciliation, no silent divergence

**Decision.** The platform's `usage_record` is the system of record for **"what was used"**; the
**provider** is the system of record for **"what was charged."** A scheduled, **idempotent**
`Reconcile(customer, period) → {matched, drift[]}` is the **only** thing that compares them — it
**surfaces drift** (usage the provider is missing, or provider charges the platform didn't report) as
an **alert**, and **never reconciles by overwrite**. Auto-correction, where allowed, is limited to
**re-reporting** missing platform→provider usage idempotently; a reduction of a provider charge always
goes through the audited **credit** path (PRD Q6).

**Why.** Two ledgers that must agree will drift unless something actively keeps them honest — but the
reconciler must never become a third writer that papers over disagreement by clobbering one side. One
source of truth per fact + a read-mostly reconciler that alerts is the design that keeps the meter and
the provider from silently diverging (the failure that makes month-end a reconstruction).

## Decision 8 — Gainshare bills VERIFIED savings only, from the P5.5 ledger, for MERGED PRs

**Decision.** `ComputeBillableSavings(customer, period) → {baseline_sum, optimized_sum, savings,
refs}` reads **only** the **P5.5 verified-delta ledger**, and only its entries for **merged** PRs.
Billable savings = `baseline SUM − optimized SUM`. An **estimated**, **unverified**, or
**verified-but-un-merged** saving contributes **zero**. Every `billable_savings` row carries
`verified_delta_refs[]` + `merge_commits[]`, so each billed saving **traces** to the verifying ledger
entries and the merges that produced it. The **baseline + holdout methodology is fixed and auditable**
— reconstructable from the stored refs, not per-invoice discretion. Gainshare `Charge` **refuses** to
raise a charge for any savings absent from the ledger.

**Why.** This is the phase's load-bearing invariant and its sharpest trust edge. The P5.5 ledger only
records deltas that passed the held-out, multi-seed, statistically-significant, regression-clean gate
**and were merged** (a git-history fact per ADR-001) — exactly the "verified, not asserted"
guarantee. Making gainshare a pure function of that ledger means the platform **cannot** bill for a
guessed saving even if a summary, an estimate, or an eager optimizer claims one. *Bill selectively
(only verified, merged), attribute precisely (to the ledger entry + merge), and prove it (the refs).*
Tested as a first-class requirement: an **estimated / un-merged** saving raises **no** charge; a
**merged, verified** saving bills and traces to its evidence.

**Alternative rejected.** Billing on the optimizer's *estimated* or *projected* savings (P4.5/P5.5
pre-verification numbers) — the definition of billing for confident guessing. Also rejected: billing
on a verified-but-un-merged proposal — the saving isn't real until the change ships (merges).

**Realization timing (PRD Q2).** Proposed: bill on **realized** per-period SUM reduction, each charge
still traced to the same verified-delta entry and **capped by the verified delta** — never bill more
than was proven.

## Decision 9 — The platform never resells provider tokens

**Decision.** Customers use their **own** LLM provider keys for optimization and eval runs. The
platform meters SUM and bills for its **service** and its **verified savings**; it **never resells or
marks up provider tokens**. **No invoice line item represents resold provider tokens.** Provider spend
is the customer's, on the customer's keys, sourced from the secrets manager at execution time by the
existing provider gateway.

**Why.** Reselling tokens would put the platform in the provider-proxy business (COGS on every token,
PCI-adjacent payment flows for provider spend, margin tied to token price) — a different, worse
business than pricing on **value** (SUM under management + verified savings). Keeping provider spend on
the customer's keys keeps platform COGS off provider tokens and keeps positioning honest: the platform
is paid for optimization and proof, not for token arbitrage.

## Decision 10 — Secrets and PCI scope stay outside the platform

**Decision.** Provider **API keys** and **webhook signing secrets** live in a **secrets manager** —
never in code, git-tracked config, or telemetry. Webhooks are **signature-verified** before any side
effect. The billing service holds provider **customer/subscription handles**, **never raw card data**,
so **PCI scope stays with the provider**. No secret appears in any span, metric label, or log
(inherits the P2.5 secrets-never-in-telemetry rule). The platform never enters or stores financial
credentials.

**Why.** Card data and provider credentials are the highest-sensitivity surface in the phase. Keeping
card data entirely with the PCI-compliant provider (the platform holds only opaque handles) keeps the
platform out of PCI scope; sourcing secrets from a manager and verifying webhook signatures closes the
two classic billing-integration holes (leaked keys, forged webhooks driving side effects).

## Data model sketch

```
account(customer_id PK, provider_customer_handle, active_plan_id, plan_config_version,
        gainshare_consent BOOL, consented_at, created_at)
        -- provider_customer_handle is opaque; NO card data. active plan + config version only.

usage_record(customer_id, period, metric ENUM('sum','seats','retention','eval_compute'),
             quantity, source_digest, reported_to_provider BOOL, provider_usage_ref, updated_at,
             PRIMARY KEY(customer_id, period, metric))          -- upsert; never a 2nd charge-bearing row

billable_savings(customer_id, period, baseline_sum, optimized_sum, savings,
                 verified_delta_refs JSON, merge_commits JSON,
                 PRIMARY KEY(customer_id, period))              -- ONLY from P5.5 ledger, merged PRs

billing_event(event_id PK, customer_id, period,
              type ENUM('charge','credit','refund','subscription_change','plan_change','gainshare_charge'),
              kind ENUM('subscription','metered','gainshare') NULL,
              idempotency_key UNIQUE, provider_ref, amount_ref, caused_by, created_at)
              -- APPEND-ONLY. idempotency_key UNIQUE = never-double-charge. amount_ref is an opaque
              -- provider handle: NO dollar value stored here. corrections are new rows, never edits.

webhook_delivery(provider_event_id PK, processed_at)           -- idempotent webhook dedupe
```

**Config store (not git):** plan definitions + price references, versioned, published without a
deploy. The DB stores only the `plan_id` + `plan_config_version` an account points at. **No priced
value is in any git-tracked file** (CI-asserted).

## Key interfaces

```
DeriveSUM(customer, period) -> Quantity                         // aggregate P2.5 cost events; deterministic on a closed period
RecordUsage(customer, period, metric, quantity, source_digest) -> UsageRecord   // UPSERT keyed {customer,period,metric}
ComputeBillableSavings(customer, period) -> {baseline_sum, optimized_sum, savings, refs}  // ONLY P5.5 ledger, merged PRs
Reconcile(customer, period) -> {matched, drift[]}              // usage vs provider; surfaces drift, no overwrite
CheckEntitlement(customer, feature, automation_level) -> {allowed, reason?, upgrade_plan?}   // plan AND level
ResolvePlan(plan_id) -> PlanConfig                            // from config store, not code/git; hot-reloadable
Charge(customer, period, kind, idempotency_key) -> BillingEvent  // provider records at most once; gainshare refuses unverified
Credit(customer, reason, ref) -> BillingEvent / Refund(...)    // ADDITIVE, audited; never deletes
HandleWebhook(signed_payload) -> ...                          // signature-verified, deduped by provider_event_id
```

## Risks

- **Double-charge under retry/redelivery** — mitigated by the `{customer, period, metric}` upsert key
  + UNIQUE `idempotency_key` + provider idempotency key + webhook dedupe (Decisions 2, 5); tested by
  replaying a period and asserting one charge.
- **Billing an unverified/estimated saving** — mitigated by gainshare reading only the P5.5 ledger for
  merged PRs and refusing savings absent from it (Decision 8); tested by an estimated saving raising no
  charge. *(Load-bearing.)*
- **Meter drifts from provider** — mitigated by one-source-of-truth-per-fact + a read-mostly
  reconciler that alerts, never overwrites (Decision 7).
- **Irreversible billing error / mutated history** — mitigated by additive credits/refunds on an
  append-only ledger (Decision 6); tested by correcting a wrong charge with originals intact.
- **Second usage pipeline diverges** — mitigated by SUM as a derivation of P2.5 events (Decision 1).
- **Plan/price change needs a deploy, or leaks prices into git** — mitigated by plans-as-config,
  hot-reloadable, CI-asserted out of git (Decision 3).
- **Over-limit silently allowed or dropped** — mitigated by explicit deny-with-upgrade-path (Decision
  4).
- **Free/underpaid user reaches Autonomous auto-merge** — mitigated by the plan×level gate the P6 loop
  consults, falling back to open-PR (Decision 4).
- **Leaked provider keys / forged webhooks / card data in scope** — mitigated by secrets-manager keys,
  signature-verified webhooks, provider-handles-only (no card data), no secret in telemetry (Decision
  10).
- **Accidental token resale / COGS on provider tokens** — mitigated by customer-own-keys and
  no-resold-token invoice line (Decision 9).
- **Provider outage blocks the product or bills the window twice** — mitigated by buffer-and-report
  idempotently on recovery (Decision 5).
