# Tasks — P16: Context Strategy Optimization

Four waves. **Wave 16a** = context-policy materialization on the Go engine, the specified interim refusal
for other languages, and the drop-tolerance admissibility gate. **Wave 16b** = retrieval tuning verified
on held-out eval sets with the retriever pinned per measurement run. **Wave 16c** = user-initiated change
(`context-authoring`), which depends on P13's shared `authored-change` contract landing first and is
independently revertible. **Wave 16d** = all-language coverage (`context-language-coverage`, §10), which
depends on P13's shared `language-coverage` contract landing first, is independent of 16c, and is likewise
independently revertible.

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
- [x] 1.5 Add the additive `context_drop_tolerance` attribute to `NodeOverride` (+ `isEmpty` / `Refs` /
      `Validate`) and to `ResolvedNode`, omit-when-absent so `config_hash` stays byte-compatible. →
      `internal/variantspec/spec.go:183`, `resolved.go:46` (Test: `TestDropToleranceIsAdditiveToHash` —
      a node with no tolerance hashes identically to a pre-P16 node).

## 2. Backend — Context materialization, Go engine (16a, the hard part)

- [x] 2.1 Document the message-assembly rewrite contract: what call-site region is rewritten, and why a
      policy is a *region* rewrite and not an argument swap. → `design.md` Decision 2 + Interfaces sketch.
- [x] 2.2 Replace `refuseContext` for the Go engine with a real materialization: rewrite the call site's
      message-assembly region so the resolved policy governs the message list, deterministically given
      policy + params + conversation + seed. → `internal/transform/rewrite.go:409` `rewriteContext`
      (Test: `TestGoContextMaterializes` — a `sliding-window` override on a Go node emits an edit, not a
      refusal).
- [x] 2.3 Materialize inline as **code** (P10 apply-mode): the resolved assembly appears in the diff; no
      apply mode hides it. → `internal/transform/engine.go` context dispatch (Test:
      `TestContextChangeAppearsInDiff`).
- [x] 2.4 🔴 Guarantee **no silent drop**: a node whose resolved `ContextPolicy` differs from its
      discovered assembly yields an edit or a typed `unsafeRewrite`; it is never resolved-and-discarded.
      → `internal/transform/rewrite.go` (Test: `TestContextOverrideNeverSilentlyDropped` — asserts the
      override is applied OR refused, never absent-with-base-hash).
- [x] 2.5 Make the Go materialization **byte-identical** across runs for LLM-free policies
      (`sliding-window`, `semantic-compaction`, augmentation). → `internal/transform/rewrite.go` (Test:
      `TestGoContextMaterializationDeterministic` — same `config_hash` + `source_revision` + seed →
      identical diff).
- [x] 2.6 A host-calling policy (`summarization`) reaches its model only through `HostServices`,
      host-side, and captures its `ResolvedRequest`; 🚫 no summarizer call from a sandboxed node. →
      `internal/registry/context_policies.go:188` (existing) + transform wiring (Test:
      `TestSummarizerRunsHostSideOnly`).

## 3. Backend — The specified interim refusal, per language (16a)

- [x] 3.1 Specify the interim refusal as a requirement: a node carrying an un-applicable `ContextPolicy`
      SHALL be refused at transform, naming node + policy + reason, per language until its rewriter
      lands. → spec `context-policy` "SHALL be refused at transform".
- [x] 3.2 Correct `refuseContext`'s reason text: name the owning phase (P16), not P3, and state the
      rewrite is landing rather than deferred-forever. → `internal/transform/rewrite.go:417`
      `refuseContext` (Test: `TestRefusalNamesOwningPhase`).
- [x] 3.3 Keep the tree-sitter span engine's context refusal in place, **tested**, until its rewriter
      lands: an override on an unbuilt language refuses loudly and names itself. →
      `internal/transform/rewrite_span.go:59` dispatch (Test: `TestSpanEngineContextRefusesNotDrops`).
- [x] 3.4 🔴 Assert the refusal is not a silent no-op: an override on an unbuilt language produces a
      typed error, and the variant does **not** transform as its base config. →
      `internal/transform/rewrite_span.go` (Test: `TestInterimRefusalIsLoudNotSilent`).

## 4. Backend + System Designer — New policies behind the interface (16a)

