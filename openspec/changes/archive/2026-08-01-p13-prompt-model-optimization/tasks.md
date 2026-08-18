# Tasks — P13: Prompt & Model Optimization

Four waves. **Wave 13a** = deeper prompt operators (`prompt-rewrite`), complete on its own. **Wave 13b**
= model selection under a quality guardrail (`model-selection`), which is what [P6](../p6-autonomous-optimizer/)'s
loop consumes. **Wave 13c** = user-initiated change: the cross-axis `authored-change` contract plus
`prompt-model-authoring`, independently revertible and a prerequisite for P14/P15/P16's authoring waves.
**Wave 13d** = language coverage: the cross-axis `language-coverage` contract plus
`prompt-model-language-coverage`, independent of 13c, likewise independently revertible, and a
prerequisite for P14/P15/P16's coverage waves (§17–§22).

**This round ships specs, not code.** Documentation tasks are checked `[x]`; implementation tasks are
left `[ ]` and each ends with an **evidence pointer** → the file that will carry it and the test that
proves it.

**Standing constraints.** *Diagnosis proposes, verification decides* — every operator emits only
candidates. Every rewrite publishes a **new content-addressed prompt version** (P10 immutability); a
rewrite that un-applies a node is **refused**, not dropped. A downgrade is admissible **only** under the
held-out CI-overlap guardrail. Effects land **only** in existing `ResolvedNode` fields
(`PromptRef`/`ModelRef`/`ProviderParams`) so `config_hash` participation is automatic — **no** new
`Dimension`, `Kind`, table, oracle, or metric. Cost is `eval_cost_usd` and plan **names** only.

**Standing constraints for 13c.** One spine, two origins — **no** authoring-only resolve/transform/gate.
`Origin` is recorded, **never** hashed. Every refusal that binds an operator binds a user identically, and
**no flag, role, plan, or parameter overrides one**. Refusal moves left: **preflight** names the cause
before submission and spends nothing. An authored change may apply **unverified**, but `unverified` never
enters the ledger, never counts in an aggregate, and never auto-merges. **A user may author the change; a
user may not author the evidence.**

**Standing constraints for 13d.** Coverage is a **total** function over (axis × registered language ×
form) — **absence is not a value**. Every refusal carries one of **three** stable cause identifiers, and
the **most specific true** one is reported: the change → the row → the call site's source → **the language
last**. Every gap **names its missing artifact**. **One** coverage source is read by transform, preflight,
console, CLI and every document, asserted in **both** directions. A row is admitted only on **executable
evidence**, and **no gate is relaxed to reach a language**. The same override **means the same thing** in
every language. Coverage is **identical on every plan**.

---

## 1. AI Engineer + System Designer — Fix the operator contracts before any code (13a)

- [x] 1.1 Author the PRD and this change set; enumerate the four new prompt operators and the model
      guardrail, each with EXISTS/PARTIAL/ABSENT honesty against the spine. →
      `docs/prd/P13-prompt-model-optimization.md`, `proposal.md`, `design.md`.
- [x] 1.2 Write the `prompt-rewrite` and `model-selection` spec deltas (SHALL + ≥1 scenario each,
      NFRs as first-class requirements). → `specs/prompt-rewrite/spec.md`, `specs/model-selection/spec.md`.
- [x] 1.3 Confirm **no one-way-door contract** is opened (no new `Dimension`/`Kind`/table), so this
      change ships **no** `decisions.md`. Record the reasoning in `design.md` Decision 8. → `design.md`.
- [x] 1.4 Ratify the **held-out split derivation** (deterministic from `config_hash` + case ids) and the
      **minimum-held-out** floor below which the guardrail returns `inadmissible-insufficient-data`
      rather than a false tie. → `internal/proposal/guardrail.go` `HeldOutSplit`
      (Test: `TestHeldOutSplitIsDeterministic`, `TestGuardrailInsufficientDataIsThirdVerdict`).

## 2. AI Engineer + Backend — Deeper prompt operators (13a)

- [x] 2.1 Add `instruction_harden` as a catalog row handling under-specification, declining when
      ungrounded. → `internal/proposal/catalog.go` `instructionHardenOp` in `DefaultCatalog()`
      (Test: `TestInstructionHardenOnlyOnGroundedUnderspec`).
- [x] 2.2 Add `few_shot_curate` as a catalog row (remove/reorder dead exemplars), grounded-or-silent. →
      `internal/proposal/catalog.go` `fewShotCurateOp` (Test: `TestFewShotCurateGroundedOrSilent`).
- [x] 2.3 Add `prompt_compress` (token-reduction) as a catalog row, competing on the full metric family
      with no token target as a goal. → `internal/proposal/catalog.go` `promptCompressOp`
      (Test: `TestCompressCompetesOnMetricsNotTokenTarget`).
- [x] 2.4 Add `redundancy_remove` as a catalog row. → `internal/proposal/catalog.go` `redundancyRemoveOp`
      (Test: `TestRedundancyRemoveGrounded`).
