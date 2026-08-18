# Node Wiring — Spec (folded from P15)

Product rationale: [`../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../docs/prd/P15-workflow-wiring-optimization.md)
§6 (FR1–FR8), §7. Design reasoning: [`../../changes/archive/2026-07-31-p15-workflow-wiring-optimization/design.md`](../../changes/archive/2026-07-31-p15-workflow-wiring-optimization/design.md) Decisions 1, 2, 5, 6.

Covers the wiring axis as a source of optimization proposals: node **merge** (fuse adjacent nodes),
free **reorder** (including parallelizing independent nodes), and **prune** (drop a dead node) — each
producing a Variant Spec whose `Order`/`Edges` change and thus a new `config_hash`, scored by the
axis-agnostic harness with no eval-side change, and surfaced only when verification confirms it.

> **Why the axis lives in `Order`/`Edges`, not a new dimension.** The structural axis is already modeled
> on the Variant Spec and already **identity-bearing** in `config_hash`
> ([`spec.go:253-258`](../../../internal/variantspec/spec.go)). So this capability needs no new
> `Dimension`, registry `Kind`, or hashed field — only new *operators* that produce candidates in the
> `Order`/`Edges` space and one honest **interim refusal** for the source materialization that does not
> yet exist. Adding a bespoke metric or a wiring-flavored score would be a second definition of "better";
> landing the effect in `config_hash` is sufficient for the harness that consumes `config_hash` + `Trace`.

## Implementation evidence

| Requirement | Where it lives | Proof |
|---|---|---|
| Merge operator, adjacent-pair only | [`proposal/catalog.go`](../../../internal/proposal/catalog.go) `mergeOp`, one row in `DefaultCatalog()` | `TestMergeProducesFusedSpec`, `TestMergeIsAdjacentPairOnly`, `TestDefaultCatalogIncludesMerge` |
| Free reorder + parallelize, bounded | [`proposal/catalog.go`](../../../internal/proposal/catalog.go) `reorderOp.Propose`, `independentAdjacentPairs` | `TestFreeReorderIndependentNodes` |
| Prune rewires neighbours | [`proposal/catalog.go`](../../../internal/proposal/catalog.go) `pruneNode` | `TestPruneRewiresNeighbours`, `TestPruneAndMergeDifferOnFanIn` |
| Lineage; parent never mutated | [`variantspec/rearrange.go`](../../../internal/variantspec/rearrange.go) `Reorder`; `OperatorInput.BaseVariantID` | `TestMergeDerivesWithLineageParentUnchanged` |
| Distinct `config_hash`, no eval change | `Order`/`Edges` in [`variantspec/resolved.go`](../../../internal/variantspec/resolved.go) | `TestMergeChangesConfigHash`, `TestWiringScoredByExistingHarness` |
| Determinism | pure slice iteration, no map ordering | `TestWiringProposalsAreDeterministic` |
| Verification-gated surfacing | [`verification/recommend.go`](../../../internal/verification/recommend.go) | `TestUnverifiedMergeNotSurfaced`, `TestVerifiedMergeIsSurfaced` |
| Interim refusal at transform | [`transform/rewrite.go`](../../../internal/transform/rewrite.go) `refuseWiring`/`checkWiring`, gated in `Generate` and `GenerateTransform` | `TestWiringRefusedNotNoop`, `TestWiringRefusalIsObservableNoDiff`, `TestP5Commit_ReorderRefusedAtTransform` |

🚫 No `Dimension`, registry `Kind`, `NodeOverride` field, or DB table was added for this capability —
guarded structurally by `TestNoNewDimensionForWiring`.

⚠️ **The honest boundary.** Wiring is modeled, gated, hashed, and scored — it is **not yet materialized
as source**. Every wiring-differing spec is refused at transform today. When a wiring rewriter lands it
replaces that refusal and nothing above it changes.

## Requirements

### Requirement: The proposal catalog SHALL provide a merge operator that fuses two adjacent nodes

A merge operator (`OpMerge`) SHALL be registered in the default proposal catalog and SHALL produce a
candidate Variant Spec that fuses **two adjacent nodes** into one when a single model call can subsume
both. Merge SHALL apply only to an adjacent pair, never to a non-adjacent or many-to-one set.

#### Scenario: A redundant adjacent pair yields a merge candidate

- **WHEN** the catalog is asked to propose for two adjacent nodes flagged as redundant
- **THEN** a merge candidate is produced
- **AND** before this capability existed, no merge candidate was produced for the same input.

#### Scenario: Merge does not span non-adjacent nodes

- **WHEN** the two nodes flagged for merge are not adjacent in the order
- **THEN** no merge candidate fusing them across the gap is produced.

### Requirement: A merge candidate SHALL drop the absorbed node and rewire its edges through the survivor

The merge candidate's `Order` SHALL omit the absorbed node, and its `Edges` SHALL be rewired so the
absorbed node's inbound edges retarget the surviving node and its outbound edges re-source from the
surviving node. All other per-node overrides SHALL be inherited unchanged.

#### Scenario: The absorbed node leaves the order and its edges move to the survivor

- **WHEN** two adjacent nodes are merged with one designated the survivor
- **THEN** the candidate's order no longer lists the absorbed node
- **AND** every edge that entered or left the absorbed node now enters or leaves the survivor
- **AND** no other node's overrides are changed.

### Requirement: The catalog SHALL provide free edge rewiring — reorder and prune

The catalog SHALL propose reordering of data-independent nodes (including marking data-independent nodes
parallelizable) and pruning of a dead node, each producing a candidate Variant Spec in which only the
wiring is changed and all per-node overrides are inherited.

#### Scenario: Independent nodes can be reordered

- **WHEN** two nodes with no data dependency between them are candidates for reordering
- **THEN** a candidate Variant Spec with the reordered `Order` is produced
- **AND** the per-node overrides are unchanged from the parent.

#### Scenario: A dead node is pruned and its neighbours rewired

- **WHEN** a node whose output nothing downstream reads is flagged
- **THEN** a candidate that removes it from `Order` and rewires its neighbours is produced.

### Requirement: A wiring candidate SHALL be derived with lineage and SHALL NOT mutate the parent

Every wiring candidate SHALL be a newly derived Variant Spec carrying a parent reference, and the parent
spec SHALL remain unchanged.

#### Scenario: The parent survives derivation

- **WHEN** a merge, reorder, or prune candidate is derived from a parent spec
- **THEN** the candidate carries the parent's identity as its parent reference
- **AND** the parent spec is byte-identical before and after the derivation.

### Requirement: A wiring change SHALL yield a distinct config_hash and be scored with no eval-side change

Because `Order` and `Edges` participate in `config_hash`, a merge, reorder, or prune SHALL produce a
`config_hash` distinct from its parent, and the resulting configuration SHALL be scored by the existing
evaluation harness with no new metric, scorer, or dimension-label branch introduced for wiring.

#### Scenario: A wiring change is a new configuration

- **WHEN** a merge, reorder, or prune candidate is resolved
- **THEN** its `config_hash` differs from the parent's
- **AND** a wiring change that resolves to the same configuration hashes identically to the parent.

#### Scenario: The harness scores wiring without a wiring-specific addition

- **WHEN** a wiring-changed configuration is evaluated
- **THEN** it is scored through the existing harness consuming `config_hash` and the trace
- **AND** no metric or scorer specific to the wiring axis is added.

### Requirement: Wiring proposals SHALL be deterministic

For a given base spec and a given diagnosis or signal, the produced candidate `Order`, `Edges`, absorbed
or pruned node choice, and `config_hash` SHALL be identical on every run.

#### Scenario: Re-proposing yields an identical candidate

- **WHEN** the same base spec and the same signal are proposed for twice
- **THEN** the two candidate specs are byte-identical
- **AND** their `config_hash` values are identical.

### Requirement: A wiring candidate SHALL be surfaced as a recommended change only after verification

A produced wiring candidate SHALL be presented as a recommended change only after verification shows it
better or cheaper on held-out data. A produced-but-unverified candidate SHALL NOT be presented as a
recommended change.

#### Scenario: An unverified merge is not recommended

- **WHEN** a merge candidate is produced but verification does not confirm it is better or cheaper on
  held-out data
- **THEN** it is not presented as a recommended change
- **AND** it is at most labelled exploratory.

#### Scenario: A verified merge is recommended

- **WHEN** a merge candidate is confirmed better or cheaper on held-out data
- **THEN** it may be presented as a recommended change.

### Requirement: An un-materializable wiring change SHALL be refused at transform, never silently applied

A wiring change the transform engine cannot materialize SHALL be refused at transform with a typed error
that names the wiring axis. It SHALL NOT be silently dropped, and SHALL NOT be applied as a no-op.

The refusal is scoped to what the SOURCE STATES: a spec whose node SET differs from the discovered one
(the dropped call is still in the tree), or one that inverts an order the source states by running two
calls as consecutive sibling statements. An ordering between calls the source does not order, or an edge
the IR never recorded, is a declaration with no source counterpart and is not refused.

#### Scenario: A wiring-differing spec is refused, not no-op'd

- **WHEN** a resolved spec drops a node the source still contains, or inverts a source-stated order it
  cannot materialize
- **THEN** the transform refuses it with a typed error naming the wiring axis
- **AND** no diff is generated
- **AND** the wiring change is not applied as a no-op that would let its `config_hash` be scored against
  unchanged source.

#### Scenario: The refusal is observable

- **WHEN** a wiring change is refused at transform
- **THEN** the refusal is present in the transform result and identifies the wiring axis.
