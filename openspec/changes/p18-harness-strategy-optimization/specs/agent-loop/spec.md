# Agent Loop — Spec Delta (P18)

Product rationale: [`../../../../../docs/prd/P18-harness-strategy-optimization.md`](../../../../../docs/prd/P18-harness-strategy-optimization.md)
§6 (FR8–FR19), §7 (NFR1–NFR7). Design reasoning: [`../../design.md`](../../design.md) Decisions 1, 3, 4, 5,
6, 7; [`../../decisions.md`](../../decisions.md) D-2, D-3, D-4, D-5, D-6.

Covers the harness **dimension** — the new `DimHarness` axis, its sparse override, its resolution, its
participation in `config_hash`, its **interim refusal** at transform, its composition with P15 wiring, the
`OpHarnessStrategy` operator with its cost/quality admissibility gate, and the bounded-autonomy / sandbox
containment guarantees. The strategy catalog itself is the `harness-strategy` capability.

> **Why the axis is modelled and hashed now but refused at transform.** Materializing a control loop at a
> call site — wrapping a single call in a bounded turn loop with a stop condition and a critic — is code
> generation, strictly more structural than the already-refused `skills`/`context`. Shipping it modelled,
> resolved, and hashed but **refused** (never silently dropped) is the repo's honest interim: a silent drop
> would let the platform score a variant as if its scaffold changed when the emitted source did not — the
> one false result an eval platform must never produce.

## ADDED Requirements

### Requirement: Harness strategy SHALL be a new closed Dimension

The variant-spec Dimension set SHALL gain a `DimHarness` member. The set SHALL remain closed, and every
error and iteration over dimensions SHALL be able to name the harness dimension.

#### Scenario: The harness dimension is a member of the closed set

- **WHEN** the set of overridable dimensions is enumerated
- **THEN** the harness dimension is a member
- **AND** the set remains closed to unnamed dimensions.

### Requirement: A node's harness SHALL be a sparse, ref-only override

A `NodeOverride` SHALL carry an optional harness reference. An absent harness override SHALL mean "leave
this node's scaffold as discovered." The override SHALL reference a strategy by `version_id` only.

#### Scenario: An absent harness override leaves the node as discovered

- **WHEN** a node is overridden with no harness reference
- **THEN** its resolved scaffold is the discovered default
- **AND** no harness value is introduced.

#### Scenario: A harness override resolves to its registry entry

- **WHEN** a node carries a harness `version_id`
- **THEN** it resolves to that strategy entry, pinned by `version_id`
- **AND** an unresolvable reference fails the resolve closed naming the reference.

### Requirement: The resolved harness SHALL participate in config_hash, changing it iff the harness changes

The resolved node SHALL carry the harness reference such that `config_hash` changes if and only if a node's
resolved harness changes. The field SHALL be additive.

#### Scenario: Changing the harness changes the hash

- **WHEN** two otherwise-identical configurations differ only in one node's resolved harness
- **THEN** their `config_hash` values differ.

#### Scenario: An unrelated change does not change the hash via the harness field

- **WHEN** a configuration is re-resolved with no change to any node's harness
- **THEN** the harness field contributes no change to `config_hash`.

### Requirement: A configuration declaring no harness SHALL hash byte-identically to its pre-P18 form

A configuration in which no node declares a harness override and no non-default harness was discovered SHALL
produce a `config_hash` byte-identical to the value it produced before the harness field existed.

#### Scenario: A no-harness configuration reproduces its prior hash

- **WHEN** a configuration that declares no harness is resolved
- **THEN** its canonical bytes contain no harness key
- **AND** its `config_hash` equals the value produced before the harness field was added.

#### Scenario: Existing golden vectors reproduce unchanged

- **WHEN** the frozen `config_hash` golden vectors are reproduced after the harness field is added
- **THEN** every vector reproduces bit-for-bit.

### Requirement: The IR SHALL record a node's discovered default harness additively

The intermediate representation SHALL record each node's discovered default harness as an additive field,
defaulting to `single-shot` unless discovery can prove a loop at the call site. Existing IR consumers SHALL
NOT break.

#### Scenario: A node with no proven loop defaults to single-shot

- **WHEN** a node is discovered and no loop is proven at its call site
- **THEN** its recorded default harness is `single-shot`.

#### Scenario: The added IR field is backward-compatible

- **WHEN** an IR consumer that predates the field reads a discovered IR
- **THEN** it continues to function
- **AND** the field's absence is a valid state.

