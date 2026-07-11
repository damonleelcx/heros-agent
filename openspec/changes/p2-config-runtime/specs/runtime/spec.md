# Runtime — Spec Delta (P2)

Product rationale: [`../../../../docs/prd/P2-config-runtime.md`](../../../../docs/prd/P2-config-runtime.md) §6 (FR11–FR18).
Applies the source-transformation apply model per
[ADR-001](../../../../docs/adr/ADR-001-source-transformation-apply-model.md).

Covers the Loader, the transform application + build path, the provider gateway, the Executor, and
the idempotency/reproducibility guarantees.

## ADDED Requirements

### Requirement: The Loader SHALL resolve every *_ref at invocation time and fail closed on any unresolved reference

At invocation time the Loader SHALL resolve every `model_ref`, `prompt_ref`, `skill_ref`, and
`context_policy` in the Variant Spec against the registries. If any reference does not resolve, the
Loader SHALL abort before any transformation is generated — no diff, no worktree, no build, no run,
no side effects.

#### Scenario: Dangling ref aborts before any transform is generated
- **WHEN** a Variant Spec contains a `prompt_ref` that does not resolve to a registry entry
- **THEN** the run aborts during resolution
- **AND** no transformation is generated, no worktree is created, no node executes, no provider call
  is made, and no partial run record is written

### Requirement: The Runtime SHALL apply the transformation to an isolated worktree and build it before running

The Runtime SHALL check out an isolated git worktree/branch at the target `source_revision`, apply
the generated codemod there, and run the target's build. It SHALL run only a transformed working
copy that built successfully; it SHALL NOT mutate the user's working tree in place, and it SHALL
reject a transform whose build fails before any node executes.

#### Scenario: Build precedes any node execution
- **WHEN** a resolved Variant Spec's transformation is applied to an isolated worktree
- **THEN** the target build runs on that worktree before any node executes
- **AND** a build failure yields terminal status `build-rejected` with no node execution and no
  provider call
- **AND** the user's working tree at `source_revision` is unchanged

#### Scenario: Build cache reuse for an identical variant
- **WHEN** a run is requested for a `config_hash` whose transformed artifact is already built and
  cached
- **THEN** the Runtime reuses the cached build instead of regenerating and rebuilding
- **AND** the reused artifact is byte-identical to the originally built one

### Requirement: Models SHALL be invoked through a unified provider gateway so that provider swaps are transparent

All model calls made by the running transformed code SHALL pass through a single provider gateway
that normalizes request and response shapes across providers. Swapping a node's provider SHALL
require the transformation to rewrite only its `model_ref` at the call site, with no other change to
workflow logic.

#### Scenario: Provider swap rewrites only the model_ref
- **WHEN** a node's `model_ref` is changed from an Anthropic model entry to an OpenAI model entry
  and nothing else in the Variant Spec changes
- **THEN** the generated diff edits only that node's model argument, and the transformed run
  executes successfully against OpenAI
- **AND** the node receives a normalized response of the same shape it received from Anthropic
- **AND** no other workflow source is modified

### Requirement: The gateway SHALL apply per-call timeouts and bounded backoff and SHALL source secrets from a manager, never exposing them

Every provider call SHALL carry a timeout and SHALL retry transient failures with bounded
exponential backoff. Provider credentials SHALL be sourced from a secrets manager and SHALL NOT
appear in the Variant Spec, generated diffs, database rows, logs, error messages, or run records.

#### Scenario: Slow provider is bounded by timeout
- **WHEN** a provider does not respond within the configured per-call timeout
- **THEN** the call is aborted at the timeout and retried with backoff up to the retry limit
- **AND** the executor is not blocked indefinitely by the slow provider

#### Scenario: Secrets never leak
- **WHEN** a run completes and its logs, generated diffs, run records, and error messages are
  inspected
- **THEN** no provider API key or secret value appears in any of them

### Requirement: The Executor SHALL run the transformed working copy in a sandbox and pass node I/O through the typed contract

The Executor SHALL execute the built, transformed working copy **in a sandbox**, walking the node
graph in the Variant Spec's declared ordering, and SHALL pass each node's output through the P0
typed I/O contract before it becomes a downstream node's input. The measured run SHALL be the
transformed source that would ship — not a proxy.

#### Scenario: End-to-end run of the transformed copy
- **WHEN** a hardcoded 3-node graph is transformed by a valid Variant Spec, built, and executed
- **THEN** the built, transformed copy runs in a sandbox with each node in declared order
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

### Requirement: A run of a given config_hash + seed SHALL be reproducible across transform, build, and seed propagation

Given identical `{config_hash, source_revision, seed}`, the Runtime SHALL generate a byte-identical
diff, build it deterministically (given a pinned toolchain), and propagate the identical seed to
every provider call and stochastic step.

#### Scenario: Same config_hash + seed replays identically
- **WHEN** the same `{config_hash, source_revision, seed}` is run twice
- **THEN** both runs generate a byte-identical diff and build the same artifact
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

### Requirement: An applied change SHALL be rolled back by a clean single git revert

Every change the Runtime applies to a variant branch SHALL be revertible as a single `git revert`
that restores the prior source exactly, leaving no residual edits.

#### Scenario: Clean revert restores prior source
- **WHEN** an applied variant commit is rolled back with a single `git revert`
- **THEN** the repository source matches the pre-transform `source_revision` byte-for-byte
- **AND** no residual edits or orphaned worktree state remain

### Requirement: Each run SHALL be identified by a run_id and persist per-node I/O, the generated diff, and a terminal status queryable by the UI

Each run SHALL have a `run_id` and SHALL persist, per node, the input and output (as content-hashed
blob references), a reference to the generated diff, and a terminal status of `succeeded`, `failed`,
`halted`, or `build-rejected`, queryable by the run/inspect UI.

#### Scenario: UI can read run state, the diff, and per-node I/O
- **WHEN** the bare run/inspect UI requests a run by `run_id`
- **THEN** it receives the terminal status, a reference to the generated diff, and — for each
  executed node — references to its input and output blobs
- **AND** a run still in progress reports a non-terminal status distinct from `succeeded`,
  `failed`, `halted`, and `build-rejected`