- [x] 2.5 Route every new operator's rewrite through P10's **content-addressed publish** so each emits a
      new `PromptRef` and never mutates a version. → `internal/proposal/catalog.go` reuses
      `syntheticPromptRef` / registry publish (Test: `TestRewritePublishesNewImmutableVersion`).
- [x] 2.6 🔴 **Refuse a rewrite that un-applies a node.** A slot-set change that leaves a call-site value
      unbound is refused at resolve with the slot **named**, via P10 impact analysis — never a silent
      drop. → `internal/variantspec/resolve.go` binding check (Test: `TestCompressionUnApplyIsRefusedNamingSlot`).
- [x] 2.7 Attach each candidate's **grounding** (the cases addressed) so verification and review see the
      change's purpose. → `internal/proposal/catalog.go` `Candidate.Grounding`
      (Test: `TestPromptCandidateCarriesGrounding`).
- [x] 2.8 Add a `gain.go` **prior** for each new prompt operator (ordering only, never a result). →
      `internal/proposal/gain.go` `operatorPrior` (Test: `TestNewPromptOperatorsHavePriors`).
- [x] 2.9 🚫 **Never apply a candidate directly.** Assert every new prompt operator's output reaches a
      diff only through the P5.5 gate. → `internal/proposal/*` (Test: `TestPromptCandidateNeverAppliedWithoutVerification`).

## 3. AI Engineer — Model selection under guardrail (13b)

- [x] 3.1 Implement the **downgrade guardrail**: a cheaper model is admissible only when its
      `task_success` CI overlaps the incumbent's on **held-out** cases (reuse `evalstats.Compare`
      overlap). → `internal/proposal/guardrail.go` (Test: `TestDowngradeInadmissibleWhenCINoOverlap`).
- [x] 3.2 🔴 **Held-out isolation.** The guardrail's cases are **disjoint** from the operator's
      motivating cases. → `internal/proposal/guardrail.go` (Test: `TestGuardrailCasesDisjointFromMotivating`).
- [x] 3.3 Make an admitted downgrade an **equal-quality-cheaper tie**: reported as a cost win and a
      quality tie, never a quality win. → `internal/proposal/catalog.go` `modelDowngradeOp` + verdict
      (Test: `TestDowngradeTieIsCostWinNotQualityWin`).
- [x] 3.4 Wire the guardrail as an **admissibility** predicate on `modelDowngradeOp`, leaving
      `modelUpgradeOp`/`enableThinkingOp` admissibility unchanged. → `internal/proposal/catalog.go`
      (Test: `TestUpgradeAdmissibilityUnchanged`).
- [x] 3.5 Implement **parameter tuning** (temperature/max-tokens) via `ProviderParams`, materialized in
      **bound** mode (ADR-004). → `internal/proposal/catalog.go` `paramTuneOp`, `internal/transform/boundmode.go`
      (Test: `TestParamTuneMaterializesInBoundMode`).
- [x] 3.6 🔴 **Refuse an un-materializable inline param override** with a named cause — never dropped. →
      `internal/transform/rewrite.go` (Test: `TestInlineParamOverrideRefusedNotDropped`).
- [x] 3.7 🔴 **Refuse a cross-provider swap at a user call site** (ADR-002), producing no diff; keep
      intra-provider swaps applying. → `internal/transform/rewrite.go:81` `rewriteModel`
      (Test: `TestCrossProviderSwapRefusedNoDiff`, `TestIntraProviderSwapApplies`).
- [x] 3.8 Add a `gain.go` prior for `paramTuneOp`; downgrade/upgrade priors already exist. →
      `internal/proposal/gain.go` (Test: `TestParamTuneHasPrior`).

## 4. Backend + System Designer — Contract preservation (both waves)

- [x] 4.1 Assert a P13 candidate's only hashed effect is `PromptRef`/`ModelRef`/`ProviderParams`; P0
      golden vectors reproduce bit-for-bit. → `internal/confighash/*_golden_test.go` unchanged
      (Test: `TestGoldenVectorsStillReproduce`).
- [x] 4.2 🚫 **No new hashed field, `Dimension`, `Kind`, table, oracle, or metric.** Assert the
      `Dimension` enum and registry `Kind` set are unchanged. → structural test
      (Test: `TestNoNewDimensionOrKind`).
- [x] 4.3 Assert the eval harness still consumes only `config_hash` + `Trace` — no operator label
      reaches it. → `internal/evalharness/*` unchanged (Test: `TestEvalRemainsAxisAgnostic`).

## 5. QA — Acceptance gate (both waves)

- [x] 5.1 Ungrounded-or-silent: each new prompt operator yields **zero** candidates on an ungrounded
      request. → (Test: `TestUngroundedYieldsZeroCandidates`).
- [x] 5.2 Immutability: a rewrite creates a new `version_id`; the parent stays resolvable. →
      (Test: `TestRewriteCreatesNewVersionParentIntact`).
