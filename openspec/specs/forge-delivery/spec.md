# Forge Delivery — Spec (folded from P12)

Product rationale: [`../../../docs/prd/P12-forge-delivery.md`](../../../docs/prd/P12-forge-delivery.md)
§6 (FR1–FR15) and §7. Architecture decision:
[`../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md`](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md).
Design reasoning: [`../../changes/archive/2026-07-25-p12-forge-delivery/design.md`](../../changes/archive/2026-07-25-p12-forge-delivery/design.md) Decisions 1, 2, 3, 5, 6, 7, 8.

Covers how a verified optimization reaches a customer's repository: two credential modes producing
identical pull requests, evidence in the pull request, idempotency and supersession, volume bounds, the
never-merge rule, halt behavior, and the requirement that having no delivery route is a **reported
state** rather than silence.

> **The load-bearing property.** In the default mode the platform holds **no forge credential**. The
> customer's CI opens the pull request with the ephemeral, repository-scoped token it already holds, so
> the credential described in the P2 exit analysis as *"the highest blast-radius action in the system"*
> is never created on our side. Where a credential does exist — the opt-in hosted App — it is
> contained, and the spec says so plainly rather than presenting the two modes as equivalent.

## Requirements

### Requirement: Delivery SHALL support two modes producing identical pull requests

Delivery SHALL support a CI-mediated mode and a hosted application mode. For the same proposal and
target, both modes SHALL produce **identical pull-request content**.

#### Scenario: The same proposal produces the same pull request in either mode

- **WHEN** the same proposal is delivered to the same target through each mode in turn
- **THEN** the resulting pull-request content is identical
- **AND** the difference between the modes is which credential opened it, not what it says.

#### Scenario: The mode is not a product tier

- **WHEN** a customer uses either mode
- **THEN** the evidence, diff, and lineage in the pull request are the same
- **AND** neither mode receives a reduced or enhanced pull request.

### Requirement: CI-mediated delivery SHALL be the default and SHALL NOT require a platform-held forge credential

CI-mediated delivery SHALL be the default mode. In it the platform SHALL NOT receive, store, or request
a forge credential; the pull request SHALL be opened by the customer's own continuous-integration
environment using a credential that environment already holds.

#### Scenario: The platform holds no forge credential in the default mode

- **WHEN** delivery is configured in the default mode
- **THEN** the platform holds no forge credential for that repository
- **AND** no code path reads, stores, or requests one.

#### Scenario: The customer's own credential opens the pull request

- **WHEN** a verified proposal is delivered in the default mode
- **THEN** the pull request is opened by the customer's continuous-integration environment
- **AND** the credential used is the one that environment already holds for that repository.

### Requirement: A hosted application installation SHALL be per-repository, least-privilege, and customer-revocable

Where the platform holds a forge credential, the installation SHALL be scoped to repositories the
customer selects rather than granted organization-wide by default, SHALL hold a permission set no
broader than opening and updating pull requests on those repositories, and SHALL be revocable by the
customer without contacting the platform.

#### Scenario: Installation is scoped to selected repositories

- **WHEN** a customer installs the application
- **THEN** it applies only to the repositories they selected
- **AND** an organization-wide grant is not the default.

#### Scenario: The permission set is no broader than delivery requires

- **WHEN** the installation's permissions are inspected
- **THEN** they permit opening and updating pull requests on the selected repositories and nothing
  broader
- **AND** a broader permission is a specification change rather than a configuration choice.

#### Scenario: The customer can revoke without contacting the platform

- **WHEN** a customer revokes the installation from their own forge settings
- **THEN** the platform can no longer deliver to those repositories
- **AND** revocation required no action by, or request to, the platform.

### Requirement: A forge credential SHALL NOT be logged, embedded, or transmitted outside the platform

A forge credential held by the platform SHALL NOT appear in any log line, telemetry attribute,
pull-request body, or artifact, and SHALL NOT leave the platform.

#### Scenario: No credential in any emitted surface

- **WHEN** the platform performs a delivery and emits logs, telemetry, a pull-request body, and artifacts
- **THEN** no credential value appears in any of them
- **AND** this holds on the failure path as well as the success path.

### Requirement: Only a change that passed the verification gate SHALL be delivered

A pull request SHALL be opened only for a change that passed the verification gate. Delivery SHALL NOT
provide a path by which an unverified change reaches a repository.

#### Scenario: An unverified change is undeliverable

- **WHEN** delivery is attempted for a change that has not passed the verification gate
- **THEN** no pull request is opened
- **AND** the attempt is refused rather than queued.

#### Scenario: Every delivery entry point enforces the gate

- **WHEN** delivery is invoked through any entry point, in either mode
- **THEN** the verification precondition is enforced
- **AND** no entry point bypasses it.

### Requirement: Every pull request SHALL carry its evidence

A pull request SHALL contain the diff, the verified delta with its confidence interval, the evaluation
evidence, the configuration-hash lineage, and a reference that opens the full evidence in the console.

#### Scenario: A reviewer can judge the change without leaving the pull request

- **WHEN** a reviewer opens a delivered pull request
- **THEN** the diff, the verified delta with its interval, the evaluation evidence, and the lineage are
  present
- **AND** a reference to the full evidence in the console is included.

#### Scenario: A statistical tie reads as a tie

- **WHEN** a delivered change's verified delta has an interval overlapping the baseline
- **THEN** the pull request presents it as a tie
- **AND** it is not described as an improvement.

