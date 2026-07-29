# Tasks — P17: Memory Strategy Optimization

> **Superseded in part by [P18](../p18-memory-runtime/).** P17's refusal named two missing artifacts —
> a memory runtime and the call-site rewriter that reads and writes it. P18 built both, so three P17
> claims are no longer true and have been updated **in place** rather than left standing:
> the axis is no longer uniform across languages (Python materializes); `memoryCoverage` is now a
> per-cell read of the materializer table; and the console no longer says the limit is
> language-independent. **Every P17 guarantee survives unchanged** — no silent drop, a typed refusal for
> every uncovered cell, `none` ≡ absent, and every `config_hash` bit-for-bit identical.

Three waves. **Wave 20a** = the `memory-store` registry Kind and the *modeled, refused* `DimMemory`
Dimension — a complete phase on its own: a memory strategy can be referenced, resolved, hashed, and
**refused** at transform. **Wave 20b** = the `OpMemoryPolicy` operator and the metric wiring, so the
diagnosis engine can propose a memory swap and surface it honestly as refused-not-scored. **Wave 20c** =
`memory-authoring` — the second origin, so a workflow owner can *make the change themselves* instead of
waiting for the catalog to nominate it. 20c follows 20b because its central job is to render 20a's refusal
faithfully, and it cannot precede the refusal it renders.

**Standing constraints.** This is **greenfield** — memory is added the one canonical way, via the
eight-step "add an axis" checklist. **Memory is not context** ([P16](../p16-context-strategy-optimization/)):
memory persists *across* invocations; context is within one call. **`none` ≡ absent** — a `none` node
hashes byte-identically to a no-memory node, and the P0 golden `config_hash` vectors reproduce unchanged.
**The transform refuses** a `MemoryRef ≠ none` node with a typed `unsafeRewrite`, never a silent drop, in
**both** engines. **No scored memory win is claimed at M20** — diagnosis proposes, verification decides,
and while the transform refuses a memory proposal cannot be a win.

The eight checklist steps and their tests are **code**, landing in 20a/20b; the authored-change path and
its surface land in 20c. Docs are marked `[x]` on authoring. `🔴` = a security/must-fail test. `🚫` = a banned action. `→` = evidence pointer.

---

## 1. System Designer — Fix the one-way-door contracts before any code (docs)

- [x] 1.1 Record the **new registry `Kind` `memory` + `memory_entry` table** decision (a one-way door).
      → `decisions.md` D1 (rejected: a discriminator on `context_entry`; L5/L6).
- [x] 1.2 Record the **`DimMemory` vs `DimContext` boundary** — memory persists across invocations, context
      is within one call; the classifier already splits them (`taxonomy.go:108` *"between turns"*). A
      one-way door. → `decisions.md` D2 (rejected: memory as a context sub-mode; L5 + strategy).
- [x] 1.3 Record the **additive `omitempty`, `none ≡ absent`** hash-compatibility contract (a one-way door:
      the P0 golden vectors). → `decisions.md` D3 (rejected: always-present field; L2/L5).
- [x] 1.4 Record the **interim-refusal contract** — `MemoryRef ≠ none` → typed `unsafeRewrite`, first-class,
      never a silent drop. → `decisions.md` D4 (rejected: silent-drop / block-at-resolve; L1/L2).
- [x] 1.5 Record the **closed, versioned builtin strategy set + `ParamsSchema`** decision. →
      `decisions.md` D5 (rejected: free-form strings/open params; L5/L2).
- [x] 1.6 Record the **operator-proposes / verification-decides / no-win-while-refused** contract. →
      `decisions.md` D6 (rejected: an operator that reports memory gains; L1 honesty + core principle).
- [x] 1.7 Record the **user-authored memory change** contract — a user MAY author (resolve/hash/record/
      compare), MAY NOT apply while the transform refuses; the boundary is stated **before** the choice,
      the refusal is raised at **preflight** with the transform's own typed cause, and a refusal is never
      rendered as success. → `decisions.md` D7 (rejected: no authoring at all, L5/L6; a second apply path,
      L1; refusal discovered at apply, L1/L8).

