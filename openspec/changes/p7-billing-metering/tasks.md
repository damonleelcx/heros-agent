# Tasks — P7: Billing, Metering & Entitlements (Commercialization)

Two waves. **Wave 7a** = metering + entitlements + subscription/metered billing (depends on P2.5;
sellable once P4 exists). **Wave 7b** = verified-savings / gainshare billing (depends on P5.5).
No dollar amounts, percentages, or price bands anywhere — plans are named (Free / Team / Business /
Enterprise); limits + price references are **configuration, not code, not in git**.

## 1. Backend + System Designer — Account model & plan config (7a)
- [ ] 1.1 Add the `account` model: `customer_id`, `provider_customer_handle` (opaque; **no card
      data**), `active_plan_id`, `plan_config_version`, `gainshare_consent` + `consented_at`. The
      platform holds provider **handles** only; card data stays with the PCI-compliant provider.
- [ ] 1.2 Implement `ResolvePlan(plan_id) → PlanConfig` reading plan definitions (limits, SUM band,
      seat/retention allowances, **price references**) from the **config store**, not code and **not
      git**. Make it **hot-reloadable** — a config publish changes plans with **no code deploy**.
- [ ] 1.3 Emit a `plan_change` audit event on every plan/price-config publish; keep no price value in
      any git-tracked file (assert in CI that plan config is sourced only from the config store).
- [ ] 1.4 Test: repoint a fixture plan's limit / price reference in the config store; assert the new
      entitlement/limit takes effect with **zero code change and no deploy** (FR6).

## 2. Backend + System Designer — Idempotent metering & SUM derivation (7a)
- [ ] 2.1 Implement `DeriveSUM(customer, period) → Quantity` as an **aggregation of the P2.5 cost
      events** (tokens × price) attributed to the customer — **reuse the telemetry substrate, no new
      collection pipeline**. Make re-derivation of a closed period **deterministic** from the same
      events (FR1).
- [ ] 2.2 Add `usage_record` with **PRIMARY KEY `{customer_id, period, metric}`** for metrics
      {`sum`, `seats`, `retention`, `eval_compute`}; implement `RecordUsage(...)` as an **upsert**
      keyed by that tuple + a `source_digest`, so re-reporting/re-deriving the same period **updates
      one row, never appends a charge-bearing duplicate** (FR2).
- [ ] 2.3 Ensure SUM inherits P2/P2.5 retry-idempotency: a redelivered cost event does **not**
      double-count SUM.
- [ ] 2.4 Test — **never double-count:** report a period's usage, replay the same events/records N
      times; assert the `{customer, period, metric}` record is **one row** with the correct quantity.

## 3. Backend + DevOps — Billing-provider integration: subscription + metered (7a)
- [ ] 3.1 Integrate the **Stripe-style provider**: create/manage subscriptions and report metered
      usage; store only provider **handles/refs** (`provider_customer_handle`,
      `provider_subscription_ref`, `provider_usage_ref`). Proration + dunning are the provider's.
- [ ] 3.2 Implement `Charge(customer, period, kind, idempotency_key)` for `subscription` and
      `metered`; put a **UNIQUE `idempotency_key`** on `billing_event` so the provider records **at
      most one** charge per key — **never double-charge** (FR10).
- [ ] 3.3 Source provider API keys + webhook signing secrets from a **secrets manager**; never place
      them in code, git-config, or telemetry. Verify no secret reaches any span/metric-label/log
      (inherit the P2.5 secrets-never-in-telemetry rule).
- [ ] 3.4 Assert **no-resale:** runs use the **customer's own provider keys**; no invoice line item
      represents resold/marked-up provider tokens (FR13).
- [ ] 3.5 Test: a retried metered-usage report for the same `{customer, period, metric}` yields **one**
      provider charge (FR10); provider outage → usage **buffered** → reported on recovery → billed
      **once** (NFR).