- [x] 4.1 Specify `hierarchical-summary` and `structured-extraction` as additions behind the `Policy`
      interface, decided by warrant not by default. → spec `context-policy` "added via the Policy
      interface"; PRD §14 Q4 (whether `structured-extraction` is lossy).
- [x] 4.2 Implement `hierarchical-summary` (`Name` / `ParamsSchema` / `Assemble`) and register it via
      `Store.AddPolicy`; no schema change, no `Dimension` change. →
      `internal/registry/context_policies.go` + `internal/registry/store.go:41` (Test:
      `TestHierarchicalSummaryPolicyAddedNoSchemaChange`).
- [x] 4.3 Implement `structured-extraction` behind the interface with its `Lossy` flag set per the Q4
      decision; drop is measured, not assumed. → `internal/registry/context_policies.go` (Test:
      `TestStructuredExtractionDropMeasured`).

## 5. AI Engineer — Drop-loss as a scored, gated signal (16a)

- [x] 5.1 Specify `DropRatio` as a scored quality signal, with a measured `0.0` distinguished from an
      unmeasured lossless policy (the `Lossy` flag). → spec `context-policy` "modeled as a scored quality
      signal"; PRD FR7.
- [x] 5.2 Record a materialized lossy policy's observed drop per node per run via the existing
      `context_drop_ratio` telemetry; no new metric family. →
      `internal/telemetry/context_assembly.go:78` (existing emit) + eval wiring (Test:
      `TestMaterializedDropRecordedAsSignal`).
- [x] 5.3 🔴 Implement the **drop-tolerance admissibility gate**: a proposal whose resolved policy would
      drive a node's drop ratio past its tolerance is **inadmissible** — rejected before transform and
      before eval spend. → `internal/proposal/catalog.go` admissibility + `internal/proposal/gain.go`
      (Test: `TestProposalPastDropToleranceInadmissible`).
- [x] 5.4 Show a context reduction as lower `eval_tokens_total` at non-regressing `task_success` through
      the axis-agnostic harness; 🚫 no context-specific scorer. → consumed from
      `internal/evalharness/metricnames.go:27` (Test: `TestContextReductionLowersEvalTokensNoRegression`).

## 6. AI Engineer + Backend — Retrieval tuning, verified on held-out data (16b)

- [x] 6.1 Specify `OpRAGTune` proposals — top-k, chunk size, rerank on/off, embedding model — admissible
      only on a `RetrievalRAG` node. → spec `retrieval-tuning`; PRD FR10; existing gate
      [`catalog.go:214-216`](../../../internal/proposal/catalog.go).
- [x] 6.2 Extend `OpRAGTune` to propose **chunk size** and **embedding-model** variants in addition to
      the existing top-k / retriever swaps. → `internal/proposal/catalog.go:218` `ragTuneOp.Propose`
      (Test: `TestRAGTuneProposesChunkAndEmbedding`).
- [x] 6.3 🔴 Verify a retrieval change on a **held-out** eval set disjoint from its tuning set; a win on
      the tuning set alone is **not** a verified delta. → verification wiring over P4 eval sets (Test:
      `TestRetrievalVerifiedOnHeldoutSet` — an overlapping split is refused).
- [x] 6.4 🔴 Pin the retriever, its params, and the seed per measurement run so the same `config_hash`
      issues the identical `ResolvedRequest`, including rerank. →
      `internal/registry/context_policies.go:245` (existing `ResolvedRequest`) + measurement pinning
      (Test: `TestRetrievalMeasurementDeterministic`).
- [x] 6.5 Apply the drop gate to retrieval: a tuning proposal that would push a node past its tolerance
      is inadmissible (a larger top-k / lossy rerank can shrink the retained conversation). →
      `internal/proposal/catalog.go` (Test: `TestRetrievalTunePastDropToleranceInadmissible`).
- [x] 6.6 Record pure augmentation as retrieval, not loss: `DropRatio` 0 with a positive
      `RetrievedChunks`. → `internal/registry/context_policies.go:281-288` (existing) (Test:
      `TestAugmentationIsNotDrop`).

## 7. QA — Acceptance gate (16a + 16b)

- [x] 7.1 🔴 **No-silent-drop suite**: a differing context policy yields an edit or a typed refusal; a
      test asserts the override is never applied as the base config. → (Test:
      `TestContextOverrideNeverSilentlyDropped`, `TestInterimRefusalIsLoudNotSilent`).
