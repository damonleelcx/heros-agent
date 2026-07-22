# Registries — Spec Delta (P2)

Product rationale: [`../../../../../docs/prd/P2-config-runtime.md`](../../../../../docs/prd/P2-config-runtime.md) §6 (FR6–FR10).

Covers the four registries: **model**, **prompt**, **skill**, **context**.

## ADDED Requirements

### Requirement: Each registry entry SHALL receive an immutable, content-addressed version ID, and published versions SHALL never be mutated in place

Every published entry in the model, prompt, skill, and context registries SHALL be assigned an
immutable `version_id` derived from its content. Editing an entry SHALL create a new version;
an existing published version SHALL never be altered.

#### Scenario: Editing produces a new version, old one intact
- **WHEN** a registered prompt entry is edited and re-published
- **THEN** a new `version_id` is created for the edited content
- **AND** the original `version_id` still resolves to the original, unchanged content

#### Scenario: In-place mutation rejected
- **WHEN** a write attempts to modify the content of an already-published `version_id`
- **THEN** the write is rejected
- **AND** the stored content for that `version_id` is unchanged

### Requirement: Registries SHALL evolve additively so that Variant Specs pinning older versions keep resolving

Registry schema evolution SHALL follow expand-contract: adding fields or new versions SHALL NOT
break resolution of Variant Specs that reference older version IDs.

#### Scenario: Old spec resolves after a new version is published
- **WHEN** a new model entry version is published and the model schema gains an optional field
- **THEN** a Variant Spec pinning the older model `version_id` still resolves and executes
  unchanged

### Requirement: A prompt registry entry SHALL be a template with named variable slots that renders deterministically

A prompt entry SHALL be a template with named variable slots. Given identical variable bindings,
rendering the template SHALL produce identical output.

#### Scenario: Deterministic render
- **WHEN** the same prompt `version_id` is rendered twice with identical variable bindings
- **THEN** both renders produce byte-identical output

#### Scenario: Missing binding is an error, not a silent blank
- **WHEN** a prompt template with a required slot `{{query}}` is rendered without a binding for
  `query`
- **THEN** rendering fails with an error identifying the missing slot
- **AND** no partially-rendered prompt is passed to a provider

### Requirement: A skill registry entry SHALL carry a JSON-schema contract and the runtime SHALL validate against it before binding

Each skill entry SHALL map a skill name to a JSON-schema contract for its inputs and outputs plus
an implementation handle. The runtime SHALL validate tool availability and argument shape against
the JSON-schema contract before binding the skill to a node.

#### Scenario: Argument-shape violation caught before execution
- **WHEN** a node binds a skill and supplies an argument object that violates the skill entry's
  input JSON-schema
- **THEN** binding fails with a schema-validation error before the node executes
- **AND** the skill implementation is not invoked

#### Scenario: Unavailable skill rejected
- **WHEN** a Variant Spec's `skill_refs` names a skill version ID that is not present in the Skill
  Registry
- **THEN** resolution fails closed and the node does not execute

### Requirement: A model registry entry SHALL capture provider, model ID, and inference params as a versioned unit

A model entry SHALL record provider + model ID + inference params (temperature, max_tokens,
thinking budget, seed) as a single immutable versioned unit referenced by `model_ref`.

#### Scenario: Params are pinned by the version
- **WHEN** a Variant Spec references a model `version_id`
- **THEN** the provider, model ID, and all inference params resolve exactly as stored in that
  version
- **AND** changing any param requires publishing a new `version_id`
