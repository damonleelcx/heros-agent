# Tasks — P16: Context Strategy Optimization

Two waves. **Wave 16a** = context-policy materialization on the Go engine, the specified interim refusal
for other languages, and the drop-tolerance admissibility gate. **Wave 16b** = retrieval tuning verified
on held-out eval sets with the retriever pinned per measurement run.

**Standing constraints.** No new `Dimension` — `DimContext` already exists
([`internal/variantspec/spec.go:46`](../../../internal/variantspec/spec.go)) and is reused for
retrieval. No registry-schema and no `ContextSpec` change — new policies are rows behind the `Policy`
interface via `Store.AddPolicy`. No new telemetry family — reduction reads `eval_tokens_total`, loss
reads `context_drop_ratio`. **A context override is never resolved-and-dropped**: every differing policy
yields an edit or a typed refusal. Context is **code** (P10 apply-mode): the resolved assembly ships in
the diff or the transform is rejected. The drop-tolerance attribute is **additive, omit-when-absent**.
🔴 marks a security/must-fail test; 🚫 marks a banned action; → points at the evidence.

This is a **doc round**: the specs and this plan ship now; the doc tasks are checked, the code tasks are
unchecked and carry their implementation target and the test that will prove them.

---

## 1. System Designer — Fix the contracts before any rewriter ships (16a)

- [x] 1.1 Decide that context stays **one axis**: retrieval is a `rag-retrieval` policy with params, not
      a new `DimRetrieval`; no `Dimension` member is added. → `design.md` Decision 7; `decisions.md` D-1.
- [x] 1.2 Decide **drop tolerance** is a per-node admissibility input, additive and omit-when-absent, not
      a policy fact — so a node declaring none hashes byte-identically to pre-P16. → `design.md`
      Decision 3 + §8.4; `decisions.md` D-2.
- [x] 1.3 Decide the **interim refusal** is a first-class, tested, per-language behavior and correct the
      refusal's owner (currently P3) to this phase. → `design.md` Decision 1; spec `context-policy`
      "SHALL be refused at transform".
- [x] 1.4 Decide **held-out verification** and **retriever pinning** as the retrieval-tuning invariants.
      → `design.md` Decisions 4, 5; spec `retrieval-tuning`.
- [ ] 1.5 Add the additive `context_drop_tolerance` attribute to `NodeOverride` (+ `isEmpty` / `Refs` /
      `Validate`) and to `ResolvedNode`, omit-when-absent so `config_hash` stays byte-compatible. →
      `internal/variantspec/spec.go:183`, `resolved.go:46` (Test: `TestDropToleranceIsAdditiveToHash` —
      a node with no tolerance hashes identically to a pre-P16 node).

## 2. Backend — Context materialization, Go engine (16a, the hard part)

- [x] 2.1 Document the message-assembly rewrite contract: what call-site region is rewritten, and why a
      policy is a *region* rewrite and not an argument swap. → `design.md` Decision 2 + Interfaces sketch.
- [ ] 2.2 Replace `refuseContext` for the Go engine with a real materialization: rewrite the call site's
      message-assembly region so the resolved policy governs the message list, deterministically given
      policy + params + conversation + seed. → `internal/transform/rewrite.go:409` `rewriteContext`
      (Test: `TestGoContextMaterializes` — a `sliding-window` override on a Go node emits an edit, not a
      refusal).
- [ ] 2.3 Materialize inline as **code** (P10 apply-mode): the resolved assembly appears in the diff; no
      apply mode hides it. → `internal/transform/engine.go` context dispatch (Test:
      `TestContextChangeAppearsInDiff`).
- [ ] 2.4 🔴 Guarantee **no silent drop**: a node whose resolved `ContextPolicy` differs from its
      discovered assembly yields an edit or a typed `unsafeRewrite`; it is never resolved-and-discarded.
      → `internal/transform/rewrite.go` (Test: `TestContextOverrideNeverSilentlyDropped` — asserts the
      override is applied OR refused, never absent-with-base-hash).
