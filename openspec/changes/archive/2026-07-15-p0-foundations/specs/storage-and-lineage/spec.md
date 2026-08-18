## ADDED Requirements

### Requirement: config_hash SHALL be a deterministic hash over a canonicalized resolved configuration

`config_hash` SHALL be computed as a hash (SHA-256) over the canonical serialization of a fully
resolved configuration: `ir_version`, each node's resolved bindings (`model_ref@version`,
`prompt_ref@version`, `skill_refs[]@version`, `context_policy` + params), the node ordering/graph, and
provider inference params. Canonicalization SHALL sort object keys, normalize number representation,
and use UTF-8 with no insignificant whitespace. Cross-reference: `docs/prd/P0-foundations.md` §6
(FR12) and §8.4.

#### Scenario: Identical configurations produce identical hashes regardless of serialization order
- **WHEN** the same resolved configuration is serialized twice with differently-ordered object keys
- **THEN** both canonicalize identically and produce the same `config_hash`.

#### Scenario: Changing a bound registry version changes the hash
- **WHEN** one node's `prompt_ref` is repointed from version 3 to version 4
- **THEN** the resulting `config_hash` differs from the original.

### Requirement: config_hash SHALL exclude run-time-only values

`config_hash` SHALL NOT incorporate `run_id`, `seed`, or `timestamp`, so that the same configuration
executed under different seeds shares one `config_hash`. (FR13, NFR2.)

#### Scenario: The same config under different seeds shares one hash
- **WHEN** a configuration is run under seeds 1, 2, and 3
- **THEN** all three runs carry the same `config_hash`
- **AND** their multi-seed results roll up under that single configuration for confidence-interval
  computation.

#### Scenario: config_hash is stable across wall-clock time
- **WHEN** the same configuration is hashed on two different days
- **THEN** the `config_hash` is identical, because `timestamp` is excluded.

### Requirement: A config_hash SHALL resolve to exact registry versions and content-hashed blobs

Given a `config_hash`, the lineage SHALL be sufficient to reconstruct the exact configuration —
resolved model/prompt/skill/context versions, node ordering, and the content-hashed blobs used — so any
result is reproducible from lineage alone. `variant_id` is a stable logical label that MAY map to many
`config_hash` values over its edit history; a `config_hash` is immutable and content-defined. (FR14,
NFR2.)

#### Scenario: A run is reproduced from its lineage
- **WHEN** an engineer has only a `config_hash` and a `seed`
- **THEN** the lineage resolves the exact registry versions, node ordering, and blob content hashes
  needed to reconstruct and replay the configuration.

#### Scenario: One variant_id maps to many immutable config_hashes
- **WHEN** a user edits variant `v3`'s prompt binding three times
- **THEN** `v3` (a stable `variant_id`) is associated with three distinct immutable `config_hash`
  values, each independently replayable.

### Requirement: Large blobs SHALL be stored content-hashed in object storage and referenced, not inlined

Prompt and artifact blobs SHALL be stored in object storage keyed by the SHA-256 of their bytes, and
SHALL be referenced by content hash from events and eval results rather than inlined. (FR15, NFR7.)

#### Scenario: Identical prompts deduplicate
- **WHEN** 1,000 invocations render the identical prompt text
- **THEN** the object store holds one blob keyed by that content hash
- **AND** all 1,000 events reference it by hash rather than each storing the text.

#### Scenario: Events reference blobs rather than embedding them
- **WHEN** an event records a large prompt
- **THEN** the event carries the blob's content hash reference, and the bytes live only in object
  storage.

### Requirement: Data SHALL be routed to three stores by shape, all keyed by config_hash

The system SHALL route spans to an OpenTelemetry-compatible span store, metrics to a time-series
database, and eval results to Postgres — every record keyed by `config_hash` for reproducibility and
attribution. The choice SHALL be justified by the back-of-envelope volume estimate (PRD §8.2), not by
reflex. (FR16, NFR5, NFR7.)

#### Scenario: Each query shape hits the store built for it
- **WHEN** a user asks for a metric trend over time, a per-run trace drill-down, and a variant-vs-variant
  comparison table
- **THEN** the trend is served from the TSDB, the drill-down from the span store, and the comparison
  from Postgres — each keyed by `config_hash`.

#### Scenario: The store choice matches the estimated volume
- **WHEN** an optimization run produces ~10⁷ metric events, ~3×10⁶ spans, and ~2×10⁵ eval rows
- **THEN** the high-count numeric metrics land in the TSDB, the high-volume drill-down spans in the
  span store (sampled/retention-bounded), and the low-volume relational eval rows in Postgres.

### Requirement: The relational store SHALL enforce the tagging and lineage invariants structurally

Postgres SHALL enforce: all seven tag columns `NOT NULL`; a uniqueness constraint on `config_hash`
where a row represents a configuration (and on the natural key of an eval result); and foreign keys
from eval results to variant, node, and case. The database enforces the tagging contract that
application code will eventually forget. (FR17, NFR3.)

#### Scenario: A duplicate configuration insert is rejected
- **WHEN** two rows are inserted for the same configuration with the same `config_hash` where the row
  represents a configuration
- **THEN** the uniqueness constraint rejects the duplicate.

#### Scenario: An eval result with a dangling foreign key is rejected
- **WHEN** an eval-result row references a `variant_id` that does not exist
- **THEN** the foreign-key constraint rejects the write.

### Requirement: Both schemas SHALL evolve additively and be frozen at M0

The IR and metric-event schemas SHALL be versioned and evolve via expand-migrate-contract: add a
nullable/optional field, dual-write/backfill, then drop the old — so older variants keep resolving
throughout. A breaking change SHALL bump the MAJOR version. Both schemas SHALL be reviewed and frozen
at Milestone M0. (NFR1, M0 exit.)

#### Scenario: An IR field is renamed safely via expand-migrate-contract
- **WHEN** a node field must be renamed
- **THEN** the new field is added first (expand) while the old remains, both are populated (migrate),
  and only after consumers move is the old field removed (contract)
- **AND** older IR samples still validate at every step until the final MAJOR-bumping contract step.

#### Scenario: The M0 freeze gate holds
- **WHEN** M0 is declared complete
- **THEN** both schemas are reviewed and frozen, CI is green, and subsequent changes to either schema
  are additive within the MAJOR line or require an explicit MAJOR bump.
