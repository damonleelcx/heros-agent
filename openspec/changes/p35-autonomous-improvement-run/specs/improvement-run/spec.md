# Improvement Run — Spec (P35)

One question becomes a bounded plan, a set of verified proposals, an approval, an application, a
re-measurement, and — when the change reproduces — a pull request. Every gate it passes through already
exists; this capability adds no second one.

## ADDED Requirements

### Requirement: The system SHALL translate a question into a bounded plan before executing anything

#### Scenario: The plan is explicit
- **WHEN** a question is accepted
- **THEN** a plan is produced carrying the workflow, the source revision, the axes in scope, the maximum candidate count, the spend budget and the stopping condition
- **AND** it is shown before any candidate is generated

#### Scenario: Spend above the disclosure threshold
- **WHEN** the plan's projected spend exceeds the disclosure threshold
- **THEN** execution does not begin until the plan is acknowledged

#### Scenario: An untranslatable question
- **WHEN** a question cannot be translated into a bounded plan
- **THEN** it is refused
- **AND** it is NOT run with default bounds

### Requirement: The system SHALL surface only candidates that pass the held-out verification gate

#### Scenario: A gate-failing high scorer
- **WHEN** a candidate scores highly but fails a gate
- **THEN** it is not surfaced and not delivered, however high its composite

#### Scenario: An unverified candidate
- **WHEN** a candidate has not been verified on held-out data
- **THEN** it is not surfaced and not delivered

#### Scenario: A contract-violating candidate
- **WHEN** a candidate violates a typed I/O contract
- **THEN** it is rejected before verification and never surfaced

#### Scenario: The verified delta travels with the proposal
- **WHEN** a proposal is rendered anywhere
- **THEN** it carries its verified delta with the confidence interval and the size of the set behind it

### Requirement: The system SHALL name which of the closed states applies when nothing can be proposed

#### Scenario: A named empty state
- **WHEN** no proposal can be generated
- **THEN** the reported state is one of the closed set, named
- **AND** it is not reported as an empty result or a generic failure

### Requirement: The system SHALL require explicit per-proposal approval bound to a configuration and revision

#### Scenario: One approval per proposal
- **WHEN** proposals are surfaced
- **THEN** each carries its own approval decision
- **AND** no control approves more than one proposal

#### Scenario: Declining one proposal
- **WHEN** a proposal is declined
- **THEN** the run continues with the remaining proposals
- **AND** the declined proposal remains visible with its decision recorded

#### Scenario: The subject moves
- **WHEN** the `config_hash` or the source revision changes after an approval is given
- **THEN** the approval is void and is re-requested

#### Scenario: No override exists
- **WHEN** a plan, role, entitlement, flag or request parameter would materialise a configuration the transform refuses
- **THEN** the configuration is still refused

### Requirement: The system SHALL re-measure after applying and withdraw a change that fails to reproduce

#### Scenario: Re-measurement reproduces
- **WHEN** an applied change re-measures within its verified delta's interval
- **THEN** it proceeds to delivery

#### Scenario: Re-measurement disagrees
- **WHEN** an applied change fails to reproduce its verified delta
- **THEN** it is withdrawn before delivery
- **AND** both measurements are reported

#### Scenario: A pinned measurement run
- **WHEN** a re-measurement run's resolved `config_hash` does not match what was requested
- **THEN** the run fails rather than being scored

### Requirement: The system SHALL bound every run and report which bound stopped it

#### Scenario: A bound is reached
- **WHEN** a run reaches its spend budget, its candidate cap, or its stopping condition
- **THEN** it stops and reports which bound stopped it

#### Scenario: Budget exhaustion is not an error
- **WHEN** a run stops because its budget is exhausted
- **THEN** it is reported as a stopping condition with the results obtained so far

#### Scenario: Kill switch
- **WHEN** the kill switch is armed
- **THEN** the run halts at the next safe point

#### Scenario: Cancellation is atomic with respect to the repository
- **WHEN** a run is cancelled at any point
- **THEN** either a pull request exists or nothing was pushed
- **AND** no partial branch is left on the customer's repository

### Requirement: The system SHALL record every run in an append-only ledger

#### Scenario: Ledger entry
- **WHEN** a run completes, stops or is cancelled
- **THEN** the ledger records the plan, the candidates, their verdicts, the approvals and the deliveries

#### Scenario: Reconciliation is a necessary path
- **WHEN** a run is interrupted between applying a change and delivering it
- **THEN** the next reconciliation pass resolves the state from the append-only record with no human step
- **AND** that pass runs whether or not anything was interrupted
