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

- [x] 1.1 Add `internal/memoryruntime`: a `Store` interface, the **key scheme** (what scopes one
      conversation), and the **lifetime** (when an entry expires). → `internal/memoryruntime/store.go`
      (Test: `TestStoreKeySchemeScopesByNodeAndSession`).
- [x] 1.2 Implement `Recall` / `Record` for all five strategies as ONE dispatch over the closed set, so a
      sixth strategy cannot silently no-op. → `internal/memoryruntime/strategy.go`
      (Test: `TestEveryBuiltinStrategyImplementsRecallAndRecord`).
- [x] 1.3 🔴 **Determinism.** The same store state + the same params produce byte-identical recall output.
      → `internal/memoryruntime/strategy_test.go` (Test: `TestRecallDeterministic`).
- [x] 1.4 🔴 **Bounded by construction.** `scratchpad` never exceeds `max_entries`, `summary-buffer` never
      exceeds `max_tokens`, `vector-recall` never returns more than `top_k`. A strategy that could grow
      without bound is a memory leak in the customer's process.
      → `internal/memoryruntime/strategy_test.go` (Test: `TestStrategiesAreBounded`).
- [x] 1.5 🚫 **No provider call from the runtime.** `summary-buffer`'s summarization is a HOST service, not
      something the generated artifact calls — a generated file that reached a provider would put a
      credential in the customer's process. → `internal/memoryruntime/strategy.go`
      (Test: `TestRuntimeMakesNoProviderCall`).
- [x] 1.6 One definition of a strategy's behaviour, bound to the sealed vocabulary.
      → `internal/memoryruntime/strategy.go` (Test: `TestEveryBuiltinStrategyImplementsRecallAndRecord`).
      🔴 NOT as methods on `registry.MemoryStrategy`, as this task first said. Putting them there would
      drag a `Store` dependency into every consumer of a sealed definition, and would need ten delegating
      methods that can drift. The dispatch is keyed by strategy NAME, and a conformance test asserts the
      sealed vocabulary and the runtime name exactly the same set — which binds them without the import.

## 2. The generated artifact

- [x] 2.1 Emit a **dependency-free** memory module per language, alongside the call-site edit in the SAME
      patch, so one revert restores everything. → `internal/transform/memoryartifact.go`
      (Test: `TestArtifactShipsInTheSamePatch`).
- [x] 2.2 🔴 **Byte-identical regeneration.** The same resolved config regenerates the artifact byte-for-
      byte. → `internal/transform/memoryartifact_test.go` (Test: `TestArtifactRegeneratesByteIdentically`).
