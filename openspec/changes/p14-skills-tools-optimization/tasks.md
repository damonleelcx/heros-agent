# Tasks — P14: Skills & Tools Optimization

Two waves. **Wave 14a** = skill-binding materialization + the interim-refusal contract — complete on its
own, touching `internal/transform` and the operator set. **Wave 14b** = the tools≠skills IR split + tool
pruning / minimization, which depends on the additive IR change landing first.

**Standing constraints.** Binding a skill is **construction** from the **sealed** schema, verification-
gated — a subtly-wrong tool schema *compiles and degrades quality invisibly*, so a materialized skill is a
**proposal**, never merged on the diagnosis alone. An un-applicable override is **refused loudly**
(`ErrUnsafeRewrite`), **never silently dropped**. The tools≠skills split is **additive**: `ToolsSkills` is
frozen and retained, pre-P14 IR bytes and `config_hash`es unchanged. A tool is **selected** against the
discovered set (fail closed), **not** a registry `Kind`. **No eval change and no new metric** — the
axis-agnostic harness scores a prune via `eval_tokens_total` / `tool_error_rate`. Failures surface only
through the `toolcontract` allowlisted error codes.

**Status: IMPLEMENTED.** Every task in this file is `[x]`. The `→ path (Test: Name)` pointer on each
one names the code that landed and the test that holds it. Verified green with `go build ./...`,
`go vet ./...`, `gofmt -l`, `go test ./...`, `make schema`, `make discovery-ci`, `make console-types-check`,
and a browser check of the new console surface at `/preview/p14`.

---

## 1. System Designer — Fix the one-way-door contracts before any code ships (14a/14b)

- [x] 1.1 Ratify the **tools≠skills IR split** as additive: new `Tools`/`Skills` fields (`omitempty`,
      DeclaredEnv pattern), `ToolsSkills` frozen and never repurposed. → [`decisions.md`](decisions.md)
      D-14.1; PRD §8.3 D3, §8.4.
- [x] 1.2 Ratify **`DimTools` as a dimension with no new registry `Kind`** — a tool is selected against
      the discovered set (fail closed), not resolved from a ref. → `decisions.md` D-14.2; PRD §8.3 D4.
- [x] 1.3 Ratify the **interim-refusal contract** — an un-applicable `SkillRef` (unsupported language)
      and a dynamic tool set refuse with `ErrUnsafeRewrite`, never a silent drop or partial diff. →
      `decisions.md` D-14.3; PRD §8.3 D2.
- [x] 1.4 Ratify **tool selection's additive `config_hash` participation** (nil-when-empty, decisions.md
      D-1.4 pattern applied a second time). → `decisions.md` D-14.2; PRD §8.3 D5.
- [x] 1.5 Decide **which language gets the first skill materializer** (PRD §14 Q1). → `decisions.md`
      D-14.4: **Go**, gated on a declared per-provider tool-value form; every other language keeps
      D-14.3's refusal, and a Go call site whose provider has no declared form refuses by name.

## 2. Backend — Skill materialization (14a, the security-critical work)

- [x] 2.1 Replace `refuseSkills` for the first supported language with **construction of the SDK tool
      value from the sealed schema** (`SkillEntry.Spec.InputSchema`). 🔴 The shape SHALL come from the
      contract, never inferred from a value. → `internal/transform/skillbind.go` `materializeSkills`,
      dispatched from `rewrite.go` `rewriteSkills` (Test: `TestSkillMaterializedMatchesSealedSchema`,
      `TestSkillMaterializedShapeComesFromContractNotValue`).
- [x] 2.2 🔴 Keep the **interim refusal** for every unsupported language: an un-applicable `SkillRef`
      returns `ErrUnsafeRewrite` naming node + `skills` dim; **no** partial diff. → `refuseSkills`
      retained for every span language; `rewrite_span.go:62` unchanged (Test:
      `TestUnappliedSkillRefusesAndEmitsNoDiff`, `TestGenerate_SpanSkillRefusalNamesNodeAndDimension`,
      plus the per-provider / dynamic-set refusals `TestSkillRefusesProviderWithNoDeclaredToolForm`,
      `TestSkillBindingRefusesDynamicToolSet`).
