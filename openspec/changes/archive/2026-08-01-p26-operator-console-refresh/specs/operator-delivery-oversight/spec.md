# Operator Delivery Oversight — Spec Delta (P26)

Product rationale: [`../../../../../docs/prd/P26-operator-console-refresh.md`](../../../../../docs/prd/P26-operator-console-refresh.md)
§6 (FR48–FR52). Technical decisions: [`../../design.md`](../../design.md) D2, D9.

Covers oversight of the most consequential thing the platform does to a customer: change their repository.

> The asymmetry: **a merge is observed, never inferred.** A pull request that closed may have been merged,
> squashed, rebased, or abandoned, and only one of those is a delivery. So the surface carries three
> outcomes, and the third — *unknown* — is a real answer rather than a gap to be papered over with the most
> likely one.
>
> And the boundary that makes this capability safe: **it reads and it does nothing.** Delivery is downstream
> of verification and never a path around it, and the platform holds no forge credential by default — so an
> operator control that retried a delivery would need one.

## ADDED Requirements

### Requirement: The console SHALL show delivery records per tenant and as a cross-tenant aggregate

The console SHALL show the forge-delivery lifecycle: pull requests opened, their state, merges observed,
and deliveries that are undeliverable with their typed cause. Both a per-tenant view and a cross-tenant
aggregate SHALL exist.

#### Scenario: A support operator answers a delivery question without impersonating
- **WHEN** an operator with the granted capability opens the delivery surface for a tenant
- **THEN** that tenant's delivery records are shown with their state and any typed undeliverable cause
- **AND** answering the question requires no impersonation session.

#### Scenario: The fleet picture exists and drills down
- **WHEN** the cross-tenant delivery aggregate is opened
- **THEN** the fleet counts are shown
- **AND** each count offers the drill-down to the individual records behind it.

### Requirement: A merge SHALL be shown as observed, and the outcome SHALL have three states

The surface SHALL distinguish `merged`, `closed unmerged` and `state unknown`. A merge SHALL NOT be
inferred from a pull request closing.

#### Scenario: A closed pull request is not read as a merge
- **WHEN** a delivery's pull request is closed without being merged
- **THEN** the surface shows `closed unmerged`
- **AND** it does not show `merged`.

#### Scenario: Unknown is a rendered state, not a blank
- **WHEN** the merge outcome has not been observed
- **THEN** the surface shows `state unknown`
- **AND** it does not display the most likely outcome in its place.

### Requirement: The console SHALL show the change-delivery rollout state and its undeliverable causes

The surface SHALL show which rollout stage a change is in, together with the undeliverable count and the
typed causes behind it.

#### Scenario: A stalled rollout is visible with its cause
- **WHEN** a change's rollout has stopped progressing
- **THEN** the surface shows the stage it stopped at
- **AND** the undeliverable count is shown with its typed causes rather than as a single total.

#### Scenario: The cause is a stable identifier, not prose
- **WHEN** an undeliverable cause is rendered
- **THEN** it derives from a stable cause identifier
- **AND** the same cause renders identically wherever it appears.

### Requirement: The audit chain's merge coverage SHALL be stated honestly

The audit surface SHALL name which merge paths the hash-chained record covers — autonomous merges mirrored
into the chain — and SHALL NOT imply coverage of customer-CI-mediated deliveries. The delivery surface
SHALL link to the chain for the paths it does cover.

#### Scenario: The chain does not claim more than it holds
- **WHEN** an operator reads the audit surface
- **THEN** it states which merge paths are mirrored into the chain and which are not
- **AND** no wording implies the chain records every merge the platform is involved in.

#### Scenario: The covered path is reachable from the delivery surface
- **WHEN** a delivery corresponds to a merge path the chain covers
- **THEN** the delivery surface links to the corresponding chain entry.

### Requirement: The delivery surface SHALL be read-only

No control on this surface SHALL open, close, retry or merge a delivery, and none SHALL cause the platform
to hold or use a forge credential.

#### Scenario: No write exists on this surface
- **WHEN** the delivery surface's controls are enumerated
- **THEN** none performs, retries or cancels a delivery
- **AND** the surface performs no action against a forge.

#### Scenario: Verification is not bypassable from here
- **WHEN** an operator views an undeliverable delivery
- **THEN** no control exists that would deliver it anyway
- **AND** delivery remains downstream of verification.

### Requirement: Every cross-tenant delivery read SHALL be audited on the same code path as the read

A cross-tenant read SHALL record its actor, capability and scope in the audit chain, written on the same
code path as the read rather than by a poller.

#### Scenario: A cross-tenant view is logged
- **WHEN** an operator opens the cross-tenant delivery aggregate
- **THEN** an audit entry records the actor, the capability and the scope.

#### Scenario: A crash cannot leave a read unlogged
- **WHEN** the process fails after serving a cross-tenant read
- **THEN** no read exists that was served without its audit entry having been written on the same path.
