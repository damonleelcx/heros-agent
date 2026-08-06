# deployment-topology

## ADDED Requirements

### Requirement: A documented operating procedure SHALL be executable in the order it states

The seam-flip procedure instructed the operator to set a password (step 2) before flipping the console's
identity seam (step 3), while the page that sets the password was reachable only after step 3. An order
that cannot be followed is worse than no order: it is followed until it fails, and the failure looks like
the operator's mistake.

#### Scenario: Each step's precondition is satisfiable by the steps before it
- **WHEN** a runbook prescribes an ordered procedure against a deployment
- **THEN** every step is performable in the state left by the steps preceding it
- **AND** a step whose precondition the procedure cannot itself establish is marked as blocked, naming
  what it waits on

#### Scenario: A safety step that cannot run is not described as a safety
- **WHEN** a procedure's safety argument depends on a step that cannot be executed in the stated order
- **THEN** the document states that the safety is not in effect
- **AND** it does not present the resulting action as verified

### Requirement: A one-way step SHALL state whether its reversal has been exercised

#### Scenario: A rollback is distinguished from a rollback that has been performed
- **WHEN** a document describes reverting a one-way step
- **THEN** it states whether that reversal has actually been carried out on a deployment
- **AND** an unexercised reversal is recorded as a claim rather than a procedure

### Requirement: Environment drift SHALL be re-appliable without a window in which the workload cannot boot

Where a live workload carries a literal value for a variable the checked-in manifest declares as a
secret reference, the API rejects the entire Deployment
(`valueFrom … may not be specified when value is not empty`). Server-side apply does not resolve it: the
container's environment list merges by name there as well.

#### Scenario: A drifted deployment can be brought back to its manifest
- **WHEN** a workload's live environment conflicts in shape with the checked-in manifest
- **THEN** a documented procedure exists that reconciles it
- **AND** that procedure does not pass through a state in which the workload's own launch checks would
  refuse to start

#### Scenario: The reconciliation is guarded against operating on the wrong entry
- **WHEN** the reconciliation addresses environment entries by position
- **THEN** it asserts the identity of each entry it modifies before modifying it
- **AND** it fails without writing if an assertion does not hold

### Requirement: An out-of-band change to a deployed workload SHALL be recorded in the manifests or be expected to disappear

#### Scenario: A value set outside the manifests is reverted by the next apply
- **WHEN** a variable declared in the checked-in manifest is changed directly against the cluster
- **THEN** the next apply of that manifest restores the declared value
- **AND** any procedure that sets such a variable states that it must be performed after the apply, not
  before
