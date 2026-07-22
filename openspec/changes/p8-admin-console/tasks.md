# Tasks — P8: Admin & Operations Console (Platform Administration)

Two waves. **Wave 8a** = admin RBAC + tenant/billing/entitlement admin + audit log (alongside P7).
**Wave 8b** = fleet ops + global autonomous controls + cross-tenant observability + compliance
(depends on P6 fleet controls + P2.5 aggregates). This is the **highest-blast-radius** surface —
design security-first. No dollar amounts, percentages, or price bands anywhere — plans are named
(Free / Team / Business / Enterprise); model-registry **price references** are configuration, not
code, not in git.

## 1. Backend + DevOps — Admin identity, SSO+MFA, sessions (8a)
- [ ] 1.1 Integrate a **dedicated admin identity provider separate from customer auth**; require **SSO +
      MFA**. Add `admin_principal` (never a tenant principal). Source SSO/MFA + session-signing secrets
      from a **secrets manager** — never in code/git/telemetry (FR1).
- [ ] 1.2 Implement `admin_session` with a **short TTL** and **immediate revocation**; authorize a
      request only against a live, unexpired, unrevoked session — deny expired/revoked at the **next**
      request, no grace (FR2).
- [ ] 1.3 Test — auth: no MFA ⇒ denied; SSO+MFA ⇒ session issued; a customer-auth principal cannot reach
      any admin capability (FR1); an **expired** and a **revoked** session are denied at the next request
      (FR2).

## 2. Backend — RBAC gate & least privilege (8a)
- [ ] 2.1 Add the four **roles** (Support / Billing-Ops / Platform-SRE / Superadmin) and a **deny-by-
      default** `permission(role, capability)` map. Implement `Authorize(admin_id, capability, target) →
      {allowed, reason?}` resolving the caller's **live** role grants; **log every denial** (FR3).
- [ ] 2.2 Enforce **least privilege**: gate billing (credits/refunds) and destructive ops (suspend,
      cancel, kill switch, plan override) away from **Support** (FR4).
- [ ] 2.3 Make **role/permission grants** `admin_role_grant` **append-only**, gated to **Superadmin**,
      and audited (FR5).
- [ ] 2.4 Test — **least-privilege matrix (load-bearing):** Support is **denied + logged** on refund /
      suspend / cancel / kill / override; Billing-Ops can refund; Platform-SRE can cancel/kill;
      Superadmin can grant a role (audited); a non-Superadmin role grant is denied (FR3, FR4, FR5).

## 3. Backend — Destructive-action discipline & tenant lifecycle (8a)
- [ ] 3.1 Implement the shared command path: **authenticate → authorize → confirm + reason → write-ahead
      audit → effect → observe**. Require a **recorded reason**; write `{actor, target, action, reason,
      timestamp}`; require a **second confirmation** for irreversible actions (FR6).
- [ ] 3.2 Implement tenant lifecycle: `SuspendTenant` / `ReactivateTenant` / `SetQuota` — permission-
      gated, reason-required, audited; a **suspension halts the tenant's autonomous merges** (FR7).
- [ ] 3.3 Test — destructive discipline: a destructive action records actor+target+reason+timestamp; an
      action **without a reason** does not proceed; an irreversible action needs a **second
      confirmation**; suspending a tenant **halts its autonomous merges**; reactivate restores (FR6, FR7).

## 4. Backend + Billing-Ops — Entitlement override & billing oversight (8a)
- [ ] 4.1 Implement `OverrideEntitlement(admin_id, tenant, plan_ref, reason)` as **plans-as-config** (P7)
      — effective with **no code deploy**, **audited**, **no price value in git** (FR8).
- [ ] 4.2 Implement billing oversight for **Billing-Ops** (not Support): view invoices/dunning/
      reconciliation; `IssueCredit` / `IssueRefund` as **additive, audited** corrections via P7 (never a
      destructive edit); **gainshare oversight** showing verified+merged evidence (FR9).
- [ ] 4.3 Test — override takes effect with **no code deploy** and is audited, no price in git (FR8)
      *(load-bearing)*; Billing-Ops credit is additive+audited with originals intact and Support cannot
      (FR9); a gainshare charge with no verified evidence is surfaced as an exception (FR9).

