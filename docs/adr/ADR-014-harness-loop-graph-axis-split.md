# ADR-014 — Harness, loop and graph are three axes; the split is expand-only and the contract half is refused

- **Status:** Proposed (2026-08-18) — ruling R2 of the
  [GEHA program](../prd/P31-P38-graph-engineering-agent-program.md) is taken; the split line is §3 and is
  what [P34](../prd/P34-harness-loop-graph-split.md) §14 asks to be signed off
- **Deciders:** System Design + AI Engineering (proposed) + User (ratified R2)
- **Resolves:** `DimHarness`'s own doc comment, which defines one dimension as two things —
  [`spec.go`](../../internal/variantspec/spec.go): *"the SCAFFOLD around a node's call — **how many turns
  it runs and in what control loop**"*
- **Relates to:** the OAX axis contract in [`openspec/project.md`](../../openspec/project.md) —
  *"An axis is a `Dimension`, not a feature"* and *"`config_hash` is append-only-compatible"*. This ADR is
  the first change in the program that **cannot** satisfy the second clause naively, which is the whole
  reason it exists.
- **Owns:** phase **P34 — Harness / Loop / Graph split** ([PRD](../prd/P34-harness-loop-graph-split.md))

## Context — what problem this solves

A customer sentence has to land on exactly one axis, or the axis cannot answer it. Three sentences
currently land on the same one:

| The customer says | Should be | Lands on today |
|---|---|---|
| "stop after four turns; reflect between them" | loop | `harness` |
| "never spend more than a dollar and never reach the network" | harness | `harness` |
| "run these two calls in parallel and merge the results" | graph | **nowhere — it is inexpressible** |

The third row is not a naming problem. `VariantSpec.Order` is a `[]string`
([`spec.go:422`](../../internal/variantspec/spec.go)) — *"the node ordering the executor walks"* — and a
linear sequence has no way to say that two nodes are concurrent. `Edge.Kind` is a closed two-value set,
`"data" | "control"`, so there is no conditional edge either. `internal/arrangements` optimizes topology
by calling `nextPermutation` over that list. **The platform's graph optimization is sequence permutation.**

The first two rows *are* a naming problem, and an expensive one: an operator tightening a spend ceiling
and an engineer changing a reflection prompt edit the same registry kind, produce the same class of
`config_hash` change, and are reviewed by the same person with no way to tell from the axis which of the
two happened.

### What is already there, so the split is smaller than it sounds

- `VariantSpec` already carries `Edges []Edge`, and `Validate` already enforces that every edge connects
  nodes the ordering knows about.
- `InsertedAdapter` already models a synthetic node materialized onto an edge (P5 Decision 3) — an
  explicit, inspectable node with its own `io_contract`, not a hidden coercion.
- **`HarnessGroups` already exist** — *"harnesses that wrap an ordered edge set rather than a single node
  (P18 FR15). Additive and omitempty: a spec that declares none serialises byte-identically to a pre-P18
  spec."* P18 has already crossed from per-node to spanning-a-subgraph, and did it additively.

So the graph axis extends a structure that exists. What it must add is **concurrency** and
**conditionality**, neither of which the current shapes can express.

## Decision 1 — `loop` becomes a `Dimension`; `harness` is re-scoped; **nothing is removed**

`DimLoop` is appended to the closed enum. `DimHarness` remains, and its *meaning* narrows to the
execution envelope. Crucially, **no field is deleted from any existing type and no registry entry is
rewritten.**

This is expand-contract, and this ADR ships only the expand half:

| Half | What it does | In this program? |
|---|---|---|
| **Expand** | Add `registry.KindLoop` and `DimLoop`. New authoring writes a loop ref *and* a harness ref. Legacy loop-bearing `KindHarness` entries stay resolvable forever. | **Yes** |
| **Migrate** | New specs may not create a loop-bearing harness entry. Authoring surfaces write the pair. | **Yes** |
| **Contract** | Delete the loop fields from `HarnessSpec`. | **No — refused, see below** |

### Why the contract half is refused rather than deferred

