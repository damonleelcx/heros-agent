# PRD — P22: SSO & Identity (the seam ADR-008 deferred, made real)

| Field | Value |
|---|---|
| Phase / Milestone | P22 / M16 (identity GA gate; precedes P21 payments) |
| Target window | Lands as a wave alongside P7 entitlement; unblocks enterprise/federated sign-on |
| Lead role(s) | System Designer + Backend (co-leads) |
| Supporting role(s) | Frontend, DevOps, Product Designer, QA Engineer, Sales Operations |
| Status | **V1 + V2 green (2026-07-31)** against a signing IdP this repository runs; **real-IdP (Okta) integration — V3 — not yet run** |
| OpenSpec change | `p22-sso` |

> **Scope discipline.** P22 replaces **one function** and adds the redirect/callback routes that function
> needs. [ADR-008](../adr/ADR-008-console-tenant-identity-seam.md) built the entire customer console —
> sessions, cookies, revocation, scope derivation, fail-closed routing, every page — against an abstract
> authenticated tenant principal behind the seam `verify(assertion) → { tenantId }`, and reserved the
> *mechanism* for later. P22 **is** that later. It changes the seam and **nothing above it**. On the
> operator surface P8 already shipped a real SSO+MFA identity module ([`internal/adminidentity`](../../internal/adminidentity/))
> whose provider interface is documented as *"the integration point a SAML/OIDC admin IdP plugs into"*
> ([`authn.go`](../../internal/adminidentity/authn.go) `ProviderKindHMAC`); P22 makes that pluggable
> provider **real**, it does not reinvent the module.

> **The one-sentence job.** *Prove who a tenant is against their own identity provider, resolve exactly
> one `tenantId`, and let the client never widen that answer* — the standing lesson (ADR-008 Rule 2) that
> a request must not be trusted to describe its own authority.

## 1. Summary

The platform can authenticate a **credential** (`internal/auth` maps an API key to
`Principal{TenantID, Role}`) and it can hold a **session** (the P9 console mints a server-side session for
a verified tenant principal, revocable at the next request with no grace). What it cannot yet do is prove
that a *person* is who they claim to be against an identity provider the customer already runs — because
ADR-008 deliberately deferred that mechanism to avoid choosing a published contract with the customer's IT
organization as a side effect of building a UI. Today the customer seam ships two implementations and no
third: `configured` (a deployment-injected assertion→tenant map, the same shape `auth.Registry` uses) and
`dev` (local only, refuses to boot in production). Both are honest, and neither is single-sign-on. P22
delivers the real mechanism: **OIDC (Authorization Code + PKCE)** as the primary customer flow and **SAML
2.0** as the enterprise alternative, **both behind the unchanged `verify(assertion) → { tenantId }` seam**,
so that when P22 lands the session store, the cookie, the fail-closed middleware, the scope derivation and
every tenant page are byte-for-byte what they were — the one file that changes is the seam, plus the
redirect/callback routes the flow requires.

On the operator surface the work is different in kind: the identity module is already real. `internal/adminidentity`
authenticates an **operator** principal (no tenant_id, cross-tenant, can halt the fleet) through a dedicated
IdP, requires a **verified MFA factor** separately from SSO, sources every signing key from the secrets
manager, and reads the session store on every request so revocation is immediate. What is a fixture today is
the *issuer*: the shipped `HMACProvider` verifies test-mode signed assertions, and the module's own comment
names the seam it fills — a real SAML/OIDC admin IdP. P22 delivers that real, pluggable operator provider
and a **platform-verified** MFA factor (WebAuthn preferred, TOTP fallback), keeping the operator identity
domain **disjoint** from the customer domain (different origin, different cookie jar, a compile-time-distinct
principal type — [ADR-006](../adr/ADR-006-console-deploy-packaging.md) / P19 Decision 5).

The load-bearing invariants are all L1 安全: **the assertion is never persisted** (verified, exchanged for a
session, dropped); **the tenant is authoritative server-side** and a client-supplied tenant never widens
scope; **no secret** — client secret, SAML signing certificate private key, OIDC signing key — ever reaches
git, a manifest, a bundle, or a log, resolved instead through the same `Secrets` seam P7 and `adminidentity`
already use; the flow carries **state, nonce, PKCE, a redirect-URI allowlist and assertion replay bounds**;
and when the IdP is **unreachable the surface fails closed** — no login, never fail-open — with `/readyz`
reporting the identity provider's reachability as an aggregated component (P19 readiness). **M16 — identity**
means a customer federates the console against their own Okta/Entra/Ping via OIDC or SAML with no new
password store on our side, an operator signs in through a real IdP with a real second factor, and the two
domains cannot reach each other by construction.

## 2. Problem & context

ADR-008 named the gap precisely and then held it open on purpose. Five problems make "identity" a phase
rather than a patch, and each maps to a design commitment, not a library call.

- **🔴 The customer console proves a *credential*, not a *person*, and cannot federate.** `internal/auth`
  binds an API key to `Principal{TenantID, Role, APIKeyID}` from configuration; the console's `configured`
  seam reads a static `CONSOLE_TENANT_ASSERTIONS` map ([`identity.ts`](../../web/console/src/lib/identity.ts)).
  Neither can accept an assertion from the customer's own IdP, which is exactly what an enterprise buyer's
  security review requires before they will put their staff on the console. There is **no** `/me`, no token
  endpoint, no OIDC/SAML integration anywhere in the repository (ADR-008 Context). SSO is the missing
  mechanism, and it is a published federation contract with the customer's IT organization — a one-way door
  (🔴 `careful-api-creation`) that must be decided deliberately, not fall out of a UI change.
- **🔴 "Pick OIDC and build it into the session exchange" is the tempting wrong move ADR-008 already
  rejected.** The session, revocation, scope derivation and fail-closed routing are **identical** under every
  candidate mechanism, so building them *around* a specific mechanism would couple a stable, tested surface to
  a choice that has enterprise constraints (customer IdP federation, SCIM, per-seat revocation) still in view.
  The cost of being wrong is a migration for every existing tenant. P22 must therefore land entirely **inside
  the seam** and prove that nothing above it moved.
- **🔴 Multi-tenant IdP mapping cannot be code, or every new customer is a deploy.** A federated identity
  arrives as `sub@issuer` (OIDC) or a `NameID` + email domain (SAML); the platform must resolve it to exactly
  one authoritative `tenantId`. If that mapping is a hardcoded branch, onboarding a tenant means a code change
  and a release — the precise failure `configured` was built to avoid. The mapping (domain-based, per-tenant
  IdP registration, just-in-time provisioning) has to be **configuration a secrets/config source injects**,
  changeable without a deploy, consistent with ADR-004 fail-static binding.
