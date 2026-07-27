# Design — P22: SSO & Identity

Product rationale: [`../../../docs/prd/P22-sso-identity.md`](../../../docs/prd/P22-sso-identity.md).
Inherits [ADR-008](../../../docs/adr/ADR-008-console-tenant-identity-seam.md) (the console binds an abstract
tenant principal through one seam; the mechanism was deferred — P22 supplies it),
[ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (the transformed program calls
its own providers; our identity is platform-internal), [ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md)
(fail-static config binding for the tenant map), [ADR-006](../../../docs/adr/ADR-006-console-deploy-packaging.md)
/ [P19 Decision 5](../p19-deployment/design.md) (the operator console is a second origin, disjoint cookie jar),
and [`secrets-baseline.md`](../../../docs/decisions/secrets-baseline.md) §1.1 (the one secret mechanism identity
reuses).

Every decision below is arbitrated on the **八级法则** — the single trade-off law this project uses:

> **安全 > 稳定 > UX > 运维 > 可演进 > 可扩展 > 维护 > 实现**

with its three iron laws: (L1) a higher level's degradation is never traded for a lower level's convenience;
(L2) decide at the highest level that separates the options and do not fall back down for a lower-level
convenience; (L3) 实现 (single-shot implementation cost) is always the floor and never outranks anything above
it.

## Context

P22 is downstream of the identity work that already exists, and its content is a **seam replacement on each of
two disjoint identity domains**, not a new subsystem.

**What already exists (and does not change above the seam):**

- The **customer seam** is ADR-008's `verify(assertion) → { tenantId }`
  ([`web/console/src/lib/identity.ts`](../../../web/console/src/lib/identity.ts)), with two implementations —
  `configured` (a deployment-injected `CONSOLE_TENANT_ASSERTIONS` map, the shape `auth.Registry` already uses)
  and `dev` (local only, refuses to boot in production). Above it, the **session layer**
  ([`session.ts`](../../../web/console/src/lib/session.ts)) holds `{ id, tenantId, issuedAt, expiresAt,
  revokedAt }` server-side, mints an opaque browser token that is not the session id, reads the store on
  **every** request (so revocation is immediate, no grace), and redirects an unauthenticated request rather
  than rendering a shell. None of that changes.
- The **operator seam** already exists and is real: [`internal/adminidentity`](../../../internal/adminidentity/)
  defines `IdentityProvider.Verify(assertion) → Claims`, refuses a session without MFA evidence
  (`ErrMFARequired`), sources SSO/MFA/session-signing keys from the secrets manager
  ([`secrets.go`](../../../internal/adminidentity/secrets.go), reusing `providergateway.Secrets`), and denies a
  disabled principal (`StatusDisabled`). The shipped `HMACProvider` runs in `TestMode`, and its enum comment
  names the seam it fills: *"the integration point a SAML/OIDC admin IdP plugs into"*.
- The platform's **one secret mechanism** (`providergateway.Secrets`, `HEROS_SECRETS_SOURCE`, `/readyz →
  secrets_source`) per `secrets-baseline.md` §1.1, already reused by both P7 billing and `adminidentity`.
- The P19 **`/readyz` aggregation** (`AddComponentProbe`, named degraded components) and the operator surface's
  second-origin / disjoint-cookie-jar posture.

**What is absent and P22-shaped:** any OIDC or SAML verifier; the `/auth/*` redirect/callback routes; a
tenant-mapping strategy richer than a static map; a real (non-fixture) operator IdP; a platform-verified MFA
factor; and the replay/CSRF/redirect-allowlist defenses a redirect flow needs (the current seam has no redirect
flow, so it has none of these attack surfaces yet).

Three properties from the rulebook are non-negotiable and shape every decision: **the assertion is never
persisted, the tenant is authoritative server-side, and a client-supplied tenant never widens scope** (ADR-008
Rules 1 & 2); **secrets never appear in git / manifest / log / trace / bundle**; and **fail closed — an
unreachable IdP issues no login, never fail-open**.

## Decision 1 — The seam is the only thing that changes

**Chosen:** P22 replaces the seam implementation (`verify(assertion) → { tenantId }`) and adds the
redirect/callback routes the flow needs, and **nothing above the seam changes** — session store, cookie,
revocation, scope derivation, fail-closed middleware and every tenant page are byte-for-byte what they were, and
this is asserted by a test, not assumed.

**Why (L1 安全 / L5 可演进).** ADR-008's whole value is that everything above the seam is written against an
abstract authenticated tenant principal and knows nothing about how the assertion was proved. That is the
property that makes the identity mechanism a **replaceable implementation of one function** rather than a
cross-cutting concern; the moment "OIDC" or "SAML" leaks into the session or the scope derivation, the seam
stops being a seam and the next mechanism is a migration for every tenant. ADR-008 Rule 3 says this in exactly
these terms and P22 is the phase that has to keep the promise. The arbitration is L1/L5, not L8: the cheaper
first write couples the session exchange to the mechanism you are building today, which is precisely the door
ADR-008 refused to walk through.

**Rejected — make the session/scope layer "OIDC-aware".** The faster path to a working sign-in and the one
"just make it work" produces. Rejected because it re-opens the one-way door ADR-008 closed, and buys nothing:
the layer above the seam is identical under OIDC, SAML and configured.

## Decision 2 — OIDC (Authorization Code + PKCE) primary, SAML 2.0 enterprise alternative, one seam

**Chosen:** the customer console supports **OIDC Authorization Code flow with PKCE** as the primary mechanism
(`state`, `nonce`, discovery/JWKS-validated ID token) and **SAML 2.0** (SP-initiated, signed assertions,
audience restriction, allowlisted ACS) as the enterprise alternative — **both behind the same seam**, both
resolving to exactly one `tenantId`.

**Why (tie at L1 安全, decided at L5 可演进 / L4 运维).** At L1 the two mechanisms tie: OIDC with PKCE and SAML
2.0 with signed assertions both federate identity safely when implemented correctly, so per L2 the decision
moves down to the level that actually separates them. It settles at 可演进/运维: **OIDC is the lower operational
burden** for the majority of customers (a discovery document, JWKS, JSON) and is where modern IdPs are going,
so it is the *primary*; **SAML is the enterprise procurement reality** — a large buyer's security team often
mandates it — so it is a first-class *alternative*, not an afterthought. Shipping both behind one seam means the
choice is the customer's deployment config, not our code, and neither mechanism is expressed above the seam
(Decision 1). L8 (one mechanism is less code) is real but per L3 cannot be the reason to strand the buyer who
needs the other.

**Rejected — SAML-first / OIDC-only.** SAML-first imposes SAML's operational weight (XML signatures,
canonicalization footguns, metadata exchange) on every customer including the self-hoster who does not need it.
OIDC-only loses the enterprise buyer whose procurement mandates SAML. Both fail L2 by deciding at the wrong
level — the mechanism is a customer-deployment property, not a platform-global one.

**Rejected — the OAuth implicit flow.** It puts a token in a URL fragment, exposing it to the browser and
history. Rejected at L1: Authorization Code + PKCE keeps the token server-side at the BFF, which is also why the
assertion can be dropped (Decision 4) and no key ever reaches the browser.

## Decision 3 — Tenant mapping is configuration, not code

**Chosen:** an SSO identity maps to a tenant by a **configured strategy** — verified-email-domain → tenant,
per-tenant IdP registration (issuer/entityID → tenant), or just-in-time provisioning under an explicit allow
rule — and the map is **configuration** injected the way `CONSOLE_TENANT_ASSERTIONS` and the `Secrets` seam are,
**changeable without a deploy**.

**Why (L5 可演进 / L4 运维).** If the mapping is a hardcoded branch, onboarding a tenant is a code change and a
release — the precise failure the `configured` seam was built to avoid, and the failure ADR-004 fail-static
binding exists to prevent. Making it config means a new enterprise tenant is a config injection, not a deploy;
it also keeps the mapping in the same place the platform already keeps assertion→tenant bindings, so there is
one model, not two. The three strategies cover the real cases (a company with one verified domain; a company
that registers its IdP; a company that wants staff auto-provisioned under a domain it owns) without inventing a
directory sync (SCIM is a deferred follow-up, not M16).

**Rejected — a hardcoded per-tenant branch / a compiled tenant table.** A deploy per customer, and a merge
conflict surface every time two customers onboard in the same window. Rejected at L5.

## Decision 4 — Assertion dropped, tenant authoritative server-side, client never widens

**Chosen:** the OIDC ID token / SAML assertion is verified, exchanged for a session, and **dropped** — not
stored in the session record, not written to a cookie, not logged, not carried upstream; and a tenant
identifier arriving from the client in **any** position (path, query, body, header, a returned `state`) never
widens, changes, or overrides the session's tenant.

**Why (L1 安全).** These are ADR-008 Rules 1 & 2 made normative for the real mechanism. The assertion is the
credential that proves identity; persisting it turns every store, log and trace into a credential store, and the
session only ever needs the *result* (`tenantId`), never the proof. The tenant-authoritative rule is the
standing lesson that a request must not be trusted to describe its own authority — the single most common way a
multi-tenant system leaks across tenants is by reading a tenant hint the client controls. The BFF's **own**
server-held platform credential authorizes upstream calls; the session carries only the tenant.

**Rejected — keep the assertion "for audit" / accept a client tenant hint as a fast path.** Both are the exact
one-way doors ADR-008 named. An assertion kept for audit is a credential kept forever; a client tenant hint is a
cross-tenant escalation waiting for the one code path that trusts it.

## Decision 5 — Operator IdP is real and pluggable behind the existing seam; SSO + platform-verified MFA; disjoint domain

**Chosen:** a real, pluggable **OIDC/SAML admin IdP** plugs into the existing `adminidentity.IdentityProvider`
seam (replacing the fixture `TestMode` HMAC issuer for production), keeping `Verify`/`Describe` and the
enum-named provider kind; every operator authentication requires **SSO and a platform-verified second factor**
(WebAuthn preferred, TOTP fallback), and the operator identity domain stays **disjoint** from the customer
domain — a different origin, a disjoint cookie jar, and a principal type carrying no tenant_id that is a compile
error to confuse with a customer principal.

**Why (L1 安全).** The operator surface is the highest blast radius in the platform: a principal that crosses
tenants and can halt the fleet. Two properties are load-bearing. First, **fill the seam that already exists**
rather than reinvent it — `adminidentity` already documents the OIDC/SAML plug point, already fails closed on a
missing MFA factor, already sources keys from the manager and reads the session store on every request; P22
supplies the real issuer, it does not rebuild the module. Second, **verify the factor rather than trust the
IdP's claim**: `adminidentity`'s own comment observes that *"the IdP requires MFA" is a configuration claim
about a system this code does not control, and a configuration claim is not an invariant* — so P22 makes the
second factor **platform-verified** (WebAuthn/TOTP), so a misconfigured IdP MFA policy still results in denial on
the fleet-halting surface. The disjoint domain is P8 Decision 11 / ADR-006 / P19 Decision 5 inherited verbatim:
isolation is the browser's origin boundary, not routing correctness.

**Rejected — reuse the customer OIDC flow for operators.** It exists and would be cheaper. Rejected at L1
exactly as ADR-008 rejected reusing P8's admin identity for customers: an operator is a **cross-tenant**
principal, and putting a cross-tenant credential on the customer identity path (or vice-versa) is the boundary
violation the two-domain split exists to prevent.

**Rejected — trust the IdP's MFA policy as sufficient (no platform verification).** Cheaper, and the current
fixture's shape. Rejected on NFR8: on the surface that can halt the fleet, "MFA is required" must be an
invariant the platform asserts, not a setting in a system we do not control.

## Decision 6 — Every identity secret through the `Secrets` seam, none in git

**Chosen:** the OIDC client secret, the SAML SP signing/decryption private key, and every session/signing key
are resolved through the `providergateway.Secrets` seam (`HEROS_SECRETS_SOURCE`), with an **ambient identity**
where the store supports it (IRSA / workload identity) so there is **no bootstrap secret** in a manifest; none
appears in git, an env-example, a client bundle, a log line, or a trace attribute.

**Why (L1 安全).** The platform has exactly one answer to "where do credentials come from at the moment of use",
and `adminidentity.secrets.go` already reuses it for operator signing keys for a stated reason: a deployment
whose LLM and billing credentials come from a manager while the key that signs sessions quietly comes from an
env var — with `/readyz` confidently reporting the manager — is a lie about its own posture. Identity secrets
are the worst to leak because they mint identities, and a client secret in a committed manifest is a plaintext
credential in git the moment it lands — an irreversible one-way door. Ambient authentication to the store means
wiring identity creates no new bootstrap secret; the alternative (a long-lived key in an env var to reach the
manager) has moved the secret, not removed it (`secrets-baseline.md` §1.1).

**Rejected — a client secret in a config field / env-example, or a fourth secret mechanism for identity.** The
config field is the `config.Config` path provider credentials were deliberately taken *off* at M2; a fourth
mechanism splits the one truth `/readyz` reports. Both rejected at L1.

## Decision 7 — Session & revocation model unchanged; revocation immediate, no grace

**Chosen:** the session model is ADR-008's, unchanged — server-side `{ id, tenantId, issuedAt, expiresAt,
revokedAt }`, an opaque browser token that is not the session id, bounded TTL, and the store **read on every
request** so a revoked or IdP-disabled session is denied at the **next request with no grace period**. A refresh
re-establishes the session by re-verifying, never silently extends past a configurable bound.

**Why (L1 安全 / L2 稳定).** The value of a stolen session to an attacker is time; immediate revocation bounds it
to one request. A self-contained token that vouches for its own expiry cannot be revoked, and "revocable at the
next refresh" is not revocable — the exact reasoning both `session.ts` and `adminidentity` already encode by
reading the store on every request rather than trusting a token's own claim. Keeping the model unchanged is also
Decision 1 in practice: the session layer is above the seam and does not move. A refresh that re-verifies (a
fresh authorization or a validated refresh-token exchange) keeps the immediacy property; a refresh that extends a
session indefinitely would reintroduce the grace window revocation-on-every-read exists to eliminate.

**Rejected — a self-contained JWT session / an indefinitely-extending refresh.** The JWT is unrevocable without
a denylist that reintroduces exactly the server-side store it was meant to avoid; the extending refresh is a
grace window by another name. Both rejected at L1.

## Decision 8 — Fail closed when the IdP is unreachable; reachability on `/readyz`

**Chosen:** when the IdP is unreachable — OIDC discovery/JWKS or SAML metadata cannot be fetched or validated —
sign-in **fails and no session is issued**; the surface never fails open, never issues a session from a cached
credential; `/readyz` reports `identity_provider: {kind, issuer, reachable}` and reports **not ready**, naming
`identity_provider`, when the IdP is unreachable.

**Why (L1 安全 / L2 稳定).** A login surface that fails open when its IdP is down authenticates *everyone* at the
worst possible moment; the only safe direction to fail on an authentication boundary is closed. The signal must
measure the right dimension — IdP **reachability** and assertion validity, not traffic freshness — and must not
depend on the very traffic it gates (the same fail-closed discipline `secrets-baseline.md` §1.1 and P19 FR13
require of the secret source). Putting identity on the P19 `/readyz` aggregation makes the claim externally
checkable by a monitor on the box in question, rather than a log line that says so; a UI is never the health
verdict (🔴 `health-signal-surface`).

**Rejected — fail-open "allow if IdP down" / a cached-credential fallback login.** The first is an availability
choice that trades away the entire security property of the boundary; the second issues sessions against a
credential the IdP can no longer vouch for. Both rejected at L1 (禁止静默回落).

## Decision 9 — Replay / CSRF / open-redirect closed first-class

**Chosen:** the callback enforces a **single-use `state` bound to the browser** (CSRF), a **`nonce`** binding the
ID token to the request, **PKCE** binding the code to the client, an assertion **freshness window** plus a
**one-time** guard (replay), and a **redirect-URI / SAML ACS allowlist** (open redirect). An assertion outside
its window or seen twice, a missing/forged `state`, a reused code, or an off-allowlist redirect target is
refused with a single generic reason.

**Why (L1 安全).** A redirect flow is a new attack surface the current seam does not have, and each defense
closes a specific, well-known hole: without `state`, the callback is CSRF-able; without `nonce`, an ID token can
be replayed from another session; without PKCE, an intercepted code is usable; without a freshness/one-time
bound, a captured assertion is a permanent credential (the same bound `adminidentity` already applies with
`MaxAssertionAge`); without a redirect allowlist, the flow is an open redirect and a token-exfiltration vector.
The generic refusal reason mirrors both seams' existing posture — distinguishing "no such assertion" from "bad
signature" tells an attacker which half they got wrong and helps no real user.

**Rejected — a reflected or wildcard redirect target / distinguishing refusal reasons.** A reflected redirect is
an open redirect by construction; distinct refusal reasons are a probing oracle. Both rejected at L1.

## Interfaces sketch

The customer seam contract is **unchanged** — only its implementation and the routes around it are new:

```
web/console/src/lib/
  identity.ts        # verify(assertion) → { tenantId }   (CONTRACT UNCHANGED)
                     #   PROVIDER ∈ { oidc, saml, configured, dev }   (dev refuses production, as today)
                     #   oidc:  Auth-Code + PKCE, state, nonce, JWKS-validated ID token → tenant map
                     #   saml:  signed assertion, audience restriction, allowlisted ACS → tenant map
  session.ts         # UNCHANGED — { id, tenantId, issuedAt, expiresAt, revokedAt }, store read every request
  middleware.ts      # UNCHANGED — fail-closed by route prefix
app/auth/
  login/route.ts     # begins the flow: build authz URL, set single-use state+PKCE verifier (HttpOnly)
  callback/route.ts  # verify state+nonce+PKCE, verify assertion, map → tenantId, issueSession(), drop assertion
  saml/acs/route.ts  # SAML Assertion Consumer Service (allowlisted), same verify → issueSession → drop

# tenant mapping — CONFIG, not code (injected like CONSOLE_TENANT_ASSERTIONS / via Secrets):
#   CONSOLE_IDP_TENANT_MAP = {
#     "strategy": "domain" | "per-issuer" | "jit",
#     "issuers":  { "<issuer|entityID>": { "tenant": "<id>", "verified_domains": ["acme.com"] } },
#     "jit_allow": [ "acme.com" ]        # JIT only under an explicit allow rule
#   }
```

Operator seam — the interface **already exists** (`internal/adminidentity`); P22 adds a real implementation:

```
internal/adminidentity/
  authn.go   # IdentityProvider { Verify(ctx, Assertion) (Claims, error); Describe() ProviderInfo }  (UNCHANGED)
             #   ProviderKindHMAC = "admin-idp-hmac"   (fixture, TestMode)
             # + ProviderKindOIDC / ProviderKindSAML   (NEW real, pluggable — same interface)
             # + platform-verified second factor: WebAuthn (preferred) / TOTP (fallback)
  secrets.go # UNCHANGED — SSO/MFA/session keys via providergateway.Secrets, fail-closed
  session.go # UNCHANGED — short-TTL, revocable, store read every request; Disable ⇒ RevokeAllFor
```

`/readyz` (extended, `internal/api/server.go` — mirrors the P19 `secrets_source` shape):

```json
{
  "status": "not_ready",
  "components": {
    "identity_provider": {"status": "not_ready", "kind": "oidc",
                          "issuer": "https://acme.okta.com", "reachable": false},
    "admin_idp":         {"status": "ready", "kind": "admin-idp-oidc",
                          "issuer": "https://idp.heros.internal", "test_mode": false},
    "secrets_source":    {"status": "ready", "kind": "aws-secrets-manager"}
  },
  "degraded": ["identity_provider"]
}
```

## Risks

- **The mechanism leaks above the seam** → Decision 1 + the NFR1 "unchanged above the seam" regression test; a
  diff touching a file above the seam other than to call it is a review failure.
- **Cross-tenant resolution** (a self-asserted claim reaches another tenant) → Decision 3: map only to a
  **proven** domain / registered issuer, refuse unmapped identities, JIT never crosses a boundary; tested
  adversarially first.
- **An identity secret reaches git** → Decision 6: `Secrets` seam + ambient identity, no bootstrap secret;
  gitleaks + the console bundle scan + an apply-time lint; a committed identity secret fails CI.
- **Callback CSRF / assertion replay / open redirect** → Decision 9: single-use browser-bound `state`, `nonce`,
  PKCE, freshness + one-time guard, redirect/ACS allowlist; each defense has a red-able test.
- **Fail-open on IdP outage / cached-credential login** → Decision 8: fail-closed, no fallback; `/readyz` names
  `identity_provider`; the signal measures reachability, not traffic.
- **Operator MFA is a claim, not an invariant** → Decision 5: platform-verified WebAuthn/TOTP; valid SSO + no
  verified factor ⇒ no session.
- **Operator surface reachable from a customer session** → Decision 5: disjoint origin + cookie jar + principal
  type; no promotion path from `auth.Principal`; a cross-origin unreachability test.
