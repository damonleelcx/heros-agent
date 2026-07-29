# Language Coverage — Spec (folded from P13)

Product rationale: [`../../../docs/prd/P13-prompt-model-optimization.md`](../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR41–FR51), §7 (NFR19–NFR23). Design reasoning: [`../../changes/p13-prompt-model-optimization/design.md`](../../changes/p13-prompt-model-optimization/design.md) Decision 13.

This capability is the **cross-axis contract for what the apply path can do in which language**. It is
defined once here and referenced — never restated — by the per-axis coverage capabilities:
[`prompt-model-language-coverage`](../prompt-model-language-coverage/spec.md) (P13),
[`skill-tool-language-coverage`](../../changes/p14-skills-tools-optimization/specs/skill-tool-language-coverage/spec.md) (P14),
[`wiring-language-coverage`](../../changes/p15-workflow-wiring-optimization/specs/wiring-language-coverage/spec.md) (P15), and
[`context-language-coverage`](../context-language-coverage/spec.md) (P16).

> **The one sentence this capability exists to enforce: every language discovery finds, the apply path
> either changes or refuses by name — and "we only built Go" is a cause with an owner, not a category.**
>
> Discovery ships seven language frontends. The transform engine registers all seven
> ([`internal/transform/engines.go`](../../../internal/transform/engines.go)) — but what each one can
> *apply* differs per axis, and today it differs by a lot: model and prompt materialize in four languages,
> context and wiring in two, skill binding and tool pruning in one. That spread is not the defect. The
> defect is that the spread is discoverable only by reading five tables in three packages, that some cells
> have no row at all, and that a call site whose real problem is its own shape can still be told the
> problem is its language.
>
> So this capability fixes the shape of the claim before it grows the claim. Coverage is a **total
> function** over (axis × registered language × the form the axis binds against) — every cell has a value,
> and "absent" is not one of them. A refusal names **which of three different things** is missing: the
> change is not expressible at a call site in any language, or this call site's own source cannot carry
> it, or this language has no materializer yet. Those are answered by three different people — the
> platform's designer, the customer's engineer, and the platform's backlog — and collapsing them sends the
> reader to the wrong one.
>
> The corollary is the part that must not soften under schedule pressure: **the target is every
> registered language on every axis**, and a language sits in the "no materializer" cell only while it
> carries a named missing artifact. What it may never do is get there by weakening a gate. A materializer
> that reaches a new language by guessing an SDK's spelling, skipping the reparse check, or inferring a
> binding site nobody wrote has not extended coverage — it has moved the failure from a refusal a user
> reads to a diff that compiles and is wrong.

## Requirements

### Requirement: Coverage SHALL be stated as a total function over every registered language

For every optimization axis that materializes at a call site, the platform SHALL publish a coverage
table with an entry for **every** language the discovery frontend registers, and for every form that axis
binds against within it. A language or form that the axis cannot apply SHALL be present in the table with
a refusal cause. Absence from a coverage table SHALL NOT be a way of expressing a limitation.

#### Scenario: Every registered language has an entry on every axis

- **WHEN** an axis's coverage is enumerated
- **THEN** every language registered by the discovery frontend appears in it
- **AND** each entry states either that the axis materializes there or the named cause that it does not
- **AND** no registered language is absent.

#### Scenario: A newly registered language cannot ship without a coverage answer

- **WHEN** a language frontend is added to discovery
- **THEN** each axis's coverage table gains an entry for it
- **AND** a check fails while any axis has no entry for that language.

### Requirement: A coverage claim SHALL be per cell, never per language

A coverage entry SHALL identify the language **and** the form the axis binds against — the provider and
SDK generation for a tool value, the registry row and its locator for an argument, the policy for a
context selection, the statement form for a wiring move. A statement that an axis "supports" a language
SHALL NOT be published without the cells it is true of.

#### Scenario: A supported language with an unsupported cell refuses by cell

- **WHEN** a call site is in a language the axis materializes, but its provider, registry row, or policy
  has no entry
