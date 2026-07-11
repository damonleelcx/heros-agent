# Typed Contracts — P5 Delta

Cross-reference: [`../../../../docs/prd/P5-contracts-rearrange-tracing.md`](../../../../docs/prd/P5-contracts-rearrange-tracing.md) §6 (FR1–FR7).

Enforces the per-node `io_contract` reserved on every IR node since P0 as an ordering-coherence
validator with adapter auto-insertion. The load-bearing property: **no silently-broken reorder**.

## ADDED Requirements

### Requirement: The system SHALL decide producer→consumer schema satisfaction with a single predicate shared with the runtime Executor

Ordering coherence SHALL be decided by one predicate `Satisfies(output_schema, input_schema)` — a
structural-subtype check over the P0 `io_contract` JSON Schemas: every field the consumer *requires*
SHALL be present in and type-compatible with the producer's `output_schema`, while extra producer
fields are permitted. This SHALL be the **same** predicate the P2 Runtime Executor uses to pass node
I/O through the typed contract at runtime, so a statically-validated ordering and its runtime
enforcement never disagree.

#### Scenario: A producer missing a consumer-required field does not satisfy the contract
- **WHEN** producer A's `output_schema` lacks a field that consumer B's `input_schema` marks required
- **THEN** `Satisfies(A.output, B.input)` returns `mismatch` and names the missing field
- **AND** an A→B ordering is not coherent as-is.

#### Scenario: Extra producer fields do not break satisfaction
- **WHEN** producer A emits every field B requires plus additional fields B does not consume
- **THEN** `Satisfies(A.output, B.input)` returns `ok`.

#### Scenario: Static verdict and runtime enforcement agree
- **WHEN** an ordering is judged **coherent** by `ValidateOrdering` and then executed by the P2 Runtime
- **THEN** the run does **not** halt on a typed-contract violation
- **AND** an ordering the validator **rejects** is not runnable.

### Requirement: The ordering-coherence verdict SHALL be total, pure, and deterministic over every edge

`ValidateOrdering(ir, ordering, catalog)` SHALL apply `Satisfies` to **every** producer→consumer data
edge and return exactly one of `coherent`, `adapted(adapters)`, or `rejected(diagnostics)`. There SHALL
be no "unknown" or "allowed-anyway" outcome for any edge. The verdict SHALL be a pure function of the
node schemas and the adapter catalog: the same ordering over the same IR SHALL yield the same verdict
and the same inserted adapters on every evaluation.

#### Scenario: Every edge is classified — no escape hatch
- **WHEN** an ordering is validated
- **THEN** each producer→consumer edge is classified coherent, adaptable, or incoherent
- **AND** no edge is left unclassified or admitted as coherent despite a mismatch.

#### Scenario: Validation is deterministic
- **WHEN** the same reordering over the same IR is validated twice
- **THEN** the two verdicts are identical
- **AND** any inserted adapters are identical.

### Requirement: The system SHALL auto-insert an explicit, validated adapter node where a mismatch is bridgeable

Where a producer→consumer mismatch is **adaptable** by a transform from the fixed typed adapter catalog
(field rename, projection, wrap/unwrap, default-fill, declared format coercion), the system SHALL
synthesize an **adapter node**, insert it on the edge in the resulting Variant Spec, and represent it as
an **explicit, inspectable node carrying its own `io_contract`** — never a hidden coercion. The adapter
SHALL itself be validated: its `input_schema` satisfied by the upstream producer and its `output_schema`
satisfying the downstream consumer.

#### Scenario: A field-rename mismatch is bridged by an inserted adapter
- **WHEN** producer A emits `answer` and consumer B requires `response` of the same type, and a rename
  adapter bridges them
- **THEN** the system inserts a rename adapter node A→adapter→B in the Variant Spec
- **AND** the adapter is an explicit node carrying its own `io_contract`
- **AND** the resulting ordering is `adapted`, and the committed spec runs without a runtime contract
  halt.

#### Scenario: An adapter that would drop a consumer-required field is refused
- **WHEN** the only candidate adapter would satisfy the consumer only by discarding a field the consumer
  requires, or would silently lose data
- **THEN** the system does **not** insert that adapter
- **AND** the edge is treated as incoherent (rejected), not silently coerced.

### Requirement: The system SHALL reject an incoherent reorder and SHALL NOT persist it as runnable

When a proposed ordering contains an **incoherent** edge with no admissible catalog adapter, the system
SHALL **reject** the ordering with a typed diagnostic that names the producer node, the consumer node,
and the specific schema field(s) that fail to match, and SHALL NOT persist the incoherent ordering as a
runnable Variant Spec. This is the "no silently-broken reorder" guarantee.

#### Scenario: A consumer placed before its data producer is rejected, not saved
- **WHEN** a reorder places consumer B (which requires field `summary`) ahead of the only producer of
  `summary`, and no adapter can supply `summary`
- **THEN** `ValidateOrdering` returns `rejected` with a diagnostic naming B, the missing producer, and
  the field `summary`
- **AND** the incoherent ordering is not persisted as a runnable Variant Spec
- **AND** no partial or "best-effort" runnable spec is produced.

#### Scenario: The diagnostic is specific enough to act on
- **WHEN** an ordering is rejected
- **THEN** the diagnostic identifies the exact producer, consumer, and mismatching field(s)
- **AND** it is attributable to the specific offending edge, not a whole-graph error.

### Requirement: A coherent ordering SHALL produce a new lineage-tracked Variant Spec

A coherent ordering — including one made coherent by inserted adapters — SHALL produce a new Variant
Spec with a new `config_hash` and `parent_variant_id` lineage to the spec it was derived from, so the
arrangement is diffable and comparable on the P4 leaderboard.

#### Scenario: An adapter-augmented coherent ordering yields a diffable variant
- **WHEN** a reorder is made coherent by an inserted adapter and committed
- **THEN** a new Variant Spec is produced with a new `config_hash`
- **AND** it carries `parent_variant_id` lineage to the source spec
- **AND** the inserted adapter appears in the spec's node list and its diff against the parent.