- [ ] 2.5 Make the Go materialization **byte-identical** across runs for LLM-free policies
      (`sliding-window`, `semantic-compaction`, augmentation). → `internal/transform/rewrite.go` (Test:
      `TestGoContextMaterializationDeterministic` — same `config_hash` + `source_revision` + seed →
      identical diff).
- [ ] 2.6 A host-calling policy (`summarization`) reaches its model only through `HostServices`,
      host-side, and captures its `ResolvedRequest`; 🚫 no summarizer call from a sandboxed node. →
      `internal/registry/context_policies.go:188` (existing) + transform wiring (Test:
      `TestSummarizerRunsHostSideOnly`).

## 3. Backend — The specified interim refusal, per language (16a)

- [x] 3.1 Specify the interim refusal as a requirement: a node carrying an un-applicable `ContextPolicy`
      SHALL be refused at transform, naming node + policy + reason, per language until its rewriter
      lands. → spec `context-policy` "SHALL be refused at transform".
- [ ] 3.2 Correct `refuseContext`'s reason text: name the owning phase (P16), not P3, and state the
      rewrite is landing rather than deferred-forever. → `internal/transform/rewrite.go:417`
      `refuseContext` (Test: `TestRefusalNamesOwningPhase`).
- [ ] 3.3 Keep the tree-sitter span engine's context refusal in place, **tested**, until its rewriter
      lands: an override on an unbuilt language refuses loudly and names itself. →
      `internal/transform/rewrite_span.go:59` dispatch (Test: `TestSpanEngineContextRefusesNotDrops`).
- [ ] 3.4 🔴 Assert the refusal is not a silent no-op: an override on an unbuilt language produces a
      typed error, and the variant does **not** transform as its base config. →
      `internal/transform/rewrite_span.go` (Test: `TestInterimRefusalIsLoudNotSilent`).

## 4. Backend + System Designer — New policies behind the interface (16a)

- [x] 4.1 Specify `hierarchical-summary` and `structured-extraction` as additions behind the `Policy`
      interface, decided by warrant not by default. → spec `context-policy` "added via the Policy
      interface"; PRD §14 Q4 (whether `structured-extraction` is lossy).
- [ ] 4.2 Implement `hierarchical-summary` (`Name` / `ParamsSchema` / `Assemble`) and register it via
      `Store.AddPolicy`; no schema change, no `Dimension` change. →
      `internal/registry/context_policies.go` + `internal/registry/store.go:41` (Test:
      `TestHierarchicalSummaryPolicyAddedNoSchemaChange`).
- [ ] 4.3 Implement `structured-extraction` behind the interface with its `Lossy` flag set per the Q4
      decision; drop is measured, not assumed. → `internal/registry/context_policies.go` (Test:
      `TestStructuredExtractionDropMeasured`).

## 5. AI Engineer — Drop-loss as a scored, gated signal (16a)

- [x] 5.1 Specify `DropRatio` as a scored quality signal, with a measured `0.0` distinguished from an
      unmeasured lossless policy (the `Lossy` flag). → spec `context-policy` "modeled as a scored quality
      signal"; PRD FR7.
- [ ] 5.2 Record a materialized lossy policy's observed drop per node per run via the existing
      `context_drop_ratio` telemetry; no new metric family. →
      `internal/telemetry/context_assembly.go:78` (existing emit) + eval wiring (Test:
      `TestMaterializedDropRecordedAsSignal`).
- [ ] 5.3 🔴 Implement the **drop-tolerance admissibility gate**: a proposal whose resolved policy would
      drive a node's drop ratio past its tolerance is **inadmissible** — rejected before transform and
      before eval spend. → `internal/proposal/catalog.go` admissibility + `internal/proposal/gain.go`
      (Test: `TestProposalPastDropToleranceInadmissible`).
- [ ] 5.4 Show a context reduction as lower `eval_tokens_total` at non-regressing `task_success` through
      the axis-agnostic harness; 🚫 no context-specific scorer. → consumed from
      `internal/evalharness/metricnames.go:27` (Test: `TestContextReductionLowersEvalTokensNoRegression`).

## 6. AI Engineer + Backend — Retrieval tuning, verified on held-out data (16b)

