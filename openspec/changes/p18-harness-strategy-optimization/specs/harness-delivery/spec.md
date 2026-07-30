# Harness Delivery — Spec Delta (P18)

Product rationale: [`../../../../../docs/prd/P18-harness-strategy-optimization.md`](../../../../../docs/prd/P18-harness-strategy-optimization.md)
§6 (FR46–FR51). Architecture decision:
[`../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../design.md`](../../design.md) Decision 13.

The cross-axis rules are defined once in
[`change-delivery`](../../../p13-prompt-model-optimization/specs/change-delivery/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the harness axis.

> **This axis carries the one distinction the delivery table is most likely to get wrong: a scaffold is
> structure, but its bounds are numbers.**
>
> Swapping a node from a single call to a reason-and-act loop changes **how many calls the program
> makes and in what control flow**. That is a loop — program structure — and no binding document can
> introduce one: `notRuntimeResolvable`, permanent, on the same footing as wiring. But a strategy's
> `max_turns`, its retry budget, and its stop condition are **parameters of a loop that is already
> written**, and those are data in exactly the sense the document was designed for. They are refused
> today only because the schema has no field: `noRolloutBinding`, ours to fix.
>
> The second distinction is between two refusals that both mention the runtime and mean opposite
> things. **`hostAbsent`** — already specified by [`harness-runtime`](../harness-runtime/spec.md) — is
> about *executing* a strategy whose host service is not running: the strategy is deliverable, it is
> simply not runnable here and now, and it refuses rather than substituting. **`notRuntimeResolvable`**
> is about *delivering* a change as data at all, and it is true whether or not any host is running.
> One is answered by starting a service; the other cannot be answered. A table that renders them the
> same way sends an operator to restart something that was never the problem.

## ADDED Requirements

### Requirement: A harness strategy change SHALL be refused for the runtime route as not runtime-resolvable

A change that swaps a node's harness strategy SHALL be refused for the runtime route with cause
`notRuntimeResolvable`, in every language and for every strategy. The refusal SHALL name the
introduction of a control loop as the reason.

#### Scenario: A strategy swap is refused permanently

- **WHEN** the runtime-route eligibility of a harness strategy swap is read for any cell
- **THEN** the cause is `notRuntimeResolvable`
- **AND** the reported reason names the control loop
- **AND** no missing artifact or completion date is attached to it.

#### Scenario: The apply mode does not change the answer

- **WHEN** a harness strategy swap targets a node in `bound` mode
- **THEN** the runtime route is still refused with cause `notRuntimeResolvable`
- **AND** a `bound` migration is not suggested.

### Requirement: Harness strategy parameters SHALL be refused for the runtime route with a named missing artifact

A change confined to a strategy's bounded parameters — its turn ceiling, retry budget, or stop
condition — SHALL be refused for the runtime route with cause `noRolloutBinding`, naming the absent
binding document field. It SHALL NOT be refused as `notRuntimeResolvable`.

#### Scenario: A turn-ceiling change names the missing field

- **WHEN** an accepted change alters only a strategy's turn ceiling
- **THEN** the runtime route is refused with cause `noRolloutBinding`
- **AND** the reported cause names the document field that does not exist yet.

### Requirement: The strategy cell and the parameter cell SHALL appear separately

The delivery table SHALL carry the harness strategy swap and its bounded parameters as distinct cells
whose causes are not inferred from one another, and the parameter cell SHALL be distinguishable from a
permanent boundary.

#### Scenario: The two harness cells are not merged

- **WHEN** the delivery table for this axis is read
- **THEN** the strategy swap and its bounded parameters appear as separate cells with different causes
- **AND** neither cell's cause is inferred from the other's.

#### Scenario: One row for "harness" is not an admissible rendering

- **WHEN** a surface renders this axis's runtime-route eligibility
- **THEN** it renders two rows
- **AND** a single collapsed "harness is not rollout-eligible" row is not produced.

### Requirement: A parameter that changes a bound SHALL NOT be able to remove one

Where a bounded parameter becomes rollout-eligible, the schema SHALL admit only values within the
strategy's declared parameter schema, and SHALL NOT admit an absent, unbounded, or non-positive turn
ceiling.

#### Scenario: An unbounded ceiling is inexpressible in a rollout arm

- **WHEN** a rollout arm is authored with an absent or unbounded turn ceiling
- **THEN** authoring is refused by the strategy's parameter schema
- **AND** the refusal is the same one the registry returns at seal.

#### Scenario: A parameter inapplicable to a strategy stays inexpressible

- **WHEN** a rollout arm carries a parameter the candidate strategy does not declare
- **THEN** authoring is refused
- **AND** the parameter is not silently ignored.

### Requirement: An absent host service SHALL be reported distinctly from a delivery refusal

The `hostAbsent` execution refusal and the `notRuntimeResolvable` delivery cause SHALL be separate,
separately readable conditions. A surface SHALL NOT render one in place of the other, and an absent
host SHALL NOT be reported as making a change undeliverable.

#### Scenario: A missing host does not change delivery eligibility

- **WHEN** a harness strategy's host service is absent
- **THEN** the change's delivery eligibility is unchanged
- **AND** the absent host is reported as an execution condition with its own cause.

#### Scenario: An operator is not sent to restart the wrong thing

- **WHEN** a harness change is refused for the runtime route
- **THEN** the reported cause does not mention a host service
- **AND** starting a host service is not offered as a remedy.

### Requirement: The harness refusal totality SHALL hold across both routes

No path SHALL exist by which a harness strategy takes effect without a materializer. Authoring a
rollout whose candidate arm swaps a harness strategy SHALL be refused with the same typed cause the
transform returns.

#### Scenario: A harness rollout candidate is refused

- **WHEN** a rollout is authored with a candidate arm carrying a different harness strategy
- **THEN** authoring is refused with the transform's typed cause
- **AND** no document is written that carries a harness strategy.

#### Scenario: The totality canary covers the second route

- **WHEN** a node constructed to carry a real harness strategy is pushed through each delivery route
- **THEN** both come back refused
- **AND** a sabotaged refusal on either route turns the cell red.
