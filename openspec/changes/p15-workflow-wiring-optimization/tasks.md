# Tasks — P15: Workflow / Node-Wiring Optimization

Two waves. **Wave 15a** = the node-wiring operators — implement `OpMerge`, broaden reorder toward free
rewiring, confirm prune — every candidate derived, gated, and hashed. **Wave 15b** = wiring-safety as a
first-class requirement set — reject-at-compile, adapter reconciliation in the diff, admissibility,
deterministic identity, and the interim refusal for un-materializable wiring.

This round ships **docs** (the PRD + this OpenSpec change). Doc tasks are `[x]`; the code tasks they
specify are `[ ]` with a `→ file (Test: Name)` pointer. 🔴 marks a security/must-fail gate; 🚫 marks a
banned action; → marks the evidence pointer.

**Standing constraints.** The wiring axis lives entirely in `VariantSpec.Order`/`Edges`/`InsertedAdapter`
— **no** new `Dimension`, registry `Kind`, `NodeOverride` field, or DB table. `Order`/`Edges` are
**identity-bearing**, so a wiring change is a new `config_hash` with **no eval-side change** (the harness
consumes `config_hash` + `Trace`). Every candidate is **derived** with `ParentVariantID`; the parent is
never mutated. Every candidate is validated by the **one** gate (`GateReorder`) before any transform.
Wiring is **not** yet materialized as source — an un-materializable wiring spec is **refused at transform**,
never silently dropped or no-op'd. A candidate is **surfaced only after P5.5 verification** on held-out data.

---

## 1. System Designer — Fix the one-way-door contracts before any operator ships (15a)

- [x] 1.1 Record the **`OpMerge` semantics** — adjacent-pair only, survivor subsumes, absorbed node
      dropped from `Order`, edges mechanically rewired through the survivor — as a one-way door, because
      a stored proposal row will name `OpMerge` and every future reader depends on its meaning.
      → [`decisions.md`](decisions.md) D-1; PRD §8.3 D1.
- [x] 1.2 Record the **adapter-insertion posture** — explicit `InsertedAdapter` node, its `io_contract`
      carried, materialized as generated source in the same diff, never a runtime shim — as a one-way
      door. → [`decisions.md`](decisions.md) D-2; PRD §8.3 D4.
- [x] 1.3 State the **EXISTS / PARTIAL / ABSENT** ledger so the honest boundary is on the record (ordering
      EXISTS; free rewiring PARTIAL; `OpMerge` RESERVED; source materialization ABSENT). → PRD §8.2.
- [ ] 1.4 🚫 Do **not** add a `Dimension` const, registry `Kind`, `NodeOverride` field, or DB table — the
      axis is `Order`/`Edges`, already hashed. → guard: `TestNoNewDimensionForWiring` (asserts the
      `Dimension` enum still has exactly the four content values, [`spec.go:42-47`](../../../internal/variantspec/spec.go)).

## 2. Backend — The merge operator (15a)

- [x] 2.1 Specify `mergeOp`: `Kind()=OpMerge`, `HandlesSignal()=SignalRedundantNode`, `Propose` derives a
      candidate that drops the absorbed node from `Order` and rewires its edges through the survivor.
      → `internal/proposal/catalog.go` `mergeOp` (Test: `TestMergeProducesFusedSpec`).
- [ ] 2.2 Implement `mergeOp.Propose` on the `Reorder`/derive helpers so the candidate carries
      `ParentVariantID` and leaves the parent spec byte-identical. → `internal/proposal/catalog.go`
      (Test: `TestMergeDerivesWithLineageParentUnchanged`).
- [ ] 2.3 Register `mergeOp{}` in `DefaultCatalog()` — one row in the dispatch table, never a switch edit.
      → [`catalog.go:17-31`](../../../internal/proposal/catalog.go) (Test: `TestDefaultCatalogIncludesMerge`).
- [ ] 2.4 Confirm the `OpMerge` gain prior is live now that the operator exists (it already sits in
      `gain.go`). → [`gain.go:20,29`](../../../internal/proposal/gain.go) (Test: `TestMergeHasPrior`).
- [ ] 2.5 A merge candidate's `config_hash` differs from its parent (Order/Edges are identity-bearing);
      a merge that resolved to the same configuration hashes identically. → (Test: `TestMergeChangesConfigHash`).

## 3. Backend — Free edge rewiring (15a)

- [x] 3.1 Specify **free reorder**: reorder data-independent nodes and mark parallelizable ones, beyond
      the single lost-in-middle swap [`catalog.go:193-198`](../../../internal/proposal/catalog.go).
      → `internal/proposal/catalog.go` `reorderOp` (Test: `TestFreeReorderIndependentNodes`).
- [ ] 3.2 Implement bounded independent-node reordering; every candidate routes through `GateReorder`.
      → `internal/proposal/catalog.go` (Test: `TestReorderCandidatesAreGated`).
- [ ] 3.3 Confirm **prune** rewires neighbours and drops the dead node (already implemented — assert the
      shape holds under P15). → [`catalog.go:326-344`](../../../internal/proposal/catalog.go)
      (Test: `TestPruneRewiresNeighbours`).
- [ ] 3.4 Every wiring candidate is deterministic — same base + signal → identical candidate spec and
      `config_hash`. → (Test: `TestWiringProposalsAreDeterministic`).

## 4. Backend — The interim refusal for un-materializable wiring (15a)

