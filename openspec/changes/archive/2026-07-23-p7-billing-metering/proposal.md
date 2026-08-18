## Why

The platform can discover, configure, run, evaluate, diagnose, propose, verify, and (P6)
autonomously apply verified optimization pull requests — but it cannot **charge** for any of it.
There is no customer, no plan, no billing period, no entitlement; every capability is reachable by
anyone who can reach the API, including the Autonomous auto-merge loop (P6), the highest-authority
actor in the system. P7 is **commercialization**: it turns the built product into a business without
adding a single new telemetry pipeline.

Three gaps block revenue, each answered by one capability:

- **No value metric, no meter.** P2.5 already emits per-call cost events (tokens × price, fully
  tagged, keyed by `config_hash`), but nothing aggregates them into a **per-customer, per-period**
  figure to price against. Standing up a *second* usage pipeline would create two sources of truth for
  "what did this customer use" and a permanent reconciliation problem. The value metric — **LLM spend
  under management (SUM)** — must be a **derivation of the existing cost-event substrate**, not a new
  collector.
- **No entitlements, so nothing is sellable.** Without a plan model there is no difference between
  Free and Enterprise. Packaging *is* the product boundary — it decides which surface (CLI, PRs,
  dashboard, auto-merge) a customer reaches — and plan definitions must change (raise a limit, repoint
  a price reference, add a plan) **without a code deploy**, because pricing is a business lever pulled
  weekly, not an engineering release.
- **No billing, and billing is unforgiving.** Charging money is the least-reversible thing the
  platform does. A double-charge, a charge for a saving that never materialized, or a charge that
  cannot be traced and refunded is a trust-and-legal incident, not a bug. Billing must be
  **idempotent** (never double-charge), **reversible** (every error corrected by an additive credit or
  refund with audit, never by deleting data), and **auditable** (every charge reconstructable to the
  usage that justified it).

The sharpest edge is **gainshare** — billing a share of the savings the platform produces. Savings
are exactly the thing an optimizer is tempted to *assert*, and the platform's founding law —
*analysis without verification is confident guessing* — has its most expensive failure mode here:
**billing** a customer for a guessed saving. The antidote already exists: the **P5.5 verified-delta
ledger** records only deltas that passed held-out, statistically-significant, regression-clean
verification **and were merged** (a git-history fact per ADR-001). Gainshare MUST read that ledger and
nothing else. **Unverified or estimated savings are never billed.**

P7 ships in **two waves**. **7a** (metering + entitlements + subscription billing) is sellable the
moment the eval harness (**P4**) exists and depends on the cost-event substrate (**P2.5**). **7b**
(verified-savings / gainshare billing) depends on **P5.5**'s verified-delta ledger and MUST NOT ship
an estimated-savings shortcut to beat its dependency. Milestone **M10 — self-serve billing live** is
the first-dollar milestone.

Depends on **P2.5** (cost-event substrate SUM is derived from; the observability substrate revenue
signals ride on), **P4** (the eval harness — the sellable unit of value 7a prices against), **P5.5**
(the verified-delta ledger — the only input to gainshare, gating 7b), **P6** (the Autonomous
auto-merge loop — the top-tier entitlement), **P2** (Runtime + queue + idempotency discipline the
meter and billing inherit), and **ADR-001** (apply = source-transformation PR; a merge is the
git-history event gainshare attributes a saving to).

## What Changes

- **New capability `metering`.** **SUM** for a customer in a billing period is computed as an
  **aggregation of the P2.5 cost events** (tokens × price) attributed to that customer — **reusing the
  telemetry substrate, not a new pipeline** — and re-deriving a closed period is deterministic. Every
  meter — **SUM, seats, retention, cloud eval compute** — is an **idempotent usage record keyed
  `{customer, period, metric}`**, upserted so the **same period's usage is never double-counted**.
  **Billable savings** = `baseline SUM − optimized SUM`, attributable **only** to **merged**
  optimization PRs, computed **only** from the **P5.5 verified-delta ledger**; **estimated /
  unverified / un-merged savings contribute zero**, and the **baseline + holdout methodology is fixed
  and auditable**. Reported usage is **reconcilable against the billing provider** — a reconciliation
  surfaces any drift rather than silently accepting divergence.