## 2. Product + System Designer — the specs (docs)

- [x] 2.1 Author the PRD (14 sections). → `docs/prd/P17-memory-strategy-optimization.md`.
- [x] 2.2 Author the `proposal.md` (Why / What Changes / Impact). → `proposal.md`.
- [x] 2.3 Author `design.md` (Context / 6 decisions with rejected alternatives + level / interfaces / risks).
      → `design.md`.
- [x] 2.4 Author the `memory-store` spec delta. → `specs/memory-store/spec.md`.
- [x] 2.5 Author the `memory-policy` spec delta, including the interim-refusal and hash-participation NFR
      requirements. → `specs/memory-policy/spec.md`.
- [x] 2.6 Author the `memory-authoring` spec delta — the per-axis binding of P13's shared `authored-change`
      contract (referenced, never restated), plus the three P17-specific rules D7 fixes.
      → `specs/memory-authoring/spec.md`; PRD §6 FR17–FR24, §7 NFR9–NFR12.

---

## 3. Backend — Step 1–2: Dimension + NodeOverride (20a)

- [x] 3.1 **Step 1 — the Dimension const.** Add `DimMemory Dimension = "memory"` to the closed enum.
      → `internal/variantspec/spec.go:42` (Test: `TestDimensionEnumClosedIncludesMemory`).
- [x] 3.2 **Step 2 — the NodeOverride field.** Add `MemoryRef string json:"memory_ref,omitempty"`; wire it
      into `isEmpty`, `Refs`, and `Validate` exactly as the sibling refs.
      → `internal/variantspec/spec.go:183` (Test: `TestNodeOverrideMemoryRefIsEmptyAndRefs`).
- [x] 3.3 Assert a no-memory `NodeOverride` serializes **byte-identically** to a pre-P17 override
      (additive/`omitempty`). → `internal/variantspec/spec.go` (Test: `TestNoMemoryOverrideBytesUnchanged`).

## 4. Backend — Step 3–4: resolve + ResolvedNode (20a)

- [x] 4.1 **Step 3 — resolve.** Add a memory block to `resolveNode` (resolve `MemoryRef` to a registry
      entry; default `none` when unset) and a `DimMemory` case to `ResolvedOverride.Dimensions()` so the
      transform iterates memory iff it is set.
      → `internal/variantspec/resolve.go:67,154` (Test: `TestDimensionsReportsMemoryIffSet`).
- [x] 4.2 **Step 4 — the ResolvedNode field.** Add an additive `omitempty`, nil/empty-when-unset memory
      field; a node that binds no memory emits **no** memory key.
      → `internal/variantspec/resolved.go:46` (Test: `TestResolvedNodeMemoryOmittedWhenNone`).
- [x] 4.3 🔴 **`none` ≡ absent.** Assert a `none` node and a no-memory node canonicalize to identical bytes,
      and the P0 golden `config_hash` vectors reproduce unchanged.
      → `internal/variantspec/resolved_config_golden_test.go` (Test: `TestNoneMemoryHashesAsAbsent`).
- [x] 4.4 Assert two specs differing only in memory strategy (or params) produce **different**
      `config_hash`es, and one differing only in non-identity-bearing order hashes the same.
      → `internal/variantspec/p17_memory_resolve_test.go` (Test: `TestConfigHashChangesIffMemoryChanges`).
      🔴 Not `internal/confighash`, as the plan first said: that package is the CANONICALIZER and knows
      nothing about memory. The claim is about the resolved projection, so the test lives where the
      projection does — a test in confighash would have had to build the shape it was meant to check.

## 5. Backend — Step 5: the memory registry (20a)

- [x] 5.1 **Step 5 — the Kind.** Add `KindMemory Kind = "memory"` and the `memory_entry` table constant.
      → `internal/registry/registry.go:57,66` (Test: `TestKindMemoryHashedIntoVersionID`).
