## Why

Three customer sentences land on one axis, and one of them lands nowhere:

- *"stop after four turns; reflect between them"* → should be **loop**, lands on `harness`
- *"never spend more than a dollar and never reach the network"* → should be **harness**, lands on `harness`
- *"run these two calls in parallel and merge the results"* → should be **graph**, is **inexpressible**

The third is not a naming problem. `VariantSpec.Order` is a `[]string` — a linear sequence cannot say two
nodes are concurrent — and `Edge.Kind` is a closed two-value set, `"data" | "control"`, so there is no
conditional edge. `internal/arrangements` optimizes topology with `nextPermutation` over that list. The
platform's graph optimization is sequence permutation, which is a real capability and is not graph
engineering.

The first two are a naming problem with a review cost. `DimHarness`'s own doc comment defines it as two
things — *"how many turns it runs and in what control loop"* — so an operator tightening a spend ceiling
and an engineer changing a reflection prompt edit the same registry kind and produce the same class of
`config_hash` change, with nothing in the axis to tell them apart.

Program ruling **R2** separates them. [ADR-014](../../../docs/adr/ADR-014-harness-loop-graph-axis-split.md)
carries the reasoning, including why the clean version of this change is refused: removing the loop fields
from `HarnessSpec` would change the `version_id` of every harness entry that has one — `registry.Kind` is
hashed into the `version_id` — which changes the `config_hash` of every spec referencing it, which orphans
every measurement ever taken on a multi-turn node.

## What Changes

- **ADDED** `DimLoop` and `registry.KindLoop` — the iteration policy: strategy, stop condition, `max_turns`
  as a chosen value, reflection prompt, critic binding. **No new strategy**; the existing five and the
  existing four stop conditions are relocated, not extended.
- **MODIFIED** `DimHarness` — narrowed to the execution envelope: sandbox posture, host-service provision,
  turn and spend ceilings, retries, timeouts, concurrency limit, guardrail and approval-gate bindings.
- **ADDED** the ceiling/value split: the envelope imposes a ceiling, the loop chooses within it, and a
  `max_turns` above the ceiling is refused at resolve naming both values.
- **MOVED LEFT** the host-service refusal: a `react-loop` with no tool executor is refused at **resolve**
  rather than failing at run.
- **ADDED** graph topology at the spec level — **concurrency** declared over the existing `order`,
  **conditional edges** whose predicates follow ADR-004's `expr` binding rules, and **merge**, which a
  fan-in must declare or be refused at validate.
- **ADDED** the ambiguity refusal: a spec setting both a legacy loop-bearing `harness_ref` and a `loop_ref`
  is refused at resolve, naming both.
- **NOT REMOVED, deliberately:** the loop fields on `HarnessSpec`. Legacy entries remain resolvable
  indefinitely; new authoring cannot create one. The contract half of expand-contract is **refused on the
  record**, not deferred.
- **NOT CHANGED:** the eval harness, its scorers, its oracles and its metric family. An axis needing a
  bespoke oracle is designed wrong. Nor `internal/typedcontract`, which gates every new topology form
  unchanged.

## Impact

- **Affected capabilities:** `loop-strategy` (new), `graph-topology` (new), `harness-envelope` (modified
  from P18), and by reference `node-wiring`, `wiring-safety`, `rearrangement`
- **Affected code/systems:** `internal/variantspec` (enum, spec fields, validation),
  `internal/registry` (new kind, exhaustive switches), `internal/harnessruntime` (envelope vs loop),
  `internal/transform` (topology rewriters and their refusals), `internal/arrangements`,
  `internal/proposal` (two new operators), `web/console` (`/app/harness`, `/app/wiring` re-cut)
- **Dependencies:** upstream — P0 (golden vectors, the fence), P2 (registries), P5 (typed contracts),
  P18 (`HarnessGroups` precedent), ADR-004 (`expr` grammar), ADR-014. Unblocks —
  [P33](../../../docs/prd/P33-surface-assessment.md) reporting on `loop` and `graph`, and
  [P36](../../../docs/prd/P36-agent-self-configuration.md), the point of the program.
- **Backward compatibility is the acceptance criterion.** A spec with no `loop_ref` and no graph
  declaration must serialise byte-identically to its pre-P34 form, and the P0 golden vectors must pass
  unchanged.
- **Documents only in this program.** Every task is unchecked.