## 5. Backend + System Designer — Model-registry admin (8b)
- [ ] 5.1 Implement `AdminRegistry.AddModel / DeprecateModel / RepointPriceRef(...)` administering models
      + the per-model **price references** used to derive SUM (config, not git); audit every change (FR10).
- [ ] 5.2 Enforce **non-retroactivity**: a price-ref repoint / deprecation does **not** rewrite already-
      closed metering/billing periods; closed periods keep the reference in effect at close (FR10).
- [ ] 5.3 Test: repoint a price ref ⇒ audited, closed period unchanged, only open/future periods use the
      new ref; deprecate a model ⇒ recorded, closed-period SUM unchanged (FR10).

## 6. Platform-SRE + Backend — Job/queue ops & worker-fleet health (8b)
- [ ] 6.1 Implement `Jobs.List / Retry / Cancel(admin_id, job, reason)` over the **existing P4/P6
      queue** (not a second queue) and `FleetHealth()`; **cancel** = confirm + reason + audit (FR11).
- [ ] 6.2 Test: view/retry/cancel a job (cancel gated + audited); fleet health renders from the existing
      queue/fleet, not a new pipeline (FR11).

## 7. AI Engineer + Platform-SRE — Global & per-tenant kill switch (8b)
- [ ] 7.1 Implement `KillSwitch.Arm(scope=global|tenant:<id>, reason) / Disarm / State` writing
      `kill_switch_state`, **wired to the P6 kill switch** the loop consults **before every merge**;
      effective **immediately, no deploy**; settable policy defaults; audit every arm/disarm (FR12).
- [ ] 7.2 Read kill-switch state **fail-closed to halt** — if state is indeterminate (store unreachable),
      **no merge proceeds** and the last-good Variant Spec stays live (FR12).
- [ ] 7.3 Test — **kill switch (load-bearing):** arming **global** halts **every** tenant's autonomous
      merges immediately, no deploy; arming **per-tenant** halts **only** that tenant (others continue);
      indeterminate state **fails closed to halt** (FR12).

## 8. Backend + Product — Support impersonation (8a)
- [ ] 8.1 Implement `Impersonate.Start(admin_id, tenant, reason, ttl)` — **reason-required**, **time-
      bounded** (auto-expiring), **read-scoped by default**; every impersonated action logged as
      impersonation (acting admin + tenant), not as the tenant (FR13).
- [ ] 8.2 Require explicit `Elevate(...)` + a **second confirmation** for any write while impersonating;
      end the session automatically at its time bound (FR13).
- [ ] 8.3 Test — **impersonation (load-bearing):** no reason ⇒ denied; read-scoped session is time-
      bounded + every action audited as impersonation; a write in read scope is denied until elevated +
      second-confirmed; session auto-expires (FR13).

## 9. Backend + DevOps — Append-only tamper-evident audit log (8a)
- [ ] 9.1 Implement `audit_entry` **append-only + hash-chained** (`entry_hash = H(prev_hash ‖ payload)`)
      on a write-once store; **no mutate/delete path** for any role including Superadmin. Implement
      `Audit.Verify() → {intact, break_at?}` (FR15).
- [ ] 9.2 Capture **every admin action** and **every P6 autonomous merge** (with motivating diagnosis,
      verified delta, merge commit — mirroring the P6 change ledger); an admin action that cannot be
      audited **fails closed** (FR16).
- [ ] 9.3 Test — **tamper-evidence (load-bearing):** a mutate/delete attempt (incl. Superadmin) is
      prevented and `Audit.Verify()` **detects** the chain break; **every** autonomous merge appears with
      its diagnosis/delta/commit; an action while the audit store is unavailable does **not** take effect
      (FR15, FR16).

## 10. Backend + DevOps — Cross-tenant read models (8b)
- [ ] 10.1 Implement `CrossTenantView(admin_id, aggregate)` over the **P2.5 substrate** — usage/SUM,
      COGS/provider spend, revenue/ops aggregates (**mechanism, not numbers**), top consumers, anomalies;
      **read-only**, **permission-gated**; treat a single-tenant drill-down as a **per-tenant** view (FR14).