- [x] 5.2 Add `memory.go` with the register/resolve path (seal/decode/version_id) shaped exactly like
      `model.go`. → `internal/registry/memory.go` (Test: `TestMemoryRegistrySealDecodeRoundTrip`).
- [x] 5.3 Ship the **five builtin strategies** (`none`, `scratchpad`, `summary-buffer`, `vector-recall`,
      `entity-memory`) as a closed set, each with a `ParamsSchema`, a title, and a description; add a
      cardinality assertion (like `TaxonomySize`). → `internal/registry/memory_builtins.go`
      (Test: `TestBuiltinStrategySetClosedAndSized`).
- [x] 5.4 Reject a memory entry whose params violate the strategy's `ParamsSchema`, at seal time.
      → `internal/registry/memory.go` (Test: `TestParamsSchemaViolationRejectedAtSeal`).
- [x] 5.5 🔴 Assert a memory ref resolves **only** in the memory registry: a memory ref pasted into another
      dimension, and a foreign ref pasted into memory, both **fail closed**.
      → `internal/registry/memory_test.go` (Test: `TestMemoryRefFailsClosedCrossDimension`).
- [x] 5.6 🚫 No inline strategy definitions: a spec that inlines params instead of referencing a version_id
      is rejected. → `internal/variantspec/resolve.go` (Test: `TestMemoryInlineDefinitionRejected`).

## 6. Backend — Step 6: the IR default + discovery frontend (20a)

- [x] 6.1 **Step 6 — the IR field.** Add an additive `omitempty` memory field to `IRNode`, defaulting to
      `none`. → `internal/discovery/emit.go` (Test: `TestIRNodeMemoryDefaultsToNone`).
- [x] 6.2 Add a discovery frontend that emits the per-node memory default, so the resolver always resolves
      against a concrete base. → `internal/discovery/extract.go` (Test: `TestDiscoveryEmitsMemoryDefault`).
- [x] 6.3 Assert determinism: the same target at the same revision emits the same IR memory defaults.
      → `internal/discovery/emit_test.go` (Test: `TestMemoryDefaultDeterministic`).

## 7. Backend — Step 7: the interim refusal (20a, the hard part) 🔴

- [x] 7.1 🔴 **Step 7 (AST engine).** Add `refuseMemory` returning a typed `unsafeRewrite` that names the
      node, the `memory` dimension, and the reason (call-site materialization of a cross-invocation store
      is deferred); register `rewriteMemory` in the dispatch table for `DimMemory`.
      → `internal/transform/rewrite.go:54` (Test: `TestMemoryOverrideRefusedInASTEngine`).
- [x] 7.2 🔴 **Step 7 (span engine).** Refuse identically in the tree-sitter span rewriter, so no target
      language applies a memory change through the other path.
      → `internal/transform/rewrite_span.go:59` (Test: `TestMemoryOverrideRefusedInSpanEngine`).
- [x] 7.3 🔴 The refusal uses the repo's `unsafeRewrite` type and is **distinct** from `ErrUnknownNode` /
      `ErrUnresolvedRef` / `ErrInvalidSpec`; a `MemoryRef ≠ none` node produces **no** diff.
      → `internal/transform/edit.go:90` (Test: `TestMemoryRefusalTypedAndProducesNoDiff`).
- [x] 7.4 🔴 **Refusal totality (canary).** A node constructed to carry a real memory strategy must come
      back refused on **every** path; there is no code path that drops the override or emits a memory edit.
      → `internal/transform/rewrite_test.go` (Test: `TestMemoryRefusalTotalityCanary`).
- [x] 7.5 The refusal is a property of the **transform only**: a spec carrying a `MemoryRef` still resolves
      and still produces a stable, reproducible `config_hash`.
      → `internal/variantspec/resolve_test.go` (Test: `TestMemoryRefResolvesAndHashesDespiteRefusal`).

