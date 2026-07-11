# Design — P8: Admin & Operations Console (Platform Administration)

Cross-reference: product rationale in
[`../../../docs/prd/P8-admin-console.md`](../../../docs/prd/P8-admin-console.md).

> **No dollar amounts, percentages, or price bands appear in this document.** Plans are named
> (Free / Team / Business / Enterprise); the model-registry **price references** the console repoints
> are **configuration, not code, not in git**.

## Context

P8 is the platform's **operator console** — the back-office surface the platform team uses to run the
whole system, distinct from the customer-facing dashboard (P4/P7). It is the **highest-blast-radius
surface** in the platform: one console that **crosses tenant boundaries**, can change a tenant's
**plan/entitlements and billing**, can **operate any tenant's jobs**, and can **halt the entire
autonomous optimizer fleet**. Three forces shape every decision. First, **one action can affect every
tenant**, so **security and least privilege are correctness invariants, not features** — separate admin
identity, SSO+MFA, short-lived revocable sessions, deny-by-default per-capability gating, and
confirmation+reason+audit on every privileged action. Second, the platform already **owns the truth
elsewhere** (P2.5 usage/cost, P4/P6 jobs/fleet, P6 merge audit + kill switch, P7 accounts/billing/
entitlements), so P8 is a **read-model + privileged-command layer** — it reads aggregates and issues
commands, it stands up **no** second pipeline. Third, the console is the **operator brake on the most
autonomous actor** (the P6 auto-merge loop), so its kill switch must be a **real, immediate, fail-
closed** halt and every autonomous merge must be on a **tamper-evident** record. The phase reuses
machinery already built and adds exactly the operator layer: admin identity/RBAC, the privileged
command surface, cross-tenant read models, and the append-only tamper-evident audit log. It ships in
two waves: **8a** (admin RBAC + tenant/billing/entitlement admin + audit log; alongside P7) and **8b**
(fleet ops + global autonomous controls + cross-tenant observability + compliance).

## Decision 1 — Admin identity is separate from customer auth; SSO + MFA, short-lived, revocable

**Decision.** Admin access is authenticated through a **dedicated admin identity provider**, entirely
**separate from the P7 customer auth path**, and requires **SSO + MFA**. An `admin_principal` is never
a tenant principal. `admin_session` carries a **short TTL** and is **immediately revocable**; a request
is authorized only against a **live, unexpired, unrevoked** session, denied at the next request
otherwise.

**Why.** Operator power is categorically different from customer power — it crosses tenants and can halt
the fleet — so it must not share an identity, a login, or a trust domain with customers. MFA raises the
bar on the credential that unlocks the platform's most dangerous surface; short-TTL + revocable
sessions bound the blast radius of a lost laptop or a hijacked session to minutes, not days.

**Alternative rejected.** Reusing customer auth with an "is_admin" flag — one compromised customer path
or one privilege-flag bug would expose cross-tenant operator power; and it conflates two trust domains
that must stay separate.

## Decision 2 — Every capability is deny-by-default permission-gated; least privilege across four roles

**Decision.** `Authorize(admin_id, capability, target) → {allowed, reason?}` resolves the caller's
**live** role grants against a **deny-by-default** permission map. Four roles partition capability:

| Capability | Support | Billing-Ops | Platform-SRE | Superadmin |
|---|---|---|---|---|
| Search/view tenants, read jobs/fleet | ✓ | ✓ | ✓ | ✓ |
| Impersonate (read-scoped, bounded) | ✓ | ✓ | ✓ | ✓ |
| Credits/refunds, billing oversight, entitlement/plan override | — | ✓ | — | ✓ |
| Job retry/cancel, model registry, **kill switch** | — | — | ✓ | ✓ |
| Tenant suspend/reactivate, quota | — | ✓ | ✓ | ✓ |
| Role/permission grants, GDPR compliance | — | — | — | ✓ |

A capability with no granting permission is **denied and the denial logged**. A **Support** role can
therefore neither **bill** nor perform **destructive** ops (the load-bearing least-privilege
invariant). **Role grants are Superadmin-only and audited.**

**Why.** Packaging capability into least-privileged roles is the primary blast-radius control: it means
no single persona can both move money *and* halt the fleet *and* erase the record. Deny-by-default makes
"unlisted ⇒ denied" a structural property, so adding a capability without granting it to a role fails
**safe**. The screen reads the same map the backend enforces, so UI and gate never disagree.

**Alternative rejected.** A single "admin" role — all-or-nothing power, the prod-shell anti-pattern P8
exists to remove.

## Decision 3 — Confirmation + recorded reason + audit on every privileged action; second confirm for irreversible

