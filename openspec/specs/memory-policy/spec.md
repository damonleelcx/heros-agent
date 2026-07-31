# Memory Policy — Spec (folded from P17)

Product rationale: [`../../../docs/prd/P17-memory-strategy-optimization.md`](../../../docs/prd/P17-memory-strategy-optimization.md)
§6 (FR7–FR16), §7 and §8. Design reasoning: [`../../changes/p17-memory-strategy-optimization/design.md`](../../changes/p17-memory-strategy-optimization/design.md) Decisions 2, 3, 4, 6;
[`../../changes/p17-memory-strategy-optimization/decisions.md`](../../changes/p17-memory-strategy-optimization/decisions.md) D2, D3, D4, D6.

Covers the new `DimMemory` Dimension — binding a memory strategy at a node, resolving and hashing it, and
**refusing** to materialize it at the call site until a memory rewrite is safe. Memory persists **across**
invocations; it is disjoint from [P16](../../changes/p16-context-strategy-optimization/)'s within-call
`DimContext`.

> **Why the refusal is first-class.** Binding a memory backend at a call site is not an argument swap — it
> is wiring a store the surrounding code reads and writes *between* turns, and there is no live memory
> runtime to bind to (the sweeper was removed at the pivot). Realizing it now would emit a diff nobody can
> trust. The repo's honest pattern is to **refuse until safe** with a typed `unsafeRewrite`
> (`refuseSkills`/`refuseContext`), keeping the override modeled, resolvable, and hashable while deferring
> only the materialization. Refusing — rather than silently dropping — is what makes the boundary
> observable and lets a test make it **go red**.

## Requirements

### Requirement: The system SHALL provide a `DimMemory` Dimension iterated only when set

`DimMemory` SHALL be a member of the closed Dimension enum, and a node's set of active dimensions SHALL
report memory when and only when a memory override is set, so the transform engine iterates memory exactly
as it does the other dimensions.

#### Scenario: Memory is reported as a dimension only when overridden

- **WHEN** a resolved override sets a memory strategy
- **THEN** the node's active dimensions include memory.

#### Scenario: A node with no memory override does not report memory

- **WHEN** a resolved override sets no memory strategy
- **THEN** the node's active dimensions do not include memory
- **AND** no memory code path is entered for that node.

### Requirement: A node SHALL carry an additive, omitempty memory override

`NodeOverride` SHALL gain a `MemoryRef` field that is additive and omitted when unset, participating in the
override's emptiness, reference-collection, and validation checks exactly as the sibling refs do. A node
that sets no memory strategy SHALL serialize byte-identically to a pre-P17 node.

#### Scenario: An unset memory override adds no bytes

- **WHEN** a node override sets no `MemoryRef`
- **THEN** the override serializes byte-identically to one authored before the memory field existed.

#### Scenario: A memory-only override is not empty

- **WHEN** a node override sets only a `MemoryRef`
- **THEN** the override reports itself as non-empty
- **AND** the `MemoryRef` appears in its collected references and is validated.

### Requirement: The resolved memory field SHALL participate in config_hash and be omitted when absent

`ResolvedNode` SHALL gain an additive memory field that is omitted (no key emitted) when the node carries no
memory strategy, and present when it does. Because the configuration hash is purely structural, the field
SHALL participate in the hash automatically when present, with no change to the hashing code.

#### Scenario: A no-memory node hashes as it did before the field existed

- **WHEN** a resolved node carries no memory strategy
- **THEN** it emits no memory key
- **AND** its config_hash equals the config_hash it produced before the memory field existed
- **AND** the P0 golden config-hash vectors reproduce unchanged.

#### Scenario: A memory change changes the hash

- **WHEN** two specs are identical except for their memory strategy or its params
- **THEN** they produce different config_hashes.

#### Scenario: Non-identity-bearing memory order does not change the hash

- **WHEN** two specs carry the same memory strategy and params but differ only in non-identity-bearing
  authoring order
- **THEN** they produce the same config_hash.

### Requirement: Discovery SHALL emit a per-node memory default of `none`