`registry.Kind` is **hashed into the `version_id`** (`registry/harness_test.go` asserts it, and says why:
*"the kind is hashed into every version_id and into the DB trigger's argument, so it cannot drift"*). A
`version_id` is referenced from stored Variant Specs; a Variant Spec's bytes are what `config_hash`
addresses; and `config_hash` is what every stored measurement is keyed by.

Therefore removing `Strategy` from `HarnessSpec` would change the `version_id` of every harness entry
that has one, which changes the `config_hash` of every spec that references it, which **orphans every
measurement ever taken on a multi-turn node**. Not "invalidates" — orphans: the row still exists and can
no longer be reached from any spec anyone can construct.

The OAX contract's promise is that *the no-override case hashes byte-identically*. That promise is kept
here. What cannot be kept is the stronger thing people will assume it means — that a *set* override also
hashes the same — and the honest response is to never take the contract step, not to take it quietly.

> 🔴 **A legacy loop-bearing harness ref is readable forever and authorable never.** A spec that sets both
> a legacy loop-bearing `harness_ref` and a new `loop_ref` is **refused at resolve** with a typed error
> naming both refs. Silently preferring one would let two authors read the same spec as two different
> computations.

## Decision 2 — the split line

| Axis | Owns | Backed today by |
|---|---|---|
| **Loop** (`DimLoop`, `registry.KindLoop`) | which control loop runs; the stop condition; `max_turns` **as a chosen value**; the reflection prompt; the critic binding | the five loops in [`strategy.go:88`](../../internal/harnessruntime/strategy.go) — `single-shot`, `reflexion`, `react-loop`, `plan-execute`, `critic-loop` — and the closed stop vocabulary `answer-marker` / `no-tool-call` / `plan-complete` / `max-turns` |
| **Harness** (`DimHarness`, re-scoped) | the envelope the loop runs inside: sandbox posture, host services, **ceilings** on turns and spend, retries, timeouts, concurrency limits, guardrail and approval gates | `harnessruntime.HostService` (`HostNone` / `HostToolInvoker` / `HostPlanner` / `HostCritic`), `TurnCeiling = 16`, `internal/sandbox`, `internal/runqueue`, `internal/approval`, `herosagent/caps.go` |
| **Graph** (spec-level, **not** a `Dimension` — see Decision 3) | topology: ordering, concurrency, conditional routing, fan-out / fan-in, merge, subgraph extent | `VariantSpec.Order`, `.Edges`, `.HarnessGroups`, `InsertedAdapter`, `internal/typedcontract`, `internal/arrangements` |

**The ceiling / value distinction is the load-bearing line.** `TurnCeiling` is harness because
`boundedCeiling` already argues it as a policy: *"the ceiling is a policy about how much autonomous
tool-calling one node may do, and honouring a value the registry would not seal would make this a second
and looser gate."* A policy about blast radius belongs to the envelope. `max_turns` — the number a loop
picks *within* that ceiling — belongs to the loop. One is imposed, the other chosen, and an operator
raising a ceiling is doing something categorically different from an engineer picking four instead of two.

## Decision 3 — graph stays **spec-level**, not a `Dimension`

Every member of `Dimensions()` is a property **of one node**: its model, its prompt, its skills, its
context, its tools, its memory, its envelope. Topology is a property **between** nodes. Making graph the
first `Dimension` that is not per-node would break the one invariant that lets the transform engine
iterate `Dimensions()` and the eval harness stay axis-agnostic.

