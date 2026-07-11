## Why

By the end of P7 the platform is a business — it discovers, configures, runs, evaluates, diagnoses,
verifies, and autonomously merges optimization PRs for paying tenants across named plans — but there
is **no supported way to operate it**. Every day-two task (suspend a tenant on request, fix a
double-charge, clear a backed-up queue, halt a runaway autonomous fleet, action a data-deletion
request) would today be done by an engineer with a database shell and a production credential:
**unaudited, unscoped, and catastrophically over-privileged**. That is the exact anti-pattern P8
exists to kill.

P8 is the **internal operator console** — the back-office surface, **not** the customer-facing web
dashboard (P4/P7). It is the platform's **highest-blast-radius surface**: one console that **crosses
tenant boundaries**, can **change a tenant's plan/entitlements and billing** (credits/refunds), can
**retry/cancel any tenant's jobs**, and can **halt the entire autonomous optimizer fleet** — globally
or per tenant — so no further PR merges. Because a single action here can affect every customer at
once, P8 is designed **security-first**.

Five gaps block safe operation, each answered by the design:

- **No operator identity, so no least privilege.** The platform has *customer* auth (P7) but no
  **admin** identity, role model, or per-capability permissions. Operator access must run on a
  **separate admin identity provider** with **SSO + MFA**, **short-lived, revocable** sessions, and a
  **deny-by-default** gate over four roles (**Support / Billing-Ops / Platform-SRE / Superadmin**) — a
  Support agent must not be able to issue a refund or halt the fleet.
- **Cross-tenant blast radius with no guardrail.** Acting across tenants is the point *and* the danger.
  Every **destructive/privileged** action needs a **confirmation + recorded reason + audit entry**
  (who/what/whom/when/why), and an **irreversible** action needs a **second confirmation**.
- **The autonomous fleet has no operator brake.** P6 gives each *tenant* a per-run kill switch; the
  platform team needs a **global** and **per-tenant** brake from one place — halt further autonomous
  merges **immediately**, everywhere or for one tenant, **without a deploy**, wired to P6.
- **Support can't help without unsafely becoming the tenant.** **Impersonation** must be **reason-
  required, time-bounded, read-scoped by default, and fully audited** — the privacy-respecting
  alternative to copying a credential.
- **No cross-tenant visibility and no compliance path.** Aggregate read models (usage/SUM, COGS,
  revenue/ops, top consumers, anomalies) must be **permission-gated** and **every view logged**; a
  **GDPR data-deletion** request must be **actionable + verifiable** from the console; and the audit
  log recording all of it must be **append-only + tamper-evident** — not even a Superadmin can erase
  their tracks.

P8 adds **no new pipeline** — it is a privileged **read-model + command** surface over existing
machinery: metrics/cost from **P2.5**, jobs/queue from **P4/P6**, the autonomous change ledger + kill
switch from **P6**, tenants/billing/entitlements from **P7**, and **ADR-001**'s merge-as-git-fact (what
the audit captures and the kill switch prevents). It ships in **two waves**: **8a** (admin RBAC +
tenant/billing/entitlement admin + audit log) alongside P7, and **8b** (fleet ops + global autonomous
controls + cross-tenant observability + compliance). Milestone **M11 — operator console live (platform
manageable end-to-end)**.

## What Changes

- **New capability `admin-rbac`.** Admin access is authenticated through a **dedicated admin identity
  provider separate from customer auth** and requires **SSO + MFA**; sessions are **short-lived and
  immediately revocable**. **Every** admin capability is **permission-gated (deny by default)** via a
  **role** (Support / Billing-Ops / Platform-SRE / Superadmin), enforcing **least privilege** — a
  **Support** role can neither **bill** (credits/refunds) nor perform **destructive** ops (suspend,
  cancel jobs, kill switch, plan override). **Role/permission grants** are **Superadmin-only and
  audited**.