- **THEN** the change is refused naming that form
- **AND** the refusal states which forms in that language would have been materialized
- **AND** the refusal does not describe the language as unsupported.

#### Scenario: No surface publishes a language-level capability claim

- **WHEN** an interface, document, or generated description states what the axis can apply
- **THEN** the statement is derived from the coverage table's cells
- **AND** no surface states a capability the table does not carry.

### Requirement: A refusal SHALL name which of three different things is missing

Every materialization refusal SHALL carry a typed cause belonging to exactly one of three classes:

1. **not-expressible-at-a-call-site** — the change cannot be written into source in any language, because
   the value does not exist until run time (a summarized context, a retrieved chunk set, a routing
   decision that belongs at the gateway).
2. **call-site-cannot-carry-it** — this call site's own source cannot express the change: the argument
   set is unpacked from a variadic mapping, the tool list is assembled at run time, no registry row
   declares a locator for the SDK it uses, the value is bound on a builder the frontend did not record.
3. **no-materializer-for-this-language** — the platform has not landed the rewriter, splitter, resolver,
   or form table for this cell.

The three SHALL be distinguishable by a stable identifier, not only by prose.

#### Scenario: The three causes are distinguishable programmatically

- **WHEN** refusals of each class are raised
- **THEN** each carries a distinct stable cause identifier
- **AND** a consumer can classify a refusal without parsing its sentence.

#### Scenario: A run-time-assembled value is not reported as a language gap

- **WHEN** a change is refused because the call site assembles the value at run time
- **THEN** the cause is `call-site-cannot-carry-it`
- **AND** the refusal does not state that the language has no materializer
- **AND** it does not imply that a future rewriter would apply the change to that call site.

#### Scenario: A change that no language can carry is not reported as a language gap

- **WHEN** a change is refused because its value does not exist until run time
- **THEN** the cause is `not-expressible-at-a-call-site`
- **AND** the refusal is identical in every language
- **AND** it does not promise a rewriter.

### Requirement: A refusal SHALL report the most specific true cause, asking the language question last

When more than one refusal class is true of a call site, the reported cause SHALL be the most specific
one. The order of evaluation SHALL be: the change itself, then the registry row, then the call site's own
source, then the language. A language-level cause SHALL NOT be reported while a more specific true cause
exists.

#### Scenario: An unpacked call site in an unsupported language reports the unpacking

- **WHEN** a call site in a language with no materializer also passes its arguments as an unpacked
  mapping
- **THEN** the refusal names the unpacking
- **AND** it does not name the missing materializer as the reason
- **AND** the same call site refuses identically once that language's materializer lands.

#### Scenario: A not-at-call-site change refuses ahead of every other question

- **WHEN** a change that no language can carry is submitted on a call site that also has no registry row
- **THEN** the refusal names the change
- **AND** it is the same sentence in every language.

### Requirement: Every registered language SHALL be a coverage target, and a gap SHALL name its missing artifact

An axis SHALL treat materialization in every registered language as its target state. Where a cell is not
materialized because the platform has not built it, the coverage entry SHALL name the specific missing
artifact — the form table row, the list splitter, the statement resolver, the registry row, the frontend
field — rather than describing the language as unsupported or the work as pending.

#### Scenario: A platform gap names what would close it

- **WHEN** a `no-materializer-for-this-language` entry is read
- **THEN** it names the artifact whose absence causes it
- **AND** it does not describe the language as unsupported by the platform.

#### Scenario: A structural impossibility is not filed as a platform gap

- **WHEN** a cell cannot be materialized because the language or SDK expresses the value nowhere the
  frontend can locate
- **THEN** the entry states that fact as its cause
- **AND** it is not counted as a `no-materializer-for-this-language` gap.

### Requirement: Coverage SHALL be read from one source by the engine, the authoring surface, the command line, and every document

The transform's refusal, an authoring surface's pre-submission answer, the command line's offline
verdict, the console's rendering, and any published coverage table SHALL derive from the same coverage
source. A surface SHALL NOT hold its own list of what a language can carry.

