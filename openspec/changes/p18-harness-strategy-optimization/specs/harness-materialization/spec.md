# Harness Materialization — Spec Delta (P18)

Product rationale: [`../../../../../docs/prd/P18-harness-strategy-optimization.md`](../../../../../docs/prd/P18-harness-strategy-optimization.md)
§15 (FR30–FR41), §16 (A20–A25). Design reasoning: [`../../design.md`](../../design.md) Addendum Decisions 9, 10, 11;
[`../../decisions.md`](../../decisions.md) D-4 (narrowed), D-9, D-10, D-11, D-13.

Covers the second artifact Decision 4's refusal named as missing: the **call-site rewriter** that drives
the harness runtime, the generated artifact it calls, and the **per-cell narrowing** of the interim
refusal.

> **The one decision everything else follows from: DRIVE AND DECIDE, OR REFUSE.**
>
> A harness is a loop, and a loop is two separable capabilities — *driving* the call again, and *deciding*
> whether to. Emitting only the drive half yields a fixed-turn loop that burns N calls and discards N−1
> answers; emitting only the decide half yields a strategy that can tell it should continue and cannot. In
> either case the node runs a behaviour its `config_hash` does not name. So a cell materializes when it can
> emit **both** halves, and refuses by name otherwise.

## MODIFIED Requirements

### Requirement: A harness override SHALL be refused at transform with a typed error, never silently dropped

The refusal of the `agent-loop` capability is **narrowed per cell, never removed**. A cell —
`(language, strategy, call-shape)` — for which a materializer exists SHALL emit the change; every other
cell SHALL still return a typed `unsafeRewrite` naming the node, the `harness` dimension, the strategy,
and the reason. The resolved configuration SHALL still carry the override in the refused case. The system
SHALL NOT report an axis-wide verdict that contradicts the per-cell answer in either direction.

#### Scenario: An uncovered cell still refuses with a typed error

- **WHEN** a resolved node carrying a harness override reaches a cell with no materializer
- **THEN** the transform returns a typed `unsafeRewrite` naming the node, the dimension, and the strategy
- **AND** the resolved configuration still carries the override.

#### Scenario: A covered cell emits the change

- **WHEN** a resolved node carrying a harness override reaches a cell with a materializer
- **THEN** the transform emits a diff carrying both the loop and the generated artifact.

#### Scenario: The axis-wide claim matches the per-cell answer

- **WHEN** the coverage read and the transform are compared for the same cell
- **THEN** both report the same verdict
- **AND** no independently maintained table can report a cell as covered that the engine refuses.

## ADDED Requirements

### Requirement: A cell SHALL materialize only when it can both drive the call and decide whether to continue

A materialization SHALL emit a loop only when the generated runtime can re-invoke the call **and**
evaluate the strategy's stop condition against the response. A call site that can carry one half but not
the other SHALL be refused **whole**, naming which half is missing.

#### Scenario: A half-materializable call site is refused whole

- **WHEN** a call site can be re-invoked but its response cannot be evaluated against the stop condition
- **THEN** the call site is refused, naming the missing half
- **AND** no partial loop is emitted.

#### Scenario: Both halves are computed before any edit is emitted

- **WHEN** a materialization is attempted
- **THEN** both halves are resolved before the first edit is produced
- **AND** a failure in either half yields a refusal rather than a partial edit set.

### Requirement: `single-shot` SHALL be the identity and SHALL materialize everywhere

`single-shot` SHALL emit nothing and SHALL be reported as materializing in every language, because one
turn is exactly the un-rewritten call site. No surface SHALL report a refusal for it.

#### Scenario: The identity strategy emits nothing

- **WHEN** a node's harness resolves to `single-shot`
- **THEN** no edit is emitted for the harness dimension
- **AND** the resulting source is byte-identical to the un-rewritten source.

### Requirement: A strategy requiring a host service SHALL be refused at a call site, naming the service

A call site offers no injection point for a planner, a tool executor, or a critic. A strategy requiring
one SHALL be refused there, naming the service it needs, and SHALL NOT be degraded to a strategy that does
not need it.

#### Scenario: A tool-driven strategy is refused by name

- **WHEN** `react-loop` is materialized at a call site
- **THEN** it is refused naming the tool-execution service the generated module cannot supply
- **AND** no loop that omits tool execution is emitted.

#### Scenario: A critic-driven strategy is refused by name

- **WHEN** `critic-loop` is materialized at a call site
- **THEN** it is refused naming the separate critic call the generated module may not make.

### Requirement: The generated artifact SHALL be dependency-free, deterministic, and shipped in the same patch

The emitted harness module SHALL import nothing outside its language's standard library, SHALL regenerate
byte-identically from the same resolved configuration, and SHALL ship in the **same** patch as the
call-site edit so one revert restores both.

#### Scenario: Regeneration is byte-identical

- **WHEN** the same resolved configuration is transformed twice
- **THEN** the emitted artifact is byte-identical on both runs.

#### Scenario: The artifact and the call-site edit travel together

- **WHEN** a harness materialization produces a patch
- **THEN** the patch contains both the generated module and the rewritten call site.

#### Scenario: No third-party dependency is introduced

- **WHEN** the emitted artifact's imports are enumerated
- **THEN** every import resolves within the language's standard library.

### Requirement: The strategy and its params SHALL travel as data, not as generated control flow

The emitted artifact SHALL read the strategy name and its params from the binding document rather than
branching on them in generated code, so retuning a bound parameter is a document change and not a code
change.

#### Scenario: Retuning a bound parameter does not regenerate code

- **WHEN** a bound harness parameter is changed
- **THEN** the emitted module's source is unchanged
- **AND** the change is expressed in the binding document.

### Requirement: Materialization SHALL NOT change what a configuration is

The rewriter SHALL change only what the transform **emits**. Every `config_hash` minted before the
rewriter landed SHALL reproduce bit-for-bit after it, and a stored harness configuration SHALL
materialize without being re-authored.

#### Scenario: Hashes are unchanged by the rewriter

- **WHEN** a configuration hashed before the rewriter landed is resolved again after it
- **THEN** the `config_hash` is byte-identical.

#### Scenario: A stored authored change survives to the rewriter

- **WHEN** a harness configuration authored while its cell refused is resolved after a materializer lands
  for that cell
- **THEN** it materializes without being re-authored.
