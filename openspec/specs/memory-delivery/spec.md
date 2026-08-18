# Memory Delivery — Spec (folded from P17)

Product rationale: [`../../../docs/prd/P17-memory-strategy-optimization.md`](../../../docs/prd/P17-memory-strategy-optimization.md)
§6 (FR34–FR39). Architecture decision:
[`../../../docs/adr/ADR-010-runtime-gradual-rollout.md`](../../../docs/adr/ADR-010-runtime-gradual-rollout.md).
Design reasoning: [`../../changes/archive/2026-08-01-p17-memory-strategy-optimization/design.md`](../../changes/archive/2026-08-01-p17-memory-strategy-optimization/design.md) Decision 8.

The cross-axis rules are defined once in
[`change-delivery`](../change-delivery/spec.md) (P13) and are
**not restated here**. This capability adds only what is specific to the memory axis.

> **This is the axis where both routes refuse, and it is the reason the cross-axis contract needed to
> exist at all.**
>
> A memory strategy is modelled, resolved, hashed, proposed — and then **refused at transform** in both
> engines, in every language, because wiring a store read and write around a call is a structural
> rewrite that is not yet safe. Before this capability, that was the end of the sentence: no diff, so
> no pull request, so nothing. A verified memory proposal produced silence, and the silence looked
> exactly like a proposal nobody had gotten to yet.
>
> The runtime route does not rescue it, and the honest reason is worth stating precisely rather than
> hedging. A memory strategy needs a **store** — something that persists between invocations, inside
> the customer's process or beside it. The binding document carries values; it does not carry a
> running store, and we do not ship one into the customer's tree. So this axis is
> `notRuntimeResolvable` today for a reason that is *contingent* rather than permanent — unlike
> wiring, a memory runtime could exist — and the table says which of those two it is, because the
> difference decides whether anyone should ask again.
>
> What this capability buys is therefore not a route. It is that a memory proposal now says
> **"undeliverable, by both routes, for these two named reasons"** instead of saying nothing. That is
> the difference between a product that refuses and a product that appears broken.

## Requirements

### Requirement: A memory change SHALL be refused by both routes, with each cause named separately

An accepted memory change SHALL report the source route's typed transform refusal and the runtime
route's `notRuntimeResolvable` cause as two distinct, separately readable causes. The change SHALL be
reported as undeliverable rather than pending.

#### Scenario: Both causes are recorded against the change

- **WHEN** a memory change is accepted and delivery is attempted
- **THEN** the source-route cause is the typed transform refusal naming node and dimension
- **AND** the runtime-route cause is `notRuntimeResolvable` naming the absent store
- **AND** both are readable without inferring one from the other.

### Requirement: A memory change SHALL be reported as undeliverable rather than pending

A memory change SHALL carry a delivery state of undeliverable. It SHALL NOT be rendered as queued,
awaiting review, awaiting delivery, or in progress in any surface or record.

#### Scenario: An undeliverable memory change is never shown as awaiting delivery

- **WHEN** a memory proposal's delivery state is rendered
- **THEN** it reads as undeliverable with its causes
- **AND** it is not shown as queued, in review, or in progress.

#### Scenario: The state is the same in every surface

- **WHEN** the same memory proposal is read from the console, the command line, and the API
- **THEN** all three report undeliverable
- **AND** none of them reports a state the others do not.

### Requirement: The memory runtime-route refusal SHALL be recorded as contingent, not permanent

The memory axis's `notRuntimeResolvable` cause SHALL be distinguishable from a permanent boundary and
SHALL name the missing runtime component. It SHALL NOT be presented with a completion date it does not
have.

#### Scenario: The memory refusal is distinguishable from the wiring refusal

- **WHEN** the memory row and the wiring row are read from the same table
- **THEN** the memory row names a missing runtime component and the wiring row names a boundary
- **AND** the two are structurally distinguishable rather than sharing one rendering.

#### Scenario: Contingent does not mean scheduled

- **WHEN** the memory row is rendered
- **THEN** it carries no delivery date, milestone, or commitment
- **AND** naming the missing component is not presented as a promise to build it.

### Requirement: A change to the identity strategy SHALL be reported as having nothing to deliver

A change whose resulting memory strategy is `none` SHALL be reported as requiring no delivery, rather
than as delivered or as refused.

#### Scenario: Removing a memory strategy needs no route

- **WHEN** an accepted change resolves a node's memory strategy to `none`
- **THEN** the delivery state is that there is nothing to deliver
- **AND** it is not counted as a delivery, and not reported as a refusal.

### Requirement: The refusal SHALL hold for a rollout candidate exactly as it holds at transform

Authoring a rollout whose candidate arm carries a memory strategy other than `none` SHALL be refused,
and no path SHALL exist by which a memory strategy reaches a customer's process.

#### Scenario: A memory rollout candidate is refused

- **WHEN** a rollout is authored with a candidate arm carrying a memory strategy
- **THEN** authoring is refused with the same typed cause the transform returns
- **AND** no document is written that carries a memory strategy.

#### Scenario: The refusal totality holds across both routes

- **WHEN** the paths by which a memory strategy could take effect are enumerated
- **THEN** there are none
- **AND** a test constructed to make a memory strategy take effect by either route comes back refused.

### Requirement: A memory proposal SHALL remain honestly surfaced as refused-not-scored

The addition of a delivery report SHALL NOT cause a memory proposal to be presented as a result, a
win, or a pending win. Its refused-not-scored status SHALL be unchanged.

#### Scenario: Delivery reporting does not upgrade a refused proposal

- **WHEN** a memory proposal carries a delivery report naming two refusals
- **THEN** it is still surfaced as refused-not-scored
- **AND** no memory win is reported anywhere.
