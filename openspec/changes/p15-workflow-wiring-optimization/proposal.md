## Why

The platform optimizes each LLM node across four content dimensions — model, prompt, skills, context
([`internal/variantspec/spec.go:42-47`](../../../internal/variantspec/spec.go)) — but the **shape of the
graph** is where a large class of real wins lives: two adjacent calls one model can subsume in one, a
node whose output nothing downstream reads, an ordering that buries the decisive context. That structural
axis already exists on the Variant Spec — `VariantSpec.Order` and `VariantSpec.Edges` are first-class and
**identity-bearing** in `config_hash` ([`spec.go:253-258`](../../../internal/variantspec/spec.go)) — and
two of its operators are real: `OpReorder` and `OpPrune` are implemented and registered in the proposal
catalog ([`internal/proposal/catalog.go:175-199`, `:326-344`](../../../internal/proposal/catalog.go)).

**The axis is half-operated.** `OpMerge` is a **reserved constant with a gain prior and no
implementation**: it appears at [`operator.go:46`](../../../internal/proposal/operator.go) and
[`gain.go:20,29`](../../../internal/proposal/gain.go), but there is no `mergeOp` and it is **absent from
`DefaultCatalog()`**. The reorder operator does only a single adjacent swap of a lost-in-middle node
([`catalog.go:193-198`](../../../internal/proposal/catalog.go)); free rewiring — parallelizing
independent nodes, general reordering — is proposed by nothing. And no wiring change is materialized as
source: the transform engine emits model and prompt edits and **refuses** skills and context
([`rewrite.go:388,417`](../../../internal/transform/rewrite.go)); it emits nothing for `Order`/`Edges`.

**Safety here is the load-bearing requirement, not a footnote.** Rearranging a graph can break it in a
way content edits cannot: a node may consume a field only its predecessor produced, and moving or pruning
that predecessor makes the input undefined. The typed-contract coherence gate already exists to catch
this ([`internal/typedcontract/adapter.go:61-84`](../../../internal/typedcontract/adapter.go),
`GateReorder` at [`rearrange.go:52`](../../../internal/variantspec/rearrange.go)), and the OpenSpec format
contract uses *"reject a Variant Spec whose node ordering violates a typed I/O contract"* as its canonical
example of a behavioral requirement ([`AGENTS.md:87`](../../AGENTS.md)). P15 makes that example a shipped,
named requirement.

This change turns wiring into a **full optimization axis** (`node-wiring`) and promotes its safety to a
**first-class requirement** (`wiring-safety`), consuming P1/P2/P3/P4/P5.5 unchanged. Because the harness
is axis-agnostic — it consumes `config_hash` + `Trace`, never a Dimension label
([`internal/evalharness/evaluator.go`](../../../internal/evalharness/evaluator.go)) — a wiring-changed
`config_hash` is scored with **no eval-side change at all**.

## What Changes

- **New capability `node-wiring`.** The proposal catalog gains a real **merge** operator (`OpMerge`),
  registered in `DefaultCatalog()`, that fuses **two adjacent nodes** into one when a single model call
  can subsume both — producing a candidate Variant Spec whose absorbed node is **dropped from `Order`**
  and whose **`Edges` are rewired through the survivor**. **Free edge rewiring** — reordering independent
  nodes (including marking data-independent nodes parallelizable) and **pruning** dead nodes — each
  produces a candidate the same way, via the existing `Reorder`/prune derivation, with only wiring moved
  and per-node overrides inherited. Every candidate is **derived with `ParentVariantID` lineage** (the
  parent is never mutated), and because `Order`/`Edges` are identity-bearing, each yields a **new
  `config_hash`** and is scored as a distinct configuration. Proposals are **deterministic** (same base +
  signal → same candidate + same hash), and a produced candidate is **surfaced as a recommended change
  only after P5.5 verification** shows it better or cheaper on held-out data — diagnosis proposes,
  verification decides. Until source-level materialization exists, a resolved spec whose `Order`/`Edges`
  differ from the discovered wiring is **refused at transform** (`unsafeRewrite`-class, naming the axis) —
  never silently dropped, never a no-op that would let a wiring `config_hash` be scored against unchanged
  code.