- **The operator IdP is real but its issuer is a fixture, and its MFA is a *claim*, not a verification.**
  `adminidentity` verifies signed assertions and refuses a session without MFA evidence — but the shipped
  `HMACProvider` runs in `TestMode` against a fixture issuer (`p8hermes`), and the MFA it checks is *evidence
  the IdP says it verified*, signed with a separate key. That is the right shape (the platform denies if the
  IdP's MFA policy is ever misconfigured), and it is not yet a real IdP or a factor the platform itself
  verifies. The module's comment already names the seam a real SAML/OIDC admin IdP plugs into; P22 fills it,
  and adds a **platform-verified** WebAuthn/TOTP factor so operator MFA is an invariant the platform can
  assert, not only a claim it can refuse.
- **A secret on the identity path is the worst secret to leak, and identity flows are full of them.** An OIDC
  client secret, a SAML SP signing-certificate private key, a session-signing key — any one committed to a
  manifest is a plaintext credential in git the moment it lands, and it is the credential that mints
  *identities*. The repository already has exactly one answer to "where do secrets come from at the moment of
  use" (the `providergateway.Secrets` seam, `secrets-baseline.md` §1.1, reused verbatim by `adminidentity`);
  a fourth mechanism for identity secrets would split that truth and is forbidden. Identity also introduces
  its own attack surface — CSRF on the callback, an open redirect, a replayed assertion, an implicit-flow
  token in a URL fragment — none of which the current seam has to defend because it has no redirect flow yet.
- **🔴 Both verifiers are green, and neither has met an IdP this repository did not write.** The OIDC and SAML
  paths pass end to end over real sockets — but against a signing IdP built here, for these tests. That is the
  right way to prove a *refusal* (a fixture can be told to send a stale assertion, and Okta cannot), and it
  leaves one class of failure entirely untested: the things a real provider does that a fixture author never
  thinks to imitate. A discovery document whose `issuer` differs from the URL it was fetched from; a JWKS
  carrying several keys and rotating them on the provider's own schedule; an `e` encoded with padding; a SAML
  response where the *assertion* is signed and the response is not; a signing certificate replaced during a
  rollover window. Each of those breaks a verifier that passes every fixture test, and the repository already
  says so — [`liveidp_test.go`](../../internal/adminidentity/liveidp_test.go) exists precisely to ask a real
  org those questions, and **it skips**, because no org has been pointed at it. **Naming a real provider is
  also a product decision, not only a test**: "we federate with OIDC" is a standard; "we federate with Okta"
  is something a buyer's security team can verify in an afternoon, and it is the sentence they ask for.

## 3. Goals & non-goals

### Goals

- **G1 — The seam is the only thing that changes (ADR-008 invariant).** P22 replaces `verify(assertion) →
  { tenantId }` with an OIDC/SAML-backed implementation and adds the redirect/callback routes the flow needs;
  the session store, cookie, revocation, scope derivation, fail-closed middleware and every tenant page are
  **untouched** and this is asserted by test, not assumed.
- **G2 — OIDC (Authorization Code + PKCE) primary, SAML 2.0 enterprise alternative, one seam.** Both mechanisms
  resolve through the same seam to exactly one `tenantId`; the flow uses the Authorization Code grant with PKCE
  (never implicit), `state`, and `nonce`; SAML uses signed assertions with an allowlisted ACS and audience
  restriction. Everything above the seam is mechanism-agnostic.
- **G3 — Multi-tenant IdP mapping is configuration, changeable without a deploy.** An SSO identity maps to a
  tenant by configured strategy (verified email domain → tenant, per-tenant IdP registration, or just-in-time
  provisioning under an explicit allow rule); the map is injected like `CONSOLE_TENANT_ASSERTIONS` / the
  `Secrets` seam, never a code branch, and a new tenant onboards without a release.
- **G4 — The tenant is authoritative server-side; the client never widens it.** The verified assertion yields
  the tenant; a tenant identifier arriving in any client-controlled position (path, query, body, header, a
  forged `state`) never changes, widens or overrides the session's tenant. The assertion is verified, exchanged
  for a session, and dropped — never persisted, logged, cookied, or carried upstream.
- **G5 — The operator console gets a real, pluggable IdP and a platform-verified MFA factor.** A real
  OIDC/SAML admin IdP plugs into the existing `adminidentity.IdentityProvider` seam; every operator
  authentication presents SSO **and** a verified second factor (WebAuthn preferred, TOTP fallback) the platform
  itself verifies; the operator identity domain stays disjoint from the customer domain (separate origin,
  disjoint cookie jar, a principal type that is a compile error to confuse).
- **G6 — Sessions, revocation and refresh are unchanged and fail-closed.** Bounded session TTL; revocation
  effective at the **next request with no grace period** (the store is read on every request); a refresh that
  re-verifies rather than silently extends; login/logout/callback routes that redirect an unauthenticated
  request rather than render a broken shell.
- **G7 — No identity secret leaks on any path.** OIDC client secret, SAML SP private key, and every session/
  signing key are resolved through the `Secrets` seam with an ambient identity where available; none appears in
  git, a manifest, an env-example, a client bundle, a log line, or a trace attribute — enforced as build-/apply-
  time gates, not review habits.
- **G8 — The flow defends against replay, CSRF and open redirect first-class.** `state` is single-use and
  bound to the browser; `nonce` binds the ID token to the request; PKCE binds the code to the client; the
  redirect/ACS URI is an **allowlist**; an assertion outside its freshness window or seen twice is refused with
  a single generic reason.
- **G9 — Identity reachability is on `/readyz`.** The identity provider is an aggregated readiness component:
  when the IdP (OIDC discovery / JWKS / SAML metadata) is unreachable the surface **fails closed** — no login —
  and `/readyz` reports **not ready** and **names** `identity_provider`, consistent with P19 readiness
  aggregation and never fail-open.
- **G10 — Okta is the first *named*, *verified* real identity provider, on both domains, and it earns no
  Okta-specific code.** A real Okta org SHALL be the reference deployment for the customer console (OIDC and
  SAML) and for the operator console, proven by a completed sign-in rather than by a fixture — and the only
  thing that is Okta-shaped SHALL be **configuration and a registration recipe**. If federating with Okta
  requires a branch anywhere in a verifier, the abstraction has already failed and the next provider is a
  second branch (八级法则 **可扩展**). Entra ID, Ping and Google Workspace stay expressible on the same
  configuration; what P22 additionally commits to is that **one** of them has actually been run.
- **G11 — What an IdP-side deactivation does on our side is stated as the number it is, not as a slogan.** The
  platform revokes **its own** sessions immediately (G6). It has **no back-channel from the customer's
  directory**, so a user deactivated at the IdP keeps a live console session until that session expires. That
  window SHALL be stated — it is the session TTL — and every surface describing revocation SHALL distinguish
  **platform-side revocation** (immediate, at the next request) from **IdP-side deactivation** (bounded by the
  TTL). A claim a customer's security team can disprove in one afternoon is worse than the true, smaller one.

### Non-goals (explicitly deferred, with the owner)

- **A password database or a home-grown IdP.** P22 federates against the customer's IdP and verifies second
  factors; it does **not** store passwords, run a credential-recovery flow, or become an identity provider.
  (Owner: nobody — this is out of scope by principle; a customer with no IdP uses the `configured` seam.)
- **Entitlement, plan, and billing changes.** Identity proves *who*; **what they may spend** is P7
  (`internal/account`, plans-by-name, no-price-in-git) and P21 (payments). P22 does not read or write
  entitlement, and a session carries only the tenant, never a plan. (Owner: P7 / P21.)
- **The customer's *transformed program* identity.** How the optimized program authenticates to *its own*
  providers is out of scope: the customer's program calls its own providers directly
  ([ADR-002](../adr/ADR-002-provider-gateway-serves-platform-callers.md)), and P22 is platform-internal
  console/operator identity only. (Owner: ADR-002 boundary.)
- **SCIM / directory provisioning and per-seat lifecycle.** JIT provisioning under an allow rule is in scope;
  a full SCIM push/pull sync of the customer's directory is a later enterprise phase, not M16. (Owner: a future
  identity-provisioning phase.)
- **A *user* model with per-user audit attribution.** ADR-008 states the session holds a tenant, not a user,
  because the platform could not prove one; P22 makes a *subject* provable, but the decision to promote that
  into a first-class `user` with per-user revocation and audit is a scoped follow-up, not assumed here (ADR-008
  Consequences). P22 records the subject on the session where it is provable and refuses to invent it where it
  is not.

## 4. Users & personas

- **The enterprise customer's IT / security administrator** — will not put staff on the console until it
  federates against their own IdP (Okta / Entra ID / Ping / Google Workspace) over OIDC or SAML, with no new
  password store on our side and per-seat revocation at *their* IdP taking effect on our side. Judges the
  delivery by whether it survives their security questionnaire.
- **The customer end user (console)** — signs in by being redirected to their company IdP and back; never sees
  a platform password, never holds a platform credential in the browser. Wants "sign in with my company
  account" and a session that ends when their IT says it ends.
- **The self-hosting / small-team customer (open core)** — has no enterprise IdP; keeps the `configured` seam
  (deployment-injected assertion→tenant map) or points OIDC at a lightweight self-hosted IdP. Must not be
  forced to stand up SAML to run the console.
- **The platform operator (us)** — signs in to the P8 operator console through a real admin IdP with a verified
  second factor; is a cross-tenant principal that can halt the fleet, so the highest-blast-radius surface.
  Needs SSO + MFA to be an invariant, not a configuration claim, and needs offboarding to kill live sessions.
- **Downstream: the P9 console session layer and the P8 operator layer** — consume the seam. Everything above
  it (sessions, cookies, revocation, scope, fail-closed routing, RBAC) is written against an abstract principal
  and must remain so.

## 5. User stories / jobs-to-be-done

**Enterprise IT / security administrator**
- As an IT admin, I want the console to **federate against our OIDC/SAML IdP**, so that my staff use our
  existing accounts and MFA and I never manage a second password store.
- As a security admin, I want **per-tenant IdP registration and a verified-domain mapping**, so that a user
  from `@acme.com` lands in the Acme tenant and can never be resolved into another tenant.
- As a security admin, I want to know **exactly what happens on your side when I disable a user in Okta**, so
  that my offboarding runbook is right rather than optimistic. *(The true answer, and the one the product must
  give: they cannot start a new session — the next sign-in fails at the IdP — and any session they already
  hold ends when it expires, within the console session TTL. There is no back-channel from the directory, so
  "at their next request" would be false; G11, FR27. A push-based revocation is the SCIM / event-hook
  follow-up, and it is not claimed.)*

**Customer end user**
- As a console user, I want to **click "sign in with my company account"** and be redirected to my IdP and
  back, so that I never type a platform password and never hold a platform credential.
- As a console user, I want to be **redirected to sign-in rather than shown a broken page** when my session
  ends, so that the product tells me I am signed out instead of looking broken.

**Self-hosting / open-core customer**
- As a self-hoster with no enterprise IdP, I want to **keep the configured seam or point OIDC at my own
  lightweight IdP**, so that I am not forced into SAML to run the console.

**Platform operator (us)**
- As an operator, I want to **sign in through a real admin IdP with a WebAuthn factor the platform verifies**,
  so that "operator access requires MFA" is an invariant the platform asserts, not a claim I trust the IdP to
  keep.
- As a Superadmin, I want **disabling an operator to revoke their live sessions**, so that offboarding a
  cross-tenant principal takes effect in minutes, not at the next token refresh.

**Downstream session / operator layers**
- As the session layer, I want the **seam to hand me exactly `{ tenantId }` and nothing about how it was
  proved**, so that I remain mechanism-agnostic and unchanged across OIDC, SAML, and configured.

## 6. Functional requirements

These map 1:1 to OpenSpec requirements in `p22-sso` across two capabilities: `sso-identity` (the customer
OIDC/SAML seam) and `operator-sso-mfa` (the P8 operator surface).

### Customer SSO — the seam and its flows (`sso-identity`)
- **FR1** The console SHALL authenticate through the unchanged `verify(assertion) → { tenantId }` seam; P22
  SHALL replace the seam implementation and add redirect/callback routes only, and SHALL NOT modify the session
  store, cookie, revocation, scope derivation, fail-closed middleware, or any tenant page. A change above the
  seam is non-conformant.