- [x] 4.1 Specify the **interim refusal**: a resolved spec whose `Order`/`Edges` differ from the discovered
      wiring is refused at transform with an `unsafeRewrite`-class error naming the wiring axis — the
      analogue of `refuseSkills`/`refuseContext`. → PRD §6 FR8; spec `node-wiring`.
- [ ] 4.2 🔴 Implement the refusal in the transform engine; a wiring-differing spec returns the typed
      refusal, **never** a silent no-op that would let a wiring `config_hash` be scored against unchanged
      source. → `internal/transform/rewrite.go` `refuseWiring` (analogue of
      [`refuseSkills`/`refuseContext`, :388,:417](../../../internal/transform/rewrite.go))
      (Test: `TestWiringRefusedNotNoop`).
- [ ] 4.3 The refusal is **observable** in the transform result and names the axis, and **no diff** is
      emitted for the refused spec. → (Test: `TestWiringRefusalIsObservableNoDiff`).

## 5. Backend + System Designer — Wiring-safety: the coherence gate as a requirement (15b)

- [x] 5.1 Specify that a candidate ordering is validated by `GateReorder` → `ValidateOrdering` **before**
      any codemod is generated. → spec `wiring-safety`; [`rearrange.go:52`](../../../internal/variantspec/rearrange.go).
- [ ] 5.2 🔴 Assert **reject-at-compile**: an ordering that consumes a field before it is produced yields
      **no runnable spec** (`GateReorder` returns `(nil, verdict)`) and **no diff, codemod, or PR**. The
      gate must **go red**. → extends `TestGateReorder_RejectedYieldsNoRunnableSpec`
      ([`rearrange_test.go:69`](../../../internal/variantspec/rearrange_test.go)) to merge/prune candidates
      (Test: `TestIncoherentWiringRejectedAtCompile`).
- [ ] 5.3 Assert an **`adapted`** verdict records the adapter as an explicit `InsertedAdapter` node and
      rewires edges producer→adapter→consumer. → [`rearrange.go:66-89`](../../../internal/variantspec/rearrange.go)
      (Test: `TestAdaptedVerdictRecordsAdapter`, cf. `TestGateReorder_AdaptedRecordsAdapter`).
- [ ] 5.4 🔴 Assert an adapter is admissible **only if it drops nothing the consumer requires**; a
      non-satisfying adapter is refused and the ordering rejected with it. →
      [`adapter.go:73-82`](../../../internal/typedcontract/adapter.go) (Test: `TestAdapterDropsNothingRequired`).
- [ ] 5.5 Assert an inserted adapter appears as **generated source in the same reviewable diff** — no
      coercion exists outside the diff. → (Test: `TestAdapterIsInReviewableDiff`).
- [ ] 5.6 Assert adapter **identity is deterministic** — same reorder → same adapter ids and `config_hash`.
      → [`rearrange.go:91-93`](../../../internal/variantspec/rearrange.go) (Test: `TestAdapterIdentityDeterministic`).

## 6. AI Engineer — Verification-gated surfacing (15a→15b)

- [x] 6.1 Specify that a produced wiring candidate is **surfaced as a recommended change only after P5.5
      verification** shows it better or cheaper on held-out data; a produced candidate is exploratory
      until then. → PRD §6 FR7; spec `node-wiring`.
- [ ] 6.2 A wiring-changed `config_hash` is scored by the **existing** harness — no metric added, no
      Dimension-label branch. → 🚫 no new metric in `internal/evalharness`
      (Test: `TestWiringScoredByExistingHarness`).
- [ ] 6.3 A merge that reads redundant but scores worse on held-out data is **not** surfaced as a
      recommendation. → (Test: `TestUnverifiedMergeNotSurfaced`).

## 7. QA — Acceptance gate

- [x] 7.1 Merge-shape suite: absorbed node dropped from `Order`, edges rewired through survivor, parent
      unchanged, `config_hash` differs. → (Test: `TestMergeProducesFusedSpec`, `TestMergeChangesConfigHash`).
- [x] 7.2 🔴 Safety suite: incoherent ordering → no runnable spec, no diff; the gate **goes red**. →
      (Test: `TestIncoherentWiringRejectedAtCompile`).
- [x] 7.3 Adapter suite: `adapted` records the adapter in the spec **and** the diff; a non-satisfying
      adapter is refused. → (Test: `TestAdaptedVerdictRecordsAdapter`, `TestAdapterDropsNothingRequired`,
      `TestAdapterIsInReviewableDiff`).
- [x] 7.4 Determinism suite: same base + signal → identical candidate spec, adapter ids, and `config_hash`.
      → (Test: `TestWiringProposalsAreDeterministic`, `TestAdapterIdentityDeterministic`).
- [x] 7.5 🔴 Interim-refusal suite: a wiring-differing resolved spec is refused at transform naming the
      axis, never a silent no-op; no diff emitted. → (Test: `TestWiringRefusedNotNoop`,
      `TestWiringRefusalIsObservableNoDiff`).
- [x] 7.6 Eval-agnostic suite: a wiring-changed `config_hash` scores through the existing harness with no
      P15 eval change. → (Test: `TestWiringScoredByExistingHarness`).

## 8. Documentation

- [x] 8.1 Author the P15 PRD (14 sections). → [`../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../docs/prd/P15-workflow-wiring-optimization.md).
- [x] 8.2 Author this OpenSpec change: `proposal.md`, `design.md`, `tasks.md`, `decisions.md`, and the two
      capability spec deltas (`node-wiring`, `wiring-safety`).
- [ ] 8.3 On delivery, fold the two P15 capability specs into `openspec/specs/`. → `openspec/specs/{node-wiring,wiring-safety}/spec.md`.
