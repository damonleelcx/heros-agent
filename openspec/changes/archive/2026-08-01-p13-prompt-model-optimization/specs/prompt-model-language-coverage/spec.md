# Prompt & Model Language Coverage — Spec Delta (P13)

Product rationale: [`../../../../../docs/prd/P13-prompt-model-optimization.md`](../../../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR52–FR56). Design reasoning: [`../../design.md`](../../design.md) Decision 13.

The cross-axis rules — coverage as a total function, per-cell claims, the three refusal classes and their
evaluation order, one coverage source, executable evidence for a row, no gate weakened, offline parity,
and no plan-shaped coverage — are defined once in [`language-coverage`](../language-coverage/spec.md) and
are **not restated here**. This capability adds only what is specific to the model and prompt axis.

> **What is actually missing, per language.** Model and prompt materialize today in Go, Python,
> TypeScript and JavaScript. The other three registered languages do not refuse for the same reason, and
> the difference is the whole design:
>
> - **Kotlin** has named arguments, and the syntactic analyzer already produces real spans and a real
>   insert point for them. It never gets used, because every Kotlin row in the signature registry declares
>   no argument locator — the SDKs those rows cover bind the model on a **builder** at construction, so
>   the call site genuinely has no model argument to point at.
> - **Java and Rust** have no named-argument form at a call site at all. Their SDKs bind the model on a
>   builder chain or in a request struct assembled before the call.
>
> Both sentences describe the same missing thing from two directions: the platform can rewrite an
> **argument**, and these SDKs do not bind at an argument. So the coverage work here is not "add three
> rewriters" — it is to generalize what the engine points at, from *the argument a call site wrote* to
> **the binding site the program wrote**, of which a named argument is one form, a builder-chain call is a
> second, and a struct-literal field is a third. That generalization is what makes Kotlin a registry-row
> change, and Java and Rust a frontend change plus rows, rather than three separate inventions.

## ADDED Requirements

### Requirement: The engine SHALL locate a value by binding site, of which a named argument is one form

The transform engine SHALL express the place a model, prompt, or provider parameter is stated as a
**binding site**, and SHALL support at least three forms of it: a **named argument** at the call site, a
**builder-chain call** that sets the value before the call, and a **field of a request value** constructed
before the call. A language that expresses a binding in one of these forms SHALL NOT be refused for
lacking another.

#### Scenario: A builder-bound model is located and rewritten

- **WHEN** a call site's model is set by a builder-chain call rather than by an argument
- **THEN** the binding site is located
- **AND** the override is materialized by replacing the value the builder call wrote
- **AND** the emitted change parses.

#### Scenario: A request-struct field is located and rewritten

- **WHEN** a call site's model is set on a request value constructed before the call
- **THEN** the field is located as a binding site
- **AND** the override is materialized at that field.

#### Scenario: The absence of a named argument is not by itself a refusal

- **WHEN** a language with no named-argument form carries a located builder or struct binding site
- **THEN** the change is not refused for the absence of named arguments
- **AND** the refusal, if any, names the specific missing locator or form.

### Requirement: The signature registry SHALL express where an SDK binds, not only which argument it names

A registry row SHALL be able to declare that its SDK binds a dimension at a builder call or a request
field, with the locator that finds it. A row that binds nowhere the frontend can locate SHALL say so
explicitly, and its refusal SHALL name the SDK's binding style rather than the language.

#### Scenario: A builder-binding row is expressible

- **WHEN** a registry row covers an SDK that binds the model on a builder
- **THEN** the row declares that binding form and its locator
- **AND** a call site matching that row is materializable
- **AND** no engine change is required to add another such row.

#### Scenario: A row that binds nowhere locatable refuses by SDK, not by language

- **WHEN** an SDK binds a dimension in a place the frontend cannot locate
- **THEN** the refusal names the SDK and the binding style
- **AND** it does not state that the language has no materializer
- **AND** it does not state that the language has no named arguments as the operative reason.

### Requirement: Every registered language SHALL carry model and prompt coverage entries with a named gap

The model and prompt coverage table SHALL carry an entry for each registered language stating, per
binding form, whether the axis materializes there and — where it does not — which of the registry row,
the frontend's binding-site extraction, or the rewriter is missing.

#### Scenario: Kotlin's gap is reported as a registry gap

- **WHEN** Kotlin's model coverage entry is read
- **THEN** it states that the language's binding form is rewritable
- **AND** it names the absence of a row declaring a call-site or builder locator as the cause
- **AND** it does not describe Kotlin as unsupported.

#### Scenario: Java's and Rust's gaps are reported as binding-form gaps

- **WHEN** Java's or Rust's model coverage entry is read
- **THEN** it names the binding form its SDKs use
- **AND** it names the frontend extraction or row that would close the gap.

### Requirement: An authoring surface SHALL state the model and prompt boundary for a node before a value is chosen

Before a user selects a model, a prompt version, or a parameter on a node, the surface SHALL state
whether that node can carry the change, derived from the shared coverage source. A node that cannot carry
it SHALL NOT be presented with an empty or silently shortened list.

#### Scenario: The boundary is stated, not implied by an empty list

- **WHEN** a node's language and binding form cannot carry a model change
- **THEN** the surface states that, naming the language and the binding form
- **AND** it does not render an empty selector
- **AND** a submission through any surface is refused with the transform's own typed cause.

### Requirement: Extending model and prompt coverage SHALL NOT change any existing materialization

Adding a binding form, a registry row, or a language's rewriter SHALL leave every previously
materializable call site's emitted change byte-identical and every `config_hash` unchanged.

#### Scenario: Existing diffs are unchanged by an added form

- **WHEN** a new binding form or language row is added
- **THEN** previously materializable call sites emit byte-identical changes
- **AND** golden hash vectors reproduce unchanged
- **AND** no previously refused call site becomes silently applied without its own coverage entry.
