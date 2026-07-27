# Tasks — P17: Memory Strategy Optimization

Two waves. **Wave 20a** = the `memory-store` registry Kind and the *modeled, refused* `DimMemory`
Dimension — a complete phase on its own: a memory strategy can be referenced, resolved, hashed, and
**refused** at transform. **Wave 20b** = the `OpMemoryPolicy` operator and the metric wiring, so the
diagnosis engine can propose a memory swap and surface it honestly as refused-not-scored.

**Standing constraints.** This is **greenfield** — memory is added the one canonical way, via the
eight-step "add an axis" checklist. **Memory is not context** ([P16](../p16-context-strategy-optimization/)):
memory persists *across* invocations; context is within one call. **`none` ≡ absent** — a `none` node
hashes byte-identically to a no-memory node, and the P0 golden `config_hash` vectors reproduce unchanged.
**The transform refuses** a `MemoryRef ≠ none` node with a typed `unsafeRewrite`, never a silent drop, in
**both** engines. **No scored memory win is claimed at M20** — diagnosis proposes, verification decides,
and while the transform refuses a memory proposal cannot be a win.

This round ships **docs** (marked `[x]`). The eight checklist steps and their tests are **code** (`[ ]`)
and land in 20a/20b. `🔴` = a security/must-fail test. `🚫` = a banned action. `→` = evidence pointer.

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

## 2. Product + System Designer — the specs (docs)

- [x] 2.1 Author the PRD (14 sections). → `docs/prd/P17-memory-strategy-optimization.md`.
- [x] 2.2 Author the `proposal.md` (Why / What Changes / Impact). → `proposal.md`.
- [x] 2.3 Author `design.md` (Context / 6 decisions with rejected alternatives + level / interfaces / risks).
      → `design.md`.
- [x] 2.4 Author the `memory-store` spec delta. → `specs/memory-store/spec.md`.
- [x] 2.5 Author the `memory-policy` spec delta, including the interim-refusal and hash-participation NFR
      requirements. → `specs/memory-policy/spec.md`.

---

## 3. Backend — Step 1–2: Dimension + NodeOverride (20a)

- [ ] 3.1 **Step 1 — the Dimension const.** Add `DimMemory Dimension = "memory"` to the closed enum.
      → `internal/variantspec/spec.go:42` (Test: `TestDimensionEnumClosedIncludesMemory`).
- [ ] 3.2 **Step 2 — the NodeOverride field.** Add `MemoryRef string json:"memory_ref,omitempty"`; wire it
      into `isEmpty`, `Refs`, and `Validate` exactly as the sibling refs.
      → `internal/variantspec/spec.go:183` (Test: `TestNodeOverrideMemoryRefIsEmptyAndRefs`).
- [ ] 3.3 Assert a no-memory `NodeOverride` serializes **byte-identically** to a pre-P17 override
      (additive/`omitempty`). → `internal/variantspec/spec.go` (Test: `TestNoMemoryOverrideBytesUnchanged`).

## 4. Backend — Step 3–4: resolve + ResolvedNode (20a)

- [ ] 4.1 **Step 3 — resolve.** Add a memory block to `resolveNode` (resolve `MemoryRef` to a registry
      entry; default `none` when unset) and a `DimMemory` case to `ResolvedOverride.Dimensions()` so the
      transform iterates memory iff it is set.
      → `internal/variantspec/resolve.go:67,154` (Test: `TestDimensionsReportsMemoryIffSet`).
- [ ] 4.2 **Step 4 — the ResolvedNode field.** Add an additive `omitempty`, nil/empty-when-unset memory
      field; a node that binds no memory emits **no** memory key.
      → `internal/variantspec/resolved.go:46` (Test: `TestResolvedNodeMemoryOmittedWhenNone`).
- [ ] 4.3 🔴 **`none` ≡ absent.** Assert a `none` node and a no-memory node canonicalize to identical bytes,
      and the P0 golden `config_hash` vectors reproduce unchanged.
      → `internal/variantspec/resolved_config_golden_test.go` (Test: `TestNoneMemoryHashesAsAbsent`).
