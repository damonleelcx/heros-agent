# Tasks — P13: Prompt & Model Optimization

Two waves. **Wave 13a** = deeper prompt operators (`prompt-rewrite`), complete on its own. **Wave 13b**
= model selection under a quality guardrail (`model-selection`), which is what [P6](../p6-autonomous-optimizer/)'s
loop consumes.

**This round ships specs, not code.** Documentation tasks are checked `[x]`; implementation tasks are
left `[ ]` and each ends with an **evidence pointer** → the file that will carry it and the test that
proves it.

**Standing constraints.** *Diagnosis proposes, verification decides* — every operator emits only
candidates. Every rewrite publishes a **new content-addressed prompt version** (P10 immutability); a
rewrite that un-applies a node is **refused**, not dropped. A downgrade is admissible **only** under the
held-out CI-overlap guardrail. Effects land **only** in existing `ResolvedNode` fields
(`PromptRef`/`ModelRef`/`ProviderParams`) so `config_hash` participation is automatic — **no** new
`Dimension`, `Kind`, table, oracle, or metric. Cost is `eval_cost_usd` and plan **names** only.

---

## 1. AI Engineer + System Designer — Fix the operator contracts before any code (13a)

- [x] 1.1 Author the PRD and this change set; enumerate the four new prompt operators and the model
      guardrail, each with EXISTS/PARTIAL/ABSENT honesty against the spine. →
      `docs/prd/P13-prompt-model-optimization.md`, `proposal.md`, `design.md`.
- [x] 1.2 Write the `prompt-rewrite` and `model-selection` spec deltas (SHALL + ≥1 scenario each,
      NFRs as first-class requirements). → `specs/prompt-rewrite/spec.md`, `specs/model-selection/spec.md`.
- [x] 1.3 Confirm **no one-way-door contract** is opened (no new `Dimension`/`Kind`/table), so this
      change ships **no** `decisions.md`. Record the reasoning in `design.md` Decision 8. → `design.md`.
- [ ] 1.4 Ratify the **held-out split derivation** (deterministic from `config_hash` + case ids) and the
      **minimum-held-out** floor below which the guardrail returns `inadmissible-insufficient-data`
      rather than a false tie. → `internal/proposal/guardrail.go` `HeldOutSplit`
      (Test: `TestHeldOutSplitIsDeterministic`, `TestGuardrailInsufficientDataIsThirdVerdict`).

## 2. AI Engineer + Backend — Deeper prompt operators (13a)

- [ ] 2.1 Add `instruction_harden` as a catalog row handling under-specification, declining when
      ungrounded. → `internal/proposal/catalog.go` `instructionHardenOp` in `DefaultCatalog()`
      (Test: `TestInstructionHardenOnlyOnGroundedUnderspec`).
- [ ] 2.2 Add `few_shot_curate` as a catalog row (remove/reorder dead exemplars), grounded-or-silent. →
      `internal/proposal/catalog.go` `fewShotCurateOp` (Test: `TestFewShotCurateGroundedOrSilent`).
- [ ] 2.3 Add `prompt_compress` (token-reduction) as a catalog row, competing on the full metric family
      with no token target as a goal. → `internal/proposal/catalog.go` `promptCompressOp`
      (Test: `TestCompressCompetesOnMetricsNotTokenTarget`).
- [ ] 2.4 Add `redundancy_remove` as a catalog row. → `internal/proposal/catalog.go` `redundancyRemoveOp`
      (Test: `TestRedundancyRemoveGrounded`).
- [ ] 2.5 Route every new operator's rewrite through P10's **content-addressed publish** so each emits a
      new `PromptRef` and never mutates a version. → `internal/proposal/catalog.go` reuses
      `syntheticPromptRef` / registry publish (Test: `TestRewritePublishesNewImmutableVersion`).
- [ ] 2.6 🔴 **Refuse a rewrite that un-applies a node.** A slot-set change that leaves a call-site value
      unbound is refused at resolve with the slot **named**, via P10 impact analysis — never a silent
      drop. → `internal/variantspec/resolve.go` binding check (Test: `TestCompressionUnApplyIsRefusedNamingSlot`).
- [ ] 2.7 Attach each candidate's **grounding** (the cases addressed) so verification and review see the
      change's purpose. → `internal/proposal/catalog.go` `Candidate.Grounding`
      (Test: `TestPromptCandidateCarriesGrounding`).
- [ ] 2.8 Add a `gain.go` **prior** for each new prompt operator (ordering only, never a result). →
      `internal/proposal/gain.go` `operatorPrior` (Test: `TestNewPromptOperatorsHavePriors`).
- [ ] 2.9 🚫 **Never apply a candidate directly.** Assert every new prompt operator's output reaches a
      diff only through the P5.5 gate. → `internal/proposal/*` (Test: `TestPromptCandidateNeverAppliedWithoutVerification`).

## 3. AI Engineer — Model selection under guardrail (13b)

- [ ] 3.1 Implement the **downgrade guardrail**: a cheaper model is admissible only when its
      `task_success` CI overlaps the incumbent's on **held-out** cases (reuse `evalstats.Compare`
      overlap). → `internal/proposal/guardrail.go` (Test: `TestDowngradeInadmissibleWhenCINoOverlap`).
