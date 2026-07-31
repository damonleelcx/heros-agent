# Change Delivery — Spec Delta (P13)

Product rationale: [`../../../../../docs/prd/P13-prompt-model-optimization.md`](../../../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR57–FR68), §7 (NFR24–NFR27). Architecture decision:
[`../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../design.md`](../../design.md) Decision 14.

This capability is the **third cross-axis contract: how an accepted change reaches a running agent**.
It is defined once here and referenced — never restated — by the per-axis delivery capabilities:
[`prompt-model-delivery`](../prompt-model-delivery/spec.md) (P13),
[`skill-tool-delivery`](../../../p14-skills-tools-optimization/specs/skill-tool-delivery/spec.md) (P14),
[`wiring-delivery`](../../../p15-workflow-wiring-optimization/specs/wiring-delivery/spec.md) (P15),
[`context-delivery`](../../../p16-context-strategy-optimization/specs/context-delivery/spec.md) (P16),
[`memory-delivery`](../../../p17-memory-strategy-optimization/specs/memory-delivery/spec.md) (P17), and
[`harness-delivery`](../../../p18-harness-strategy-optimization/specs/harness-delivery/spec.md) (P18).

It consumes, and does not modify, [`forge-delivery`](../../../../specs/forge-delivery/spec.md) and
[`delivery-record`](../../../../specs/delivery-record/spec.md) (P12).

> **The one sentence this capability exists to enforce: an accepted change either reaches the running
> agent by a named route, or the platform says which route was expected and why it did not — a change
> that no route can deliver is a reported state, never a silent nothing.**
>
> Until now delivery was one chain with one shape: a rewriter produces a diff, P12 opens a pull
> request, a human merges. That chain is correct and stays the default. What it never said is what
> happens when the chain's **first link refuses** — which, read honestly against the coverage tables,
> is the common case rather than the exception. Memory refuses in every language; harness refuses in
> every language; skill binding materializes in Go for two providers. When the rewriter refuses there
> is no diff, so there is no pull request, so nothing ships — and the only thing the product currently
> says about that is nothing at all. It can prove a change is better and then deliver silence.
>
> So this capability fixes the shape of delivery before it grows a second route. Delivery is a **total
> function** over (axis × change × route): every cell has a value, and "absent" is not one of them.
> Then it adds the second route under the bounds
> [ADR-010](../../../../../docs/adr/ADR-010-runtime-gradual-rollout.md) fixed — a **gradual rollout**
> that is a two-armed binding document resolved **inside the customer's own process**, deterministic
> and offline, expiring on its own, reverting on its own, and **never** the way a change becomes
> permanent.
>
> The corollary is the part that must not soften under commercial pressure: **a rollout is evidence,
> not delivery.** It never merges, it never writes a verified delta, it never counts as a win, and it
> never ends with the customer's repository describing something other than what their agent does.
> Permanence costs a codemod, a pull request, and a human — the same as it always did.

## ADDED Requirements

### Requirement: Delivery SHALL be stated as a total function over every accepted change

For every optimization axis, the platform SHALL publish a delivery table with an entry for **every**
route and every cell that axis defines. A route that cannot deliver a given cell SHALL be present in
the table with a typed refusal cause, and SHALL NOT be absent, blank, or represented as unknown.

#### Scenario: Every axis cell has a delivery value

- **WHEN** the delivery table is enumerated for any axis
- **THEN** every cell names either a route that delivers it or a typed refusal cause
- **AND** no cell is missing, empty, or reported as unknown.

#### Scenario: A change no route can deliver is reported, not dropped

- **WHEN** a change is accepted and no route can deliver it
- **THEN** the platform records and surfaces that condition against the change, naming the route that
  was expected and the cause that refused it
- **AND** the change is not silently discarded, and is not presented as delivered or pending.

### Requirement: The platform SHALL offer exactly two delivery routes, and they SHALL NOT be equivalent

Delivery SHALL consist of the **source route** — a call-site materialization carried by a pull request
under [`forge-delivery`](../../../../specs/forge-delivery/spec.md) — and the **runtime route** — a
gradual rollout under a binding document. The routes SHALL NOT be presented as interchangeable
alternatives, and the source route SHALL remain the default.

#### Scenario: The source route is the default

- **WHEN** an accepted change is deliverable by both routes
- **THEN** the source route is what the platform proposes
- **AND** the runtime route is offered as an additional, explicitly chosen step.

#### Scenario: The routes are described by what they cost, not as a tier

- **WHEN** the two routes are presented to a user
- **THEN** the runtime route is described as temporary and evidence-producing, and the source route as
  permanent