- **New capability `wiring-safety`.** The typed-contract coherence gate becomes a named requirement set.
  A candidate ordering is validated by `GateReorder` → `ValidateOrdering` **before any codemod is
  generated**. A Variant Spec whose reordering or rewiring **violates a typed I/O contract SHALL be
  rejected at compile** — `GateReorder` returns no runnable spec, so no diff, codemod, or pull request is
  generated ([`AGENTS.md:87`](../../AGENTS.md)'s example made real). When a **catalogued adapter**
  reconciles a producer→consumer mismatch, the ordering is admitted (`adapted`), the adapter is recorded
  as an **explicit `InsertedAdapter` node** ([`spec.go:213-229`](../../../internal/variantspec/spec.go)),
  edges are rewired producer→adapter→consumer, and the adapter **ships as generated source in the same
  reviewable diff** — never a hidden runtime coercion. An adapter is admissible **only if it drops
  nothing the consumer requires** ([`adapter.go:73-82`](../../../internal/typedcontract/adapter.go)), and
  adapter identity is **deterministic** so the same reorder yields the same inserted adapters and the same
  `config_hash` on every evaluation.
- **New capability `wiring-materialization` (wave 15c).** The interim refusal is replaced for exactly ONE
  shape: a **transposition of two adjacent, independent sibling statements**, materialized as a
  **permutation of the file's lines** — same count, same multiset, every changed line inside one of the
  two swapped blocks. It is admitted only when both call sites are in the same file at the same nesting,
  consecutive, whole-line, neither control flow, and neither binds a name the other reads; the analysis
  is conservative, so unprovable independence is a refusal. Go and Python have statement materializers;
  every other language refuses **by name**. 🚫 A merge, a prune, an edge change, a non-adjacent move, or
  two transpositions at once keep the 15b refusal unchanged.
- **Not changed here.** **No general wiring codemod** — the rewriter that physically MOVES a call to an
  arbitrary position, or fuses or deletes one, is still deferred; the interim refusal holds for all of it. **No
  new eval metric and no wiring-specific scorer** — the harness stays axis-agnostic and scores a
  wiring-changed `config_hash` with the existing `RunMetrics`. **No `config_hash` contract change** —
  `Order`/`Edges`/`InsertedAdapter` already participate; P0's golden vectors are unchanged. **No new
  `Dimension` const, registry `Kind`, `NodeOverride` field, or DB table.** **No new diagnosis taxonomy
  code** — redundancy arrives as the existing `SignalRedundantNode`, exactly as `OpPrune` already consumes it.

## Impact

- **Affected capabilities:** `node-wiring` (new), `wiring-safety` (new), `wiring-materialization` (new, 15c). Consumed, not modified:
  `workflow-ir` (P1), `config-layer`/`runtime` (P2), `typed-contract` (P3), `eval-harness`/`scoring`
  (P4), `diagnosis`/`proposal` (P5.5).
- **Affected code/systems:** `internal/proposal` gains one operator (`mergeOp`) and one `DefaultCatalog()`
  row ([`catalog.go:17-31`](../../../internal/proposal/catalog.go)) plus its existing `OpMerge` prior in
  `gain.go`; `internal/variantspec` reuses `Reorder`/`GateReorder`/`withAdapters` unchanged; the transform
  engine gains a typed **interim refusal** for wiring (the honest analogue of `refuseSkills`/`refuseContext`
  at [`rewrite.go:388,417`](../../../internal/transform/rewrite.go)). No store, no schema, no eval change.
- **Dependencies:** requires **P1** (IR), **P2** (spec + `config_hash`), **P3** (typed-contract catalog),
  **P4** (axis-agnostic harness), **P5.5** (diagnosis, catalog, priors, and the verification that gates
  surfacing).
- **Unblocks:** the **structural axis becomes fully operable** (merge/reorder/prune all proposable and
  rankable alongside content operators); a future **source-level wiring codemod** has a complete, gated,
  hashed upstream to build on — it only has to replace the interim refusal with real emission.
- **Breaking:** none. `OpMerge` was a reserved constant with no behavior; implementing it is additive. No
  existing `config_hash` changes; a spec with no wiring change hashes byte-identically to today.
- **Sequencing:** **15a** (the node-wiring operators) is a complete step on its own — candidates are
  produced, gated, and hashed. **15b** (wiring-safety as a named requirement set) formalizes the gate,
  adapter posture, determinism, and interim refusal that 15a already routes through. **15c** narrows that
refusal by exactly one shape — the adjacent transposition — and is independently revertible: removing it
returns the axis to 15b's behaviour with no upstream change.
