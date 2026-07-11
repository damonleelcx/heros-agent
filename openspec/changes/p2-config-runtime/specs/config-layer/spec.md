# Configuration Layer — Spec Delta (P2)

Product rationale: [`../../../../docs/prd/P2-config-runtime.md`](../../../../docs/prd/P2-config-runtime.md) §6 (FR1–FR5).

## ADDED Requirements

### Requirement: The shim SHALL resolve each node's four override dimensions from the Variant Spec at invocation time

The Configuration Layer wraps each discovered call site in a shim. At the moment a node is about to
execute, the shim SHALL resolve its **model**, **prompt**, **skills**, and **context** dimensions
from the active Variant Spec and the registries — never from hardcoded source literals.

#### Scenario: Override takes effect without source change
- **WHEN** a Variant Spec sets `model_ref` for node `N` to a registry model entry different from
  the IR-captured default
- **THEN** node `N` executes using the overridden model
- **AND** the target repository source for node `N` is not modified

#### Scenario: Absent override falls back to the IR default
- **WHEN** a Variant Spec entry for node `N` omits `prompt_ref`
- **THEN** the shim resolves node `N`'s prompt to the IR-captured default prompt
- **AND** the run proceeds without error

### Requirement: Each override dimension SHALL be independently overridable

A node's four dimensions SHALL be settable independently; overriding one dimension SHALL NOT force
re-specification of the others.

#### Scenario: Model-only override
- **WHEN** a Variant Spec sets only `model_ref` for node `N` and omits `prompt_ref`,
  `skill_refs`, and `context_policy`
- **THEN** node `N` runs with the overridden model and default prompt, skills, and context
- **AND** no other node's configuration changes

### Requirement: A Variant Spec SHALL be a per-node reference map plus a node ordering, referencing registry entries by immutable ID only

A Variant Spec SHALL have the structure `{node_id → {model_ref, prompt_ref, skill_refs[],
context_policy}}` together with a node ordering/graph. Every `*_ref` SHALL be an immutable registry
version ID; inline definitions SHALL NOT be permitted.

#### Scenario: Spec references entries by version ID
- **WHEN** a Variant Spec is submitted
- **THEN** every `model_ref`, `prompt_ref`, `skill_ref`, and `context_policy` is an immutable
  registry version ID
- **AND** a spec that inlines a model/prompt/skill definition instead of referencing a version ID
  is rejected

### Requirement: A Variant Spec SHALL hash to a stable config_hash that changes iff a referenced version or the ordering changes

The `config_hash` SHALL be derived from a canonical serialization of the Variant Spec that is
invariant to key ordering and serialization whitespace, pinning each `*_ref` to its immutable
version ID and including the node ordering.

#### Scenario: Whitespace and key order do not change the hash
- **WHEN** two syntactically different serializations of the same Variant Spec (differing only in
  key order and whitespace) are hashed
- **THEN** they produce the identical `config_hash`

#### Scenario: Changing a referenced version changes the hash
- **WHEN** a node's `prompt_ref` is changed to a different immutable version ID
- **THEN** the resulting `config_hash` differs from the original
- **AND** changing the node ordering while keeping all refs identical also changes the `config_hash`

### Requirement: The system SHALL reject an invalid Variant Spec before any node executes

A Variant Spec that references a non-existent node, a `*_ref` that does not resolve to a registry
entry, or a `context_policy` that is not registered SHALL be rejected before execution begins, with
no side effects.

#### Scenario: Unregistered context policy rejected up front
- **WHEN** a Variant Spec sets `context_policy` for node `N` to a name that is not registered
- **THEN** the spec is rejected before any node executes
- **AND** no run record or provider call is created
- **AND** the rejection names node `N` and the offending dimension