- [x] 5.3 Un-apply refusal goes **red**: a compression dropping a live `{{slot}}` is refused naming the
      slot. → (Test: `TestUnApplyRefusalGoesRed`).
- [x] 5.4 Guardrail goes **red**: a non-overlapping downgrade is inadmissible even with lower
      `eval_cost_usd`. → (Test: `TestNonOverlappingDowngradeInadmissible`).
- [x] 5.5 Statistical honesty: every verdict carries a CI; CI-overlapping candidates report tied; no
      single-seed decision. → (Test: `TestP13VerdictsAreMultiSeedWithTies`).
- [x] 5.6 A shorter-but-worse prompt **fails** (FR8 can go red). → (Test: `TestShorterWorsePromptIsNotAWin`).

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

## 8. System Designer — The authored-change contract, before any authoring code (13c)

- [x] 8.1 Author the cross-axis `authored-change` spec delta — one spine two origins, origin-blind
      refusals, preflight, unverified labeling, conflict/reversal/audit/offline/egress, and *a user may
      not author the evidence*. → `specs/authored-change/spec.md`.
- [x] 8.2 Author the `prompt-model-authoring` spec delta (model / params / prompt authoring within the
      existing refusal set). → `specs/prompt-model-authoring/spec.md`.
- [x] 8.3 Record Decisions 9–12 with the rejected alternatives (second apply path, expert override,
      block-until-verified, presumed-fine verdict) arbitrated by the eight-level ordering. → `design.md`.
- [x] 8.4 🔴 **Assert one spine.** A structural test enumerates transform entry points and asserts an
      authored change reaches a diff through the same one, bypassing no gate an operator must pass. →
      `internal/authoring/` (Test: `TestSingleApplyPathAcrossOrigins`).
- [x] 8.5 🚫 **`Origin` is not hashed.** An authored and an operator-proposed configuration that are
      byte-identical hash identically; P0 golden vectors reproduce. → `internal/confighash/`
      (Test: `TestOriginDoesNotAffectConfigHash`, `TestGoldenVectorsStillReproduce`).

## 9. Backend — Draft lifecycle, preflight, and the append-only record (13c)

- [x] 9.1 Add the draft model: `ParentVariantID`, per-node edits, actor/tenant, `ForkedFromProposal`,
      concurrency token. The parent is **never** mutated. → `internal/authoring/draft.go`
      (Test: `TestDraftNeverMutatesParent`).
- [x] 9.2 Implement **preflight**: resolve + gates + materializability probe returning
      `admissible` / `refused{cause,node,field}` / `not-yet-measurable{missing}`, publishing nothing,
      writing no diff, spending no eval budget. → `internal/authoring/preflight.go`
      (Test: `TestPreflightSpendsNothing`, `TestPreflightNamesCauseAndNode`).
- [x] 9.3 🔴 **Never refuse on ignorance.** An unmeasured admissibility input yields
      `not-yet-measurable`, never `admissible` and never `refused`. → `internal/authoring/preflight.go`
      (Test: `TestPreflightThirdVerdictOnUnknownInput`).
- [x] 9.4 🔴 **Stale submit is a named conflict.** Two drafts from one parent yield two variants; a draft
      whose parent advanced is refused by name, never overwriting. → `internal/authoring/submit.go`
      (Test: `TestConcurrentDraftsYieldTwoVariants`, `TestStaleDraftRefusedByName`).
- [x] 9.5 Implement **reversal**: re-derive from the recorded parent so the resulting `config_hash` is
      byte-identical to pre-edit; never an in-place restore. → `internal/authoring/revert.go`
      (Test: `TestRevertReproducesParentHashByteIdentical`).
- [x] 9.6 Record every submitted authored change **append-only** (actor, tenant, ts, parent, axis,
      `config_hash`, diff ref, origin, forked-from), following the P12 delivery-record posture. Schema,
      migration and code land **together**; the migration is idempotent and guarded by semantics, not by
      object name. → `internal/authoring/record.go`, `db/migrations/` (Test: `TestAuthoredRecordIsAppendOnly`).
- [x] 9.7 🚫 **No override.** Assert no flag, role, plan, entitlement, or request parameter suppresses a
      refusal; the refusal set is enumerated, not sampled. → `internal/authoring/`
      (Test: `TestNoOverrideSuppressesAnyRefusal`).
- [x] 9.8 Add preflight / submit / revert routes behind the entitlement + permission check, returning the
      typed cause verbatim. A 403, a 404 and a transport failure stay three distinguishable outcomes. →
      `internal/api/p13authoring.go` (Test: `TestFailureClassesDistinguishable` — 402 not-entitled, 403 not-permitted,
      401 no-principal, 409 stale, 422 inadmissible, 502 record-unreachable and 503 not-mounted are each
      asserted distinct, so no two classes collapse; `TestAuthoringActorComesFromTheSession`).

## 10. AI Engineer — Authoring meets the guardrail (13c)

