# Retrieval Tuning — Spec Delta (P16)

Product rationale: [`../../../../../docs/prd/P16-context-strategy-optimization.md`](../../../../../docs/prd/P16-context-strategy-optimization.md)
§6 (FR10–FR14), §7. Design reasoning: [`../../design.md`](../../design.md) Decisions 4, 5, 7.

Covers RAG parameter optimization — top-k, chunk size, rerank on/off, embedding model — proposed as
`OpRAGTune` variants, verified on held-out eval sets, and measured deterministically with the retriever
pinned.

> **Why held-out verification and pinning are non-negotiable.** Retrieval is where a "win" is easiest to
> fake. A top-k tuned to an eval set and "verified" on the same set is overfit dressed as a result —
> indefensible the first time it regresses on real traffic. And retrieval is non-deterministic unless
> pinned, so an unpinned retriever makes a configuration hash non-reproducible, breaking the one thing
> the lineage design exists to guarantee. Verification therefore runs on a **held-out** set disjoint
> from the tuning set, and every measurement run pins the retriever, its params, and the seed so the
> same hash issues the identical resolved retrieval request.

## ADDED Requirements

### Requirement: Retrieval tuning SHALL be proposed only on retrieval nodes

Retrieval-parameter variants — top-k, chunk size, rerank on/off, and embedding model — SHALL be proposed
as Variant Specs, and SHALL be admissible only on a node the pattern classifier labels as retrieval
(RAG).

#### Scenario: A retrieval-parameter variant is proposed on a RAG node

- **WHEN** a retrieval node is diagnosed with a retrieval miss
- **THEN** the tuning operator proposes variants over top-k, chunk size, rerank, and embedding model.

#### Scenario: Retrieval tuning is not proposed on a non-retrieval node

- **WHEN** the diagnosed node is not classified as a retrieval (RAG) node
- **THEN** no retrieval-tuning variant is proposed for it.

### Requirement: A retrieval change SHALL be verified on a held-out eval set

A retrieval change SHALL be verified on an evaluation set disjoint from the set its parameters were
selected on. A retrieval win measured only on the tuning set SHALL NOT be presentable as a verified
delta.

#### Scenario: Verification uses a disjoint held-out set

- **WHEN** a retrieval change's parameters are selected on a tuning set and the change is then verified
- **THEN** the verification runs on a held-out set that is disjoint from the tuning set
- **AND** the resulting verdict is the one reported.

#### Scenario: An overlapping split is refused

- **WHEN** the tuning set and the held-out set intersect
- **THEN** the verification is refused
- **AND** the change is not presented as a verified delta.

### Requirement: A retrieval measurement run SHALL be deterministic

A retrieval measurement run SHALL pin the retriever, its parameters, and the seed, so re-running the
same configuration hash at the same source revision issues the identical resolved retrieval request,
including any rerank.

#### Scenario: The same configuration issues the same resolved request

- **WHEN** the same configuration hash at the same source revision is measured twice
- **THEN** both runs issue the identical resolved retrieval request
- **AND** the reproducibility claim is at the resolved-request level, including the rerank.

#### Scenario: An unpinned retriever is not a measurement run

- **WHEN** a retrieval run does not pin the retriever, its parameters, and the seed
- **THEN** it is not accepted as a measurement run for scoring
- **AND** its result does not update the verified-delta ledger.

### Requirement: A retrieval change past a node's drop tolerance SHALL be inadmissible

A retrieval-tuning proposal that would raise a node's drop ratio past that node's tolerance SHALL be
inadmissible, because a larger top-k or a lossy rerank can shrink the retained conversation.

#### Scenario: A tuning proposal that over-drops is rejected

- **WHEN** a retrieval-tuning proposal's resolved policy would push a node's drop ratio past its
  tolerance
- **THEN** the proposal is inadmissible
- **AND** it is rejected before transform and before any evaluation run.

### Requirement: Retrieval augmentation SHALL be recorded as retrieval, not as loss

A pure retrieval augmentation — chunks prepended with the conversation preserved — SHALL report a drop
ratio of zero and a positive retrieved-chunk count.

#### Scenario: Augmentation reports zero drop and a chunk count

- **WHEN** a retrieval policy prepends retrieved chunks and preserves the conversation
- **THEN** its recorded drop ratio is zero
- **AND** its retrieved-chunk count is positive.

### Requirement: A retrieval reduction SHALL be legible as fewer eval tokens without success regression

A retrieval change that reduces assembled context SHALL be scored through the axis-agnostic harness and
show up as fewer total evaluation tokens at non-regressing task success, with no retrieval-specific
scoring path.

#### Scenario: A retrieval reduction lowers eval tokens at equal success

- **WHEN** a retrieval change reduces the assembled context and is scored
- **THEN** the run reports fewer total evaluation tokens than the base configuration
- **AND** task success does not regress
- **AND** the reduction is read through the standard metric family, not a retrieval-specific scorer.
