# Tasks — P22: SSO & Identity

Ordered by workstream. P22 replaces the ADR-008 seam on two disjoint identity domains and adds the redirect/
callback routes each mechanism needs — and **nothing above the seam**. Each task is independently verifiable.
Every PR carries an **identity-form impact matrix** (customer OIDC / customer SAML / customer configured /
operator) with every "not affected" row explaining *why*.

## 1. System Designer + Backend — Decide the one-way doors first (blocks everything else)
- [ ] 1.1 Ratify **D1 (the seam is the only thing that changes)**, **D2 (OIDC primary, SAML enterprise, one
      seam)**, and **D5 (operator IdP real+pluggable behind the existing seam; disjoint domain)** in `design.md`
      — these are the published federation contract (a one-way door), decided before any verifier is written.
- [ ] 1.2 Define the **federation contract**: the trusted issuer set, the claims mapped (`sub`, `email`,
      domain / `NameID`), and the assertion validity/freshness bounds — one source the OIDC and SAML verifiers
      both derive from.
- [ ] 1.3 Confirm the **tenant-mapping config shape** (`domain` / `per-issuer` / `jit` with an explicit allow
      rule), injected like `CONSOLE_TENANT_ASSERTIONS` / via the `Secrets` seam — no compiled per-tenant branch.

## 2. Backend — The customer seam implementation (OIDC first, behind the unchanged contract)
- [ ] 2.1 Implement the **OIDC (Authorization Code + PKCE)** provider behind `verify(assertion) → { tenantId }`:
      discovery/JWKS validation, `state`, `nonce`, ID-token verification; the seam contract is unchanged.
- [ ] 2.2 Implement the **tenant mapping** (domain / per-issuer / JIT-under-allow-rule) from injected config;
      refuse an unmapped identity as a **security event**; honor only a **proven** domain (no cross-tenant
      resolution).
- [ ] 2.3 Ensure the **assertion is dropped** — verified, exchanged for a session, never stored/cookied/logged/
      traced/carried upstream; the BFF's own credential authorizes upstream calls.
- [ ] 2.4 Implement the **SAML 2.0** provider behind the same seam: signed assertion, audience restriction,
      allowlisted ACS; resolves to exactly one `tenantId`.

## 3. Frontend — The redirect/callback routes and the no-key/shell rules
- [ ] 3.1 Add `/auth/login`, `/auth/callback`, and the SAML **ACS** route in the BFF; set the single-use
      browser-bound `state` + PKCE verifier as `HttpOnly`, consume the assertion server-side, call the existing
      `issueSession()`.
- [ ] 3.2 Honor the **shell rule**: an unauthenticated request or a failed callback **redirects** to sign-in with
      a `reason` (session-ended vs sign-in vs IdP-unreachable vs not-provisioned) — never a broken shell.
- [ ] 3.3 Honor the **no-key rule**: the client secret and platform credential stay server-side; the browser
      holds only the opaque `HttpOnly` session token; the ID token / SAML assertion never reaches client JS or a
      URL fragment. Extend the **bundle scan** to the client id/secret surface.
- [ ] 3.4 Keep **build artifact and runtime config separate**: issuer, client id, redirect allowlist and mapping
      strategy arrive as **runtime** injection so one image federates against different IdPs without a rebuild.

## 4. Backend — Session, revocation, fail-closed (assert unchanged above the seam)
- [ ] 4.1 Assert by **regression test** that the session store, cookie flags, revocation, scope derivation and
      fail-closed middleware are **unchanged** by P22 (ADR-008 Rule 3, NFR1) — the only changed files are the
      seam and the `/auth/*` routes.
- [ ] 4.2 Confirm **revocation is immediate, no grace** (store read every request) and that a **refresh
      re-verifies** rather than silently extends; no self-vouching token is used as the session.
- [ ] 4.3 Implement **fail-closed** sign-in: an unreachable IdP (OIDC discovery/JWKS or SAML metadata) issues
      **no session**; no cached-credential login; no silent fallback to a weaker mechanism.

## 5. Backend — Identity security posture (replay / CSRF / open-redirect / secrets)
- [ ] 5.1 Enforce **CSRF/replay defenses** at the callback: single-use browser-bound `state`, `nonce`, PKCE, an
      assertion **freshness window** + a **one-time** guard; a single generic refusal reason.
- [ ] 5.2 Enforce the **redirect / SAML ACS allowlist**; refuse an off-allowlist or reflected/wildcard target.
- [ ] 5.3 Resolve **every identity secret** (OIDC client secret, SAML SP private key, session/signing keys)
      through the `Secrets` seam with an ambient identity and **no bootstrap secret**; add the apply-time lint +
      extend gitleaks so a committed identity secret fails CI.

