# Prompt & Model Authoring — Spec (folded from P13)

Product rationale: [`../../../docs/prd/P13-prompt-model-optimization.md`](../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR34–FR40), §7 (NFR11–NFR18). Design reasoning: [`../../changes/p13-prompt-model-optimization/design.md`](../../changes/p13-prompt-model-optimization/design.md) Decisions 9–12.
Shared contract: [`../authored-change/spec.md`](../authored-change/spec.md) — every requirement there
applies here and is **not** restated.

Covers the user-initiated half of the prompt+model axis: setting a node's model, its provider
parameters, and its prompt version directly, rather than waiting for an operator to propose one.

> **Why this axis authors first, and why it is still the strictest.** Prompt and model are the only
> dimensions the codemod already materializes, so this is the one axis where "let the user change it"
> produces a real diff today rather than a modeled intention. That is exactly why the refusals must be
> reproduced verbatim on the authoring path: the two ways this axis can be silently wrong — a
> cross-provider swap that compiles against the wrong SDK, and a parameter override an inline node
> cannot carry — are *more* likely from a human than from an operator, because a human reasonably
> expects a dropdown of models to contain every model. The authoring surface therefore does not offer
> what the transform would refuse, and when a user reaches a refusal anyway it is named at preflight,
> with the node and the reason, before anything is published.
>
> The guardrail is the other half. A user may author a cheaper model and apply it — it is their
> workflow and their cost. What they may not do is *call it safe*. An authored downgrade is `unverified`
> until the harness runs, and the held-out CI-overlap guardrail is what decides whether it is reported
> as an equal-quality-cheaper tie or as a quality regression. Authoring changes who picks the candidate;
> it changes nothing about who judges it.

## Requirements

### Requirement: A user SHALL be able to author a node's model reference, and an intra-provider swap SHALL apply

An authoring surface SHALL let a user set a node's model reference directly. An authored intra-provider
model swap SHALL be materialized through the existing model rewriter and SHALL yield a new `config_hash`
through the existing `ResolvedNode.ModelRef` field.

#### Scenario: An authored intra-provider swap produces a diff

- **WHEN** a user authors a model change to a different model of the same provider
- **THEN** preflight returns `admissible`
- **AND** applying it produces a reviewable diff containing the model edit
- **AND** the resolved configuration hash differs from the parent's.

#### Scenario: The authored model change introduces no new hashed field

- **WHEN** an authored model change is resolved
- **THEN** its only hashed effect is the node's model reference
- **AND** pre-existing golden hash vectors reproduce unchanged.

### Requirement: An authored cross-provider model swap SHALL be refused at preflight and SHALL NOT be offered

An authoring surface SHALL NOT present a model belonging to a different provider than the node's call
site as an applicable choice. If such a change is submitted through any surface, it SHALL be refused with
the cross-provider cause before a diff is produced.

#### Scenario: Cross-provider models are not offered at the call site

- **WHEN** a user opens the model choices for a node whose call site uses one provider's SDK
- **THEN** models of other providers are not presented as applicable choices
- **AND** the boundary is stated rather than the models being silently absent.

#### Scenario: A submitted cross-provider swap is refused by name

- **WHEN** a cross-provider model change for a call-site node is submitted through the API or command line
- **THEN** it is refused with the named cross-provider cause
- **AND** no diff is produced
- **AND** no entitlement or flag permits it.

### Requirement: A user SHALL be able to author provider parameters, refused by name where the apply mode cannot carry them

An authoring surface SHALL let a user set a node's provider parameters. An authored parameter change on a
node in bound apply mode SHALL be materialized; an authored parameter change on an inline node with no
applicable parameter rewriter SHALL be refused at preflight with the node and the reason named, and SHALL
NOT be dropped.

#### Scenario: A parameter change materializes in bound mode

- **WHEN** a user authors a temperature or max-tokens change on a bound-mode node
- **THEN** preflight returns `admissible`
- **AND** the change is materialized and participates in the configuration hash through the node's provider parameters.

#### Scenario: A parameter change on an un-carryable inline node is refused, not dropped

- **WHEN** a user authors a parameter change on an inline node with no applicable parameter rewriter
- **THEN** preflight returns `refused`, naming the node and the apply-mode reason
- **AND** no diff is produced
- **AND** the parameter is not silently omitted from an otherwise-applied change.

### Requirement: A user SHALL be able to author a node's prompt by selecting or publishing a version, and publishing SHALL be immutable

An authoring surface SHALL let a user bind a node to an existing published prompt version or publish a
new one. A published authored prompt SHALL be a new content-addressed version; the prior version SHALL
remain resolvable; no authoring surface SHALL express in-place mutation of a version.

#### Scenario: Authoring a prompt publishes a new version

- **WHEN** a user edits a prompt body and publishes it
- **THEN** a new content-addressed version identifier is created
- **AND** the parent version remains resolvable
- **AND** the node's prompt reference is the new version.

#### Scenario: No interface expresses mutation

- **WHEN** the authoring surface's prompt operations are enumerated
- **THEN** none of them modifies an existing version in place.

### Requirement: An authored prompt change that would un-apply a node SHALL be refused at preflight with the slot named

An authored prompt whose slot set changes such that a call site's supplied value no longer binds SHALL be
refused before the change is applied, naming the slot that stopped binding and the node it belongs to.

#### Scenario: A removed slot that the call site still supplies is named

- **WHEN** a user authors a prompt that removes a slot the call site still supplies
- **THEN** preflight returns `refused` naming the slot and the node
- **AND** no diff is produced.

#### Scenario: An added slot with no supplied value is named

- **WHEN** a user authors a prompt that introduces a slot no call-site value binds to
- **THEN** preflight returns `refused` naming the slot and the node
- **AND** no diff is produced.

### Requirement: An authored model downgrade SHALL be applicable but SHALL NOT be reported as equal-quality without clearing the held-out guardrail

A user MAY author and apply a change to a cheaper model. Such a change SHALL be `unverified` until the
harness runs. When verification is requested, the same held-out CI-overlap guardrail SHALL decide the
report: an authored downgrade whose task-success interval overlaps the incumbent's on held-out cases
SHALL be reported as a cost win and a quality tie; one whose interval does not overlap SHALL be reported
as a quality regression and SHALL NOT be described as equal-quality.

#### Scenario: An authored downgrade applies while unverified

- **WHEN** a user authors a change to a cheaper model and applies it without requesting verification
- **THEN** a diff is produced
- **AND** the change's verification state is `unverified`
- **AND** no equal-quality or cost-saving claim is attached to it.

#### Scenario: A verified authored downgrade is judged by the same guardrail

- **WHEN** a user requests verification of an authored downgrade
- **THEN** the held-out CI-overlap guardrail produces the verdict
- **AND** a non-overlapping result is reported as a quality regression despite the lower cost
- **AND** the user did not select the held-out cases.

### Requirement: The authoring surface SHALL gain model and parameter authoring without gaining an evaluator

Extending the authoring surface to the model and parameter dimensions SHALL NOT introduce a score, rank,
winner, confidence interval, or promotion path into it, and SHALL NOT remove or degrade any prompt
authoring capability it already provides.

#### Scenario: Model authoring adds no evaluation

- **WHEN** the model and parameter authoring controls are inspected
- **THEN** no score, rank, winner, or interval is computed or displayed by the surface itself.

#### Scenario: Existing prompt authoring capability is preserved

- **WHEN** the authoring surface is compared against its pre-change capability set
- **THEN** every prompt authoring, diffing, binding, and impact-analysis capability remains available.
