# Metrics & Observability — Spec Delta (P2.5)

Product rationale: [`../../../../docs/prd/P2.5-metrics-observability.md`](../../../../docs/prd/P2.5-metrics-observability.md)
§6 (FR1–FR19) and §7 (NFR1–NFR10).

Covers auto-instrumentation at the provider gateway, the operational metric taxonomy, mandatory
full-tag emission, drillable spans, three-store routing keyed by `config_hash`, cardinality/label
discipline, secrets-never-in-traces, the evaluator-plugin interface stub, degrade-safe idempotent
emission, and the live run-monitoring view. Builds on P0's `metric-event-schema` and
`storage-and-lineage` and P2's `runtime`/`config-layer` gateway seam.

## ADDED Requirements

### Requirement: Operational metrics SHALL be auto-instrumented at the provider gateway with no user code

Collection SHALL be auto-instrumented at the provider gateway so that every provider call
and every node execution emits its operational metrics and spans **without any user code, per-node
annotation, or metrics API call by a workflow author**. No operational metric SHALL require a
workflow author to instrument a call site; a node cannot be executed through the Runtime without
being instrumented.

#### Scenario: A workflow with zero telemetry code is fully instrumented
- **WHEN** a fixture workflow containing no telemetry code and no per-node annotation is executed
  through the Runtime
- **THEN** the full operational metric set (latency, cost, tokens, reliability, throughput) is
  emitted for every provider call
- **AND** a node span is emitted for every node execution
- **AND** the workflow author added zero lines to obtain it.

#### Scenario: An un-instrumented node is not possible
- **WHEN** a node executes through the provider gateway
- **THEN** its operational metrics and span are emitted by the substrate itself
- **AND** there is no execution path through the Runtime that produces an un-instrumented provider call.

### Requirement: The substrate SHALL collect the full operational metric taxonomy per call

