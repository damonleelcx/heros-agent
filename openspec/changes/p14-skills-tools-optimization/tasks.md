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

**This round ships the specs, not the code.** Doc tasks are `[x]`; code tasks are `[ ]` with a
`→ path (Test: Name)` evidence pointer for the implementing round.

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
- [ ] 1.5 Decide **which language gets the first skill materializer** (PRD §14 Q1). → to be recorded in
      `decisions.md` before 14a code lands (Go is the likely first — the AST engine is most precise and
      `refuseSkills` is a Go rewriter today).

## 2. Backend — Skill materialization (14a, the security-critical work)

- [ ] 2.1 Replace `refuseSkills` for the first supported language with **construction of the SDK tool
      value from the sealed schema** (`SkillEntry.Spec.InputSchema`). 🔴 The shape SHALL come from the
      contract, never inferred from a value. → `internal/transform/rewrite.go:378` (replace
      `rewriteSkills`) (Test: `TestSkillMaterializedMatchesSealedSchema`).
- [ ] 2.2 🔴 Keep the **interim refusal** for every unsupported language: an un-applicable `SkillRef`
      returns `ErrUnsafeRewrite` naming node + `skills` dim; **no** partial diff. → `refuseSkills`
      retained per-language; `rewrite_span.go:62` unchanged until each span language lands (Test:
      `TestUnappliedSkillRefusesAndEmitsNoDiff`).
- [ ] 2.3 🚫 Never silently drop a `SkillRef`. A test that a dropped ref still "succeeds" must **fail**.
      → (Test: `TestSilentSkillDropIsAFailingTest` — go-red gate).
- [ ] 2.4 Validate a materialized skill's **arguments against its compiled input contract** before the
      node executes. → `registry.SkillEntry.ValidateInput` at the bind site (Test:
      `TestMaterializedSkillArgsValidatedBeforeExecution`).
- [ ] 2.5 🔴 Confirm a materialized skill surfaces failures only through the **`toolcontract` allowlisted
      error codes**; assert no code outside `ErrorCodeWhitelist`. → `internal/toolcontract/errors.go`
      (Test: `TestBoundSkillErrorsStayInWhitelist`).
- [ ] 2.6 Confirm skill order is identity-bearing and a **no-skill** node hashes byte-identically to
      pre-P14. → `ResolvedNode.SkillRefs` (unsorted) `resolved.go:55` (Test:
      `TestSkillReorderChangesHash_NoSkillByteIdentical`).

## 3. Backend + AI Engineer — Skill operators, verification-gated (14a)

- [ ] 3.1 Add a **remove-skill** operator (or generalize prune per PRD §14 Q2). → `internal/proposal/
      operator.go:34` (new `OperatorKind`), `catalog.go:18` (row), `gain.go` (prior) (Test:
      `TestRemoveSkillOperatorEmitsCandidate`).
- [ ] 3.2 🔴 Gate **add / remove / rerank** on **verification**: each is a candidate scored by the eval
      harness; a materialized skill that regresses the score does **not** ship. → verification fan-out
      (P5.5) (Test: `TestSkillChangeShipsOnlyOnVerifiedNonRegression`).
- [ ] 3.3 Confirm `add_skill` / `add_rerank` / `fix_schema_binding` / `rag_tune` now produce an
      **applicable diff** in a supported language (they were proposal-only). → `catalog.go`
      `addSkillOp`/`addRerankOp`/`ragTuneOp` (Test: `TestSkillCatalogProducesApplicableDiff`).

## 4. Backend — tools≠skills IR split (14b, additive)

- [ ] 4.1 Add `IRNode.Tools []IRTool` and `IRNode.Skills []string`, **`omitempty` nil-when-empty**
      (DeclaredEnv pattern), leaving `ToolsSkills` frozen. → `internal/discovery/emit.go:92` (after
      `:98`) (Test: `TestSplitFieldsOmittedPreP14ByteIdentical`).
- [ ] 4.2 Teach the **discovery frontend** to classify each entry (tool vs skill) and populate the split
      at extraction. → `internal/discovery/extract.go` (Test: `TestFrontendPopulatesToolSkillSplit`).
- [ ] 4.3 Record the `IRTool` locator (or nil for a dynamically-assembled set → drives the FR14 refusal).
      → `emit.go` `IRTool.declared_at` (Test: `TestDynamicToolSetRecordedAsUnlocatable`).
- [ ] 4.4 Confirm a pre-P14 IR round-trips **byte-identically** and its `config_hash` is untouched. →
      (Test: `TestPreP14IRRoundTripsByteIdentical` + P0 golden vectors reproduce).

## 5. Backend + System Designer — `DimTools` dimension + tool selection (14b)

- [ ] 5.1 Add `DimTools = "tools"` to the closed enum. → `internal/variantspec/spec.go:42` (Test:
      `TestDimToolsInClosedEnum`).
- [ ] 5.2 Add `NodeOverride.ToolSelection` (+ `isEmpty`/`Refs`/`Validate`). → `spec.go:183` (Test:
      `TestToolSelectionOverrideValidates`).
