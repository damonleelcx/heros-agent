# Re-arrangement — P5 Delta

Cross-reference: [`../../../../docs/prd/P5-contracts-rearrange-tracing.md`](../../../../docs/prd/P5-contracts-rearrange-tracing.md) §6 (FR8–FR14).

The interactive graph editor. The riskiest interaction in the product — so **the unhappy path is
designed first**: an invalid reordering is legible, never silently broken.

## ADDED Requirements

### Requirement: The graph editor SHALL let users add, remove, reorder, and swap nodes to produce a candidate Variant Spec

The editor SHALL expose the Workflow IR and support **add, remove, reorder, and swap** of nodes,
producing a **candidate** new Variant Spec. The candidate SHALL NOT be committed until it passes
`typed-contracts` validation.

#### Scenario: An edit produces a candidate, not a committed spec
- **WHEN** a user reorders two nodes in the editor
- **THEN** a candidate Variant Spec is produced
- **AND** it is validated through the typed-contract check before it can be committed
- **AND** it is not persisted as a runnable spec until validation passes.

### Requirement: The editor SHALL validate every edit before commit and SHALL NOT silently commit a broken graph

Every add/remove/reorder/swap SHALL be validated through the `typed-contracts` ordering-coherence check
**before commit**. An edit that yields an incoherent, un-adaptable ordering SHALL NOT be silently
committed.

#### Scenario: An incoherent edit cannot be committed
- **WHEN** a user reorders into an ordering the validator rejects as incoherent
- **THEN** the editor blocks the commit
- **AND** no runnable Variant Spec is persisted for that ordering.

### Requirement: The editor SHALL render an invalid reordering as a legible, first-class state

When an edit yields a schema mismatch, the editor SHALL surface the **contract mismatch** anchored to
the **offending edge** — naming the two nodes and the specific mismatching field(s) — in plain language.
Where the mismatch is **adaptable**, the editor SHALL **preview the auto-inserted adapter**; where it is
**not**, the editor SHALL **explain what would break**. The invalid state SHALL be a distinct, labeled UI
state (not a generic error toast) and SHALL NOT be conveyed by color alone.

#### Scenario: An incoherent reorder shows the mismatch on the offending edge
- **WHEN** a user drags consumer B ahead of the producer of a field B requires, with no adapter available
- **THEN** the offending edge shows the mismatch — node B, the missing producer, and the field
- **AND** the editor explains in plain language what would break
- **AND** the commit is blocked.

#### Scenario: An adaptable reorder previews the adapter it would insert
- **WHEN** a reorder yields a mismatch bridgeable by a catalog adapter
- **THEN** the editor previews the adapter node it would insert, named for what it does
- **AND** the user can accept or decline it
- **AND** whether the user accepts or declines, the workflow is never committed in a broken state.

### Requirement: The editor SHALL be fully keyboard-operable and screen-reader-accessible

Every edit — add, remove, reorder, swap — SHALL be operable without a pointer, with labeled controls,
managed focus across a reorder, and **screen-reader announcement of the validation verdict**
(coherent / adapter-inserted / rejected). Node, edge, and validation-state encodings SHALL meet WCAG AA
contrast and SHALL NOT rely on color alone.

#### Scenario: A reorder is performed and its verdict announced by keyboard only
- **WHEN** a user reorders nodes using only the keyboard
- **THEN** the reorder is applied and focus is managed to the moved node
- **AND** the resulting validation verdict (coherent / adapter-inserted / rejected) is announced to a
  screen reader.

#### Scenario: Validation state is not color-only
- **WHEN** an edit is rejected or an adapter is inserted
- **THEN** the state is conveyed by text/label and shape, not color alone
- **AND** it meets WCAG AA contrast.

### Requirement: The editor SHALL remain responsive on large IRs by re-validating incrementally

The editor SHALL render large IRs (target: hundreds of nodes) with virtualized/canvas rendering and
SHALL re-validate only the **edges affected** by an edit, not the whole graph, so a single reorder does
not block the UI.

#### Scenario: A single reorder on a large graph re-validates only affected edges
- **WHEN** a user reorders one node in a hundreds-of-node IR
- **THEN** only the edges affected by that reorder are re-validated
- **AND** the perceived validation completes quickly (target < 200 ms) without blocking the canvas.

### Requirement: A committed edit SHALL produce a new lineage-tracked, diffable Variant Spec

A committed edit SHALL produce a new Variant Spec with `parent_variant_id` lineage to the original and a
computable diff, so the arrangement can be compared against its parent on the P4 leaderboard.

#### Scenario: Committing an edit yields a diffable variant with lineage
- **WHEN** a user commits a coherent (possibly adapter-augmented) edit
- **THEN** a new Variant Spec is produced with lineage to the parent
- **AND** a diff of the two specs (including any inserted adapter) is available to the compare view.
