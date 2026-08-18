# Agent Graph Composition — Spec (P36)

The platform's own agent becomes a graph. Its topology is authored, validated and executed by the **same**
code that handles a customer's Variant Spec — a parallel path for our own configuration would be the place
where a rule is quietly weaker, and it would be discovered by a customer.

## ADDED Requirements

### Requirement: The system SHALL validate the agent's topology with the same validator it applies to a customer's spec

#### Scenario: One code path
- **WHEN** an agent definition's topology is validated
- **THEN** it passes through the same typed-contract validation a customer's Variant Spec passes through
- **AND** no validation path exists that applies only to the platform's own agent

#### Scenario: A contract violation in the agent's own graph
- **WHEN** an edge in the agent's definition violates a typed I/O contract
- **THEN** it is refused at publish
- **AND** the mismatch is anchored to the offending edge

### Requirement: The system SHALL let a definition declare concurrency and require a merge on a fan-in

#### Scenario: Concurrent assessment nodes
- **WHEN** a definition declares that several nodes may run concurrently
- **THEN** the ordering still contains every node in a defined sequence
- **AND** the pinned result does not depend on the interleaving

#### Scenario: Fan-in without a merge
- **WHEN** two or more concurrent nodes converge and no merge is declared
- **THEN** the definition is refused at publish
- **AND** no default combination is applied

### Requirement: The system SHALL keep a pinned inference byte-identical under internal concurrency

#### Scenario: Repeated execution of the same pin
- **WHEN** the same pinned inference is executed repeatedly under a concurrent definition
- **THEN** the output is byte-identical every time
- **AND** any order-dependence in a merge is a defect rather than an accepted variance

### Requirement: The system SHALL support a conditional edge in the agent's definition under the existing expression rules

#### Scenario: Predicate validated at publish
- **WHEN** an edge in the agent's definition declares a predicate
- **THEN** the predicate is validated at publish under the same rules that govern an expression binding elsewhere
- **AND** a predicate naming an unavailable symbol is refused

### Requirement: The system SHALL expose per-node operation to operators

#### Scenario: Per-node observability
- **WHEN** the agent's health is read
- **THEN** inference counts, latency, spend and failure rates are available per node
- **AND** they are not only available as an aggregate over the definition

#### Scenario: Rollback is one act
- **WHEN** an operator rolls back to a previous definition version
- **THEN** activating that version is sufficient
- **AND** it does not require re-authoring the older shape

### Requirement: The system SHALL keep an in-flight assessment on the definition it started with

#### Scenario: Activation during an assessment
- **WHEN** a new definition is activated while an assessment is in flight
- **THEN** that assessment completes under the definition it started with
- **AND** the report records which definition produced it
