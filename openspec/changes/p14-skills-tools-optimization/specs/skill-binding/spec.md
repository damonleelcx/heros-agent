# Skill Binding — Spec Delta (P14)

Product rationale: [`../../../../../docs/prd/P14-skills-tools-optimization.md`](../../../../../docs/prd/P14-skills-tools-optimization.md)
§6 (FR1–FR6), §7 and §8. Design reasoning: [`../../design.md`](../../design.md) Decisions 1, 2, 7;
one-way-door contract [`../../decisions.md`](../../decisions.md) D-14.3.

Covers making the **skill dimension applicable** — replacing the call-site refusal with real
materialization from the skill's sealed schema, verification-gated, while keeping the **interim refusal**
a first-class, testable behavior for every language whose rewriter has not yet landed.

> **Why materialize from the sealed schema, and gate on verification.** Binding a skill means
> constructing an SDK tool value (`[]anthropic.ToolParam{{Name, InputSchema}}` and its per-SDK
> equivalents) — code generation, not argument substitution. The transform refuses this today because a
> subtly-wrong tool schema *compiles and then degrades quality invisibly* — the worst failure for an eval
> platform. Deriving the shape from the skill's **sealed** input/output schema means the version_id that
> pinned the skill pinned the shape; scoring the resulting diff means a wrong shape is caught by
> measurement, not by hope. Neither alone is enough; both together make the axis safe to apply.

## ADDED Requirements

### Requirement: A bound skill SHALL be materialized at the call site from its sealed schema

For each supported language, applying a node that binds a skill SHALL construct the provider SDK's tool
value from the skill's sealed input/output schema (the registered `KindSkill` contract the `skill_ref`
pins). The constructed shape SHALL be derived from the contract, not inferred from a value.

#### Scenario: A bound skill produces an applicable diff in a supported language

- **WHEN** a node in a supported language binds a skill and the spec is applied
- **THEN** the transform emits an edit that constructs the SDK tool value for that skill
- **AND** the tool value's shape matches the skill's sealed input/output schema.

#### Scenario: The shape comes from the contract, not the value

- **WHEN** two skills share a name but pin different sealed schemas
- **THEN** each materializes to the tool value its own sealed schema describes
- **AND** the constructed shape is not inferred from any surrounding call-site value.

### Requirement: An un-applicable skill SHALL be refused at transform, never silently dropped

A node carrying a `skill_ref` whose language has no landed materializer SHALL be refused at transform with
`ErrUnsafeRewrite`, naming the node and the `skills` dimension and the reason. The override SHALL NOT be
silently removed, and no diff SHALL be emitted for that node's skill dimension.

#### Scenario: An unsupported language refuses loudly

- **WHEN** a node binds a skill in a language that has no landed materializer
- **THEN** the transform returns `ErrUnsafeRewrite` naming the node and the skills dimension
- **AND** no edit is emitted for that node's skill dimension.

#### Scenario: A silent drop is not permitted

- **WHEN** an un-applicable skill binding is processed
- **THEN** the skill is not removed from the node while the rest of the diff proceeds
- **AND** a change that silently omitted the binding while its `config_hash` still claimed it is treated
  as a defect, not a success.

### Requirement: Skill add, remove, and rerank operators SHALL be verification-gated

The `add`, `remove`, and `rerank` skill operators SHALL each produce a candidate Variant Spec that is
realized as a diff and scored by the evaluation harness. A skill change SHALL ship only on a verified
non-regression, never on the strength of the diagnosis alone.

#### Scenario: A regressing skill change does not ship

- **WHEN** a materialized skill change is scored and regresses the evaluated result
- **THEN** the change is not shipped
- **AND** the decision is the measured verdict, not the operator's prior.

#### Scenario: A skill change is a proposal until verified

- **WHEN** a skill operator emits a candidate
- **THEN** the candidate is treated as a proposal subject to verification
- **AND** it is not applied merely because a diagnosis code triggered it.

### Requirement: A materialized skill's arguments SHALL be validated against its input contract before execution

A materialized skill's arguments SHALL be checked against the skill's compiled input contract before the
node executes, so an argument-shape violation is caught before the implementation is invoked.

#### Scenario: An out-of-contract argument is rejected before execution

- **WHEN** a bound skill would be invoked with arguments that violate its input schema
- **THEN** the violation is reported before the node executes
- **AND** the skill implementation is not invoked.

### Requirement: A skill binding SHALL participate in config_hash while a no-skill node stays byte-identical

Adding, removing, or reranking a skill SHALL change `config_hash` — skill order is identity-bearing — and
a node that binds no skill SHALL hash byte-identically to a node that predates this capability.

#### Scenario: Reranking skills changes the hash

- **WHEN** the same skills are bound to a node in a different order
- **THEN** the node's `config_hash` differs
- **AND** the order is reflected in the resolved skill references (which are not sorted).

#### Scenario: A no-skill node is byte-identical to pre-P14

- **WHEN** a node binds no skill
- **THEN** its canonical resolved bytes are identical to how they serialized before this capability
  existed
- **AND** the P0 golden `config_hash` vectors continue to reproduce.

### Requirement: A materialized skill SHALL surface failures only through the typed error envelope

A materialized skill SHALL surface tool-call failures only through the `toolcontract` typed envelope's
allowlisted error codes. It SHALL NOT introduce an error code outside the whitelist, so `tool_error_rate`
remains well-defined for a bound node.

#### Scenario: No out-of-whitelist error code is emitted

- **WHEN** a bound skill's tool call fails
- **THEN** the failure is reported with an allowlisted `toolcontract` error code
- **AND** no error code outside the whitelist appears on any path, so `tool_error_rate` stays
  well-defined.