- **New capability `entitlements`.** Feature access is gated by the customer's **active plan AND the
  automation level** (Advisory / Assisted / Autonomous): **CLI + discovery for all plans incl. Free**;
  **Assisted PRs for Team and above**; the **Web dashboard for Team and above** (seats, trace/metric
  retention, and SUM band **per plan**); **Autonomous auto-merge for Enterprise only** (the P6 loop
  consults the gate before a merge and falls back to open-PR absent the entitlement). Plan definitions
  — limits, SUM band, seat/retention allowances, and **price references** — are **configuration**
  resolved at runtime, **not code and not in git**, so a **plan/price change takes effect without a
  code deploy**. An over-limit or under-entitled action is **denied with a named reason and an upgrade
  path** — never silently dropped, degraded, or allowed.
- **New capability `billing`.** Integrate a **Stripe-style billing provider** for **subscriptions +
  metered usage + invoicing** (proration + dunning are the provider's), holding only provider
  **customer/subscription handles**, never raw card data. Billing is **idempotent — never
  double-charge** (an idempotency key on every charge-bearing operation; a re-reported
  `{customer, period, metric}` records at most one charge). A billing error is corrected via an
  **additive credit or refund** with a **full audit log** and **no deletion or mutation** of the
  underlying records. **Gainshare is billed as a share of VERIFIED savings only** — the platform
  **refuses to raise a gainshare charge** for savings absent from the verified-delta ledger. Customers
  use their **own provider keys**; the platform **never resells or marks up provider tokens** (no
  invoice line represents resold tokens). Provider **webhooks are handled idempotently** and
  **invoices are reconciled** against platform usage.
- **UI.** A billing/usage surface — SUM under management, the plan and what it entitles, and an
  invoice that separates **subscription vs. metered vs. verified gainshare**, each line traceable to
  the usage that justified it. First-class **over-limit-with-upgrade-path**, **payment-failed /
  past-due / dunning**, and **gainshare-consent** states; gainshare shows **verified + merged
  evidence**; SUM/savings charts via the **dataviz** skill; **no dollar figure hardcoded in the
  client** — every amount comes from the API/config.
- **Deferred / out of scope:** concrete prices, plan limits, SUM band boundaries, and gainshare rates
  (**configuration, not git**); the cost-event collection pipeline (**P2.5**); the verified-delta
  computation (**P4 / P5.5 / P6**); reselling provider tokens (**never**); tax calculation and revenue
  recognition (the provider + Finance); storing raw card data / a custom payments processor
  (**never** — PCI scope stays with the provider).

## Impact

- **Affected capabilities:** `metering` (new), `entitlements` (new), `billing` (new). Consumes the
  P2.5 cost-event substrate + observability, the P4 eval harness (sellable unit), the P5.5
  verified-delta ledger (gainshare's only source), the P6 auto-merge loop (top entitlement), the P2
  Runtime + queue + idempotency, and ADR-001 (merge = the savings-attributing event).
- **Affected code/systems:** a customer/account model (customer ↔ provider handle, active plan,
  gainshare consent); a **SUM derivation** over P2.5 cost events; **idempotent usage records**
  (`{customer, period, metric}`, upserted, content-digested); a **verified-billable-savings**
  computation reading only the P5.5 ledger for merged PRs; a **plan-config resolver** (limits + SUM
  band + seat/retention + price references from a config store, not git, hot-reloadable); an
  **entitlement gate** consulted by CLI, PR-open, dashboard, and the P6 loop; a **billing-provider
  integration** (subscriptions + metered usage + invoices) with idempotency keys, an **idempotent
  signature-verified webhook handler**, an **additive credit/refund** path with an append-only audit
  log, and a scheduled **usage↔provider reconciliation**; secrets-manager-sourced provider keys +
  webhook secrets (never in code/git/telemetry); Postgres schema (account, usage_record,
  billable_savings, billing_event, webhook_delivery); revenue metrics/audit on the P2.5 substrate; and
  a React billing/usage + invoice-breakdown + entitlement-denial + gainshare-consent UI.
- **Dependencies:** requires **P2.5**, **P4** (7a) and additionally **P5.5** (7b); consults **P6** and
  **ADR-001**. Two waves — **7a** (metering + entitlements + subscription billing; sellable once P4
  exists), **7b** (verified-savings / gainshare; depends on P5.5). Unblocks **M10 — self-serve billing
  live (first dollar)**; this is the terminal commercialization phase of the timeline.