- [ ] 4.4 Assert two specs differing only in memory strategy (or params) produce **different**
      `config_hash`es, and one differing only in non-identity-bearing order hashes the same.
      → `internal/confighash/confighash_test.go` (Test: `TestConfigHashChangesIffMemoryChanges`).

## 5. Backend — Step 5: the memory registry (20a)

- [ ] 5.1 **Step 5 — the Kind.** Add `KindMemory Kind = "memory"` and the `memory_entry` table constant.
      → `internal/registry/registry.go:57,66` (Test: `TestKindMemoryHashedIntoVersionID`).
- [ ] 5.2 Add `memory.go` with the register/resolve path (seal/decode/version_id) shaped exactly like
      `model.go`. → `internal/registry/memory.go` (Test: `TestMemoryRegistrySealDecodeRoundTrip`).
- [ ] 5.3 Ship the **five builtin strategies** (`none`, `scratchpad`, `summary-buffer`, `vector-recall`,
      `entity-memory`) as a closed set, each with a `ParamsSchema`, a title, and a description; add a
      cardinality assertion (like `TaxonomySize`). → `internal/registry/memory_builtins.go`
      (Test: `TestBuiltinStrategySetClosedAndSized`).
- [ ] 5.4 Reject a memory entry whose params violate the strategy's `ParamsSchema`, at seal time.
      → `internal/registry/memory.go` (Test: `TestParamsSchemaViolationRejectedAtSeal`).
- [ ] 5.5 🔴 Assert a memory ref resolves **only** in the memory registry: a memory ref pasted into another
      dimension, and a foreign ref pasted into memory, both **fail closed**.
      → `internal/registry/memory_test.go` (Test: `TestMemoryRefFailsClosedCrossDimension`).
- [ ] 5.6 🚫 No inline strategy definitions: a spec that inlines params instead of referencing a version_id
      is rejected. → `internal/variantspec/resolve.go` (Test: `TestMemoryInlineDefinitionRejected`).

## 6. Backend — Step 6: the IR default + discovery frontend (20a)

- [ ] 6.1 **Step 6 — the IR field.** Add an additive `omitempty` memory field to `IRNode`, defaulting to
      `none`. → `internal/discovery/emit.go` (Test: `TestIRNodeMemoryDefaultsToNone`).
- [ ] 6.2 Add a discovery frontend that emits the per-node memory default, so the resolver always resolves
      against a concrete base. → `internal/discovery/extract.go` (Test: `TestDiscoveryEmitsMemoryDefault`).
- [ ] 6.3 Assert determinism: the same target at the same revision emits the same IR memory defaults.
      → `internal/discovery/emit_test.go` (Test: `TestMemoryDefaultDeterministic`).

## 7. Backend — Step 7: the interim refusal (20a, the hard part) 🔴

- [ ] 7.1 🔴 **Step 7 (AST engine).** Add `refuseMemory` returning a typed `unsafeRewrite` that names the
      node, the `memory` dimension, and the reason (call-site materialization of a cross-invocation store
      is deferred); register `rewriteMemory` in the dispatch table for `DimMemory`.
      → `internal/transform/rewrite.go:54` (Test: `TestMemoryOverrideRefusedInASTEngine`).
- [ ] 7.2 🔴 **Step 7 (span engine).** Refuse identically in the tree-sitter span rewriter, so no target
      language applies a memory change through the other path.
      → `internal/transform/rewrite_span.go:59` (Test: `TestMemoryOverrideRefusedInSpanEngine`).
- [ ] 7.3 🔴 The refusal uses the repo's `unsafeRewrite` type and is **distinct** from `ErrUnknownNode` /
      `ErrUnresolvedRef` / `ErrInvalidSpec`; a `MemoryRef ≠ none` node produces **no** diff.
      → `internal/transform/edit.go:90` (Test: `TestMemoryRefusalTypedAndProducesNoDiff`).
