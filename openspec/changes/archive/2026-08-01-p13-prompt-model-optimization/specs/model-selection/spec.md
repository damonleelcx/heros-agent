# Model Selection — Spec Delta (P13)

Product rationale: [`../../../../../docs/prd/P13-prompt-model-optimization.md`](../../../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR9–FR16), §7. Design reasoning: [`../../design.md`](../../design.md) Decisions 4, 5, 6, 7, 8.

Covers model up/down-grade under an explicit quality guardrail, model-parameter tuning within the honest
apply-mode boundary, and the provider-routing refusal — all expressed through the existing model
dimension so nothing about the hash, eval, or transform contract changes.

> **Why the guardrail, and why held-out.** The downgrade operator's *goal* is a cheaper model, which
> makes "ship the cheaper one" the tempting failure. The only defensible admissibility rule is a
> predicate the operator cannot game: the cheaper model is admissible **only** when the platform cannot
> statistically tell it apart from the incumbent — its task-success confidence interval **overlaps** the
> incumbent's — on **held-out** cases the operator did not select. That is the same overlap the tie rule
> already computes, judged on data chosen to defeat overfitting. A cost win that silently costs quality
> is a stability degradation bought with cost convenience, and the ordering forbids it.

## ADDED Requirements

### Requirement: A model downgrade SHALL be admissible only under a held-out quality guardrail

A cheaper-model candidate SHALL be admissible only when its task-success confidence interval overlaps the
incumbent model's confidence interval on held-out cases. A downgrade whose task-success interval does not
overlap the incumbent's SHALL be inadmissible regardless of its cost improvement.

#### Scenario: A non-overlapping downgrade is inadmissible despite lower cost

- **WHEN** a cheaper model's task-success confidence interval does not overlap the incumbent's on the
  held-out cases
- **THEN** the downgrade is inadmissible
- **AND** its lower observed cost does not make it admissible.

#### Scenario: An overlapping downgrade is admissible

- **WHEN** a cheaper model's task-success confidence interval overlaps the incumbent's on the held-out
  cases
- **THEN** the downgrade is admissible for verification as an equal-quality-cheaper candidate.

### Requirement: The guardrail SHALL be judged on cases disjoint from the operator's motivating set

The guardrail SHALL be evaluated on held-out cases that are disjoint from the cases the proposing operator
selected to motivate its proposal, so a downgrade cannot be admitted by fitting the cases that motivated
it.

#### Scenario: Motivating and held-out cases do not overlap

- **WHEN** the guardrail is evaluated for a downgrade candidate
- **THEN** the held-out cases used are disjoint from the cases the operator selected
- **AND** no motivating case contributes to the guardrail verdict.

#### Scenario: Insufficient held-out data yields an explicit verdict, not a pass

- **WHEN** the held-out set is smaller than the declared minimum for a discriminating interval
- **THEN** the guardrail returns an explicit insufficient-data verdict
- **AND** the downgrade is not admitted as a tie by default.

### Requirement: An admitted downgrade SHALL be reported as a cost win and a quality tie, never a quality win

A downgrade the guardrail admits (overlapping confidence intervals, strictly lower cost) SHALL be a valid,
shippable outcome reported as a win on cost and a tie on quality. It SHALL NOT be reported as a quality
win.

#### Scenario: An equal-quality-cheaper downgrade is a valid tie outcome

- **WHEN** an admitted downgrade has overlapping task-success intervals and strictly lower cost
- **THEN** it is a shippable outcome
- **AND** it is reported as a cost win and a quality tie
- **AND** it is not reported as a quality win.

### Requirement: Model upgrade and extended-thinking admissibility SHALL be preserved

Model-upgrade and extended-thinking candidates SHALL keep their existing admissibility (a capability-gap
diagnosis; thinking-budget models on reasoning patterns) and SHALL become applicable changes only when the
harness ranks them a win. This change SHALL NOT relax that admissibility.

#### Scenario: An upgrade still ships only on a ranked win

- **WHEN** a model-upgrade candidate is proposed
- **THEN** it becomes an applicable change only if the harness ranks it a win
- **AND** its admissibility is unchanged by this phase.

### Requirement: Model-parameter tuning SHALL materialize where the apply mode carries it and refuse where it does not

Temperature and max-tokens tuning SHALL be modeled and hashed via the resolved node's provider parameters,
materialized where the node's apply mode can carry it (bound mode), and refused with a named cause where it
cannot (an inline node with no applicable parameter rewrite). It SHALL NOT be silently dropped.

#### Scenario: A parameter override materializes in bound mode

- **WHEN** a node in bound mode carries a temperature or max-tokens override
- **THEN** the parameter is materialized as data at the call site
- **AND** it participates in the configuration hash through the provider-parameters field.

#### Scenario: An un-materializable inline parameter override is refused

- **WHEN** an inline node carries a parameter override the engine cannot rewrite at the call site
- **THEN** the transform is refused with a named cause
- **AND** the override is not silently dropped
- **AND** no diff is produced for that node.

### Requirement: A cross-provider swap at a user call site SHALL be refused at transform

An intra-provider model swap SHALL be applied. A model swap that changes the provider at a user call site
SHALL be refused at transform with a named cause: it is modeled and hashable but not materialized, and
SHALL NOT be emitted as a diff that compiles against a different provider's SDK.

#### Scenario: An intra-provider swap applies

- **WHEN** a candidate swaps the model within the same provider at a call site
- **THEN** the model reference is rewritten
- **AND** a reviewable diff is produced.

#### Scenario: A cross-provider swap is refused with no diff

- **WHEN** a candidate selects a different provider than the call site's SDK
- **THEN** the transform is refused with a named cause
- **AND** no diff is produced
- **AND** the refusal is distinguishable from a reference that does not exist.

### Requirement: A model candidate's only hashed effect SHALL be its ModelRef and provider parameters

A model candidate SHALL express its effect solely as a changed model reference and/or provider parameters
on the affected node, so it participates in the configuration hash through existing fields with no change
to the hash contract.

#### Scenario: A model change yields a new config hash through existing fields

- **WHEN** a model candidate changes a node's model reference or provider parameters
- **THEN** the configuration hash changes through the existing resolved-node fields
- **AND** no new hashed field is introduced
- **AND** the configuration-hash golden vectors still reproduce bit-for-bit.

### Requirement: Model selection SHALL be judged multi-seed with confidence intervals and no new metric

Every model candidate SHALL be evaluated multi-seed with confidence intervals and the tie rule; a
single-seed run SHALL NOT decide a model change. The guardrail SHALL read the existing task-success metric
and its interval, and this capability SHALL introduce no new evaluation oracle, scoring metric, or
dimension.

#### Scenario: Confidence-interval overlap decides ties, not a single seed

- **WHEN** two model candidates have overlapping composite confidence intervals
- **THEN** they are reported tied
- **AND** no single-seed result decides between them.

#### Scenario: The guardrail uses the existing quality metric

- **WHEN** the downgrade guardrail is evaluated
- **THEN** it reads the existing task-success metric and its confidence interval
- **AND** no bespoke quality metric is introduced for it.
