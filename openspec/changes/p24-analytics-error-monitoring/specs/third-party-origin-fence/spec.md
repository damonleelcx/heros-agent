# Third-Party Origin Fence — Spec Delta (P24)

Product rationale: [`../../../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR34–FR39), §7 (NFR3–NFR4), §9.6 (DevOps lens). Technical decisions:
[`../../design.md`](../../design.md) D4, D9, D11.

Covers the obligation this phase owes the codebase. A phase that relaxes a guard has to leave a stronger
one behind, or it is just a relaxation.

> Three properties make this a fence rather than a preference. **The exception is per prefix**, so the
> tenant and operator surfaces gain a specific assertion where they previously had only an incidental
> one. **The allowlist is data read by both consoles**, so an origin cannot be added by editing
> middleware. And **the weight budget covers what the existing scanner cannot see** — third-party bytes
> arrive from another host and never enter the build output, so without this the payload ceiling would
> mean less after this phase than before it.

## ADDED Requirements

### Requirement: The content-security policy SHALL be split by route prefix, and the non-public prefixes SHALL name no third-party origin except the reporting origin

The tenant prefix, the BFF data prefix and every operator-console route SHALL retain `default-src 'self'`
and SHALL name no third-party origin other than the error-reporting origin under the connect directive.
The public prefix SHALL name only origins present on the checked-in allowlist.

#### Scenario: The tenant prefix is asserted specifically
- **WHEN** a response on a tenant-prefixed route is inspected
- **THEN** its policy contains `default-src 'self'`
- **AND** the only third-party origin it names is the error-reporting origin under the connect directive
- **AND** the assertion that establishes this names the prefix rather than testing the application globally.

#### Scenario: The operator console is asserted the same way
- **WHEN** a response on any operator-console route is inspected
- **THEN** its policy names no analytics and no session-replay origin.

#### Scenario: The public prefix is bounded by the allowlist
- **WHEN** a response on a public route is inspected
- **THEN** every third-party origin it names appears on the allowlist
- **AND** no origin appears that the allowlist does not carry.

### Requirement: The policy SHALL remain nonce-based and strict on every prefix

Every prefix SHALL carry a per-request nonce, `'strict-dynamic'`, no `'unsafe-inline'` for scripts, and no
`'unsafe-eval'` in a production build. An integration requiring either relaxation SHALL be refused rather
than accommodated.

#### Scenario: The shipped policy allows neither relaxation
- **WHEN** a production response on any prefix is inspected
- **THEN** its script directive contains a nonce and `'strict-dynamic'`
- **AND** it contains neither `'unsafe-inline'` nor `'unsafe-eval'`.

#### Scenario: Third-party scripts are reached through strict-dynamic, not listed
- **WHEN** the public prefix's script directive is inspected
- **THEN** it names no third-party host
- **AND** an allowlisted script is reached from the nonced loader.

#### Scenario: A relaxation-requiring integration is refused
- **WHEN** an integration cannot function without inline script execution
- **THEN** it is not installed
- **AND** the refusal is recorded rather than the policy being widened.

### Requirement: The origin allowlist SHALL be a single checked-in artefact, and a hard-coded origin SHALL fail the build

The artefact SHALL name each origin, the integration that requires it, the consent category that gates
it, the policy directive it appears under, and its transfer budget. Both consoles' middleware SHALL
construct their policy from it. An origin literal in middleware or in a Next configuration SHALL fail the
build.

#### Scenario: The header is constructed from data
- **WHEN** an origin is added to the artefact
- **THEN** both consoles' policies reflect it with no middleware change.

#### Scenario: A hard-coded origin fails the build
- **WHEN** an origin literal is written into either console's middleware or Next configuration
- **THEN** the build fails, naming the literal and its file.

#### Scenario: The two consoles cannot drift
- **WHEN** the drift check runs
- **THEN** the two consoles' derived policies agree on every shared prefix rule
- **AND** a divergence fails the check, naming the rule and both values.

### Requirement: The allowlist SHALL be asserted in both directions

A surface SHALL NOT load an origin the allowlist does not carry, and the allowlist SHALL NOT carry an
origin no surface loads.

#### Scenario: An unlisted origin is refused
- **WHEN** a page attempts to load a resource from an origin absent from the allowlist
- **THEN** the browser refuses it under the prefix's policy
- **AND** the refusal is visible in the browser's error log.

#### Scenario: A stale entry is a failure
- **WHEN** an allowlist entry names an origin that no surface loads under any consent state
- **THEN** the assertion fails, naming the entry
- **AND** the entry is removed rather than retained as a dormant permission.

### Requirement: Third-party transfer weight SHALL be budgeted per origin and measured in a real browser

Each allowlisted origin SHALL carry a stated transfer budget. The measurement SHALL be taken in a real
browser during the acceptance run. Exceeding a budget SHALL fail acceptance, naming the origin and the
overage. The budget SHALL be per origin rather than a shared total.

#### Scenario: The existing first-party ceiling cannot see this weight
- **WHEN** the first-party bundle scan runs
- **THEN** it measures only the build's own output
- **AND** the third-party measurement is taken separately, in a browser, so the ceiling's meaning is not
  quietly reduced by this phase.

#### Scenario: An overage fails with a number
- **WHEN** an allowlisted origin transfers more than its budget on the acceptance run
- **THEN** acceptance fails
- **AND** the failure names the origin, the budget and the overage.

#### Scenario: One integration cannot absorb another's headroom
- **WHEN** one origin grows while another shrinks such that the total is unchanged
- **THEN** the grown origin's budget failure is still raised
- **AND** the total is not used to excuse it.

### Requirement: An analytics, replay or error-reporting runtime in a tenant- or operator-reachable client chunk SHALL fail the build

The existing decorative-runtime scan SHALL gain the inverse rule for observability runtimes: presence in
a client chunk reachable from a tenant or operator route SHALL fail the build, naming the runtime and the
chunk.

#### Scenario: An accidental import is caught by the build
- **WHEN** an analytics, session-replay or error-reporting package is imported into a module reachable
  from a tenant route and the console is built
- **THEN** the build fails, naming the runtime and the chunk.

#### Scenario: The public surface is not caught by this rule
- **WHEN** a permitted runtime appears in a chunk reachable only from the public prefix
- **THEN** the build passes
- **AND** the runtime is subject to its origin's transfer budget instead.

### Requirement: Every fence in this capability SHALL be demonstrated red

Each assertion SHALL be validated by a deliberate violation that makes it fail. An assertion never
observed failing SHALL NOT be accepted as a fence.

#### Scenario: Each fence is validated by a violation
- **WHEN** the fence validation runs
- **THEN** a hard-coded origin, a runtime in a tenant chunk, an unlisted origin loaded at runtime, a
  stale allowlist entry and a budget overage each produce a failure
- **AND** each failure names the requirement it defends.
