# Skill Registry — Spec Delta (P3)

Product rationale: [`../../../../docs/prd/P3-context-skills-sandbox.md`](../../../../docs/prd/P3-context-skills-sandbox.md) §6 (FR7–FR11).

Hardens the P2 Skill Registry baseline: every entry carries a JSON-schema contract; the runtime
validates it before execution and fails closed on contract mismatch.

## ADDED Requirements

### Requirement: Every Skill Registry entry SHALL carry a JSON-schema contract validated as well-formed at registration

Each entry SHALL carry a JSON-schema contract for its inputs and outputs. At registration, both the
input and output schemas SHALL themselves be validated as well-formed JSON Schema; an entry whose
contract is malformed SHALL be rejected.

#### Scenario: Malformed contract rejected at registration
- **WHEN** a skill is registered with an `input_schema` that is not well-formed JSON Schema
- **THEN** registration is rejected with a schema-validation error
- **AND** the entry is not stored in the registry

#### Scenario: Well-formed contract accepted
- **WHEN** a skill is registered with well-formed input and output JSON schemas
- **THEN** the entry is stored and assigned an immutable version ID carrying both schemas

### Requirement: The runtime SHALL validate tool availability and argument shape against the contract before executing a skill

Before a node executes a skill, the runtime SHALL validate (a) that the named tool is **available** —
the referenced skill version resolves and its implementation handle is bindable in the sandbox — and
(b) that the supplied **argument object conforms** to the entry's input JSON-schema. This validation
SHALL occur before the skill implementation is invoked.

#### Scenario: Argument-schema violation rejected before the tool runs
- **WHEN** a node binds a skill and supplies an argument object that violates the entry's input
  JSON-schema
- **THEN** validation fails before the implementation is invoked
- **AND** a typed error names the skill and the violated field
- **AND** the skill implementation is never executed

#### Scenario: Unavailable tool fails closed
- **WHEN** a node's `skill_refs` names a skill version that does not resolve, or whose implementation
  handle cannot be bound in the sandbox
- **THEN** resolution fails closed
- **AND** the node does not execute

### Requirement: The runtime SHALL validate a tool result against the output schema before propagating it downstream

After a skill implementation returns, the runtime SHALL validate the result against the entry's
output JSON-schema before the result is passed to any downstream node.

#### Scenario: Output-schema violation discarded before propagation
- **WHEN** a skill returns a result that violates its output JSON-schema
- **THEN** the result is discarded and not passed downstream
- **AND** the run fails closed with a typed error naming the skill and the violated field

### Requirement: The runtime SHALL fail closed on any skill contract mismatch

On any contract mismatch — unavailable tool, input-argument violation, or output violation — the
runtime SHALL fail closed: it SHALL NOT invoke the implementation (for input/availability failures)
or SHALL discard the result (for output failures), and SHALL NOT allow the node to proceed as if the
skill had succeeded.

#### Scenario: No silent proceed on mismatch
- **WHEN** any of the skill's availability, input, or output contract checks fails
- **THEN** the node does not proceed as if the skill succeeded
- **AND** a typed, named error is surfaced rather than a partial or default result

### Requirement: Skill binding and contract validation SHALL be deterministic and independent of ambient host state

Given the same skill `version_id` and argument object, contract validation and binding SHALL produce
the same result and SHALL NOT depend on ambient host state (environment variables, host filesystem,
or wall-clock).

#### Scenario: Validation result is reproducible
- **WHEN** the same skill `version_id` is validated against the same argument object twice
- **THEN** both validations return the same verdict
- **AND** the verdict does not change based on host environment or filesystem state
