# Inference Provenance — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/p30-heros-platform-agent/design.md`](../../changes/p30-heros-platform-agent/design.md).

Covers who authored every fact in a stored graph — `frontend`, `detector`, `heros`, `operator` — and the
reading of an absent author as `legacy`.

> A run-level "contains inferred facts" boolean cannot answer *who authored THIS edge*, which is the
> only question an incident asks. There is no back-fill: stamping `frontend` onto rows nobody examined
> would erase the very distinction the field creates.

## Requirements
### Requirement: Every fact in a stored IR SHALL record who authored it
Provenance sits on the fact, not on the run, because "who authored *this* edge" is the question an
incident asks and a run-level flag cannot answer it.

#### Scenario: Each edge, node field and label carries a provenance
- **WHEN** an IR is stored
- **THEN** every edge, every node field that can be inferred, and every label carries
  `provenance ∈ {frontend, detector, heros, operator}`

#### Scenario: A HEROS-authored fact carries its full lineage
- **WHEN** a fact has provenance `heros`
- **THEN** it also records the agent `config_hash`, the `source_revision` read, the confidence, and the
  inference id

#### Scenario: Provenance is an enum, not a flag
- **WHEN** the storage shape is inspected
- **THEN** provenance admits four named values
- **AND** `operator` is reserved and unused in this phase rather than absent

### Requirement: Pre-existing IRs SHALL read as `legacy` and SHALL NOT be back-filled with a guess
Back-filling absent provenance to `frontend` would assert something about rows nobody examined.

#### Scenario: An IR written before the migration
- **WHEN** an IR stored before this change is read
- **THEN** its facts report `legacy`
- **AND** they are distinguishable from `frontend` in a query
- **AND** no migration writes `frontend` into them

#### Scenario: The migration is reversible
- **WHEN** the provenance migration is rolled back
- **THEN** every previously readable IR remains readable

### Requirement: A surface rendering an authored fact SHALL mark it, and the mark SHALL survive aggregation
A count that silently mixes a parser's facts with a model's is the failure this requirement exists to
prevent.

#### Scenario: An inferred edge is marked on the drawing
- **WHEN** a graph containing HEROS-authored edges is rendered
- **THEN** those edges are visually distinct from frontend-derived edges
- **AND** the distinction is explained in the legend

#### Scenario: A mixed count reports both parts
- **WHEN** a surface reports an edge count for a graph containing both frontend and HEROS edges
- **THEN** it reports the total and the inferred portion
- **AND** it does not report a single undifferentiated number

#### Scenario: The word is the same everywhere
- **WHEN** any surface names a HEROS-authored fact
- **THEN** it uses the term `inferred`
- **AND** it does not use "AI-generated", "guessed", "predicted" or "estimated" for this purpose

#### Scenario: A customer sees the marking, not only an operator
- **WHEN** a customer views their own workflow graph
- **THEN** inferred facts are marked on their view

### Requirement: A produced fact and an assessed observation SHALL be distinguishable
HEROS both writes graph facts and comments on evidence. Rendering the two alike would let a comment
read as a measurement.

#### Scenario: Output is typed
- **WHEN** HEROS emits any output
- **THEN** it is typed `produced` or `assessed`
- **AND** the two render differently

#### Scenario: No number is asserted where none was measured
- **WHEN** HEROS reports on quality, cost, coverage or spend
- **THEN** it does not emit a numeric figure
- **AND** where the measurement is absent it states that it is absent

#### Scenario: Assessment does not become a verdict
- **WHEN** HEROS assesses a proposal, a variant or an eval case
- **THEN** it does not mark a proposal verified, rank a variant, or score a case