For every provider call the substrate SHALL collect: **latency** (total, time-to-first-token,
tokens-per-second); **cost** (input, output, and cache token counts × the model's price); **tokens**
(prompt, completion, thinking, cache-hit, and context-window utilization); **reliability** (error
rate, timeout rate, retry count, rate-limit hits); and **throughput/concurrency** for the run.
Metrics SHALL be attributable per node and end-to-end. Quality, agent-specific, and safety metrics
are out of scope for this phase (P4/P5).

#### Scenario: A single provider call yields the full operational set
- **WHEN** one provider call completes through the gateway
- **THEN** latency (total, TTFT, tokens/sec), cost (from input/output/cache tokens × price), tokens
  (prompt/completion/thinking/cache-hit + context-window utilization), and reliability (error/
  timeout/retry/rate-limit) metrics are all emitted for that call.

#### Scenario: Cost aggregates per node, per run, and cumulatively
- **WHEN** a run executes multiple nodes
- **THEN** cost is queryable per node, summed per run, and available cumulatively
- **AND** each cost figure is derived from token counts × the model's price.

### Requirement: Every metric SHALL be emitted as a typed event carrying the full seven-tag set

Every collected metric SHALL be emitted as a typed event carrying the full seven-tag set
`{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}` populated from the run context
at emission time, plus the P0 typed payload (`metric_name`, `value`, `unit`).

#### Scenario: A fully-tagged operational event validates
- **WHEN** a latency metric is emitted for a node execution
- **THEN** the event carries all seven tags populated from the run context plus `metric_name`,
  `value`, and `unit`
- **AND** it validates against `metric-event.schema.json`.

#### Scenario: Every downstream slice is answerable from the tags
- **WHEN** a downstream consumer queries the emitted events
- **THEN** it can slice by `variant_id`/`config_hash` (configuration), `node_id` (per-node
  attribution), `case_id` (per-case / per-failure-cluster), and `seed` (multi-seed) because each is a
  present tag on every event.

### Requirement: An event missing any tag SHALL be rejected at the emission boundary and reach no store

An event missing any of the seven tags SHALL be rejected at the emission boundary and SHALL NOT be
written to any store. Enforcement SHALL be layered with P0's `NOT NULL` tag columns in the relational
store, so a missing tag is refused both at the boundary and at the row. (Target: zero untagged events
reach any store.)

#### Scenario: A missing-config_hash event is rejected at emission
- **WHEN** a subsystem attempts to emit a metric event with a null or absent `config_hash`
- **THEN** the emission boundary rejects it
- **AND** the event is not written to the TSDB, the span store, or Postgres.

#### Scenario: The database refuses an untagged row as a second line of defense
- **WHEN** a write to the eval-results table omits `seed`
- **THEN** the `NOT NULL` constraint rejects the write, so code that bypassed the boundary still cannot persist an untagged row.

### Requirement: Every metric event and span SHALL be keyed by config_hash

Every metric event and every span SHALL carry `config_hash` so that all telemetry is attributable to
an exact configuration and reproducible from lineage. Metrics for the same configuration under
different seeds SHALL roll up under one `config_hash`.

#### Scenario: Telemetry is attributable to an exact configuration
- **WHEN** a consumer filters telemetry by a given `config_hash`
- **THEN** it receives exactly the metrics and spans produced by that configuration
- **AND** the same configuration run under seeds 1, 2, and 3 rolls up under that single `config_hash`.

### Requirement: Each run SHALL emit a drillable OpenTelemetry trace through one instrumentation standard

Each run SHALL emit an OpenTelemetry trace: one run span, one span per node execution, and tool calls
as child spans, drillable per-run in the span store. Spans and metrics SHALL be emitted through a
single OpenTelemetry standard using the GenAI semantic conventions (P0 doc), not a bespoke logging
layer, and the seven tags SHALL be carried as OTel attributes.

#### Scenario: A run produces a drillable span hierarchy
- **WHEN** a run of a 3-node graph with a tool call completes
- **THEN** the span store holds one run span, three node spans, and the tool call as a child span
- **AND** an operator can drill from the run span down to any node and its tool-call child.

#### Scenario: Telemetry uses GenAI conventions, not bespoke logging
- **WHEN** a node execution emits telemetry
- **THEN** the span and its metrics follow the OTel GenAI semantic conventions
- **AND** the seven tags appear as OTel attributes.

### Requirement: Telemetry SHALL be routed to three stores by shape, each keyed by config_hash

The substrate SHALL route telemetry to three stores by shape: spans → an OTel-compatible span store
(Tempo/Jaeger); metrics → a TSDB (Prometheus/ClickHouse); eval results → Postgres — every record
keyed by `config_hash`. Each query shape SHALL be served from the store built for it and be
filterable by `config_hash`.

#### Scenario: Each query shape hits the store built for it
- **WHEN** a consumer asks for a metric trend over time, a per-run trace drill-down, and a
  variant-vs-variant comparison table
- **THEN** the trend is served from the TSDB, the drill-down from the span store, and the comparison
  from Postgres
- **AND** each query is filterable by `config_hash`.

#### Scenario: Eval-results rows enforce the tagging and lineage invariants
- **WHEN** a quality-metric row is written to Postgres
- **THEN** all seven tag columns are `NOT NULL`, the row is keyed by `config_hash`, and foreign keys
  to variant, node, and case hold.

### Requirement: Cardinality discipline SHALL keep high-cardinality identifiers out of TSDB series labels

Only low-to-moderate cardinality tags (`variant_id`, `node_id`, `seed`, plus `metric_name`) SHALL be
used as TSDB series labels. High-cardinality identifiers (`case_id`, `run_id`, `invocation_id`, and
content-hash references) SHALL NOT be TSDB series labels; they SHALL instead live as span attributes,
Postgres columns, or TSDB exemplars. This keeps active series within budget (~3×10⁴ per optimization
run) rather than exploding to ~10⁸.

#### Scenario: case_id does not become a time-series label
- **WHEN** metrics for a run over 200 cases are written to the TSDB
- **THEN** `case_id` is not used as a series label (which would multiply series 200×)
- **AND** the same event's `case_id` is retained as a span attribute and a Postgres column for slicing.

#### Scenario: Series count stays within budget at target scale
- **WHEN** an optimization run of 20 variants × 20 nodes × ~15 metric names × 5 seeds emits metrics
- **THEN** the active series count is on the order of 3×10⁴, within TSDB comfort, because only the
  low-cardinality tags are labels
- **AND** `run_id` and `invocation_id` are exemplars/attributes, never labels.

### Requirement: No secret, prompt, completion, or PII SHALL appear in any span, metric label, or log

No provider secret, API key, prompt text, completion text, or PII SHALL appear in any span attribute,
metric label, or log line. Prompts and outputs SHALL be referenced by content-hashed blob reference
only; the collector SHALL scrub such data before any store receives it. Provider credentials SHALL be
sourced from a secrets manager and never reach telemetry.

#### Scenario: A secret-bearing run leaves no secret in telemetry
- **WHEN** a run executes whose prompt embeds a secret-shaped string and whose gateway uses a
  provider API key
- **THEN** no API key, secret value, prompt text, completion text, or PII appears in any span
  attribute, metric label, or log
- **AND** the prompt and output are represented only by content-hashed blob references.

### Requirement: The substrate SHALL define an evaluator-plugin interface stub for built-in and user-defined evaluators

The substrate SHALL define a stable evaluator-plugin interface: a scoring function that consumes a
run's trace and emits quality-metric events under the same seven-tag contract, supporting both
built-in and user-defined evaluators (mirroring the Skill Registry pattern), so that P4 slots quality
metrics in without re-plumbing collection, tagging, or storage. The interface SHALL be exercised by at
least one trivial built-in reference evaluator whose events carry the full seven-tag set and land in
the eval-results store. Real quality evaluators are deferred to P4.

#### Scenario: The reference evaluator proves the seam end to end
- **WHEN** the built-in reference evaluator runs over a completed run's trace
- **THEN** it emits quality-metric events each carrying the full seven-tag set
- **AND** those events are written to the eval-results store in Postgres, keyed by `config_hash`.

#### Scenario: An evaluator cannot emit an under-tagged event
- **WHEN** an evaluator is invoked with a `RunContext`
- **THEN** the `RunContext` carries the seven tags so the evaluator's emitted events are fully tagged
  by construction, and an event missing a tag is rejected at the same emission boundary as
  operational metrics.

#### Scenario: A user-defined evaluator registers without re-plumbing the substrate
- **WHEN** a new evaluator is registered through the interface
- **THEN** its output events flow through the same tagging, cardinality, and storage path as built-in
  evaluators, with no change to collection, tagging, or storage.

### Requirement: Metric emission SHALL be idempotent under run retries

Metric emission and cost accounting SHALL be idempotent under P2's retry model: a retried node
invocation (`{run_id, node_id, attempt_group}`) SHALL NOT double-count cost and SHALL NOT
double-write a metric event or span for the same logical invocation.

#### Scenario: A retry is measured once
- **WHEN** a node's provider call fails transiently and is retried under the same
  `{run_id, node_id, attempt_group}`
- **THEN** the invocation contributes exactly one cost unit
- **AND** exactly one metric event and one span are written for that logical invocation.

### Requirement: Telemetry emission SHALL be non-blocking and SHALL NOT fail a run when a backend is unavailable

Instrumentation SHALL emit off the provider-call request path and SHALL add less than 5 ms p50
overhead per call. A telemetry-backend outage SHALL degrade telemetry but SHALL NOT block, delay, or
fail a run.

#### Scenario: A collector outage does not fail a paid run
- **WHEN** the collector or a telemetry backend is unavailable during a run
- **THEN** the run still completes successfully
- **AND** telemetry for the affected window is dropped or buffered, but no provider call is blocked or
  failed by the outage.

### Requirement: A live run-monitoring view SHALL stream per-node operational metrics with first-class states

A minimal live run-monitoring view SHALL display a run's per-node latency, cost, and token metrics as
they stream in, reading live/terminal status from the run record. Loading, error, and empty states
SHALL be first-class, and a failed or timed-out node SHALL be visually distinct from a
slow-but-healthy node.

#### Scenario: In-flight metrics stream into the monitor
- **WHEN** a run is in progress
- **THEN** the view shows each node's latency, cost, and token metrics as they arrive
- **AND** status is read from the run record rather than inferred client-side.

#### Scenario: A failed node is distinguishable from a slow one
- **WHEN** one node times out and another is merely slow
- **THEN** the timed-out node is rendered distinctly (driven by its reliability metric) from the
  slow-but-healthy node
- **AND** loading, empty, and error states are each rendered distinctly.