- [x] 7.2 Refusal suite: an override on an unbuilt language refuses with node + policy named, and the
      owner text is accurate. → (Test: `TestSpanEngineContextRefusesNotDrops`, `TestRefusalNamesOwningPhase`).
- [x] 7.3 Determinism suite: LLM-free materialization byte-identical; summarization identical
      `ResolvedRequest`. → (Test: `TestGoContextMaterializationDeterministic`, `TestSummarizerRunsHostSideOnly`).
- [x] 7.4 🔴 Drop-gate suite: a proposal past a node's tolerance is inadmissible, before transform. →
      (Test: `TestProposalPastDropToleranceInadmissible`, `TestRetrievalTunePastDropToleranceInadmissible`).
- [x] 7.5 🔴 Held-out suite: the verified set is disjoint from the tuned set; an overlap is refused. →
      (Test: `TestRetrievalVerifiedOnHeldoutSet`).
- [x] 7.6 Eval-agnosticism suite: a context reduction lowers `eval_tokens_total` at non-regressing
      `task_success` with no scorer change. → (Test: `TestContextReductionLowersEvalTokensNoRegression`).
- [x] 7.7 Additivity suite: a node with no drop tolerance hashes byte-identically to pre-P16; no new
      `Dimension`. → (Test: `TestDropToleranceIsAdditiveToHash`).

## 8. Wave 16c — user-initiated change on this axis (`context-authoring`)

> **Depends on P13's `authored-change` contract landing first.** Everything shared — one spine two
> origins, `Origin` recorded never hashed, origin-blind refusals with **no override**, `unverified` never
> a claim and never auto-merged, named conflicts, byte-exact reversal, append-only audit, entitlement,
> offline CLI parity, no new egress, and *the user does not author the evidence* — is inherited, **not**
> re-implemented here.
>
> 🔴 **This axis's authoring rules are about loss, not permission.** The decisive one: the drop gate
> **never refuses on ignorance** — an unmeasured drop ratio returns `not-yet-measurable`, never
> `admissible` and never `refused`.

**System Designer + Backend**

- [x] 8.1 Write the `context-authoring` spec delta, referencing `authored-change` rather than restating
      it. → [`specs/context-authoring/spec.md`](specs/context-authoring/spec.md).
- [x] 8.2 Record Decision 8 (loss-governed authoring; the third verdict; the classifier is not
      user-settable) with the three rejected alternatives. → [`design.md`](design.md).
- [x] 8.3 🔴 **Run the drop-tolerance gate at preflight**, before any eval spend, reusing the same gate
      the proposal path uses — not a second predicate. → `internal/authoring/context.go`
      (Test: `TestContextPreflightUsesTheSameDropGate`, `TestDropGateRefusalCostsNoEvalSpend`).
- [x] 8.4 🔴🔴 **Never refuse on ignorance, never pass on ignorance.** An unmeasured drop ratio returns
      `not-yet-measurable` naming the missing measurement. → `internal/authoring/context.go`
      (Test: `TestUnmeasuredDropRatioIsThirdVerdict`, `TestUnmeasuredNeverReturnsAdmissible`,
      `TestUnmeasuredNeverReturnsRefused`).
- [x] 8.5 Refuse an authored context change on a language with no landed rewriter, naming the **node, the
      policy, and the language**, with the transform's own typed cause. → `internal/authoring/context.go`
      (Test: `TestContextAuthoringLanguageRefusalNamesAllThree`).
- [x] 8.6 Let a user declare or clear a node's **drop tolerance**; declaring re-hashes and clearing
      reproduces the pre-declaration hash **byte-identically**. → `internal/authoring/context.go`
      (Test: `TestDropToleranceDeclareAndClearIsByteExact`).
- [x] 8.7 Report — not silently accept — a declared tolerance the node's **current** policy already
      exceeds, naming the policy and the measured ratio. → `internal/authoring/context.go`
      (Test: `TestDeclaredToleranceAlreadyExceededIsReported`).
- [x] 8.8 🔴 **The classifier label is not user-settable.** Retrieval parameters are offered and accepted
      only on a `RetrievalRAG`-labelled node; no surface, flag, role, or request parameter sets the label.
      → `internal/authoring/context.go` (Test: `TestRetrievalParamsRefusedOnNonRAGNode`,
      `TestNoPathSetsClassifierLabel`).
- [x] 8.9 Offer only **registered** policies; free text is not a selection path. →
      `internal/authoring/context.go` (Test: `TestOnlyRegisteredPoliciesOffered`).
