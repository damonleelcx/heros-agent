# Memory Materialization — Spec (folded from P18)

Product rationale: [`../../../docs/prd/P18-memory-runtime.md`](../../../docs/prd/P18-memory-runtime.md)
§6 (FR9–FR22), §7. Design reasoning: [`../../changes/archive/2026-07-31-p18-memory-runtime/design.md`](../../changes/archive/2026-07-31-p18-memory-runtime/design.md) Decisions 1, 4, 6, 7;
[`../../changes/archive/2026-07-31-p18-memory-runtime/decisions.md`](../../changes/archive/2026-07-31-p18-memory-runtime/decisions.md) D2, D5, D6, D7.

Covers the second artifact [P17](../../changes/archive/2026-08-01-p17-memory-strategy-optimization)'s refusal named: the
**call-site rewriter** that reads and writes the runtime, plus the generated module it calls and the
per-cell narrowing of P17's refusal.

> **The decision this capability turns on: BOTH HALVES OR REFUSE.** A memory strategy is a read *and* a
> write. Recall-only reads from a store nothing fills; record-only fills a store nothing reads. **Both
> behave as `none`** while the `config_hash` claims another strategy — P17's *"scored a configuration that
> never ran"* failure, one layer down and **harder to see**, because a diff genuinely was emitted, the
> build passes, and a reviewer reads real memory code. So a cell materializes only when it can emit both,
> and a call site admitting one half is refused **whole**, naming which half is missing.

## Requirements

### Requirement: A generated memory module SHALL ship in the same patch as the call-site edit

Materializing a memory strategy SHALL emit a generated module alongside the call-site edit in a single
patch, so one revert restores both.

#### Scenario: One patch carries both

- **WHEN** a memory strategy is materialized at a call site
- **THEN** the patch contains the generated module and the call-site edit
- **AND** reverting the patch removes both.

#### Scenario: The artifact regenerates byte-identically

- **WHEN** the same resolved configuration is materialized twice
- **THEN** the generated module is byte-identical both times.

### Requirement: The generated module SHALL be dependency-free and carry params as data

The generated module SHALL import nothing outside its language's standard library, and SHALL read the
strategy and its parameters from the binding document as data rather than embedding them as code.

#### Scenario: No third-party import is emitted

- **WHEN** the generated module is inspected
- **THEN** it imports only standard-library modules.

#### Scenario: Retuning a parameter is a document change

- **WHEN** a strategy parameter changes and the configuration is re-materialized
- **THEN** the change appears in the binding document
- **AND** the generated module's code is unchanged.

### Requirement: Recall SHALL be materialized as an expression replacement

Where a call site writes its message list, recall SHALL be materialized by replacing that written argument
with a call into the generated module. The rewrite SHALL construct no SDK-shaped value.

#### Scenario: A written message list is wrapped

- **WHEN** a call site writes its message list and a memory strategy is applied
- **THEN** the emitted diff replaces that argument with a call into the generated module
- **AND** the surrounding call is otherwise unchanged.

### Requirement: Record SHALL be materialized as a statement following the call

Record SHALL be materialized as a statement immediately following the call, and SHALL be admitted only
where the call is a simple assignment at statement level.

#### Scenario: A simple assignment admits the record

- **WHEN** the call's result is assigned to a variable at statement level
- **THEN** a record statement is emitted immediately after it.

#### Scenario: A call that is not a simple assignment does not admit the record

- **WHEN** the call appears in a position that is not a simple statement-level assignment
- **THEN** the record half is not admitted.

### Requirement: A cell SHALL materialize only when it can emit both halves

A memory strategy SHALL be materialized only where both recall and record can be emitted. A call site
admitting one half and not the other SHALL be refused whole, and the refusal SHALL name which half is
missing.

#### Scenario: A recall-capable, record-incapable call site is refused whole

- **WHEN** a call site writes its message list but its call is not a simple statement-level assignment
- **THEN** the memory override is refused
- **AND** no diff is produced, including no recall-only diff
- **AND** the refusal names the record half as the missing one.

