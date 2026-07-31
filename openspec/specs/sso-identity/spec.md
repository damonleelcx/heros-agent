# Customer Console SSO & Identity — Spec (folded from P22)

Implements the mechanism [`ADR-008`](../../../docs/adr/ADR-008-console-tenant-identity-seam.md) deferred,
behind the **unchanged** `verify(assertion) -> { tenantId }` seam. Inherits
[`ADR-002`](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (the transformed program
carries its own identity; ours is platform-internal),
[`ADR-004`](../../../docs/adr/ADR-004-runtime-config-binding.md) (fail-static binding for the tenant map) and
[`secrets-baseline.md`](../../../docs/decisions/secrets-baseline.md) 1.1 (the one secret mechanism identity
reuses). Customer-facing wording: [`docs/sales/P22-identity-copy.md`](../../../docs/sales/P22-identity-copy.md).

Product rationale:
[`docs/prd/P22-sso-identity.md`](../../../docs/prd/P22-sso-identity.md). This capability supplies the
identity **mechanism** ADR-008 deferred, behind the unchanged `verify(assertion) → { tenantId }` seam.

### Requirement: The console SHALL authenticate through the unchanged tenant-identity seam, changing only the seam implementation and its routes

P22 replaces the implementation of `verify(assertion) → { tenantId }` and adds the redirect/callback routes the
flow requires. It SHALL NOT modify the session store, the session cookie, revocation, scope derivation, the
fail-closed middleware, or any tenant page (ADR-008 Rule 3).

#### Scenario: The layer above the seam is unchanged
- **WHEN** P22 lands and a tenant signs in through the new OIDC or SAML mechanism
- **THEN** the session record is exactly `{ id, tenantId, issuedAt, expiresAt, revokedAt }` as before
- **AND** the session store, cookie flags, revocation semantics, scope derivation and fail-closed middleware are
  unchanged, asserted by a regression test
- **AND** the only changed source files are the seam implementation and the added `/auth/*` routes

#### Scenario: A change above the seam is rejected
- **WHEN** a change couples the session exchange, scope derivation or a tenant page to OIDC- or SAML-specific
  concepts
- **THEN** it is non-conformant with this capability

### Requirement: The console SHALL support OIDC Authorization Code flow with PKCE as the primary mechanism

The flow SHALL use the Authorization Code grant with PKCE, a single-use `state`, a `nonce`, and an ID token
validated against the IdP's discovery document and JWKS. The implicit flow SHALL NOT be used, and no token SHALL
be placed in a URL or fragment.

#### Scenario: A valid OIDC sign-in resolves a tenant
- **WHEN** a user completes the OIDC Authorization Code + PKCE flow and the ID token validates (issuer, audience,
  signature via JWKS, `nonce`, expiry)
- **THEN** the seam resolves exactly one `tenantId` and a session is issued for it
- **AND** the ID token is dropped, never persisted

#### Scenario: The implicit flow is refused
- **WHEN** an authorization response would deliver a token in a URL fragment (implicit flow)
- **THEN** the flow is non-conformant and no session is issued

### Requirement: The console SHALL support SAML 2.0 as the enterprise alternative behind the same seam

SAML 2.0 SHALL be SP-initiated, verify the assertion signature, enforce the audience restriction, and accept the
response only at an allowlisted Assertion Consumer Service (ACS) URL. It SHALL resolve to exactly one `tenantId`
through the same seam as OIDC.

#### Scenario: A valid SAML assertion resolves a tenant
- **WHEN** a signed SAML assertion arrives at the allowlisted ACS, its signature verifies, its audience matches,
  and it is within its validity window
- **THEN** the seam resolves exactly one `tenantId` and a session is issued
- **AND** the assertion is dropped, never persisted

#### Scenario: A response to a non-allowlisted ACS is refused
- **WHEN** a SAML response targets an ACS URL not on the allowlist
- **THEN** it is refused and no session is issued

### Requirement: The assertion SHALL never be persisted

The OIDC ID token / SAML assertion SHALL be verified, exchanged for a session, and dropped — not stored in the
session record, not written to a cookie, not logged, not emitted as a trace attribute, and not carried upstream.
The BFF's own server-held platform credential authorizes upstream calls.

#### Scenario: No assertion material survives sign-in
- **WHEN** a session has been issued from a verified assertion
- **THEN** a search of the session record, the cookies, the logs and the telemetry for the ID token / SAML
  assertion material returns nothing
- **AND** upstream calls are authorized by the BFF's own credential, carrying only the tenant

### Requirement: The tenant SHALL be authoritative and server-side; the client SHALL NOT widen it

A tenant identifier arriving from the client in any path, query, body, header, or a returned `state` SHALL NEVER
widen, change, or override the session's tenant. The tenant comes only from the verified assertion via the
configured mapping.

#### Scenario: A forged client tenant does not widen scope
- **WHEN** a request carries a tenant identifier in a path, query, body, header, or `state` that differs from the
  session's tenant
- **THEN** the resolved tenant is unchanged and every upstream call remains scoped to the session's tenant
- **AND** the discrepancy does not grant access to another tenant's data

### Requirement: An SSO identity SHALL map to a tenant by a configured strategy, changeable without a deploy

The mapping SHALL be configuration — verified-email-domain → tenant, per-tenant IdP registration
(issuer/entityID → tenant), or just-in-time provisioning under an explicit allow rule — injected the way
`CONSOLE_TENANT_ASSERTIONS` / the `Secrets` seam are injected. A hardcoded per-tenant branch is non-conformant,
and a mapping change SHALL NOT require a code deploy.

#### Scenario: Onboarding a tenant is a config change, not a deploy
- **WHEN** a new enterprise tenant's issuer/entityID and verified domain are added to the injected mapping
- **THEN** users from that IdP resolve to that tenant on the next sign-in with no code change and no redeploy

#### Scenario: A hardcoded mapping is rejected
- **WHEN** the tenant mapping is expressed as a compiled per-tenant branch or table
- **THEN** it is non-conformant with this capability

### Requirement: An identity matching no mapping rule SHALL be refused as a security event

Just-in-time provisioning SHALL occur only under an explicit configured allow rule. An identity matching no rule
SHALL be refused, not auto-created, and the refusal SHALL be recorded as a security event.

#### Scenario: An unmapped identity is refused, not provisioned
- **WHEN** a verified assertion carries an identity that matches no configured mapping or JIT allow rule
- **THEN** no session is issued and no tenant is auto-created
- **AND** the refusal is recorded as a security event

#### Scenario: JIT provisions only under an allow rule
- **WHEN** a verified identity's domain is on the configured JIT allow list for a tenant
- **THEN** the identity may be provisioned into that tenant
- **AND** an identity whose domain is not on any allow list is refused

### Requirement: A domain or claim the IdP asserts SHALL be verified, not trusted

Email-domain mapping SHALL resolve only to a domain the tenant has proven ownership of via configuration, so a
self-asserted `email` claim from an unrelated IdP cannot claim another tenant's domain.

#### Scenario: A self-asserted domain cannot claim another tenant
- **WHEN** an assertion from IdP A carries `email` in a domain that tenant B has registered as verified
- **THEN** the identity does not resolve to tenant B unless IdP A is the registered IdP for that verified domain
- **AND** the attempt is refused rather than resolved across the tenant boundary

### Requirement: Sessions SHALL retain the ADR-008 model with revocation effective at the next request and no grace

Sessions SHALL be server-side `{ id, tenantId, issuedAt, expiresAt, revokedAt }` with a bounded TTL, an opaque
browser token that is not the session id, and the store SHALL be read on every request so a revoked or
IdP-disabled session is denied at the next request with no grace period.

#### Scenario: Revocation takes effect at the next request
- **WHEN** a session is revoked (locally or because the IdP disabled the user)
- **THEN** the very next request presenting that session is denied
- **AND** there is no cached "was valid a moment ago" path that serves it

### Requirement: A refresh SHALL re-verify rather than silently extend, and a self-vouching token SHALL NOT be the session

A refresh SHALL re-establish the session by re-verifying (a fresh authorization or a validated refresh-token
exchange) and SHALL NOT extend a session past a bound the deployment can configure. A self-contained token whose
own expiry claim cannot be revoked SHALL NOT be used as the session.

#### Scenario: A refresh cannot outlive revocation
- **WHEN** a session is revoked and a refresh is then attempted
- **THEN** the refresh does not resurrect or extend the revoked session
- **AND** the session remains a server-side record read on every request, never a self-vouching token

### Requirement: The identity flow SHALL fail closed when the IdP is unreachable

When the IdP is unreachable — OIDC discovery/JWKS or SAML metadata cannot be fetched or validated — sign-in SHALL
fail and no session SHALL be issued. The surface SHALL NEVER fail open and SHALL NEVER issue a session from a
cached credential.

#### Scenario: IdP outage issues no session
- **WHEN** the IdP's discovery/JWKS (OIDC) or metadata (SAML) cannot be fetched or validated
- **THEN** sign-in fails and no session is issued
- **AND** the surface does not fall back to a cached credential or fail open

### Requirement: Login, logout and callback SHALL redirect rather than render, and logout SHALL revoke server-side

Login, logout and callback SHALL be routes that redirect an unauthenticated request to sign-in rather than render
a shell. Logout SHALL revoke the server-side session so the next request is denied, and where the IdP supports it
SHALL initiate front-channel logout.

#### Scenario: An unauthenticated request redirects
- **WHEN** an unauthenticated request reaches a tenant route or a callback fails
- **THEN** it is redirected to sign-in with a `reason` distinguishing "session ended" from "sign in"
- **AND** no tenant shell is rendered before the redirect

#### Scenario: Logout denies the next request
- **WHEN** a user logs out
- **THEN** the server-side session is revoked and the next request presenting it is denied

### Requirement: No identity secret SHALL appear on any uncontrolled path

The OIDC client secret, the SAML SP signing/decryption private key, and every session/signing key SHALL be
resolved through the `Secrets` seam (`HEROS_SECRETS_SOURCE`), with an ambient identity where the store supports
it and no bootstrap secret in a manifest. None SHALL appear in git, an env-example, a client bundle, a log line,
or a trace attribute.

#### Scenario: A committed identity secret fails the build
- **WHEN** an OIDC client secret, SAML private key, or session key is committed to a manifest, an env-example, or
  a bundle
- **THEN** the secret scan fails the build
- **AND** the running deployment resolves the secret through the `Secrets` seam with no bootstrap secret in the
  manifest

### Requirement: The callback SHALL enforce CSRF and replay defenses

The callback SHALL enforce a single-use `state` bound to the browser, a `nonce` bound to the ID token, PKCE
binding the code to the client, and an assertion freshness window plus a one-time guard. A missing/forged
`state`, a reused authorization code, or a stale/replayed assertion SHALL be refused with a single generic
reason.

#### Scenario: A replayed callback is refused
- **WHEN** a callback presents a `state` already consumed, a reused authorization code, or an assertion outside
  its freshness window or seen before
- **THEN** it is refused with a single generic reason and no session is issued

#### Scenario: Removing a defense turns a test red
- **WHEN** the `state`, `nonce`, PKCE, or freshness/one-time check is removed
- **THEN** the corresponding security test goes red

### Requirement: The redirect / SAML ACS target SHALL be an allowlist

A callback or ACS target not on the configured allowlist SHALL be refused, so the flow cannot be turned into an
open redirect. A wildcard or reflected redirect target is non-conformant.

#### Scenario: An off-allowlist redirect is refused
- **WHEN** the flow is asked to redirect to, or accept a response at, a target not on the allowlist
- **THEN** it is refused and no session is issued

### Requirement: The identity provider SHALL be an aggregated /readyz component

`/readyz` SHALL report `identity_provider: {kind, issuer, reachable}` and SHALL report not ready, naming
`identity_provider`, when the IdP is unreachable. The signal SHALL measure reachability, not traffic freshness,
and SHALL NOT depend on the traffic it gates.

#### Scenario: An unreachable IdP is named on /readyz
- **WHEN** the IdP is unreachable
- **THEN** `/readyz` reports `not_ready`, lists `identity_provider` in the degraded components, and reports its
  `{kind, issuer, reachable:false}`
- **AND** the readiness verdict is read from the endpoint, never from a UI dashboard
