# Surface Assessment — Spec (P33)

An assessment reports, for each of the nine axes, what the repository does and what evidence supports the
claim. It produces **no composite score** (program ruling R4).

## ADDED Requirements

### Requirement: The system SHALL report every one of the nine axes in every assessment

The axes are `model`, `prompt`, `skills`, `tools`, `context`, `memory`, `harness`, `loop` and `graph`.

#### Scenario: All axes present
- **WHEN** an assessment completes
- **THEN** a finding exists for each of the nine axes
- **AND** no axis is omitted for having nothing to say

#### Scenario: A repository that fails at every axis
- **WHEN** no axis can be determined for a repository
- **THEN** nine findings are reported, each in state `not_measured` with its own named missing input

### Requirement: The system SHALL give every finding an evidence reference, an origin and a state

State is one of `measured`, `observed`, `not_measured`, `refused`. Origin is one of `structural`,
`inferred`.

#### Scenario: A measured finding
- **WHEN** a finding reports a number produced by an eval run
- **THEN** its state is `measured` and it carries the confidence interval and the size of the set behind it

#### Scenario: An observed finding
- **WHEN** a finding is read deterministically from the IR or the tree
- **THEN** its state is `observed` and its origin is `structural`

#### Scenario: An undeterminable surface
- **WHEN** an axis cannot be determined or measured
- **THEN** its state is `not_measured` and it names the missing input
- **AND** it is never reported as zero and never omitted

#### Scenario: An axis this build cannot assess
- **WHEN** the build cannot assess an axis for the target's language
- **THEN** its state is `refused` and the refusal names which of the frontend, the analysis, or the language support is missing

### Requirement: The system SHALL label an inferred finding as an inference and carry its pinned address

#### Scenario: Inference is visible without interaction
- **WHEN** a finding with origin `inferred` is rendered
- **THEN** its origin is visible without hovering or expanding
- **AND** it carries the content address of the pinned inference that produced it

#### Scenario: Structural extraction runs first
- **WHEN** an axis can be determined deterministically
- **THEN** it is extracted structurally and no inference is run for it

### Requirement: The system SHALL NOT emit a composite score, grade, level or cross-tenant ranking

#### Scenario: No composite is producible
- **WHEN** any assessment completes
- **THEN** no field carries a score, grade or level spanning more than one axis
- **AND** no code path exists that computes one

#### Scenario: No cross-tenant comparison
- **WHEN** an assessment is rendered
- **THEN** it contains no comparison against another tenant's repository

### Requirement: The system SHALL order findings by evidence strength

#### Scenario: Ordering
- **WHEN** findings are listed
- **THEN** they are ordered `measured`, then `observed`, then `inferred`, then `not_measured`
- **AND** they are not ordered by a severity that is itself an inference

### Requirement: The system SHALL attribute an empty graph to the frontend rather than to the repository

#### Scenario: Zero edges from a frontend that emits none
- **WHEN** discovery produces zero edges because the language's frontend does not emit edges
- **THEN** the graph finding names the language and the frontend as the missing input
- **AND** the repository is NOT reported as having a flat or single-layer graph

#### Scenario: Coverage is not asserted from an absence
- **WHEN** no model fallback ran and no rule produced a label
- **THEN** the surface does NOT report that rules covered everything

### Requirement: The system SHALL pin an inference per source revision and agent configuration

#### Scenario: Repeat assessment
- **WHEN** an assessment is run twice for the same `(source_revision, agent config_hash)`
- **THEN** the findings are identical, including their evidence references
- **AND** no provider call is made on the second run

#### Scenario: Explicit re-inference
- **WHEN** re-inference is explicitly requested
- **THEN** the result is presented as a diff against the pinned result

#### Scenario: Inference cannot conclude
- **WHEN** an inference cannot reach a conclusion
- **THEN** it returns `not_measured` with a named missing input
- **AND** it does not return a low-confidence conclusion

### Requirement: The system SHALL record the agent configuration and provider model that produced an assessment

#### Scenario: Attribution of a finding to its producer
- **WHEN** a finding is stored
- **THEN** it records the agent `config_hash` and the provider model version that produced it
- **AND** a change in provider model version is distinguishable from a change in the customer's repository

### Requirement: The system SHALL bound and attribute the cost of an assessment

#### Scenario: Budget exhausted
- **WHEN** an assessment reaches its spend cap
- **THEN** the remaining axes report `not_measured` with `budget exhausted` as the named missing input
- **AND** the partial report is not presented as complete

#### Scenario: Disclosure above a threshold
- **WHEN** an assessment's projected spend exceeds the disclosure threshold
- **THEN** the projected spend is shown before it is spent
