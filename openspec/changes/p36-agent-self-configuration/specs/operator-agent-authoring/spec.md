# Operator Agent Authoring — Delta (P36)

The axis editor grows from six axes over an implicit single node to **nine axes over N nodes**. The
binding discipline is unchanged and is the reason this is a `MODIFIED` rather than a rewrite: every axis is
still edited against its existing vocabulary, and a new axis that arrived as a text box would defeat the
requirement it was added under.

The definition's shape, its node list and its topology are in
[`../heros-agent-definition/spec.md`](../heros-agent-definition/spec.md); its graph semantics are in
[`../agent-graph-composition/spec.md`](../agent-graph-composition/spec.md).

## MODIFIED Requirements

### Requirement: Each axis SHALL be edited against its existing vocabulary and never as free text

A free-text field for a value that has a closed vocabulary is a field that eventually holds a value nothing
can interpret. Every axis already has a registry or a versioned builtin set; the console binds to it.

This now covers **nine** axes — prompt, model, skills, tools, context, memory, harness, loop and graph —
and each is edited **per node**.

#### Scenario: The loop axis binds to the loop vocabulary
- **WHEN** an operator edits a node's loop
- **THEN** the choices are the registered loop strategies and their declared stop conditions
- **AND** neither the strategy nor the stop condition is enterable as free text

#### Scenario: The graph axis binds to the declared nodes
- **WHEN** an operator edits the definition's topology
- **THEN** the selectable endpoints are the nodes the definition declares
- **AND** an edge naming a node the definition does not declare is refused at save

#### Scenario: Params are still validated at save against the vocabulary's schema
- **WHEN** an operator saves params for the loop or harness axis
- **THEN** they are validated against the schema that vocabulary declares
- **AND** an invalid param is refused at save, naming the axis and the node

#### Scenario: Every axis is edited per node
- **WHEN** a definition declares more than one node
- **THEN** each node's axes are edited independently
- **AND** a refusal names the node as well as the axis

### Requirement: A strategy whose host service the runner cannot supply SHALL be refused at selection, not at run

The requirement is unchanged in force and now covers the **loop** axis, which is where the control-loop
strategies live after [P34](../../../p34-harness-loop-graph-split/).

#### Scenario: A loop strategy needing an unavailable host service
- **WHEN** an operator selects a loop strategy whose host service this runner does not supply
- **THEN** it is refused at selection, naming the strategy and the missing host service

#### Scenario: A loop exceeding its node's envelope ceiling
- **WHEN** a node's loop declares more turns than that node's harness envelope permits
- **THEN** it is refused at save, naming both values

## ADDED Requirements

### Requirement: An unavailable axis SHALL be rendered with its reason rather than omitted

A hidden axis is indistinguishable from one that does not exist. This held for wiring while it was
vacuous; it holds for every axis a build cannot carry.

#### Scenario: An axis this build cannot carry
- **WHEN** an axis cannot be carried by the deployed build
- **THEN** it is rendered read-only with the reason it is unavailable
- **AND** it is not omitted from the editor

#### Scenario: Wiring on a single-node definition
- **WHEN** a definition declares one node
- **THEN** the wiring axis is rendered with the reason that there is no second node to order it against
- **AND** the reason text is not removed merely because multi-node definitions now exist

### Requirement: The operator surface fence SHALL cover every axis and node kind

#### Scenario: An axis without an operator surface
- **WHEN** an axis exists in the definition vocabulary and has no operator surface
- **THEN** the build fails

#### Scenario: Credential fields remain references across the larger surface
- **WHEN** the definition's fields are inspected reflectively
- **THEN** no field, including every field added by this change, can hold a credential value
- **AND** anything key-shaped offered where a reference belongs is refused