#### Scenario: The editor cannot offer what the engine refuses

- **WHEN** an authoring surface decides whether to offer a change on a node
- **THEN** the answer is derived from the same coverage source the transform refuses from
- **AND** a test fails if the surface offers a cell the engine refuses
- **AND** a test fails if the engine materializes a cell the surface does not offer.

#### Scenario: A published table cannot drift from the engine

- **WHEN** a human-readable coverage table is published
- **THEN** a check fails when it stops matching the engine's table
- **AND** the check names the differing cell.

### Requirement: A coverage row SHALL be admitted only on executable evidence, and SHALL NOT weaken any gate

Adding a coverage entry SHALL require an executable proof that the emitted change is well-formed in that
cell — the language's reparse assertion and, where the change constructs source, the build gate.
Extending coverage SHALL NOT remove, relax, or make optional any gate that applied before the extension.

#### Scenario: A row without a proof is not a row

- **WHEN** a coverage entry is proposed for a cell
- **THEN** it is accompanied by a test that emits the change in that cell and asserts the result parses
- **AND** where the change constructs source, the build gate is asserted for it
- **AND** the entry is rejected without them.

#### Scenario: No language reaches coverage by skipping a check

- **WHEN** the engines are enumerated
- **THEN** every language's engine supplies every gate the contract requires
- **AND** a language whose engine omits one is rejected rather than served
- **AND** no configuration disables a gate for one language only.

### Requirement: The same override SHALL mean the same thing in every language

A given resolved configuration SHALL have one meaning across every language's materializer. A language's
implementation SHALL NOT apply a broader, narrower, or different interpretation of the same override.

#### Scenario: One spec, one meaning

- **WHEN** the same resolved override is materialized in two languages
- **THEN** the resulting change expresses the same configuration in each
- **AND** neither language applies an interpretation the other refuses
- **AND** a divergence is a defect rather than a per-language behavior.

### Requirement: The offline surface SHALL carry the same coverage table, versioned, and name its version in a refusal

The command-line surface SHALL reach its coverage verdict from a local copy of the same table, SHALL
report the same typed cause text as the hosted surface, and SHALL name the table's version in a refusal so
a verdict that differs from the hosted one is diagnosable rather than mysterious.

#### Scenario: An offline refusal is identical and self-identifying

- **WHEN** a change is refused offline for a coverage reason
- **THEN** the typed cause matches the hosted surface's
- **AND** the refusal names the version of the local coverage table
- **AND** no network call is required to reach the verdict.

#### Scenario: A stale local table is visible rather than silent

- **WHEN** the local coverage table is older than the platform's
- **THEN** the difference is reportable by version
- **AND** a refusal caused by the older table can be attributed to it.

### Requirement: A workflow spanning more than one language SHALL be refused by name

A patch SHALL be emitted for one language, because one verifier gates it. A workflow whose nodes span
more than one language SHALL be refused with a cause naming the languages found, and SHALL NOT be
materialized in the language of the majority of its nodes.

#### Scenario: A polyglot workflow refuses and says what it found

- **WHEN** a workflow's discovered nodes span more than one language
- **THEN** the refusal names the languages
- **AND** no patch is emitted
- **AND** no language is selected on behalf of the user.

### Requirement: A coverage gap SHALL NOT be presented as a product capability, a plan boundary, or an override

Interface text, machine payloads, generated narration, and commercial material SHALL describe an
unmaterialized cell as not yet applied by the platform. They SHALL NOT present it as a plan limitation, a
setting, or something an entitlement, role, or flag would unlock.

#### Scenario: A coverage refusal carries no upgrade path

- **WHEN** a coverage refusal is rendered on any surface
- **THEN** it does not offer a plan, role, flag, or setting as a way to obtain the change
- **AND** it states the missing artifact instead.

#### Scenario: Coverage is identical on every plan

- **WHEN** the same call site is submitted under different plans and roles
- **THEN** the coverage verdict is identical
- **AND** no entitlement changes which cells materialize.