- [ ] 10.2 **Log every authorized cross-tenant view** (viewer + read model + timestamp); **deny**
      unauthorized admins; enforce a **minimum-cohort** floor to prevent re-identifying a small tenant
      (FR14).
- [ ] 10.3 Test — **cross-tenant (load-bearing):** an unauthorized admin is **denied**; an authorized
      view succeeds and **is logged** (FR14).

## 11. Superadmin + Backend — Compliance / GDPR deletion (8b)
- [ ] 11.1 Implement `GDPR.Execute(admin_id, subject_ref, reason)` (Superadmin, second-confirmation):
      remove/**tombstone** the subject's content; produce a **verifiable completion record**; audit the
      action (FR17).
- [ ] 11.2 Keep a **non-PII tombstone reference** in the audit chain so **no entry is removed** and the
      chain stays verifiable (FR17).
- [ ] 11.3 Test — **GDPR (load-bearing):** a deletion request is actionable ⇒ content removed/tombstoned,
      completion **verifiable**, action audited; the append-only chain stays intact via the non-PII
      tombstone ref (FR17).

## 12. Frontend + Product — Back-office application (8a → 8b)

The console is a **separate Next.js (App Router, TypeScript) application on its own origin with its own
BFF** — not a role-gated section of the [P9](../p9-web-console/) customer console (design.md Decision
11). It shares P9's token **system** but not its appearance (Decision 12), and P9's interface rules
apply here unchanged rather than being restated.

**Isolation & credential custody (8a — do these first)**
- [ ] 12.1 Stand up the console as an **independent Next.js application on its own origin** with its own
      BFF, deployed as its own unit. It shares **no** origin, session cookie, or client bundle with the
      customer console (FR19).
- [ ] 12.2 Hold the platform credential **server-side in the admin BFF**; issue an `HttpOnly`,
      `SameSite` session bound to an **admin principal**. Add a **build-time gate** scanning the shipped
      bundle for credential material — machine-enforced, because a written rule alone has a demonstrated
      failure rate (FR20).
- [ ] 12.3 Make every console route **fail closed**: an unauthenticated request **redirects to sign-in**
      rather than rendering a shell that then fails each request (FR20).
- [ ] 12.4 Enforce **disjoint session domains**: a tenant session presented to the admin BFF is refused;
      an admin session authorizes nothing on the customer console (FR21).
- [ ] 12.5 Test — **security assertions, not review items**: no credential in the shipped bundle; an
      unauthenticated route redirects; a revoked admin session is denied at the next request; a tenant
      session reaches no admin capability.

**Role-scoped interface**
- [ ] 12.6 Product: design the **role-based IA** — each role (Support / Billing-Ops / Platform-SRE /
      Superadmin) sees a coherent, minimal surface; **denied-with-escalation** names who holds a
      capability, never a bare 403.
- [ ] 12.7 Frontend: render **role-scoped views** by reading the **same permission map** the backend
      enforces (screen and gate never disagree); no capability appears that the role doesn't grant (FR22).

**Operator chrome & dangerous-action friction**
- [ ] 12.8 Build the **distinct operator chrome** — distinct accent plus **persistent identification of
      the console and the acting admin principal on every view** — from the shared token system, not a
      fourth palette. This is a safety requirement: its named failure is an operator with both consoles
      open acting cross-tenant while believing the view is single-tenant (FR23).
- [ ] 12.9 Frontend + Product: **dangerous-action confirmation** proportional to blast radius —
      reason-required confirm; **type-the-target / second confirmation** for irreversible; the **global**
      kill switch visually distinct + higher-friction than per-tenant, so "halt this tenant" can never be
      mistaken for "halt the fleet" (FR6, FR12, FR24).
- [ ] 12.10 Frontend + Product: **impersonation-consent** flow (tenant + reason + read-only scope +
      expiry) and a **persistent active-session banner** ("impersonating <tenant>, read-only, expires in
      N min — every action logged") with an always-visible **End** control; write elevation requires a
      second confirmation (FR13, FR25).

**Views, states & the interface floor**
- [ ] 12.11 Frontend: cross-tenant read models + an **audit-log viewer**; charts via the **dataviz**
      skill (contrast, light/dark) **with a tabular fallback**; **no number hardcoded** — plan names and
      price refs resolve from config and no price value is in the client bundle (FR28).
- [ ] 12.12 Render **loading / empty / denied / degraded** as **four distinct** states on every view: a
      permission denial is not an empty result, and a transport failure is not absence of data (FR26).
- [ ] 12.13 Hold the console to the **same interface floor as P9** — keyboard reachability with visible
      focus, scoped table headers, WCAG 2.1 AA contrast, English strings with `Intl` pinned to `en-US`,
      values escaped on render. 🚫 The audience being internal is **not** grounds to relax it (FR27).
- [ ] 12.14 Test — automated accessibility audit **plus a keyboard-only pass** per page; the denied and
      degraded paths walked, not only the populated one.
- [ ] 12.15 🔴 Acceptance for every user-visible behavior is **rendered-browser evidence** against a real
      API response — a green build, a passing type check, and passing unit tests are all compatible with
      a page that renders nothing or the wrong tenant.

## 13. DevOps — Observability, secrets, rollout
- [ ] 13.1 Emit admin activity on the **P2.5 substrate**: logins, **MFA failures**, privileged-action
      volume, active impersonations, kill-switch state, cross-tenant views; alert on anomalies (a spike
      in privileged actions, a kill switch left armed) (FR18).
- [ ] 13.2 Assert **no secret** (SSO/MFA, session-signing key, provider handle) appears in any span,
      metric label, or log; the console stores **no** card data (FR18; PRD §7).
- [ ] 13.3 Migrations **expand-only** (admin_principal, admin_role_grant, admin_session, permission,
      audit_entry, impersonation_session, kill_switch_state, gdpr_request); no priced value in git.
- [ ] 13.4 Rollout: **8a** behind an **admin feature flag**, admin IdP in test mode, roles seeded
      **minimal**, dark until the M11-8a checklist is green; **8b** enabled only after the P6 kill switch
      + P2.5 aggregates are live; **drill the global kill switch in staging** before arming it in
      production; no capability ships without its **permission + audit gate** verified.

## 14. Testing & review
- [ ] 14.1 Fixtures: an **admin IdP** (SSO+MFA, test mode); **four admin principals**, one per role;
      synthetic **tenants** on named plans (P7 fixtures) with entitlements/invoices/reconciliation + a
      gainshare saving (verified+merged vs estimated); a **P4/P6 job queue** with running/failed jobs +
      fleet health; a running **P6 loop** per tenant with change ledger + kill switch; a **P2.5**
      cross-tenant aggregate feed; an **append-only hash-chained audit store**.
- [ ] 14.2 RBAC tests: SSO+MFA required; short-TTL + revocable sessions; the **least-privilege matrix**
      (1.3, 2.4).
- [ ] 14.3 Operations tests: destructive discipline + tenant lifecycle (3.3); entitlement override with
      no deploy + billing oversight (4.3); registry non-retroactive (5.3); job ops (6.2); **kill switch
      global + per-tenant + fail-closed** (7.3); **impersonation** (8.3).
- [ ] 14.4 Audit/observability tests: **tamper-evidence + every autonomous merge captured** (9.3);
      **cross-tenant denied/logged** (10.3); **GDPR actionable + verifiable + chain intact** (11.3);
      admin actions observable on P2.5 + no secret in telemetry (13.1, 13.2).
- [ ] 14.5 UI verification: SSO+MFA login → role-scoped home → tenant suspend (reason) → entitlement
      override → Billing-Ops credit → job cancel → **global vs per-tenant kill switch** → impersonation
      consent + banner → cross-tenant read model → GDPR delete (second confirm) → audit viewer; confirm
      role-scoping, denied-with-escalation, and **no hardcoded number**.
- [ ] 14.6 Confirm the M11 exit checklist (PRD §13) is green — 8a first, then 8b.
