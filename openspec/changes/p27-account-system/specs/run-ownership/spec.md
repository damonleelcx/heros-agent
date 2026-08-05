# Run Ownership — the work belongs to somebody, and scope travels inside the credential

Product rationale: [`docs/prd/P27-account-system.md`](../../../../../docs/prd/P27-account-system.md) §6
(FR14–FR19). Design: [`design.md`](../../design.md) §3.8, §4, §5.2.

Migration `0005_p2_run.up.sql` keys `run` by `run_id` with no tenant column, and there is no collection route
beside `GET /api/v1/runs/{run_id}`. [`scope.ts`](../../../../../web/console/src/lib/scope.ts) states the
consequence carefully: the platform's routes are "keyed by subject … not by tenant", and it declines to claim
the console enforces isolation. The isolation it defers to — *"the platform against the credential and the
`X-Console-Tenant` header"* — does not exist: the platform never reads that header, and every console request
carries one process-wide credential.

This capability writes the owner, adds the scoped list surface, and closes the isolation gap by putting the
tenant **inside** the credential rather than beside it.

Inherits [ADR-008](../../../../../docs/adr/ADR-008-console-tenant-identity-seam.md) Rule 2 — *a request must not
be trusted to describe its own authority* — which it strengthens rather than relaxes.

## ADDED Requirements

### Requirement: The platform SHALL record the owning tenant when work is created, and SHALL distinguish an absent owner from an absent result

Runs, variant specs and eval runs record the owning tenant at write time, derived from the verified principal.
A row created before this capability carries a NULL owner meaning **pre-ownership**. Proposals are excluded:
migration 0025 already gave `proposal` a `tenant_id NOT NULL`, so they have been tenant-scoped since P5.5 and
have no pre-ownership state.

#### Scenario: New work records its owner
- **WHEN** a run, variant spec or eval run is created by an authenticated principal
- **THEN** the owning tenant is stored on the row, taken from the verified principal
- **AND** it is not taken from any client-supplied value

#### Scenario: Pre-ownership rows are not guessed at
- **WHEN** a row exists that was created before this capability
- **THEN** its owner is NULL
- **AND** no process infers, backfills or assigns an owner for it from a neighbouring table

#### Scenario: Pre-ownership is a distinct state, not an empty list
- **WHEN** a tenant whose only work predates this capability opens a listing surface
- **THEN** the surface reports that work exists which predates ownership recording, with its own message and
  its own next action
- **AND** it does **not** render the same state as a tenant that has never run anything

### Requirement: The platform SHALL provide a tenant-scoped listing of work that accepts no tenant parameter

#### Scenario: A tenant lists its own runs
- **WHEN** an authenticated principal requests the run collection
- **THEN** the response contains this tenant's runs, newest first, paged with a stable cursor
- **AND** it contains no run belonging to another tenant, and no run whose owner is NULL

#### Scenario: The endpoint has no tenant parameter to get wrong
- **WHEN** a request carries a tenant or organization identifier in its path, query, body or headers
- **THEN** that value has no effect on the result set
- **AND** the route exposes no parameter by which a tenant could be supplied

#### Scenario: No listing is unbounded
- **WHEN** a tenant holds a very large number of runs
- **THEN** the response is a bounded page with a cursor
- **AND** no code path returns the full set

### Requirement: The platform SHALL derive scope from the verified credential, and the `X-Console-Tenant` header SHALL be removed

The header is deleted rather than made authoritative. An ignored header that names authority is read by the
next reader as the mechanism.

#### Scenario: The header no longer exists on the wire
- **WHEN** the console forwards a request upstream
- **THEN** no `X-Console-Tenant` header is sent
- **AND** the platform has no code path that reads one

#### Scenario: Reintroducing the header fails the build
- **WHEN** a change adds a header naming a tenant to the upstream forwarder
- **THEN** a build fence fails
- **AND** the fence has been observed failing against a checked-in broken fixture

#### Scenario: A header cannot widen scope
- **WHEN** a request presents a valid credential together with a header naming a different tenant
- **THEN** the resolved scope is the credential's tenant, unchanged

### Requirement: The console BFF SHALL exchange its session for a short-lived tenant-scoped platform token, and SHALL fail closed without one

The browser continues to receive no credential of any kind.

#### Scenario: The forwarded credential carries the tenant
- **WHEN** the BFF makes an upstream call on behalf of a session
- **THEN** it presents a short-lived token bound to that session's tenant
- **AND** the platform resolves the tenant from that token through its existing credential path

#### Scenario: No scoped token means no upstream call
- **WHEN** the BFF cannot obtain a scoped token for any reason
- **THEN** it makes no upstream call and the surface fails closed
- **AND** the failure is reported as an infrastructure failure, not as an empty result

#### Scenario: The browser still holds nothing
- **WHEN** a session is active
- **THEN** the browser holds only an opaque session token it cannot read
- **AND** no platform credential, scoped token or provider secret reaches the browser

#### Scenario: A positive token cache is bounded, and a revocation is not cached
- **WHEN** a scoped token is cached for reuse within its lifetime
- **THEN** the cache holds accepted results only
- **AND** no revocation decision is served from a cache

### Requirement: A subject belonging to another tenant SHALL be indistinguishable from a subject that does not exist

#### Scenario: Cross-tenant read answers 404
- **WHEN** a principal requests a run, variant spec or eval run owned by another tenant
- **THEN** the response is `404` with a body identical to that of a non-existent identifier
- **AND** the response does not confirm that the subject exists

#### Scenario: The test that proves this can fail
- **WHEN** the cross-tenant test is written
- **THEN** it issues the two probes as **two different tenants**
- **AND** a version of the test that issues both as the same tenant is rejected, because it passes vacuously

### Requirement: The owning tenant of existing work SHALL be immutable

#### Scenario: No transfer interface exists
- **WHEN** the API surface is enumerated
- **THEN** there is no endpoint, parameter or administrative operation that changes an existing row's owning
  tenant
- **AND** the absence is asserted by test, because this property erodes by somebody adding a convenient
  endpoint rather than by a bug

#### Scenario: Ownership does not follow a membership change
- **WHEN** the person who created a run leaves the organization
- **THEN** the run's owning tenant is unchanged and it remains listed for that organization