## 4. Backend + DevOps — Idempotent webhooks, reconciliation, reversibility (7a)
- [ ] 4.1 Implement `HandleWebhook(signed_payload)`: **signature-verify** before any side effect;
      **dedupe** by `provider_event_id` in `webhook_delivery` so a **redelivered webhook is processed
      once** (FR14).
- [ ] 4.2 Implement `Reconcile(customer, period) → {matched, drift[]}` comparing `usage_record` to the
      provider's recorded usage/invoices; **surface drift** (records the provider is missing, or has
      that the platform didn't report) as an alert — **never** reconcile-by-overwrite (FR4).
- [ ] 4.3 Implement **additive** `Credit(customer, reason, ref)` / `Refund(...)`: corrections are new
      audited `billing_event` rows; **never delete or mutate** the underlying usage/invoice records
      (FR11).
- [ ] 4.4 Test — **reversibility:** inject a wrong charge, correct it via credit; assert originals are
      intact, the correction is a new audited row, net is right, **no data loss** (FR11).
- [ ] 4.5 Test — **reconciliation:** seed a drift; assert it is surfaced, not silently accepted (FR4,
      FR14).

## 5. Backend — Entitlement gate: plan × automation level (7a)
- [ ] 5.1 Implement `CheckEntitlement(customer, feature, automation_level) → {allowed, reason?,
      upgrade_plan?}` gating by **active plan AND automation level**: CLI + discovery for **all incl.
      Free**; Assisted PRs for **Team+**; dashboard (seats/retention/SUM band per plan) for **Team+**;
      **Autonomous auto-merge for Enterprise only** (FR5).
- [ ] 5.2 Wire the gate into every gated surface — CLI, PR-open, dashboard, and **the P6 auto-merge
      loop** (the loop consults the gate before a merge and **falls back to open-PR** absent the
      Enterprise entitlement — FR8).
- [ ] 5.3 Make every denial **explicit**: return the **named reason + upgrade plan**; **never**
      silently drop, degrade, or allow an over-limit/under-entitled action (FR7).
- [ ] 5.4 Test — entitlement matrix: CLI/discovery on Free; Assisted PR denied Free / allowed Team+;
      dashboard denied Free / allowed Team+; auto-merge denied Team & Business / allowed Enterprise
      (FR5, FR8).
- [ ] 5.5 Test — over-limit: an action past the plan's SUM band / seat limit is **denied with a named
      reason + upgrade path**, never silently (FR7).

## 6. AI Engineer — Verified billable savings & gainshare (7b, depends on P5.5)
- [ ] 6.1 Implement `ComputeBillableSavings(customer, period) → {baseline_sum, optimized_sum, savings,
      refs}` reading **only** the **P5.5 verified-delta ledger** for **merged** PRs; savings =
      `baseline SUM − optimized SUM`. An **estimated / unverified / un-merged** saving contributes
      **zero** (FR3).
- [ ] 6.2 Persist `billable_savings` with `verified_delta_refs[]` + `merge_commits[]` so every billed
      saving **traces** to the verifying ledger entries and the merges that produced it; keep the
      **baseline + holdout methodology fixed and auditable** (reconstructable from the refs) (FR3).
- [ ] 6.3 Implement gainshare `Charge(..., kind = gainshare)` that **refuses** to raise a charge for
      savings **absent from the verified-delta ledger** (FR12) — gainshare bills a share of **VERIFIED
      savings only**.
- [ ] 6.4 Test — **unverified-not-billed (load-bearing):** an **estimated / un-merged** saving raises
      **no** gainshare charge (`ComputeBillableSavings` returns zero for it; `Charge` refuses); a
      **merged, verified** saving produces a gainshare charge that **traces** to its ledger refs +
      merge commits (FR3, FR12).
- [ ] 6.5 Test — auditability: the baseline + holdout methodology behind a gainshare figure is
      reconstructable from stored refs.

