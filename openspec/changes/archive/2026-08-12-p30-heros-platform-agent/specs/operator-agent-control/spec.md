# operator-agent-control

## ADDED Requirements

### Requirement: The durable kill switch SHALL gate inference fleet-wide
An in-memory brake that forgets it was pulled across a restart is worse than none, because the console
then asserts the brake is off when the operator's last act was to pull it.

#### Scenario: Arming stops inference
- **WHEN** an operator arms the kill switch
- **THEN** no inference runs for any tenant
- **AND** surfaces render rule-derived facts and report HEROS as unavailable

#### Scenario: The state survives a restart
- **WHEN** the switch is armed and the process restarts
- **THEN** the switch is still armed
- **AND** the console shows it armed

#### Scenario: Stored inferences remain readable
- **WHEN** the switch is armed
- **THEN** previously stored inferences continue to be served
- **AND** they are marked as produced before the switch was armed

### Requirement: HEROS spend SHALL be metered per tenant per inference
#### Scenario: Usage is recorded
- **WHEN** an inference completes
- **THEN** its input and output tokens are recorded against the tenant and the inference id

#### Scenario: An unpriced model yields `unpriced`, never zero
- **WHEN** the model used has no price in the catalog
- **THEN** the recorded cost is `unpriced`
- **AND** it is not recorded as `0`

#### Scenario: Cost is labelled as an estimate
- **WHEN** a cost figure is rendered
- **THEN** it is labelled as an estimate
- **AND** it is not presented as a billed amount

### Requirement: Spend caps SHALL be enforced before the call
A cap reconciled after the fact is not a cap.

#### Scenario: A per-tenant cap stops inference
- **WHEN** a tenant has reached its configured spend cap
- **THEN** no further inference runs for that tenant
- **AND** the surface reports the cap as the reason

#### Scenario: A fleet cap stops inference
- **WHEN** the fleet-wide cap is reached
- **THEN** no further inference runs for any tenant
- **AND** the operator console reports it

#### Scenario: Reaching a cap is an event
- **WHEN** a cap is reached
- **THEN** a structured event is emitted from the central enumeration
- **AND** it is not a silent stop

### Requirement: HEROS's resolved state SHALL be externally readable
A health signal that requires reading logs is not a health signal.

#### Scenario: Readiness reports the agent
- **WHEN** the readiness endpoint is queried
- **THEN** it reports HEROS as one of `disabled`, `ready`, `credential_unresolved`, `capped`
- **AND** the reported state matches the state inference actually resolves to

#### Scenario: The state is not asserted from configuration alone
- **WHEN** a credential reference is configured but does not resolve
- **THEN** readiness reports `credential_unresolved`
- **AND** it does not report `ready` on the grounds that a reference is present

### Requirement: Every run SHALL emit a structured event from the central enumeration
#### Scenario: No literal event names
- **WHEN** the inference package emits an event or an error code
- **THEN** the name comes from the central enumeration
- **AND** a static check fails the build on a literal

### Requirement: The operator console SHALL show what each definition produced
#### Scenario: Inference count per definition
- **WHEN** an operator views an agent definition
- **THEN** the number of stored inferences produced under its `config_hash` is shown

#### Scenario: The delta against the previous definition is shown
- **WHEN** a new definition passes rehearsal
- **THEN** the console shows its per-fixture precision and recall alongside the previously active
  definition's
- **AND** a regression on any fixture is marked