- **AND** neither is described as a reduced or enhanced version of the other.

### Requirement: The runtime route SHALL be a precursor to the source route and SHALL NOT make a change permanent

A gradual rollout SHALL NOT be a terminal delivery state. Making a rolled-out change permanent SHALL
require a call-site materialization, a pull request, and a human merge. The platform SHALL NOT provide
a path by which a change becomes the durable configuration without passing through the source route.

#### Scenario: A successful rollout does not end the change's journey

- **WHEN** a rollout completes without tripping a guard
- **THEN** the change's delivery state is not `delivered`
- **AND** the platform surfaces the remaining step as a pull request that a human must merge.

#### Scenario: No path makes a rollout durable

- **WHEN** the delivery paths are enumerated
- **THEN** there is no path that converts a rollout into a permanent configuration without a merged
  pull request
- **AND** a rollout on an axis whose cell has no materializer is reported as unable to become
  permanent, naming the missing materializer.

### Requirement: A rollout SHALL be resolved inside the customer's process, and the platform SHALL hold no place in the request path

Arm resolution SHALL be performed by the generated binding accessor running in the customer's own
program. The platform SHALL NOT receive, proxy, or observe the customer's production invocations as
part of a rollout, and the accessor SHALL NOT open a connection to the platform in order to resolve an
arm.

#### Scenario: No platform component is on the request path

- **WHEN** a rollout is active and the customer's program makes a call
- **THEN** the arm is chosen by the accessor compiled into that program
- **AND** no request reaches the platform as a consequence of resolving the arm.

#### Scenario: A rollout survives platform unavailability

- **WHEN** the platform is unreachable while a rollout is active
- **THEN** arm assignment, guard evaluation, and expiry continue to work unchanged
- **AND** the customer's program does not fail, stall, or change behaviour because of it.

### Requirement: Arm assignment SHALL be deterministic and SHALL NOT use randomness or wall-clock

The candidate arm SHALL be selected as a pure function of the rollout's identity and a caller-supplied
stable assignment key. The implementation SHALL NOT use a random source, a wall-clock reading, a
process identifier, or replica-local state. Where the caller supplies no key, assignment SHALL be
per-invocation and the document SHALL record that it is.

#### Scenario: The same key resolves to the same arm everywhere

- **WHEN** the same assignment key is resolved against the same rollout on two different replicas
- **THEN** both resolve to the same arm
- **AND** no coordination between the replicas is required to achieve it.

#### Scenario: Assignment is reproducible after the fact

- **WHEN** a past invocation's rollout identity and assignment key are replayed
- **THEN** the arm it received is reproduced exactly
- **AND** the reproduction requires no stored assignment table.

#### Scenario: A missing key is reported, not silently substituted

- **WHEN** a rollout resolves for a caller that supplied no assignment key
- **THEN** assignment is per-invocation
- **AND** the weaker guarantee is recorded, rather than a substitute key being synthesized.

### Requirement: Every invocation SHALL be attributed to its arm's own config_hash

The resolver SHALL emit, on every invocation, the `config_hash` of the **arm it resolved** — not the
identity of the rollout, and not the parent's hash for a candidate invocation. Two runs recorded under
the same `config_hash` SHALL remain comparable in the presence of a rollout.

#### Scenario: A candidate invocation is recorded under the candidate's hash

- **WHEN** an invocation is assigned to the candidate arm
- **THEN** the emitted `config_hash` is the candidate configuration's own hash
- **AND** the rollout's identity and the arm are emitted alongside it as separate fields.

#### Scenario: Emitting the document's identity in place of an arm's hash is a defect

- **WHEN** a resolver emits a rollout identity where an arm `config_hash` is required
- **THEN** the run is failed rather than scored
- **AND** the failure is of the same class as resolving a configuration that was not requested.

### Requirement: A rollout SHALL carry a bounded lifetime and SHALL serve the parent arm on expiry

Every rollout SHALL declare an expiry fixed when it is written. After expiry the accessor SHALL serve
the parent arm. Expiry SHALL be evaluated without a network call and without human presence, and a
rollout SHALL NOT be extendable except by a new document change.

#### Scenario: An expired rollout serves the parent

- **WHEN** a rollout's expiry has passed
- **THEN** every invocation resolves to the parent arm
- **AND** no platform interaction is required for that to take effect.

#### Scenario: A forgotten rollout cannot become the durable configuration

- **WHEN** a rollout is left unattended past its expiry
- **THEN** the running configuration is the parent
- **AND** the customer's repository continues to describe what their agent does.