### Requirement: A node or node-group carrying a HarnessRef SHALL be refused at transform, never silently dropped

A resolved configuration in which a node — or an ordered edge set — carries a harness reference SHALL be
refused at transform with a typed unsafe-rewrite error naming the strategy and the reason. The override
SHALL NOT be silently dropped, and the transform SHALL NOT emit an incorrect control loop.

#### Scenario: A harness override is refused with a typed error

- **WHEN** a resolved node carrying a harness reference is transformed
- **THEN** the transform returns a typed unsafe-rewrite error
- **AND** the error names the strategy and states that materializing a control loop is not an argument swap.

#### Scenario: The override is present-and-refused, not absent

- **WHEN** a harness override is refused at transform
- **THEN** the resolved configuration still carries the harness reference
- **AND** the refusal is observable rather than the override being silently omitted.

#### Scenario: A group harness is refused naming its edge set

- **WHEN** a harness reference scoped to an ordered edge set is transformed
- **THEN** the transform refuses with a typed error naming the edge set
- **AND** no partial or incorrect loop is emitted.

### Requirement: A harness SHALL compose with wiring and SHALL NOT reorder nodes

A harness SHALL wrap a single node or an explicit ordered edge set. The group form SHALL consume the
existing node ordering and edges rather than re-deriving them, and a harness override SHALL NOT change node
ordering.

#### Scenario: A group harness consumes the given edge set

- **WHEN** a harness is scoped to an ordered edge set
- **THEN** it references the existing ordering and edges
- **AND** it does not define a second, divergent edge set.

#### Scenario: A harness override never reorders

- **WHEN** a configuration differs from its parent only by a harness override
- **THEN** the node ordering is unchanged
- **AND** no reordering is attributed to the harness change.

### Requirement: A harness variant SHALL be scored by the existing evaluation harness with no scoring change

A configuration differing only in a node's harness SHALL be scored by the existing axis-agnostic evaluation
harness on `config_hash` and trace, with no new metric and no change to scoring.

#### Scenario: A harness variant is scored without an eval change

- **WHEN** a harness variant is evaluated
- **THEN** it is scored using the standard metric family
- **AND** no new metric and no scoring change is introduced for the harness axis.

### Requirement: The proposal catalog SHALL provide a verification-gated harness-strategy operator

The proposal catalog SHALL provide a harness-strategy operator that emits harness-override variant specs.
Every proposed scaffold swap SHALL be routed through verification before it can ship.

#### Scenario: A proposed scaffold swap is verification-gated

- **WHEN** the harness-strategy operator proposes a scaffold swap
- **THEN** the proposal is routed through verification
- **AND** it does not ship on an unverified result.

### Requirement: A heavier harness SHALL be admissible only when its task_success gain outweighs its added cost and latency

A strategy that adds turns SHALL be admitted over a lighter one only when the measured `task_success` gain
outweighs its added `eval_cost_usd` and `eval_latency_ms`, computed on held-out cases disjoint from those
used to shape the proposal.

#### Scenario: A cost-only win is rejected

- **WHEN** a heavier harness raises `eval_cost_usd` or `eval_latency_ms` without a commensurate
  `task_success` gain
- **THEN** the admissibility gate rejects the swap.

#### Scenario: Admissibility is measured on a disjoint held-out set

- **WHEN** admissibility is computed for a scaffold swap
- **THEN** the cases used are disjoint from any used to generate the proposal
- **AND** the gate cannot be satisfied by tuning on its own test set.

### Requirement: A multi-turn harness SHALL be bounded and SHALL run within the node's existing sandbox and grant

Every multi-turn strategy SHALL declare a bounded `max_turns` and a stop condition; no strategy SHALL
express an unbounded loop. The added turns SHALL run within the node's existing sandbox and tool grant,
SHALL NOT widen egress or tool scope, and the enlarged turn and tool-call surface SHALL be observable in the
trace.

#### Scenario: A run reaching the turn ceiling terminates and is recorded

- **WHEN** a multi-turn run reaches its `max_turns` ceiling
- **THEN** it terminates
- **AND** the termination is recorded rather than the run hanging.

#### Scenario: The added turns reach nothing outside the existing grant

- **WHEN** a multi-turn strategy runs its added turns
- **THEN** those turns reach no egress destination or tool outside the node's existing grant
- **AND** the enlarged turn and tool-call surface is observable in the trace.
