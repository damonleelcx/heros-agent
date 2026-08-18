# Eval-Set Decisiveness — Spec (P33)

A generated eval set whose oracles cannot fail scores perfectly. `internal/evalboard`'s `CoverageView`
already computes the properties that expose this — `OracleCoverage`, `NIndecisive` (*cases carrying an
oracle that can never fail*) and the vacuous-dimension list — and none of them reaches a screen. This
capability makes them travel with every score, and makes the cases themselves enumerable.

## ADDED Requirements

### Requirement: The system SHALL report an eval set's decisiveness wherever it reports that set's score

#### Scenario: Score carries decisiveness
- **WHEN** a measured finding reports a score from an eval set
- **THEN** it also reports `n_cases`, oracle coverage, and the count of cases carrying an oracle that can never fail
- **AND** these are rendered beside the score, not behind a link

#### Scenario: A set that cannot fail
- **WHEN** every case in an eval set carries an oracle that can never fail
- **THEN** the finding states that the set cannot fail
- **AND** the score is not presented as evidence of quality

#### Scenario: Coverage was not measured
- **WHEN** oracle coverage was not measured for an eval set
- **THEN** that is stated, and the score is labelled accordingly

### Requirement: The system SHALL make the cases of an eval set enumerable

#### Scenario: Cases are listable
- **WHEN** a reader opens an eval set reported by a finding
- **THEN** the individual cases are listed, not only counted
- **AND** each case shows its oracle and whether that oracle can fail

#### Scenario: Vacuous dimensions are named
- **WHEN** an eval set contains dimensions that no case exercises
- **THEN** those dimensions are named in the report

### Requirement: The system SHALL report a measured result with its interval and its seed count

#### Scenario: Multi-seed reporting
- **WHEN** a measured finding is reported
- **THEN** it carries the confidence interval and the number of seeds
- **AND** a result from a single seed is labelled as such

#### Scenario: Overlapping intervals are a tie
- **WHEN** two measured results have overlapping confidence intervals
- **THEN** they are reported as a tie rather than as an ordering

### Requirement: The system SHALL name the reason a workflow could not be measured

#### Scenario: Four reasons stay four
- **WHEN** an eval cannot be run for a workflow
- **THEN** the named missing input is exactly one of: no runnable entry point, missing credential, sandbox refusal, or unsupported language
- **AND** each renders a distinct message