## 6. Backend — Operator SSO + MFA made real (P8 surface)
- [ ] 6.1 Implement a **real, pluggable OIDC/SAML admin IdP** behind the existing
      `adminidentity.IdentityProvider` seam (new provider kinds alongside `admin-idp-hmac`), keeping
      `Verify`/`Describe`; the fixture `TestMode` issuer refuses production.
- [ ] 6.2 Add a **platform-verified second factor** (WebAuthn preferred, TOTP fallback): valid SSO + no verified
      factor ⇒ **no session** (`ErrMFARequired`); the platform's verification, not the IdP's claim, is the
      invariant.
- [ ] 6.3 Keep the operator domain **disjoint** (separate origin, disjoint cookie jar, principal type with no
      tenant_id, no promotion path from `auth.Principal`); **disable ⇒ revoke all sessions** for the principal.
- [ ] 6.4 Source operator identity secrets from the manager under the reserved logical names, **fail closed**,
      and report the live `admin_idp` on `/readyz`.

## 7. DevOps + Backend — Readiness and secret wiring
- [ ] 7.1 Extend `/readyz` (`internal/api/server.go`) to aggregate **`identity_provider: {kind, issuer,
      reachable}`** (customer) alongside the existing `admin_idp` and `secrets_source`, naming it when degraded;
      the signal measures **reachability**, not traffic, and does not depend on the traffic it gates.
- [ ] 7.2 Wire identity secrets to the store paths — env (dev), AWS Secrets Manager (managed, per
      `secrets-baseline.md`), an on-prem equivalent for air-gapped — with **no bootstrap secret**; the
      provider→secret mapping is **configuration, not code**.

## 8. QA — The acceptance gate (adversarial before happy-path green)
- [ ] 8.1 **Seam-unchanged** regression: assert the whole layer above the seam is byte-for-byte unchanged (NFR1).
- [ ] 8.2 **Cross-tenant resolution refused** (NFR9, first): a self-asserted domain from a foreign IdP is
      refused; JIT never crosses a tenant boundary; an unmapped identity is a security event, not a signup.
- [ ] 8.3 **Assertion-never-persisted** grep (NFR2) and **forged-tenant-never-widens** (NFR3), tested
      adversarially in every client-controlled position.
- [ ] 8.4 **Security mechanisms tested by removal** (NFR7): replayed `state`, reused code, stale/replayed
      assertion, off-allowlist redirect each **refused**; removing a defense turns a test **red**.
- [ ] 8.5 **Fail-closed** (NFR5): IdP down ⇒ **no session**, no cached-credential login; `/readyz` names
      `identity_provider`.
- [ ] 8.6 **Operator MFA by denial** (NFR8): valid SSO + no verified factor ⇒ no session; a **disabled operator**
      obtains no session and their live sessions are revoked on disable; the operator surface is **unreachable
      from a customer origin** (cross-origin test).
- [ ] 8.7 **Identity-form matrix**: customer OIDC, customer SAML, customer configured (open-core), and operator
      are each exercised; a capability that works in only one form is a bug.

## 9. Product Designer + Sales Operations — Messages and the honest boundary
- [ ] 9.1 Write the sign-in / MFA messages: define every term; distinguish **"session ended" / "sign in" / "IdP
      unreachable" / "not provisioned for this tenant"**; leak **no internal mechanism** (secret logical name,
      provider-kind literal, issuer allowlist); single-source the glossary.
- [ ] 9.2 Encode the **honest commitment boundary**: sell **SSO federation (OIDC + SAML) + verified operator
      MFA** (built); do **not** commit SCIM, a per-seat user/audit model, or transformed-program identity
      (ADR-002); state that we run **no password database / home-grown IdP** as a differentiator; per-seat
      revocation is a **next-request** effect, stated as such.
- [ ] 9.3 Honesty gates: **no price value and no plan gate** on the identity path (identity proves *who*;
      entitlement is P7, payments P21); no operator-facing message implies SSO is paywalled by wiring a plan
      check into the seam.

## 10. Documentation & fold-in
- [ ] 10.1 Cross-link the PRD, this change, and the ADRs it inherits (002/004/006/008) and `secrets-baseline.md`;
      add the P22 row to `docs/prd/README.md`.
- [ ] 10.2 On deploy, fold the two delta specs (`sso-identity`, `operator-sso-mfa`) into `openspec/specs/` (drop
      the `## ADDED` headers).

## Verification record
- [ ] V1 M16 exit checklist (PRD §13) fully green across all four identity forms (customer OIDC / customer SAML /
      customer configured / operator).
- [ ] V2 Identity-form impact matrix attached to every P22 PR, with the seam-unchanged (NFR1), assertion-never-
      persisted (NFR2), tenant-never-widened (NFR3) and cross-tenant-refused (NFR9) fences proven able to go red.