### Requirement: A rollout SHALL revert locally on a declared guard, and resuming SHALL require a human change

A rollout SHALL be able to declare guard conditions on the candidate arm. When a guard trips, the
accessor SHALL fall back to the parent arm in-process and SHALL record the cause. The platform SHALL
NOT be consulted to perform the revert, and the rollout SHALL NOT resume automatically.

#### Scenario: A tripped guard reverts without the platform

- **WHEN** the candidate arm trips a declared guard
- **THEN** subsequent invocations resolve to the parent arm
- **AND** the trip and its cause are recorded locally
- **AND** no call is made to the platform to effect the revert.

#### Scenario: Resuming requires an authored change

- **WHEN** a rollout has reverted on a guard
- **THEN** it does not resume on its own, on a timer, or on the guard condition clearing
- **AND** resuming requires an edited document delivered as a pull request and merged by a human.

### Requirement: Runtime-route eligibility SHALL be published per cell with typed causes in a fixed order

For each axis cell, eligibility for the runtime route SHALL be one of: eligible, or refused with cause
`notRuntimeResolvable`, `nodeNotBound`, or `noRolloutBinding`. Causes SHALL be evaluated in that order,
and the first applicable cause SHALL be the one reported.

#### Scenario: A permanent boundary is reported before a migration

- **WHEN** a cell is both `notRuntimeResolvable` and on a node in `inline` mode
- **THEN** the reported cause is `notRuntimeResolvable`
- **AND** the user is not directed to migrate the node to `bound` mode.

#### Scenario: A missing schema field is not reported as a customer problem

- **WHEN** a cell is runtime-resolvable and its node is bound, but the document schema carries no field
  for that axis
- **THEN** the reported cause is `noRolloutBinding`, naming the missing artifact
- **AND** it is not reported as `nodeNotBound`.

#### Scenario: A permanent boundary is never reported as pending

- **WHEN** a cell's cause is `notRuntimeResolvable`
- **THEN** it is presented as a boundary rather than as unbuilt work
- **AND** no completion date, backlog item, or "not yet" framing is attached to it.

### Requirement: A rollout SHALL be inert during verification and evaluation runs

During an evaluation or verification run the resolver SHALL be pinned, and a rollout SHALL NOT
influence which configuration executes. A verified delta SHALL NOT be produced from a partially
exposed configuration.

#### Scenario: A measurement run ignores an active rollout

- **WHEN** a verification run executes against a node carrying an active rollout
- **THEN** the configuration under measurement is the one the run requested
- **AND** the rollout does not alter it.

#### Scenario: A rollout produces no verified delta

- **WHEN** a rollout accumulates production evidence
- **THEN** that evidence is not written to the verified-delta ledger
- **AND** it is not counted as a win, a regression, or a tie.

### Requirement: The runtime route SHALL NOT introduce a second resolve, hash, or gate path

A change delivered by the runtime route SHALL be derived, resolved, hashed, and gated by the same
components that process a change delivered by the source route. The platform SHALL NOT provide a
rollout-only resolve path, rollout-only hash derivation, or rollout-only gate.

#### Scenario: A rollout arm and a materialized change hash identically

- **WHEN** the same configuration is prepared for a rollout arm and for a call-site materialization
- **THEN** both resolve to the same `config_hash`
- **AND** both are subject to the same gates.

#### Scenario: No second apply path exists

- **WHEN** the apply paths are enumerated
- **THEN** the runtime route reuses the single resolve-hash-gate spine
- **AND** no safety gate exists in one route and not the other.

### Requirement: Rollout SHALL be entitlement-gated server-side and SHALL respect an active halt

Authoring or activating a rollout SHALL be gated by entitlement on the server, on the same footing as
delivery. An active halt SHALL prevent a new rollout from being authored, and an unreadable halt state
SHALL fail closed.

#### Scenario: Entitlement is enforced on the server

- **WHEN** a rollout is authored by a caller without the entitlement
- **THEN** the request is refused server-side
- **AND** the refusal does not depend on a client-side check.

#### Scenario: A halt stops new rollouts and an unreadable halt fails closed

- **WHEN** a halt is active, or the halt state cannot be read
- **THEN** no new rollout is authored
- **AND** the condition is reported rather than treated as permission.

#### Scenario: A halt does not strand an active rollout

- **WHEN** a halt becomes active while a rollout is running in a customer's process
- **THEN** the running rollout continues to expire and to guard itself locally
- **AND** the platform does not reach into the customer's process to stop it.
