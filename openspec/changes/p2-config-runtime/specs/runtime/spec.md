# Runtime — Spec Delta (P2)

Product rationale: [`../../../../docs/prd/P2-config-runtime.md`](../../../../docs/prd/P2-config-runtime.md) §6 (FR11–FR18).

Covers the Loader, provider gateway, Executor, and the idempotency/reproducibility guarantees.

## ADDED Requirements

### Requirement: The Loader SHALL resolve every *_ref at invocation time and fail closed on any unresolved reference

At invocation time the Loader SHALL resolve every `model_ref`, `prompt_ref`, `skill_ref`, and
`context_policy` in the Variant Spec against the registries. If any reference does not resolve, the
Loader SHALL abort the run before any node executes — no partial execution, no side effects.

#### Scenario: Dangling ref aborts before node 1
- **WHEN** a Variant Spec contains a `prompt_ref` that does not resolve to a registry entry
- **THEN** the run aborts during resolution
- **AND** no node executes, no provider call is made, and no partial run record is written

### Requirement: Models SHALL be invoked through a unified provider gateway so that provider swaps are transparent

All model calls SHALL pass through a single provider gateway that normalizes request and response
shapes across providers. Swapping a node's provider SHALL require changing only its `model_ref`,
with no change to workflow code.

#### Scenario: Provider swap changes only the model_ref
- **WHEN** a node's `model_ref` is changed from an Anthropic model entry to an OpenAI model entry
  and nothing else in the Variant Spec or workflow code changes
- **THEN** the run executes successfully against OpenAI
- **AND** the node receives a normalized response of the same shape it received from Anthropic
- **AND** no workflow source code is modified

### Requirement: The gateway SHALL apply per-call timeouts and bounded backoff and SHALL source secrets from a manager, never exposing them

Every provider call SHALL carry a timeout and SHALL retry transient failures with bounded
exponential backoff. Provider credentials SHALL be sourced from a secrets manager and SHALL NOT
appear in the Variant Spec, database rows, logs, error messages, or run records.

#### Scenario: Slow provider is bounded by timeout
- **WHEN** a provider does not respond within the configured per-call timeout
- **THEN** the call is aborted at the timeout and retried with backoff up to the retry limit
- **AND** the executor is not blocked indefinitely by the slow provider

#### Scenario: Secrets never leak
- **WHEN** a run completes and its logs, run records, and error messages are inspected
- **THEN** no provider API key or secret value appears in any of them

### Requirement: The Executor SHALL walk the node graph through the shim and pass node I/O through the typed contract

The Executor SHALL execute nodes in the Variant Spec's declared ordering, each through the shim,
and SHALL pass each node's output through the P0 typed I/O contract before it becomes a downstream
node's input.

#### Scenario: End-to-end graph run
- **WHEN** a hardcoded 3-node graph is executed with a valid Variant Spec
- **THEN** each node runs in declared order through the shim
- **AND** each node's output is validated against the typed I/O contract before feeding the next
  node
- **AND** the run ends with per-node input/output records and a terminal `succeeded` status

### Requirement: The Executor SHALL halt on a typed-contract violation rather than passing malformed data downstream

When a node's I/O violates the typed contract, the Executor SHALL halt the run with a typed error
identifying the node and dimension, and SHALL NOT pass the malformed data to a downstream node.

#### Scenario: Contract halt
- **WHEN** node `A` emits an output that violates the input contract of downstream node `B`
- **THEN** the run halts with a typed error naming node `A` and the violated contract
- **AND** node `B` does not execute on the malformed input
- **AND** the run's terminal status is `halted`, not `succeeded`

### Requirement: A run of a given config_hash + seed SHALL be reproducible

Given identical `{config_hash, seed}`, the Runtime SHALL resolve to byte-identical configuration
and SHALL propagate the identical seed to every provider call and stochastic step.

#### Scenario: Same config_hash + seed replays identically
- **WHEN** the same `{config_hash, seed}` is run twice
- **THEN** both runs produce a byte-identical resolved configuration
- **AND** each provider call in the second run receives the same seed as the corresponding call in
  the first run

### Requirement: Provider calls SHALL be idempotent under retry — no double-charge and no double-write

A retried node invocation SHALL carry a stable idempotency key so the provider is not charged
twice, and run-result writes SHALL be guarded so a retry does not create duplicate records.

#### Scenario: Retry does not double-charge
- **WHEN** a node's provider call fails transiently after the provider has processed it, and the
  gateway retries with the same idempotency key `{run_id, node_id, attempt_group}`
- **THEN** the provider de-duplicates on the idempotency key and the node is charged exactly once

#### Scenario: Retry does not double-write
- **WHEN** two write attempts for the same `(run_id, node_id, attempt_group)` race
- **THEN** the unique constraint causes exactly one `node_execution` row to be written
- **AND** the second attempt is a caught conflict, not a duplicate row

### Requirement: Each run SHALL be identified by a run_id and persist per-node I/O and a terminal status queryable by the UI

Each run SHALL have a `run_id` and SHALL persist, per node, the input and output (as content-hashed
blob references) and a terminal status of `succeeded`, `failed`, or `halted`, queryable by the
run/inspect UI.

#### Scenario: UI can read run state and per-node I/O
- **WHEN** the bare run/inspect UI requests a run by `run_id`
- **THEN** it receives the terminal status and, for each executed node, references to its input and
  output blobs
- **AND** a run still in progress reports a non-terminal status distinct from `succeeded`,
  `failed`, and `halted`