- [x] 8.10 An authored retrieval verification **pins** retriever + params + seed, and its held-out set is
      platform-derived and **disjoint from the cases shown as motivation**. →
      `internal/proposal/`, `internal/authoring/` (Test: `TestAuthoredRetrievalRunIsPinned`,
      `TestAuthoredRetrievalHeldOutDisjointFromShownMotivation`).

**Frontend**

- [x] 8.11 🔴 Render the drop gate's **three** verdicts as three states — admissible, refused (node +
      tolerance + measured ratio), not-yet-measurable (missing measurement). 🚫 Never collapse
      `not-yet-measurable` into a disabled control. → `web/console/src/app/app/context/`
      (Test: `tests/context-authoring.test.mjs` — "8.11 the drop gate's three verdicts render as three distinct states", "8.11 the over-tolerance refusal shows both numbers").
- [x] 8.12 🔴 Display `DropRatio` as **information discarded**, never as a token or cost saving; the
      hazard palette stays reserved for hazard. → `web/console/src/app/app/context/`
      (Test: `tests/context-authoring.test.mjs` — "8.12 the drop ratio is described as information discarded, never as a saving", "8.12 the gate is described as a measurement, not a guarantee").
- [x] 8.13 On a node whose language has no landed rewriter, state the boundary with the language named —
      not a silently disabled control. → `web/console/` (Test: `tests/context-authoring.test.mjs` — "8.13 a node whose language has no rewriter states the boundary with the language named").
- [x] 8.14 Retrieval parameters appear only on classifier-labelled retrieval nodes, with the reason stated
      when absent; no control offers to change the label. → `web/console/`
      (Test: `tests/context-authoring.test.mjs` — "8.14 retrieval parameters are gated by the classifier, with the reason stated").
- [x] 8.15 🔴 Adding authoring controls removes **no** existing capability from the context surface;
      design-system tokens only. → `web/console/` (Test: `tests/context-authoring.test.mjs` — "8.15 adding authoring removed no existing capability from the context surface", "8.15 the context authoring surface derives nothing",
      `npm run scan:tokens`).

**DevOps + QA**

- [x] 8.16 CLI parity: select a policy, set params, declare a tolerance, and tune retrieval offline, with
      the same typed cause and the same three verdicts. → `internal/cli/`
      (Test: `TestCLIContextAuthoringOfflineParity`).
- [x] 8.17 🔴 Every verdict class goes **red**: over-tolerance refusal, unmeasured third verdict,
      unsupported-language refusal, non-RAG retrieval refusal, unregistered policy. →
      (Test: `TestContextAuthoringVerdictsGoRed`).
- [x] 8.18 🔴 No flag, role, plan, or entitlement lets an authored context change bypass the drop gate,
      the language refusal, or the classifier gate. → (Test: `TestNoOverrideOnContextAuthoring`).
- [x] 8.19 An unverified authored context change is attributed **no** token or cost saving and is absent
      from the verified-delta ledger. → (Test: `TestUnverifiedContextChangeClaimsNothing`).
- [x] 8.20 Pure augmentation records `DropRatio` 0 with a positive retrieved-chunk count — an authored
      retrieval add is not reported as loss. → (Test: `TestAuthoredAugmentationIsNotLoss`).
- [x] 8.21 Assert downstream: after an authored policy change, read back the emitted diff, the
      append-only record, and the **resolved policy frozen into the node** — a 2xx is not evidence. →
      (Test: `TestAuthoredContextChangeAssertsDownstreamState`).

**Product Designer + Sales Operations**

- [x] 8.22 Specify the wording for the three verdicts, and especially that *"we have not measured this
      yet"* reads as a fact about the platform, with what would make it measurable — not as a refusal. →
      [`specs/context-authoring/spec.md`](specs/context-authoring/spec.md).
- [x] 8.23 Specify that drop ratio is described as information discarded, and that a smaller context is
      never described as cheaper until the harness has ruled. →
      [`specs/context-authoring/spec.md`](specs/context-authoring/spec.md).
- [x] 8.24 State the claim and its boundary: users choose their own context strategy and the platform
      shows what each one discards; 🚫 the drop gate is a **measured** check, not a guarantee, and where
      it has not measured it says so; there is no override, and a user cannot relabel a node to unlock
      retrieval tuning. → PRD §9 Sales lens.