- **FR2** The console SHALL support **OIDC Authorization Code flow with PKCE** as the primary mechanism, using
  `state`, `nonce`, and a discovery/JWKS-validated ID token; the **implicit** flow SHALL NOT be used, and a
  token SHALL NEVER be placed in a URL or fragment.
- **FR3** The console SHALL support **SAML 2.0** (SP-initiated, signed assertions, audience restriction, an
  allowlisted Assertion Consumer Service URL) as the enterprise alternative behind the same seam; both
  mechanisms SHALL resolve to exactly one `tenantId`.
- **FR4** The **assertion (OIDC ID token / SAML assertion) SHALL never be persisted** — not stored in the
  session record, not written to a cookie, not logged, not carried upstream; it is verified, exchanged for a
  session, and dropped. The BFF's own server-held platform credential authorizes upstream calls.
- **FR5** The **tenant SHALL be authoritative and server-side**: a tenant identifier arriving from the client
  in any path, query, body, header, or a returned `state` SHALL NEVER widen, change, or override the session's
  tenant.

### Multi-tenant IdP mapping (`sso-identity`)
- **FR6** An SSO identity SHALL map to a tenant by a **configured strategy** — verified-email-domain → tenant,
  per-tenant IdP registration (issuer/entityID → tenant), or just-in-time provisioning under an explicit allow
  rule — and the mapping SHALL be **configuration** injected like `CONSOLE_TENANT_ASSERTIONS` / the `Secrets`
  seam, **changeable without a deploy**. A hardcoded per-tenant branch is non-conformant.
