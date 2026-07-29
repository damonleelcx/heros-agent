# Tasks — P18: The Memory Runtime and the Call-Site Rewriter

The phase [P17](../p17-memory-strategy-optimization/) deferred. P17 modeled memory end-to-end and
**refused** at transform, naming exactly two missing artifacts: *a memory runtime (a store, a lifetime,
and a key scheme) plus the call-site rewriter that reads and writes it.* This phase builds both and lifts
the refusal **per cell** — never wholesale.

**The one decision everything else follows from: BOTH HALVES OR REFUSE.**

A memory strategy is a read *and* a write. Materializing only the read would produce an agent that recalls
from a store nothing ever fills, and materializing only the write would fill a store nothing ever reads.
Either way the node runs a behaviour the `config_hash` does not name — which is the *"scored a
configuration that never ran"* failure P17 exists to prevent, re-introduced one layer down and harder to
see. So a cell materializes when it can emit **recall AND record**, and refuses by name otherwise.

**Standing constraints.** The refusal never becomes a silent no-op — every cell that cannot materialize
still returns a typed `unsafeRewrite` (P17 D4 is unchanged, its scope narrows). `none` remains the
identity and still emits nothing. `config_hash` is untouched: this phase changes what the transform
*emits*, never what a configuration *is*, so every P17 hash reproduces bit-for-bit. The generated runtime
is **dependency-free and deterministic** — the same resolved config regenerates byte-identical files.

`🔴` = a security/must-fail test. `🚫` = a banned action. `→` = evidence pointer.

---

## 0. System Designer — fix the one-way-door contracts before any code (docs)

The P17 discipline, applied to a phase whose doors are of a different kind. P17's doors were about
**identity** (what a configuration IS) and are now frozen. P18's are about **behaviour** and about **what
ships into the customer's repository** — a generated file, keyed data, and a rewritten call site, none of
which can be un-shipped once a customer has run them.

- [x] 0.1 Record the **key scheme** `{node_id, session_id}`, both required, no tenant part. A one-way door
      over customer data: changing the key orphans every stored memory.
      → `decisions.md` D1 (rejected: node-only ⇒ cross-user leak, L1; session-only, L1/L2; a tenant part
      as a second isolation boundary, L1; defaulting an empty session, L1).