- **New capability `admin-operations`.** Every **destructive/privileged** action requires a
  **confirmation + recorded reason + audit** ({actor, target, action, reason, timestamp}), with a
  **second confirmation** for irreversible actions. **Tenant lifecycle** (search/view,
  suspend/reactivate, quota — a suspension halts the tenant's autonomous merges). **Entitlement/plan
  override** takes effect **without a code deploy** (plans-as-config, P7) and is **audited** (no price
  in git). **Billing oversight** (invoices, dunning, reconciliation, **additive/audited** credits/
  refunds, gainshare oversight) is **Billing-Ops**, not Support. **Model-registry administration**
  (add/deprecate models, repoint the per-model **price references** used for SUM) is **audited** and
  **non-retroactive** on closed periods. **Job/queue operations** (view/retry/cancel on the P4/P6
  queue; worker-fleet health; cancel gated). A **global and per-tenant kill switch** **immediately**
  halts further autonomous PR merges (wired to P6, no deploy, policy defaults, each fire audited,
  state reads **fail-closed to halt**). **Support impersonation** is **reason-required, time-bounded,
  read-scoped by default, and fully audited** (writes need elevation + second confirmation).
- **New capability `admin-observability-audit`.** **Cross-tenant read models** (usage/SUM, COGS,
  revenue/ops aggregates — mechanism, not numbers; top consumers; anomalies) are **read-only,
  permission-gated**, and **every authorized cross-tenant view is logged** (unauthorized denied). The
  **audit log** is **append-only + tamper-evident** (mutation/deletion prevented + detectable; no role,
  including Superadmin, exempt) and captures **every admin action AND every P6 autonomous merge** (with
  diagnosis/verified-delta/merge-commit). **GDPR data-deletion** is **actionable + verifiable** from
  the console (content removed/tombstoned, verifiable completion record, audited; a **non-PII tombstone
  reference** keeps the chain intact). **Admin actions are observable** on the P2.5 substrate (logins,
  MFA failures, privileged actions, kill-switch state, impersonations, cross-tenant views) with anomaly
  alerts.
- **UI.** A **back-office** surface with **role-scoped** views (an operator sees only what their role
  grants), **dangerous-action confirmation** patterns (reason-required; type-the-target/second
  confirmation for irreversible; global vs per-tenant kill switch visually distinct and higher-
  friction), an **impersonation-consent flow + persistent active-session banner**, **denied-with-
  escalation** states, and first-class loading/empty/denied/degraded states; cross-tenant charts via
  the **dataviz** skill; **no number hardcoded** (plan names + price refs from config).
- **Deferred / out of scope:** the customer-facing web dashboard (**P4/P7**); the metric/cost
  collection pipeline (**P2.5**); the job executor (**P4/P6**); the autonomous loop mechanics + per-run
  kill switch + change ledger (**P6**); the billing provider integration + idempotency + proration/
  dunning (**P7**); concrete prices/limits/rates (**config, not git**); entering financial credentials
  / storing card data / a payments processor (**never**); a general-purpose arbitrary-SQL prod-shell
  replacement (**explicitly not** — the anti-pattern P8 removes).

## Impact

- **Affected capabilities:** `admin-rbac` (new), `admin-operations` (new), `admin-observability-audit`
  (new). Consumes the **P2.5** telemetry substrate (cross-tenant read models + admin-action
  observability), the **P4/P6** job queue + worker fleet (operated, not duplicated), the **P6** change
  ledger + kill switch (merge-audit + global/per-tenant halt), the **P7** account/entitlement/billing
  services (administered), and **ADR-001** (merge = the git-history event audited and kill-switch-
  prevented).
- **Affected code/systems:** a **dedicated admin identity provider** integration (SSO + MFA); admin
  identity/session stores (`admin_principal`, `admin_role_grant` append-only, `admin_session` short-TTL
  + revocable, a deny-by-default `permission` map); a **deny-by-default authorization gate**
  (`Authorize(admin_id, capability, target)`) wired into every capability and into the P6 merge path
  (kill switch); privileged **command** handlers for tenant lifecycle, entitlement/plan override
  (plans-as-config, no deploy), billing oversight (additive credits/refunds via P7, gainshare
  oversight), model-registry admin (price refs, deprecations, non-retroactive), job ops (view/retry/
  cancel), the **global + per-tenant kill switch** (wired to P6, fail-closed to halt), and
  **impersonation** (reason-required, time-bounded, read-scoped, audited); an **append-only, hash-
  chained tamper-evident audit log** (`audit_entry`) capturing every admin action + every autonomous
  merge, with `Audit.Verify()`; **cross-tenant read models** over P2.5 (permission-gated, every view
  logged); a **GDPR** deletion path (`gdpr_request`, tombstone + verifiable completion); admin-action
  metrics/audit on the P2.5 substrate; secrets-manager-sourced SSO/MFA + session-signing secrets (never
  in code/git/telemetry); and a React **back-office** UI (role-scoped views, dangerous-action
  confirmation, impersonation consent + banner, denied-with-escalation, cross-tenant read models, audit
  viewer).
- **Dependencies:** requires **P7** (8a: administers tenants/billing/entitlements) and additionally
  **P6** + **P2.5** (8b: global fleet controls + cross-tenant read models); consults **P4/P6** (jobs)
  and **ADR-001** (merge = the audited/kill-switched event). Two waves — **8a** (admin RBAC +
  tenant/billing/entitlement admin + audit log; alongside P7), **8b** (fleet ops + global autonomous
  controls + cross-tenant observability + compliance). Unblocks **M11 — operator console live (platform
  manageable end-to-end)**.
