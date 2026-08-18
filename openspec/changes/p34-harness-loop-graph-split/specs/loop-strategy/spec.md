# Loop Strategy — Spec (P34)

`DimLoop` carries the iteration **policy**: which control loop runs, its stop condition, `max_turns` as a
chosen value, the reflection prompt and the critic binding. It relocates behaviour that exists today
inside `DimHarness`; it adds no new strategy.

## ADDED Requirements

### Requirement: The system SHALL provide a loop dimension sealed by its own registry kind

#### Scenario: The dimension is a member of the closed enum
- **WHEN** the dimension set is enumerated
- **THEN** `loop` is a member
- **AND** its wire value is stable, because it is recorded on error records and spec rows

#### Scenario: The kind is hashed into the version id
- **WHEN** a loop entry is sealed
- **THEN** the kind is part of the content address
- **AND** a loop ref pasted into another dimension fails to resolve rather than resolving silently

### Requirement: The system SHALL restrict the loop vocabulary to the strategies and stop conditions that already exist

#### Scenario: The five strategies
- **WHEN** a loop entry names a strategy
- **THEN** it is one of `single-shot`, `reflexion`, `react-loop`, `plan-execute`, `critic-loop`

#### Scenario: The four stop conditions
- **WHEN** a loop entry names a stop condition
- **THEN** it is one of `answer-marker`, `no-tool-call`, `plan-complete`, `max-turns`

### Requirement: The system SHALL fail to resolve a loop naming a strategy this build does not implement

#### Scenario: Unknown strategy does not fall back
- **WHEN** a loop entry names a strategy the build does not implement
- **THEN** resolution fails
- **AND** it does NOT fall back to `single-shot`, which would run one turn under a multi-turn `config_hash`

### Requirement: The system SHALL refuse an unbounded or invalid turn count

#### Scenario: Zero or negative turns
- **WHEN** a loop declares `max_turns` less than one
- **THEN** it is refused
- **AND** it is not defaulted to one

#### Scenario: Single-shot expresses no turn count
- **WHEN** the strategy is `single-shot`
- **THEN** `max_turns` is inexpressible at the registry layer
- **AND** nothing can make the loop run more than one turn

### Requirement: The system SHALL refuse a spec that sets both a legacy loop-bearing harness ref and a loop ref

#### Scenario: Ambiguous configuration
- **WHEN** a spec sets a `harness_ref` that carries loop fields and also sets a `loop_ref`
- **THEN** it is refused at resolve
- **AND** the refusal names both refs

#### Scenario: Legacy refs remain resolvable
- **WHEN** a spec authored before this change references a loop-bearing harness entry and sets no `loop_ref`
- **THEN** it resolves
- **AND** it reproduces the same `config_hash` it produced before this change

#### Scenario: New authoring cannot create a legacy entry
- **WHEN** an author publishes a loop configuration through any surface
- **THEN** a loop entry is created and a loop-bearing harness entry is not
