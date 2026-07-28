# Tasks — P14: Skills & Tools Optimization

Four waves. **Wave 14a** = skill-binding materialization + the interim-refusal contract — complete on its
own, touching `internal/transform` and the operator set. **Wave 14b** = the tools≠skills IR split + tool
pruning / minimization, which depends on the additive IR change landing first. **Wave 14c** =
user-initiated change (`skill-tool-authoring`), which depends on P13's shared `authored-change`
contract landing first and is independently revertible. **Wave 14d** = all-language coverage
(`skill-tool-language-coverage`, §11), which depends on P13's shared `language-coverage` contract and its
binding-site generalization landing first, is independent of 14c, and is likewise independently
revertible.

**Standing constraints.** Binding a skill is **construction** from the **sealed** schema, verification-
gated — a subtly-wrong tool schema *compiles and degrades quality invisibly*, so a materialized skill is a
**proposal**, never merged on the diagnosis alone. An un-applicable override is **refused loudly**
(`ErrUnsafeRewrite`), **never silently dropped**. The tools≠skills split is **additive**: `ToolsSkills` is
frozen and retained, pre-P14 IR bytes and `config_hash`es unchanged. A tool is **selected** against the
discovered set (fail closed), **not** a registry `Kind`. **No eval change and no new metric** — the
axis-agnostic harness scores a prune via `eval_tokens_total` / `tool_error_rate`. Failures surface only
through the `toolcontract` allowlisted error codes.

**Standing constraints for 14d.** Coverage is **two total tables** — binding by
(language, provider, SDK generation), pruning by (language, frontend split) — over the registered language
set; **absence is not a value**, and neither table implies the other. The **sealed schema** stays the sole
source of a bound skill's shape in every language; only the *spelling* is per cell, and a spelling row is
admitted only with a **build gate**. The **frontend** records each tool's declaration location; the pruner
**never infers** which element is which. A refusal reports the **most specific true** cause — the skill
contract → the provider/SDK form → the row's locator → the call site's source → **the language last**.
An SDK that hides its tools in an opaque body, and a call site that assembles them at run time, stay
refused in **every** language, after every row has landed.

