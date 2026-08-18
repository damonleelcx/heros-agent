# Wiring Language Coverage — Spec Delta (P15)

Product rationale: [`../../../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../../../docs/prd/P15-workflow-wiring-optimization.md)
§6 (FR31–FR36). Design reasoning: [`../../design.md`](../../design.md) Decision 11, [`../../decisions.md`](../../decisions.md) D-5.

The cross-axis rules — coverage as a total function over every registered language, per-cell claims, the
three refusal classes and their evaluation order, one coverage source, executable evidence for every row,
no gate weakened to reach a language, offline parity, and no plan-shaped coverage — are defined once in
[`language-coverage`](../../../p13-prompt-model-optimization/specs/language-coverage/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the wiring axis.

> **Wiring is the axis where a coverage gap and a source-shape gap look most alike, and are least alike.**
> A reorder materializes as a transposition of two adjacent statements, which needs one thing per
> language: a resolver that says which statement encloses a call site's line and where that statement
> begins and ends. Two languages have one; five do not, and for them the missing artifact is exactly that
> resolver — nothing about their syntax makes the move unsound.
>
> The gap that is *not* a coverage gap is the one a real repository hits first: a workflow may have **no
> adjacent transposable pair at all**, because its nodes are not adjacent statements, or the wiring
> difference is a merge, a prune, or a non-adjacent move rather than a single exchange. That is a fact
> about the customer's source and about the requested change, and it refuses identically in a language
> with a resolver and in one without. Reporting it as "no wiring rewriter for your language" would send
> an engineer to wait for work that would not help them.

## ADDED Requirements

### Requirement: Every registered language SHALL carry a wiring coverage entry naming its missing artifact

The wiring coverage table SHALL carry an entry for every registered language stating whether an adjacent
transposition can be materialized there and, where it cannot, naming the statement resolver as the missing
artifact.

#### Scenario: A language without a resolver names the resolver

- **WHEN** a language's wiring coverage entry is read
- **THEN** it states whether a transposition can be emitted
- **AND** where it cannot, it names the absent statement resolver
- **AND** it does not describe the language as structurally incapable of the move.

#### Scenario: No registered language is absent from the table

- **WHEN** the wiring coverage table is enumerated
- **THEN** every registered language appears
- **AND** each entry is either materializing or a named gap.

### Requirement: A statement resolver SHALL be the only per-language part of a wiring move

The engine SHALL keep the plan, the permutation invariant, the edge-set check, the coherence gate, and the
emitted edit language-neutral, and SHALL confine per-language knowledge to resolving a statement's
boundaries. Adding a language SHALL be adding a resolver and nothing else.

#### Scenario: Adding a language adds one artifact

- **WHEN** a language gains wiring coverage
- **THEN** the change adds a statement resolver and its coverage entry
- **AND** no gate, invariant, or plan rule is duplicated per language
- **AND** the emitted edit is produced by the same neutral code path.

#### Scenario: The invariant holds identically in every language

- **WHEN** a transposition is emitted in any covered language
- **THEN** the result is asserted to be a permutation of the original lines
- **AND** the assertion is the same one in every language
- **AND** an emission that fails it is refused rather than emitted.

### Requirement: A wiring change that is not a single adjacent transposition SHALL refuse identically in every language

A merge, a prune, an added or removed edge, a non-adjacent move, or more than one exchange SHALL be
refused with a cause naming the requested shape, in every language, whether or not that language has a
resolver.

#### Scenario: A non-transposable change is refused by shape, not by language

- **WHEN** a wiring change that is not a single adjacent transposition is submitted
- **THEN** the refusal names the shape of the requested change
- **AND** the refusal is identical in a language with a resolver and in one without
- **AND** it does not promise that a resolver would carry it.

#### Scenario: A workflow with no adjacent transposable pair says so

- **WHEN** a workflow's nodes are not adjacent statements
- **THEN** the refusal states that the source offers no transposable pair
- **AND** it does not name the language as the reason.

### Requirement: A wiring refusal SHALL be ordered specific-first, with the language asked last

For a refused wiring change, the reported cause SHALL be the most specific true one, evaluated in the
order: the requested change's shape, then the coherence gate, then the source's statement structure, then
the language's resolver.

#### Scenario: An untransposable shape in an uncovered language reports the shape

- **WHEN** a merge is requested on a node in a language with no resolver
- **THEN** the refusal names the merge
- **AND** it does not report the missing resolver as the operative cause.

#### Scenario: A coherence-gate refusal precedes every language question

- **WHEN** a reorder would violate the coherence gate
- **THEN** the refusal names the violated ordering constraint
- **AND** it is identical in every language.

### Requirement: An unmaterializable wiring draft SHALL NOT be scored in any language

A wiring change that cannot be materialized SHALL NOT be emitted as a scoreable variant in any language,
covered or not, because scoring it would measure unchanged source against a changed configuration hash.

#### Scenario: A refused wiring draft produces no variant to score

- **WHEN** a wiring change is refused for any cause in any language
- **THEN** no scoreable variant is produced
- **AND** no evaluation is run against unchanged source.

### Requirement: The authoring surface SHALL state the wiring boundary for a node before a move is attempted

Before a user expresses a reorder, the surface SHALL state — from the shared coverage source — whether the
node's language can carry a transposition and whether the workflow offers a transposable pair.

#### Scenario: The boundary is stated before the interaction, not after

- **WHEN** a node's language has no resolver, or the workflow has no adjacent transposable pair
- **THEN** the surface states which of the two is the case, before the user expresses a move
- **AND** a submission is refused with the transform's own typed cause.
