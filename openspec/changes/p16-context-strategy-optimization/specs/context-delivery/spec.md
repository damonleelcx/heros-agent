# Context Delivery — Spec Delta (P16)

Product rationale: [`../../../../../docs/prd/P16-context-strategy-optimization.md`](../../../../../docs/prd/P16-context-strategy-optimization.md)
§6 (FR52–FR57). Architecture decision:
[`../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../design.md`](../../design.md) Decision 10.

The cross-axis rules are defined once in
[`change-delivery`](../../../p13-prompt-model-optimization/specs/change-delivery/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the context axis.

> **This axis splits down the middle, and the split is not where a reader expects it.**
>
> "Context strategy" sounds like one thing. For delivery it is two, and they land in different columns.
> **A retrieval parameter is a number** — a `top_k`, a token budget, a similarity floor. It is exactly
> the kind of fact a binding document was built to carry, and the only reason it is refused today is
> that the schema has no field for it: `noRolloutBinding`, ours to fix. **A selection policy is a
> deletion.** The materializer applies a policy by *removing the turns the policy does not retain* from
> the constructed message list — that is a source rewrite of a written data structure, and no document
> can perform it: `notRuntimeResolvable`, permanent.
>
> The requirement that matters most here is neither of those. It is that **the drop record survives the
> new route.** This axis's central honesty guarantee is that a context change which discards
> information records what it discarded — the recording is unskippable by construction, not by
> discipline. A rollout introduces a second way for a context decision to take effect, and a second
> path is exactly where an unskippable guarantee quietly becomes skippable. So the rule is stated as a
> property of the decision rather than of the route: whatever chooses what the model sees records what
> it dropped, no matter which arm chose it.

## ADDED Requirements

### Requirement: A retrieval parameter change SHALL be refused for the runtime route with a named missing artifact

A change confined to a retrieval parameter SHALL be refused for the runtime route with cause
`noRolloutBinding`, naming the absent binding document field. It SHALL NOT be refused as
`notRuntimeResolvable`.

#### Scenario: A top-k change names the missing field

- **WHEN** an accepted change alters only a retrieval parameter
- **THEN** the runtime route is refused with cause `noRolloutBinding`
- **AND** the reported cause names the document field that does not exist yet
- **AND** the owner is the platform.

#### Scenario: A retrieval parameter is not reported as structurally impossible

- **WHEN** the retrieval-parameter cell is read
- **THEN** it is distinguishable from a permanent boundary
- **AND** it is presented as a cell that can gain a row.

### Requirement: A selection policy change SHALL be refused for the runtime route as not runtime-resolvable

A change to which turns a node retains SHALL be refused for the runtime route with cause
`notRuntimeResolvable`, in every language. The refusal SHALL name the deletion of written turns as the
reason and SHALL NOT be presented as pending work.

#### Scenario: A retention policy is refused permanently

- **WHEN** the runtime-route eligibility of a selection policy change is read for any language
- **THEN** the cause is `notRuntimeResolvable`
- **AND** no missing artifact or completion date is attached to it.

### Requirement: Retrieval parameters and selection policy SHALL appear as separate cells

The delivery table SHALL carry retrieval parameters and selection policy as distinct cells whose causes
are not inferred from one another, and the retrieval cell SHALL be distinguishable from a permanent
boundary.

#### Scenario: The two context cells are not merged

- **WHEN** the delivery table for this axis is read
- **THEN** retrieval parameters and selection policy appear as separate cells with different causes
- **AND** neither cell's cause is inferred from the other's.

#### Scenario: One row for "context" is not an admissible rendering

- **WHEN** a surface renders this axis's runtime-route eligibility
- **THEN** it renders two rows
- **AND** a single collapsed "context is not rollout-eligible" row is not produced.

### Requirement: A context decision SHALL record what it dropped regardless of which arm made it

Where a rollout becomes possible on this axis, the drop record SHALL be produced by the same
unskippable path for a candidate-arm decision as for a parent-arm decision, and SHALL be byte-
comparable between them. A route SHALL NOT exist by which a context decision takes effect without its
drop record.

#### Scenario: Both arms produce a drop record

- **WHEN** a rollout is active on a context change and invocations resolve to each arm in turn
- **THEN** each invocation produces a drop record through the same path
- **AND** the records are byte-comparable in shape.

#### Scenario: No arm can skip the recording

- **WHEN** the paths by which a context decision can take effect are enumerated
- **THEN** every path passes through the recording
- **AND** no arm, route, or apply mode bypasses it.

### Requirement: The drop-tolerance gate SHALL run before rollout authoring and SHALL NOT refuse on ignorance

A context change SHALL pass the drop-tolerance gate before it may be authored as a rollout candidate.
The gate SHALL NOT refuse a change merely because tolerance for a dropped item is unknown.

#### Scenario: A gate-rejected context change cannot be rolled out

- **WHEN** the drop-tolerance gate rejects a context change
- **THEN** authoring a rollout with it as the candidate arm is refused
- **AND** the refusal names the gate.

#### Scenario: Unknown tolerance does not block authoring

- **WHEN** tolerance for a dropped item is unknown
- **THEN** the gate does not refuse on that basis
- **AND** the unknown is recorded on the change and carried with the rollout.

### Requirement: A held-out retrieval verdict SHALL bound what may be rolled out

A retrieval change whose evaluation split overlaps its retrieval corpus SHALL NOT be authorable as a
rollout candidate, on the same terms as it is refused a verdict.

#### Scenario: An overlapping split blocks rollout authoring

- **WHEN** a retrieval change's held-out verdict was refused for an overlapping split
- **THEN** authoring a rollout with it as the candidate arm is refused
- **AND** the refusal names the overlap rather than the delivery route.
