# CI Integration — Spec (folded from P11)

Product rationale: [`../../../docs/prd/P11-cli-ci-integration.md`](../../../docs/prd/P11-cli-ci-integration.md)
§6 (FR20–FR26) and §7. Design reasoning: [`../../changes/archive/2026-07-25-p11-cli-ci-integration/design.md`](../../changes/archive/2026-07-25-p11-cli-ci-integration/design.md) Decision 5.
Delivery counterpart: [`../../changes/archive/2026-07-25-p12-forge-delivery/`](../../changes/archive/2026-07-25-p12-forge-delivery/) — its CI-mediated
mode runs inside this integration.

Covers the supported CI artifact: a published, versioned action rather than a snippet; checks and
artifacts; the discrimination between *the customer's gate failed* and *our tool broke*; and the rule
that **our availability must never fail a customer's build**.

> **Why build-safety is a hard rule.** Failing a customer's pipeline because our service blinked is a
> stability cost we impose on them for a reason unrelated to their code. A CI step that fails for an
> unclear or external reason gets disabled — after which the check protects nothing, which is worse for
> both parties than the outage was.

## Requirements

### Requirement: A published, versioned CI integration SHALL be provided

A CI integration SHALL be published as a versioned, referencable artifact for at least one major forge,
with the equivalent invocation documented for the others. It SHALL NOT be distributed only as a
copyable snippet.

#### Scenario: The integration is referenced by version

- **WHEN** a customer adds the integration to a pipeline
- **THEN** they reference a published, versioned artifact
- **AND** a fix published by the vendor reaches them by changing that reference.

#### Scenario: Other forges have a documented invocation

- **WHEN** a customer uses a forge without a first-class artifact
- **THEN** a documented invocation achieving the same behavior is available.

### Requirement: The CI integration SHALL post a check and upload the run artifacts

The integration SHALL post a check reporting the run's outcome, and SHALL upload the intermediate
representation and the run report as build artifacts.

#### Scenario: The outcome is visible as a check

- **WHEN** the integration completes
- **THEN** a check reports the run's outcome
- **AND** the outcome is legible without opening the job log.

#### Scenario: The IR and report are retrievable

- **WHEN** the integration completes
- **THEN** the intermediate representation and the run report are available as build artifacts.

### Requirement: Platform unavailability SHALL NOT fail the build

A CI step SHALL NOT fail the build because the platform is unreachable, degraded, or slow. It SHALL
report the condition and continue, and SHALL bound the time it waits.

#### Scenario: An unreachable platform reports and continues

- **WHEN** the platform is unreachable during a CI step
- **THEN** the step reports the condition
- **AND** the build does not fail because of it.

#### Scenario: A slow platform does not stall the pipeline

- **WHEN** the platform accepts a connection but does not respond within the configured bound
- **THEN** the step stops waiting, reports the condition, and continues
- **AND** the build neither fails nor hangs.

#### Scenario: A degraded platform response is reported, not fatal

- **WHEN** the platform returns an error response during a CI step
- **THEN** the step reports it
- **AND** the build does not fail because of it.

### Requirement: A customer-configured quality gate failing SHALL fail the build

When a quality gate the customer configured fails, the CI step SHALL fail the build and SHALL identify
the gate that failed.

#### Scenario: A configured gate fails the build

- **WHEN** a customer-configured quality gate fails
- **THEN** the CI step fails the build
- **AND** it names the gate that failed.

#### Scenario: Gate failure is distinguishable from an operational failure

- **WHEN** a build fails because of a configured gate
- **THEN** the reported outcome distinguishes it from a tool or platform failure
- **AND** a consumer can tell which remedy applies.

### Requirement: Credentials supplied to the CI integration SHALL NOT appear in logs, checks, or artifacts

Credentials consumed by the integration SHALL be taken from the CI secret mechanism and SHALL NOT
appear in job logs, check output, or uploaded artifacts.

#### Scenario: No credential in any emitted surface

- **WHEN** the integration runs and emits logs, a check, and artifacts
- **THEN** no credential value appears in any of them
- **AND** this holds on the failure path as well as the success path.

#### Scenario: Uploaded artifacts are covered

- **WHEN** artifacts are uploaded
- **THEN** they contain no credential value
- **AND** the guarantee extends to artifacts, which persist beyond the job.

### Requirement: The CI integration SHALL be usable without linking

The integration SHALL run and produce its local outputs without any run being transmitted to the
platform.

#### Scenario: A pipeline that publishes nothing

- **WHEN** the integration is configured without linking
- **THEN** it runs, posts its check, and uploads its artifacts
- **AND** nothing is transmitted to the platform.

### Requirement: The CI integration SHALL expose the hook forge delivery uses without defining its contract

The integration SHALL expose the extension point through which CI-mediated forge delivery runs, and
SHALL NOT itself define the delivery contract, the pull-request content, or the delivery record.

#### Scenario: Delivery runs through the exposed hook

- **WHEN** CI-mediated forge delivery is enabled for a repository
- **THEN** it executes through the integration's exposed extension point.

#### Scenario: The integration does not define delivery behavior

- **WHEN** the integration's own behavior is specified
- **THEN** it does not define pull-request content, delivery idempotency, or the delivery record
- **AND** those remain owned by the forge-delivery capability.
