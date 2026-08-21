# Source-Bound Editing — Spec (P37)

Product rationale: [`../../../../../docs/prd/P37-source-bound-editors.md`](../../../../../docs/prd/P37-source-bound-editors.md) §6.1, §6.2.
Design reasoning: [`../../design.md`](../../design.md) D1, D6, D7.

## ADDED Requirements

### Requirement: An axis surface SHALL be bound to one node from the reader's own imported source

The subject is a `(workflow, node)` pair drawn from the IR the platform derived from the reader's
connected repository. The surface renders that node's current value for its axis.

#### Scenario: The surface names its subject
- **WHEN** a reader opens an axis surface with a connected repository
- **THEN** the surface names the workflow and the node it is bound to
- **AND** it renders that node's current value for that axis, resolved from the IR

#### Scenario: The subject persists across axis surfaces
- **WHEN** a reader moves from one axis surface to another
- **THEN** the same subject remains selected
- **AND** the subject is displayed on each surface

#### Scenario: A single candidate is named, not silently assumed
- **WHEN** the reader's connected source yields exactly one candidate node
- **THEN** it is selected without asking
- **AND** its name is still displayed, because being told which node was chosen is not the same as being defaulted into it

#### Scenario: An ambiguous subject asks once
- **WHEN** more than one candidate node exists and none has been selected
- **THEN** the reader is asked once, in the shell
- **AND** the answer applies to every axis surface without being asked again

### Requirement: An axis surface SHALL NOT render a fixture in the position the reader's own data occupies

#### Scenario: No repository connected
- **WHEN** a reader opens an axis surface with no connected repository
- **THEN** the surface renders `not_connected`, names the missing input, and links to the connection flow
- **AND** the reader's data position contains no sample node, fixture value or demonstration diff

#### Scenario: Not-connected is a business state, not a transport failure
- **WHEN** the surface reports `not_connected`
- **THEN** it is delivered as a 200 carrying that state
- **AND** it is distinguishable from `not-mounted`, `read-failed` and `not-reported`

#### Scenario: A worked example is labelled and lives elsewhere
- **WHEN** a worked example is rendered anywhere in the console
- **THEN** it is on the reading surface and labelled as the platform's fixture
- **AND** it is never presented as the reader's own node

### Requirement: Every axis SHALL be edited through the shared editor kit and never as free text

#### Scenario: The picker binds to the axis vocabulary
- **WHEN** a reader edits an axis
- **THEN** the choices are that axis's closed vocabulary at its recorded set version
- **AND** no free-text field accepts a value belonging to that vocabulary

#### Scenario: The params form is derived from the schema
- **WHEN** a reader selects an entry that takes parameters
- **THEN** the form's fields are derived from that entry's declared params schema
- **AND** a parameter failing the schema is refused at save, naming the entry and the parameter

#### Scenario: An unavailable option is shown, not hidden
- **WHEN** an option requires a service this deployment does not supply
- **THEN** it is rendered, disabled, naming the service it needs
- **AND** it is not omitted from the list

#### Scenario: Preflight shows the effect before the save
- **WHEN** a reader composes a change
- **THEN** the resulting `config_hash` and the diff against the parent variant are shown before saving
- **AND** both are computed server-side and rendered as received

#### Scenario: A saved change is unverified until measured
- **WHEN** a change is saved
- **THEN** it is stamped `unverified` until the harness has run against it
- **AND** it is not described as an improvement

### Requirement: A node's axis state SHALL be rendered per node and SHALL NOT be averaged

#### Scenario: One uncovered node among many
- **WHEN** a reader's workflow contains one node with no policy for an axis and several with one
- **THEN** the uncovered node's state is visible
- **AND** no aggregate percentage is rendered in place of the per-node states

#### Scenario: The state vocabulary is shared
- **WHEN** any axis surface renders a node's state
- **THEN** it is exactly one of `measured`, `observed`, `not_measured`, `refused`
- **AND** no surface introduces a state word of its own

### Requirement: An unresolvable axis value SHALL be reported as not measured and SHALL NOT fall back to a default

#### Scenario: The node's policy cannot be resolved
- **WHEN** the platform cannot resolve a node's current value for an axis
- **THEN** the surface renders `not_measured`, naming the missing input
- **AND** it does not render the vocabulary's default as though it were the node's value
- **AND** a WARN is recorded carrying `request_id`, `trace_id` and `span_id`

### Requirement: A saved axis change SHALL be verifiable in the store, not inferred from the response

#### Scenario: A save is proved by a read
- **WHEN** a reader saves an axis change and the request returns success
- **THEN** a registry entry and a variant exist for that change
- **AND** the surface renders the `config_hash` those rows produce