- [ ] 3.2 🔴 **Held-out isolation.** The guardrail's cases are **disjoint** from the operator's
      motivating cases. → `internal/proposal/guardrail.go` (Test: `TestGuardrailCasesDisjointFromMotivating`).
- [ ] 3.3 Make an admitted downgrade an **equal-quality-cheaper tie**: reported as a cost win and a
      quality tie, never a quality win. → `internal/proposal/catalog.go` `modelDowngradeOp` + verdict
      (Test: `TestDowngradeTieIsCostWinNotQualityWin`).
- [ ] 3.4 Wire the guardrail as an **admissibility** predicate on `modelDowngradeOp`, leaving
      `modelUpgradeOp`/`enableThinkingOp` admissibility unchanged. → `internal/proposal/catalog.go`
      (Test: `TestUpgradeAdmissibilityUnchanged`).
- [ ] 3.5 Implement **parameter tuning** (temperature/max-tokens) via `ProviderParams`, materialized in
      **bound** mode (ADR-004). → `internal/proposal/catalog.go` `paramTuneOp`, `internal/transform/boundmode.go`
      (Test: `TestParamTuneMaterializesInBoundMode`).
- [ ] 3.6 🔴 **Refuse an un-materializable inline param override** with a named cause — never dropped. →
      `internal/transform/rewrite.go` (Test: `TestInlineParamOverrideRefusedNotDropped`).
- [ ] 3.7 🔴 **Refuse a cross-provider swap at a user call site** (ADR-002), producing no diff; keep
      intra-provider swaps applying. → `internal/transform/rewrite.go:81` `rewriteModel`
      (Test: `TestCrossProviderSwapRefusedNoDiff`, `TestIntraProviderSwapApplies`).
- [ ] 3.8 Add a `gain.go` prior for `paramTuneOp`; downgrade/upgrade priors already exist. →
      `internal/proposal/gain.go` (Test: `TestParamTuneHasPrior`).

## 4. Backend + System Designer — Contract preservation (both waves)

- [ ] 4.1 Assert a P13 candidate's only hashed effect is `PromptRef`/`ModelRef`/`ProviderParams`; P0
      golden vectors reproduce bit-for-bit. → `internal/confighash/*_golden_test.go` unchanged
      (Test: `TestGoldenVectorsStillReproduce`).
- [ ] 4.2 🚫 **No new hashed field, `Dimension`, `Kind`, table, oracle, or metric.** Assert the
      `Dimension` enum and registry `Kind` set are unchanged. → structural test
      (Test: `TestNoNewDimensionOrKind`).
- [ ] 4.3 Assert the eval harness still consumes only `config_hash` + `Trace` — no operator label
      reaches it. → `internal/evalharness/*` unchanged (Test: `TestEvalRemainsAxisAgnostic`).

## 5. QA — Acceptance gate (both waves)

- [ ] 5.1 Ungrounded-or-silent: each new prompt operator yields **zero** candidates on an ungrounded
      request. → (Test: `TestUngroundedYieldsZeroCandidates`).
- [ ] 5.2 Immutability: a rewrite creates a new `version_id`; the parent stays resolvable. →
      (Test: `TestRewriteCreatesNewVersionParentIntact`).
- [ ] 5.3 Un-apply refusal goes **red**: a compression dropping a live `{{slot}}` is refused naming the
      slot. → (Test: `TestUnApplyRefusalGoesRed`).
- [ ] 5.4 Guardrail goes **red**: a non-overlapping downgrade is inadmissible even with lower
      `eval_cost_usd`. → (Test: `TestNonOverlappingDowngradeInadmissible`).
- [ ] 5.5 Statistical honesty: every verdict carries a CI; CI-overlapping candidates report tied; no
      single-seed decision. → (Test: `TestP13VerdictsAreMultiSeedWithTies`).
- [ ] 5.6 A shorter-but-worse prompt **fails** (FR8 can go red). → (Test: `TestShorterWorsePromptIsNotAWin`).

## 6. Product Designer — The offered change (both waves)

- [x] 6.1 Specify that a prompt proposal is presented as a **reviewable diff of a new version** with its
      grounding attached, and a downgrade as an **equal-quality-cheaper tie judged on held-out cases** —
      written into the specs even though no editor surface is added. → `specs/prompt-rewrite/spec.md`,
      `specs/model-selection/spec.md`.
- [x] 6.2 Specify the unhappy path: an un-applying rewrite names **which slot** stopped binding,
      in-editor (P10 impact analysis), never at codemod time. → `specs/prompt-rewrite/spec.md`.

## 7. Sales Operations — Claims and the refused boundary (13b)

- [x] 7.1 State the verifiable claim: prompt/model changes are modeled **and applied**, and every shipped
      change passed a multi-seed gate; every downgrade cleared a held-out guardrail. → `design.md` §9 lens
      (folded from PRD §9). 🚫 Never present an LLM suggestion as a result — verification decides.
- [x] 7.2 State the **refused** boundary: "model selection" does **not** include cross-provider routing
      at customer call sites (ADR-002). Cost by plan **name** and `eval_cost_usd` only — no price. →
      PRD §9 Sales lens, `specs/model-selection/spec.md`.

## 8. Documentation

- [x] 8.1 Cross-reference the PRD from both spec deltas and the design. → done in each file header.
- [x] 8.2 On merge, fold the two P13 capability specs into `openspec/specs/`. → (folding step, at deploy).