#### Scenario: No partial memory diff exists

- **WHEN** the emitted patches for every memory materialization are enumerated
- **THEN** none contains a recall without a record, or a record without a recall.

#### Scenario: Coverage cannot claim a half-materializable cell

- **WHEN** the coverage table reports a cell as materializing
- **THEN** that cell emits both halves.

### Requirement: The rewritten source SHALL reparse and the edit SHALL be minimal

A materialized memory change SHALL produce source that reparses under the language's own parser, and SHALL
touch no line other than those its own edits target.

#### Scenario: The result parses

- **WHEN** a memory change is materialized
- **THEN** the rewritten file reparses without error.

#### Scenario: No untargeted line is modified

- **WHEN** the emitted diff is inspected
- **THEN** only the call site's own lines and the generated module are changed.

### Requirement: A refusal SHALL name the most specific true cause

Where a memory change cannot be materialized, the reported cause SHALL be the most specific true one,
evaluated in the order: the strategy, then the call site's own source, then the missing half, then the
language's materializer.

#### Scenario: An unpacked call site is told about the call

- **WHEN** a call site passes unpacked arguments instead of a written message list
- **THEN** the refusal names the unpacking as the cause
- **AND** the cause does not attribute the limit to the language's materializer.

#### Scenario: The unpacking cause survives every future materializer

- **WHEN** a language's materializer lands
- **THEN** an unpacked call site in that language is refused with the same cause.

### Requirement: The refusal SHALL be narrowed per cell, never removed

Coverage SHALL be a per-cell read derived from the materializer table. Every cell without a materializer
SHALL continue to return a typed unsafe-rewrite refusal, and no memory override SHALL be silently dropped.

#### Scenario: An uncovered cell still refuses

- **WHEN** a memory strategy is applied in a cell with no materializer
- **THEN** it is refused with a typed error naming the memory dimension
- **AND** no diff is produced.

#### Scenario: Coverage matches the engine cell for cell

- **WHEN** the coverage table and the engine's behaviour are compared for every cell
- **THEN** each cell's reported status matches what the engine does.

### Requirement: Materialization SHALL NOT change what a configuration is

No field introduced by materialization SHALL participate in `config_hash`. Every configuration hash
recorded before materialization existed SHALL reproduce unchanged.

#### Scenario: Pre-existing hashes reproduce

- **WHEN** a configuration authored before materialization existed is resolved
- **THEN** its `config_hash` is byte-identical to the previously recorded value.

#### Scenario: `none` still hashes as absent

- **WHEN** a node resolves its memory strategy to `none`
- **THEN** its canonical bytes are identical to a node declaring no memory strategy.

#### Scenario: Materialization status is not hashed

- **WHEN** the same configuration is resolved in a build that materializes it and one that refuses it
- **THEN** both produce the same `config_hash`.

### Requirement: A memory proposal SHALL be scored only where it materializes

An operator-proposed memory change SHALL become verifiable where its cell materializes, and SHALL remain
refused-not-scored where its cell refuses.

#### Scenario: A covered cell yields a verifiable candidate

- **WHEN** a memory proposal targets a node whose cell materializes
- **THEN** it compiles to a diff and is eligible for verification.

#### Scenario: An uncovered cell yields no score

- **WHEN** a memory proposal targets a node whose cell refuses
- **THEN** it is surfaced as refused-not-scored
- **AND** it is never counted as a win, a regression, or a tie.

### Requirement: The authoring surface SHALL report per-cell applicability

The authoring surface SHALL state applicability per cell rather than for the axis as a whole, read from
the same coverage source the engine refuses from.

#### Scenario: A covered cell is offered as applicable

- **WHEN** the surface renders a node whose cell materializes
- **THEN** it states that the change can be applied to source.

#### Scenario: The language-independent claim is withdrawn where it is no longer true

- **WHEN** at least one cell materializes
- **THEN** the surface no longer states that the limit is independent of the language
- **AND** it states the per-cell position instead.