**Decision.** Every state-changing admin action follows one shape: **authenticate → authorize (deny by
default) → confirm + reason → write-ahead audit → effect → observe.** The audit entry (`{actor, target,
action, reason, timestamp}`) is committed **before** the effect (write-ahead, mirroring P6's merge
discipline). A **reversible** per-tenant action needs one reason-required confirmation; an
**irreversible** action (GDPR deletion) needs an explicit **second confirmation** (type-the-target).

**Why.** Because a mis-click can hit the wrong tenant or the whole fleet, friction is scaled to blast
radius, and nothing privileged happens off the record. Write-ahead audit means no effect can escape the
trail even under a crash; if the audit store is unavailable, the action **fails closed** rather than
proceeding unaudited. Reversibility is the default (suspend↔reactivate, arm↔disarm, credit-as-additive-
correction); one-way doors are gated behind a second confirmation.

## Decision 4 — The kill switch is a real, immediate, fail-closed brake; global and per-tenant

**Decision.** `KillSwitch.Arm(scope = global | tenant:<id>, reason)` sets `kill_switch_state` that the
**P6** loop consults **before every merge**. A **global** arm halts **every** tenant's autonomous
merges; a **per-tenant** arm halts one. Both take effect **immediately** and **without a deploy**. The
state is read **fail-closed to halt** — if it cannot be determined, no merge proceeds. Every arm/disarm
is audited; policy defaults are settable.

**Why.** P6 gives each *tenant* a per-run stop; the platform needs a **platform-level** brake for a bad
model, a provider incident, or a runaway search across the fleet. Wiring it to the **same** P6 kill
switch the loop already checks means there is one enforcement point, not a second, drift-prone one.
Fail-closed-to-halt encodes the AI-safety default: "can't tell if we're stopped" means **stopped**. The
global control is deliberately higher-friction and visually distinct from per-tenant so "halt this
tenant" is never mistaken for "halt the fleet."

**Open (PRD Q2).** Arming global is a *safe* (halt) action ⇒ one operator + reason; **disarming**
globally (resuming the fleet) is the riskier direction ⇒ proposed **two-person** authorization.

## Decision 5 — Impersonation is consented, time-bounded, read-scoped, and fully audited

**Decision.** `Impersonate.Start(admin_id, tenant, reason, ttl) → ImpersonationSession` requires a
**reason**, is **time-bounded** (auto-expiring), and is **read-scoped by default**. Every action during
the session is logged as **impersonation** (acting admin + impersonated tenant), never as the tenant. A
**write** requires explicit `Elevate(...)` + a **second confirmation**; the session ends automatically
at its bound.

**Why.** Support often must see what a tenant sees, but doing that by copying a credential or querying
tenant data ad hoc is an unaudited privacy breach. Making impersonation a first-class, bounded, logged
capability is the privacy-respecting alternative — the tenant's data is reachable only within a stated
reason, a bounded window, read-only, and on the record. Write-elevation + second confirmation keeps
"see" and "act as" distinct.

**Open (PRD Q3).** Proposed: read-scoped impersonation for **Support**; **write-scoped** impersonation
requires a higher role — so Support can *see* but never *act as* a tenant.

## Decision 6 — The audit log is append-only, tamper-evident, and captures every merge

