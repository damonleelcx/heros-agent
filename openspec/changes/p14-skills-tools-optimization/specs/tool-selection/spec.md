# Tool Selection — Spec Delta (P14)

Product rationale: [`../../../../../docs/prd/P14-skills-tools-optimization.md`](../../../../../docs/prd/P14-skills-tools-optimization.md)
§6 (FR7–FR14), §7 and §8. Design reasoning: [`../../design.md`](../../design.md) Decisions 3, 4, 5, 6, 7;
one-way-door contracts [`../../decisions.md`](../../decisions.md) D-14.1, D-14.2.

Covers splitting tools from skills in the IR (additive, append-only), and making **tool selection** —
pruning and minimization — a first-class dimension scored by the existing axis-agnostic harness.

> **Why the split is additive and a tool is not a registry `Kind`.** The IR conflates tools and skills
> into one frozen slice, and a tool prune (deletion of an already-present tool) cannot be expressed while
> that slice also carries skills (bound by construction). The split is additive — new `omitempty`
> fields, the frozen slice retained — because repurposing a frozen field breaks the golden vectors and
> orphans `config_hash`-keyed rows. A tool is **selected** against the node's discovered set, not
> resolved from a registered ref: it is already identified by its call site, so sealing it into the
> registry would invent a second identity for something that already has one. Selection is validated
> against the discovered set and fails **closed**, the same pattern as `env` against `DeclaredEnv` and
> `expr` against `in_scope`.

## ADDED Requirements

### Requirement: The IR SHALL carry tools and skills as separate, additive fields

The intermediate representation SHALL carry tools and skills as distinct fields, added additively
(`omitempty`, absent when empty), so an IR that predates them serializes byte-identically and the existing
conflated slice is retained unchanged and never repurposed.

#### Scenario: A pre-P14 IR serializes byte-identically

- **WHEN** an IR that declares no split tools or skills is serialized
- **THEN** it emits no tools field and no skills field
- **AND** its bytes are identical to how it serialized before these fields existed.

#### Scenario: The conflated slice is retained

- **WHEN** the split fields are added
- **THEN** the existing conflated tools-and-skills slice remains present and unchanged
- **AND** it is not repurposed to mean only tools or only skills.

### Requirement: The discovery frontend SHALL classify and populate the split at extraction

Discovery SHALL classify each discovered entry as a tool (a provider-native function/tool the model may
call) or a skill (a registered platform capability) and populate the split fields at extraction. The
classification SHALL be recorded, not left for a consumer to infer.

#### Scenario: Tools and skills are separated at extraction

- **WHEN** a node declares both a provider-native tool and a registered platform capability
- **THEN** the tool populates the tools field and the capability populates the skills field
- **AND** neither is derived after the fact by a downstream consumer.

### Requirement: A tool selection SHALL be validated against the node's discovered tool set

A tool selection SHALL name tools by their discovered call-site identifiers and SHALL be validated against
the tools the IR records for that node. A selection naming a tool the IR does not record for the node
SHALL be rejected (fail closed).

#### Scenario: A selection over an undiscovered tool is rejected

- **WHEN** a tool selection names a tool the node's discovered tool set does not contain
- **THEN** the selection is rejected at resolve
- **AND** it is not applied to nothing.

#### Scenario: A selection over discovered tools is accepted

- **WHEN** a tool selection names only tools the node's discovered set contains
- **THEN** the selection resolves
- **AND** it constrains which of those tools the node offers.

### Requirement: An unused tool SHALL be prunable as a call-site deletion

A tool a node offers but the evaluation set never exercises SHALL be prunable, expressed as a call-site
deletion of an already-present tool rather than a construction, reducing the tokens spent declaring it and
the surface for a tool error.

#### Scenario: A pruned tool is deleted at the call site

- **WHEN** a node offers a tool the eval set never calls and a prune is applied in a supported call form
- **THEN** the transform emits an edit that deletes that tool's declaration
- **AND** the edit is a deletion of an existing element, not a construction of a new value.

### Requirement: Tool-set minimization SHALL be expressible as a candidate

The minimal tool set that preserves task success SHALL be expressible as a candidate the optimizer can
propose and the harness can score against the full set.

#### Scenario: A minimal tool set is proposed for scoring

- **WHEN** a node offers more tools than the eval set exercises
- **THEN** a candidate carrying the minimal tool set that preserves task success is emitted
- **AND** it is scored against the full-tool-set configuration.

### Requirement: A tool selection SHALL participate in config_hash additively

A tool selection SHALL join the resolved configuration additively (absent when empty), so pruning a tool
changes `config_hash` while a node that prunes nothing hashes byte-identically to how it did before this
field existed.

#### Scenario: Pruning a tool changes the hash

- **WHEN** a node prunes at least one tool
- **THEN** its `config_hash` differs from the unpruned configuration.

#### Scenario: A no-prune node is byte-identical to pre-P14

- **WHEN** a node prunes no tool
- **THEN** it emits no tool-selection field
- **AND** its canonical resolved bytes are identical to how they serialized before this field existed.

#### Scenario: Reordering a selection is not identity-bearing

- **WHEN** two specs carry the same kept-tool set in different authoring order
- **THEN** they canonicalize identically
- **AND** their `config_hash` is the same.

### Requirement: Tool pruning SHALL be scored by the existing harness with no new metric

Tool pruning and minimization SHALL be scored by the existing axis-agnostic evaluation harness, which
consumes only `config_hash` and the trace. Their benefit SHALL surface as fewer total tokens and a lower
tool-error rate. No new evaluation metric SHALL be introduced.

#### Scenario: A pruned set is scored without an eval change

- **WHEN** a pruned tool set is evaluated
- **THEN** it is scored by the unchanged harness from its `config_hash` and trace
- **AND** no dimension-specific metric or code path is added to score it.

#### Scenario: The saving surfaces in existing metrics

- **WHEN** a prune reduces the tools a node declares
- **THEN** the effect appears as fewer total tokens and, where a pruned tool was erroring, a lower
  tool-error rate
- **AND** the saving is not reported through a new bespoke metric.

### Requirement: A tool selection over a dynamically-assembled set SHALL be refused, not guessed

A tool selection over a tool set the frontend cannot locate as a static, deletable declaration SHALL be
refused at transform with `ErrUnsafeRewrite`. A deletion SHALL NOT be guessed against a dynamically-built
tool list.

#### Scenario: A dynamic tool set refuses loudly

- **WHEN** a prune targets a node whose tool set is assembled dynamically rather than declared statically
- **THEN** the transform returns `ErrUnsafeRewrite` naming the node and the tools dimension
- **AND** no deletion is emitted.