## 9. Documentation

- [x] 9.1 Author the P16 PRD (14 sections). → `docs/prd/P16-context-strategy-optimization.md`.
- [x] 9.2 Author the change proposal, this task plan, and the design record. → `proposal.md`,
      `tasks.md`, `design.md`.
- [x] 9.3 Author the three capability spec deltas. →
      `specs/context-policy/spec.md`, `specs/retrieval-tuning/spec.md`, `specs/context-authoring/spec.md`.
- [x] 9.4 Record the one-way-door contracts (no new `Dimension`; additive drop-tolerance). →
      `decisions.md`.
- [x] 9.5 Fold the capability specs into `openspec/specs/`. → `openspec/specs/{context-policy,
      retrieval-tuning,context-authoring,context-language-coverage}/spec.md` — cross-references rewritten
      to the folded depth and verified to resolve.

---

## 10. Wave 16d — all-language coverage on this axis (`context-language-coverage`)

> **Depends on P13's `language-coverage` contract landing first.** Everything shared — totality over the
> registered language set, per-cell claims, the three typed refusal classes and their specific-first
> order, one coverage source, executable evidence per row, no gate weakened to reach a language, the
> versioned offline table, and coverage no plan can move — is inherited, **not** re-implemented here.

**Standing constraints for 16d.** 🔴 **Retention is not per language** — which turns a policy retains *is*
the policy, decided by the shared selection code in every language; a splitter answers only "what are the
written elements of this list". The **drop record** is produced by the same shared path and is
byte-comparable across languages. A refusal reports the **most specific true** cause: the policy → the
registry row → the call site's own source → **the language last**. A run-time-produced policy refuses
identically in **every** language, before and after any splitter lands.

**System Designer + AI Engineer**

- [x] 10.1 Write the `context-language-coverage` spec delta, referencing `language-coverage` rather than
      restating it. → [`specs/context-language-coverage/spec.md`](specs/context-language-coverage/spec.md).
- [x] 10.2 Record **D-4** (the splitter is the only per-language part; retention and the drop record stay
      shared) and **Decision 9** (every (language, policy) pair gets a value; the policy question is asked
      first). → [`decisions.md`](decisions.md) D-4, [`design.md`](design.md) Decision 9.
- [x] 10.3 Make `ContextMaterializerCoverage()` total over **(registered language × declared policy)**,
      with a language gap naming the list splitter. → `internal/transform/coverage.go`, `contextmaterialize_span.go`
      (Test: `TestCoverageIsTotalOverRegisteredLanguages`, `TestCoverageListsEveryMaterializingLanguage`).
- [x] 10.4 🔴 Totality is generated from `discovery.DefaultFrontends` and `contextForms`; adding either a
      frontend or a policy with no entry goes red. → (Test: `TestCoverageIsTotalOverRegisteredLanguages` — the language set is read from `discovery.DefaultFrontends`).

**Backend**

- [x] 10.5 Add list splitters for typescript / javascript. → `internal/discovery/listsplit.go` (the shared splitter `spanContextMaterializers` derives from)
      (Test: `TestTypeScriptSelectionMaterializes`, `TestSplitWrittenListAcrossLanguages`).
- [x] 10.6 Add list splitters for kotlin / java / rust. → `internal/discovery/listsplit.go` `listSyntaxes`
      (Test: `TestSplitWrittenListAcrossLanguages`, `TestSplitWrittenListRefusesUnprovableBoundaries`).
