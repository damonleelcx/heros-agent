# Prompt & Model Delivery — Spec Delta (P13)

Product rationale: [`../../../../../docs/prd/P13-prompt-model-optimization.md`](../../../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR69–FR73). Architecture decision:
[`../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../design.md`](../../design.md) Decision 14.

The cross-axis rules — delivery as a total function, the two routes and their asymmetry, rollout
resolved in the customer's process, deterministic assignment, arm-level `config_hash` attribution,
bounded expiry, local revert with human resume, the three eligibility causes and their order,
inertness during measurement, one resolve-hash-gate spine, and entitlement/halt behaviour — are
defined once in [`change-delivery`](../change-delivery/spec.md) and are **not restated here**. This
capability adds only what is specific to the prompt and model axis.

> **This is the only axis where the runtime route is live today, and it is live for a reason that is
> not about this axis being important.** Model id, inference params and prompt template are precisely
> the fields [ADR-009](../../../../../docs/adr/ADR-009-binding-document-format.md) already fixed in the
> binding document, because [ADR-004](../../../../../docs/adr/ADR-004-runtime-config-binding.md)
> already decided they are *data* rather than program structure. A rollout on this axis adds a second
> value to a field that was designed to change without a codemod. Nothing else on the six axes has
> that property yet.
>
> The cell that must not be blurred is **provider**. Swapping a node's model *within* a provider is a
> value change; swapping the provider is a rewrite of the SDK call itself
> ([ADR-002](../../../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md), P2 FR12 as
> narrowed) — the codemod refuses it, and no document can carry it. A user reading "model is
> rollout-eligible" and concluding "so I can canary Anthropic against OpenAI" has been misled by a
> table that was too coarse. So the unit of eligibility here is **(field, provider-crossing)**, not
> "the model dimension."

## ADDED Requirements

### Requirement: Model id, inference params, and prompt version SHALL be eligible for the runtime route on a bound node

For a node in `bound` mode, a change confined to the model id within one provider, to inference
parameters, or to the prompt template version SHALL be eligible for a gradual rollout. Each such
change SHALL also remain deliverable by the source route.

#### Scenario: A prompt version change rolls out on a bound node

- **WHEN** an accepted change swaps a node's prompt template version and the node is in `bound` mode
- **THEN** the change is eligible for the runtime route
- **AND** it is also proposed as a pull request, which remains the way it becomes permanent.

#### Scenario: The same change on an inline node reports the migration

- **WHEN** the same change targets a node in `inline` mode
- **THEN** the runtime route is refused with cause `nodeNotBound`
- **AND** the source route is unaffected and still delivers it.

### Requirement: A provider-crossing model change SHALL be refused for the runtime route as not runtime-resolvable

A change that moves a node from one provider to another SHALL be refused for the runtime route with
cause `notRuntimeResolvable`, regardless of the node's apply mode. The refusal SHALL name the SDK call
rewrite as the reason and SHALL NOT direct the user to migrate the node to `bound` mode.

#### Scenario: A cross-provider swap is refused before the apply mode is consulted

- **WHEN** an accepted change moves a node from one provider to another
- **THEN** the runtime route is refused with cause `notRuntimeResolvable`
- **AND** the reported reason names the SDK call rewrite
- **AND** no `bound` migration is suggested.

#### Scenario: The two model cells are distinguishable in the table

- **WHEN** the delivery table for this axis is read
- **THEN** a within-provider model change and a provider-crossing model change appear as separate cells
- **AND** they carry different eligibility values.

### Requirement: A rollout arm SHALL carry a complete resolved configuration, not a partial override

Each arm of a rollout SHALL reference a fully resolved configuration whose values are present in the
binding document. An arm SHALL NOT be expressed as a delta against the other arm, and the document
SHALL NOT require the reader to compose two entries to know what an arm runs.

#### Scenario: Both arms are readable in the diff

- **WHEN** a reviewer reads the pull request that introduces a rollout
- **THEN** the effective resolved values of both arms are present in the change
- **AND** neither arm requires composing a delta to be understood.

### Requirement: A held-out downgrade guardrail verdict SHALL bound what may be rolled out

A model change that the held-out guardrail decided against SHALL NOT be eligible to be authored as a
rollout candidate. A change the guardrail could not decide SHALL be eligible only when the ambiguity
is recorded on the rollout.

#### Scenario: A guardrail-rejected downgrade cannot be rolled out

- **WHEN** a model downgrade was decided against by the held-out guardrail
- **THEN** authoring a rollout with it as the candidate arm is refused
- **AND** the refusal names the guardrail verdict.

#### Scenario: An undecided verdict is carried, not hidden

- **WHEN** the guardrail returned neither a cost-win nor a quality-tie
- **THEN** a rollout may be authored
- **AND** the undecided verdict is recorded on the rollout and surfaced with it.

### Requirement: An authored prompt or model change SHALL be rollout-eligible on the same terms and SHALL claim nothing more

A change originated by a user under [`authored-change`](../authored-change/spec.md) SHALL be eligible
for the runtime route under the same per-cell rules as an operator-proposed change. Its rollout
evidence SHALL remain `unverified` and SHALL NOT be counted as a result.

#### Scenario: An authored change may be rolled out

- **WHEN** a user authors a prompt change on a bound node and requests a rollout
- **THEN** the rollout is permitted under the same cell rules
- **AND** its origin is recorded and not hashed.

#### Scenario: Rollout does not launder an unverified change into a result

- **WHEN** an authored change accumulates rollout evidence
- **THEN** it remains stamped `unverified`
- **AND** it is not written to the verified-delta ledger and is not reported as a win.
