# Skill & Tool Delivery — Spec (folded from P14)

Product rationale: [`../../../docs/prd/P14-skills-tools-optimization.md`](../../../docs/prd/P14-skills-tools-optimization.md)
§6 (FR53–FR58). Architecture decision:
[`../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../changes/archive/2026-07-29-p14-skills-tools-optimization/design.md`](../../changes/archive/2026-07-29-p14-skills-tools-optimization/design.md) Decision 10.

The cross-axis rules are defined once in
[`change-delivery`](../change-delivery/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the skills and tools axis.

> **This axis refuses the runtime route for two different reasons, and collapsing them would repeat the
> exact mistake [`skill-tool-language-coverage`](../skill-tool-language-coverage/spec.md) was written
> to prevent.**
>
> **Binding a skill is construction.** What must exist at the call site is a *provider SDK's tool
> value* — a typed object in the target language, spelled the way that SDK spells it. A binding
> document holds data; it does not hold a constructed SDK value, and no version of the document schema
> could hold one without becoming a code generator that runs at request time. That is a **permanent**
> boundary: `notRuntimeResolvable`, with no completion date attached to it.
>
> **Selecting among tools already written at the call site is data.** Which of the tools the program
> already constructs are offered on a given call is a set, and a set is exactly the kind of fact a
> document can carry. That cell is refused today only because the schema has no field for it —
> `noRolloutBinding`, with a named missing artifact and an owner on our side.
>
> Those two sit one line apart in the same table and point at opposite conclusions. The first says
> *stop asking*; the second says *ask again after the schema lands*. A single "tools are not
> rollout-eligible" row would tell every reader the wrong one of those.

## Requirements

### Requirement: Skill binding SHALL be refused for the runtime route as not runtime-resolvable

A change that binds a skill at a call site SHALL be refused for the runtime route with cause
`notRuntimeResolvable`, in every language and for every provider. The refusal SHALL name construction
of a provider SDK tool value as the reason, and SHALL NOT be presented as pending work.

#### Scenario: Skill binding is refused in every cell

- **WHEN** the runtime-route eligibility of skill binding is read for any (language, provider) cell
- **THEN** the cause is `notRuntimeResolvable`
- **AND** no cell reports it as eligible or as awaiting an artifact.

### Requirement: The skill binding refusal SHALL NOT be presented as unbuilt work

The skill binding refusal SHALL carry no named missing artifact, milestone, or "not yet" framing in any
surface that renders it, and SHALL be structurally distinguishable from a cell whose cause names a
missing artifact.

#### Scenario: The refusal is not softened into a backlog item

- **WHEN** the skill binding cell is presented in the console or on the command line
- **THEN** it is described as a boundary
- **AND** no schema field, milestone, or "not yet" framing is attached to it.

#### Scenario: A boundary row is distinguishable from a backlog row

- **WHEN** the skill binding cell and the tool-set cell are rendered together
- **THEN** the first carries no artifact and the second names one
- **AND** the two are structurally distinct rather than sharing one rendering.

### Requirement: The offered tool set SHALL be refused for the runtime route with a named missing artifact

A change confined to which already-constructed tools are offered on a call SHALL be refused for the
runtime route with cause `noRolloutBinding`, naming the absent document field. It SHALL NOT be refused
as `notRuntimeResolvable` and SHALL NOT be refused as `nodeNotBound`.

#### Scenario: Tool-set selection names the missing field

- **WHEN** an accepted change alters only which written tools a call offers
- **THEN** the runtime route is refused with cause `noRolloutBinding`
- **AND** the reported cause names the binding document field that does not exist yet
- **AND** the owner is the platform, not the customer.

#### Scenario: The two tool cells are not merged

- **WHEN** the delivery table for this axis is read
- **THEN** skill binding and tool-set selection appear as separate cells with different causes
- **AND** neither cell's cause is inferred from the other's.

### Requirement: A tool prune SHALL report the frontend gap rather than the delivery route

Where a tool prune cannot proceed because the discovery frontend records no tool split for a call site,
the reported condition SHALL name the frontend gap. It SHALL NOT be reported as a runtime-route
eligibility cause, and it SHALL NOT be reported as a missing rewriter.

#### Scenario: A frontend gap is not disguised as a delivery refusal

- **WHEN** a prune is requested for a call site whose language records no tool split
- **THEN** the condition reported is the frontend gap
- **AND** it is distinguishable from `noRolloutBinding` and from a missing materializer.

### Requirement: A call site with no written tool list SHALL be told what it lacks, on either route

Where a call site passes its arguments as an unpacked mapping and therefore has no tool argument to
replace and no written tool list to select from, both routes SHALL report that call-site shape as the
cause. Neither route SHALL report the language or the document schema as the reason.

#### Scenario: An unpacked call site gets the same answer from both routes

- **WHEN** delivery is attempted for a call site that passes arguments as an unpacked mapping
- **THEN** the source route and the runtime route both name the call site's own shape
- **AND** neither tells the author to wait for a materializer or for a schema field.

### Requirement: A skills or tools change SHALL report both routes' outcomes together

Where both routes refuse a skills or tools change, the platform SHALL surface both causes against the
change rather than the first one encountered.

#### Scenario: Both refusals are visible

- **WHEN** an accepted skill binding change is refused by the source route in a language with no
  materializer and by the runtime route as not runtime-resolvable
- **THEN** both causes are recorded and surfaced against the change
- **AND** the change is reported as undeliverable rather than pending.
