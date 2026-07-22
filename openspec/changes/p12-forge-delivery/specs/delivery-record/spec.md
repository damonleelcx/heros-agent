# Delivery Record — Spec Delta (P12)

Product rationale: [`../../../../../docs/prd/P12-forge-delivery.md`](../../../../../docs/prd/P12-forge-delivery.md)
§6 (FR16–FR22) and §8.2. Architecture decision:
[`../../../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md`](../../../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md)
(blocker #2 and its rejected alternatives). Design reasoning:
[`../../design.md`](../../design.md) Decision 4.

Covers the record of what was delivered where: an **append-only** `delivery` record with its own
lifecycle, leaving `transform` immutable and untouched; the mode captured for audit; the **observed
merge** that gives verified-savings billing its input; and the delivery state the console shows.

> **Why this is a separate record rather than a column on `transform`.** A transform is produced
> **once** and is immutable by design — that immutability is what makes configuration-hash
> reproducibility checkable. A delivery has a **lifecycle**: opened, updated, superseded, closed,
> merged, possibly reverted — and the same transform may legitimately be delivered to more than one
> target. Forcing a lifecycle-bearing fact into an immutable row was the wrong shape before the
> blocker existed; the blocker merely made it visible.

## ADDED Requirements

### Requirement: Every delivery and state change SHALL append to a delivery record

Every delivery, and every subsequent change to its state, SHALL append an entry to a delivery record
keyed by the configuration hash, the source revision, and the delivery target.

#### Scenario: A delivery is recorded when it occurs

- **WHEN** a pull request is opened for a proposal
- **THEN** an entry is appended recording the configuration hash, source revision, target, and the
  resulting pull-request reference.

#### Scenario: Each state change appends rather than replaces

- **WHEN** a recorded delivery changes state
- **THEN** a new entry is appended
- **AND** the previous entry remains.

### Requirement: The delivery record SHALL be append-only

The delivery record SHALL NOT be mutated or deleted. A state change SHALL be expressed as a new entry,
so the full history of a delivery is reconstructable.

#### Scenario: The history of a delivery is reconstructable

- **WHEN** a delivery has passed through several states
- **THEN** every state it occupied is recoverable from the record in order
- **AND** no earlier state was overwritten.

#### Scenario: No interface mutates or deletes an entry

- **WHEN** the operations available on the delivery record are enumerated
- **THEN** none mutates or deletes an existing entry.

### Requirement: Delivery SHALL NOT modify the transform record

Recording a delivery SHALL NOT modify the transform record in any way. The transform record's
immutability SHALL remain intact.

#### Scenario: The transform record is unchanged by delivery

- **WHEN** a proposal is delivered and its outcome recorded
- **THEN** the corresponding transform record is byte-identical to what it was before delivery.

#### Scenario: Transform immutability still holds

- **WHEN** delivery is in use and the transform record's immutability is exercised
- **THEN** attempts to modify a transform are still rejected
- **AND** the immutability guarantee is not relaxed to accommodate delivery.

### Requirement: The delivery record SHALL capture which mode performed the delivery

Each delivery entry SHALL record whether it was performed through the CI-mediated mode or the hosted
application mode.

#### Scenario: An audit can identify the credential path

- **WHEN** a recorded delivery is examined after the fact
- **THEN** the mode that performed it is recoverable from the record
- **AND** it is possible to determine which credential path opened a given pull request.

### Requirement: A merge into the target branch SHALL be recorded against its delivery

A merge of a delivered pull request into the customer's target branch SHALL be recorded against that
delivery. A pull request closed without merging SHALL NOT be recorded as merged.

#### Scenario: A merge is recorded from an observation of the merge

- **WHEN** a delivered pull request is merged into the target branch
- **THEN** a merged state is appended to that delivery
- **AND** the record reflects an observed merge rather than an inference from the pull request closing.

#### Scenario: A close without merge is not a merge

- **WHEN** a delivered pull request is closed without being merged
- **THEN** a closed state is appended
- **AND** no merged state is recorded.

#### Scenario: Verified-savings computation has an observable input

- **WHEN** savings are computed for a period
- **THEN** each contributing delta corresponds to a delivery with a recorded merge
- **AND** a delta with no recorded merge does not contribute.

### Requirement: A revert after a merge SHALL appear as a further state, not as an overwrite

When a merged change is subsequently reverted, the revert SHALL be appended as a further state. The
merged state SHALL remain in the record.

#### Scenario: A revert is visible as a sequence

- **WHEN** a merged delivery is later reverted
- **THEN** the record contains the merged state followed by the reverted state
- **AND** the merged state was not removed or overwritten.

#### Scenario: A disputed period is answerable from the record

- **WHEN** a billed period containing a merge and a later revert is examined
- **THEN** both events and their order are recoverable from the record.

### Requirement: The console SHALL show each delivery's state and its originating proposal

The console SHALL display, for each delivery, its current state — open, merged, closed, or superseded —
and a link to the proposal that produced it.

#### Scenario: Delivery outcomes are visible

- **WHEN** a customer views their deliveries
- **THEN** each shows its current state
- **AND** the states open, merged, closed, and superseded are distinguishable from one another.

#### Scenario: A delivery links back to its proposal

- **WHEN** a delivery is displayed
- **THEN** the proposal that produced it is reachable from it
- **AND** the evidence behind the change can be reached without searching for it.