## 8. AI Engineer — Step 8: the operator (20b)

- [x] 8.1 **Step 8 — the OperatorKind.** Add `OpMemoryPolicy OperatorKind = "memory_policy_switch"`.
      → `internal/proposal/operator.go:34` (Test: `TestOpMemoryPolicyKindStable`).
- [x] 8.2 Add the `DefaultCatalog` row mapping a memory bottleneck signal (`stale_read` /
      `contradictory_memory`) to a proposed strategy swap.
      → `internal/proposal/catalog.go:18` (Test: `TestCatalogHasMemoryPolicyRow`).
- [x] 8.3 Add the `operatorPrior` and `verifyOrderHint` entries for `OpMemoryPolicy` (a coarse ordering
      hint, never a result). → `internal/proposal/gain.go:8,26` (Test: `TestMemoryPolicyPriorAndOrderHint`).
- [x] 8.4 🚫 **No win while refused.** An `OpMemoryPolicy` proposal resolves and hashes but is refused at
      transform, so it yields **no** verified result at M20; assert the proposal is surfaced as
      refused-not-scored, never as a gain.
      → `internal/proposal/operator_test.go` (Test: `TestMemoryProposalRefusedNotScored`).
- [x] 8.5 Map the improvement signal to the classifier's **existing** metric set (`memory_hit_rate` primary,
      `staleness`, `recall_precision`, `write_amplification`); add **no** new metric and **no** taxonomy
      change. → `internal/proposal/catalog.go` (Test: `TestMemorySignalUsesExistingMetricSet`).

## 9. Backend + Product Designer — the authored-change path (20c)

The user-originated origin on P13's shared `authored-change` spine, bound to this axis by D7. **One
preflight verdict, two readers** — the surface and the apply path — so the sentence a user reads before
choosing and the refusal the engine raises cannot drift apart.

🔴 It lands **inside `internal/authoring`**, not in a package of its own. That is the D7 contract made
structural: an `internal/memoryauthoring` beside it would be a second apply path, which is exactly what
"one spine, two origins" forbids. A memory change is therefore authored through the **existing**
`/api/p13/authoring/preflight` and `/submit` routes, carrying a `memory_ref` edit.

- [x] 9.1 **Preflight refuses early.** The existing `Preflighter`'s materializer probe raises the memory
      refusal before any worktree, build, or eval spend; the memory edit is wired into `Edit`,
      `Dimensions()` and `applyEdit`. → `internal/authoring/draft.go`, `internal/authoring/preflight.go`
      (Test: `TestPreflightRefusesWithTransformCause`).
- [x] 9.2 🔴 The preflight cause is the transform's **verbatim**, and a structural check forbids this
      package from authoring a memory refusal sentence of its own; the pre-selection boundary is READ
      from the engine's coverage table (`transform.CoverageFor("memory")`), never restated.
      → `internal/authoring/memory.go`, `internal/api/p17memory.go`
      (Test: `TestPreflightCauseMatchesTransformRefusal`, `TestMemoryBoundaryDerivesFromTheEngineCoverage`).
- [x] 9.3 **Authoring API**: the closed builtin vocabulary with each strategy's `ParamsSchema`, validated
      through the registry's OWN validator (no second validator to drift). Free text is not a selection
      path; a params violation is rejected before sealing.
      → `internal/authoring/memory.go` (Test: `TestAuthorSelectsSealsAndRejectsFreeText`).
- [x] 9.4 🔴 **Clearing is byte-exact.** An empty `memory_ref` removes the key (`omitempty`), so
      select-then-clear reproduces the prior override bytes exactly; `none` is the identity option.
      → `internal/authoring/draft.go` (Test: `TestClearReproducesPriorHashByteIdentically`).
- [x] 9.5 `Origin=user` + actor + `ParentVariantID` recorded on the candidate; origin is **absent from
      the hashed spec**. → `internal/authoring/draft.go` (Test: `TestAuthoredOriginRecordedNotHashed`).
