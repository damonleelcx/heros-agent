# Memory Store — Spec (folded from P17)

Product rationale: [`../../../docs/prd/P17-memory-strategy-optimization.md`](../../../docs/prd/P17-memory-strategy-optimization.md)
§6 (FR1–FR6), §7 and §8.5. Design reasoning: [`../../changes/archive/2026-08-01-p17-memory-strategy-optimization/design.md`](../../changes/archive/2026-08-01-p17-memory-strategy-optimization/design.md) Decisions 1 and 5;
[`../../changes/archive/2026-08-01-p17-memory-strategy-optimization/decisions.md`](../../changes/archive/2026-08-01-p17-memory-strategy-optimization/decisions.md) D1, D5.

Covers the new content-addressed registry `Kind` `memory` — the versioned vocabulary of memory strategies
an override references. A memory strategy persists **across** invocations and sessions; it is not context
assembly (that is [P16](../../changes/archive/2026-07-29-p16-context-strategy-optimization/)'s within-call `DimContext`).

> **Why a new Kind and a closed strategy set.** The `Kind` is hashed into every `version_id`, so a distinct
> `memory` Kind is what makes a memory ref pasted into another dimension **fail closed** instead of
> silently resolving; a discriminator on the context table would weld two dimensions into one id namespace
> forever. And a **closed, versioned** builtin set with a `ParamsSchema` is how the codebase already keeps
> stored labels interpretable (cf. `TaxonomyVersion`): a stored `config_hash` that references
> `summary-buffer` must still mean the same thing months later, and a malformed strategy must be rejected
> at seal, not discovered at run time.

## Requirements

### Requirement: The system SHALL provide a `memory` registry Kind backed by its own table

A new registry `Kind` `memory` SHALL exist alongside `model`, `prompt`, `skill`, and `context`, backed by a
new `memory_entry` table. A memory entry SHALL be content-addressed by a `version_id` derived from its
sealed envelope, unique across all registries.

#### Scenario: A memory strategy is a content-addressed entry

- **WHEN** a memory strategy is sealed into the registry
- **THEN** it is stored in the `memory_entry` table under a `version_id` derived from its sealed envelope
- **AND** the `version_id` is unique across all registry kinds.

#### Scenario: The same strategy and params seal to the same id

- **WHEN** two memory entries with the same name, strategy, and params are sealed
- **THEN** they produce the identical `version_id`
- **AND** they are the same entry.

### Requirement: A memory ref SHALL resolve only in the memory registry and otherwise fail closed

Because the Kind is part of the content address, a memory `version_id` SHALL resolve only against the memory
registry. A memory ref used in another dimension, and a foreign ref used as a memory ref, SHALL fail to
resolve rather than bind the wrong entry.

#### Scenario: A memory ref pasted into another dimension fails closed

- **WHEN** a memory `version_id` is supplied where a model, prompt, skill, or context ref is expected
- **THEN** it does not resolve
- **AND** the failure is a not-found, not a wrong-dimension binding.

#### Scenario: A foreign ref supplied as a memory ref fails closed

- **WHEN** a non-memory `version_id` is supplied as a `MemoryRef`
- **THEN** it does not resolve against the memory registry.

### Requirement: The builtin strategy vocabulary SHALL be exactly five, closed and versioned

The platform SHALL ship exactly the strategies `none`, `scratchpad`, `summary-buffer`, `vector-recall`, and
`entity-memory` as a closed set for the shipped strategy-set version. A strategy name outside the set SHALL
NOT resolve, and adding a strategy without a version bump SHALL fail a cardinality assertion.

#### Scenario: The five builtins resolve

- **WHEN** any of `none`, `scratchpad`, `summary-buffer`, `vector-recall`, `entity-memory` is referenced
- **THEN** it resolves to a builtin strategy definition.

#### Scenario: A name outside the set does not resolve

- **WHEN** a memory strategy name that is not one of the five builtins is referenced
- **THEN** it does not resolve.

#### Scenario: An unversioned sixth strategy fails loudly

- **WHEN** a sixth builtin strategy is added without bumping the strategy-set version
- **THEN** a cardinality assertion fails
- **AND** the addition does not silently change what a stored strategy name means.

### Requirement: Each strategy SHALL declare a `ParamsSchema` and reject violating params at seal

Every builtin strategy SHALL declare a `ParamsSchema` describing its tunable parameters. A memory entry
whose params violate its strategy's schema SHALL be rejected at seal time, not at run time.

#### Scenario: Schema-valid params are accepted

- **WHEN** a memory entry is sealed with params that satisfy its strategy's `ParamsSchema`
- **THEN** the entry is accepted and stored.

#### Scenario: Schema-violating params are rejected at seal

- **WHEN** a memory entry is sealed with params that violate its strategy's `ParamsSchema`
- **THEN** the seal is rejected
- **AND** the rejection occurs before the entry is stored.

### Requirement: `none` SHALL be the identity strategy

The `none` strategy SHALL denote the absence of any memory. A resolved node whose strategy is `none` SHALL
carry the same empty memory representation as a node with no memory strategy at all, so it hashes
byte-identically to a no-memory node.

#### Scenario: A `none` node is indistinguishable from a no-memory node

- **WHEN** a node resolves its memory strategy to `none`
- **THEN** its resolved memory representation is empty
- **AND** its canonical bytes are identical to a node that declares no memory strategy.

### Requirement: A memory strategy SHALL be referenced by version_id, never inlined

A spec SHALL reference a memory strategy by its registry `version_id`. A spec SHALL NOT inline a strategy's
params in place of a reference, so a memory configuration is always resolvable back from a `config_hash`.

#### Scenario: An inline strategy definition is rejected

- **WHEN** a spec supplies memory params inline instead of a memory `version_id`
- **THEN** the spec is rejected at resolve
- **AND** the rejection names the inline definition as the cause.

### Requirement: Each strategy SHALL carry a human title and description distinct from its wire name

Every strategy SHALL carry a stable human-readable title and a description, kept distinct from its wire
name, so the interface layer, the strategy entity, and the code name remain three separate layers.

#### Scenario: A strategy exposes a title and description separate from its wire name

- **WHEN** a builtin strategy is inspected
- **THEN** it exposes a human title and a description
- **AND** neither is required to equal its wire name.
