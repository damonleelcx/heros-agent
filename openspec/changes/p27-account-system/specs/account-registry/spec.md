# Account Registry — the durable tenant and its credentials

Product rationale: [`docs/prd/P27-account-system.md`](../../../../../docs/prd/P27-account-system.md) §6
(FR1–FR5). Design: [`design.md`](../../design.md) §3.1, §3.5, §5.6.

Today a tenant is a key in a map [`auth.Registry`](../../../../../internal/auth/registry.go) builds from
configuration at boot, and an API key is a plaintext string in that same file. This capability makes both
durable, keeps every existing deployment working by demoting the configuration list to a **seed**, and makes a
credential revocable at the next request.

Inherits [ADR-004](../../../../../docs/adr/ADR-004-runtime-config-binding.md) (fail-static binding) and
[`secrets-baseline.md`](../../../../../docs/decisions/secrets-baseline.md) — this capability introduces no new
secret mechanism.

## ADDED Requirements

### Requirement: The platform SHALL store each tenant as a durable record and resolve every principal against it

A tenant record is `{tenant_id, name, status, created_at}` with `status ∈ {active, suspended}` — the same
vocabulary `account.Status` already uses, because one lifecycle with two enums is a place for two answers to
disagree.

#### Scenario: A tenant created at runtime survives a restart
- **WHEN** a tenant is created while the platform is running, and the platform is then fully restarted
- **THEN** the tenant is still present with the same identifier, name and status
- **AND** every credential issued to it still authenticates

#### Scenario: Resolution reads the durable record
- **WHEN** a request presents a valid credential
- **THEN** the resolved principal's tenant is read from the durable record, not from process configuration

### Requirement: The platform SHALL apply configured tenant credentials as an expand-only seed at boot

The seed creates what is absent. It SHALL NOT update, overwrite, downgrade or delete an existing tenant or
credential. Configuration describes a starting point; the database is the truth.

#### Scenario: An upgrade preserves every configured tenant
- **WHEN** a deployment holding N configured tenant credentials starts for the first time on this version
- **THEN** there are exactly N tenant records and N credential records afterwards
- **AND** every API key that authenticated before the upgrade authenticates after it

#### Scenario: The seed is idempotent
- **WHEN** the platform boots a second time against the same configuration
- **THEN** no row is created, updated or deleted, verifiable by comparing a full table checksum before and
  after

#### Scenario: A runtime tenant is not deleted by the seed
- **WHEN** a tenant that does not appear in configuration exists in the store, and the platform restarts
- **THEN** that tenant and its credentials are untouched

#### Scenario: A partially failed seed refuses to serve
- **WHEN** any configured entry cannot be seeded
- **THEN** the platform refuses to serve traffic and reports which entry failed
- **AND** it does **not** start with a subset of its tenants present

#### Scenario: The seed's outcome is reported, not inferred
- **WHEN** the platform becomes ready
- **THEN** the readiness surface reports how many tenants and credentials the seed created and how many were
  already present

### Requirement: The platform SHALL store API credentials as hashes and reveal the plaintext exactly once

A credential record is `{credential_id, tenant_id, user_id?, label, hash, created_at, revoked_at?}`. `user_id`
absent means a **machine credential**, not an unknown owner.

#### Scenario: The secret is returned once and never again
- **WHEN** a credential is created
- **THEN** the plaintext appears in that response only
- **AND** no later read, list, export, log line or trace attribute contains it

#### Scenario: Verification compares against the stored hash
- **WHEN** a credential is presented
- **THEN** it is verified against the stored hash in constant time
- **AND** no plaintext credential is stored anywhere at rest

#### Scenario: A credential names a person or names none
- **WHEN** a credential is created through an interactive session
- **THEN** it records the acting user
- **AND WHEN** a credential is created as a machine credential
- **THEN** its user reference is **absent**, never a placeholder value

### Requirement: The platform SHALL refuse a revoked credential at the next request, and SHALL NOT cache a verification result

Verification is one lookup per authenticated request. A cached *accept* is a cached *non-revocation*, so
there is no positive-result cache — see `design.md`'s correction to NFR1.

#### Scenario: Revocation takes effect immediately after sustained successful use
- **WHEN** a credential has been used successfully many times in immediate succession and is then revoked
- **THEN** its **next** request is refused
- **AND** the test asserting this drives the successful requests FIRST, because a test that revokes a
  never-used credential passes against exactly the caching implementation this forbids

#### Scenario: A revoked credential is not a probing oracle
- **WHEN** a revoked credential and an unknown credential are each presented
- **THEN** both receive the same generic unauthorized response, indistinguishable in body and status
- **AND** the platform's own logs distinguish them

### Requirement: The platform SHALL halt a suspended tenant at authentication rather than at each feature

#### Scenario: A suspended tenant is refused once, not per feature
- **WHEN** a credential belonging to a suspended tenant is presented
- **THEN** the request is refused at authentication
- **AND** no feature-level gate is reached, so no surface can accidentally omit the check

#### Scenario: Suspension is legible to us and opaque on the wire
- **WHEN** a suspended tenant's request is refused
- **THEN** the wire response is indistinguishable from an unknown credential
- **AND** the platform records the suspension as the distinguishable cause

### Requirement: The command line SHALL be able to obtain a user-scoped credential through a device authorization

`heros login` with no `--token` requests a device code, the person approves it in the console behind their
existing SSO session and selects one of their organizations, and the CLI polls until a credential is issued.
The CLI SHALL NOT handle a password, an assertion or an ID token at any point.

#### Scenario: A developer signs in without being handed a secret
- **WHEN** `heros login` is run with no token
- **THEN** the CLI displays a short verification code and a URL, and polls for the result
- **AND WHEN** a person holding an active membership approves that code in the console and selects an
  organization
- **THEN** the CLI receives a credential scoped to that organization and stores it

#### Scenario: The issued credential names the approving person
- **WHEN** a device authorization completes
- **THEN** the created credential record carries the approving person's user reference
- **AND** it is listed in that organization's credentials as a **personal** credential, labelled with the
  device the CLI reported

#### Scenario: Removing the member revokes their command-line access
- **WHEN** the approving person's membership is removed
- **THEN** the credential that device authorization issued is revoked
- **AND** the CLI's next request is refused, with no restart and no grace period

#### Scenario: A device code is short-lived and single-use
- **WHEN** a device code has expired, has already been approved, or was denied
- **THEN** no credential is issued
- **AND** the CLI's message does not distinguish denial from expiry, because the difference is only useful to
  somebody guessing codes

#### Scenario: Only a member may approve, and only into their own organization
- **WHEN** an approval names an organization in which the approver holds no active membership
- **THEN** it is refused and no credential is issued

#### Scenario: The machine path is unchanged
- **WHEN** `heros login --token <value>` is run
- **THEN** it behaves exactly as before
- **AND** a credential with no user reference is reported as a **machine** credential and is **not** revoked by
  removing any person

### Requirement: The identity endpoint SHALL name the person and the organization additively

#### Scenario: Existing consumers are unaffected
- **WHEN** the identity endpoint answers
- **THEN** the `identity` field keeps its current name, meaning and value
- **AND** both existing callers — the CLI's token validation and the console's platform-token seam — continue
  to work without change

#### Scenario: The response says who and where
- **WHEN** a credential carrying a user reference is presented
- **THEN** the response additionally names the organization and the acting person, and reports the credential
  as personal
- **AND WHEN** a machine credential is presented
- **THEN** the acting person is **absent** — never a placeholder — and the credential is reported as machine