- [x] 9.6 🚫 **No apply, no delivery, no score.** Preflight spends nothing and returns `refused` — its
      own verdict, distinct from `admissible` and `not_yet_measurable`.
      → `internal/authoring/preflight.go` (Test: `TestAuthoredMemoryNeverAppliedOrScored`).
- [x] 9.7 🔴 **Two origins, one spine.** A user-authored memory configuration and the operator's proposal
      of the same configuration produce byte-identical specs and one `config_hash`.
      → `internal/authoring/p17_memory_test.go`
      (Test: `TestAuthoredAndProposedIndistinguishableDownstream`).
- [x] 9.8 Serve the surface's read model — vocabulary + boundary — from the engine, taking **no** tenant,
      plan, or role, so no entitlement can move the boundary.
      → `internal/api/p17memory.go`, `GET /api/p17/memory`
      (Test: `TestMemoryReadModelStatesTheBoundaryBeforeAnyChoice`, `TestMemoryReadModelFailsClosedOnUnknownLanguage`).

## 10. Frontend — the `/app/memory` authoring surface (20c)

Where a workflow owner actually makes the change. Its hardest job is not the form — it is rendering a
refusal faithfully, up front, without either hiding it or making the surface pointless.

- [x] 10.1 Add the `/app/memory` surface: per-node strategy selection from the closed builtin set, a
      schema-driven params editor, and a clear action. → `web/console/src/app/app/memory/{page,authoring,strategies}.tsx`
      (Test: `web/console/tests/memory.test.mjs`, 9 tests; 324/324 console suite green).
- [x] 10.2 🔴 **The boundary is stated before the choice**, sourced from the shared preflight verdict — not
      a second sentence written beside the control — and attributed to the platform's deferred artifact,
      never to the user's call site, language, or strategy.
      → `web/console/src/app/app/memory/page.tsx` (Test: `memory.test.mjs` — boundary-before-choice).
- [x] 10.3 🚫 **The control is live, not silently disabled.** A user can select, parameterize, and clear;
      the reason it cannot be applied is *stated*, because a greyed-out control says nothing about why.
      → `web/console/src/app/app/memory/authoring.tsx` (Test: `memory.test.mjs` — live-control).
- [x] 10.4 🔴 **A refusal is never rendered as success.** No applied / delivered / partially-applied state,
      no attributed gain; `refused` renders as its own state, distinct from `failed` and `pending`.
      → `web/console/src/app/app/memory/page.tsx` (Test: `memory.test.mjs` — refused-not-success).
- [x] 10.5 Show the authored variant's real `config_hash` and its parent, so the user sees what the change
      *did* produce — the modeled half that is not refused.
      → `web/console/src/app/app/memory/page.tsx` (Test: `memory.test.mjs` — hash-visible).
- [x] 10.6 Verify the surface in a real browser (render, select, parameterize, clear, refusal state).

## 11. QA — acceptance gate

- [x] 11.1 🔴 Refusal suite: a `MemoryRef ≠ none` node is refused with a typed `unsafeRewrite` in **both**
      engines, distinct from unknown/invalid errors, producing no diff; the canary cannot be made to pass.
      → `internal/transform/rewrite_test.go` (Test: `TestMemoryRefusalSuite`).
- [x] 11.2 🔴 Identity suite: `none` ≡ absent bytes; the P0 golden vectors reproduce.
      → `internal/variantspec/resolved_config_golden_test.go` (Test: `TestNoneMemoryHashesAsAbsent`).
- [x] 11.3 Hash-participation suite: memory strategy/param change ⇒ different hash; non-identity-bearing
      order ⇒ same hash. → `internal/variantspec/p17_memory_resolve_test.go` (Test: `TestConfigHashChangesIffMemoryChanges`).
      🔴 Not `internal/confighash`, as the plan first said: that package is the CANONICALIZER and knows
      nothing about memory. The claim is about the resolved projection, so the test lives where the
      projection does — a test in confighash would have had to build the shape it was meant to check.
