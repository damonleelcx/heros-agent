# Skill & Tool Language Coverage — Spec (folded from P14)

Product rationale: [`../../../docs/prd/P14-skills-tools-optimization.md`](../../../docs/prd/P14-skills-tools-optimization.md)
§6 (FR23–FR31). Design reasoning: [`../../changes/archive/2026-07-29-p14-skills-tools-optimization/design.md`](../../changes/archive/2026-07-29-p14-skills-tools-optimization/design.md) Decision 9, [`../../changes/archive/2026-07-29-p14-skills-tools-optimization/decisions.md`](../../changes/archive/2026-07-29-p14-skills-tools-optimization/decisions.md) D-14.5.

The cross-axis rules — coverage as a total function over every registered language, per-cell claims, the
three refusal classes and their evaluation order, one coverage source read by engine and surface and
command line, executable evidence for every row, no gate weakened to reach a language, offline parity, and
no plan-shaped coverage — are defined once in
[`language-coverage`](../language-coverage/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the skills and tools axis.

> **This is the axis with the narrowest coverage and the two most-confused refusals.** Skill binding
> materializes in Go and for two providers; tool pruning materializes in Go and nowhere else. Both
> boundaries are real, and neither is the boundary a reader assumes.
>
> **Binding a skill is construction**, and what must be constructed is a *provider SDK's tool value*. The
> shape comes from the skill's sealed input schema — that part is language-independent and stays exactly
> as it is, because the pinned version is what pins the shape. What is per language is the **spelling**:
> how this language's SDK for this provider writes a tool list, and which generation of that SDK. So the
> unit of coverage here is the cell **(language, provider, SDK generation)**, and a language with one
> provider's spelling is not a language with two.
>
> **Pruning a tool is deletion**, and its blocker is not the rewriter at all. A deletion needs to know
> which written element is which — and outside Go the discovery frontends record no tool split for a call
> site, so a prune has nothing to prune *against*. That is a **frontend** gap wearing a rewriter's
> clothing, and stating it as "no pruner has landed for this language" sends the reader to the wrong
> backlog.
>
> The third thing this capability must keep separate is the refusal a Python user actually hits today:
> a call site that passes its arguments as an unpacked mapping has no tool argument to replace and no
> written tool list to delete, and it would still have none the day a Python materializer lands. Telling
> that author their language is pending is true and useless.

## Requirements

### Requirement: Skill-materializer coverage SHALL be keyed by language, provider, and SDK generation

The skill-materializer coverage table SHALL carry an entry per **(language, provider, SDK generation)**.
A language SHALL NOT be described as materializing skills without the providers and SDK generations it is
true of, and a provider entry SHALL NOT be assumed to hold in a language for which no spelling is
declared.

#### Scenario: A covered language with an uncovered provider refuses by provider

- **WHEN** a call site is in a language with declared tool-value spellings, but its provider has none
- **THEN** the binding is refused naming the provider
- **AND** the refusal lists the providers that would have been materialized in that language
- **AND** the refusal does not state that the language has no materializer.

#### Scenario: A covered provider in an uncovered language refuses by cell

- **WHEN** a call site's provider is materializable in another language but not in this one
- **THEN** the refusal names the cell — the language and the provider together
- **AND** it names the missing spelling as the artifact that would close it.

#### Scenario: The SDK generation is part of the claim

- **WHEN** a coverage entry is read
- **THEN** it names the SDK generation its emitted spelling targets
- **AND** a call site on a different generation of the same SDK is not claimed as covered by it.

### Requirement: The sealed schema SHALL remain the sole source of a bound skill's shape in every language

In every language, a materialized skill's argument shape SHALL be derived from the pinned skill version's
sealed input schema. No language's materializer SHALL infer the shape from the surrounding call site, from
another tool present at the call site, or from a registry entry other than the pinned version.

#### Scenario: Two languages materialize one contract identically in meaning

- **WHEN** the same pinned skill is materialized in two languages
- **THEN** each expresses the same argument contract in its own SDK's spelling
- **AND** neither adds, drops, or loosens a field relative to the sealed schema.

#### Scenario: A schema with no argument shape refuses in every language

- **WHEN** a pinned skill's sealed schema declares no argument shape
- **THEN** the binding is refused in every language
- **AND** the refusal is classified as a fact about the contract rather than about the language.

### Requirement: A tool value SHALL be located by binding site, including a builder call and a request field

Materializing a bound skill and pruning a declared tool SHALL each locate the tool list by **binding
site**: an argument at the call site, a builder-chain call that sets the tools before the call, or a field
of a request value constructed before the call. A language whose SDKs bind tools on a builder SHALL NOT be
refused for lacking a tools argument.

#### Scenario: A builder-bound tool list is materializable

- **WHEN** a call site's tools are set by a builder-chain call
- **THEN** the binding site is located
- **AND** a bound skill is materialized into it
- **AND** a declared tool in it is prunable.

#### Scenario: A tool list bound nowhere locatable refuses naming the SDK

- **WHEN** an SDK carries its tools inside an opaque serialized body with no locatable declaration
- **THEN** the refusal names the SDK and that fact
- **AND** it is classified as a fact about that SDK rather than about the language.

### Requirement: Every discovery frontend SHALL record the node's tool split with a locatable declaration

Each language frontend SHALL classify a node's discovered entries into tools and skills and SHALL record,
for each tool, the identifier the call site uses and the location of its declaration. A tool the frontend
cannot locate as a written declaration SHALL be recorded as having none, so a prune against it refuses
rather than deleting nothing.

#### Scenario: A non-Go frontend records a prunable tool set

- **WHEN** a node in any registered language declares a static tool list
- **THEN** the frontend records each tool's identifier and the location of its declaration
- **AND** a selection over that set resolves and materializes as a deletion.

#### Scenario: An unlocatable tool is recorded as unlocatable, not omitted

- **WHEN** a node's tool set is assembled at run time
- **THEN** the frontend records that the declaration has no location
- **AND** a prune is refused naming the run-time assembly
- **AND** the tool is not silently absent from the node's recorded set.

### Requirement: Tool pruning SHALL be available in every language whose frontend records the split, as a deletion that changes no line count

Pruning SHALL be expressed in every such language as the deletion of an already-written element, SHALL
construct nothing, and SHALL NOT change the file's line count.

#### Scenario: A prune in a syntactic language deletes only the element

- **WHEN** a tool is pruned in a language located by spans
- **THEN** only that element's bytes and its separator are removed
- **AND** the file's line count is unchanged
- **AND** the result parses.

#### Scenario: A prune of an unlocatable set refuses rather than deleting nothing

- **WHEN** a selection is submitted for a node whose tool declarations have no recorded location
- **THEN** the prune is refused naming the node
- **AND** no deletion site is inferred.

### Requirement: Skill-binding coverage and tool-pruning coverage SHALL be stated as two tables

The two mechanics SHALL publish separate coverage, and a claim about one SHALL NOT be read as a claim
about the other. A language MAY prune before it can bind, and MAY bind for one provider while pruning for
all.

#### Scenario: Pruning coverage is read independently of binding coverage

- **WHEN** an interface or document states what this axis can apply in a language
- **THEN** binding coverage and pruning coverage are stated separately
- **AND** neither is presented as implying the other.

### Requirement: A skill or tool refusal SHALL be ordered specific-first, with the language asked last

For a refused skill or tool change, the reported cause SHALL be the most specific true one, evaluated in
the order: the skill contract, then the provider and SDK form, then the registry row's locator, then the
call site's own source, then the language.

#### Scenario: An unpacked call site is told about the unpacking

- **WHEN** a call site passes its arguments as an unpacked mapping and its language has no materializer
- **THEN** the refusal names the unpacked arguments as the reason there is no tool binding to write
- **AND** it does not report the missing materializer as the operative cause.

#### Scenario: A run-time tool set is told about the run-time assembly

- **WHEN** a call site assembles its tool set at run time in a language with no materializer
- **THEN** the refusal names the run-time assembly
- **AND** it states that a materializer would not change that outcome.

#### Scenario: An unpinned or unknown skill refuses ahead of every language question

- **WHEN** a binding names a skill that is unknown or carries no pinned version
- **THEN** the refusal names the skill and the pin
- **AND** it is identical in every language.

### Requirement: Authoring on this axis SHALL read the same per-cell coverage before offering a skill or a tool

An authoring surface SHALL decide whether to offer a skill binding on a node from the same
(language, provider, SDK generation) coverage the transform refuses from, and whether to offer a prune from
the same recorded tool set the transform deletes from.

#### Scenario: A skill is not offered for an uncovered cell, and the cell is named

- **WHEN** a node's language and provider have no declared spelling
- **THEN** skills are not offered as applicable choices
- **AND** the surface states the language and the provider
- **AND** a submission is refused with the transform's own typed cause.

#### Scenario: A prune is offered only over the recorded set

- **WHEN** a prune is offered on a node
- **THEN** the selectable tools are exactly those the frontend recorded with a location
- **AND** free-text entry is not a selection path.

### Requirement: Extending skill or tool coverage SHALL NOT change an existing materialization or hash

Adding a language, a provider spelling, an SDK generation, or a frontend's tool split SHALL leave every
previously materializable change byte-identical, every previously refused case either still refused or
covered by its own new entry, and every `config_hash` unchanged.

#### Scenario: A new cell disturbs nothing that already worked

- **WHEN** a new coverage cell is added
- **THEN** previously materializable bindings and prunes emit byte-identical changes
- **AND** a node that binds no skill and prunes no tool hashes byte-identically to before the axis existed
- **AND** golden hash vectors reproduce unchanged.
