# Session Replay — Spec (folded from P24)

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR21–FR24), §8.2 D2. Technical decisions: [`../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md`](../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md) D2, D9.

Covers the highest-value UX instrument available and the one surface it is allowed on.

> The refusal is the capability. Session replay records **the screen**. The screen under the tenant
> prefix renders prompt text, generated diffs, node identifiers, model configuration and run output; the
> operator console's screen renders cross-tenant aggregates, tenant names, active impersonation state and
> audit rows. A replay of those is a legible copy of the content class the platform's egress boundary was
> constructed to keep in. Maintaining that boundary while installing a recorder on the same content is
> not a trade-off — it is a contradiction, and it resolves in favour of the boundary.

## Requirements

### Requirement: Session replay SHALL load only on the public surface, and only under a granted category

Session replay SHALL be loaded only on routes that require no session, read no tenant data and make no
upstream platform call, and only after a `session_replay` grant.

#### Scenario: The public surface records after a grant
- **WHEN** a visitor grants `session_replay` on the public surface
- **THEN** the replay script loads from an allowlisted origin, injected with the per-request nonce.

#### Scenario: No grant, no recorder
- **WHEN** a visitor has not granted `session_replay`
- **THEN** no replay script loads on any surface
- **AND** no request to a replay origin is made.

### Requirement: Session replay SHALL be refused on every tenant surface and every operator-console route, with no override

Session replay SHALL NOT be loaded under the tenant prefix, under the BFF data prefix, or on any
operator-console route. This refusal SHALL NOT be reachable by any plan, role, entitlement, feature flag,
environment variable, configuration value or request parameter, and SHALL NOT be grantable by a customer.

#### Scenario: A tenant page is never recorded
- **WHEN** a customer works across every tenant-prefixed route and the network traffic is inspected in a
  real browser
- **THEN** no replay script is present and no replay origin is contacted.

#### Scenario: No configuration turns it on
- **WHEN** any plan, role, entitlement, flag, environment variable or request parameter is set to enable
  replay on a tenant or operator route
- **THEN** replay is still not loaded
- **AND** no code path exists that would load it on those prefixes.

#### Scenario: A customer cannot consent on behalf of their developers
- **WHEN** a tenant administrator asks for replay to be enabled on their tenant's surfaces
- **THEN** the request is refused
- **AND** the refusal states that the recording would contain prompt text, source and diffs belonging to
  people who are not the party consenting.

### Requirement: The refusal SHALL be structural, not enforced page by page

The replay script SHALL be unreachable from the tenant and operator layouts, and the replay origin SHALL
be absent from those prefixes' content-security policy. A rule enforced by per-page judgement SHALL NOT
be the mechanism.

#### Scenario: A newly added tenant page inherits the refusal
- **WHEN** a new route is added under the tenant prefix with no replay-specific consideration
- **THEN** replay is not loaded on it
- **AND** the prefix's policy names no replay origin, so a load would be refused by the browser.

#### Scenario: The policy would refuse it even if the code tried
- **WHEN** a replay script tag is placed on a tenant-prefixed page
- **THEN** the browser refuses to execute it under that prefix's policy
- **AND** the refusal is visible in the browser's error log.

### Requirement: A replay runtime in a tenant- or operator-reachable client chunk SHALL fail the build

A build in which a session-replay runtime appears in any client chunk reachable from a tenant or
operator route SHALL fail, naming the runtime and the chunk.

#### Scenario: An accidental import fails the build
- **WHEN** a session-replay package is imported into a module reachable from a tenant route and the
  console is built
- **THEN** the build fails
- **AND** the failure names the runtime and the chunk it appeared in.

#### Scenario: The fence has been demonstrated red
- **WHEN** the fence is validated
- **THEN** a deliberate fixture violation produces a build failure
- **AND** the fence is not accepted on the basis of a passing run alone.

### Requirement: Masking SHALL be on by default where replay does run

On the surface where replay runs, all text inputs and form fields SHALL be masked by default. Any
unmasking SHALL be an explicit, per-element opt-in with a recorded reason.

#### Scenario: Input content is masked without configuration
- **WHEN** replay runs on the public surface with no masking configuration supplied
- **THEN** all text input and form-field content is masked
- **AND** masking is not dependent on a selector list that must be maintained.

#### Scenario: Unmasking is deliberate and recorded
- **WHEN** an element is unmasked
- **THEN** the unmasking is declared per element
- **AND** a reason is recorded alongside the declaration.

### Requirement: Replay transfer weight SHALL be budgeted per origin

The replay origin SHALL have a stated transfer budget, measured in a real browser during the acceptance
run. Exceeding it SHALL fail acceptance, naming the origin and the overage.

#### Scenario: The budget is measured in a browser, not estimated
- **WHEN** the acceptance run loads the public surface with replay granted
- **THEN** transferred bytes are measured per origin against the stated budget
- **AND** an overage fails acceptance with the origin and the number named.