- [x] 10.1 Route an authored change into the **same** candidate/verification structures with
      `Origin: user`; no authoring-specific verdict type. → `internal/proposal/`, `internal/verification/`
      (Test: `TestAuthoredChangeUsesSameVerdictPath`).
- [x] 10.2 🔴 **The user does not author the evidence.** Case selection, held-out split and seeds stay
      platform-derived for an authored verification run. → `internal/proposal/guardrail.go`
      (Test: `TestAuthoredRunCannotSelectItsOwnCases`, `TestAuthoredHeldOutStillDisjoint`).
- [x] 10.3 An authored downgrade that fails the held-out guardrail is reported a **quality regression**,
      never an equal-quality tie. → `internal/verification/` (Test: `TestAuthoredDowngradeFailingGuardrailIsRegression`).
- [x] 10.4 🚫 A forked proposal does **not** credit the originating operator in any operator-performance
      figure. → `internal/proposal/gain.go` reporting (Test: `TestForkedProposalDoesNotCreditOperator`).

## 11. Frontend — Authoring on the existing console surfaces (13c)

- [x] 11.1 🔴 **Do not lose existing capability.** Adding authoring controls to the studio preserves every
      prompt authoring, diffing, binding, and impact-analysis capability already shipped. →
      `web/console/src/app/app/studio/` (Test: `tests/authoring.test.mjs` — "11.1 adding authoring to the studio removed no capability it already had").
- [x] 11.2 Add model + provider-parameter authoring beside prompt authoring; **cross-provider models are
      not offered**, and the boundary is stated rather than the list being silently short. →
      `web/console/src/app/app/studio/` (Test: `tests/authoring.test.mjs` — "11.2 the studio offers model and provider-parameter authoring", "11.2 cross-provider models are absent AND the boundary is stated, not silently short").
- [x] 11.3 Render preflight's three verdicts as **three** states — admissible, refused (cause + node +
      field), not-yet-measurable (missing input). 🚫 Never collapse two "cannot" states into one. →
      `web/console/src/app/app/studio/` (Test: `tests/authoring.test.mjs` — "11.3 preflight renders three distinct states, and not-yet-measurable is not a refusal").
- [x] 11.4 🚫 **The surface derives nothing.** Every score, rank, interval, tie and verdict is rendered as
      received; no client-side computation. → `web/console/` (Test: `tests/authoring.test.mjs` — "11.4 the authoring surface computes no score, rank, interval or comparison").
- [x] 11.5 Display `unverified` wherever an authored change appears alongside verified deltas, in a
      visually distinct class; the hazard palette stays reserved for hazard. → `web/console/`
      (Test: `tests/authoring.test.mjs` — "11.5 every authored-change render carries its verification state").
- [x] 11.6 New page/menu wiring is complete (route + navigation entry + permission gate) so no slot goes
      silently missing; design-system tokens only — `npm run scan:tokens` stays green. → `web/console/`
      (Test: `npm run scan:tokens`, `tests/authoring.test.mjs` — "11.6 the new surface is wired in all three places, so no slot goes silently missing").

## 12. DevOps + Backend — Offline parity and operability (13c)

- [x] 12.1 Add offline CLI authoring verbs that run the **same** gates and emit the **same** typed cause
      with no account and no network. → `internal/cli/` (Test: `TestCLIAuthorsOfflineWithIdenticalCause`).
- [x] 12.2 🚫 **No new egress.** Assert the preflight and submit payloads carry no prompt text, source,
      diff, environment value, or credential, on every path including diagnostics. → `internal/api/`
      (Test: `TestAuthoringPayloadAllowlisted`).
- [x] 12.3 Emit authoring health/audit signals that are **externally readable** (submitted, refused-by-cause,
      conflict, reverted) so an operator can diagnose without a debugger; event names and causes are
      stable identifiers, not prose. → `internal/telemetry/` (Test: `TestAuthoringSignalsExternallyReadable`).
- [x] 12.4 Confirm a deployment with authoring **disabled** behaves byte-identically to pre-13c, and that
      enabling/disabling it is reversible with no migration rollback. → `deploy/`
      (Test: `TestAuthoringDisabledIsPre13cBehavior`).

## 13. QA — The gates that must be able to go red (13c)

- [x] 13.1 🔴 A user **cannot** force a refused materialization: cross-provider swap, un-carryable inline
      param, and un-applying prompt each refuse on the authoring path with the operator-path cause. →
      (Test: `TestAuthoredRefusalsMatchOperatorRefusals`).
- [x] 13.2 🔴 An unverified authored change contributes **zero** to every aggregate improvement, savings
      and quality figure, and is absent from the verified-delta ledger. →
      (Test: `TestUnverifiedContributesZeroToAggregates`).
- [x] 13.3 🔴 An unverified authored change is **never auto-merged**, at any automation level. →
      (Test: `TestUnverifiedNeverAutoMerges`).
