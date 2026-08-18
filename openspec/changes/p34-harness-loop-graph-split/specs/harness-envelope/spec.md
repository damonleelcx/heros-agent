# Harness Envelope — Spec (P34)

`DimHarness` narrows to the **execution envelope**: what the loop is allowed to do and inside what walls.
The ceiling is imposed; the value within it is chosen by the loop
([`../loop-strategy/spec.md`](../loop-strategy/spec.md)).

> ⚠️ **These are `ADDED`, not `MODIFIED`, and the reason matters at fold time.**
> P18 is still an **open change** — its harness capabilities (`harness-strategy`, `harness-authoring`,
> `harness-runtime`, `harness-delivery`, `harness-materialization`, `agent-loop`) live under
> [`../../../p18-harness-strategy-optimization/`](../../../p18-harness-strategy-optimization/) and have
> **not been folded into `openspec/specs/`**. There is therefore no folded requirement to restate, and a
> `MODIFIED` header here would name a requirement that does not exist in the current truth.
>
> **Reconciliation is a task, not an assumption.** P18 defines the harness axis as carrying both the
> scaffold and the control loop; this change set splits them. Whichever change folds second must
> reconcile the two, and `tasks.md` §2.6 owns it. Folding P18 unchanged after this change would restore
> the conflation.

## ADDED Requirements

### Requirement: The system SHALL scope the harness dimension to the execution envelope

#### Scenario: The envelope's content
- **WHEN** a harness entry is resolved
- **THEN** it carries sandbox posture, host-service provision, turn ceiling, spend ceiling, retries, timeouts, concurrency limit, and guardrail and approval-gate bindings
- **AND** it carries no control-loop strategy and no stop condition

#### Scenario: A legacy entry still resolves
- **WHEN** a harness entry authored before this change carries loop fields
- **THEN** it resolves and reproduces its prior `config_hash`
- **AND** its loop fields are honoured for that legacy spec

### Requirement: The system SHALL impose the envelope's ceiling on the loop rather than accepting the loop's value

#### Scenario: Turn count above the ceiling
- **WHEN** a loop declares `max_turns` above the envelope's turn ceiling
- **THEN** it is refused at resolve
- **AND** the refusal names both the requested value and the ceiling

#### Scenario: Raising a ceiling changes no loop configuration
- **WHEN** an operator raises the turn ceiling
- **THEN** no loop entry's content changes
- **AND** no loop entry's `version_id` changes

#### Scenario: A spend ceiling is enforced before the call
- **WHEN** a node is about to make a provider call
- **THEN** the envelope's spend ceiling is checked before the call is made
- **AND** exhaustion is reported as a named stopping condition rather than as an error

### Requirement: The system SHALL refuse at resolve a loop whose required host service the envelope does not provide

#### Scenario: A tool-calling loop without a tool executor
- **WHEN** a spec binds `react-loop` and the envelope provides no tool executor
- **THEN** it is refused at resolve, naming the loop and the missing host service
- **AND** the refusal happens before execution rather than at run time

#### Scenario: A planning loop without a planner
- **WHEN** a spec binds `plan-execute` and the envelope provides no planner
- **THEN** it is refused at resolve

#### Scenario: A critic loop without a critic
- **WHEN** a spec binds `critic-loop` and the envelope provides no critic
- **THEN** it is refused at resolve

### Requirement: The system SHALL enforce the concurrency limit in the sandbox rather than trusting the spec

#### Scenario: A group wider than the limit
- **WHEN** a spec declares a concurrent group wider than the envelope's concurrency limit
- **THEN** it is refused at resolve

#### Scenario: The limit is enforced at execution
- **WHEN** a run executes a concurrent group
- **THEN** the sandbox enforces the limit independently of what the spec declared
