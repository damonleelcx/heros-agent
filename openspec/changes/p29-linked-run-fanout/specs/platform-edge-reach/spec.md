# Platform Edge Reach — Delta (P29)

Product rationale: [`../../../../docs/prd/P29-linked-run-fanout.md`](../../../../docs/prd/P29-linked-run-fanout.md)
§6 (FR1–FR8). Design reasoning: [`../../design.md`](../../design.md) D1.

The capability that answers one question: **can the command a customer types actually reach the
platform?** It exists because the answer was no for three commands, behind a green build, a healthy
deployment and a fence written specifically to prevent it.

## ADDED Requirements

### Requirement: Every platform path the CLI addresses SHALL be reachable from the internet on the customer hostname

A path addressed by the code that speaks to the platform from outside the cluster SHALL be published in
the deployed customer-hostname ingress manifest and SHALL be declared public in the route
classification. This SHALL hold for every such path without exception, including a path that carries
caller-supplied identifiers.

#### Scenario: A parameterised path is not exempt

- **WHEN** the CLI addresses a path built from a fixed head and a caller-supplied segment
- **THEN** the reach check treats it exactly as it treats a fixed path
- **AND** an unpublished such path fails the check
- **AND** the failure names the path and the remedy.

#### Scenario: An unpublished path fails before it reaches a customer

- **WHEN** a path the CLI addresses is absent from the ingress manifest
- **THEN** the check fails
- **AND** the message states that the command answers 404 in production once that manifest is applied.

#### Scenario: The check cannot pass vacuously

- **WHEN** the scan that derives the CLI-addressed path set finds fewer paths than the transport
  actually addresses
- **THEN** the check fails on that ground alone
- **AND** it does not report success for having found nothing to object to.

### Requirement: A published platform path SHALL be matched exactly, never by prefix

Every ingress rule routing to the platform SHALL match one path exactly. A rule matching a path prefix
SHALL NOT be used to publish a platform path.

#### Scenario: A prefix rule is refused

- **WHEN** a platform path is published with prefix matching
- **THEN** the check fails
- **AND** the message states that a prefix rule publishes every route beneath it.

#### Scenario: The routes beneath a candidate prefix are named

- **WHEN** a prefix rule would be required to publish a path
- **THEN** the check names the other registered routes that rule would also publish
- **AND** those routes are not published.

### Requirement: A machine-addressed platform route SHALL carry caller-supplied identifiers in its payload, not in its path

A route addressed by the CLI SHALL have a fixed path. Identifiers the caller supplies — a workflow
identity, a source revision, a proposal identity — SHALL travel in the request body.

#### Scenario: The four machine routes have fixed paths

- **WHEN** the CLI transmits a workflow structure, a source snapshot, a verification verdict or a
  transform receipt
- **THEN** each is addressed at a fixed path
- **AND** each is published with exact matching
- **AND** the identifiers it names appear in its payload.

#### Scenario: A duplicated identifier does not disagree with itself

- **WHEN** a payload carries an identifier that a previous contract version also carried in the path
- **THEN** exactly one of them is authoritative and it is the payload
- **AND** a request whose path and payload disagree is refused rather than resolved by precedence.

### Requirement: Both directions of the reach contract SHALL be checked

A path published in the ingress manifest SHALL be declared public in the route classification, and a
path declared public SHALL be registered by the platform.

#### Scenario: An accidental exposure fails the check

- **WHEN** the manifest publishes a path that the classification does not declare public
- **THEN** the check fails
- **AND** the message states that nothing breaks when this is wrong, which is why it is checked.

#### Scenario: A stale classification fails the check

- **WHEN** the classification names a route the platform no longer registers
- **THEN** the check fails and names the stale entry.

### Requirement: The previous parameterised routes SHALL remain served and SHALL remain unpublished

During the transition release the parameterised routes SHALL continue to be registered and SHALL be
classified as reachable only from inside the cluster.

#### Scenario: An older client inside the cluster still works

- **WHEN** a caller inside the cluster addresses a parameterised route
- **THEN** it is served exactly as before.

#### Scenario: A parameterised route is never published

- **WHEN** the ingress manifest is checked
- **THEN** no parameterised platform route appears in it.

### Requirement: A client SHALL be able to distinguish an unreachable path from a refusal

When a transmission fails, the CLI SHALL report whether the platform refused the request or the path was
not reachable, and SHALL name the next action.

#### Scenario: A 404 at the edge is not reported as a platform refusal

- **WHEN** a transmission receives a response that did not come from the platform's own handler
- **THEN** the CLI reports that the path is not reachable at the configured endpoint
- **AND** it does not report the transmission as rejected by the platform.

#### Scenario: The message names one next action

- **WHEN** any transmission fails
- **THEN** the message names exactly one next action for the reader.