- [x] 0.2 🔴 Record **both halves or refuse** — the decision the phase turns on, and the one most exposed
      to shipping pressure. → `decisions.md` D2 (rejected: recall-only and record-only, which both run
      `none` under another strategy's hash, L1; emit-and-warn, because a warning is not a gate, L1).
- [x] 0.3 Record **no provider call from the runtime**; host services injected, a missing one refuses
      rather than substitutes. → `decisions.md` D3 (rejected: the artifact calling a provider — a
      credential in the customer's process, L1; truncation fallback, which is `scratchpad` wearing
      `summary-buffer`'s hash, L1).
- [x] 0.4 Record the **count-based lifetime** and sequence ordering — no clock anywhere.
      → `decisions.md` D4 (rejected: a TTL, which makes recall depend on when it runs and the axis
      unscorable, L2).
- [x] 0.5 Record **one definition of a strategy's behaviour**, called by the artifact rather than
      re-implemented. → `decisions.md` D5 (rejected: self-contained per-language retention — seven places
      `max_entries` can be off by one, each scored as the sealed strategy, L2 + 禁止分裂).
- [x] 0.6 Record that the refusal is **narrowed per cell, never removed**, and that the P17 canary must
      still pass for uncovered cells. → `decisions.md` D6 (rejected: flipping the axis to "supported",
      which is P17 D7's bait-and-switch one phase later, L1).
- [x] 0.7 🔴 Record that **`config_hash` is untouched** — this phase changes what is EMITTED, never what a
      configuration IS. → `decisions.md` D7 (rejected: hashing the key scheme, which fragments one
      configuration across every session that ran it, L2).

## 0b. Product + System Designer — the specs (docs)

- [x] 0.8 Author the PRD (14 sections). → `docs/prd/P18-memory-runtime.md`.
- [x] 0.9 Author the `proposal.md` (Why / What Changes / Impact). → `proposal.md`.
- [x] 0.10 Author `design.md` (Context / 7 decisions with rejected alternatives + level / interfaces /
      risks). → `design.md`.
- [x] 0.11 Author the `memory-runtime` spec delta. → `specs/memory-runtime/spec.md`.
- [x] 0.12 Author the `memory-materialization` spec delta, including both-halves-or-refuse, the per-cell
      narrowing, and the hash-stability requirements. → `specs/memory-materialization/spec.md`.

## 1. The memory runtime (the first missing artifact)

- [ ] 1.1 Add `internal/memoryruntime`: a `Store` interface, the **key scheme** (what scopes one
      conversation), and the **lifetime** (when an entry expires). → `internal/memoryruntime/store.go`
      (Test: `TestStoreKeySchemeScopesByNodeAndSession`).
- [ ] 1.2 Implement `Recall` / `Record` for all five strategies as ONE dispatch over the closed set, so a
      sixth strategy cannot silently no-op. → `internal/memoryruntime/strategy.go`
      (Test: `TestEveryBuiltinStrategyImplementsRecallAndRecord`).
- [ ] 1.3 🔴 **Determinism.** The same store state + the same params produce byte-identical recall output.
      → `internal/memoryruntime/strategy_test.go` (Test: `TestRecallDeterministic`).
- [ ] 1.4 🔴 **Bounded by construction.** `scratchpad` never exceeds `max_entries`, `summary-buffer` never
      exceeds `max_tokens`, `vector-recall` never returns more than `top_k`. A strategy that could grow
      without bound is a memory leak in the customer's process.
      → `internal/memoryruntime/strategy_test.go` (Test: `TestStrategiesAreBounded`).
- [ ] 1.5 🚫 **No provider call from the runtime.** `summary-buffer`'s summarization is a HOST service, not
      something the generated artifact calls — a generated file that reached a provider would put a
      credential in the customer's process. → `internal/memoryruntime/strategy.go`
      (Test: `TestRuntimeMakesNoProviderCall`).
- [ ] 1.6 Wire `Recall`/`Record` onto the `registry.MemoryStrategy` interface the P17 registry already
      ships, so there is ONE definition of what a strategy does — read by the runtime and by the
      materializer alike. → `internal/registry/memory_builtins.go`
      (Test: `TestStrategyBehaviourHasOneDefinition`).

## 2. The generated artifact

- [ ] 2.1 Emit a **dependency-free** memory module per language, alongside the call-site edit in the SAME
      patch, so one revert restores everything. → `internal/transform/memoryartifact.go`
      (Test: `TestArtifactShipsInTheSamePatch`).
- [ ] 2.2 🔴 **Byte-identical regeneration.** The same resolved config regenerates the artifact byte-for-
      byte. → `internal/transform/memoryartifact_test.go` (Test: `TestArtifactRegeneratesByteIdentically`).
- [ ] 2.3 The artifact carries the strategy and params **as data**, read from the binding document, so
      retuning a parameter is a document change rather than a code change (ADR-004's data/structure line).
      → `internal/transform/memoryartifact.go` (Test: `TestArtifactReadsParamsAsData`).
- [ ] 2.4 🚫 The artifact imports nothing outside the language's standard library.
      → `internal/transform/memoryartifact_test.go` (Test: `TestArtifactIsDependencyFree`).

## 3. The call-site rewriter — Python

- [ ] 3.1 **Recall**: replace the written `messages=` argument with a call into the generated module. A
      pure expression replacement of an argument the author already wrote.
      → `internal/transform/memorymaterialize_span.go` (Test: `TestPythonMemoryRecallMaterializes`).
- [ ] 3.2 **Record**: insert the record statement after the call, gated on the call being a simple
      assignment at statement level. → `internal/transform/memorymaterialize_span.go`
      (Test: `TestPythonMemoryRecordMaterializes`).
- [ ] 3.3 🔴 **BOTH HALVES OR REFUSE.** A call site that can carry the recall but not the record is
      REFUSED whole, naming which half is missing. A half-materialized memory reads from a store nothing
      fills — a behaviour the `config_hash` does not name.
      → `internal/transform/memorymaterialize_span.go` (Test: `TestHalfMaterializableMemoryRefusedWhole`).
- [ ] 3.4 🔴 The rewritten source **reparses** and the edit is minimal — no untargeted line is touched.
      → `internal/transform/memorymaterialize_span_test.go` (Test: `TestPythonMemoryEditIsMinimalAndReparses`).
- [ ] 3.5 A `**kwargs` call site refuses with the CALL-SITE cause, not the platform cause — the reason is
      the unpacking, and it stays true after every rewriter lands.
      → `internal/transform/memorymaterialize_span.go` (Test: `TestKwargsCallSiteRefusesAboutTheCall`).

## 4. The call-site rewriter — Go

- [ ] 4.1 Recall + record at a Go call site, through the generated Go artifact.
      → `internal/transform/memorymaterialize.go` (Test: `TestGoMemoryMaterializes`).
- [ ] 4.2 🔴 Both-halves-or-refuse holds identically in the AST engine.
      → `internal/transform/memorymaterialize.go` (Test: `TestGoHalfMaterializableRefusedWhole`).

## 5. Coverage, and lifting the refusal per cell

- [ ] 5.1 `memoryCoverage` stops being uniform: it reads the materializer table, so a covered
      (language, strategy, call-shape) cell reports `materializes` and every other still refuses with its
      own cause. → `internal/transform/coverage.go` (Test: `TestMemoryCoverageReflectsMaterializers`).
- [ ] 5.2 🔴 **The refusal is narrowed, never removed.** Every cell without a materializer still returns a
      typed `unsafeRewrite`, and the P17 totality canary still passes for those cells.
      → `internal/transform/p17_memory_test.go` (Test: `TestMemoryRefusalTotalityCanary`).
- [ ] 5.3 🔴 `config_hash` is untouched: every P17 hash reproduces bit-for-bit. This phase changes what is
      EMITTED, never what a configuration IS.
      → `internal/variantspec/p17_memory_resolve_test.go` (Test: `TestNoneMemoryHashesAsAbsent`).

## 6. The operator wakes

- [ ] 6.1 `OpMemoryPolicy` is no longer dormant where the cell materializes: the proposal compiles to a
      real diff and becomes verifiable. → `internal/proposal/p17_memory_test.go`
      (Test: `TestMemoryProposalCompilesWhereMaterializable`).
- [ ] 6.2 🚫 It is STILL not scored where the cell refuses — the honesty contract survives the capability.
      → `internal/proposal/p17_memory_test.go` (Test: `TestMemoryProposalRefusedNotScored`).

## 7. The surfaces stop saying "no language has one"

- [ ] 7.1 The console boundary now reports per-cell applicability instead of a flat no, still read from
      the engine's coverage table. → `web/console/src/app/app/memory/`
      (Test: `web/console/tests/memory.test.mjs`).
- [ ] 7.2 🔴 The P17 copy that says the gap is language-independent must CHANGE, because it is no longer
      true. A surface that kept saying it would be lying in the opposite direction.
      → `web/console/src/app/app/memory/strategies.ts` (Test: `memory.test.mjs` — boundary-is-per-cell).

## 8. Verification on a real repository

- [ ] 8.1 Re-run against hermes-agent and report what MOVED and what did not.
      → `cmd/p17hermes` (extended) or `cmd/p18hermes`.