- [x] 13.4 Each gate is proven able to go **red**: a passing authored change and a refused one for each
      refusal class — a green-only suite proves nothing. → (Test: `TestAuthoringGatesGoRed`).
- [x] 13.5 Assert against the **downstream consumer**, not the handler's return: after an authored apply,
      the recorded diff, the append-only record and the ledger state are each read back and asserted. A
      2xx is not evidence of persistence. → (Test: `TestAuthoredApplyAssertsDownstreamState`).
- [x] 13.6 Browser acceptance: author a model change, hit a refusal, revert, and read the rendered page —
      a green build is compatible with a page that renders nothing. → (Test: `tests/authoring-acceptance.test.mjs`, run against the production build).

## 14. Product Designer — What the user is offered, and what they are told (13c)

- [x] 14.1 Specify the three preflight states in user-facing terms, with the unhappy paths named: which
      node, which field, which slot, which reason — never a generic failure. →
      `specs/authored-change/spec.md`, `specs/prompt-model-authoring/spec.md`.
- [x] 14.2 Specify that a refusal offers the **legitimate path** where one exists (switch the node to
      bound mode; route the provider at the gateway rather than the call site) instead of a dead end. →
      `specs/prompt-model-authoring/spec.md`.
- [x] 14.3 Specify the wording boundary: an authored change is *applied*, never *verified*, *improved*,
      *optimized* or *safe*; interface text, the record's field, and the code name stay three layers. →
      `specs/authored-change/spec.md`.

## 15. Sales Operations — The claim and its boundary (13c)

- [x] 15.1 State the deliverable claim: users can make **active changes** on this axis and the platform
      applies them through the same gates it applies its own — and every such change is **labeled
      unverified until the harness runs**. 🚫 Never present an authored change as an improvement. →
      PRD §9 Sales lens.
- [x] 15.2 State the refused boundary: authoring does **not** unlock cross-provider routing at customer
      call sites, does **not** provide an override for a refusal, and does **not** let a customer choose
      the cases that judge their own change. → PRD §9 Sales lens, `specs/authored-change/spec.md`.

## 16. Documentation

- [x] 16.1 Cross-reference the PRD from every spec delta and the design. → done in each file header.
- [x] 16.2 Point P14/P15/P16's authoring capabilities at `authored-change` rather than restating it. →
      done in each per-axis spec header.
- [x] 16.3 Fold the six P13 capability specs into `openspec/specs/`. → `openspec/specs/{prompt-rewrite,
      model-selection,authored-change,prompt-model-authoring,language-coverage,prompt-model-language-coverage}/spec.md`
      — cross-references rewritten to the folded depth and verified to resolve.
- [x] 16.4 Point P14/P15/P16's coverage capabilities at `language-coverage` rather than restating it. →
      done in each per-axis spec header.

---

## 17. System Designer — The coverage contract, before any language work (13d)

- [x] 17.1 Author the cross-axis `language-coverage` spec delta: totality over the registered language
      set, per-cell claims, the three refusal classes and their evaluation order, one source, executable
      evidence, semantic parity, the versioned offline table, the polyglot refusal, and coverage that no
      plan can move. → `specs/language-coverage/spec.md`.
- [x] 17.2 Author the axis-specific `prompt-model-language-coverage` spec delta: the binding-site
      generalization, the registry row's binding form, per-language entries that name the missing
      artifact, and the boundary stated before the picker. → `specs/prompt-model-language-coverage/spec.md`.
- [x] 17.3 Record Decision 13 — coverage is a total table over cells, and the engine points at a binding
      site rather than at a named argument — with what was rejected and why (the priority ordering: L6
      extensibility, L3 user-facing complexity, L1 safety). →
      `design.md` Decision 13.
- [x] 17.4 Define the coverage record type: `(axis, language, form, status, cause_id, missing_artifact)`,
      with `status ∈ {materializes, refuses}` and `cause_id` drawn from a closed set of three. It is a
      **read over the engine's own tables**, never a second table written alongside them. →
      `internal/transform/coverage.go` (Test: `TestNoSurfaceHoldsItsOwnCoverageList`, `TestEveryCoverageCellIsWellFormed`).
- [x] 17.5 🔴 **Totality is generated, not written.** Enumerate the registered language set from
      `discovery.DefaultFrontends` and assert every axis has an entry for every language; adding a
      frontend with no entry fails. → `internal/transform/` (Test: `TestCoverageIsTotalOverRegisteredLanguages`).
- [x] 17.6 🚫 **No second coverage list anywhere.** A structural test enumerates the surfaces that state
      coverage (transform refusal, preflight, console, CLI, docs) and asserts each reads the one source.
      → (Test: `TestNoSurfaceHoldsItsOwnCoverageList`).

## 18. Backend — The three causes, their order, and the binding site (13d)

