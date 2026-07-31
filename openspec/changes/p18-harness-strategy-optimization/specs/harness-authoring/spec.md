# Harness Authoring — Spec Delta (P18)

Product rationale: [`../../../../../docs/prd/P18-harness-strategy-optimization.md`](../../../../../docs/prd/P18-harness-strategy-optimization.md)
§15 (FR42–FR45), §16 (A26). Design reasoning: [`../../design.md`](../../design.md) Addendum Decision 12;
[`../../decisions.md`](../../decisions.md) D-12, D-11, D-6.

This capability is P18's **per-axis binding** of the cross-axis contract for user-initiated change,
defined once in [`authored-change`](../../../../specs/authored-change/spec.md) (P13) and referenced —
**never restated** — here, as it is by [`context-authoring`](../../../../specs/context-authoring/spec.md)
(P16) and [`memory-authoring`](../../../../specs/memory-authoring/spec.md) (P17). One spine, two origins;
origin recorded and never hashed; **a user MAY author the change, a user MAY NOT author the evidence.**

> **What P18 adds to that contract, and why it is not a softening of it.**
>
> On the memory axis the boundary was uniform: at M20 *every* memory override was refused, so the surface
> could state one fact and be right everywhere. Harness is not uniform. `single-shot` is the identity and
> materializes in every language; `reflexion` materializes where a response's text is readable;
> `react-loop`, `plan-execute` and `critic-loop` are refused at every call site because the generated
> module may not dispatch a tool, plan, or call a critic model.
>
> So the boundary this surface states is **per cell**, read from the same coverage source the transform
> refuses from — and it is stated **before** the user chooses, not discovered after. The one sentence this
> capability adds: **the user is told what will happen to their source for the strategy they are actually
> looking at, in the language they actually wrote, before they choose it.**
>
> The second half is unchanged from every sibling axis and is the harder one: a heavier scaffold costs
> more per run, and the user cannot see that cost until it runs. An authored harness change therefore
> claims **nothing** — no quality gain, no cost saving, no latency effect — until verification has run.

## ADDED Requirements

### Requirement: A user SHALL be able to select and parameterize a node's harness strategy

A user SHALL be able to set a node's harness strategy from the closed builtin set and supply its
parameters, expressed **solely** through the existing `HarnessRef` override so the change resolves,
freezes, and participates in `config_hash` through the existing field. Only registered strategies SHALL be
offered. Free text SHALL NOT be a selection path, and params violating the strategy's params schema SHALL
be rejected before the entry is sealed.

#### Scenario: A user selects a strategy and it becomes a real configuration

- **WHEN** a user selects a builtin strategy with schema-valid params for a node
- **THEN** the selection is sealed into the harness registry and referenced by `version_id`
- **AND** the resulting variant resolves to a `config_hash` that differs from its parent's.

#### Scenario: Only registered strategies are offered

- **WHEN** the authoring surface enumerates the strategies a user may choose
- **THEN** it offers exactly the closed builtin set
- **AND** no free-text entry path sets a strategy name.

#### Scenario: Schema-violating params are rejected before sealing

- **WHEN** a user supplies params that violate the selected strategy's params schema
- **THEN** the authoring request is rejected naming the offending parameter
- **AND** no harness entry is stored.

#### Scenario: A param inapplicable to the selected strategy is rejected, not ignored

- **WHEN** a user supplies a parameter the selected strategy does not declare
- **THEN** the request is rejected naming the parameter and the strategy
- **AND** the parameter is not silently dropped.

### Requirement: Clearing an authored harness change SHALL reproduce the prior hash byte-identically

A user SHALL be able to clear a node's harness strategy. Clearing SHALL reproduce the pre-selection
`config_hash` byte-identically. Selecting `single-shot` with no params SHALL be indistinguishable from
clearing — the same canonical bytes and the same `config_hash` — and no surface SHALL present the two as
states that differ in effect.

#### Scenario: Clearing backs out with no residue

- **WHEN** a user selects a harness strategy on a node and then clears it
- **THEN** the resulting `config_hash` is byte-identical to the one before the selection.

#### Scenario: `single-shot` and cleared are one state

- **WHEN** one node is authored with the `single-shot` strategy and no params, and another has no harness
  strategy at all
- **THEN** the two nodes canonicalize to identical bytes
- **AND** no surface distinguishes them by effect.

### Requirement: The per-cell boundary SHALL be stated before the choice, from the engine's own coverage source

Before a user selects a strategy, the authoring surface SHALL state — for **that** strategy in **that**
language, read from the same coverage source the transform refuses from — whether the change can be
applied to source, and where it cannot, which class of thing is missing. The statement SHALL distinguish a
missing platform artifact from a fact about the user's call site or language. A control SHALL NOT be
silently disabled; where a selection cannot be applied, the reason SHALL be readable.