- [ ] 5.3 Add a `resolveNode` tool block that **validates the selection against the node's discovered
      tool set** (fail closed — `DeclaredEnv`/`in_scope` pattern). 🔴 A selection naming an undiscovered
      tool is **rejected**. → `resolve.go:67,154` (Test: `TestToolSelectionFailsClosedOnUndiscovered`).
- [ ] 5.4 Add `ResolvedNode.ToolSelection`, **`omitempty` nil-when-empty**, so a no-prune node hashes
      byte-identically to pre-P14. → `resolved.go:46` (Test:
      `TestToolPruneChangesHash_NoPruneByteIdentical`).

## 6. Backend — tool pruning + minimization at the call site (14b)

- [ ] 6.1 Add a `DimTools` rewriter that **deletes** a pruned tool (an already-present static element).
      → `internal/transform/rewrite.go:54` (table) + a `rewriteTools` (Test:
      `TestPrunedToolDeletedAtCallSite`).
- [ ] 6.2 🔴 **Refuse** a tool selection over a **dynamically-assembled** set with `ErrUnsafeRewrite`;
      never guess a deletion. → `rewrite.go` / `rewrite_span.go:59` (Test:
      `TestDynamicToolSetRefused`).
- [ ] 6.3 Add an `OpToolPrune` operator (drop tools the eval set never exercises) + `OpToolMinimize`
      (minimal set preserving `task_success`). → `operator.go:34`, `catalog.go:18`, `gain.go` (Test:
      `TestToolPruneOperatorEmitsCandidate`, `TestToolMinimizeEmitsMinimalSet`).
- [ ] 6.4 🚫 **No new metric.** Confirm a pruned set is scored by the **unchanged** harness; the win is
      fewer `eval_tokens_total` / lower `tool_error_rate`. → `internal/evalharness` unchanged (Test:
      `TestPrunedSetScoredByExistingMetrics`).

## 7. QA — acceptance gate (14a + 14b)

- [ ] 7.1 🔴 Interim-refusal suite: an un-applicable `SkillRef` and a dynamic tool set each produce
      `ErrUnsafeRewrite` + **no** partial diff; a silent drop is a **failing** test. → (Tests:
      `TestUnappliedSkillRefusesAndEmitsNoDiff`, `TestDynamicToolSetRefused`,
      `TestSilentSkillDropIsAFailingTest`).
- [ ] 7.2 Materialization suite: a bound skill matches its sealed schema; a **wrong shape is caught by
      verification**, not just the build. → (Tests: `TestSkillMaterializedMatchesSealedSchema`,
      `TestSkillChangeShipsOnlyOnVerifiedNonRegression`).
- [ ] 7.3 Additive-hash suite: a no-skill/no-prune config reproduces the **P0 golden** `config_hash`;
      add/remove/rerank and a prune each change it; a skill reorder changes it, a tool-selection reorder
      does not. → (Tests: `TestNoSkillNoPruneReproducesGolden`, `TestReorderSemantics`).
- [ ] 7.4 Split-additivity suite: pre-P14 IR byte-identical; `ToolsSkills` unchanged; a consumer pinned
      below the new IR minor parses both. → (Tests: `TestPreP14IRRoundTripsByteIdentical`,
      `TestSplitFieldsOmittedPreP14ByteIdentical`).
- [ ] 7.5 Fail-closed suite: a tool selection naming an undiscovered tool is rejected. → (Test:
      `TestToolSelectionFailsClosedOnUndiscovered`).
- [ ] 7.6 🔴 Error-taxonomy containment: no tool/skill path emits an error code outside
      `ErrorCodeWhitelist`. → (Test: `TestBoundSkillErrorsStayInWhitelist`).
- [ ] 7.7 Determinism: same IR + spec + registry → byte-identical materialized diff + identical
      `config_hash`. → (Test: `TestSkillToolTransformDeterministic`).

## 8. Product Designer — change legibility (14b)

- [ ] 8.1 The change surface distinguishes "**bound a platform skill**" from "**pruned a provider
      tool**", drawn from the split fields. → consumes `IRNode.Tools`/`Skills` (Test:
      `TestChangeSurfaceDistinguishesToolFromSkill`).
- [ ] 8.2 A refusal is a **named** surface ("node X, dim skills: no materializer for <language> yet"),
      not a diff that looks complete. → `ErrUnsafeRewrite` detail rendered (Test:
      `TestRefusalIsNamedNotSwallowed`).

## 9. Documentation

- [x] 9.1 Write the P14 PRD (14 sections). → [`../../../docs/prd/P14-skills-tools-optimization.md`](../../../docs/prd/P14-skills-tools-optimization.md).
- [x] 9.2 Write the two capability specs. → [`specs/skill-binding/spec.md`](specs/skill-binding/spec.md),
      [`specs/tool-selection/spec.md`](specs/tool-selection/spec.md).
- [x] 9.3 Record the one-way-door contracts. → [`decisions.md`](decisions.md) (D-14.1 / D-14.2 / D-14.3).
- [ ] 9.4 On implementation, document **per-language materializer coverage** in one place (NFR7), so a
      refusal a user reads and the capability a doc claims cannot drift (the `argumentForm`
      single-source-of-truth discipline). → `internal/transform` doc comment + capability doc.