- [x] 18.1 Introduce the three stable cause identifiers — `not-expressible-at-a-call-site`,
      `call-site-cannot-carry-it`, `no-materializer-for-this-language` — on the refusal type, so a
      consumer classifies without parsing prose. → `internal/transform/` (Test: `TestEveryCoverageCellIsWellFormed`, `TestCallSiteCauseBeatsLanguageCause`).
- [x] 18.2 🔴 **Order the questions specific-first in every dimension's rewriter**: the change, then the
      registry row, then the call site's source, then the language. → `internal/transform/`
      (Test: `TestCallSiteCauseBeatsLanguageCause`, `TestCallSiteRefusalIsUnchangedByCoverage`).
- [x] 18.3 🔴 The ordering test must be able to go **red**: a fixture that is both shape-refusable and
      language-refusable asserts the shape cause, and reversing the order fails. →
      (Test: `TestCallSiteCauseBeatsLanguageCause` — verified red by asking the language question first).
- [x] 18.4 Generalize the locator from *named argument* to **binding site** with three forms — named
      argument, builder-chain call, request-value field — keeping every existing form byte-identical. →
      `internal/discovery/bindingsite.go`, `internal/transform/rewrite_span.go` `spanBindingEdit`
      (Test: `TestGenerate_Kotlin_BuilderBoundModelMaterializes`, `TestGenerate_Java_BuilderBoundModelMaterializes`,
      `TestGenerate_Rust_RequestFieldBoundModelMaterializes`).
- [x] 18.5 Extend the signature registry row **additively** to declare the binding form and its locator;
      an existing row parses and hashes unchanged. → `internal/discovery/registry.go` (`LocBuilderCall` / `LocRequestField`)
      (Test: `TestBindingFormIsAdditiveToExistingRows`).
- [x] 18.6 Add Kotlin rows for SDKs that bind at a call site or a locatable builder, and the Java/Rust
      frontend extraction for builder-chain and request-field bindings. → `internal/discovery/registry.yaml`,
      `internal/discovery/bindingsite.go` `locateRowBindings`
      (Test: `TestBindingFormIsAdditiveToExistingRows`, the three materialization tests above).
- [x] 18.7 🔴 A row whose SDK binds nowhere locatable refuses **naming the SDK and its binding style**,
      classified `call-site-cannot-carry-it` — never as a language gap. →
      (Test: `TestGenerate_Kotlin_UnwrittenBindingRefusesAboutTheSource`,
      `TestGenerate_Java_UnwrittenBindingRefusesAboutTheSource`,
      `TestGenerate_Kotlin_SharedBuilderRefusesNamingTheSharing`).
- [x] 18.8 🚫 **No gate is relaxed to reach a language.** `engineFor`'s completeness check stays, the
      binding-site edit is a NEW edit class admitting only its own line, and a test asserts no
      configuration disables a gate for one language only. →
      (Test: `TestNoLanguageSkipsAGate`, `TestBindingSiteAdmitsOnlyItsOwnLine`).

## 19. AI Engineer — Coverage growth changes no measurement (13d)

- [x] 19.1 🔴 Assert that adding a binding form, a row, or a language leaves every previously
      materializable call site's emitted change **byte-identical** and every `config_hash` unchanged; P0
      golden vectors reproduce. → (Test: `TestCoverageGrowthPreservesExistingDiffsAndHashes`,
      `TestGoldenVectorsStillReproduce`).
- [x] 19.2 🔴 **Semantic parity** — the same resolved override materialized in two languages expresses
      the same configuration, over a shared fixture rather than by inspection. →
      (Test: `TestSameOverrideMeansTheSameThingAcrossLanguages` over python/typescript/kotlin/java/rust,
      `TestBoundSkillContractParityAcrossLanguages`).
- [x] 19.3 Confirm the harness stays axis- **and** language-agnostic: a variant materialized in a newly
      covered language is scored with **zero** eval change. → (Test: `TestNewLanguageNeedsNoEvalChange`).

## 20. Frontend — Three causes are three sentences (13d)