- [x] 6.1 Specify `OpRAGTune` proposals — top-k, chunk size, rerank on/off, embedding model — admissible
      only on a `RetrievalRAG` node. → spec `retrieval-tuning`; PRD FR10; existing gate
      [`catalog.go:214-216`](../../../internal/proposal/catalog.go).
- [ ] 6.2 Extend `OpRAGTune` to propose **chunk size** and **embedding-model** variants in addition to
      the existing top-k / retriever swaps. → `internal/proposal/catalog.go:218` `ragTuneOp.Propose`
      (Test: `TestRAGTuneProposesChunkAndEmbedding`).
- [ ] 6.3 🔴 Verify a retrieval change on a **held-out** eval set disjoint from its tuning set; a win on
      the tuning set alone is **not** a verified delta. → verification wiring over P4 eval sets (Test:
      `TestRetrievalVerifiedOnHeldoutSet` — an overlapping split is refused).
- [ ] 6.4 🔴 Pin the retriever, its params, and the seed per measurement run so the same `config_hash`
      issues the identical `ResolvedRequest`, including rerank. →
      `internal/registry/context_policies.go:245` (existing `ResolvedRequest`) + measurement pinning
      (Test: `TestRetrievalMeasurementDeterministic`).
- [ ] 6.5 Apply the drop gate to retrieval: a tuning proposal that would push a node past its tolerance
      is inadmissible (a larger top-k / lossy rerank can shrink the retained conversation). →
      `internal/proposal/catalog.go` (Test: `TestRetrievalTunePastDropToleranceInadmissible`).
- [ ] 6.6 Record pure augmentation as retrieval, not loss: `DropRatio` 0 with a positive
      `RetrievedChunks`. → `internal/registry/context_policies.go:281-288` (existing) (Test:
      `TestAugmentationIsNotDrop`).

## 7. QA — Acceptance gate (16a + 16b)

- [ ] 7.1 🔴 **No-silent-drop suite**: a differing context policy yields an edit or a typed refusal; a
      test asserts the override is never applied as the base config. → (Test:
      `TestContextOverrideNeverSilentlyDropped`, `TestInterimRefusalIsLoudNotSilent`).
- [ ] 7.2 Refusal suite: an override on an unbuilt language refuses with node + policy named, and the
      owner text is accurate. → (Test: `TestSpanEngineContextRefusesNotDrops`, `TestRefusalNamesOwningPhase`).
- [ ] 7.3 Determinism suite: LLM-free materialization byte-identical; summarization identical
      `ResolvedRequest`. → (Test: `TestGoContextMaterializationDeterministic`, `TestSummarizerRunsHostSideOnly`).
- [ ] 7.4 🔴 Drop-gate suite: a proposal past a node's tolerance is inadmissible, before transform. →
      (Test: `TestProposalPastDropToleranceInadmissible`, `TestRetrievalTunePastDropToleranceInadmissible`).
- [ ] 7.5 🔴 Held-out suite: the verified set is disjoint from the tuned set; an overlap is refused. →
      (Test: `TestRetrievalVerifiedOnHeldoutSet`).
- [ ] 7.6 Eval-agnosticism suite: a context reduction lowers `eval_tokens_total` at non-regressing
      `task_success` with no scorer change. → (Test: `TestContextReductionLowersEvalTokensNoRegression`).
- [ ] 7.7 Additivity suite: a node with no drop tolerance hashes byte-identically to pre-P16; no new
      `Dimension`. → (Test: `TestDropToleranceIsAdditiveToHash`).

## 8. Documentation

- [x] 8.1 Author the P16 PRD (14 sections). → `docs/prd/P16-context-strategy-optimization.md`.
- [x] 8.2 Author the change proposal, this task plan, and the design record. → `proposal.md`,
      `tasks.md`, `design.md`.
- [x] 8.3 Author the two capability spec deltas. →
      `specs/context-policy/spec.md`, `specs/retrieval-tuning/spec.md`.
- [x] 8.4 Record the one-way-door contracts (no new `Dimension`; additive drop-tolerance). →
      `decisions.md`.
- [ ] 8.5 On merge, fold the two capability specs into `openspec/specs/`. → `openspec/specs/{context-policy,
      retrieval-tuning}/spec.md` (ADDED → Requirements, operation headers dropped).