- **FR7** Just-in-time provisioning SHALL occur **only** under an explicit configured allow rule (e.g. a
  verified domain claimed by a tenant); an identity matching no rule SHALL be **refused**, not auto-created, and
  the refusal SHALL be a security event (mirroring `adminidentity`'s "IdP asserted somebody we have never heard
  of is a security event, not a signup").
- **FR8** A **domain or claim the IdP asserts SHALL be verified**, not trusted: email-domain mapping SHALL use a
  domain the tenant has proven ownership of via configuration, so a self-asserted `email` from an unrelated IdP
  cannot claim another tenant's domain.

### Session, revocation, fail-closed (`sso-identity`)
- **FR9** Sessions SHALL retain the ADR-008 model unchanged: server-side, `{ id, tenantId, issuedAt,
  expiresAt, revokedAt }`, bounded TTL, an opaque browser token that is not the session id, and **revocation
  effective at the next request with no grace period** (the store is read on every request).
- **FR10** A **refresh** SHALL re-establish the session by re-verifying (a fresh authorization or a validated
  refresh token exchange), and SHALL NOT silently extend a session past a bound the deployment can configure; a
  self-contained token whose own expiry claim cannot be revoked SHALL NOT be the session.
- **FR11** The identity flow SHALL be **fail-closed**: when the IdP is unreachable (OIDC discovery/JWKS or SAML
  metadata cannot be fetched/validated) sign-in SHALL fail and no session SHALL be issued; the surface SHALL
  NEVER fail-open, and SHALL NEVER issue a session from a cached credential when the IdP cannot be reached.
- **FR12** Login, logout, and callback SHALL be routes that **redirect** an unauthenticated request to sign-in
  rather than render a shell; logout SHALL revoke the server-side session (and, where the IdP supports it,
  initiate single-logout) so the next request is denied.

### Identity security posture (`sso-identity`, NFR-class)
- **FR13** **No identity secret** — OIDC client secret, SAML SP signing/decryption private key, session/signing
  key — SHALL appear in git, a manifest, an env-example, a client bundle, a log line, or a trace attribute;
  each SHALL be resolved through the `Secrets` seam (`HEROS_SECRETS_SOURCE`), with an ambient identity where the
  store supports it and **no bootstrap secret** in the manifest.
- **FR14** The callback SHALL enforce **CSRF and replay defenses**: a single-use `state` bound to the browser,
  a `nonce` bound to the ID token, PKCE binding the code to the client, and an assertion **freshness window**
  and **one-time** guard so a captured assertion is a bounded replay window, not a permanent credential.
- **FR15** The redirect-URI / SAML ACS SHALL be an **allowlist**: a callback target not on the allowlist SHALL
  be refused, so the flow cannot be turned into an open redirect. A wildcard or reflected redirect target is
  non-conformant.
- **FR16** The identity provider SHALL be an **aggregated `/readyz` component**: `/readyz` SHALL report
  `identity_provider: {kind, issuer, reachable}` and report **not ready**, naming `identity_provider`, when the
  IdP is unreachable — the same reachability-not-traffic signal P19 requires.

### Operator SSO + MFA (`operator-sso-mfa`)
- **FR17** The operator console SHALL authenticate through the existing `adminidentity.IdentityProvider` seam,
  and P22 SHALL provide a **real, pluggable OIDC/SAML admin IdP** implementation behind it (replacing the
  fixture `TestMode` HMAC issuer for production), keeping `Verify`/`Describe` and the enum-named provider kind.
- **FR18** Every operator authentication SHALL require **SSO and a verified second factor**; a valid SSO
  assertion alone SHALL issue **no** session (the existing `ErrMFARequired` denial), and the second factor SHALL
  be **platform-verified** (WebAuthn preferred, TOTP fallback) rather than only an IdP claim the platform
  refuses on absence.
- **FR19** The operator identity domain SHALL remain **disjoint** from the customer domain: a different origin,
  a disjoint cookie jar, a principal type that carries no tenant_id, and no code path that promotes a customer
  `auth.Principal` — whatever its role string — into an admin session (the existing structural guard).
- **FR20** Disabling an operator principal SHALL make **live sessions revocable explicitly** (offboarding
  revokes all sessions for that principal), and a disabled principal SHALL obtain no session even with a valid
  SSO assertion and a verified factor (the existing `StatusDisabled` deny).
- **FR21** All operator identity secrets (assertion-verification / factor-verification / session-signing keys)
  SHALL be sourced from the secrets manager under reserved logical names, fail **closed** when unavailable, and
  the live admin IdP SHALL be reported on `/readyz` (`admin_idp`) — never a key, never a secret id.

### Real-IdP integration — Okta first (`sso-identity` + `operator-sso-mfa`)

> These eight requirements are what "we federate with Okta" means beyond "we implement OIDC and SAML". Every
> one of them is a thing a real provider does that a fixture does not, and each is written so that satisfying
> it for Okta satisfies it for Entra or Ping without a branch (G10).

- **FR22** An issuer registration SHALL name the authorization server **exactly as the provider will assert
  it**, and issuer comparison SHALL be **exact string equality** — never a prefix, suffix, or "contains"
  match. Okta asserts a different `iss` for its **org** authorization server (`https://<org>.okta.com`) than
  for a **custom** one (`https://<org>.okta.com/oauth2/<id>`, including the built-in `default`), and an Okta
  **custom domain** changes the host again. The registration SHALL be validated at load (absolute `https`, no
  trailing slash), and a token whose issuer is well-formed but **not registered** SHALL produce a *named
  configuration diagnosis* for the operator — distinguishable from "an identity we do not recognise" — while
  the end user gets the same single generic refusal.
- **FR23** **Signing-key rollover SHALL be normal operation, not an incident.** The verifier SHALL select the
  key by `kid` from the provider's key set, which legitimately carries several keys and rotates on the
  provider's schedule. On an unknown `kid` the verifier MAY refresh the key set **at most once per bounded
  interval** and SHALL then refuse; a `kid` is attacker-controlled input, so it SHALL NOT be able to trigger a
  fetch per request. A rotation SHALL NOT invalidate sessions already issued — an assertion is verified once,
  at sign-in.
- **FR24** **The platform SHALL NOT spend the customer's IdP rate budget.** Discovery, key-set and metadata
  fetches — including the `/readyz` reachability probe — SHALL be cached with a floor and bounded in
  frequency. A provider's per-endpoint request limits are an **org-wide** resource shared with every other
  application that customer runs on it; an unbounded probe or an unbounded refetch degrades systems that are
  not ours, which we do not get to trade away.
- **FR25** The **SAML shape a real IdP actually emits** SHALL be supported: a signature over the **assertion**
  (response-level signing optional, which is Okta's default), an `AudienceRestriction` equal to our SP entity
  id, a `Recipient`/destination on the **ACS allowlist**, and RSA with SHA-256 or stronger — SHA-1 stays out
  of the allowlist. The registration SHALL accept **more than one currently-valid signing certificate** (or
  resolve the set from IdP metadata), because a certificate rollover with room for exactly one certificate is
  a scheduled outage; a certificate outside its validity window SHALL be refused.
- **FR26** Domain-strategy mapping SHALL require the identity's email to be **asserted as verified** *in
  addition to* the issuer being registered. The registration remains the trust anchor (FR8) — a verified-email
  claim from an IdP we do not federate with is worth nothing — and the claim is defence in depth: an
  unverified email SHALL NOT resolve a tenant by domain.
- **FR27** **IdP-side deactivation SHALL be described by its true effect** (G11): a deactivated user starts
  **no new session** (their next sign-in fails at the IdP), and any session they already hold ends when it
  **expires**, bounded by the configurable console session TTL. No surface — UI, docs, sales copy, runbook —
  SHALL imply a directory back-channel the platform does not have. The **operator** domain keeps its explicit
  path: disabling the principal revokes their live sessions immediately (FR20).
- **FR28** Operator MFA SHALL be proven against a **real** provider: an ID token minted by the live admin org
  carrying an `amr` value that claims multi-factor authentication SHALL still yield **no** session without a
  **platform-verified** factor. The distinction between "the IdP says it verified" and "we verified" is only
  convincingly demonstrated when the IdP saying it is somebody else's system.
- **FR29** A **registration recipe** SHALL exist, per mechanism and per domain, stating what the customer's
  IdP administrator creates (application type, grant, redirect URIs / ACS and audience, the scopes and claims
  we need, the assignment), what they hand back (issuer, client id, metadata or certificate), and what we hand
  them (redirect URI, ACS URL, entity id). It SHALL contain **no secret**, and every value in it SHALL have an
  owner and exactly one place it goes — a recipe that leaves the reader guessing which of Okta's several
  issuer forms to paste is the failure FR22 exists to catch, moved one step earlier.

## 7. Non-functional requirements

- **NFR1 — The seam is the only change (regression-proof).** A test asserts the session store, cookie flags,
  revocation semantics, scope derivation, fail-closed middleware and tenant-page render states are unchanged by
  P22; a diff touching a file above the seam other than to call it is a review failure. (ADR-008 Rule 3.)
- **NFR2 — Assertion never persisted.** No code path writes the ID token / SAML assertion to the session, a
  cookie, a log, a trace, or an upstream call; asserted by a test that greps the session record and telemetry
  for assertion material. (ADR-008 Rule 1.)
- **NFR3 — Tenant authoritative, client cannot widen.** A forged tenant in path/query/body/header/`state`
  never changes the resolved tenant; asserted adversarially, not assumed. (ADR-008 Rule 2, the standing lesson.)
- **NFR4 — No identity secret on any path.** Client secret, SAML private key, session key never in git /
  manifest / env-example / bundle / log / trace; enforced by the existing bundle/gitleaks scans plus an
  apply-time lint. A committed identity secret fails CI.
- **NFR5 — Fail-closed, no fail-open, no silent fallback.** IdP unreachable ⇒ no login; no cached-credential
  login; no fallback from the configured mechanism to a weaker one; `/readyz` names the degraded identity
  component. The fail-closed signal measures **reachability**, not traffic, and does not depend on the traffic
  it gates. (禁止静默回落, L1.)
- **NFR6 — Revocation is immediate, no grace.** A revoked or IdP-disabled session is denied at the **next
  request** because the store is read on every request; there is no "was valid a moment ago" cache. The window a
  compromised session outlives its revocation is zero. (ADR-008 / `adminidentity`.)
- **NFR7 — Replay/CSRF/open-redirect are closed by construction.** Single-use browser-bound `state`, `nonce`,
  PKCE, assertion freshness + one-time guard, and a redirect/ACS allowlist; each has a test that goes red when
  the defense is removed.
- **NFR8 — Operator MFA is an invariant, not a claim.** The operator surface issues no session without a
  platform-verified second factor; a misconfigured IdP MFA policy still results in **denial** on the fleet-
  halting surface — a mistake fails in the safe direction.
- **NFR9 — Multi-tenant mapping cannot cross tenants.** A verified-domain or per-tenant-IdP mapping resolves to
  exactly one tenant; an identity matching no rule is refused; JIT never auto-creates across a tenant boundary.
  Cross-tenant resolution is the single most serious failure and is tested adversarially.
- **NFR10 — Interface floor is not lowered for internal users.** The operator sign-in and MFA enrollment
  surfaces meet WCAG 2.1 AA (keyboard, 200% zoom, focus) — an operator authenticating during an incident is the
  normal case (P8 Decision 12 corollary).
- **NFR11 — A fixture can prove a refusal; only a real provider can prove an acceptance.** Every claim about
  federating SHALL name the IdP it was observed against, and a result obtained against the repository's own
  signing IdP SHALL NOT be recorded in words that let a reader take it for a real one. They are different
  claims about different systems, and the gap between them is where the afternoon-long bugs live.
- **NFR12 — Naming a provider SHALL NOT create a provider branch.** Supporting Okta adds **configuration and
  documentation**; it adds no conditional in a verifier, no provider enum, no per-provider claim mapping. This
  is checkable rather than aspirational: a provider brand name appearing in verifier *logic* — as opposed to
  in a comment, a fixture or a document — is a review failure, because the second branch always follows the
  first (八级法则 **可扩展**).
- **NFR13 — Calls into somebody else's identity system are bounded, always.** Discovery, key-set, metadata and
  readiness probes are cached with a floor and rate-bounded (FR23, FR24). The failure this prevents is not
  ours to absorb: exhausting an org's request budget degrades every other application that customer runs on
  it, and "we were only checking health" is not a defence their IT organisation has to accept.
- **NFR14 — The offboarding window is a published number, not a posture.** The console session TTL is the
  window in which a session survives an IdP-side deactivation (G11, FR27); it is configurable, its default is
  documented, and every surface that discusses revocation states **which** of the two revocations it means.

## 8. System design summary

P22 is **one seam replacement on each of two disjoint identity domains**, plus the routes each mechanism needs.
It adds no session model, no scope model, no page. The customer seam resolves an OIDC/SAML assertion to a
`tenantId` through a configured tenant map; the operator seam resolves an admin IdP assertion + a verified
factor to an admin principal. The two domains never share an origin, a cookie jar, or a principal type.

```
   ┌───────────────────────── customer identity domain (P9 origin) ─────────────────────────┐
   │  browser ─▶ /auth/login ─▶ IdP (OIDC Auth-Code+PKCE / SAML 2.0) ─▶ /auth/callback (ACS)  │
   │                                    │  state · nonce · PKCE · redirect allowlist          │
   │                                    ▼                                                     │
   │              verify(assertion) ──▶ { tenantId }        ◀── ADR-008 seam, UNCHANGED above │
   │                    │  (OIDC/SAML impl replaces `configured`; map is CONFIG, not code)    │
   │                    ▼                                                                      │
   │        session store { id, tenantId, issuedAt, expiresAt, revokedAt }  (untouched)       │
   │        every upstream call scoped by session.tenantId — never client input               │
   └──────────────────────────────────────────────────────────────────────────────────────────┘
                    ▲ assertion never persisted · tenant authoritative server-side

   ┌───────────────────────── operator identity domain (P8 origin, disjoint) ────────────────┐
   │  browser ─▶ admin IdP (real OIDC/SAML) + platform-verified factor (WebAuthn/TOTP)        │
   │                    │                                                                     │
   │   adminidentity.IdentityProvider.Verify(assertion) ─▶ Claims  (seam already exists)      │
   │   SSO ✓ AND MFA ✓  ─▶ admin principal (no tenant_id) ─▶ short-TTL revocable session      │
   │   secrets: SSO/MFA/session keys via Secrets seam · disable ⇒ revoke all sessions         │
   └──────────────────────────────────────────────────────────────────────────────────────────┘
                    ▲ /readyz aggregates identity_provider (customer) + admin_idp (operator)
```

Thirteen decisions carry the design; each is recorded in
[`../../openspec/changes/p22-sso/design.md`](../../openspec/changes/p22-sso/design.md) with the alternative
that lost and the level of the **八级法则** (安全 > 稳定 > UX > 运维 > 可演进 > 可扩展 > 维护 > 实现) at which
it lost.

- **D1 — The seam is the only thing that changes** (**L1 安全 / L5 可演进**). ADR-008 built everything above
  the seam mechanism-agnostic; P22 replaces `verify` and adds routes, and asserts by test that nothing above
  moved. Rejected: making the session/scope layer "OIDC-aware".
- **D2 — OIDC (Auth Code + PKCE) primary, SAML 2.0 enterprise alternative, one seam** (tie at **L1**, decided
  at **L5/L4**). Both federate safely, so L1 does not separate them; OIDC is the lower operational burden for
  most customers and SAML is the enterprise procurement reality, so both ship behind one seam. Rejected:
  SAML-first (operational weight on everyone); OIDC-only (loses the enterprise buyer).
- **D3 — Tenant mapping is configuration, not code** (**L5 可演进 / L4 运维**). Domain/per-tenant-IdP/JIT are a
  config the `Secrets`/config source injects, changeable without a deploy — the `configured` posture extended.
  Rejected: a hardcoded per-tenant branch (a deploy per customer).
- **D4 — Assertion dropped; tenant server-side; client never widens** (**L1 安全**). ADR-008 Rules 1 & 2 made
  normative for the real mechanism. Rejected: putting any tenant hint from the client on the trust path.
- **D5 — Operator IdP is real and pluggable behind the existing seam; SSO + platform-verified MFA; disjoint
  domain** (**L1 安全**). Fill the seam `adminidentity` already documents; verify the second factor rather than
  trust the IdP's claim. Rejected: reusing customer OIDC for operators (a cross-tenant principal on a single-
  tenant path — ADR-008); trusting the IdP's MFA policy as sufficient.
- **D6 — Every identity secret through the `Secrets` seam, none in git** (**L1 安全**). One answer to "where do
  secrets come from at the moment of use", reused from `secrets-baseline.md` and `adminidentity`. Rejected: a
  client secret in an env-example or a config field; a fourth secret mechanism for identity.
- **D7 — Session & revocation model unchanged; revocation immediate, no grace** (**L1 / L2 稳定**). Server-side
  session read on every request; refresh re-verifies. Rejected: a self-contained JWT session (unrevocable); a
  refresh that extends indefinitely.
- **D8 — Fail closed when the IdP is unreachable; reachability on `/readyz`** (**L1 安全 / L2 稳定**). No login
  over a dead IdP, never fail-open, no cached-credential login; `/readyz` names `identity_provider`. Rejected:
  a fail-open "allow if IdP down"; a cached-credential fallback.
- **D9 — Replay/CSRF/open-redirect closed first-class** (**L1 安全**). Single-use browser-bound `state`,
  `nonce`, PKCE, freshness + one-time assertion guard, redirect/ACS allowlist. Rejected: the implicit flow; a
  reflected/wildcard redirect target.
- **D10 — Okta is the named reference provider; the mechanism stays generic** (**L6 可扩展 / commercial**).
  One real org is run end to end on both domains, and everything Okta-shaped lives in configuration and a
  registration recipe (G10, NFR12). Rejected: an *Okta adapter* — the first branch that makes the second
  provider a second branch; and staying provider-neutral indefinitely — a buyer's security team cannot verify
  a standard, only a deployment.
- **D11 — The issuer registration is the exact `iss` string; no normalisation, no suffix matching** (**L1
  安全**). Okta alone spells the issuer three ways (org AS, custom AS, custom domain), so the registration
  records what the token will actually say and comparison is equality (FR22). Rejected: host- or suffix-based
  matching, which is one `okta.com.attacker.example` away from trusting a stranger; and "helpfully" stripping
  or adding a path segment, which makes the trusted set unreviewable.
- **D12 — Signing material is a *set* with a rotation window, not a value; refresh is bounded** (**L2 稳定 /
  L4 运维**). Multiple keys and certificates are the normal state, unknown-`kid` refresh is rate-bounded, and
  probes are cached (FR23, FR24, FR25). Rejected: pinning one key or one certificate (turns a routine rotation
  into an outage); refetching on every unknown `kid` (hands an attacker a rate-limit weapon aimed at the
  *customer's* other applications).
- **D13 — No directory back-channel; the deactivation window is the session TTL, and it is published**
  (**L1 安全, decided against L3 UX**). The platform does not poll the customer's directory and does not
  imply it does (G11, FR27, NFR14). Rejected: **polling the IdP's admin API**, which would require the
  customer to issue us a standing, high-privilege directory credential — a larger permanent risk than the
  window it closes, and one their security review would be right to refuse; and **implying push revocation**
  in copy while shipping TTL-bounded behaviour, which is the same failure with none of the cost paid up
  front. The push version is SCIM / event hooks, sold when built.

## 9. Design by role lens

**System Designer (co-lead) — *change the seam, prove nothing above it moved, and keep the one-way door
deliberate.*** P22's whole architectural content is that ADR-008 already did the hard separation: everything
above `verify(assertion) → { tenantId }` is written against an abstract authenticated tenant principal, so the
identity *mechanism* is a replaceable implementation of one function, not a cross-cutting concern. The
discipline here is refusal — refusing to let "OIDC" or "SAML" leak upward into the session, the scope
derivation, or a page, because the moment one of them does, the seam stops being a seam and the next mechanism
is a migration. The federation contract with the customer's IdP (the issuer set we trust, the claims we map,
the domains we honor) is the published one-way door (🔴 `careful-api-creation`); it is decided here, with the
alternative that lost and the level it lost at, so a future reviewer can tell a considered federation contract
from an accident. The two identity domains are one separation stated twice: the customer seam resolves a
*tenant* (single-tenant scope), the operator seam resolves an *operator* (cross-tenant), and the type system —
not a role flag — keeps them un-confusable, which is the same "the import ban is the proof they could be two
processes" discipline P19 used for the control/data planes.

**Backend Dev (co-lead) — *the verify path is small, so it must be exactly right; the secret and the mapping
are the whole risk surface.*** The credential path is where identity lives or dies. The OIDC/SAML verifier
resolves signing material through the `Secrets` seam at the moment of use, never from `config.Config`, never
written to disk — the same rule `adminidentity.secrets.go` already encodes, reused rather than re-invented,
because "the key that signs OPERATOR SESSIONS quietly comes from an env var while `/readyz` reports the
manager" is exactly the split-truth failure that rule exists to prevent. The verifier **fails closed**: an
unreachable JWKS, an unverifiable signature, a stale or replayed assertion, an unmapped identity — all errors,
no fallback, because a fail-closed signal must measure the right dimension (IdP reachability, assertion
validity) and must never depend on the traffic it gates. The tenant mapping is the other half of the surface:
it resolves an identity to **exactly one** tenant from configuration, refuses an identity matching no rule
(a security event, not a signup), and honors only a domain a tenant has **proven** it owns — because a
self-asserted `email` claim from an unrelated IdP trying to claim another tenant's domain is the cross-tenant
resolution that NFR9 exists to make impossible. Sessions are ADR-008's, unchanged: server-side, read on every
request, revocation immediate — a token that vouches for itself cannot be revoked, and "revocable at the next
refresh" is not revocable.

**Frontend Dev (support) — *the redirect flow is new; the shell rule and the no-key rule are not.*** The
console gains `/auth/login`, `/auth/callback` (and a SAML ACS route) and a real IdP round trip, and it honors
the two frontend laws it already lives under. First, an unauthenticated request **redirects** to sign-in
rather than rendering a shell that then 401s every fetch — the redirect carries a `reason` so "your session
ended" and "sign in" are the different messages they are to a user who was signed in a moment ago; the callback
never leaves the user on a broken page when the IdP round trip fails, it routes to sign-in with a cause.
Second, **no key in the browser, ever**: the BFF holds the OIDC client secret and the platform credential
server-side, the browser holds only the opaque `HttpOnly` session token it cannot read, and the ID token /
SAML assertion is consumed at the BFF and dropped — never handed to client JS, never in a URL fragment (which
is why the implicit flow is rejected outright). Build artifact and runtime config stay separate: the issuer,
the client id, the redirect allowlist and the tenant-mapping strategy arrive as **runtime** injection, because
that separation is what lets one console image federate against Acme's Okta and Beta's Entra without a rebuild.
The bundle scan that already fails a build carrying a credential now also covers the client id/secret surface.

**DevOps Engineer (support) — *identity reachability is a health signal, and its secrets refuse to start.***
Identity is a dependency like any store, so it is on `/readyz`: `identity_provider: {kind, issuer, reachable}`
aggregated the way P19 aggregates every component, reporting not-ready and **naming** it when OIDC discovery/
JWKS or SAML metadata is unreachable — a UI is never the health verdict (🔴 `health-signal-surface`), and the
probe reads the endpoint. Every identity secret comes from the environment or the secret store and **refuses
to start** when unset (`${VAR:?}` / apply-time-required), never reaching git, a manifest, a log, a trace, or a
bundle — the same posture the console bundle scan and gitleaks already enforce, extended to the client secret
and the SAML SP private key. The secret store authenticates **ambiently** where it can (IRSA / workload
identity) so wiring identity creates **no bootstrap secret** — a manifest with a client secret in it is a
plaintext credential in git the moment it lands, an irreversible one-way door. And offboarding is an
operational verb that must actually take effect: disabling an operator revokes their live sessions now, not at
the next refresh, and that is asserted, because "offboarding silently left a live cross-tenant session
running" is the failure the operator module exists to make impossible.

**Product Designer (support) — *the interaction the user should not have is the password; define every term
and be honest about what "sign in" means.*** The signature simplicity win is that a federated user has **no
platform password to create, remember, or reset** — the interaction we remove is the whole point, and the
sign-in surface says "continue with your company account", not a form that invites a credential we then have to
refuse to store. The unhappy paths are designed, not defaulted: "your session ended" versus "sign in" are
different messages; "your IdP could not be reached, try again" is not "wrong credentials"; "your account is
not provisioned for this tenant" (JIT refused) is a clear, non-leaking message that does not tell an attacker
which half they got wrong. The naming is single-sourced: "tenant", "identity provider", "session", "operator"
map to one glossary so a wording change propagates rather than forks, and no operator-facing message leaks an
internal mechanism (a secret logical name, a provider kind literal, an issuer allowlist). The rule that a
content change affecting a user path is written into the spec even if unasked (the double-color-dot alignment)
applies to every one of these messages.

**QA Engineer (support) — *a green sign-in is not the acceptance; the seam invariants and the adversarial
paths are.*** Acceptance is behavioral and adversarial. The seam invariant is a test, not a hope: the session
store, cookie flags, revocation, scope derivation and fail-closed middleware are asserted **unchanged** by P22
(NFR1) — "sign-in works" says nothing about "we didn't couple the session to OIDC". The three iron invariants
each get a red-able fence: the assertion never appears in the session/log/trace (grep the record and telemetry
— it must be able to go red); a forged tenant in every client-controlled position never widens scope (the
adversarial NFR3 case); and cross-tenant resolution is impossible — a self-asserted domain from a foreign IdP
is refused, JIT never crosses a boundary (NFR9, the single most serious failure, tested first). The security
mechanisms are tested by **removing** them: delete the `state` check and replay a callback → the test goes red;
strip PKCE, reuse a code → red; point the redirect off-allowlist → refused; present a stale assertion → refused;
take the IdP down → **no session issued** and `/readyz` names it (fail-closed, not fail-open). Operator MFA is
proven by the denial path: valid SSO, no verified factor ⇒ **no session** (NFR8). And the customer and operator
domains are exercised as the two forms they are — a capability that works in only one is, by rule, a bug — with
a cross-origin test asserting the operator surface is unreachable from a customer session.

**Sales Operations (support) — *sell "federates with your IdP", not "we store your passwords"; keep the boundary
honest.*** The commitment discipline is exact: what P22 delivers is **SSO federation** (OIDC + SAML) against the
customer's own IdP and a **verified operator MFA factor** — that is committable because it is built. What is
**not** committable and must not be sold as present: SCIM directory sync, a full user-lifecycle/per-seat audit
model, and anything about the *transformed program's* identity (ADR-002 — the customer's program calls its own
providers; our identity is console/operator only). The honest boundary a customer's security team will probe:
we **do not** run a password database or a home-grown IdP — a differentiator, not a gap, and it survives the
technical follow-up because it is true. Per-seat revocation is where the honest claim is **smaller than the
one the market expects, and must be made anyway**: what propagates with no grace is *our* revocation — a
session we revoke is dead at the next request, because the store is read on every request. A user the customer
disables **in Okta** is a different event and it reaches us differently: they can start no new session, and
the one they hold ends when it expires (the console session TTL). There is no directory back-channel, so
"disable in Okta, dead on our side at the next request" is a sentence this product cannot say (G11, FR27).
Saying it anyway would be a promise a security team disproves with one browser tab, and it would cost more
than the gap it papers over — the push-based version is the SCIM / event-hook follow-up, sold when built. No price value and no plan gate lives on the
identity path — identity proves *who*, entitlement (P7) and payments (P21) decide *what they may spend* — so a
sales deck never implies SSO is a paywalled feature by wiring a plan check into the seam.