- [x] 2.3 🚫 Never silently drop a `SkillRef`. A test that a dropped ref still "succeeds" must **fail**.
      → (Test: `TestSilentSkillDropIsAFailingTest` — go-red gate, verified red by making `Generate`
      skip an un-applicable dimension).
- [x] 2.4 Validate a materialized skill's **arguments against its compiled input contract** before the
      node executes. → `transform.BoundSkill.ValidateArgs`/`Invoke` over `registry.SkillEntry.ValidateInput`
      (Test: `TestMaterializedSkillArgsValidatedBeforeExecution`,
      `TestBoundSkillValidatesAgainstItsOwnSealedContract`).
- [x] 2.5 🔴 Confirm a materialized skill surfaces failures only through the **`toolcontract` allowlisted
      error codes**; assert no code outside `ErrorCodeWhitelist`. → `BoundSkill.Invoke` emits only
      `toolcontract.Error` codes (Test: `TestBoundSkillErrorsStayInWhitelist`).
- [x] 2.6 Confirm skill order is identity-bearing and a **no-skill** node hashes byte-identically to
      pre-P14. → `ResolvedNode.SkillRefs` (unsorted) `resolved.go:55` (Test:
      `TestSkillReorderChangesHash_NoSkillByteIdentical`).

## 3. Backend + AI Engineer — Skill operators, verification-gated (14a)

- [x] 3.1 Add a **remove-skill** operator (PRD §14 Q2 settled: its OWN `OperatorKind`, not a prune
      generalization — the reasoning is on `OpRemoveSkill`). → `operator.go` `OpRemoveSkill` +
      `ToolUsage` evidence, `catalog.go` `removeSkillOp` + `removeSkill`, `gain.go` prior/order (Test:
      `TestRemoveSkillOperatorEmitsCandidate`, `TestRemoveSkillFiresOnNeverExercised`,
      `TestRemoveSkillDeclinesWithoutEvidence`, `TestRemoveSkillInadmissibleOffToolUse`,
      `TestRemoveSkillPreservesSurvivingOrder`).
- [x] 3.2 🔴 Gate **add / remove / rerank** on **verification**: each is a candidate scored by the eval
      harness; a materialized skill that regresses the score does **not** ship. → verification fan-out
      (P5.5) (Test: `TestSkillChangeShipsOnlyOnVerifiedNonRegression` — asserted as a PAIR, regressing
      withheld + improving admitted; `TestSkillCandidatesAreProposalsNotDecisions`).
- [x] 3.3 Confirm `add_skill` / `add_rerank` / `fix_schema_binding` / `rag_tune` now produce an
      **applicable diff** in a supported language (they were proposal-only). → drives the real
      `Compiler` → `transform.Generate` (Test: `TestSkillCatalogProducesApplicableDiff`).

## 4. Backend — tools≠skills IR split (14b, additive)

- [x] 4.1 Add `IRNode.Tools []IRTool` and `IRNode.Skills []string`, **`omitempty` nil-when-empty**
      (DeclaredEnv pattern), leaving `ToolsSkills` frozen. → `internal/discovery/emit.go` (`IRTool`,
      `IRToolLocation`, `DeclaresTool`/`ToolByName`/`ToolsRecorded`) + the additive `tools`/`skills`
      properties in `schemas/workflow-ir.schema.json` (Test: `TestSplitFieldsOmittedPreP14ByteIdentical`;
      gate: `make schema`, `make discovery-ci` — every golden still matches).
- [x] 4.2 Teach the **discovery frontend** to classify each entry (tool vs skill) and populate the split
      at extraction. → `internal/discovery/extract.go` `classifyToolsSkills` + `platformSkillForms`
      (fail-closed default: TOOL, PRD §14 Q3) (Test: `TestFrontendPopulatesToolSkillSplit`).
- [x] 4.3 Record the `IRTool` locator (or nil for a dynamically-assembled set → drives the FR14 refusal).
      → `emit.go` `IRTool.declared_at` (Test: `TestDynamicToolSetRecordedAsUnlocatable`,
      `TestStaticToolRecordsItsLocator`).
- [x] 4.4 Confirm a pre-P14 IR round-trips **byte-identically** and its `config_hash` is untouched. →
      (Test: `TestPreP14IRRoundTripsByteIdentical` + `TestNoSkillNoPruneReproducesGolden` reproduces the
      P0 golden vectors).