The intermediate representation SHALL carry a memory field on each node, additive and omitted when it would
be `none`, emitted by a discovery frontend, so the resolver always resolves against a concrete base.

#### Scenario: Every discovered node has a memory default

- **WHEN** a target is discovered
- **THEN** each node's memory default is `none`
- **AND** the same target at the same revision emits the same memory defaults deterministically.

### Requirement: A memory override other than `none` SHALL be refused at transform with a typed unsafeRewrite

A resolved node carrying a memory strategy other than `none` SHALL be refused at transform with a typed
`unsafeRewrite` that names the node, the `memory` dimension, and the reason. It SHALL NOT be silently
dropped and SHALL NOT produce a diff.

#### Scenario: A memory node is refused, not rewritten

- **WHEN** a resolved node carrying a memory strategy other than `none` is transformed
- **THEN** the transform returns a typed `unsafeRewrite` naming the node and the `memory` dimension
- **AND** no diff is produced for that node.

#### Scenario: The refusal is not confused with an invalid spec

- **WHEN** a memory override is refused at transform
- **THEN** the error is distinguishable from an unknown-node, unresolved-ref, or malformed-spec error
- **AND** it identifies the refusal as a deferred materialization, not an author error.

#### Scenario: The refusal is total across both engines

- **WHEN** a memory override is transformed through either the AST rewriter or the tree-sitter span rewriter
- **THEN** both refuse identically
- **AND** no target language applies a memory change through the other path.

#### Scenario: A memory override is never silently dropped

- **WHEN** a spec carries a memory override alongside other dimensions
- **THEN** the memory override is refused rather than ignored
- **AND** a diff is not produced as though the memory change had been applied.

### Requirement: A spec carrying a memory override SHALL still resolve and hash

The refusal SHALL be a property of the transform only. A spec carrying a `MemoryRef` SHALL still resolve to
a frozen configuration and still produce a stable, reproducible config_hash.

#### Scenario: Resolution and hashing succeed despite the transform refusal

- **WHEN** a spec carrying a memory override is resolved and hashed
- **THEN** resolution succeeds and produces a stable config_hash
- **AND** only the subsequent transform refuses.

### Requirement: An operator SHALL propose memory strategy swaps, decided by verification

A new operator SHALL be catalogued — a stable operator kind, a catalog row mapping a memory bottleneck
signal to a proposed strategy swap, and a prior with a verification-order hint — so the diagnosis engine can
propose a memory strategy change. The proposal SHALL carry no authority; its worth SHALL be decided by
verification.

#### Scenario: A memory bottleneck yields a proposed swap

- **WHEN** a memory bottleneck signal is present for a node
- **THEN** the operator proposes a memory strategy swap for that node
- **AND** the proposal is ordered by a prior that is a hint, not a result.

#### Scenario: A memory proposal is not a verified win while the transform refuses

- **WHEN** a proposed memory strategy swap is carried toward verification and the transform refuses its
  rewrite
- **THEN** the proposal yields no verified result
- **AND** it is surfaced as refused rather than reported as a gain.

### Requirement: Memory improvement SHALL be measured by the existing classifier metric set

The memory improvement signal SHALL be the pattern classifier's existing memory metric set —
`memory_hit_rate` as primary, `staleness`, `recall_precision`, and `write_amplification` — together with
eval token totals. P17 SHALL add no new metric and SHALL make no change to the pattern taxonomy or its
version.

#### Scenario: No new metric and no taxonomy change is introduced

- **WHEN** the memory improvement signal is defined
- **THEN** it references only the existing memory metric set and eval token totals
- **AND** no pattern is added or renamed and the taxonomy version is unchanged.

### Requirement: Memory and context SHALL NOT be expressible as one another

The memory Dimension SHALL model only cross-invocation persistence, and no field, ref, or operator SHALL
allow a memory change to be expressed as a context change or a context change to be expressed as a memory
change.

#### Scenario: A memory construct cannot be expressed as a context construct

- **WHEN** a memory strategy is bound at a node
- **THEN** it is expressed only through the memory Dimension and the memory registry
- **AND** it is not expressible through the context policy, context params, or context registry, nor the
  reverse.
