# Operator Oversight & Health — Spec (folded from P26)

Product rationale: [`../../../docs/prd/P26-operator-console-refresh.md`](../../../docs/prd/P26-operator-console-refresh.md)
§6 (FR70–FR77). Technical decisions: [`../../changes/archive/2026-08-01-p26-operator-console-refresh/design.md`](../../changes/archive/2026-08-01-p26-operator-console-refresh/design.md) D7, D9.

Covers the three remaining oversight questions an operator cannot answer today — which factor authenticated
a session, which tenants owe re-acceptance of which document, and whether observability reporting is
actually working — plus the interface floor every new surface in this phase inherits.

> The asymmetry: **unknown is an answer, and a guess is not.** A per-tenant deployed version that is not
> derivable renders as *unknown*, because an inferred version rendered as a version is a wrong number that
> gets acted on during an incident — which is exactly when it will be read.

## Requirements

### Requirement: An operator session SHALL show which factor authenticated it, and when

The surface SHALL show, per operator session, the authenticating factor and the time it was verified, so a
reviewer reads authentication strength rather than inferring it.

#### Scenario: A reviewer reads the factor rather than assuming it
- **WHEN** a superadmin reviews operator sessions
- **THEN** each session shows the factor that authenticated it and when it was verified
- **AND** a single-factor session is distinguishable from a multi-factor one.

#### Scenario: The surface claims no more than the identity layer provides
- **WHEN** the identity provider in use is the test-mode fixture rather than a production provider
- **THEN** the surface renders the factor the verifier recorded
- **AND** it does not represent the fixture as a production identity provider.

### Requirement: The console SHALL show, per tenant, which legal document versions are accepted and which are owed

The surface SHALL show accepted versions and versions owed after a material publication, each linking to the
archived text carrying the accepted content hash.

#### Scenario: An operator answers a re-acceptance question
- **WHEN** a material document version has been published and an operator opens the tenant's acceptance
  state
- **THEN** the accepted versions and the owed versions are shown
- **AND** each accepted entry links to the archived text at its content hash.

#### Scenario: A non-material version creates no obligation
- **WHEN** a version declared non-material has been published
- **THEN** no tenant is shown as owing re-acceptance for it.

### Requirement: Each observability integration SHALL be shown as one of three states, read from the platform's own readiness surface

The console SHALL show `absent`, `configured` or `degraded` per integration, read from the platform's
readiness surface. It SHALL NOT render a boolean, and SHALL NOT read the state from a third party's
dashboard.

#### Scenario: Absent and degraded are distinguishable on the surface
- **WHEN** one integration is unconfigured and another is configured but unreachable
- **THEN** the first shows `absent` and the second shows `degraded`
- **AND** the `degraded` entry names its failure class.

#### Scenario: The third party is not the health signal
- **WHEN** an operator asks whether reporting is working
- **THEN** the answer is rendered from the platform's own readiness surface
- **AND** it does not require the third party's dashboard, which is the least available part of the system
  during an incident.

### Requirement: A per-tenant deployment shape and version SHALL be shown where derivable and as unknown where not

The surface SHALL show the deployment shape and version where an existing signal carries it, and SHALL
render an explicit *unknown* otherwise. No version SHALL be inferred, estimated, or derived from a proxy
signal.

#### Scenario: An unknown version is stated, not guessed
- **WHEN** no signal carries a tenant's release identifier
- **THEN** the surface shows *unknown*
- **AND** it does not display a version inferred from an API contract version, a feature probe, or any
  other proxy.

#### Scenario: The missing input is recorded rather than worked around
- **WHEN** the deployed version is not derivable
- **THEN** the surface ledger carries the corresponding row as not-yet-readable, naming the collection that
  would make it readable.

### Requirement: A capability not yet readable SHALL NOT be rendered as an empty working surface

Where a read depends on a subsystem that has not shipped, the surface SHALL state that plainly and SHALL
NOT render an empty state as though it were a zero or as though the subsystem were healthy.

#### Scenario: A pre-shipping subsystem is stated as such
- **WHEN** a surface depends on a subsystem that has not yet shipped
- **THEN** it states that the subsystem is not yet available
- **AND** it renders no count, no zero, and no empty table implying there is nothing to show.

### Requirement: Every new surface SHALL meet the console's existing interface floor

Each new surface SHALL use the single token set with no colour, spacing, type-size or radius literal;
English strings with `en-US` formatting through the one swap point; keyboard reachability with visible
focus; WCAG 2.1 AA contrast in both resolved themes; the viewport floor; and the payload ceiling. The
hazard palette SHALL remain reserved for hazard.

#### Scenario: A visual literal fails the build
- **WHEN** a colour, spacing, type-size or radius literal is introduced on a new surface
- **THEN** the token scan fails the build, naming the literal.

#### Scenario: Both themes meet contrast
- **WHEN** each new surface is audited in both resolved themes
- **THEN** every token pair meets WCAG 2.1 AA
- **AND** no information is carried by a hue present in only one theme.

#### Scenario: Hazard colour is not used for volume
- **WHEN** a new read surface renders a large count or a novel state
- **THEN** it does not use the hazard palette to do so
- **AND** hazard remains legible because it remains rare.

#### Scenario: The payload ceiling holds
- **WHEN** the operator console is built with the new surfaces
- **THEN** the shipped client bundle is under the stated ceiling
- **AND** an overage fails the build with the number named.

### Requirement: Failure classes SHALL stay distinguishable, and an empty result SHALL NOT render as a zero

Subsystem-not-mounted, not-found and transport failure SHALL be three outcomes with three messages. A 404
SHALL NOT be mapped to a business state, and an empty aggregate SHALL NOT be rendered as `0`.

#### Scenario: Three failures, three messages
- **WHEN** a subsystem is not mounted, a subject is not found, and a request fails in transport
- **THEN** each produces its own message
- **AND** none is presented as another.

#### Scenario: No records is not zero
- **WHEN** an aggregate has no records
- **THEN** the surface states that there are no records
- **AND** it does not render `0`, which a reader would take as a measured value.

### Requirement: Read models SHALL be computed server-side and rendered as received

The console SHALL NOT derive, recompute, re-rank or reformat a statistical claim, a coverage percentage, a
rollout outcome or a state classification.

#### Scenario: The browser derives nothing
- **WHEN** a figure, coverage percentage, state or outcome is rendered
- **THEN** it is rendered as received from the server
- **AND** no client-side computation produces a second value for it.

#### Scenario: The BFF stays a pass-through
- **WHEN** a read passes through the console's server layer
- **THEN** it is not merged, re-ranked, reformatted or status-translated
- **AND** no aggregation is performed there.