- [x] 2.3 The artifact carries the strategy and params **as data**, read from the binding document, so
      retuning a parameter is a document change rather than a code change (ADR-004's data/structure line).
      → `internal/transform/memoryartifact.go` (Test: `TestArtifactReadsParamsAsData`).
- [x] 2.4 🚫 The artifact imports nothing outside the language's standard library.
      → `internal/transform/memoryartifact_test.go` (Test: `TestArtifactIsDependencyFree`).

## 3. The call-site rewriter — Python

- [x] 3.1 **Recall**: replace the written `messages=` argument with a call into the generated module. A
      pure expression replacement of an argument the author already wrote.
      → `internal/transform/memorymaterialize_span.go` (Test: `TestPythonMemoryRecallMaterializes`).
- [x] 3.2 **Record**: insert the record statement after the call, gated on the call being a simple
      assignment at statement level. → `internal/transform/memorymaterialize_span.go`
      (Test: `TestPythonMemoryRecordMaterializes`).
- [x] 3.3 🔴 **BOTH HALVES OR REFUSE.** A call site that can carry the recall but not the record is
      REFUSED whole, naming which half is missing. A half-materialized memory reads from a store nothing
      fills — a behaviour the `config_hash` does not name.
      → `internal/transform/memorymaterialize_span.go` (Test: `TestHalfMaterializableMemoryRefusedWhole`).
- [x] 3.4 🔴 The rewritten source **reparses** (executed through `python3`) and only the call site's own
      two lines change — with **no newline introduced**, so engine.go's line-count invariant holds.
      → `internal/transform/memorymaterialize_span_test.go` (Test: `TestPythonMemoryEditIsMinimalAndReparses`).
- [x] 3.5 A `**kwargs` call site refuses with the CALL-SITE cause, not the platform cause — the reason is
      the unpacking, and it stays true after every rewriter lands.
      → `internal/transform/memorymaterialize_span.go` (Test: `TestKwargsCallSiteRefusesAboutTheCall`).

## 4. The call-site rewriter — Go

- [x] 4.1 Establish what Go's memory materialization actually needs, and refuse with THAT rather than
      with "unsupported". → `internal/transform/memorymaterialize.go`
      (Test: `TestGoMemoryRefusalNamesTheBlockedHalf`, `TestGoMemoryCoverageStatesTheMissingArtifact`).
- [x] 4.2 🔴 Both-halves-or-refuse holds in the AST engine: the ready half is not emitted alone.
      → `internal/transform/memorymaterialize.go` (Test: `TestGoMemoryRefusalNamesTheBlockedHalf`).
- [x] 4.3 🚫 Add the per-provider response-conversion table with **zero rows**, and a test that makes the
      emptiness a decision rather than an oversight — naming the three things a row owes.
      → `internal/transform/memorymaterialize.go` (Test: `TestMemoryResponseFormTableIsEmptyAndSaysWhy`).

**🔴 Go does not materialize memory, and the reason is specific rather than general.**

Go's **read** half would work today: a generic `Recall[T any](nodeID string, msgs []T) []T` is type-safe
against any SDK's message slice without importing it, and the registry row already locates the list
(`prompt: {field: "params.Messages"}`).

Its **write** half needs one thing. Recording a turn stores what was sent *and what came back* — and in
Go those are **different static types**: the call returns the SDK's response value while the store holds
its message-parameter value. In Python they are the same duck-typed dict, which is why `_as_message`
suffices there and nothing equivalent exists here. Converting between them is per-provider: the same
shape `skillbind.go`'s `toolValueForms` uses for tool values.

🚫 **Why the table ships empty instead of with a plausible row.** This module cannot compile against any
real SDK — the Go fixture is committed as `.txt` precisely because "a directory of real .go files
importing an SDK this module does not depend on would break `go build ./...` for the whole repo". A row
written here could not be verified to compile, let alone to behave, and would be emitted into a
customer's repository as a guess. ADR-001 names that as the top risk, with the wrong-but-compiling
version the worse half. An unverified row is not a partial capability.

**What a row owes**, recorded so the next person does not rediscover it: (1) a fixture that BUILDS
against that SDK, so the emission is compiled rather than assumed; (2) a conformance assertion that the
materialized behaviour matches `internal/memoryruntime`, the bar the Python module clears by execution;
(3) an `sdkNote` dating the spelling to an SDK generation, so it can be seen to rot.

## 5. Coverage, and lifting the refusal per cell

- [x] 5.1 `memoryCoverage` stops being uniform: it reads the materializer table, so a covered
      (language, strategy, call-shape) cell reports `materializes` and every other still refuses with its
      own cause. → `internal/transform/coverage.go` (Test: `TestMemoryCoverageReflectsMaterializers`).
- [x] 5.2 🔴 **The refusal is narrowed, never removed.** Every cell without a materializer still returns a
      typed `unsafeRewrite`, and the P17 totality canary still passes for those cells.
      → `internal/transform/p17_memory_test.go` (Test: `TestMemoryRefusalTotalityCanary`).
- [x] 5.3 🔴 `config_hash` is untouched: every P17 hash reproduces bit-for-bit. This phase changes what is
      EMITTED, never what a configuration IS.
      → `internal/variantspec/p17_memory_resolve_test.go` (Test: `TestNoneMemoryHashesAsAbsent`).

## 6. The operator wakes

- [ ] 6.1 `OpMemoryPolicy` is no longer dormant where the cell materializes: the proposal compiles to a
      real diff and becomes verifiable. → `internal/proposal/p17_memory_test.go`
      (Test: `TestMemoryProposalCompilesWhereMaterializable`).
- [ ] 6.2 🚫 It is STILL not scored where the cell refuses — the honesty contract survives the capability.
      → `internal/proposal/p17_memory_test.go` (Test: `TestMemoryProposalRefusedNotScored`).

## 7. The surfaces stop saying "no language has one"

- [x] 7.1 The console boundary now reports per-cell applicability instead of a flat no, still read from
      the engine's coverage table. → `web/console/src/app/app/memory/`
      (Test: `web/console/tests/memory.test.mjs`).
- [x] 7.2 🔴 The P17 copy that says the gap is language-independent must CHANGE, because it is no longer
      true. A surface that kept saying it would be lying in the opposite direction.
      → `web/console/src/app/app/memory/strategies.ts` (Test: `memory.test.mjs` — boundary-is-per-cell).

## 8. Verification on a real repository

- [x] 8.1 Re-run against hermes-agent and report what MOVED and what did not. → `cmd/p17hermes`.

**The finding: the count did not move, and the REASON did.** On hermes-agent@`528e335` (31 Python nodes)
the run is still **186 (node × strategy) combinations, 186 refusals, 0 diffs** — identical to P17's. What
changed is the sentence, and it is the whole value of the phase for this repository:

| | P17 | P18 |
|---|---|---|
| cause class | `CauseNoMaterializer` — **ours** | `CauseCallSiteShape` — **theirs** |
| the sentence | "no memory module has been generated for any language" | "this call site passes `**summary_kwargs`, so the request is assembled elsewhere; there is no written list here to read from or append to" |
| what the reader does | wait for us | write the messages at the call site, or apply the strategy where the mapping is built |
| lifespan | temporary | **permanent** — it stays true after every rewriter lands |

Coverage moved from **materializes=7** (only the `none` identity cells) to **materializes=11** — the four
non-identity Python strategies now materialize. hermes-agent does not benefit, because every one of its
31 call sites unpacks its arguments; a Python call site that writes its message list and assigns its
result does, and that path is executed end-to-end in `TestMemoryMaterializesEndToEnd`.

🚫 This is deliberately NOT reported as "186 refusals became 186 diffs". It did not, and the run says so
with a count rather than a claim.

---

## 9. 🔴 Where this stopped, and the exact reason

**18a is complete and landed. 18b is blocked on one named piece of engine work, and the materializer is
deliberately NOT dispatched until it lands.**

### What is done and verified

| § | Delivered | Verified by |
|---|---|---|
| 1 | The memory runtime — key scheme, count-based lifetime, `Recall`/`Record` for all five strategies | 7 tests; determinism over 20 repetitions; every bound asserted under 500 turns; both host refusals asserted |
| 2 | The generated artifact — dependency-free, byte-identical, params as data | Go↔Python **conformance test executes both** and compares; sabotaging Python retention turns it red |
| 3 | The Python recall + record edits and the **both-halves-or-refuse** gate | Real discovery, real spans; the resulting source is executed through `python3` to prove it parses |

### The blocker, precisely

The materializer emits `agentmem.recall(...)` at the call site. Nothing imports `agentmem`. Adding
`import agentmem` is an edit on **line 1** — an *untargeted line* — and `gateMinimal` rejects it:

> `the rewrite would change pipeline.py:1, which is outside the targeted call site; a transform may not
> edit untargeted lines`

That gate is correct and load-bearing. Crossing it needs a **new edit class with its own admission rule**
— the precedent `isSwap` (P15) and `bindingSite` (P13) already set — admitting exactly one import line at
the top of a file whose call site is being materialized, and nothing else.

🚫 **What was NOT done, and why.** Two shortcuts were available and both were refused:

- **Loosen `gateMinimal`.** It would remove the untargeted-line check from *every* rewriter in the
  package, forever, to serve one caller. That is the trade P15's decisions.md D-4 refuses by name.
- **Ship the recall edit anyway.** It emits code calling a module that is not imported — broken source in
  a customer's repository — and, if the import were somehow handled, a recall with no record is the
  half-materialization D2 exists to forbid.

So the span dispatch still points at P17's `spanRewriteMemory`, and **every memory override is still
refused**. Nothing half-built ships.

- [x] 9.1 Add the memory-materialization **edit class**: one import insertion at the top of a
      materialized file, admitted by its own rule, with the line-count invariant restated for it rather
      than relaxed for everyone. → `internal/transform/engine.go`
      (Test: `TestMemoryImportClassAdmitsOnlyItsOwnImport`).
- [x] 9.2 Wire `spanMaterializeMemory` into the span dispatch once 9.1 lands, and assert the artifact
      ships in the SAME patch as the call-site edit.
- [ ] 9.3 Then: §4 (Go), §5 (per-cell coverage), §6 (the operator wakes), §7 (the surfaces), §8 (hermes).

### What this does NOT change

🔴 P17's guarantees are all intact and re-asserted green: every memory override is refused with a typed
`unsafeRewrite`, the totality canary still turns 28 cells red under sabotage, `none` still hashes as
absent, and every P17 `config_hash` reproduces bit-for-bit.
