## ADDED Requirements

### Requirement: Every metric/trace event SHALL carry the full seven-tag set

Every event emitted by any subsystem SHALL carry `{variant_id, run_id, node_id, case_id, seed,
timestamp, config_hash}`. This tag set is the highest-leverage decision on the project — it is what
makes slicing, comparison, reproducibility, and attribution possible. Cross-reference:
`docs/prd/P0-foundations.md` §6 (FR8) and §9.3.

#### Scenario: A fully-tagged event validates
- **WHEN** an event is emitted with all seven tags plus a typed payload
- **THEN** it validates against `metric-event.schema.json`.

#### Scenario: Each tag answers a required downstream slice
- **WHEN** the Eval Harness queries the event stream
- **THEN** it can slice by `variant_id`/`config_hash` (which configuration), `node_id` (per-node
  attribution), `case_id` (per-case / per-failure-cluster), and `seed` (multi-seed confidence
  intervals), because each is a present tag on every event.

### Requirement: All seven tags SHALL be non-null and an untagged event SHALL be rejected

No event missing any of the seven tags SHALL be persisted. Enforcement is layered: the emission
boundary SHALL reject an event missing a tag, and the relational store SHALL declare all seven tag
columns `NOT NULL`. (FR9, NFR3 — target: zero untagged events reach any store.)

#### Scenario: An event missing config_hash is rejected at emission
- **WHEN** a subsystem attempts to emit an event with a null or absent `config_hash`
- **THEN** the event is rejected at the emission boundary and is not written to any store.

#### Scenario: The database refuses an untagged row
- **WHEN** an insert into the eval-results table omits `seed`
- **THEN** the `NOT NULL` constraint rejects the write, so application code that forgot the tag cannot
  bypass the contract.

### Requirement: The metric event schema SHALL carry a typed payload and be additively extensible

Each event SHALL carry a typed payload: `metric_name`, `value`, and `unit`. The schema SHALL permit
optional additional dimensions (e.g. `node_kind`, `invocation_id`) to be added without breaking
existing consumers. (FR10, NFR1.)

#### Scenario: Adding an optional dimension does not break existing consumers
- **WHEN** an optional `node_kind` dimension is added to the schema and MINOR is bumped
- **THEN** events authored against the previous MINOR still validate
- **AND** a consumer pinned to the MAJOR still parses events carrying the new dimension.

#### Scenario: A payload without a unit is rejected
- **WHEN** an event carries `metric_name` and `value` but no `unit`
- **THEN** it is rejected as invalid, because the payload type is incomplete.

### Requirement: The metric event schema SHALL align with OpenTelemetry GenAI semantic conventions

Events SHALL map onto OpenTelemetry spans/metrics using the GenAI semantic conventions, so the platform
emits against one instrumentation standard rather than a bespoke logging layer. The seven tags SHALL be
expressible as OTel attributes. (FR11, NFR6.)

#### Scenario: An event maps to an OTel span attribute set
- **WHEN** a runtime node execution emits an event
- **THEN** the seven tags are carried as OTel attributes per the conventions doc
- **AND** the OTel conventions doc forbids placing prompt text, PII, or secrets in span attributes.

### Requirement: The tag set SHALL respect the cardinality budget for time-series storage

Low-to-moderate cardinality tags (`variant_id`, `node_id`, `seed`, plus `metric_name`) SHALL be usable
as TSDB series labels; high-cardinality identifiers (`case_id`, `run_id`, `invocation_id`,
content-hash references) SHALL NOT be TSDB series labels and SHALL instead live as span attributes and
relational columns / exemplars. This keeps active series within budget (~3×10⁴ per optimization run;
see PRD §8.2). (NFR4.)

#### Scenario: case_id does not become a time-series label
- **WHEN** metrics for a run of 200 cases are written to the TSDB
- **THEN** `case_id` is not used as a series label (which would multiply series 200×)
- **AND** the same event's `case_id` is retained as a span attribute / Postgres column for slicing.

#### Scenario: Series count stays within budget at target scale
- **WHEN** an optimization run of 20 variants × 20 nodes × ~15 metric names × 5 seeds emits metrics
- **THEN** the resulting active series count is on the order of 3×10⁴, within TSDB comfort, because
  only the low-cardinality tags are labels.

### Requirement: A hand-written metric-event sample SHALL validate in CI and a missing-tag sample SHALL fail

The repo SHALL contain a valid metric-event sample fixture and at least one invalid fixture missing a
required tag; CI SHALL validate the former and assert the latter fails the build. (NFR8, M0 exit.)

#### Scenario: CI enforces the event schema as a gate
- **WHEN** CI runs the schema-validation job
- **THEN** the valid metric-event sample passes validation
- **AND** a sample missing `run_id` fails validation, failing the build.