### Requirement: Re-delivering the same change to the same target SHALL update the existing pull request

Re-delivery of the same configuration hash and source revision to the same target SHALL update the
existing pull request rather than opening another, including under retries, restarts, and concurrent
attempts.

#### Scenario: A retried delivery does not open a second pull request

- **WHEN** a delivery is retried after a failure
- **THEN** exactly one pull request exists for that configuration hash, source revision, and target.

#### Scenario: Concurrent deliveries produce one pull request

- **WHEN** two deliveries for the same configuration hash, source revision, and target run concurrently
- **THEN** exactly one pull request exists afterwards
- **AND** the other updates it rather than creating a duplicate.

### Requirement: A superseded delivery's pull request SHALL be closed with its reason stated

When a newer verified proposal supersedes one with an open pull request, the superseded pull request
SHALL be closed and the reason SHALL be stated on it.

#### Scenario: Supersession closes the older pull request

- **WHEN** a newer verified proposal supersedes one already delivered and open
- **THEN** the superseded pull request is closed
- **AND** the reason for closing is stated on it.

#### Scenario: A superseded pull request is not left open

- **WHEN** supersession occurs
- **THEN** the older pull request does not remain open awaiting review
- **AND** the reviewer is not left with two candidates for one decision.

### Requirement: Open platform-authored pull requests per repository SHALL be bounded, and reaching the bound SHALL be reported

The number of simultaneously open platform-authored pull requests per repository SHALL be bounded.
Reaching the bound SHALL be reported, and deliveries SHALL NOT be silently discarded.

#### Scenario: A burst cannot exceed the bound

- **WHEN** more deliveries are attempted than the bound permits
- **THEN** the number of simultaneously open platform-authored pull requests does not exceed the bound.

#### Scenario: Reaching the bound is reported, not silent

- **WHEN** the bound is reached and a further delivery is attempted
- **THEN** the condition is reported
- **AND** the undelivered proposal is not silently discarded.

### Requirement: The platform SHALL NOT merge a pull request below the Autonomous automation level

Below the Autonomous automation level the platform SHALL open and update pull requests and SHALL NOT
merge them. Under Autonomous it SHALL merge only a change that passed the verification gate.

#### Scenario: Below Autonomous, a human merges

- **WHEN** a pull request is delivered at an automation level below Autonomous
- **THEN** the platform does not merge it
- **AND** it remains for a human to review and merge.

#### Scenario: Under Autonomous, only a gate-passed change merges

- **WHEN** the automation level is Autonomous and a change that did not pass the gate is present
- **THEN** the platform does not merge it.

### Requirement: Delivery SHALL be entitlement-gated server-side

Delivery SHALL be permitted only for customers whose plan includes it, and automatic merging only at
the plan that includes it. The gate SHALL be enforced by the platform, not by the client.

#### Scenario: Delivery below the entitled plan is refused

- **WHEN** delivery is requested for a customer whose plan does not include it
- **THEN** the request is refused server-side
- **AND** the refusal does not depend on the client having checked first.

#### Scenario: Automatic merging below the entitled plan is refused

- **WHEN** automatic merging is requested for a customer whose plan does not include it
- **THEN** it is refused server-side.

### Requirement: An active halt SHALL stop delivery, and an unreadable halt state SHALL fail closed

An active fleet-wide or per-tenant halt SHALL stop delivery within its scope, taking effect without a
deploy. If the halt state cannot be read, delivery SHALL NOT proceed.

#### Scenario: An armed halt stops delivery

- **WHEN** a halt is armed for a scope and a delivery within that scope is attempted
- **THEN** no pull request is opened
- **AND** the halt took effect without a deploy.

#### Scenario: An unreadable halt state prevents delivery

- **WHEN** the halt state cannot be read
- **THEN** delivery does not proceed
- **AND** the safe default is to withhold delivery rather than to deliver.

### Requirement: A repository with verified proposals and no delivery route SHALL surface that condition

A repository that has verified proposals and no configured delivery route SHALL surface that condition
as a reported state. Proposals SHALL NOT accumulate without indication.

#### Scenario: Missing route is reported, not silent

- **WHEN** verified proposals exist for a repository with no configured delivery route
- **THEN** the condition is reported
- **AND** it is distinguishable from having produced no proposals.

#### Scenario: The condition names a next action

- **WHEN** the missing-route condition is surfaced
- **THEN** it indicates what configuring a route would require
- **AND** it is not rendered as an empty result.

### Requirement: Loss of delivery capability SHALL be reported as degraded, never silent

Loss of the ability to deliver — an installation removed, or a continuous-integration credential
expired or rotated — SHALL produce a reported degraded state.

#### Scenario: A removed installation is reported

- **WHEN** a customer removes the application installation
- **THEN** the platform reports the affected repositories as unable to receive deliveries
- **AND** delivery does not simply stop without indication.

#### Scenario: An expired CI credential is reported

- **WHEN** the continuous-integration credential used for delivery is expired or rotated away
- **THEN** the condition is reported as degraded
- **AND** it is distinguishable from having no proposals to deliver.

### Requirement: The platform SHALL write only pull requests and their branches

The platform SHALL create and update only pull requests and the branches backing them. It SHALL NOT
push directly to a protected branch, create tags or releases, or file issues.

#### Scenario: No write outside pull requests and their branches

- **WHEN** the platform's forge writes are enumerated
- **THEN** they consist only of pull requests and the branches backing them
- **AND** no direct push to a protected branch, tag, release, or issue is performed.
