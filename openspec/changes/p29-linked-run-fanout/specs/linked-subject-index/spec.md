# Linked Subject Index — Delta (P29)

Product rationale: [`../../../../docs/prd/P29-linked-run-fanout.md`](../../../../docs/prd/P29-linked-run-fanout.md)
§6 (FR21–FR30). Design reasoning: [`../../design.md`](../../design.md) D3, D5.

The capability that answers *"what do I have?"* — the question the customer console has never been able
to ask, which is why every subject picker offers only what the current browser session already opened.

## ADDED Requirements

### Requirement: The platform SHALL enumerate the subjects it holds for the authenticated organization

The platform SHALL list, for the authenticated organization, the workflows, runs, variants and
transforms it holds records for.

#### Scenario: A subject appears because a record exists

- **WHEN** an organization has linked a run and transmitted a structure
- **THEN** the workflow appears in the workflow enumeration
- **AND** the run appears in the run enumeration
- **AND** neither appearance depends on any browser session having opened it.

#### Scenario: A transform appears when its receipt exists

- **WHEN** an organization has transmitted a transform receipt
- **THEN** the configuration and revision pair appears in the transform enumeration
- **AND** selecting it resolves to the transform's own view.

#### Scenario: A variant is the configuration that was measured

- **WHEN** an organization has linked runs under two configurations
- **THEN** the variant enumeration lists exactly those two
- **AND** each carries the workflow it was measured on.

### Requirement: Enumeration SHALL be scoped to the authenticated principal and SHALL NOT be an existence oracle

Every enumeration SHALL be derived from the authenticated principal's organization. A subject belonging
to another organization SHALL be absent from the list, and a request for it by identifier SHALL answer
exactly as a request for a subject that does not exist.

#### Scenario: Another organization's subject is absent

- **WHEN** two organizations hold records
- **THEN** each enumeration contains only its own organization's subjects.

#### Scenario: Naming another organization's subject reveals nothing

- **WHEN** a caller requests a subject belonging to another organization by its identifier
- **THEN** the response is identical to the response for an identifier that names nothing
- **AND** the caller cannot distinguish the two.

#### Scenario: Scope never comes from the request

- **WHEN** a request carries an organization identifier in its body or query
- **THEN** it is ignored
- **AND** the scope is the authenticated principal's organization.

### Requirement: The run enumeration SHALL contain executed and linked runs in one list, labelled by origin

Runs the platform executed and runs a customer linked SHALL be listed together, each carrying which of
the two it is.

#### Scenario: A run linked minutes ago is in the list

- **WHEN** a run is linked and the run enumeration is read
- **THEN** that run is in the list
- **AND** it is labelled as linked.

#### Scenario: The two origins are distinguishable

- **WHEN** the run enumeration contains both kinds
- **THEN** each row states its origin
- **AND** a reader can tell which runs the platform performed.

#### Scenario: A linked run's row carries only what a linked run has

- **WHEN** a linked run is listed
- **THEN** its row carries its scores with intervals, its cost, its latency, its tool version and when it
  was linked
- **AND** it carries no per-node input or output, no attempt group, and no executor status.

### Requirement: An empty enumeration SHALL be distinguishable from an unknown one and from an unmounted one

Each enumeration SHALL distinguish "this organization has none", "the platform could not read them" and
"this capability is not served in this deployment".

#### Scenario: Three outcomes, three responses

- **WHEN** an enumeration is requested and the organization holds no records
- **THEN** the response says so
- **AND** it differs from the response given when the read fails
- **AND** both differ from the response given when the capability is not mounted.

#### Scenario: A read failure is never an empty list

- **WHEN** the underlying store cannot be read
- **THEN** the response is not an empty list.

### Requirement: A run predating ownership recording SHALL be reported rather than silently omitted

Where records exist that carry no owning organization, their count SHALL be reported alongside every
enumeration that would otherwise be silently partial.

#### Scenario: The count is reported whether or not the list is empty

- **WHEN** an organization has both listed runs and unowned runs exist
- **THEN** the count of unowned runs is reported
- **AND** it is reported for a non-empty list as well as an empty one.

### Requirement: The console SHALL offer selection from the enumeration, and session memory SHALL be an ordering hint only

A subject picker SHALL be populated from the platform's enumeration. The list of subjects a session has
previously opened SHALL be used only to order that enumeration.

#### Scenario: A fresh session can select

- **WHEN** a user signs in on a machine that has never opened any subject
- **THEN** every picker offers this organization's subjects
- **AND** no picker requires an identifier to be typed.

#### Scenario: Session memory never adds a subject

- **WHEN** a session has previously opened a subject the enumeration no longer contains
- **THEN** that subject is not offered
- **AND** the session's memory of it is discarded rather than rendered.

#### Scenario: Hand entry survives

- **WHEN** a user holds an identifier from elsewhere
- **THEN** it can still be entered directly
- **AND** it resolves to the same canonical route.