### 9.1 The real org, by role lens — what changes when the IdP is somebody else's

**System Designer.** The architectural test of P22 was "does the mechanism stay below the seam". The
architectural test of the Okta increment is narrower and harder: **does naming a provider create a provider
branch** (NFR12). Everything Okta needs — three possible issuer spellings, a key set that rotates, an
assertion-level signature, a certificate rollover window — is a *configuration* shape, and the design holds
only if each one is expressed as configuration the next provider reuses. The one place a brand may legitimately
appear is the registration recipe, which is documentation.

**Backend.** Three behaviours move from "correct in principle" to "exercised": key selection by `kid` across a
rotating set, bounded refresh on an unknown `kid`, and multi-certificate SAML validation. The second one is the
security-relevant one and it is counter-intuitive — the naive implementation (refetch whenever we see a `kid`
we do not know) is both the obvious fix and a rate-limit weapon pointed at the customer's other applications
(D12, FR24).

**DevOps.** Identity gains the same artefact discipline P21's live account has: a registered application, a
client secret in the Secrets seam, a redirect URI and ACS that must match on both sides exactly, and a
readiness probe that is now hitting **someone else's rate budget** and therefore must be cached with a floor.
The reachability signal stays honest — it reads the endpoint, not a UI — but it reads it less often.

