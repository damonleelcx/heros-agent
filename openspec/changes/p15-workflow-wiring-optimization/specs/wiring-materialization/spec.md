# Wiring Materialization — Spec Delta (P15, wave 15c)

Product rationale: [`../../../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../../../docs/prd/P15-workflow-wiring-optimization.md)
§6 (FR15–FR21), §8.2, §8.3 D7–D9. Design reasoning: [`../../design.md`](../../design.md) Decisions 7–9.
One-way-door contracts: [`../../decisions.md`](../../decisions.md) D-3, D-4.

Covers the **one slice** of a call-site wiring rewriter that 15c ships: a **transposition of two
adjacent, independent sibling statements**, materialized as a permutation of the file's lines, behind
the same build gate every other transform passes. Every other wiring change — a merge, a prune, an edge
change, two or more transpositions, a language with no materializer — keeps the interim refusal the
`node-wiring` capability requires.

> **Why one transposition and not a rewriter.** Moving a call in general rewrites bindings, scope, and
> control flow: it is ADR-001's named top risk ("a bad codemod can break a build or subtly change
> behavior") with no cheap invariant to check the result against. A transposition of two whole-line
> blocks has one: the output is the input's lines, reordered. That is checkable in a line of code, it
> cannot be subtly wrong, and it is the reason this slice can ship while the rest stays refused.

## ADDED Requirements

### Requirement: Only an exact adjacent transposition SHALL be materialized

The transform engine SHALL materialize a wiring change as source only when the difference between the
spec's wiring and the discovered wiring is exactly one transposition of two adjacent nodes in the order,
with the edge set unchanged. Every other wiring difference SHALL keep the interim refusal.

#### Scenario: A single adjacent swap is attempted

- **WHEN** a resolved spec's order differs from the discovered order by exactly one adjacent
  transposition and its edges are unchanged
- **THEN** the engine attempts to materialize the swap
- **AND** a successful materialization produces a reviewable diff rather than a refusal.

#### Scenario: Any other wiring difference is still refused

- **WHEN** the wiring difference is a merge, a prune, an added or dropped edge, or more than one
  transposition
- **THEN** the change is refused with the typed error naming the wiring axis
- **AND** no diff is produced.

### Requirement: A transposition SHALL be materialized only for adjacent, whole-line sibling statements

A transposition SHALL be materialized only when both nodes' call sites are in the same file, at the same
block nesting, consecutive with nothing but blank lines between them, each occupying whole lines, and
neither is a control-flow statement. A pair failing any condition SHALL be refused with the specific
failing condition named.

#### Scenario: Two consecutive sibling statements are swappable

- **WHEN** the two call sites are consecutive statements in one block at one nesting level
- **THEN** the pair is admissible for materialization.

#### Scenario: A non-sibling pair is refused by name

- **WHEN** the two call sites are in different files, at different nesting, separated by other code, or
  either is a control-flow statement
- **THEN** the materialization is refused
- **AND** the refusal names which condition failed.

### Requirement: 🔴 A transposition SHALL be materialized only when the two statements are independent

No name bound by one statement may be read by the other, in either direction. The analysis SHALL be
conservative: where independence cannot be proven, the pair SHALL be refused rather than assumed
independent.

#### Scenario: A data dependency between the two statements is refused

- **WHEN** one statement binds a name the other reads
- **THEN** the materialization is refused
- **AND** the refusal names the shared name.

#### Scenario: Unprovable independence is refused, not assumed

- **WHEN** the frontend cannot determine what a statement binds or reads
- **THEN** the pair is refused rather than materialized.

### Requirement: 🔴 A materialized wiring change SHALL be a permutation of the file's lines

The emitted change SHALL preserve the file's line count and the multiset of its lines: no line may be
added, deleted, or altered by a wiring materialization — only moved. The minimality gate SHALL enforce
this, and SHALL confine every changed line to the two swapped blocks.

#### Scenario: The output is the input's lines, reordered

- **WHEN** a transposition is materialized
- **THEN** the resulting file has the same number of lines as the original
- **AND** the same multiset of lines
- **AND** every changed line lies inside one of the two swapped blocks.

#### Scenario: A non-permuting edit is rejected

- **WHEN** a wiring materialization would add, drop, or alter a line
- **THEN** the transform is rejected before it is proposed.

### Requirement: Materialization SHALL be per-language and named

A language with no statement materializer SHALL refuse with a message that says so. No generic textual
move SHALL be attempted for a language whose frontend cannot supply statement structure.

#### Scenario: An unsupported language refuses by name

- **WHEN** a transposition is requested for a workflow whose language has no statement materializer
- **THEN** the change is refused
- **AND** the refusal names the language and the missing materializer.

### Requirement: A materialized reorder SHALL pass the same build gate

A materialized transposition SHALL be subject to the same build/parse gate as every other transform. A
swap whose result does not parse or does not build SHALL be rejected before it is proposed.

#### Scenario: A swap that breaks the file is rejected

- **WHEN** the swapped file no longer parses
- **THEN** the transform is rejected and no diff is proposed.

### Requirement: Materialization SHALL be deterministic and self-inverse

The same spec, source revision, and tree SHALL produce a byte-identical diff, and applying the same
transposition twice SHALL return the original bytes.

#### Scenario: Regeneration is byte-identical

- **WHEN** the same transposition is materialized twice
- **THEN** the two diffs are byte-identical.

#### Scenario: The swap is its own inverse

- **WHEN** a materialized transposition is applied to its own output
- **THEN** the result is the original file, byte for byte.