#### Scenario: The limit is visible before selection

- **WHEN** the authoring surface is rendered for a node, before any strategy is chosen
- **THEN** each offered strategy carries its own applicability for the node's language
- **AND** a strategy that cannot be applied states which class of thing is missing.

#### Scenario: The statement comes from the coverage source, not a second sentence

- **WHEN** the surface's stated boundary is compared with the transform's refusal for the same cell
- **THEN** both derive from one coverage fact
- **AND** no independently-authored sentence can drift from the engine's behaviour.

#### Scenario: The identity strategy is never presented as refused

- **WHEN** `single-shot` is offered in any language
- **THEN** it is presented as applicable
- **AND** no refusal is stated for it.

### Requirement: An authored harness change SHALL be refused at preflight with the transform's own typed cause

Where the cell refuses, an authored harness change SHALL be refused **before** any transform, worktree,
build, or evaluation spend. The refusal SHALL carry the same typed cause the transform raises, naming the
node, the `harness` dimension, and the strategy. A user SHALL NOT learn of the refusal from an empty diff
or an absent result.

#### Scenario: Preflight refuses before any spend

- **WHEN** a user requests that an authored harness change be applied in a cell that refuses
- **THEN** it is refused at preflight
- **AND** no worktree is created, no build runs, and no evaluation is charged.

#### Scenario: The preflight cause is the transform's cause

- **WHEN** the same node is refused once at preflight and once by the transform engine
- **THEN** both refusals carry the same typed cause and name the same dimension and strategy.

### Requirement: A refused harness change SHALL NOT be applied, delivered, or presented as success

While a cell refuses, an authored harness change for it SHALL NOT be applied, delivered, or merged. No
surface, report, or record SHALL show an applied, delivered, or partially-applied state for it, and
`refused` SHALL be rendered as its own state, distinct from both `failed` and `pending`.

#### Scenario: No applied state exists for a refused change

- **WHEN** an authored harness change has been refused
- **THEN** no record or surface reports it as applied, delivered, or partially applied.

#### Scenario: Refused is its own state

- **WHEN** a refused harness change is presented to a user
- **THEN** it is shown as refused
- **AND** it is distinguishable from a change that failed and from one still pending.

### Requirement: Refusing to apply SHALL NOT mean refusing to model

An authored harness change SHALL be derivable, resolvable, hashable, storable, and comparable regardless
of the transform refusal: it SHALL produce a candidate variant with a real `config_hash`, a recorded
`user` origin and acting identity, and a parent-variant pointer, so it is diffable in lineage and
re-materializable unchanged once its cell is covered.

#### Scenario: A refused change still produces a real variant

- **WHEN** a user authors a harness change that the transform will refuse
- **THEN** the change still resolves to a stable `config_hash`
- **AND** it records origin `user` with the acting identity and a parent-variant pointer.

### Requirement: An authored harness change SHALL claim nothing until the harness has run

An authored harness change SHALL be stamped `unverified` and SHALL claim no `task_success` gain, no cost
saving, and no latency effect until verification has run. Where the transform refuses, it SHALL be
surfaced as refused-not-scored and SHALL NOT enter the verified-delta ledger.

#### Scenario: No improvement is attributed to an unverified authored change

- **WHEN** an authored harness change is recorded
- **THEN** no metric improvement, token saving, or cost saving is attributed to it
- **AND** it does not enter the verified-delta ledger.

#### Scenario: The added cost of a heavier scaffold is stated, not implied

- **WHEN** a user selects a strategy whose turn ceiling exceeds one
- **THEN** the surface states that the change may multiply the node's per-run cost and latency up to that
  ceiling
- **AND** it states that whether the change is worth that cost is decided by verification, not by the
  selection.

### Requirement: The two origins SHALL be indistinguishable downstream

A harness configuration a user authors and one an operator proposes SHALL resolve to the same
`config_hash`, be transformed by the same rewriter with the same result, and pass the same admissibility
gate. The system SHALL NOT provide an authoring-only resolve path, transform path, or gate.

#### Scenario: Identical configurations from different origins are identical downstream

- **WHEN** a user authors a harness configuration byte-identical to one `OpHarnessStrategy` proposed
- **THEN** both resolve to the same `config_hash`
- **AND** both are transformed by the same rewriter with the same outcome.

#### Scenario: No second apply path exists

- **WHEN** the apply path is enumerated
- **THEN** exactly one transform entry point serves both origins
- **AND** no code path applies an authored harness change while bypassing the admissibility gate a
  proposal must pass.
