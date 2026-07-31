# Context Language Coverage — Spec Delta (P16)

Product rationale: [`../../../../../docs/prd/P16-context-strategy-optimization.md`](../../../../../docs/prd/P16-context-strategy-optimization.md)
§6 (FR24–FR29). Design reasoning: [`../../design.md`](../../design.md) Decision 9, [`../../decisions.md`](../../decisions.md) D-4.

The cross-axis rules — coverage as a total function over every registered language, per-cell claims, the
three refusal classes and their evaluation order, one coverage source, executable evidence for every row,
no gate weakened to reach a language, offline parity, and no plan-shaped coverage — are defined once in
[`language-coverage`](../../../p13-prompt-model-optimization/specs/language-coverage/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the context axis.

> **Context is the axis where two of the three refusal classes are already load-bearing, and the third is
> the one that is under-built.** A selection policy materializes by *deleting the turns it does not
> retain*, which needs one thing per language: a splitter that reports the written elements of a message
> list. Go and Python have one; five registered languages do not.
>
> But a policy's coverage is not only a language question, and the design already knows it. Whether a
> policy can be written into source **at all** is a fact about the policy — a summarized context, a
> hierarchical tier set, and a retrieved chunk set do not exist until a model or a retriever produces
> them at run time, so no splitter in any language will ever materialize them. And whether *this* call
> site can carry a selection is a fact about the customer's source: a call site that unpacks its arguments
> from a mapping has written no message list to select among, and would still have written none the day
> its language's splitter lands.
>
> So this capability's job is to complete the language dimension without letting it absorb the other two.
> Every registered language gets a splitter and an entry; every policy keeps its own language-independent
> verdict; and the order in which the questions are asked stays policy first, source next, language last.

## ADDED Requirements

### Requirement: Every registered language SHALL carry a context coverage entry per policy

The context coverage table SHALL carry an entry for every **(language, policy)** pair. A selection policy
that this language cannot yet materialize SHALL name the missing list splitter; a policy that no language
can materialize SHALL carry its own language-independent cause in every language's row.

#### Scenario: Every language and policy pair has a value

- **WHEN** the context coverage table is enumerated
- **THEN** every registered language appears against every declared policy
- **AND** each entry states one of: materialized by selection, equivalent to the unrewritten call site,
  or refused with a named cause.

#### Scenario: A language gap names the splitter

- **WHEN** a selection policy's entry for a language with no splitter is read
- **THEN** it names the absent list splitter as the missing artifact
- **AND** it does not describe the policy as unmaterializable.

### Requirement: A policy that does not exist in source SHALL refuse identically in every language

A policy whose content is produced at run time by a model call, a tiered summarizer, or a retriever SHALL
be refused in every language with the same cause, and that cause SHALL NOT be affected by whether the
language has a splitter.

#### Scenario: A run-time-produced policy refuses the same way everywhere

- **WHEN** such a policy is submitted in a language with a splitter and in one without
- **THEN** both refusals carry the same cause
- **AND** neither implies that a splitter would carry it.

### Requirement: A list splitter SHALL be the only per-language part of a context selection

The engine SHALL keep the policy's retained-turn decision, the drop record, the drop-tolerance gate, and
the emitted deletion language-neutral, and SHALL confine per-language knowledge to splitting a written
list into its elements. Adding a language SHALL be adding a splitter and its coverage entry.

#### Scenario: The retention decision is shared, not reimplemented

- **WHEN** the same selection policy is materialized in two languages
- **THEN** both retain the same turns, decided by the shared policy code
- **AND** neither language's splitter decides retention
- **AND** the recorded drop is produced by the same neutral path.

#### Scenario: Adding a language adds one artifact

- **WHEN** a language gains context coverage
- **THEN** the change adds a list splitter and its coverage entry
- **AND** no gate or policy rule is duplicated per language.

### Requirement: A context refusal SHALL be ordered specific-first, with the language asked last

For a refused context change, the reported cause SHALL be the most specific true one, evaluated in the
order: the policy, then the registry row, then the call site's own source, then the language's splitter.

#### Scenario: An unpacked call site is told about the unpacking

- **WHEN** a call site passes its arguments as an unpacked mapping in a language with no splitter
- **THEN** the refusal names the absence of a written message list at that call site
- **AND** it does not report the missing splitter as the operative cause
- **AND** the same call site refuses identically once the splitter lands.

#### Scenario: An unknown policy refuses ahead of every other question

- **WHEN** a policy with no declared call-site form is submitted
- **THEN** the refusal names the policy
- **AND** it is identical in every language.

### Requirement: A materialized selection SHALL record its dropped turns in every language

In every covered language, materializing a selection SHALL record which turns were not retained, and that
record SHALL be produced by the shared path rather than by a per-language rewriter.

#### Scenario: The drop record is unskippable in a newly covered language

- **WHEN** a selection is materialized in a language that has just gained a splitter
- **THEN** the dropped turns are recorded
- **AND** the record is byte-comparable with the same selection in an existing covered language
- **AND** no code path emits the deletion without producing the record.

### Requirement: The authoring surface SHALL state the context boundary for a node before a policy is chosen

Before a user selects a context policy on a node, the surface SHALL state — from the shared coverage
source — whether that node's language can materialize a selection, and SHALL distinguish that from a
policy that no language can materialize.

#### Scenario: The two boundaries read as two different sentences

- **WHEN** a node cannot carry a selection because its language has no splitter, and separately when a
  policy is not expressible at a call site at all
- **THEN** the surface states each with its own wording
- **AND** neither is rendered as the other
- **AND** a submission is refused with the transform's own typed cause.