**Decision.** `audit_entry` is **append-only** and **hash-chained** (`entry_hash = H(prev_hash ‖
payload)`), on a write-once store. Any mutation or deletion of an entry **breaks the chain** and is
detected by `Audit.Verify() → {intact, break_at?}`. **No role, including Superadmin**, has a
mutate/delete path. The log captures **every admin action** and **every P6 autonomous merge** (with the
merge's motivating diagnosis, verified delta, and merge commit, mirroring the P6 change ledger). An
admin action that cannot be audited **fails closed**.

**Why.** The record of who did what across tenants — and what the autonomous fleet changed — is only
trustworthy if it cannot be quietly rewritten by the very operators it records, **including** the most
privileged one. A hash chain makes tamper-evidence a **structural** property rather than a policy: you
cannot alter history without leaving a detectable break. Capturing every autonomous merge closes the
loop with P6 — oversight without a record is unaccountable.

**Alternative rejected.** A mutable audit table with "admin can edit for corrections" — corrections, if
ever needed, are **new appended** entries; the original is never altered, so the chain and its
evidentiary value survive.

## Decision 7 — Cross-tenant read models: permission-gated, every view logged, mechanism not numbers

**Decision.** `CrossTenantView(admin_id, aggregate) → ReadModel` serves aggregate usage/SUM, COGS/
provider spend, revenue/ops aggregates, top consumers, and anomalies — **derived from the P2.5
substrate**, **read-only**. It is **permission-gated** (unauthorized ⇒ denied) and **every authorized
view is logged**. Aggregates expose **mechanism, not raw tenant content**; a single-tenant drill-down is
treated as a **per-tenant** view (permission-gated + logged), never hidden inside an aggregate.

**Why.** Fleet operation needs cross-tenant visibility, but any cross-tenant access is a privacy event
— so it is gated and, crucially, **logged even when allowed**, so an auditor can see who looked at whom.
Deriving from P2.5 (not a second collector) keeps one source of truth for usage/cost. A minimum-cohort
floor (PRD Q5) prevents re-identifying a small tenant from an "aggregate."

## Decision 8 — Reuse, not re-pipeline: P8 owns only the operator layer

**Decision.** P8 **owns** exactly the operator-layer data — admin identity/roles/sessions, the append-
only hash-chained audit log, impersonation sessions, kill-switch state, and GDPR requests — and
**reads/administers** everything else through the subsystems that own it: P2.5 (usage/cost + admin
telemetry), P4/P6 (jobs/fleet), P6 (merge audit + kill switch), P7 (accounts/billing/entitlements). It
stands up **no** new collection pipeline, executor, or billing integration; it issues **commands** to
those services.

**Why.** A second usage or job pipeline would create two disagreeing ledgers — the exact failure the
platform avoids elsewhere (SUM as a P2.5 derivation, jobs on the P4/P6 queue). One source of truth per
fact keeps the console honest and keeps its footprint to the security-critical layer that genuinely
needs to be new.

## Decision 9 — Model-registry admin is audited and non-retroactive on closed periods

**Decision.** `AdminRegistry.AddModel / DeprecateModel / RepointPriceRef(...)` administers models and
the per-model **price references** used to derive SUM (configuration, not git). Every change is
**audited**; a price-ref repoint or deprecation **does not** rewrite already-closed metering/billing
periods — closed periods retain the reference in effect when they closed; only open/future periods use
the new one.

**Why.** The price references feed SUM, which feeds metering and billing (P7). Letting a registry edit
retroactively change a closed period would silently rewrite invoices already reconciled — a billing
integrity violation. Non-retroactivity keeps the money-in-git and reconciliation invariants intact
while still letting operators evolve the registry.

## Decision 10 — GDPR deletion is actionable + verifiable; tombstone keeps the audit chain intact

**Decision.** `GDPR.Execute(admin_id, subject_ref, reason) → {completed, verification_ref}` (gated to
Superadmin, second-confirmation) removes or **tombstones** the subject's content and produces a
**verifiable completion record**. To reconcile "erase the subject's data" with "the audit log is
append-only," the deletion **tombstones** content and keeps a **non-PII** reference in the audit chain —
the content is gone, the *fact of deletion* is auditable, and **no audit entry is removed**, so the
tamper-evident chain stays verifiable.

**Why.** Compliance must be an operable, verifiable console action, not a manual hunt — but it must not
become the one path that can break the audit chain (which would defeat Decision 6). Tombstoning
resolves the tension: erasure of PII without erasure of the append-only record of the erasure.

**Open (PRD Q4).** The audit **chain** is retained per legal-hold policy; GDPR erasure **never** removes
an entry, only tombstones PII in/around it.

## Data model sketch

```
-- Admin identity (SEPARATE from customer/tenant auth)
admin_principal(admin_id PK, sso_subject, mfa_enrolled BOOL, status, created_at)
        -- NEVER a tenant principal; authenticated only via the admin IdP

admin_role_grant(grant_id PK, admin_id, role ENUM('support','billing_ops','platform_sre','superadmin'),
                 granted_by, granted_at, revoked_at NULL)          -- APPEND-ONLY; grant/revoke = new row, Superadmin-gated + audited

admin_session(session_id PK, admin_id, issued_at, expires_at, revoked_at NULL)
        -- SHORT TTL; a request authorizes only against a live, unexpired, unrevoked session

permission(role, capability)                                       -- DENY BY DEFAULT; unlisted (role,capability) => denied

-- Operator command + audit layer (owned by P8)
audit_entry(seq PK monotonic, prev_hash, entry_hash, actor_admin_id, target, action, reason,
            params_digest, result, created_at)
        -- APPEND-ONLY, HASH-CHAINED (entry_hash = H(prev_hash || payload)); mutation/deletion detectable.
        -- captures every admin action AND every P6 autonomous merge (diagnosis, verified delta, merge commit).

impersonation_session(imp_id PK, actor_admin_id, tenant_id, reason,
                      scope ENUM('read','elevated_write'), started_at, expires_at, ended_at NULL)
        -- reason-required, time-bounded; every impersonated action references imp_id in audit_entry

kill_switch_state(scope PK /* 'global' | 'tenant:<id>' */, armed BOOL, set_by, reason, set_at)
        -- the P6 loop consults this BEFORE every merge; read FAIL-CLOSED to halt

gdpr_request(request_id PK, subject_ref, status ENUM('received','executing','completed'),
             actor, verification_ref, tombstone_ref, created_at, completed_at NULL)
        -- content removed/tombstoned; NON-PII tombstone_ref kept so the audit chain stays intact
```

**Reused stores (read/administer, not own):** P2.5 TSDB/span store (cross-tenant aggregates + admin
metrics); P4/P6 queue (jobs + fleet health); P6 change ledger + git history (merge audit); P7 Postgres
(accounts, entitlements, billing events, reconciliation). **Config store (not git):** plan definitions
+ model **price references** the registry admin repoints; the console reads/publishes versions, never a
git-tracked number.

## Key interfaces

```
Authenticate(sso_assertion, mfa) -> AdminSession                  // admin IdP; short-TTL, revocable
Authorize(admin_id, capability, target) -> {allowed, reason?}     // deny-by-default role gate; logs denials
SuspendTenant(admin_id, tenant, reason) / ReactivateTenant(...) / SetQuota(...)   // reason+audit; suspend halts autonomous merges
OverrideEntitlement(admin_id, tenant, plan_ref, reason) -> AuditedEffect          // plans-as-config; NO deploy; audited
IssueCredit(admin_id, tenant, reason) / IssueRefund(...)          // Billing-Ops only; additive + audited (P7); Support denied
AdminRegistry.AddModel / DeprecateModel / RepointPriceRef(...)    // audited; NON-retroactive on closed periods
Jobs.List / Retry / Cancel(admin_id, job, reason) + FleetHealth() // cancel = confirm+reason+audit
KillSwitch.Arm(scope=global|tenant:<id>, reason) / Disarm(...) / State(scope)     // immediate, no-deploy, wired to P6; read fail-closed to HALT
Impersonate.Start(admin_id, tenant, reason, ttl) -> Session / Elevate(...) / auto-expire   // read-scoped default; every action logged as impersonation
CrossTenantView(admin_id, aggregate) -> ReadModel                // permission-gated; EVERY call logged; unauthorized denied
Audit.Append(entry) -> seq  /  Audit.Verify() -> {intact, break_at?}              // append-only, hash-chained; no mutate/delete path
GDPR.Execute(admin_id, subject_ref, reason) -> {completed, verification_ref}      // actionable+verifiable; tombstone + non-PII audit ref
```

## Risks

- **Over-privileged operator access (prod-shell anti-pattern)** — mitigated by a separate admin IdP +
  SSO+MFA (Decision 1), deny-by-default per-capability gating, and least-privilege roles (Decision 2);
  tested by the least-privilege matrix (Support cannot refund/suspend/cancel/kill/override).
- **Compromised/lingering session** — mitigated by short-TTL + immediately-revocable sessions (Decision
  1); expired/revoked denied at next request.
- **Mis-clicked destructive action across tenants/fleet** — mitigated by confirmation + recorded reason
  + write-ahead audit, second confirmation for irreversible, and global-vs-per-tenant friction
  (Decisions 3, 4).
- **Runaway fleet with no platform brake** — mitigated by the global + per-tenant kill switch wired to
  P6, immediate, no-deploy, **fail-closed to halt** (Decision 4). *(Load-bearing.)*
- **Impersonation as an unaudited backdoor** — mitigated by reason-required, time-bounded, read-scoped,
  fully-audited impersonation with write-elevation + second confirm (Decision 5).
- **An operator (even Superadmin) erases their tracks** — mitigated by an append-only, hash-chained,
  tamper-evident audit log with no mutate/delete path (Decision 6). *(Load-bearing.)*
- **An autonomous merge escapes the record** — mitigated by the audit log capturing every merge with
  diagnosis/delta/commit, write-ahead (Decision 6).
- **Unauthorized cross-tenant snooping / re-identification** — mitigated by permission-gated read models,
  every view logged, mechanism-not-numbers, minimum-cohort floor (Decision 7).
- **Registry edit silently rewrites closed billing periods** — mitigated by audited, non-retroactive
  registry admin (Decision 9).
- **GDPR deletion is unverifiable or breaks the audit chain** — mitigated by an actionable+verifiable
  deletion that tombstones content and keeps a non-PII audit ref (Decision 10).
- **Console outage leaves fleet un-stoppable or actions unaudited** — mitigated by fail-closed critical
  paths (audit-down ⇒ block; kill-state-unreachable ⇒ halt) and the P6 kill switch remaining an
  independent brake (Decisions 3, 4, 6).
- **Second usage/job pipeline diverges** — mitigated by read-model + command reuse; P8 owns only the
  operator layer (Decision 8).
- **Leaked SSO/MFA/session secrets or card data in scope** — mitigated by secrets-manager-sourced
  secrets (never in code/git/telemetry), provider handles only (no card data), no secret in telemetry
  (Decision 1; PRD §7).
