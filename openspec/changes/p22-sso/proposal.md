## Why

[ADR-008](../../../docs/adr/ADR-008-console-tenant-identity-seam.md) built the entire customer console —
sessions, cookies, revocation, scope derivation, fail-closed routing, every page — against an abstract
authenticated tenant principal behind **one function**: `verify(assertion) → { tenantId }`. It then
**deferred the mechanism** on purpose, because choosing OIDC/SAML is a published federation contract with the
customer's IT organization (a one-way door 🔴 `careful-api-creation`) whose cost, if wrong, is a migration for
every tenant. Today the seam ships two honest implementations and no third: `configured` (a deployment-injected
assertion→tenant map, the shape `auth.Registry` already uses) and `dev` (local only, refuses to boot in
production). Neither is single-sign-on. There is no `/me`, no token endpoint, no OIDC/SAML anywhere in the
repo. Enterprise buyers will not put staff on the console until it federates against their own IdP, and that
mechanism is precisely what ADR-008 reserved for later. **P22 is that later.**

On the operator surface the situation is different in kind: the identity module is already real.
[`internal/adminidentity`](../../../internal/adminidentity/) authenticates a cross-tenant **operator**
principal through a dedicated IdP, refuses a session without MFA evidence, sources every signing key from the
secrets manager, and reads the session store on every request so revocation is immediate. What is a fixture is
the *issuer*: the shipped `HMACProvider` runs in `TestMode`, and its own comment names the seam it fills —
*"the integration point a SAML/OIDC admin IdP plugs into"* ([`authn.go`](../../../internal/adminidentity/authn.go)
`ProviderKindHMAC`). And the MFA it checks is a *claim* the IdP signs, not a factor the platform itself
verifies. P22 makes that provider **real and pluggable** and adds a **platform-verified** second factor, so
operator MFA is an invariant the platform asserts rather than a configuration it trusts.

P22 changes the seam on each of two disjoint identity domains and **nothing above it** — honoring ADR-008
Rule 3: *"The seam is the only thing P7 changes … Sessions, revocation, scope derivation, fail-closed routing
and every page above are untouched."* It reuses the platform's one secret mechanism (`providergateway.Secrets`,
per `secrets-baseline.md` §1.1) rather than inventing a fourth, and puts identity reachability on the P19
`/readyz` aggregation so an unreachable IdP fails **closed**, never open.

## What Changes

- **Customer console SSO — real mechanism behind the unchanged seam.** Replace the `configured` seam's static
  map with an **OIDC (Authorization Code + PKCE)** verifier as the primary mechanism and a **SAML 2.0**
  verifier as the enterprise alternative, both resolving to exactly one `tenantId`. Add the `/auth/login`,
  `/auth/callback` and SAML ACS routes the flow needs. **No change above the seam** — session store, cookie,
  revocation, scope derivation, fail-closed middleware and every tenant page are untouched (asserted by test).
- **Multi-tenant IdP mapping as configuration.** An SSO identity maps to a tenant by a configured strategy —
  verified-email-domain → tenant, per-tenant IdP registration (issuer/entityID → tenant), or just-in-time
  provisioning under an explicit allow rule — injected like `CONSOLE_TENANT_ASSERTIONS` / the `Secrets` seam,
  **changeable without a deploy**. An identity matching no rule is refused (a security event, not a signup);
  domain mapping honors only a **proven** domain.
- **Assertion never persisted; tenant authoritative server-side.** The OIDC ID token / SAML assertion is
  verified, exchanged for a session, and dropped — never stored, cookied, logged, or carried upstream. A
  client-supplied tenant (path/query/body/header/`state`) never widens the session's tenant.
- **Operator console SSO + MFA made real (P8 surface).** Provide a real, pluggable **OIDC/SAML admin IdP**
  behind the existing `adminidentity.IdentityProvider` seam (replacing the fixture `TestMode` issuer for
  production), and a **platform-verified** second factor (WebAuthn preferred, TOTP fallback). Keep the operator
  domain **disjoint** from the customer domain (separate origin, disjoint cookie jar, a principal type that is a
  compile error to confuse). Disabling an operator revokes their live sessions.
- **Identity security posture as first-class requirements.** No identity secret (OIDC client secret, SAML SP
  private key, session/signing keys) in git/manifest/env-example/bundle/log/trace — resolved through the
  `Secrets` seam with an ambient identity and no bootstrap secret. Single-use browser-bound `state`, `nonce`,
  PKCE, assertion freshness + one-time replay guard, and a redirect/ACS **allowlist**. **Fail-closed** when the
  IdP is unreachable — no login, never fail-open, no cached-credential login — with `/readyz` reporting
  `identity_provider: {kind, issuer, reachable}` and naming it when degraded.
- **Non-goals (not built):** no password database or home-grown IdP; **no** entitlement/billing change (P7/P21);
  the transformed program's identity is out of scope (ADR-002); SCIM/directory provisioning and a first-class
  per-user model are deferred follow-ups.

## Impact

- **Affected capabilities:** `sso-identity` (**new** — the customer OIDC/SAML seam, tenant mapping, session/
  revocation/fail-closed, and the identity security posture); `operator-sso-mfa` (**new** — the P8 operator
  surface made real). Both are delta specs under this change; folded into `openspec/specs/` on deploy.
- **Affected code/systems:** `web/console/src/lib/identity.ts` (the seam implementation) and the new
  `/auth/*` routes + BFF; `web/console` session/middleware layer **unchanged** (regression-asserted);
  `internal/adminidentity` (real provider behind the existing `IdentityProvider` seam + platform-verified MFA);
  `web/admin-console` sign-in/callback; `internal/api` `/readyz` (add `identity_provider` component; `admin_idp`
  already present); the `providergateway.Secrets` wiring (identity secrets).
- **Dependencies:** **upstream** — ADR-008 (the seam + everything above it), P9 web console, P8 operator console
  + `internal/adminidentity`, the `providergateway.Secrets` seam + `secrets-baseline.md` §1.1, ADR-002
  (transformed-program-identity boundary), ADR-004 (fail-static config), ADR-006 / P19 Decision 5 (operator
  second origin), P19 `/readyz` aggregation. **Unblocks** — enterprise/federated go-to-market; a first-class
  `user` model with per-user revocation/audit; SCIM provisioning; P21 payments (attaches a method to a proven
  identity). **Not depended on:** a password store; P7/P21 entitlement/billing (identity does not read or write
  it).
