# Wiring Safety — Spec Delta (P15)

Product rationale: [`../../../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../../../docs/prd/P15-workflow-wiring-optimization.md)
§6 (FR9–FR14), §7. Design reasoning: [`../../design.md`](../../design.md) Decisions 3, 4.

Covers the typed-contract coherence gate as a first-class requirement: a Variant Spec whose reordering
or rewiring violates a typed I/O contract is **rejected at compile**, unless a catalogued adapter
reconciles the mismatch — in which case the adapter is recorded explicitly and **ships in the same
reviewable diff**. This is the exact behavior [`openspec/AGENTS.md:87`](../../../../AGENTS.md) cites as its
canonical example of a behavioral requirement, made a shipped requirement.

> **Why the gate is fail-closed and the adapter is explicit.** Rearranging a graph can break it in a way
> content edits cannot: a node may consume a field only its predecessor produced. Catching that after a
> diff exists trades a safety guarantee for the convenience of not gating early. `GateReorder` returning
> **no runnable spec** on an incoherent verdict makes it *physically impossible* to hand a rejected
> ordering to the transform engine ([`rearrange.go:52-56`](../../../../../internal/variantspec/rearrange.go)).
> And a coercion that reconciles a mismatch invisibly is a value change hidden from review; recording it
> as an explicit adapter node in the diff keeps *an indirection never hides a value from review*.

## ADDED Requirements

### Requirement: A candidate ordering SHALL be validated before any codemod is generated

Every candidate ordering — from a merge, reorder, or prune — SHALL be validated by the typed-contract
coherence gate before any codemod, diff, or pull request is generated for it.

#### Scenario: Validation precedes transform

- **WHEN** a wiring candidate is produced
- **THEN** it is validated by the coherence gate before any codemod is generated
- **AND** no transform runs on an unvalidated ordering.

### Requirement: An ordering that violates a typed I/O contract SHALL be rejected at compile

A Variant Spec whose reordering or rewiring violates a typed I/O contract SHALL be rejected: the gate
SHALL return no runnable spec, and no codemod, diff, or pull request SHALL be generated from it.

#### Scenario: An incoherent reorder yields no runnable spec

- **WHEN** an ordering places a consumer before the producer of a field it requires
- **THEN** the gate returns no runnable spec
- **AND** no diff, codemod, or pull request is generated
- **AND** the rejection is a compile-time outcome, not a downstream build failure.

#### Scenario: The gate can go red

- **WHEN** a test submits a deliberately incoherent ordering
- **THEN** the gate rejects it and no runnable spec is produced
- **AND** the rejection is observable as a distinct verdict rather than a silent pass.

### Requirement: A catalogued adapter reconciling a mismatch SHALL be recorded and rewired explicitly

When a catalogued adapter can bridge a producer→consumer mismatch, the ordering SHALL be admitted, the
adapter SHALL be recorded as an explicit inserted-adapter node carrying its own I/O contract, and the
edge SHALL be rewired producer→adapter→consumer.

#### Scenario: An adapted verdict records the adapter node

- **WHEN** a reorder's mismatch is bridged by a catalogued adapter
- **THEN** the candidate carries an explicit inserted-adapter node with its own I/O contract
- **AND** the edge is rewired from producer→adapter and adapter→consumer.

### Requirement: An adapter SHALL be admissible only if it drops nothing the consumer requires

An adapter SHALL be admitted only if its input schema is satisfied by the producer and its output schema
satisfies the consumer. An adapter that would silently drop a consumer-required field SHALL be rejected,
and the ordering SHALL be rejected with it.

#### Scenario: A field-dropping adapter is refused

- **WHEN** the only candidate adapter for a mismatch would drop a field the consumer requires
- **THEN** the adapter is not admitted
- **AND** the ordering is rejected with no runnable spec.

#### Scenario: A satisfying adapter is admitted

- **WHEN** a candidate adapter's input is satisfied by the producer and its output satisfies the consumer
- **THEN** the adapter is admitted and the ordering is coherent.

### Requirement: An inserted adapter SHALL ship in the same reviewable diff

An inserted adapter SHALL be materialized as generated source that appears in the same reviewable diff as
the wiring change, present in the spec's node list and its diff against the parent. No coercion SHALL
exist outside that diff.

#### Scenario: The adapter is visible generated source

- **WHEN** a wiring change requires an inserted adapter
- **THEN** the adapter appears as generated source in the same diff a reviewer reads
- **AND** no runtime coercion reconciles the mismatch outside that diff.

### Requirement: Adapter insertion SHALL be deterministic

The identity of an inserted adapter and the order in which the catalog is tried SHALL be deterministic,
so that the same reordering yields the same inserted adapters and the same `config_hash` on every
evaluation.

#### Scenario: The same reorder yields the same adapter and hash

- **WHEN** the same reordering is evaluated twice
- **THEN** the inserted adapter identities are identical across the two evaluations
- **AND** the resulting `config_hash` values are identical.
