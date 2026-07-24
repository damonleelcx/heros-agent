# ADR-008 — The console binds to an abstract tenant principal through one seam; P7 owns the mechanism

- **Status:** Accepted (2026-07-24)
- **Deciders:** System Design + Backend (proposed) + User (ratified)
- **Resolves:** [`openspec/changes/p9-web-console/tasks.md`](../../openspec/changes/p9-web-console/tasks.md)
  §2.3, and PRD [P9](../prd/P9-web-console.md) §14 Q1.
- **Relates to:** `design.md` Decision 1 (the session boundary this identity binds), and P8's admin
  identity, which is a **different and disjoint** domain (P8 owns operator principals; this ADR owns
  nothing about them).
- **Owns:** phase **P9 — Web Console**, §3.1.

## Context

P9's session exchange must authenticate *something* and bind the resulting session to a tenant. PRD
§14 Q1 records that **P7 has not named the tenant identity mechanism** — hosted auth, OIDC against the
customer's own IdP, or both — and states that P9 *"must not pre-empt the P7 mechanism."*

What exists today is narrower than an identity system and worth stating precisely, because the
temptation is to mistake it for one:

- `internal/auth` maps an **API key** to `Principal{TenantID, Role, APIKeyID}` from configuration
  (`auth.Registry`). That is the platform's entire notion of "who is calling", and it authenticates a
  **credential**, not a person.
- `internal/account` holds `Account{CustomerID, ActivePlanID, PlanConfigVersion, …}` — the P7 billing
  subject. It has no credential, no login, and no session.
- There is no customer-side `/me`, no token endpoint, no password, and no IdP integration anywhere in
  the repository.

So P9 cannot "confirm the binding with P7" in the sense the task anticipated, because there is nothing
yet to confirm. What it can do — and what this ADR decides — is make sure that when P7 does decide, the
decision lands in **one function** and touches nothing above it.

## Decision

**The console authenticates through a single `TenantIdentity` seam whose entire contract is
`verify(assertion) → { tenantId }`. Everything above it — the session, the cookie, the fail-closed
routing, the scope derivation, the entitlement read — is written against an abstract authenticated
tenant principal and knows nothing about how the assertion was proved.**

```
verify(assertion)  ──▶  { tenantId }            the whole contract
                          │
       session store ◀────┘   session { id, tenantId, issuedAt, expiresAt, revokedAt? }
                          │
       every upstream call is scoped by session.tenantId — never by client input (NFR12)
```

P9 ships two implementations of the seam and **no third**:

| Implementation | Where it runs | What it proves |
|---|---|---|
| `configured` | Any deployment | The assertion resolves to a tenant through deployment configuration the BFF reads from its environment — the same shape `auth.Registry` already uses to bind a credential to a tenant. It introduces **no new identity model**; it reads the platform's existing one. |
| `dev` | Local development only | Accepts a declared tenant so the console is runnable without a platform credential in a browser (README, §13.3). **Refuses to start when `NODE_ENV=production`** — a development identity provider that can run in production is not a development identity provider. |

Three rules bind both implementations and any future one:

1. **The assertion is never persisted.** It is verified, exchanged for a session, and dropped. It is
   not stored in the session record, not written to a cookie, not logged, and not carried upstream.
   The BFF's **own** server-held platform credential is what authorizes upstream calls; the session
   carries only the tenant.
2. **The tenant is authoritative and server-side.** A tenant identifier arriving from the client in a
   path, query, body or header never widens, changes or overrides the session's tenant. This is the
   standing lesson that a request must not be trusted to describe its own authority.
3. **The seam is the only thing P7 changes.** When P7 names its mechanism — hosted auth, OIDC, SAML,
   anything — it replaces `verify` and adds whatever redirect/callback routes that mechanism needs.
   Sessions, revocation, scope derivation, fail-closed routing and every page above are untouched.

## Alternatives rejected

**Design the session exchange against a specific mechanism now (pick OIDC and build it).** Faster to a
working sign-in, and it is what "just make it work" produces. Rejected on **L5 不可演进** and on
ownership: the tenant identity model is a published contract with the customer's own IT organization,
and choosing it as a side effect of building a UI is exactly the one-way door 🔴 `careful-api-creation`
is about. P9 would also be picking it *for* P7 without P7's constraints in view (customer IdP
federation, SCIM provisioning, per-seat revocation) — and the cost of being wrong is a migration for
every existing tenant.

**Let the browser hold the tenant API key and call the platform directly, with no session at all.**
The cheapest possible option, and the one this entire phase exists to refuse: a long-lived platform
credential in a browser is exfiltrable by any XSS and has no per-user revocation. **L1 安全.**

**Reuse P8's admin identity for customer sign-in.** It exists and it works. Rejected because the two
domains are deliberately disjoint — different origin, different cookie jar, a deliberately
non-confusable cookie name — and an admin principal is a **cross-tenant** principal. Making it a
customer identity would put a cross-tenant credential on a single-tenant surface, which is the P9/P8
boundary violation the PRD's surface discipline opens with.

**Wait for P7 before building the console's session at all.** Rejected as a schedule choice with no
technical benefit: everything above the seam is identical under every candidate mechanism, so waiting
buys nothing and blocks the whole phase on a decision that is not P9's.

## Consequences

- Sign-in is **not** finished when P7 lands; the seam is. That is stated here so nobody reads a working
  `configured` sign-in as evidence that customer identity is done. `dev` in particular is a development
  affordance, and the guard that stops it booting in production is the load-bearing part of it.
- The session store is the console's own, server-side, and holds `{ id, tenantId, issuedAt, expiresAt,
  revokedAt }` and nothing else. It deliberately does **not** hold user identity — P9 has no concept of
  a *user*, only of a tenant, because that is all the platform can currently prove. When P7 introduces
  users, per-user revocation and audit attribution become possible; today's session can be revoked, but
  it cannot say *whose* it was, and pretending otherwise in an audit field would be worse than the gap.
