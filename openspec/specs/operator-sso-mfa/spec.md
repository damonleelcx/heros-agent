# Operator SSO & Verified MFA — Spec (folded from P22)

Makes [`internal/adminidentity`](../../../internal/adminidentity/) pluggable against a real OIDC/SAML admin
IdP and its second factor **platform-verified**, without reinventing the module. Inherits
[`ADR-006`](../../../docs/adr/ADR-006-console-deploy-packaging.md) / P19 Decision 5 (the operator console is a
second origin with a disjoint cookie jar) and
[`secrets-baseline.md`](../../../docs/decisions/secrets-baseline.md) 1.1.

Product rationale:
[`docs/prd/P22-sso-identity.md`](../../../docs/prd/P22-sso-identity.md) §9 (System Designer / Backend). This
capability makes the operator identity module's already-real SSO+MFA seam
([`internal/adminidentity`](../../../internal/adminidentity/)) **pluggable against a real IdP** and its second
factor **platform-verified**, without reinventing the module.

### Requirement: The operator console SHALL authenticate through the existing admin-identity provider seam with a real, pluggable IdP

P22 SHALL provide a real, pluggable OIDC/SAML admin IdP implementation behind the existing
`adminidentity.IdentityProvider` seam (replacing the fixture `TestMode` HMAC issuer for production), keeping the
`Verify`/`Describe` contract and the enum-named provider kind. The seam contract SHALL NOT change.

#### Scenario: A real admin IdP plugs into the existing seam
- **WHEN** the operator console is deployed with a real OIDC or SAML admin IdP
- **THEN** it authenticates through the same `IdentityProvider.Verify(assertion) → Claims` contract the fixture
  used
- **AND** `Describe()` reports the live provider kind and issuer with `test_mode: false`

#### Scenario: The fixture issuer is refused in production
- **WHEN** a production operator console is pointed at the fixture `TestMode` issuer
- **THEN** the development identity mode refuses to boot (mirroring `ADMIN_IDENTITY_MODE=dev` in production)

### Requirement: Every operator authentication SHALL require SSO and a platform-verified second factor

A valid SSO assertion alone SHALL issue no session (the existing `ErrMFARequired` denial). The second factor
SHALL be platform-verified — WebAuthn preferred, TOTP fallback — rather than only an IdP claim the platform
refuses on absence, so a misconfigured IdP MFA policy still results in denial on the fleet-halting surface.

#### Scenario: Valid SSO without a verified factor issues no session
- **WHEN** an operator presents a valid SSO assertion but no platform-verified second factor
- **THEN** no admin session is issued and the denial is `ErrMFARequired`
- **AND** the denial is recorded as a security event

#### Scenario: A verified factor completes authentication
- **WHEN** an operator presents a valid SSO assertion and a platform-verified WebAuthn (or TOTP) factor
- **THEN** an admin session is issued for the operator principal

#### Scenario: A misconfigured IdP MFA policy still denies
- **WHEN** the IdP asserts MFA was satisfied but the platform does not itself verify a second factor
- **THEN** no session is issued — the platform's verification, not the IdP's claim, is the invariant

### Requirement: The operator identity domain SHALL remain disjoint from the customer domain

The operator surface SHALL run on a different origin with a disjoint cookie jar, and the operator principal type
SHALL carry no tenant_id. There SHALL be no code path that promotes a customer `auth.Principal` — whatever its
role string — into an admin session.

#### Scenario: A customer session cannot reach an operator capability
- **WHEN** a customer session (any tenant role) is presented to the operator surface
- **THEN** it does not authorize any admin capability, and no promotion path exists from a tenant principal to an
  admin session
- **AND** the operator surface is unreachable from the customer console's origin, asserted by a cross-origin test

### Requirement: Disabling an operator principal SHALL revoke live sessions and deny future authentication

Disabling an operator SHALL make their live sessions explicitly revocable (offboarding revokes all sessions for
that principal), and a disabled principal SHALL obtain no session even with a valid SSO assertion and a verified
factor.

#### Scenario: Offboarding takes effect immediately
- **WHEN** an operator principal is disabled and their sessions are revoked
- **THEN** their next request is denied and no new session can be obtained even with a valid SSO assertion and a
  verified factor

### Requirement: All operator identity secrets SHALL be secrets-manager-sourced, fail closed, and reported on /readyz

The assertion-verification, factor-verification and session-signing keys SHALL be sourced from the secrets
manager under reserved logical names (via `providergateway.Secrets`), SHALL fail closed when unavailable (no
unsigned or unverified path), and the live admin IdP SHALL be reported on `/readyz` (`admin_idp`) — never a key,
never a secret id.

#### Scenario: A missing signing key fails closed
- **WHEN** an operator identity signing key cannot be sourced from the secrets manager
- **THEN** no session is issued and no assertion is verified against a fallback key — the surface fails closed

#### Scenario: The live admin IdP is named on /readyz
- **WHEN** `/readyz` is read
- **THEN** it reports `admin_idp` with the live provider's kind and issuer, and never a key or a secret id
