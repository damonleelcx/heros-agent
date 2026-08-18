# Variable Bindings — Spec (folded from P10)

Product rationale: [`../../../docs/prd/P10-prompt-model-studio.md`](../../../docs/prd/P10-prompt-model-studio.md)
§6 (FR7–FR14) and §8.1. Design reasoning: [`../../changes/archive/2026-08-01-p10-prompt-model-studio/design.md`](../../changes/archive/2026-08-01-p10-prompt-model-studio/design.md) Decisions 2 and 3.

Covers making a prompt slot bindable to something the call site does not already spell: an explicit
`bindings` map on a node override with four validated source kinds, **all validated at spec-resolve
time before any transformation is generated**, while preserving every protective refusal the existing
codemod already makes.

> **Why this exists.** Today a slot binds only to a call-site expression **spelled identically**, and
> the engine refuses to guess otherwise. That refusal is correct — guessing which value belongs in a
> slot is how a rewrite silently ships the wrong prompt. The gap is that **explicit binding is not
> expressible**, so a prompt edited to add a variable cannot be applied anywhere. This capability adds
> a way to state what the engine declines to infer; it does not make the engine infer anything.

## Requirements

### Requirement: A node override SHALL support an explicit bindings map with four source kinds

A node override SHALL support a map from prompt slot name to a binding source. A binding source SHALL
be of kind **`literal`** (a constant value), **`expr`** (an expression in scope at the call site),
**`env`** (a named environment variable), or **`input`** (a typed value from the node's input).

#### Scenario: Each source kind is expressible

- **WHEN** a node override binds one slot to a literal, one to an in-scope expression, one to an
  environment variable, and one to a typed input
- **THEN** each binding is accepted as a distinct source kind
- **AND** the kind is recorded explicitly rather than inferred from the value's shape.

#### Scenario: An unrecognized source kind is rejected

- **WHEN** a binding declares a source kind outside the defined set
- **THEN** the specification is rejected
- **AND** the rejection names the offending node and slot.

### Requirement: Every slot of a node's pinned prompt SHALL be satisfied exactly once, or the specification SHALL be rejected

Every slot declared by a node's pinned prompt version SHALL be satisfied by exactly one of: an explicit
binding, or a call-site expression spelled identically to the slot name. A slot satisfied by neither,
or by both, SHALL cause the specification to be **rejected**, naming the node, the dimension, and the
slot.

#### Scenario: An unsatisfied slot is rejected with the slot named

- **WHEN** a node pins a prompt version declaring a slot that has no explicit binding and no
  identically-spelled call-site expression
- **THEN** the specification is rejected
- **AND** the rejection names the node, the dimension, and the slot.

#### Scenario: A slot satisfied by both a binding and a call-site expression is rejected

- **WHEN** a slot has an explicit binding and the call site also supplies an identically-spelled
  expression
- **THEN** the specification is rejected as ambiguous
- **AND** the rejection names the node and the slot, rather than silently preferring one source.

#### Scenario: A fully satisfied slot set resolves

- **WHEN** every slot of a node's pinned prompt is satisfied exactly once
- **THEN** the specification resolves
- **AND** each slot records the source that satisfied it.

### Requirement: All binding validation SHALL occur at specification resolution, before any transformation is generated

Every binding failure SHALL be detected when the Variant Spec is resolved, before a transformation is
generated. No binding failure class SHALL first become visible at transformation time.

#### Scenario: A binding failure is reported before any transformation exists

- **WHEN** a specification contains any invalid binding
- **THEN** it is rejected during resolution
- **AND** no transformation is generated, no worktree is created, and no build is attempted.

#### Scenario: Binding failures use the existing specification error shape

- **WHEN** a binding is rejected
- **THEN** the failure is reported through the same error shape used for other specification errors,
  carrying node, dimension, and the offending reference
- **AND** a second error channel is not introduced for this class of mistake.

### Requirement: An expr binding SHALL be validated against the in-scope symbols recorded for that call site

An `expr` binding SHALL name an expression the intermediate representation records as in scope at that
node's call site. An expression not so recorded SHALL be rejected at resolution.

#### Scenario: An out-of-scope expression is rejected at resolution

- **WHEN** an `expr` binding names an expression not recorded as in scope at that call site
- **THEN** the specification is rejected during resolution
- **AND** the rejection names the node, the slot, and the expression.

#### Scenario: An in-scope expression is accepted

- **WHEN** an `expr` binding names an expression recorded as in scope at that call site
- **THEN** the binding is accepted
- **AND** the transformation supplies that expression at the call site.

#### Scenario: A conservative scope record fails closed

- **WHEN** the recorded in-scope symbol set is narrower than the true lexical scope
- **THEN** a binding naming an unrecorded expression is rejected rather than accepted
- **AND** the failure direction is a false rejection, never a false acceptance.

### Requirement: An env binding SHALL name a declared variable, and an absent value at run time SHALL be a typed failure

An `env` binding SHALL name a declared environment variable. When that variable has no value at run
time, the node SHALL fail with a typed error. An empty or default value SHALL NOT be substituted into
the prompt.

#### Scenario: An undeclared environment variable is rejected at resolution

- **WHEN** an `env` binding names a variable that is not declared
- **THEN** the specification is rejected during resolution
- **AND** the rejection names the node, the slot, and the variable.

#### Scenario: An absent value at run time fails typed, not silently

- **WHEN** an `env` binding's variable has no value at run time
- **THEN** the node fails with a typed error identifying the variable
- **AND** no prompt is sent to a provider with an empty substitution in that slot.

### Requirement: An input binding SHALL satisfy the node's typed I/O contract

An `input` binding SHALL reference a value admitted by the node's typed input contract. A binding whose
referenced value violates that contract SHALL be rejected at resolution.

#### Scenario: A contract-violating input binding is rejected

- **WHEN** an `input` binding references a value that the node's typed input contract does not admit
- **THEN** the specification is rejected during resolution
- **AND** the rejection names the node, the slot, and the contract violation.

#### Scenario: A contract-satisfying input binding is accepted

- **WHEN** an `input` binding references a value the node's typed input contract admits
- **THEN** the binding is accepted.

### Requirement: A call-site expression that no slot binds SHALL remain a refusal

A call-site expression feeding the prompt that no slot binds SHALL cause the transformation to be
refused. The introduction of explicit bindings SHALL NOT weaken this refusal.

#### Scenario: An unclaimed call-site value still refuses

- **WHEN** a call site feeds an expression into its prompt and no slot of the pinned prompt version
  binds it
- **THEN** the transformation is refused
- **AND** the refusal names the unclaimed expression, because rewriting past it would silently drop a
  runtime value.

#### Scenario: Explicit bindings do not license dropping a call-site value

- **WHEN** every slot is satisfied by explicit bindings while the call site still feeds an unclaimed
  expression into its prompt
- **THEN** the transformation is still refused
- **AND** satisfying the slots elsewhere does not make the unclaimed value safe to discard.

### Requirement: Bindings SHALL be part of the resolved configuration and SHALL extend config_hash additively

Bindings SHALL be part of the resolved configuration, so the configuration hash changes if and only if
a binding changes. A specification that declares no bindings SHALL produce the same configuration hash
it produces today.

#### Scenario: Changing a binding changes the configuration hash

- **WHEN** a specification's binding for a slot is changed and nothing else is
- **THEN** the resulting configuration hash differs.

#### Scenario: A specification with no bindings hashes unchanged

- **WHEN** a specification that declares no bindings is hashed
- **THEN** the resulting configuration hash is identical to the hash it produced before bindings
  existed
- **AND** existing specifications remain reproducible.

#### Scenario: Two specifications differing only in binding order hash identically

- **WHEN** two specifications declare the same bindings in a different order
- **THEN** they produce the same configuration hash
- **AND** the hash depends on the binding set, not on its serialization order.