## 7. Frontend + Product — Billing/usage UI, paywall, gainshare consent
- [ ] 7.1 Product: design the **packaging boundary** by plan name (Free = CLI + discovery; Team+ =
      Assisted PRs + dashboard; Enterprise = Autonomous auto-merge) and the **paywall-as-invitation**
      — every denial names what's gated + the upgrade path (FR7). No prices in design assets; plans by
      **name**, amounts from config.
- [ ] 7.2 Product: design the **gainshare consent** flow as an informed, recorded, **revocable**
      contract — states the tradeoff plainly ("a share of savings we **verify and merge**"; shows the
      baseline + holdout method) (7b).
- [ ] 7.3 Frontend: billing/usage surface — **SUM under management**, active plan + entitlements, and
      an **invoice breakdown** (subscription / metered / **verified gainshare**), each line traceable
      to the justifying usage. Gainshare shows **verified + merged evidence** (links to the verified
      delta + merged PR).
- [ ] 7.4 Frontend: first-class states — **over-limit-with-upgrade-path**, **payment-failed /
      past-due / dunning** (driven by provider webhook state), loading, empty; **no dollar figure
      hardcoded in the client** — every amount from the API/config.
- [ ] 7.5 Frontend: SUM + savings charts via the **dataviz** skill (contrast, light/dark); keyboard-
      reachable consent + denial states.

## 8. DevOps — Revenue observability & rollout
- [ ] 8.1 Emit metering + billing signals on the **P2.5 substrate**: SUM per customer/period, metered
      totals, invoice/dunning state, **failed charges**, gainshare billed, **reconciliation drift**.
      Alert on **failed charges** and **drift** (G11, NFR).
- [ ] 8.2 Migrations **expand-only** (account, usage_record, billable_savings, billing_event,
      webhook_delivery); plan config ships via the **config store**, never a migration.
- [ ] 8.3 Rollout: **7a** behind a billing feature flag in provider **test mode**, dark until the
      M10-7a checklist is green; **7b** (gainshare) enabled **only** after the P5.5 verified-delta
      ledger is live and the estimated-saving-bills-nothing test (6.4) passes; Enterprise auto-merge
      entitlement stays off until the gate (task 5) is verified.

## 9. Testing & review
- [ ] 9.1 Fixtures: a synthetic customer on each named plan with **config-store** plan definitions
      (limits, SUM band, seat/retention, price **references** — fixture config, **no real prices**); a
      stream of **P2.5 cost events** over a period; a **P5.5 verified-delta ledger** holding a
      **merged verified** saving **and** an **estimated / un-merged** saving; a **stub Stripe-style
      provider** with subscriptions, metered usage, invoices, and **redeliverable webhooks**.
- [ ] 9.2 Metering tests: SUM derived from P2.5 events + deterministic re-derivation (2.1); replayed
      period counts once (2.4); reconciliation surfaces drift (4.5).
- [ ] 9.3 Entitlement tests: the plan × level matrix (5.4); over-limit denied with upgrade path (5.5);
      **plan/price change with no code deploy** (1.4).
- [ ] 9.4 Billing tests: never-double-charge on retry/redelivery (3.5, 4.1); reversibility via credit
      with no data loss (4.4); no-resale (3.4).
- [ ] 9.5 Gainshare tests: **unverified/estimated saving bills nothing**; verified+merged saving bills
      and traces to ledger refs + merges (6.4); baseline/holdout auditable (6.5).
- [ ] 9.6 Secrets test: provider keys/webhook secrets from the secrets manager, in **no**
      span/label/log; an **unsigned** webhook is rejected before any side effect.
- [ ] 9.7 UI verification: drive plan-select → usage/SUM → invoice breakdown → over-limit-with-upgrade
      → gainshare-consent → payment-failed against the stub provider; confirm every state renders, **no
      dollar figure is hardcoded**, and gainshare shows verified+merged evidence.
- [ ] 9.8 Confirm the M10 exit checklist (PRD §13) is green — 7a first, then 7b.