- [x] 10.7 🚫 **No splitter decides retention.** A structural test asserts every language routes through
      the shared `SelectionPolicy.Retain`. → (Test: `TestRetentionIsSharedNotPerLanguage` — asserted both
      from the splitter's signature, which is handed no policy, and behaviourally across two languages).
- [x] 10.8 🔴 A list mixing written elements with a spread (`*history`, `...history`) makes the list
      unselectable and refuses under `call-site-cannot-carry-it` **naming the spread** — never a partial
      selection. → `internal/discovery/listsplit.go` (Test: `TestSpreadMakesTheListUnselectable`).
- [x] 10.9 Keep the refusal order: policy → row → source → language, in every engine. →
      (Test: `TestSpanEngineContextRefusesNotDrops`, `TestKwargsSiteIsToldAboutTheKwargsNotTheRewriter`).

**AI Engineer + QA**

- [x] 10.10 🔴 **The drop record travels**: a selection in a newly covered language produces a record
      byte-comparable with an existing language's, and no path emits the deletion without it. →
      (Test: `TestDropRecordIsUnskippableInEveryLanguage`).
- [x] 10.11 🔴 A run-time-produced policy refuses with the **same cause** in a language with a splitter and
      one without. → (Test: `TestSpanEngineContextRefusesNotDrops` — asserts the `not-expressible-at-a-call-site` cause).
- [x] 10.12 🔴 A call site that wrote **no message list** reports **that** in a language with no splitter,
      and refuses identically after the splitter lands; reversing the order goes red. →
      (Test: `TestCallSiteCauseBeatsLanguageCause`, `TestCallSiteRefusalIsUnchangedByCoverage`).
- [x] 10.13 Each splitter row carries a fixture that emits a selection and asserts the reparse; a row
      without one is rejected. → (Test: `TestEverySplitterRowHasAProof`).
- [x] 10.14 Assert against the **downstream consumer**: after a selection in a newly covered language,
      read back the emitted diff, the reparse, the recorded drop, and the coverage cell. →
      (Test: `TestNewLanguageSelectionAssertsDownstreamState`).

**Frontend + DevOps**

- [x] 10.15 Render "no language can materialize this policy" and "this language cannot select yet" as two
      distinct states, from the shared source, before a policy is chosen. →
      `web/console/src/app/app/context/page.tsx` ("Two different reasons a policy is declined")
      (Test: `context.test.mjs`, `coverage.test.mjs`; browser-verified).
- [x] 10.16 Carry the (language, policy) cells in the CLI's versioned offline table; a refusal names the
      version and the cause text matches the hosted surface. → `internal/cli/coverage.go`
      (Test: `TestCoverageIsOfflineAndVersioned`).

**Product Designer + Sales Operations**

- [x] 10.17 Specify the two wordings: a missing splitter is **not yet applied by the platform**; a
      run-time-produced policy is **not expressible in source**, with no "when". →
      `specs/context-language-coverage/spec.md`.
- [x] 10.18 State the claim per cell: 🚫 never "we optimize context in any language" — that promises
      summarization materialization which refuses in **every** language, including Go. → PRD §9.2 Sales lens.

## 11. Wave 16e — delivery cells on this axis (`context-delivery`)

> Cross-axis rules come from **P13's `change-delivery`** and
> [ADR-010](../../../docs/adr/ADR-010-runtime-gradual-rollout.md); they are referenced, never restated.

**System Designer**

- [x] 11.1 🔴 **The axis splits, and not where a reader expects.** A retrieval parameter is a number →
      `noRolloutBinding` with a named missing field, ours to fix. A selection policy is a **deletion** of
      written turns → `notRuntimeResolvable`, permanent. Specify them as separate cells whose causes are
      not inferred from each other. → `specs/context-delivery/spec.md`
      (Test: `TestRetrievalAndPolicyCarryDifferentCauses`).

**Backend**

- [x] 11.2 🔴 **The drop record survives the second route.** A candidate-arm context decision produces a
      drop record through the **same unskippable path** as a parent-arm one, byte-comparable in shape,
      and no arm, route, or apply mode bypasses the recording. A second way for a context decision to
      take effect is exactly where an unskippable guarantee becomes skippable. →
      (Test: `TestBothArmsRecordTheDrop`, `TestNoPathTakesEffectWithoutARecord`).
- [x] 11.3 The drop-tolerance gate runs **before** rollout authoring and 🚫 still never refuses on
      ignorance — an unknown tolerance is recorded and carried, not treated as a rejection. →
      (Test: `TestDropToleranceGatesRolloutAuthoring`, `TestUnknownToleranceDoesNotBlockAuthoring`).
- [x] 11.4 A retrieval change whose held-out verdict was refused for an **overlapping split** cannot be a
      rollout candidate; the refusal names the overlap, not the delivery route. →
      (Test: `TestOverlappingSplitBlocksRolloutAuthoring`).

**Frontend + Product Designer**

- [x] 11.5 Render the two context cells separately, and the retrieval cell as one that can gain a row
      rather than a structural impossibility. → `web/console/src/app/app/context/`
      (Test: `context.test.mjs`, `delivery.test.mjs`).
- [x] 11.6 State the claim per cell: 🚫 never "we tune your context live" — retention refuses the runtime
      route in **every** language. → PRD §9.2 Sales lens.
