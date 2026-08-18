# Wiring Delivery — Spec Delta (P15)

Product rationale: [`../../../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../../../docs/prd/P15-workflow-wiring-optimization.md)
§6 (FR52–FR56). Architecture decision:
[`../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../design.md`](../../design.md) Decision 12.

The cross-axis rules are defined once in
[`change-delivery`](../../../p13-prompt-model-optimization/specs/change-delivery/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the workflow wiring axis.

> **This is the axis whose refusal must never soften, and the axis where the runtime route is most
> tempting as an escape hatch. Both facts have the same root: wiring is not data.**
>
> The order in which a program's statements execute, whether two calls run concurrently, and which
> node's output feeds which node's input are **compiled program structure**. There is no document that
> can reorder statements in a built binary, and there will not be one — a "document" that could would
> be an interpreter, and shipping an interpreter into the customer's process to reorder their own code
> is a larger change to their system than any optimization could justify. So every cell on this axis is
> `notRuntimeResolvable`, permanently, and a future revision of the coverage table that moves any of
> them into "pending" is claiming an ability that cannot exist.
>
> The second hazard is subtler and more dangerous. This axis is the one with a **coherence gate that
> rejects at compile** — an incoherent ordering yields no runnable spec, and therefore no codemod, no
> diff, and no pull request. A second delivery route arriving next to a gate whose whole purpose is to
> produce *nothing* is an obvious place for someone to reason "the rewriter refused, so let us just
> roll it out instead." That reading would convert the strongest safety gate in the system into a
> speed bump. The rule below states the opposite explicitly, because leaving it implied is how it gets
> lost.

## ADDED Requirements

### Requirement: Every wiring cell SHALL be refused for the runtime route as not runtime-resolvable

Node ordering, parallelization, merge, and every other wiring change SHALL be refused for the runtime
route with cause `notRuntimeResolvable`, in every language and for every call-site shape. The cause
SHALL NOT depend on the node's apply mode.

#### Scenario: Every wiring cell carries the same permanent cause

- **WHEN** the runtime-route eligibility of any wiring change is read
- **THEN** the cause is `notRuntimeResolvable`
- **AND** the answer is the same for a `bound` node and an `inline` node.

#### Scenario: A bound node does not unlock wiring

- **WHEN** a wiring change targets a node in `bound` mode
- **THEN** the runtime route is still refused with cause `notRuntimeResolvable`
- **AND** the presence of a binding document is not reported as making it closer to possible.

### Requirement: The wiring refusal SHALL NOT be presented as unbuilt work

The wiring axis's runtime-route refusal SHALL be presented as a boundary in every surface that renders
it. No milestone, backlog item, missing artifact, or "not yet" framing SHALL be attached to it, and it
SHALL be distinguishable in the coverage table from causes that name a missing artifact.

#### Scenario: No completion date is attachable

- **WHEN** the wiring row is rendered in the console, on the command line, or in an API response
- **THEN** it carries no named missing artifact and no expected date
- **AND** it is visually and structurally distinct from a `noRolloutBinding` row.

### Requirement: A change the coherence gate rejected SHALL NOT be deliverable by any route

A wiring change that the typed contract and reordering gate rejected SHALL produce no runnable spec,
and therefore SHALL NOT be authorable as a rollout candidate, SHALL NOT be deliverable as a pull
request, and SHALL NOT reach a customer's process by any path.

#### Scenario: A gate-rejected ordering cannot be rolled out

- **WHEN** an ordering is rejected by the coherence gate
- **THEN** authoring a rollout with it as the candidate arm is refused
- **AND** the refusal names the gate, not the delivery route.

#### Scenario: The runtime route is not an alternative to the gate

- **WHEN** the delivery paths available to a gate-rejected change are enumerated
- **THEN** there are none
- **AND** no path exists by which a rollout admits a configuration the gate rejected.

### Requirement: A rejected transform SHALL be reported as undeliverable rather than pending

Where the wiring transform returns a rejection, the change's delivery state SHALL be reported as
undeliverable with both routes' causes named. It SHALL NOT be reported as awaiting delivery, awaiting
review, or in progress.

#### Scenario: A rejected reorder reports both routes

- **WHEN** a reorder is rejected at transform
- **THEN** the change is reported as undeliverable
- **AND** the source-route cause is the rejection and the runtime-route cause is
  `notRuntimeResolvable`
- **AND** neither is reported as pending.

### Requirement: A materializable wiring change SHALL remain deliverable by the source route unchanged

Where a wiring change passes the gate and the axis has a materializer for its language and shape, the
source route SHALL deliver it exactly as before. The addition of a second route SHALL NOT alter the
source route's gates, diff, or evidence for this axis.

#### Scenario: An adjacent-statement swap still ships as a pull request

- **WHEN** a gate-passed adjacent-statement swap is materialized in a covered language
- **THEN** it is delivered as a pull request with its evidence, unchanged by this capability
- **AND** the runtime route's refusal does not appear as a warning on that delivery.
