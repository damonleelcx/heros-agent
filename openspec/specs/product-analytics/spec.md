# Product Analytics — Spec (folded from P24)

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR14–FR20), §9.5 (AI Engineer lens), §9.8 (Sales lens). Technical decisions:
[`../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md`](../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md) D3, D5, D8.

Covers interface-usage measurement, and the two boundaries that keep it from becoming something else: a
tenant page never gets a browser tag, and no figure that originates here may become a business number.

> The asymmetry: **a URL is not a permissible event field.** A path under `/app` carries variant, run,
> node and tenant identifiers, so the surface is named by an id from a closed enum — and the complete set
> of reportable surfaces is therefore something a reviewer can read, which is never true of a URL.

## Requirements

### Requirement: An analytics browser tag SHALL load only on the public surface, and only under a granted category

A browser analytics tag SHALL be loaded only on routes that require no session, read no tenant data and
make no upstream platform call, and only after a `product_analytics` grant.

#### Scenario: The public surface loads a tag after a grant
- **WHEN** a visitor grants `product_analytics` on the public surface
- **THEN** the analytics tag loads from an allowlisted origin
- **AND** it is injected with the response's per-request nonce.

#### Scenario: No grant, no tag
- **WHEN** a visitor has not granted `product_analytics`
- **THEN** no analytics tag is loaded on any surface
- **AND** no request to an analytics origin is made.

### Requirement: No analytics browser tag SHALL be loaded on a tenant surface, a data route, or the operator console

Routes under the tenant prefix, routes under the BFF data prefix, and every operator-console route SHALL
carry no browser analytics tag. Usage on those surfaces SHALL be emitted server-side.

#### Scenario: A tenant page contacts only its own origin
- **WHEN** a customer loads any tenant-prefixed route and its network traffic is inspected in a real
  browser
- **THEN** every request targets the console's own origin, except error reporting to the origin named in
  that prefix's policy
- **AND** no analytics origin is contacted.

#### Scenario: The operator console carries no analytics tag
- **WHEN** any operator-console route is loaded
- **THEN** no analytics tag is present
- **AND** the operator console's policy names no analytics origin.

### Requirement: A server-emitted analytics event's payload SHALL be constructed from an allowlist

The payload SHALL be built field by field from a named, checked-in list carrying a one-line justification
per field. A field added to an internal representation SHALL be absent from a transmitted event by
default. The transmitted key set SHALL be asserted to be a subset of the allowlist, and every allowlist
entry SHALL be asserted to be populated by something.

#### Scenario: A new internal field does not reach the wire
- **WHEN** a field is added to the internal representation an analytics event is derived from, without
  being added to the allowlist
- **THEN** the transmitted event does not contain it
- **AND** the omission is visible as a missing feature rather than discovered externally.

#### Scenario: The allowlist is asserted in both directions
- **WHEN** the allowlist assertion runs
- **THEN** no transmitted key falls outside the allowlist
- **AND** no allowlist entry exists that nothing populates.

### Requirement: A surface SHALL be identified by an id from a closed enum, never by its URL

An event SHALL NOT carry a path, a query string, a fragment, a referrer beyond first-party, or any free
text. The surface identifier SHALL be a value from a closed enum.

#### Scenario: A path cannot be carried
- **WHEN** an attempt is made to emit an analytics event carrying a request path
- **THEN** the event is refused at construction
- **AND** the refusal names the field.

#### Scenario: The reportable set is enumerable
- **WHEN** a reviewer asks which surfaces can appear in an analytics event
- **THEN** the complete answer is the closed enum
- **AND** no runtime value outside it can appear.

### Requirement: Event names SHALL come from a central enum

Analytics event names SHALL be enum values alongside the existing `event.name` convention. An ad-hoc
event name SHALL fail the build.

#### Scenario: An ad-hoc name fails the build
- **WHEN** an event is emitted with a string literal name not present in the central enum
- **THEN** the build fails, naming the literal and its location.

### Requirement: No analytics figure SHALL become a business number

A figure originating in the analytics integration SHALL NOT be rendered on a customer-facing surface,
used to derive an invoiced quantity, or presented as a platform metric. The boundary SHALL be asserted,
not documented.

#### Scenario: A customer-facing surface renders no analytics figure
- **WHEN** every customer-facing surface is audited
- **THEN** no rendered figure derives from the analytics integration
- **AND** the assertion that establishes this fails if such a figure is introduced.

#### Scenario: Metering is unaffected
- **WHEN** an invoiced quantity is derived
- **THEN** it derives from linked runs on the telemetry substrate
- **AND** no analytics figure contributes to it, and none is used to infer or extrapolate it.

#### Scenario: Frequency questions are answered from the substrate
- **WHEN** an operator asks how often a surface is used or how often a failure occurs
- **THEN** the authoritative answer comes from the telemetry substrate, where events are complete
- **AND** the sampled, consent-gated, ad-blockable analytics figure is not cited as the answer.

### Requirement: No cross-site or advertising identifier SHALL be configured

Cross-site identifiers, advertising identifiers, remarketing audiences and conversion pixels SHALL NOT
be configured. IP anonymisation SHALL be enabled and ad-personalisation signals disabled.

#### Scenario: The configuration is asserted, not documented
- **WHEN** the analytics configuration is inspected by its assertion
- **THEN** IP anonymisation is on and ad-personalisation signals are off
- **AND** no advertising identifier, remarketing audience or conversion pixel is present.

#### Scenario: No advertising origin is reachable
- **WHEN** the origin allowlist is inspected
- **THEN** it contains no advertising, remarketing or conversion-tracking origin
- **AND** adding one would require an allowlist entry, which is a reviewable change.