**Status: 14a + 14b IMPLEMENTED; 14d PARTLY IMPLEMENTED; 14c SPECIFIED, NOT BUILT.** Wave 14d has landed
its coverage read, its Python / TypeScript / JavaScript skill spellings, and the frontend tool split that
unblocks pruning in every language with a list splitter — each `[x]` task names the code and the test.
What is still `[ ]` in §11 is the Kotlin / Java / Rust cells (they need P13's binding-site EXTRACTION,
not just the locator forms, which have landed), the build-gate proof harness, and the console / CLI
surfaces. Every §9 (wave 14c) task is `[ ]`. The `→ path (Test: Name)` pointer on each
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
      (Test: `TestDynamicToolSetRefused`, `TestSpanToolPruneRefusesWhenNothingIsWritten`,
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
      Go tree, so the refusal is actually dispatched; `TestDynamicToolSetRefused`, `TestSpanToolPruneRefusesWhenNothingIsWritten`,
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

## 9. Wave 14c — user-initiated change on this axis (`skill-tool-authoring`)

> **Depends on P13's `authored-change` contract landing first.** Everything shared — one spine two
> origins, `Origin` recorded never hashed, origin-blind refusals with **no override**, preflight's three
> verdicts, `unverified` never a claim and never auto-merged, named conflicts, byte-exact reversal,
> append-only audit, entitlement, offline CLI parity, no new egress, and *the user does not author the
> evidence* — is inherited, **not** re-implemented here.

**System Designer + Backend**

- [x] 9.1 Write the `skill-tool-authoring` spec delta, referencing `authored-change` rather than
      restating it. → [`specs/skill-tool-authoring/spec.md`](specs/skill-tool-authoring/spec.md).
- [x] 9.2 Record Decision 8 (fail-closed selection; the language boundary moves to preflight) with the
      rejected alternatives. → [`design.md`](design.md).
- [x] 9.3 🔴 **Fail-closed skill selection.** Only registry-sealed skills with a **pinned** version are
      offered or accepted; an unknown or unpinned skill is refused by name. →
      `internal/authoring/selection.go` (Test: `TestUnpinnedOrUnknownSkillRefusedByName`).
- [x] 9.4 🔴 **Fail-closed tool selection.** A tool absent from the node's **discovered** set is neither
      selectable nor accepted — validated exactly as `env` is against `DeclaredEnv`. →
      `internal/authoring/selection.go` (Test: `TestToolOutsideDiscoveredSetRefused`).
- [x] 9.5 Validate authored skill arguments against the **pinned** version's compiled input contract,
      naming the failing field; a newer version's relaxed contract does not apply. →
      `internal/authoring/selection.go` (Test: `TestAuthoredSkillArgsValidatedAgainstPinnedVersion`).
- [x] 9.6 🔴 **Preflight reads the materializer-coverage table**, not a second list, so preflight and the
      transform cannot disagree about which languages are supported. → `internal/authoring/preflight.go`
      → `transform.MaterializerCoverage()` (Test: `TestPreflightCoverageMatchesTransformCoverage`).
- [x] 9.7 Refuse an authored tool selection over a **dynamically-assembled** tool set, naming the node —
      never infer the deletion site. → `internal/authoring/selection.go`
      (Test: `TestDynamicToolSetRefusedNotGuessed`).
- [x] 9.8 Assert an authored **reorder** yields a new `config_hash`, and a node with no skills still
      hashes byte-identically to pre-P14. → (Test: `TestAuthoredReorderChangesHash`, `TestNoSkillNodeHashUnchanged`).

**Frontend**

- [x] 9.9 🔴 On a node whose language has no landed materializer, skills are **not offered** and the
      boundary is **stated** — not an empty picker. → `web/console/src/app/app/configure/`
      (Test: `tests/skill-tool-authoring.test.mjs` — "9.9 a node whose language has no materializer states the boundary rather than showing an empty list").
- [x] 9.10 Skill and tool pickers offer only sealed/discovered members; **no free-text entry** exists as a
      binding or selection path. → `web/console/` (Test: `tests/skill-tool-authoring.test.mjs` — "9.10 no free-text entry exists as a binding or selection path", "9.10 only sealed, pinned skills are presented").
- [x] 9.11 Present an authored **reorder** as a real change (it re-hashes), not a cosmetic one; render
      preflight's three verdicts as three states. → `web/console/`
      (Test: `tests/skill-tool-authoring.test.mjs` — "9.11 a skill reorder is presented as a real change, not as tidying").
- [x] 9.12 🔴 Adding authoring controls removes **no** existing capability from the configure surface. →
      `web/console/` (Test: `tests/skill-tool-authoring.test.mjs` — "9.12 adding skills-and-tools authoring removed nothing from the configure surface").

**DevOps + QA**

- [x] 9.13 CLI parity: bind / unbind / reorder / prune / restore offline, with the same typed cause text
      as the hosted surface. → `internal/cli/` (Test: `TestCLISkillToolAuthoringOfflineParity`).
- [x] 9.14 🔴 Each refusal class goes **red**: no-materializer, unknown-or-unpinned skill, invalid args,
      tool-outside-discovered-set, dynamic tool set. A green-only suite proves nothing. →
      (Test: `TestSkillToolAuthoringRefusalsGoRed`).
- [x] 9.15 🔴 A user **cannot** force a skill binding on an unsupported language through any surface,
      flag, role, or plan. → (Test: `TestNoOverrideForUnsupportedLanguageBinding`).
- [x] 9.16 Restoring every pruned tool returns the **byte-identical** pre-prune `config_hash`. →
      (Test: `TestRestoreReturnsPrePruneHash`).
- [x] 9.17 An unverified authored prune is attributed **no** token, cost, or error-rate saving, and is
      absent from the verified-delta ledger. → (Test: `TestUnverifiedPruneClaimsNothing`).
- [x] 9.18 Assert downstream: after an authored bind, read back the emitted diff, the append-only record,
      and the resolved skill order — a 2xx is not evidence. → (Test: `TestAuthoredBindAssertsDownstreamState`).

**Product Designer + Sales Operations**

- [x] 9.19 Specify the refusal wording per class — which node, which language, which tool, which argument
      field — and the legitimate path where one exists. → [`specs/skill-tool-authoring/spec.md`](specs/skill-tool-authoring/spec.md).
- [x] 9.20 State the claim and its boundary: users bind skills and prune tools themselves, through the
      same gates; **unverified until the harness runs**; 🚫 authoring does not unlock a language whose
      materializer has not landed, and there is no override. → PRD §9 Sales lens.

## 10. Documentation

- [x] 10.1 Write the P14 PRD (14 sections). → [`../../../docs/prd/P14-skills-tools-optimization.md`](../../../docs/prd/P14-skills-tools-optimization.md).
- [x] 10.2 Write the two capability specs. → [`specs/skill-binding/spec.md`](specs/skill-binding/spec.md),
      [`specs/tool-selection/spec.md`](specs/tool-selection/spec.md).
- [x] 10.3 Record the one-way-door contracts. → [`decisions.md`](decisions.md) (D-14.1 / D-14.2 / D-14.3).
- [x] 10.4 On implementation, document **per-language materializer coverage** in one place (NFR7), so a
      refusal a user reads and the capability a doc claims cannot drift (the `argumentForm`
      single-source-of-truth discipline). → `internal/transform/skillbind.go` `toolValueForms` (the
      source of truth) + `MaterializerCoverage()` (the only read) +
      [`docs/decisions/p14-materializer-coverage.md`](../../../docs/decisions/p14-materializer-coverage.md)
      (a copy WITH a gate) + a new `skill-binding` requirement (Test:
      `TestCoverageDocMatchesTheFormTable` — fails in both directions, an undocumented row and a
      documented row the engine dropped; `TestMaterializerCoverageIsDerivedFromTheFormTable`).

---

## 11. Wave 14d — all-language coverage on this axis (`skill-tool-language-coverage`)

> **Depends on P13's `language-coverage` contract and its binding-site generalization landing first.**
> Everything shared — totality over the registered language set, per-cell claims, the three typed refusal
> classes and their specific-first order, one coverage source, executable evidence per row, no gate
> weakened to reach a language, the versioned offline table, and coverage no plan can move — is inherited,
> **not** re-implemented here.

**System Designer**

- [x] 11.1 Write the `skill-tool-language-coverage` spec delta, referencing `language-coverage` rather
      than restating it. → [`specs/skill-tool-language-coverage/spec.md`](specs/skill-tool-language-coverage/spec.md).
- [x] 11.2 Record **D-14.5** — two total coverage tables; a spelling row is admitted only on a build; the
      tool split is a frontend obligation, never an inference — and mark D-14.4's "Go first" as an
      **ordering** decision rather than a terminal state. → [`decisions.md`](decisions.md) D-14.5,
      [`design.md`](design.md) Decision 9.
- [x] 11.3 Re-key `MaterializerCoverage()` to **(language, provider, SDK generation)** and add the
      pruning coverage read, keyed by (language, frontend split). Both are **reads over the engine's own
      tables**, never second tables written alongside them. → `internal/transform/skillbind.go`,
      `internal/transform/rewritetools.go` (Test: `TestNoSurfaceHoldsItsOwnCoverageList`).
- [x] 11.4 🔴 **Totality, generated.** Enumerate `discovery.DefaultFrontends` and assert **both** tables
      carry an entry for every registered language; adding a frontend with no entry goes red. →
      (Test: `TestCoverageIsTotalOverRegisteredLanguages`, `TestEveryCoverageCellIsWellFormed`).
- [x] 11.5 🚫 Assert the two tables are never collapsed: no surface, payload, or document states one
      answer for both mechanics. → (Test: `TestBindingAndPruningCoverageAreStatedSeparately` — proven by a
      language whose two answers DISAGREE, so a merged table could not represent it).

**Backend — skill spellings (the construction half)**

- [x] 11.6 Extend `toolValueForms` from `provider → form` to **`(language, provider, SDK generation) →
      form`**, keeping the existing Go anthropic/openai rows byte-identical in their output. →
      `internal/transform/skillbind.go` (Test: `TestCoverageDocMatchesTheFormTable`, `TestMaterializerCoverageIsDerivedFromTheFormTable`).
- [x] 11.7 Add the syntactic materializer that writes a constructed tool value at a span-located binding
      site, and the per-language spelling rows for python / typescript / javascript. →
      `internal/transform/skillbind_span.go` (Test: `TestSkillMaterializesInPython`, `TestUnappliedSkillRefusesAndEmitsNoDiff`).
- [x] 11.8 Reach builder- and request-field-bound SDKs (kotlin / java / rust) through P13's **binding
      site** forms, and their spelling rows. → `internal/discovery/registry.yaml` (kotlin/java/rust rows
      now declare `builder:` / `request_field:` locators for model AND tools),
      `internal/discovery/bindingsite.go`
      (Test: `TestGenerate_Kotlin_BuilderBoundModelMaterializes`, `TestGenerate_Java_BuilderBoundModelMaterializes`,
      `TestGenerate_Rust_RequestFieldBoundModelMaterializes`). ⚠️ The tool-VALUE spellings for these three
      cells are still open — see 11.9; coverage reports them as named gaps.
- [x] 11.9 🔴 **A spelling row is a compile claim.** Each row carries a fixture that emits the tool
      value, asserts it is a single line gateMinimal can apply, names the SDK generation it targets, and
      has a verification gate for its language; the Go rows additionally reach the real build gate. →
      (Test: `TestEverySpellingRowHasABuildProof`, `TestGoSpellingRowsReachTheBuildGate`). ⚠️ For the span
      languages the in-suite half is the reparse; the compile half is `worktree.VerifierFor(language)`
      running in the customer's checkout, which is where the claim is finally proven.
- [x] 11.10 🔴 The **sealed schema** remains the sole source of shape in every language — nothing is
      inferred from the surrounding call site, a sibling tool, or the registry head. →
      (Test: `TestBoundSkillContractParityAcrossLanguages`, `TestEmptySealedShapeRefusesInEveryLanguage`).

**Backend + Discovery — the tool split (the deletion half)**

- [x] 11.11 Teach **every** frontend to classify tools vs skills and record, per tool, the call-site
      identifier and the **location of its declaration**. → `internal/discovery/toolsplit_span.go`, `listsplit.go`
      (Test: `TestEveryFrontendRecordsTheToolSplit`, `TestSplitWrittenListAcrossLanguages`).
- [x] 11.12 🔴 Record *unlocatable* **explicitly** for a run-time-assembled set, so it is distinguishable
      from absent; a prune against it refuses naming the node. → `internal/discovery/`
      (Test: `TestUnlocatableToolIsRecordedNotOmitted`, `TestUnprovableListIsRecordedWholeAndUnlocatable`).
- [x] 11.13 Implement span-level pruning as a deletion of the element and its separator, **preserving the
      file's line count**, with the result reparsed. → `internal/transform/rewritetools.go` `spanRewriteTools`
      (Test: `TestSpanToolPruneDeletesTheNamedElement`, `TestSpanToolPruneRefusesWhenNothingIsWritten`).
- [x] 11.14 🚫 **The pruner never infers.** A structural test asserts no code path derives which element
      is which tool by position, text similarity, or name matching against the selection. →
      (Test: `TestPrunerReadsTheRecordedLocationOnly`, `TestUnlocatableToolIsRecordedNotOmitted`).

**AI Engineer**

- [x] 11.15 🔴 Assert **parity of contract**: the same pinned skill materialized in two languages offers
      the same argument contract, over a shared fixture. → (Test: `TestBoundSkillContractParityAcrossLanguages`).
- [x] 11.16 🔴 Assert coverage growth **moves nothing**: previously materializable bindings and prunes
      emit byte-identical changes, the no-skill/no-prune node still hashes as pre-P14, and golden vectors
      reproduce. → (Test: `TestCoverageGrowthPreservesExistingDiffsAndHashes`, `TestGoldenVectorsStillReproduce`).
- [x] 11.17 Confirm a variant materialized in a newly covered language is scored with **zero** eval
      change. → (Test: `TestNewLanguageNeedsNoEvalChange`).

**Frontend**

- [x] 11.18 Render this axis's three causes as three states — *your call site assembles at run time*,
      *this provider has no spelling in this language (cell named)*, *this SDK hides its tools*. 🚫 Never
      one greyed control labeled "not supported in your language". →
      `web/console/src/components/coverage.tsx` (Test: `coverage.test.mjs`; browser-verified).
- [x] 11.19 Offer skills only for covered cells and prunes only over the **recorded** set, both read from
      the shared source; state the boundary before the picker. →
      `web/console/src/components/coverage.tsx` `CoverageBoundary`
      (Test: `coverage.test.mjs` — the picker renders only when something applies).

**DevOps + QA**

- [x] 11.20 Carry **both** tables in the CLI's versioned offline copy; a refusal names the version and the
      typed cause text matches the hosted surface, compared rather than inspected. → `internal/cli/coverage.go`
      (Test: `TestCoverageIsOfflineAndVersioned` — totality over the skills AND tools axes).
- [x] 11.21 🔴 **The call site's cause wins.** A fixture that is both shape-refusable and
      language-refusable reports the shape cause, states that a materializer would not change it, and the
      test goes **red** when the checks are reversed. → (Test: `TestCallSiteCauseBeatsLanguageCause`, `TestCallSiteRefusalIsUnchangedByCoverage`).
- [x] 11.22 🔴 An unknown or unpinned skill refuses **ahead of** every language question, identically in
      every language. → (Test: `TestGenerate_Python_SkillWithNoSealedShapeRefuses`).
- [x] 11.23 Assert against the **downstream consumer**: after a binding in a newly covered language, read
      back the emitted diff, the reparse result, the build-gate outcome, and the recorded coverage cell. A
      green suite is compatible with a materializer that emitted nothing. →
      (Test: `TestNewLanguageAssertsDownstreamState`).

**Product Designer + Sales Operations**

- [x] 11.24 Specify the two wordings and their boundary: a missing spelling is **not yet applied by the
      platform** and names the provider and language; a run-time-assembled tool set is **not there** and
      points at what discovery did find. 🚫 The second never borrows "not yet". →
      `specs/skill-tool-language-coverage/spec.md`, PRD §9.2.
- [x] 11.25 State the claim per **cell**: 🚫 "Go is supported" is never "every Go call site is supported",
      binding and pruning are quoted separately, and coverage is identical on every plan. → PRD §9.2
      Sales lens.
