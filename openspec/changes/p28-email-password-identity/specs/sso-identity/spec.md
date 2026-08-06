# sso-identity

## MODIFIED Requirements

### Requirement: The console SHALL authenticate through the unchanged tenant-identity seam, changing only the seam implementation and its routes

P22 replaces the implementation of `verify(assertion) → { tenantId }` and adds the redirect/callback routes the
flow requires. P28 adds a `password` kind and, with it, a second entry point on the same seam —
`verifyPassword(email, password) → { tenantId, userId }` — because a two-field credential cannot be carried by a
one-string assertion without inventing an encoding. Neither phase SHALL modify the session store, the session
cookie, revocation, scope derivation, the fail-closed middleware, or any tenant page (ADR-008 Rule 3).

#### Scenario: The layer above the seam is unchanged
- **WHEN** a tenant signs in through OIDC, SAML, or an email and a password
- **THEN** the session record carries the same fields it carried before, plus the person where one is proved
- **AND** the session store, cookie flags, revocation semantics, scope derivation and fail-closed middleware are
  unchanged, asserted by a regression test
- **AND** the only changed source files are the seam implementations and the added routes

#### Scenario: A change above the seam is rejected
- **WHEN** a change couples the session exchange, scope derivation or a tenant page to OIDC-, SAML- or
  password-specific concepts
- **THEN** it is non-conformant with this capability

#### Scenario: The password kind holds no verifier in the console
- **WHEN** the seam kind is `password` and a person submits an address and a password
- **THEN** the console forwards them once to the platform and holds no stored hash and no verification logic
- **AND** the submitted password is not written to the session record, a cookie, a log, or an upstream call
  other than that one verification

### Requirement: The assertion SHALL never be persisted

An assertion — and, on the `password` kind, a submitted password — is verified, exchanged for a session, and
dropped. It is not stored in the session record, not written to a cookie, not logged, and not carried upstream
beyond the single verification call.

#### Scenario: Nothing retains the proof
- **WHEN** any seam kind completes a sign-in
- **THEN** the proof presented appears in no session record, cookie, log line or telemetry attribute
- **AND** a test asserts the absence across those surfaces

### Requirement: The identity provider SHALL be an aggregated /readyz component

The readiness surface names the identity mechanism in force and whether it is serviceable, and never anything
behind it.

#### Scenario: The mechanism is reported
- **WHEN** `/readyz` is read
- **THEN** it names the seam kind in force and reports reachability
- **AND** it discloses no client id, secret, issuer allowlist or credential

#### Scenario: A password deployment reports its mail dependency
- **WHEN** the seam kind is `password`
- **THEN** `/readyz` additionally reports whether mail is configured, because confirmation and reset depend on
  it and a deployment that cannot send them is degraded rather than healthy