- [ ] 7.4 🔴 **Refusal totality (canary).** A node constructed to carry a real memory strategy must come
      back refused on **every** path; there is no code path that drops the override or emits a memory edit.
      → `internal/transform/rewrite_test.go` (Test: `TestMemoryRefusalTotalityCanary`).
- [ ] 7.5 The refusal is a property of the **transform only**: a spec carrying a `MemoryRef` still resolves
      and still produces a stable, reproducible `config_hash`.
      → `internal/variantspec/resolve_test.go` (Test: `TestMemoryRefResolvesAndHashesDespiteRefusal`).

## 8. AI Engineer — Step 8: the operator (20b)

- [ ] 8.1 **Step 8 — the OperatorKind.** Add `OpMemoryPolicy OperatorKind = "memory_policy_switch"`.
      → `internal/proposal/operator.go:34` (Test: `TestOpMemoryPolicyKindStable`).
- [ ] 8.2 Add the `DefaultCatalog` row mapping a memory bottleneck signal (`stale_read` /
      `contradictory_memory`) to a proposed strategy swap.
      → `internal/proposal/catalog.go:18` (Test: `TestCatalogHasMemoryPolicyRow`).
- [ ] 8.3 Add the `operatorPrior` and `verifyOrderHint` entries for `OpMemoryPolicy` (a coarse ordering
      hint, never a result). → `internal/proposal/gain.go:8,26` (Test: `TestMemoryPolicyPriorAndOrderHint`).
- [ ] 8.4 🚫 **No win while refused.** An `OpMemoryPolicy` proposal resolves and hashes but is refused at
      transform, so it yields **no** verified result at M20; assert the proposal is surfaced as
      refused-not-scored, never as a gain.
      → `internal/proposal/operator_test.go` (Test: `TestMemoryProposalRefusedNotScored`).
- [ ] 8.5 Map the improvement signal to the classifier's **existing** metric set (`memory_hit_rate` primary,
      `staleness`, `recall_precision`, `write_amplification`); add **no** new metric and **no** taxonomy
      change. → `internal/proposal/catalog.go` (Test: `TestMemorySignalUsesExistingMetricSet`).

## 9. QA — acceptance gate

- [ ] 9.1 🔴 Refusal suite: a `MemoryRef ≠ none` node is refused with a typed `unsafeRewrite` in **both**
      engines, distinct from unknown/invalid errors, producing no diff; the canary cannot be made to pass.
      → `internal/transform/rewrite_test.go` (Test: `TestMemoryRefusalSuite`).
- [ ] 9.2 🔴 Identity suite: `none` ≡ absent bytes; the P0 golden vectors reproduce.
      → `internal/variantspec/resolved_config_golden_test.go` (Test: `TestNoneMemoryHashesAsAbsent`).
- [ ] 9.3 Hash-participation suite: memory strategy/param change ⇒ different hash; non-identity-bearing
      order ⇒ same hash. → `internal/confighash/confighash_test.go` (Test: `TestConfigHashChangesIffMemoryChanges`).
- [ ] 9.4 Registry suite: five builtins resolve; a sixth without a version bump fails the cardinality
      assertion; a foreign/cross-dimension ref fails closed; a params violation is rejected at seal.
      → `internal/registry/memory_test.go` (Test: `TestMemoryRegistrySuite`).
- [ ] 9.5 Boundary suite: no memory construct is expressible as a context construct or vice versa.
      → `internal/variantspec/spec_test.go` (Test: `TestMemoryContextDisjoint`).
- [ ] 9.6 Operator dormancy suite: `OpMemoryPolicy` is catalogued with a prior and produces no scored memory
      result at M20. → `internal/proposal/operator_test.go` (Test: `TestMemoryProposalRefusedNotScored`).

## 10. Documentation (docs)

- [x] 10.1 On phase completion, fold the two P17 capability specs into `openspec/specs/`. → deferred to the
      merge round (not this doc round); noted here so the fold is not forgotten.
- [x] 10.2 Cross-reference [P16](../p16-context-strategy-optimization/) at the memory-vs-context boundary in
      both the PRD (§8.2) and `design.md` (Decision 2), so the split is stated once and linked, not restated.
