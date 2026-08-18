# Axis Node Projection — Spec (folded from P29)

Product rationale: [`../../../docs/prd/P29-linked-run-fanout.md`](../../../docs/prd/P29-linked-run-fanout.md)
§6 (FR31–FR45). Design reasoning: [`../../changes/archive/2026-08-07-p29-linked-run-fanout/design.md`](../../changes/archive/2026-08-07-p29-linked-run-fanout/design.md) D2, D3, D7.

The join nobody had made. Coverage, the delivery route table, the memory vocabulary and the harness
boundary are total build facts and all correct; the customer's nodes arrive in the opt-in structure
payload. This capability multiplies the two and states, on every axis surface, *what applies to
**your** nodes* — while refusing, structurally, to invent the half it was not told.

## Requirements

### Requirement: Each axis surface SHALL render a projection of its coverage onto the organization's reported nodes

For every optimization axis with a console surface, the platform SHALL provide a read model pairing that
axis's coverage rows with the nodes this organization has reported, and the surface SHALL render it.

#### Scenario: An axis surface shows this organization's counts

- **WHEN** an organization has reported a workflow's structure and opens an axis surface
- **THEN** the surface shows how many of that workflow's nodes the axis applies to and how many it
  refuses
- **AND** each refusal is grouped by its cause.

#### Scenario: Every axis with a surface is covered

- **WHEN** the set of axis surfaces is enumerated
- **THEN** each of them renders a projection
- **AND** no axis surface is left showing only build facts.

#### Scenario: The node list is reachable from the count

- **WHEN** a count is displayed
- **THEN** the nodes it counts can be listed
- **AND** each node is identified by its symbol, file and line span.

### Requirement: A projected cell SHALL carry exactly one of four states

A node's cell for an axis SHALL be `applies`, `refused` with a named cause, `not-applicable`, or
`not-reported`. These SHALL be four distinguishable states.

#### Scenario: Not reported is its own state

- **WHEN** a node exists in a reported structure but carries no verdict for an axis
- **THEN** its cell is `not-reported`
- **AND** it is visually and semantically distinct from the other three.

#### Scenario: Not applicable is never rendered from an absence

- **WHEN** any input to a cell is missing
- **THEN** the cell is not rendered as `not-applicable`
- **AND** `not-applicable` is rendered only where the axis's own table states it.

#### Scenario: A refusal names its cause and its owner

- **WHEN** a cell is `refused`
- **THEN** it carries the cause's stable identifier
- **AND** the surface renders whose move it is from that identifier, never from prose.

### Requirement: The platform SHALL NOT compute a node's verdict

The platform SHALL render only verdicts it received. It SHALL NOT derive a node's verdict from the
node's language, form, model, policy or any other property it holds.

#### Scenario: A language-covered node with no verdict is not claimed to apply

- **WHEN** a node's language and form are covered by the axis's table and no verdict was transmitted for
  that node
- **THEN** the cell is `not-reported`
- **AND** it is not `applies`.

#### Scenario: The prohibition is enforced, not merely observed

- **WHEN** the projection code is inspected
- **THEN** no path produces a verdict from platform-held properties
- **AND** a check fails if such a path is added.

#### Scenario: A not-reported cell names the command that would report it

- **WHEN** a cell is `not-reported`
- **THEN** the surface names the one command or option that would produce a verdict for it.

### Requirement: A projection SHALL state the coverage table version it was computed against, and a mismatch SHALL be labelled stale

The stored coverage table version SHALL be compared with the version the running build uses. Where they
differ, the projection SHALL be labelled stale.

#### Scenario: A stale projection is labelled and excluded

- **WHEN** the stored version differs from the running build's
- **THEN** the projection is labelled stale
- **AND** its counts are still shown
- **AND** they are excluded from every aggregate total.

#### Scenario: An absent version is stale, not current

- **WHEN** a stored structure carries no coverage table version
- **THEN** its projection is treated as stale
- **AND** it is not assumed to match the running build.

#### Scenario: Both versions are displayed

- **WHEN** a projection is stale
- **THEN** both the stored version and the running build's version are shown.

### Requirement: Every projected count SHALL state its denominator

A count of nodes SHALL be accompanied by how many nodes it is out of, and by how many of the workflow's
nodes were reported at all.

#### Scenario: Partial reporting is visible

- **WHEN** a workflow has more nodes than the organization has reported verdicts for
- **THEN** the surface states both numbers
- **AND** a reader can tell the projection is over a subset.

#### Scenario: A percentage is never shown without its denominator

- **WHEN** any proportion is displayed
- **THEN** the counts it was computed from are displayed beside it.

### Requirement: The delivery route table SHALL be projected the same way

The two delivery routes SHALL be evaluated per reported node, and a node refused by both routes SHALL be
counted as undeliverable.

#### Scenario: Undeliverable nodes are counted and listed

- **WHEN** an organization's reported nodes are projected against the delivery route table
- **THEN** the number refused by both routes is shown
- **AND** those nodes can be listed.

#### Scenario: Undeliverable has no hopeful synonym

- **WHEN** a node is undeliverable
- **THEN** it is not rendered as pending, queued or awaiting
- **AND** a permanently refused cause is rendered differently from an unbuilt one.

### Requirement: The worked examples on each axis surface SHALL be retained

The verbatim engine examples already rendered on the axis surfaces SHALL remain, and the projection
SHALL be added beside them under its own heading.

#### Scenario: Nothing is removed

- **WHEN** an axis surface is compared before and after this change
- **THEN** every panel present before is still present
- **AND** the projection is an addition.

#### Scenario: Live data and worked examples are distinguishable

- **WHEN** an axis surface renders both
- **THEN** each states which it is
- **AND** a reader can tell the example from their own data without inspecting values.

### Requirement: The projection SHALL read one coverage source, in both directions

The projection SHALL read the same coverage source the transform engine refuses from, the CLI reports,
and the coverage surface renders.

#### Scenario: A surface may not offer a cell the engine refuses

- **WHEN** the projection offers an axis for a node
- **THEN** the engine's own table admits that axis for that node's language and form.

#### Scenario: The engine may not apply a cell no surface offers

- **WHEN** the engine materialises a change for a cell
- **THEN** the projection has a row for that cell.