## 5. Backend + System Designer — `DimTools` dimension + tool selection (14b)

- [x] 5.1 Add `DimTools = "tools"` to the closed enum. → `internal/variantspec/spec.go` + the exported
      `Dimensions()` so an iterating consumer cannot miss a member (Test: `TestDimToolsInClosedEnum`).
- [x] 5.2 Add `NodeOverride.ToolSelection` (+ `isEmpty`/`SelectedTools`/`Validate`). `SelectedTools`
      rather than `Refs`, and deliberately NOT part of `VariantSpec.Refs()`: a tool has no registry
      identity (D-14.2), so a loader asked to resolve one would fail on something never registered. →
      `spec.go` (Test: `TestToolSelectionOverrideValidates`).
- [x] 5.3 Add a `resolveNode` tool block that **validates the selection against the node's discovered
      tool set** (fail closed — `DeclaredEnv`/`in_scope` pattern). 🔴 A selection naming an undiscovered
      tool is **rejected**, and so is one over an IR that predates the split (`Tools == nil` is "not
      recorded", not "no tools"). → `resolve.go` + `ErrToolNotDiscovered` (Test:
      `TestToolSelectionFailsClosedOnUndiscovered`).
- [x] 5.4 Add `ResolvedNode.ToolSelection`, **`omitempty` nil-when-empty** and SORTED (set semantics,
      unlike `SkillRefs`), so a no-prune node hashes byte-identically to pre-P14. → `resolved.go` (Test:
      `TestToolPruneChangesHash_NoPruneByteIdentical`, `TestReorderSemantics`).

## 6. Backend — tool pruning + minimization at the call site (14b)

- [x] 6.1 Add a `DimTools` rewriter that **deletes** a pruned tool (an already-present static element).
      → `internal/transform/rewritetools.go` `rewriteTools` + `absorbSeparator`, registered in
      `rewrite.go`'s table (Test: `TestPrunedToolDeletedAtCallSite`,
      `TestPruneNeverChangesTheLineCount`, `TestToolPruneIsDeterministic`).
- [x] 6.2 🔴 **Refuse** a tool selection over a **dynamically-assembled** set with `ErrUnsafeRewrite`;
      never guess a deletion. → `rewritetools.go` + `spanRewriteTools` in `rewrite_span.go`'s table
      (Test: `TestDynamicToolSetRefused`, `TestSpanToolPruneRefuses`,
      `TestPruneRefusesWhenTreeAndIRDisagree`).
- [x] 6.3 Add an `OpToolPrune` operator (drop tools the eval set never exercises) + `OpToolMinimize`
      (minimal set preserving `task_success`), driven by the new `SignalUnusedTools`. →
      `operator.go`, `catalog.go`, `gain.go` (Test: `TestToolPruneOperatorEmitsCandidate`,
      `TestToolMinimizeEmitsMinimalSet`, `TestToolMinimizeNeverProposesTheEmptySet`,
      `TestToolPruneDeclinesWithoutEvidence`, `TestToolPruneTouchesOnlyTheToolsDimension`).
- [x] 6.4 🚫 **No new metric.** Confirm a pruned set is scored by the **unchanged** harness; the win is
      fewer `eval_tokens_total` / lower `tool_error_rate`. → `internal/evalharness` unchanged (Test:
      `TestPrunedSetScoredByExistingMetrics` — asserts the family's COUNT, not just its members, so a
      seventh metric fails; `TestPrunedCandidateIsAnOrdinaryConfig`).

## 7. QA — acceptance gate (14a + 14b)

- [x] 7.1 🔴 Interim-refusal suite: an un-applicable `SkillRef` and a dynamic tool set each produce
      `ErrUnsafeRewrite` + **no** partial diff; a silent drop is a **failing** test. → (Tests:
      `TestUnappliedSkillRefusesAndEmitsNoDiff` — driven against a REAL Python fixture, not a relabelled
      Go tree, so the refusal is actually dispatched; `TestDynamicToolSetRefused`, `TestSpanToolPruneRefuses`,
      `TestSilentSkillDropIsAFailingTest` — verified red).