- [x] 11.4 Registry suite: five builtins resolve; a sixth without a version bump fails the cardinality
      assertion; a foreign/cross-dimension ref fails closed; a params violation is rejected at seal.
      → `internal/registry/memory_test.go` (Test: `TestMemoryRegistrySuite`).
- [x] 11.5 Boundary suite: no memory construct is expressible as a context construct or vice versa.
      → `internal/variantspec/spec_test.go` (Test: `TestMemoryContextDisjoint`).
- [x] 11.6 Operator dormancy suite: `OpMemoryPolicy` is catalogued with a prior and produces no scored memory
      result at M20. → `internal/proposal/operator_test.go` (Test: `TestMemoryProposalRefusedNotScored`).

- [x] 11.7 🔴 **Evidence audit.** Parse this plan and assert every test a **completed** task names as its
      proof actually exists. A `[x]` with a `(Test: …)` pointer is a CLAIM, and a green build cannot
      check it: an unwritten proof does not fail, it never runs.
      → `internal/variantspec/p17_acceptance_test.go`
      (Test: `TestP17NamedEvidenceExists`, `TestP17ConsoleEvidenceExists`).
- [x] 11.8 Pin the one-way doors at the struct level: the enum grew by exactly one, and every
      memory-carrying field is `omitempty`. → `internal/variantspec/p17_acceptance_test.go`
      (Test: `TestP17AddsExactlyOneDimension`, `TestP17MemoryFieldIsAdditiveEverywhere`).

## 12. Documentation (docs)

- [x] 12.1 On phase completion, fold the P17 capability specs into `openspec/specs/`. → DONE: three, not
      two — `memory-store`, `memory-policy`, and the `memory-authoring` capability 20c added. Paths
      rebased and link-checked; the two siblings it references that are not yet folded (P14
      `skill-tool-authoring`, P15 `wiring-authoring`) point at their change deltas and say so, rather
      than at a folded path that does not exist.
- [x] 12.2 Cross-reference [P16](../p16-context-strategy-optimization/) at the memory-vs-context boundary in
      both the PRD (§8.2) and `design.md` (Decision 2), so the split is stated once and linked, not restated.

## 13. Verification on a real repository

- [x] 13.1 Drive every P17 code path against **github.com/nousresearch/hermes-agent** at `528e335`
      (31 Python nodes). → `cmd/p17hermes` (`go run ./cmd/p17hermes -repo /tmp/hermes-agent`).

What the run establishes, on the real tree rather than on fixtures:

| § | Claim | Result |
|---|---|---|
| 1 | discovery emits a concrete memory default per node | `none` × 31, and **no** `memory` key in the emitted IR — the default costs nothing |
| 2 | 🔴 `none` ≡ absent | identical canonical bytes and identical `config_hash` (`9d0ee9fc9b32…`) — no stored hash moved when the axis shipped |
| 3 | the hash moves iff the memory moves | 6 distinct strategies/params → 6 distinct hashes; `none` alone returns the baseline |
| 4 | 🔴 **the headline** | **186 (node × strategy) combinations, 186 typed refusals, 0 diffs.** A mixed spec is refused WHOLE, so no partial diff exists to be scored |
| 4 | coverage is uniform | 35 cells over 7 languages, 28 refusals — no language is named as the blocker |
| 5 | the operator is catalogued and dormant | 6 candidates on a real node, none of them `none`, each stating its own refusal in the rationale, none carrying a result |
| 6 | the authored path | selection resolves + hashes + records `origin=user`/parent/`unverified`; a params violation is rejected **before** sealing; the operator's route to the same configuration hashes **identically**; clearing reproduces the parent hash **byte-exactly** |

🔴 The finding is the refusal, not a gap in the demonstration. A run that produced a diff here would mean
the override had been silently dropped and the variant scored as its base configuration — the number would
be wrong and would look exactly like a number that is right. §4 is the proof that cannot happen.