- [x] 20.1 Render the three refusal classes as three distinct states: *change your call site*, *this
      cannot be written in source at all*, *the platform has not built this yet (artifact named)*. The
      hazard palette stays reserved for hazard. → `web/console/src/components/coverage.tsx`
      (Test: `coverage.test.mjs` — "each refusal class gets its own visual treatment", "the coverage
      states do not spend the hazard palette"; browser-verified at `/app/coverage` and `/preview/coverage`).
- [x] 20.2 State the boundary **before** the picker, from the shared source, naming the language and
      the binding form; 🚫 never an empty selector. → `web/console/src/components/coverage.tsx`
      `CoverageBoundary`, `src/app/app/wiring/boundaries.tsx`, `src/app/app/context/page.tsx`
      (Test: `coverage.test.mjs` — "the boundary component states which boundary it is, and never renders
      an empty picker").
- [x] 20.3 Surface the per-axis coverage table as a read model rendered as received — no client-side
      derivation of what a language supports. → `internal/api/coverage.go` (`GET /api/v1/coverage`,
      takes no tenant/plan/role), `web/console/src/app/app/coverage/`
      (Test: `coverage.test.mjs` — "the page renders the platform's verdict rather than computing one").
- [x] 20.4 New page/menu wiring is complete (route + navigation entry + permission gate); design-system
      tokens only. → `src/lib/routes.ts`, `src/app/app/layout.tsx` (rail + command path),
      `src/lib/telemetry.ts` (route template) (Test: `npm run scan:tokens`, `craft.test.mjs` R19,
      `coverage.test.mjs` — "coverage has a route and a command-path entry").

## 21. DevOps + QA — The offline table, and the gates that must go red (13d)

- [x] 21.1 Ship the CLI's **versioned** local coverage table; a refusal names the version and the typed
      cause text matches the hosted surface, compared rather than inspected. → `internal/cli/coverage.go`
      (`heros coverage`), `CoverageRefusalSuffix`, the `status` summary line
      (Test: `TestCoverageIsOfflineAndVersioned`, `TestCoverageRefusalSuffixNamesTheVersion`,
      `TestStatusReportsTheCoverageVersion`).
- [x] 21.2 🔴 Every coverage row carries a **named executable proof**: a test that emits the change in
      that cell and asserts the result parses, plus the build gate wherever source is constructed. A row
      without one is rejected. → (Test: `TestEveryCoverageRowHasAProof` — asserted structurally against
      the engine's own tables, so a row cannot be added by editing a doc).
- [x] 21.3 🔴 A polyglot workflow refuses by name, listing the languages found, and emits no patch. →
      `internal/transform/engines.go` `engineFor` (Test: `TestPolyglotWorkflowRefusesByName`).
- [x] 21.4 🔴 Coverage is **identical on every plan**: the same call site under different plans and roles
      yields the same verdict. → (Test: `TestCoverageIsPlanInvariant`).
- [x] 21.5 Assert against the **downstream consumer**: after a materialization in a newly covered
      language, read back the emitted diff, the reparse result, and the recorded coverage cell — a green
      build is not evidence. → (Test: `TestNewLanguageAssertsDownstreamState`).

## 22. Product Designer + Sales Operations — What the gap is called (13d)

- [x] 22.1 Specify the wording boundary: an unmaterialized cell is **not yet applied by the platform**
      and names its missing artifact; it is never a plan limitation, a setting, or something a flag would
      unlock. → `specs/language-coverage/spec.md`.
- [x] 22.2 Specify that a call site the platform will **never** apply does not borrow "not yet" — a
      run-time-assembled value is a fact about the source and says so. → `specs/language-coverage/spec.md`.
- [x] 22.3 State the claim and its boundary: the platform states, **per cell**, what it applies and what
      it refuses. 🚫 "Go is supported" is never "every Go call site is supported," and coverage is
      identical on every plan. → PRD §9.2 Sales lens.

## 23. Wave 13e — how a change reaches a running agent (`change-delivery`, `prompt-model-delivery`)

> Deferred by design: this wave is **docs-first**. The decision is
> [ADR-010](../../../docs/adr/ADR-010-runtime-gradual-rollout.md); nothing below writes a rollout into a
> customer tree until the binding document schema change is specified, because
> [ADR-009](../../../docs/adr/ADR-009-binding-document-format.md) already established that the document's
> shape is a one-way door the moment it ships.

**System Designer**

- [x] 23.1 🔴 **Delivery as a total function.** Specify (axis × change × route) with no absent cell, and
      the reported state for a change no route can deliver. A change that falls out of the table
      silently is the defect this wave exists to remove. → `specs/change-delivery/spec.md`
      (Test: `TestDeliveryTableIsTotalOverEveryAxis`).
- [x] 23.2 Specify the two routes and their **asymmetry** — source is the default and the only road to
      permanence; runtime is temporary and evidence-producing. 🚫 Never presented as a tier or as
      interchangeable. → `specs/change-delivery/spec.md`.
- [x] 23.3 🔴 **The precursor rule.** No path converts a rollout into a durable configuration without a
      merged pull request; a completed rollout's state is **not** `delivered`. → `specs/change-delivery/spec.md`
      (Test: `TestRolloutNeverReachesDeliveredState`).
- [x] 23.4 Specify the three eligibility causes and their evaluation order — `notRuntimeResolvable` →
      `nodeNotBound` → `noRolloutBinding` — and why a permanent boundary is announced first. →
      `specs/change-delivery/spec.md` (Test: `TestEligibilityCauseOrderPrefersTheBoundary`).
- [x] 23.5 State that the runtime route reuses the **one** resolve-hash-gate spine; a rollout-only
      resolve path, hash derivation, or gate is forbidden. → `specs/change-delivery/spec.md`
      (Test: `TestRolloutArmAndMaterializedChangeHashIdentically`).

**Backend**

- [x] 23.6 🔴 **Arm assignment is deterministic and offline.** A pure function of rollout identity and a
      caller-supplied key; no random source, no wall-clock, no process id, no replica-local state. Two
      replicas agree without coordination, and a past assignment replays exactly. →
      (Test: `TestArmAssignmentIsPureAndReplicaAgnostic`, `TestArmAssignmentReplaysWithoutATable`).
- [x] 23.7 A caller with no assignment key gets per-invocation assignment, and the **weaker guarantee is
      recorded** rather than a key being synthesized. → (Test: `TestMissingAssignmentKeyIsRecordedNotSynthesized`).
- [x] 23.8 🔴 **Arm-level `config_hash` attribution.** Every invocation emits the hash of the arm it
      resolved, plus rollout id and arm as separate fields. A resolver emitting the rollout's identity
      where an arm hash belongs **fails the run** — the same class as resolving an unrequested
      configuration (ADR-004 H1). This is what keeps two runs of one hash comparable, which is the
      objection ADR-002 raised against per-node runtime decisions. →
      (Test: `TestCandidateInvocationRecordsCandidateHash`, `TestRolloutIdentityInHashSlotFailsTheRun`).
- [x] 23.9 Bounded expiry, evaluated with no network call and no human present; an expired rollout serves
      the **parent**; extension only by a new document change. →
      (Test: `TestExpiredRolloutServesParentOffline`).
- [x] 23.10 🔴 **Local guard, human resume.** A tripped guard falls back to the parent in-process and
      records the cause with **no call to the platform**; it does not resume on a timer or on the
      condition clearing. → (Test: `TestGuardTripRevertsWithoutPlatform`, `TestRolloutDoesNotSelfResume`).
- [x] 23.11 A rollout is **inert** during eval and verification runs (the resolver is pinned), and its
      production evidence never enters the verified-delta ledger. →
      (Test: `TestRolloutIsInertUnderPinnedResolver`, `TestRolloutEvidenceIsNotAVerifiedDelta`).
- [x] 23.12 Entitlement enforced **server-side**; an active or unreadable halt blocks new rollouts and
      fails closed; a halt does **not** reach into a customer's process to stop a running one. →
      (Test: `TestRolloutEntitlementIsServerSide`, `TestUnreadableHaltFailsClosed`).

**Backend — this axis's own cells**

- [x] 23.13 Model id (within one provider), inference params, and prompt version are rollout-eligible on
      a `bound` node; the same change on an `inline` node reports `nodeNotBound` and the source route is
      unaffected. → `specs/prompt-model-delivery/spec.md` (Test: `TestBoundNodeFieldsAreRolloutEligible`).
- [x] 23.14 🔴 **The provider cell.** A provider-crossing model change is `notRuntimeResolvable` in every
      apply mode, naming the SDK call rewrite, and 🚫 never suggesting a `bound` migration. The two model
      cells appear separately in the table. → `specs/prompt-model-delivery/spec.md`
      (Test: `TestProviderCrossingRefusesBeforeApplyModeIsRead`).
- [x] 23.15 Each arm carries a **complete resolved configuration**, never a delta against the other arm,
      so both arms' effective values are readable in the diff (ADR-004 H2). →
      `specs/prompt-model-delivery/spec.md` (Test: `TestBothArmsAreReadableWithoutComposition`).
- [x] 23.16 A guardrail-rejected downgrade cannot be a rollout candidate; an undecided verdict may be,
      and the ambiguity is recorded on the rollout. → `specs/prompt-model-delivery/spec.md`
      (Test: `TestGuardrailVerdictBoundsRolloutAuthoring`).
- [x] 23.17 An authored change is rollout-eligible on the same cell rules and stays `unverified` — a
      rollout SHALL NOT launder it into a result. → `specs/prompt-model-delivery/spec.md`
      (Test: `TestRolloutDoesNotUpgradeAnAuthoredChange`).

**Frontend + Product Designer**

- [x] 23.18 Render the delivery state as a **route table**, not a status word: which route delivered,
      which refused, and the cause with its owner. A change refused by both routes reads as
      **undeliverable**, never as pending, queued, or in review. →
      `web/console/src/app/app/delivery/` (Test: `delivery.test.mjs`).
- [x] 23.19 A `notRuntimeResolvable` row is structurally distinct from a `noRolloutBinding` row — the
      first carries no artifact, no milestone, no "not yet"; the second names the missing field and its
      owner. → (Test: `TestBoundaryAndBacklogRowsAreDistinguishable`).
- [x] 23.20 Specify the wording boundary: a rollout is described as **evidence under real load**, never
      as a deployment, a release, or a result. 🚫 "Rolled out" is never "shipped". → PRD §9 lenses.

**QA**

- [x] 23.21 🔴 **The gate that must go red.** Sabotage each of: arm-hash attribution, expiry, guard
      revert, the precursor rule, and the eligibility order — each sabotage turns a distinct test red.
      A delivery guarantee that cannot be made to fail is decoration.
      → (Test: `TestDeliveryGuaranteesAreSabotageable`).
