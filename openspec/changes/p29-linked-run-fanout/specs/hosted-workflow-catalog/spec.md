# Hosted Workflow Catalog — Delta (P29)

Product rationale: [`../../../../docs/prd/P29-linked-run-fanout.md`](../../../../docs/prd/P29-linked-run-fanout.md)
§6 (FR46–FR55). Design reasoning: [`../../design.md`](../../design.md) D4, D7.

One structure store, read by everything that draws or lists a workflow. Before this change the studio
matrix read a process-local catalogue that only a demo binary ever filled, while the graph read the
linked structures — so two surfaces about the same workflow disagreed about whether it existed.

## ADDED Requirements

### Requirement: Every surface that renders a workflow's nodes SHALL read the reported structure store

The node list, the studio matrix, the workflow graph, the eval board and the scorecard SHALL derive a
workflow's nodes from the structures the organization reported.

#### Scenario: One workflow, one answer

- **WHEN** an organization has reported a workflow's structure
- **THEN** every surface that names that workflow's nodes names the same nodes
- **AND** no surface reports the workflow as absent while another renders it.

#### Scenario: A process-local catalogue is not a source

- **WHEN** the deployment is inspected
- **THEN** no console-facing surface derives a workflow from a catalogue loaded at process start
- **AND** a demo fixture cannot become a tenant's data.

### Requirement: The studio matrix SHALL render columns from the organization's reported nodes

The matrix's columns SHALL be this organization's nodes for the selected workflow, and its rows SHALL be
the model registry.

#### Scenario: The matrix opens on a reported workflow

- **WHEN** a workflow whose structure was reported is opened in the studio
- **THEN** the matrix renders one column per reported node
- **AND** each column carries the node's symbol and the model that node currently calls.

#### Scenario: An unreported workflow says so

- **WHEN** a workflow with no reported structure is opened
- **THEN** the surface states that no structure has been reported for it
- **AND** it names the command that would report one
- **AND** this is distinct from the response given for a workflow that does not exist.

### Requirement: A hosted action requiring a provider credential SHALL be refused by name

Any matrix action that would call a provider on the organization's behalf SHALL be refused with a stated
cause: the platform holds no customer provider credential.

#### Scenario: A test run is refused, not hidden

- **WHEN** a reader activates a cell action that would call a provider
- **THEN** the refusal states that the platform holds no provider credential for this organization
- **AND** it names the local command that performs the same exploration.

#### Scenario: The refusal is not a plan boundary

- **WHEN** the refusal is rendered
- **THEN** it does not suggest that any plan, role or setting would remove it.

### Requirement: A binding authored in the hosted matrix SHALL travel the existing authoring spine

A model or prompt selection made in the hosted matrix SHALL be submitted through the same preflight,
resolve, gate and transform path an operator candidate travels.

#### Scenario: No second apply path

- **WHEN** a binding is authored in the console
- **THEN** it is preflighted, resolved and gated by the same components an operator candidate uses
- **AND** no authoring-only resolve, transform or gate exists.

#### Scenario: A refusal binds the console identically

- **WHEN** a change the transform refuses is authored in the console
- **THEN** it is refused with the same typed cause
- **AND** no plan, role, entitlement or request parameter materialises it.

#### Scenario: An authored change is stamped unverified

- **WHEN** a change is authored and applied without a verdict
- **THEN** it is recorded as unverified
- **AND** it contributes zero to every aggregate improvement or savings figure.

### Requirement: The workflow graph SHALL state what it does and does not carry

The graph drawn from a reported structure SHALL state that it carries no pattern labels and that its
regions are unclassified.

#### Scenario: Unclassified is stated as data

- **WHEN** a graph is drawn from a reported structure
- **THEN** its regions are rendered as unclassified
- **AND** that state is carried in the read model rather than inferred from a missing field.

#### Scenario: A label is never guessed from a symbol

- **WHEN** a node carries a symbol name that resembles a known pattern
- **THEN** no pattern label is assigned to it.

### Requirement: A read failure SHALL be distinguishable from an unreported workflow

The structure read SHALL distinguish "this organization has reported no structure for this workflow"
from "the structure could not be read".

#### Scenario: An outage is not reported as an absence

- **WHEN** the structure store cannot be read
- **THEN** the surface reports a read failure
- **AND** it does not report that no structure has been reported.
