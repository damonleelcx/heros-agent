# Memory Authoring — Spec (folded from P17)

Product rationale: [`../../../docs/prd/P17-memory-strategy-optimization.md`](../../../docs/prd/P17-memory-strategy-optimization.md)
§6 (FR17–FR24), §7 (NFR9–NFR12). Design reasoning: [`../../changes/archive/2026-08-01-p17-memory-strategy-optimization/design.md`](../../changes/archive/2026-08-01-p17-memory-strategy-optimization/design.md) Decision 7;
[`../../changes/archive/2026-08-01-p17-memory-strategy-optimization/decisions.md`](../../changes/archive/2026-08-01-p17-memory-strategy-optimization/decisions.md) D7.

This capability is P17's **per-axis binding** of the cross-axis contract for user-initiated change,
defined once in [`authored-change`](../authored-change/spec.md)
(P13) and referenced — **never restated** — here, as it is by
[`skill-tool-authoring`](../../changes/archive/2026-07-29-p14-skills-tools-optimization/specs/skill-tool-authoring/spec.md) (P14, not yet folded),
[`wiring-authoring`](../../changes/archive/2026-07-31-p15-workflow-wiring-optimization/specs/wiring-authoring/spec.md) (P15, not yet folded), and
[`context-authoring`](../context-authoring/spec.md) (P16).
One spine, two origins; origin recorded and never hashed; **a user MAY author the change, a user MAY NOT
author the evidence.**

> **What P17 adds to that contract, and why it is not a softening of it.**
>
> On the other four axes an authored change reaches the user's source as a diff and is merely *unscored*.
> On this one it does not reach the source at all: at M20 the transform **refuses** every
> `MemoryRef ≠ none` node, in both engines, because materializing a cross-invocation store at a call site
> is deferred ([`memory-policy`](../memory-policy/spec.md)). So the shared contract's "an authored change
> may be applied without a verdict" **does not** get to fire here, and pretending otherwise would be the
> exact dishonesty the shared contract was written to prevent.
>
> The resolution is to be precise about which half is refused. **Modeling is not refused. Materialization
> is.** Selecting a strategy resolves, hashes, versions, records, and compares — a real `config_hash`, a
> real parent pointer, a real lineage diff, re-materializable unchanged the day the rewriter lands. What
> is refused is the codemod, and the refusal must be **stated before the user chooses**, carry **the same
> typed cause the transform raises**, and never be dressed as the change having worked.
>
> The one sentence this capability adds: **the platform may refuse to apply an authored change, but never
> silently, never late, and never disguised as success.**

## Requirements

### Requirement: A user SHALL be able to select and parameterize a node's memory strategy

A user SHALL be able to set a node's memory strategy from the closed builtin set and supply its
parameters, expressed **solely** through the existing `MemoryRef` override so the change resolves,
freezes, and participates in `config_hash` through the existing field. Only registered strategies SHALL
be offered. Free text SHALL NOT be a selection path, and params violating the strategy's `ParamsSchema`
SHALL be rejected before the entry is sealed.

#### Scenario: A user selects a strategy and it becomes a real configuration

- **WHEN** a user selects a builtin strategy with schema-valid params for a node
- **THEN** the selection is sealed into the memory registry and referenced by `version_id`
- **AND** the resulting variant resolves to a `config_hash` that differs from its parent's.

#### Scenario: Only registered strategies are offered

- **WHEN** the authoring surface enumerates the strategies a user may choose
- **THEN** it offers exactly the closed builtin set
- **AND** no free-text entry path sets a strategy name.

#### Scenario: Schema-violating params are rejected before sealing

- **WHEN** a user supplies params that violate the selected strategy's `ParamsSchema`
- **THEN** the authoring request is rejected naming the offending parameter
- **AND** no memory entry is stored.

### Requirement: Clearing an authored memory change SHALL reproduce the prior hash byte-identically

A user SHALL be able to clear a node's memory strategy. Clearing SHALL reproduce the pre-selection
`config_hash` byte-identically. Selecting `none` SHALL be indistinguishable from clearing — the same
canonical bytes and the same `config_hash` — and no surface SHALL present the two as states that differ
in effect.

#### Scenario: Clearing backs out with no residue

- **WHEN** a user selects a memory strategy on a node and then clears it
- **THEN** the resulting `config_hash` is byte-identical to the one before the selection.

#### Scenario: `none` and cleared are one state

- **WHEN** one node is authored with the `none` strategy and another has no memory strategy at all
- **THEN** the two nodes canonicalize to identical bytes
- **AND** no surface distinguishes them by effect.

### Requirement: An authored memory change SHALL be refused at preflight with the transform's own typed cause

An authored memory change SHALL be refused **before** any transform, worktree, build, or evaluation
spend. The refusal SHALL carry the same typed cause the transform raises, naming the node, the `memory`
dimension, and the deferred call-site materialization. A user SHALL NOT learn of the refusal from an
empty diff or an absent result.

