# Harness Strategy — Spec Delta (P18)

Product rationale: [`../../../../../docs/prd/P18-harness-strategy-optimization.md`](../../../../../docs/prd/P18-harness-strategy-optimization.md)
§6 (FR1–FR7), §7 (NFR8, NFR9). Design reasoning: [`../../design.md`](../../design.md) Decisions 1, 2, 7;
[`../../decisions.md`](../../decisions.md) D-1.

Covers the strategy catalog — the new content-addressed registry Kind `harness` and the five builtin
strategies with their params schemas. This delta does **not** cover the Dimension, resolution, hashing,
refusal, or operator — those are the `agent-loop` capability.

> **Why strategies are a versioned registry Kind, not an in-code table.** Content addressing is the
> platform's lineage guarantee: a stored `config_hash` must resolve the exact strategy bytes that produced
> it. An in-code table cannot be pinned — the moment it changes, a stored hash no longer resolves the
> strategy that produced it. The Kind is hashed into every `version_id`, so a `harness` ref used in another
> dimension fails closed rather than colliding.

## ADDED Requirements

### Requirement: The registry SHALL provide a content-addressed Kind for harness strategies

The registry SHALL provide a fifth Kind, `harness`, whose entries are sealed and decoded by content
address exactly as the model, prompt, skill, and context Kinds. A harness `version_id` SHALL be unique
across all registries.

#### Scenario: A strategy seals to a content-addressed version_id

- **WHEN** a harness strategy is registered
- **THEN** it is sealed to a `version_id` derived from its content and Kind
- **AND** an identical strategy re-registered yields the same `version_id`.

#### Scenario: A harness ref used in a non-harness dimension fails closed

- **WHEN** a `harness` `version_id` is supplied where a model, prompt, skill, or context ref is expected
  (or vice versa)
- **THEN** resolution fails closed with a typed error
- **AND** the ref does not resolve to an entry of the wrong Kind.

### Requirement: The builtin catalog SHALL enumerate five strategies

The builtin catalog SHALL register exactly five strategies — `single-shot`, `react-loop`, `plan-execute`,
`reflexion`, and `critic-loop` — each a content-addressed entry.

#### Scenario: The five builtins are registered and resolvable

- **WHEN** the builtin catalog is loaded
- **THEN** `single-shot`, `react-loop`, `plan-execute`, `reflexion`, and `critic-loop` are each present as
  a resolvable entry
- **AND** each has a distinct `version_id`.

### Requirement: Each strategy SHALL declare a params schema

Each strategy SHALL declare a params schema covering, as applicable to it, `max_turns`, a stop condition,
an optional critic model ref, and a retry budget. A param inapplicable to a strategy SHALL be inexpressible
for it, not silently accepted and ignored.

#### Scenario: A multi-turn strategy declares max_turns and a stop condition

- **WHEN** a `react-loop`, `plan-execute`, `reflexion`, or `critic-loop` strategy is inspected
- **THEN** its params schema declares a bounded `max_turns` and a stop condition.

#### Scenario: A param inapplicable to a strategy is inexpressible

- **WHEN** a param not applicable to a strategy is supplied (for example a critic model ref on `single-shot`)
- **THEN** the strategy rejects it
- **AND** it is not silently dropped or ignored.

### Requirement: A HarnessRef SHALL be a version_id only; inline strategy definitions SHALL be rejected

A harness override SHALL reference a strategy by its immutable `version_id` and nothing else. A spec that
inlines a strategy definition SHALL be rejected.

#### Scenario: An inline strategy definition is rejected

- **WHEN** a spec carries an inline strategy definition instead of a `version_id`
- **THEN** the spec is rejected with a typed error
- **AND** the reason names the inline definition.

#### Scenario: A version_id resolves the exact strategy bytes

- **WHEN** a `HarnessRef` is resolved
- **THEN** it yields the exact strategy content sealed under that `version_id`
- **AND** the same ref resolves to the same content on any later resolution.

### Requirement: Strategy params SHALL be validated at seal, not at resolve

Strategy params SHALL be validated when the strategy is sealed/registered. `max_turns` SHALL be a bounded
positive integer, a declared critic model ref SHALL resolve to a `model` registry entry, and a retry budget
SHALL be bounded. An out-of-range or unresolvable param SHALL fail registration.

#### Scenario: An out-of-range max_turns fails registration

- **WHEN** a strategy is registered with a `max_turns` that is not a bounded positive integer
- **THEN** registration fails with a typed error naming the param
- **AND** no entry is sealed.

#### Scenario: An unresolvable critic model ref fails registration

- **WHEN** a strategy declares a critic model ref that does not resolve to a `model` entry
- **THEN** registration fails
- **AND** the failure names the unresolvable ref.

### Requirement: single-shot SHALL be the explicit identity of the implicit default

The `single-shot` strategy SHALL represent today's implicit one-call default. Resolving a node that already
runs a single call to `single-shot` SHALL be a no-op on behavior and on `config_hash`.

#### Scenario: single-shot on a one-call node changes nothing

- **WHEN** a node that already runs a single call is resolved with `single-shot`
- **THEN** its behavior is unchanged
- **AND** its `config_hash` is identical to the same node with no harness override.

### Requirement: critic-loop SHALL pair a generator with a separate critic model

The `critic-loop` strategy SHALL express a generator paired with a **separate** critic model, named by its
own model ref in the params schema.

#### Scenario: critic-loop names a separate critic model

- **WHEN** a `critic-loop` strategy is inspected
- **THEN** its params declare a critic model ref distinct from the generating node's model
- **AND** the critic ref resolves to a `model` registry entry.