So the graph axis is expressed where topology already lives — on the spec, beside `Order` and `Edges`,
hashed into `config_hash` as they already are (`Order` is documented as identity-bearing: *"reordering
changes config_hash (FR4), because the wiring is part of a configuration"*).

What it adds, additively and `omitempty`, so a spec declaring none serialises byte-identically:

1. **Concurrency.** `Order` cannot say two nodes run together. A `groups` structure over the existing
   order expresses fan-out and fan-in **without replacing `Order`** — the sequence remains the
   deterministic walk, and a group declares which of its members may run concurrently.
2. **Conditionality.** `Edge.Kind` grows a third member for a predicate edge. This is the one place where
   the closed-set discipline is genuinely at risk, because a predicate is an expression and an expression
   is code — so a conditional edge is subject to the **same** `expr` binding rules ADR-004 already
   imposes: declared and validated at spec-resolve time, never inferred, and refused when the name is not
   in the program's lexical scope.
3. **Merge.** `OpMerge` is reserved and unimplemented in the proposal operators. A fan-in with no merge
   is a fan-in whose results are dropped, so merge is not a later nicety; it is what makes concurrency
   mean anything.

> 🔴 **Every one of the three is gated by `internal/typedcontract`, unchanged.** P5's rule is that a
> re-arrangement whose edges violate a typed I/O contract is rejected *before* a codemod is generated. A
> concurrent group and a conditional edge are re-arrangements. They do not get a weaker gate for being
> new — and where the transform cannot safely materialize one at a call site, it is refused with a typed
> `unsafeRewrite` naming the node and the axis, never silently dropped.

## Why this design — the arbitration

Under the eight-level rule this is decided at **level 5 (不可演进 — evolvability) against level 8
(implementation cost)**, and level 8 is the floor, so it is not a close call.

Leaving harness conflated is cheap forever and evolvable never: every future loop strategy, every future
sandbox control, and every customer sentence about either one lands on one axis whose name answers
neither. The split's whole cost is implementation, which the rule places last by construction.

Level 2 (stability) is what dictates *how*, not *whether*. The naive split — move the fields, migrate the
rows — trades a stability catastrophe (orphaned measurements) for a maintenance saving, which is an L1
violation of the rule. Expand-only keeps every stored hash resolvable, and pays for it in a permanent
legacy read path. That is the correct trade and it should be uncomfortable: the residue is real and it
does not go away.

### Alternatives considered

| Option | Why not |
|---|---|
| **A — keep six axes; document loop as part of harness** | My original recommendation, and cheapest. Refused by ruling R2. Its real weakness is the third row of the table above: it does nothing at all for graph, which is the axis the program is named after. |
| **B — full split with a migration that rewrites harness entries** | Rewrites content-addressed, immutable registry entries. The registry's immutability is enforced by DB trigger; this option asks to defeat it. |
| **C — make graph a `Dimension`** | Breaks the per-node invariant that keeps the transform engine's iteration and the harness's axis-agnosticism honest. Would make `Dimensions()` mean two things. |
| **D — a new `GraphSpec` document alongside the Variant Spec** | A second configuration document is a second `config_hash` input, a second resolve path, and a second place for a gate to be missing. The spec already holds `Order` and `Edges`; topology has a home. |

## What this does not change

- **The eval harness does not learn that these axes exist.** Reduction still shows up in
  `eval_tokens_total` / `eval_cost_usd` / `task_success`. An axis needing a bespoke oracle is designed
  wrong, and none of these three needs one.
- **`Order` remains the deterministic walk.** Concurrency is declared over it, not instead of it, so a
  replayed run visits nodes in a defined sequence even when the live run overlapped them.
- **Diagnosis proposes; verification decides** — on all three axes, identically.
- **The no-override case still hashes byte-identically.** A spec with no `loop_ref` and no graph groups
  serialises exactly as it did before P34, and the P0 golden vectors keep reproducing.

## Consequences

**Accepted:**
- A permanent legacy read path for loop-bearing `KindHarness` entries, with a refusal when both are set.
  This residue never expires, and the contract half is refused **on the record** so a later phase
  proposing it is proposing a change to this ADR.
- `registry.Kind` gains a member, and every consumer that switches exhaustively over kinds must grow a
  case. That is the intended pressure: a `Kind` switch that compiles without the new case is a consumer
  that would have silently mis-sealed a loop.
- Concurrency makes runs **non-deterministic in wall-clock interleaving** while remaining deterministic in
  `config_hash`. P4's multi-seed statistics already assume run-to-run variance; nothing about the
  ranking changes, but tracing and attribution must stop assuming a single linear span sequence.

**Open, and carried into [P34](../prd/P34-harness-loop-graph-split.md) §14:**
- Whether spend ceilings sit with harness (this ADR's position) or with loop.
- Whether a conditional edge's predicate is restricted to the `expr` binding grammar or gets its own,
  narrower one.