#### Scenario: Preflight refuses before any spend

- **WHEN** a user requests that an authored memory change be applied
- **THEN** it is refused at preflight
- **AND** no worktree is created, no build runs, and no evaluation is charged.

#### Scenario: The preflight cause is the transform's cause

- **WHEN** the same node is refused once at preflight and once by the transform engine
- **THEN** both refusals carry the same typed cause and name the same dimension
- **AND** the cause identifies the missing platform artifact, not a defect in the user's call site.

### Requirement: The boundary SHALL be stated before the choice, and the control SHALL NOT be silently disabled

Before a user selects a strategy, the authoring surface SHALL state — from the same coverage source the
transform refuses from — that a memory change cannot be applied to source at this milestone, expressed as
a fact about the platform's missing artifact rather than about the user's call site, language, or choice
of strategy. The control SHALL remain live with the boundary stated; it SHALL NOT be disabled without a
reason a user can read.

#### Scenario: The limit is visible before selection

- **WHEN** the authoring surface is rendered for a node, before any strategy is chosen
- **THEN** it states that a memory change cannot be applied to source at this milestone
- **AND** it attributes the limit to the deferred platform artifact.

#### Scenario: The statement comes from the coverage source, not a second sentence

- **WHEN** the surface's stated boundary is compared with the transform's refusal
- **THEN** both derive from one coverage fact
- **AND** no independently-authored sentence can drift from the engine's behaviour.

#### Scenario: The control is live rather than silently disabled

- **WHEN** a user opens the memory authoring control on any node
- **THEN** the control accepts a selection
- **AND** the reason the selection cannot be applied is stated rather than expressed as an inert control.

### Requirement: A refused memory change SHALL NOT be applied, delivered, or presented as success

While the transform refuses a memory rewrite, an authored memory change SHALL NOT be applied, delivered,
or merged. No surface, report, or record SHALL show an applied, delivered, or partially-applied state for
it, and `refused` SHALL be rendered as its own state, distinct from both `failed` and `pending`.

#### Scenario: No applied state exists for a refused change

- **WHEN** an authored memory change has been refused
- **THEN** no record or surface reports it as applied, delivered, or partially applied.

#### Scenario: Refused is its own state

- **WHEN** a refused memory change is presented to a user
- **THEN** it is shown as refused
- **AND** it is distinguishable from a change that failed and from one still pending.

### Requirement: Refusing to apply SHALL NOT mean refusing to model

An authored memory change SHALL be derivable, resolvable, hashable, storable, and comparable regardless of
the transform refusal: it SHALL produce a candidate variant with a real `config_hash`, a recorded `user`
origin and acting identity, and a parent-variant pointer, so it is diffable in lineage and
re-materializable unchanged once the call-site rewriter lands.

#### Scenario: A refused change still produces a real variant

- **WHEN** a user authors a memory change that the transform will refuse
- **THEN** the change still resolves to a stable `config_hash`
- **AND** it records origin `user` with the acting identity and a parent-variant pointer.

#### Scenario: The stored change survives to the rewriter

- **WHEN** the same stored authored memory configuration is resolved again after a call-site rewriter lands
- **THEN** it resolves to the same `config_hash`
- **AND** it materializes without being re-authored.

### Requirement: An authored memory change SHALL claim nothing until the harness has run

An authored memory change SHALL be stamped `unverified` and SHALL claim no memory-hit-rate gain, no
staleness reduction, no token or cost saving, and no quality effect until verification has run. While the
transform refuses, it SHALL be surfaced as refused-not-scored, and it SHALL NOT enter the verified-delta
ledger.

#### Scenario: No improvement is attributed to an unverified authored change

- **WHEN** an authored memory change is recorded
- **THEN** no metric improvement, token saving, or cost saving is attributed to it
- **AND** it does not enter the verified-delta ledger.

#### Scenario: Refused-not-scored, never a win

- **WHEN** an authored memory change is refused at transform
- **THEN** it is surfaced as refused-not-scored
- **AND** it is never counted as a win, a regression, or a tie.

### Requirement: The two origins SHALL be indistinguishable downstream

A memory configuration a user authors and one an operator proposes SHALL resolve to the same
`config_hash`, be refused by the same rewriter with the same typed cause, and pass the same gates. The
system SHALL NOT provide an authoring-only resolve path, transform path, or gate.

#### Scenario: Identical configurations from different origins are identical downstream

- **WHEN** a user authors a memory configuration byte-identical to one `OpMemoryPolicy` proposed
- **THEN** both resolve to the same `config_hash`
- **AND** both are refused by the same rewriter with the same typed cause.

#### Scenario: No second apply path exists

- **WHEN** the apply path is enumerated
- **THEN** exactly one transform entry point serves both origins
- **AND** no code path applies an authored memory change while bypassing a gate a proposal must pass.