**Product Designer + Sales Operations.** The whole increment turns one sentence from a standard into a
verifiable fact: *"we federate with your Okta"*. What comes with it is the discipline of not extending that
sentence one word further than the build — specifically the offboarding claim (G11, FR27), which is the place
where the market's expectation and this product's behaviour differ, and where saying the expected thing costs
a customer's trust the first time they test it.

**QA.** The acceptance shape inverts. Refusals stay proven against the repository's own IdP, because that is
the only IdP that can be *told* to misbehave — a real org will not issue a stale assertion on request.
Acceptances move to the real org, because that is the only thing that proves a real discovery document parses
and a real key set loads. Neither substitutes for the other, and a record that does not say which one it used
is the failure NFR11 names.

## 10. Dependencies

- **Upstream (must exist for P22 to replace the seam):** ADR-008 (the tenant-identity seam and the whole layer
  above it); P9 web console (`web/console` session/identity/middleware); P8 operator console and
  `internal/adminidentity` (the SSO+MFA module P22 makes real); the `providergateway.Secrets` seam +
  `secrets-baseline.md` §1.1 (identity secrets reuse it); ADR-002 (gateway serves platform callers — the
  transformed-program-identity boundary); ADR-004 (fail-static config binding for the tenant map); ADR-006 /
  P19 Decision 5 (operator console is a second origin, disjoint cookie jar); P19 `/readyz` aggregation.
- **P22 unblocks:** the enterprise/federated go-to-market motion (Sales); a first-class **user** model with
  per-user revocation and audit attribution (the follow-up ADR-008 Consequences names); SCIM / directory
  provisioning as a later phase; P21 payments (which assumes a proven identity to attach a payment method to).
- **Deliberately not depended on:** a password store or home-grown IdP (out of scope by principle); P7/P21
  entitlement/billing (identity does not read or write it).

### 10.1 What a real Okta org requires — the artefacts this repository cannot produce

The verifiers are built and green. What separates that from *"a customer signs in with their Okta account"* is
a set of artefacts that live in an identity org, most of which belong to somebody who does not work here.
Listing them is not administrative detail: two of them are credentials this repository must never contain, one
of them is a **mutual** agreement (a URL that must match character-for-character on both sides), and one is a
person. Conflating "the code is right" with "a customer can sign in" is how a phase reports itself finished
while nobody outside the building can log in.

| # | Artefact | Shape | Owner | Why it is not optional |
|---|---|---|---|---|
| **A** | An **OIDC application** in the customer's org | Web application, Authorization Code **+ PKCE**, our redirect URI registered, the users or groups **assigned** | The customer's IdP administrator | Nothing about federation is self-service from our side. An unassigned application authenticates nobody, and the resulting error is at their IdP, not ours — which is why FR29's recipe has to say so. |
| **B** | The application's **client secret** | Into the Secrets seam under its reserved logical name, resolved at the moment of use | DevOps | Never in git, a manifest, an env-example or a bundle (FR13). It is the credential that mints identities; it is also the one people paste into a config file because it arrives in an email. |
| **C** | The **issuer** and **client id**, registered in the tenant map | The `iss` string **exactly as the token will spell it** (org AS / custom AS / custom domain — three different answers) plus the tenant's `verified_domains` | DevOps, from what the customer's admin hands back | This is the trust anchor (FR8, FR22). Registering the wrong one of Okta's three issuer forms produces a refusal rather than a wrong tenant — fail-closed and correct, and completely opaque unless the operator gets the named diagnosis FR22 requires. |
| **D** | For SAML: the **entity id / audience**, the **ACS URL**, and the **signing certificate(s)** | Ours to give (audience, ACS); theirs to give (certificate, or a metadata URL) | Both sides | A mutual agreement, and the one place a typo is silent on our side and loud on theirs. More than one certificate must be accepted, or the customer's next routine rotation is our outage (FR25). |
| **E** | For the operator console: **our own** admin org application | A second, disjoint application in a different org or realm, with operators assigned | Us (DevOps) | The operator domain is deliberately not the customer domain (FR19). Sharing an application would be the cross-tenant principal on a single-tenant path that D5 exists to refuse. Platform-verified MFA remains ours regardless of what that org's policy says (FR28). |
| **F** | **A human who has completed a sign-in** | One round trip, at a real IdP, with real credentials | A person | The read-only probe ([`liveidp_test.go`](../../internal/adminidentity/liveidp_test.go)) proves discovery parses and the key set loads without any credential. It deliberately stops there: completing a sign-in means somebody typing their own password at their own keyboard, which is the boundary this phase exists to respect, not a step to automate around. |

Two properties are worth stating, because both are design outcomes rather than accidents:

- **None of these is code.** A, D and E are acts in an identity console; B is a credential; C is configuration;
  F is a person. If any of them turned out to require a code change, NFR12 has been violated and the next
  provider will require another one.
- **C is the one that will be got wrong**, and it is worth over-serving. Okta spells its issuer three ways
  depending on which authorization server the application uses and whether the org has a custom domain; the
  recipe (FR29) must say which one to paste, and the platform must name the mismatch when it happens rather
  than answering "not provisioned" to an operator who then goes looking in the wrong place.

## 11. Risks & mitigations