- [x] 7.2 Materialization suite: a bound skill matches its sealed schema; a **wrong shape is caught by
      verification**, not just the build. → (Tests: `TestSkillMaterializedMatchesSealedSchema`,
      `TestSkillMaterializedShapeComesFromContractNotValue`,
      `TestSkillChangeShipsOnlyOnVerifiedNonRegression`).
- [x] 7.3 Additive-hash suite: a no-skill/no-prune config reproduces the **P0 golden** `config_hash`;
      add/remove/rerank and a prune each change it; a skill reorder changes it, a tool-selection reorder
      does not. → (Tests: `TestNoSkillNoPruneReproducesGolden`, `TestReorderSemantics`,
      `TestSkillReorderChangesHash_NoSkillByteIdentical`, `TestToolPruneChangesHash_NoPruneByteIdentical`).
- [x] 7.4 Split-additivity suite: pre-P14 IR byte-identical; `ToolsSkills` unchanged; a consumer pinned
      below the new IR minor parses both. → (Tests: `TestPreP14IRRoundTripsByteIdentical`,
      `TestSplitFieldsOmittedPreP14ByteIdentical`, `TestPinnedConsumerParsesBothIRMinors`).
- [x] 7.5 Fail-closed suite: a tool selection naming an undiscovered tool is rejected. → (Test:
      `TestToolSelectionFailsClosedOnUndiscovered`, including the pre-split-IR case).
- [x] 7.6 🔴 Error-taxonomy containment: no tool/skill path emits an error code outside
      `ErrorCodeWhitelist`. → (Test: `TestBoundSkillErrorsStayInWhitelist`).
- [x] 7.7 Determinism: same IR + spec + registry → byte-identical materialized diff + identical
      `config_hash`. → (Tests: `TestSkillToolTransformDeterministic`, `TestToolPruneIsDeterministic`
      for the diff half; `TestSkillToolResolveDeterministic` for the `config_hash` half).

## 8. Product Designer — change legibility (14b)

- [x] 8.1 The change surface distinguishes "**bound a platform skill**" from "**pruned a provider
      tool**", drawn from the split fields. → `proposal.DimChange.Kind`/`Items` + `ChangeKind.Legible`,
      `toolChange` reads `IRNode.Tools` via `Compiled.IR`; rendered by
      [`web/console/src/components/p14SkillsTools.tsx`](../../../web/console/src/components/p14SkillsTools.tsx)
      (Test: `TestChangeSurfaceDistinguishesToolFromSkill`; browser-verified at `/preview/p14`).
- [x] 8.2 A refusal is a **named** surface ("node X, dim skills: no materializer for <language> yet"),
      not a diff that looks complete. → `BuildRefused` + `proposal.ChangeRefusal` (a refusal is a VERDICT,
      not an error that aborts the batch), `api.RefusedCard`, `RefusalNotice` rendered above everything
      for every operator (Test: `TestRefusalIsNamedNotSwallowed`,
      `TestRealFailureIsNotReportedAsARefusal`; browser-verified at `/preview/p14` -> Refused).

## 9. Documentation

- [x] 9.1 Write the P14 PRD (14 sections). → [`../../../docs/prd/P14-skills-tools-optimization.md`](../../../docs/prd/P14-skills-tools-optimization.md).
- [x] 9.2 Write the two capability specs. → [`specs/skill-binding/spec.md`](specs/skill-binding/spec.md),
      [`specs/tool-selection/spec.md`](specs/tool-selection/spec.md).
- [x] 9.3 Record the one-way-door contracts. → [`decisions.md`](decisions.md) (D-14.1 / D-14.2 / D-14.3).
- [x] 9.4 On implementation, document **per-language materializer coverage** in one place (NFR7), so a
      refusal a user reads and the capability a doc claims cannot drift (the `argumentForm`
      single-source-of-truth discipline). → `internal/transform/skillbind.go` `toolValueForms` (the
      source of truth) + `MaterializerCoverage()` (the only read) +
      [`docs/decisions/p14-materializer-coverage.md`](../../../docs/decisions/p14-materializer-coverage.md)
      (a copy WITH a gate) + a new `skill-binding` requirement (Test:
      `TestCoverageDocMatchesTheFormTable` — fails in both directions, an undocumented row and a
      documented row the engine dropped; `TestMaterializerCoverageIsDerivedFromTheFormTable`).
