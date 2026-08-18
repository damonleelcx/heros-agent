# Telemetry Deployment Posture — Spec (folded from P24)

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR1–FR5), §7 (NFR6–NFR7). Technical decisions: [`../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md`](../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md) D1.

Covers the question a private-deploy customer asks first: *does this thing phone home?* The same
binaries and the same two consoles run in our hosted deployment, in a customer's own Compose or
Kubernetes install, and in an air-gapped network we will never see. This capability makes the answer
different in each, by construction rather than by configuration discipline.

> The asymmetry that carries it: **absence is a decision, and a decision does not warn.** An
> unconfigured integration is silent — no log line, no readiness noise, no console warning — because a
> deployment with no analytics is the normal case, not a broken one. What *is* loud is a configured
> integration that cannot transmit, which is a fault.

## Requirements

### Requirement: Each integration SHALL be absent unless configured, and absence SHALL be silent

An analytics, session-replay or error-reporting integration with no configured identifier SHALL emit no
third-party request, load no third-party script, write no non-essential storage entry, and produce no
warning or per-request log line about its absence.

#### Scenario: A build with nothing configured contacts nobody
- **WHEN** the platform is started with no analytics measurement id, no session-replay project id and no
  error-reporting DSN
- **THEN** no request to any third-party origin is made from any surface
- **AND** no third-party script is present in any served page
- **AND** no warning about the missing configuration is logged or displayed.

#### Scenario: Absence is a tested configuration, not a degraded one
- **WHEN** the test suite runs against a build with nothing configured
- **THEN** every surface renders and every function works
- **AND** readiness reports the platform as ready.

### Requirement: Absent SHALL be the default on every substrate other than the platform's own hosted deployment

The Docker Compose artefacts, the Kubernetes base and every overlay SHALL carry no measurement id, no
project id and no DSN, and SHALL NOT accept one from a discovered or inherited default.

#### Scenario: A customer deployment carries no identifier
- **WHEN** the Compose artefacts and the Kubernetes base and overlays are inspected
- **THEN** no analytics measurement id, session-replay project id or error-reporting DSN appears in any
  of them
- **AND** no default value is supplied for any of the three.

#### Scenario: An inherited default cannot enable an integration
- **WHEN** a deployment is applied with no explicit telemetry configuration
- **THEN** all three integrations resolve to absent
- **AND** no fallback, discovery mechanism or built-in constant supplies an identifier.

### Requirement: The air-gapped package SHALL be asserted to reference no external origin, at package-build time

The air-gapped artefact SHALL reference zero external origins — no script host, no ingest host, no font
host, no image host. The assertion SHALL run as part of the package build and SHALL fail it, rather than
being checked at install time or stated in documentation.

#### Scenario: The assertion runs in the build that produces the artefact
- **WHEN** the air-gapped package is built
- **THEN** the build asserts that the artefact references zero external origins
- **AND** a package that references one fails the build, naming the origin and the file it appears in.

#### Scenario: The claim is not deferred to install time
- **WHEN** an operator receives the air-gapped package
- **THEN** the no-external-origin property was established by the build, not by a check they must run
- **AND** the assertion's result is part of the package's own verification output.

### Requirement: Each integration's state SHALL be readable on the readiness surface as one of three named states

Each integration SHALL report `absent`, `configured` or `degraded` on the existing readiness surface, in
words, never as a boolean. `degraded` SHALL name the integration and the observed failure class.

#### Scenario: Three states are distinguishable
- **WHEN** the readiness surface is read
- **THEN** each integration reports exactly one of `absent`, `configured` or `degraded`
- **AND** `absent` is distinguishable from `degraded` without inspecting logs.

#### Scenario: A degraded integration names its failure class
- **WHEN** an integration is configured and its transmit target is unreachable
- **THEN** readiness reports it `degraded`
- **AND** the report names the integration and the failure class.

#### Scenario: A third-party console is not the health signal
- **WHEN** an operator asks whether reporting is working
- **THEN** the answer is available on the platform's own readiness surface
- **AND** it does not require reading the third party's dashboard.

### Requirement: No integration SHALL be a startup dependency or affect a served request

A service SHALL start, serve and pass readiness with an integration misconfigured or unreachable. No
transmit SHALL block, delay, retry into an unbounded queue, fail or panic a served request.

#### Scenario: A misconfigured integration does not prevent startup
- **WHEN** a service starts with a malformed DSN
- **THEN** the service starts and serves normally
- **AND** readiness reports that integration `degraded`.

#### Scenario: An unreachable target does not reach a customer
- **WHEN** the transmit target is unreachable for the duration of a load run
- **THEN** no served request fails, and no served route's p99 latency changes measurably
- **AND** the failure is logged at most once per interval rather than once per event.

### Requirement: No integration SHALL be present in the CLI

The `heros` CLI SHALL carry no analytics, no session replay and no error reporting. Its offline-first,
account-free, network-free operation SHALL be unchanged.

#### Scenario: The CLI transmits nothing about itself
- **WHEN** the CLI is exercised across discovery, apply, eval, authoring and upgrade with no network
- **THEN** every command completes
- **AND** no telemetry, analytics, machine identifier or hostname is transmitted on any path.