| # | Risk | Owner | Mitigation |
|---|---|---|---|
| R1 | The mechanism leaks above the seam and the session/scope layer becomes OIDC-coupled — the migration ADR-008 exists to prevent. | System Designer | D1/FR1/NFR1: replace `verify` + add routes only; a test asserts everything above the seam is unchanged; a diff above the seam is a review failure. |
| R2 | A self-asserted claim resolves an identity into another tenant (cross-tenant escalation). | Backend | D3/FR6–FR8/NFR9: map only to a **proven** domain / registered issuer; refuse unmapped identities; JIT never crosses a boundary; tested adversarially first. |
| R3 | An identity secret (client secret / SAML private key) lands in git or a manifest. | DevOps | D6/FR13/NFR4: `Secrets` seam + ambient identity, no bootstrap secret; gitleaks + bundle scan + apply-time lint; a committed secret fails CI. |
| R4 | The callback is CSRF'd, an assertion replayed, or the redirect turned into an open redirect. | Backend | D9/FR14/FR15/NFR7: single-use browser-bound `state`, `nonce`, PKCE, freshness + one-time guard, redirect/ACS allowlist; each defense has a red-able test. |
| R5 | The IdP goes down and the surface fails open (or a cached credential logs someone in). | Backend + DevOps | D8/FR11/NFR5: fail-closed, no cached-credential login, no silent fallback; `/readyz` names `identity_provider`; the signal measures reachability. |
| R6 | Operator MFA is a claim the IdP might misconfigure, not an invariant the platform holds. | Backend | D5/FR18/NFR8: platform-verified WebAuthn/TOTP; valid SSO + no verified factor ⇒ no session; proven by the denial path. |
| R7 | The operator surface becomes reachable from a customer session (domain confusion). | System Designer + Frontend | D5/FR19: disjoint origin + cookie jar + principal type; no promotion path from `auth.Principal`; a cross-origin unreachability test. |
| R8 | Revocation/offboarding is not immediate (a disabled user/operator keeps a live session). | Backend | FR9/FR20/NFR6: store read on every request, no grace; disable revokes all sessions; asserted, not assumed. **Scope note:** this is *platform-side* revocation. IdP-side deactivation is R12. |
| R9 | The issuer registered is not the issuer the token asserts (Okta's org AS vs custom AS vs custom domain), and sign-in fails with a message that sends the operator looking in the wrong place. | DevOps / Backend | D11/FR22: exact-match registration validated at load, plus a **named configuration diagnosis** for the operator that is distinct from "unknown identity" — same generic refusal to the end user, different sentence in the log. FR29's recipe states which form to paste. |
| R10 | A routine signing-key or certificate rotation at the customer's IdP breaks sign-in, and the outage is ours to explain. | Backend | D12/FR23/FR25: key selection by `kid` over a set, bounded refresh on an unknown one, and a SAML registration that accepts every currently-valid certificate (or resolves them from metadata). Pinning one of anything is the failure. |
| R11 | An attacker sends a stream of unknown `kid`s, we refetch each time, and the customer's **other** applications hit their org's rate limit. | Backend / DevOps | D12/FR24/NFR13: refresh is rate-bounded and probes are cached with a floor. The blast radius here lands on somebody else's systems, which is why it is a security requirement rather than a performance one. |
| R12 | A customer's security team is told a user disabled in Okta loses access on the next request, tests it, and finds the session still alive — the trust cost lands on the whole delivery. | Sales Ops / Product | G11/D13/FR27/NFR14: the true effect is stated everywhere it appears — no new session, existing one bounded by the published TTL — and the push-based version is named as the SCIM / event-hook follow-up rather than implied. |
| R13 | Supporting Okta grows an Okta branch, and the second provider grows a second one. | System Designer | D10/NFR12: Okta contributes configuration and a recipe, not code; a provider brand in verifier *logic* is a review failure. The check is cheap and the drift it prevents is not. |

## 12. Rollout & test strategy

- **Seam-first, mechanism-behind.** Land the OIDC verifier behind the unchanged `verify(assertion) →
  { tenantId }` seam and prove — by the NFR1 unchanged-above-the-seam test and a full session/revocation/scope
  regression — that nothing above moved. Then add SAML 2.0 behind the same seam. The `configured` and `dev`
  implementations stay for the open-core / local paths.
- **Adversarial before happy-path green.** The acceptance gate leads with the failures: cross-tenant resolution
  refused (NFR9), forged-tenant-never-widens (NFR3), assertion-never-persisted grep (NFR2), replayed
  `state`/reused code/off-allowlist redirect/stale assertion all refused (NFR7), IdP-down fails closed and is
  named on `/readyz` (NFR5). A happy-path sign-in that passes while any of these is red is not acceptance.
- **Operator MFA proven by denial.** Valid SSO + no verified factor ⇒ no session (NFR8); a disabled operator
  obtains no session and their live sessions are revoked on disable (FR20); the operator surface is unreachable
  from a customer origin (FR19), asserted by a cross-origin test.
- **Both domains, both mechanisms — the form matrix.** Customer OIDC, customer SAML, customer `configured`
  (open-core), and operator IdP are each exercised; a capability that works in only one form is a bug. Every PR
  carries the identity-form impact matrix with every "not affected" row explaining why.
- **Health and secret assertions are machine checks.** `/readyz` names `identity_provider` and `admin_idp`;
  probes read the endpoint; a stopped IdP turns readiness red and names it. No identity secret in any
  bundle/manifest/log (build scan + apply lint); the client secret / SAML key surface is covered by the same
  scan.
- **Against the real org, the two kinds of test do not swap places.** Refusals stay against the repository's
  own signing IdP — it is the only one that can be *instructed* to send a stale assertion, a wrapped
  signature, or a token signed with the wrong key, and every one of those tests must remain able to go red.
  Acceptances move to the real org, because only a real provider proves that its discovery document parses,
  its key set loads, and its assertion shape verifies (NFR11). A record that does not say which one produced
  a given green is not a record.
- **The probe first, the sign-in second, and the sign-in is a person.** The read-only probe
  (`PROBE_ISSUER` / `PROBE_CLIENT_ID` → [`liveidp_test.go`](../../internal/adminidentity/liveidp_test.go))
  answers the cheap questions with no credential: does discovery parse, does the key set load, is the
  authorization URL we build the one we intended (code flow, S256, no implicit response type). It is free in
  CI because it skips when unpointed. Completing a sign-in comes after, and it is a human at their own
  keyboard (§10.1 artefact F) — the same boundary P21 draws around typing a card number.
- **Rotation and rate posture are exercised, not assumed.** A key set carrying more than one key selects
  correctly by `kid`; an unknown `kid` refreshes **once** within the bound and then refuses, asserted by
  counting fetches rather than by reading the code; a SAML registration holding two valid certificates accepts
  an assertion signed by either, and refuses one signed by a certificate outside its validity window.
- **The offboarding claim is tested as stated, including the part that is a limit.** A user deactivated at the
  IdP cannot start a new session (the failure comes from the IdP, and the console renders it as its own
  message, not "wrong credentials"); a session already held survives until expiry, and that is asserted so the
  published TTL (NFR14) stays the true number rather than a hopeful one.

## 13. Success metrics & acceptance criteria (M16 exit checklist)

**Status: green (2026-07-31), against a signing IdP this repository runs.** Every box below names where
it is proven. A box is checked only when a test would go red if the property stopped holding — the four
load-bearing fences (NFR1, NFR2, NFR3, NFR9) were each deliberately broken and observed red before being
restored.

> **What this checklist claims, precisely.** The mechanisms are correct and the refusals are real, proven
> against an IdP that can be *instructed* to misbehave — which is the only way to prove a refusal, and is
> why this is the right shape for these boxes. It does **not** claim that a real provider has been
> federated with: no Okta org has issued a token this platform accepted (NFR11). That is §13.1, and it is
> not green.

- [x] The customer console signs in via **OIDC Authorization Code + PKCE** (state + nonce, JWKS-validated) and
      via **SAML 2.0** (signed assertion, allowlisted ACS, audience restriction), both resolving to exactly one
      `tenantId` through the **unchanged** `verify(assertion) → { tenantId }` seam.
      *(`tests/sso-identity.test.mjs`, both flows end-to-end over real sockets against a signing IdP; both also
      driven in a real Chrome via `npm run dev:sso`.)*
- [x] A test asserts the **session store, cookie flags, revocation, scope derivation, fail-closed middleware and
      tenant pages are unchanged** by P22 (ADR-008 Rule 3); the only changed files are the seam and the added
      routes. *(Pinned sha256 of `session.ts` / `middleware.ts` / `scope.ts` / `entitlements.ts`, the session
      cookie's own declarations by value, plus a rule that no mechanism word may appear above the seam.)*
- [x] The **assertion is never persisted** — a grep of the session record and telemetry for ID-token / SAML
      material is empty; a **forged tenant** in path/query/body/header/`state` never widens scope.
      *(Structural: `logIdentity` has no field that could carry an assertion and `Session` has none that could
      store one. The NFR3 half asserts the tenant on the header the stub platform ACTUALLY received.)*
- [x] The **tenant→IdP mapping is configuration** (domain / per-tenant IdP / JIT-under-allow-rule), changeable
      **without a deploy**; an identity matching no rule is **refused** as a security event; mapping honors only
      a **proven** domain, and no identity resolves across a tenant boundary. *(`CONSOLE_IDP_TENANT_MAP`, with
      `verified_domains` nested under the issuer registration — the test deployment registers two issuers so
      the cross-tenant case is one the map could express if the code let it.)*
- [x] **No identity secret** appears in git / manifest / env-example / bundle / log / trace; client secret,
      SAML SP private key and session keys resolve through the `Secrets` seam with **no bootstrap secret**; the
      secret scan is green. *(`scan-bundle.mjs`, `check-no-plaintext-secrets.sh` — which also refuses a
      **bootstrap secret for the store itself** — and two new gitleaks rules. Each new lint leg was proven able
      to go red before being claimed.)*
- [x] The callback enforces **single-use browser-bound `state`, `nonce`, PKCE, assertion freshness + one-time
      guard, and a redirect/ACS allowlist**; replay, CSRF, reused code, off-allowlist redirect and stale
      assertion are each **refused**, each with a red-able test. *(Nine named tests, each mutating a genuinely
      valid message rather than hand-writing an invalid one.)*
- [x] **IdP unreachable ⇒ no session** (fail-closed, no cached-credential login, no silent fallback); `/readyz`
      reports **not ready** and **names** `identity_provider`, reporting `{kind, issuer, reachable}`.
      *(This test found a real defect: a five-minute metadata cache made a dead IdP look serviceable at the
      login hop, so the user was redirected onto a dead host. Fixed with `ensureReachable`/`ensureMetadata`.)*
- [x] The **operator console** authenticates through a **real, pluggable OIDC/SAML admin IdP** behind the
      existing `adminidentity.IdentityProvider` seam, requires **SSO + a platform-verified factor** (valid SSO
      alone ⇒ no session), stays **disjoint** from the customer domain (cross-origin unreachability asserted),
      and **disable revokes live sessions**; `admin_idp` is on `/readyz`. *(`internal/adminidentity/p22_test.go`
      mints an ID token claiming `amr:["mfa"]` and asserts NO session is issued — the platform's verification,
      not the IdP's claim, is the invariant.)*
- [x] The **identity-form impact matrix** (customer OIDC / customer SAML / customer configured / operator) is
      attached to every P22 PR, and every form is exercised. *(Matrix in
      [`openspec/changes/p22-sso/tasks.md`](../../openspec/changes/p22-sso/tasks.md) § Verification record; the
      post-sign-in assertion is written once and run against each form so it cannot drift for one of them.)*
- [x] Sign-in messages define every term, distinguish **"session ended" / "sign in" / "IdP unreachable" /
      "not provisioned for this tenant"**, leak **no internal mechanism**, and carry **no price value or plan
      gate** on the identity path. *(`src/content/identity.ts` + `docs/sales/P22-identity-copy.md`; both the
      mechanism-leak and the no-plan-gate fences were verified red.)*

### One recommendation not followed, and why

§14 Q1 recommends *"a well-audited standard library behind the seam interface"* for the OIDC/SAML
verifiers. **The implementation uses the language standard libraries instead** — `node:crypto` and Go's
`crypto/*` do the RSA, ECDSA and SHA-2; what is hand-written is the JWS envelope, the JWKS lookup, and
(for SAML) an XML reader plus exclusive canonicalization.

The reason is not that libraries are unavailable in principle: the console ships four runtime
dependencies behind a build-time bundle fence, and the platform's operator authentication path is the
highest-blast-radius boundary it has. Adding an unread dependency inside either was the larger risk.
What that trade buys has to be paid for in evidence, and it is: the algorithm allowlists are
asymmetric-only so `alg:none` and HMAC confusion have no landing site; signature wrapping is closed
**structurally** (only the element whose digest verified is read); and both canonicalizers are checked
against the W3C exclusive-c14n specification's own worked example, which this repository did not author.

**This is worth revisiting.** If a well-audited OIDC/SAML library is later vendored, it drops in behind
the same seam with no caller change — which is the property Q1 actually cares about — and the tests
above become its conformance suite rather than being replaced.

### 13.1 V3 — the real-Okta exit checklist (**not green**; nothing below has been run against a real org)

> A separate checklist rather than more boxes on §13, for the same reason P21 keeps its live-mode list
> separate: §13 is about the platform and this is about an org. Nothing here is verifiable by a build, and a
> box is checked only when it was observed against the **real** org, with the org named in the record
> (NFR11). The artefacts it depends on are §10.1.

- [ ] The **read-only probe passes against the real org** — discovery parses, the key set loads, `Describe()`
      reports the issuer we registered, and the authorization URL we build is Authorization Code + **S256**
      with no implicit response type. *(`PROBE_ISSUER` / `PROBE_CLIENT_ID` → `liveidp_test.go`; no credential
      used, no code exchanged.)*
- [ ] A **human completes a customer-console OIDC sign-in** against the real org and the console renders as
      the mapped tenant; the assertion appears in **no** session record, log line or trace (the NFR2 fence run
      against a real token, not a minted one).
- [ ] A **human completes a customer-console SAML sign-in** against the real org's SAML application:
      assertion-level signature accepted, audience equal to our entity id, `Recipient` on the ACS allowlist,
      SHA-256 or stronger (FR25).
- [ ] The **issuer registered is the issuer asserted**, and the *wrong* one is diagnosed: a registration
      naming the org authorization server while the application uses a custom one (or vice-versa) produces a
      **named configuration error** for the operator and the same single generic refusal for the user (FR22).
- [ ] **Key rotation is a non-event**: the real key set carrying more than one key selects correctly by `kid`;
      an unknown `kid` triggers **at most one** refresh within the bound — asserted by counting fetches — and
      then refuses (FR23, FR24).
- [ ] **Certificate rollover is a non-event**: the SAML registration holds every currently-valid certificate
      and accepts an assertion signed by either; one signed outside its validity window is refused (FR25).
- [ ] **Domain mapping requires a verified email**: an identity from the registered issuer whose email is not
      asserted as verified does **not** resolve a tenant by domain (FR26).
- [ ] The **operator console signs in through the real admin org**, and an ID token **that org actually
      issued** carrying an MFA `amr` value **still yields no session** without a platform-verified factor
      (FR28) — the invariant demonstrated against somebody else's system.
- [ ] **Rate posture is honest**: readiness probes and metadata fetches against the real org are cached with a
      floor, and a burst of unknown `kid`s does not produce a burst of fetches (FR24, NFR13).
- [ ] **Deactivation behaves exactly as documented**: a user deactivated in the org starts **no new session**
      (rendered as its own message, not "wrong credentials"), and a session they already hold survives until
      expiry — the published TTL being the true window (FR27, NFR14).
- [ ] The **registration recipe is followed by somebody who did not write it**, front to back, for OIDC and
      for SAML, on the customer domain and the operator domain — and every value it names had one owner and
      one destination (FR29). A recipe verified only by its author is a recipe verified against its author's
      assumptions.
- [ ] **No provider branch was added**: the diff for this increment contains configuration, documentation and
      tests, and **no** provider brand in verifier logic (NFR12, D10).

## 14. Open questions

- **Q1 — Which OIDC/SAML library and validation surface is the reference?** The verifier needs JWKS caching,
  clock-skew tolerance, and SAML signature/canonicalization handling — all classic footguns. *Recommendation:
  a well-audited standard library behind the seam interface, with the JWKS/metadata fetch on the fail-closed
  `/readyz` path; the library choice is an implementation detail below the seam, not a contract.*
- **Q2 — Does the session grow a first-class `user`, or stay tenant-only for now?** ADR-008 holds the session
  at `{ tenantId }` because a user was unprovable; P22 makes a *subject* provable. *Recommendation: record the
  subject on the session where provable for audit attribution, but defer promoting `user` into a first-class
  model (per-user revocation, SCIM) to a scoped follow-up — do not invent a user field where JIT/configured
  cannot prove one (ADR-008 Consequences).*
- **Q3 — WebAuthn vs TOTP as the default operator factor, and enrollment flow.** WebAuthn is phishing-resistant
  and preferred; TOTP is the universal fallback. *Recommendation: WebAuthn primary with a Superadmin-gated,
  audited enrollment (mirroring `adminidentity`'s "MFA enrolment is a deliberate act by a Superadmin"), TOTP as
  the documented fallback; both platform-verified, never IdP-claimed alone.*
- **Q4 — Per-tenant IdP registration UX and who owns it.** Registering an enterprise tenant's issuer/entityID
  and verified domain is a config act today (FR6); whether it gets an operator-console surface (P8) or stays a
  deployment-injected config is open. *Recommendation: config-injected for M16 (no new one-way door), an
  operator-console registration surface as a P8 follow-up once the config contract is stable.*
- **Q5 — Single-logout (SLO) scope.** FR12 revokes the server-side session on logout; IdP-initiated SLO and
  back-channel logout are a larger contract. *Recommendation: local session revocation on logout for M16,
  best-effort front-channel IdP logout where supported, back-channel SLO deferred to the enterprise
  provisioning follow-up.*
- **Q6 — Which Okta authorization server does the recipe default to — the org one, or a custom one?** They
  assert **different issuers** (FR22), and the choice changes what the customer's admin configures: the org
  authorization server is the smallest configuration and carries the standard claim set; a custom one (the
  built-in `default`, or a dedicated one) is where custom claims, scopes and per-application policies live.
  *Recommendation: the recipe **defaults to the org authorization server**, because the console needs `sub`,
  `email` and `email_verified` and nothing else, and every extra configuration step is a step to get wrong.
  Document the custom form beside it — customers with a claims policy will use one, and FR22 makes both
  expressible without a code path. Neither may become required.*
- **Q7 — Do we ask for a `groups` claim?** Tempting, because roles-within-a-tenant is the obvious next
  question. *Recommendation: **no**, not in this increment. Identity proves who; roles are the RBAC model's
  job, and asking a customer's IdP administrator to release group membership we do not consume is asking for
  more access than we need — the fastest way to fail a security review is to request a claim and then not
  use it. When roles are modelled, the ask becomes justifiable and the recipe grows one line.*
- **Q8 — Which org backs the *operator* console?** A dedicated org, a dedicated authorization server inside
  our corporate org, or a plain application in it? *Recommendation: at minimum a **dedicated application with
  its own authorization server and its own assignment**, because the operator principal is cross-tenant and
  can halt the fleet — sharing the general corporate application would make "everyone with a corporate
  account" the population that only MFA stands between and the fleet. A fully separate org is stronger and
  costlier; the decision belongs with whoever owns our own IT posture, and it should be made explicitly
  rather than inherited from wherever the first application happened to be created.*
- **Q9 — When does push-based revocation (SCIM / event hooks) get scheduled?** Deferred deliberately (D13),
  and R12 is the cost of leaving it deferred. *Recommendation: keep it deferred and name the **trigger**
  rather than a date — the first customer whose security policy requires directory-push revocation, or the
  first deployment whose session TTL has to be shortened past usability to compensate. Both are observable
  events; "next quarter" is not.*
