# Tasks — P18: Harness Strategy Optimization

Three waves. **Wave 18a** = the strategy catalog and the harness dimension — modelled, resolved, hashed,
scored by the unchanged eval harness, and **refused** at transform. **Wave 18b** = the operator and the
cost/quality admissibility gate. **Wave 18c** (§10–§14, below) = the two artifacts 18a's refusal named as
missing — a **harness runtime** and a **call-site rewriter** — plus the **authored change**, so a user can
change their own scaffold rather than waiting for the operator to propose one.

**Standing constraints.** The axis is added strictly through the canonical eight-step checklist
(authoring kit §"add an axis"). Harness is a **closed `Dimension`**, a **content-addressed registry
Kind**, and an **additive `omitempty` hashed field** — a no-harness config hashes **byte-identically** to
its pre-P18 form. A `HarnessRef` at transform is **refused with a typed `unsafeRewrite`, never silently
dropped** (mirrors `refuseSkills`, `internal/transform/rewrite.go:388`). **No new eval metric, no scoring
change** — the axis rides `task_success`/`eval_cost_usd`/`eval_latency_ms`. Autonomous turns run in the
node's **existing** P3 sandbox and grant. 🚫 No runtime topology engine is resurrected. 🚫 A harness
override never reorders nodes (that is P15).

The doc tasks below are `[x]`; the code tasks and their tests are `[ ]` until they are built **and their
tests are green** — a `[x]` next to a `(Test: Name)` pointer is a claim that the named test exists and
passes.

---

## 1. System Designer — Fix the one-way-door contracts before any code (18a)

- [x] 1.1 Ratify the new registry **Kind `harness`** and its `harness_entry` table as a one-way door. →
      `decisions.md` D1; `internal/registry/registry.go:57`.
- [x] 1.2 Ratify **`DimHarness`** as a new closed `Dimension`, and the additive-`omitempty` shape for the
      `NodeOverride`/`ResolvedNode` field (byte-identical no-harness hash). → `decisions.md` D2, D3;
      mirrors `resolved.go` D-1.4.
- [x] 1.3 Ratify the **interim-refusal contract** (refuse-never-drop) and the **harness-vs-wiring
      boundary** (compose with P15, never reorder). → `decisions.md` D4, D5.
- [x] 1.4 Ratify the **cost/quality admissibility rule** (Δtask_success vs Δcost/Δlatency on held-out) and
      the **L1 blast-radius** note (autonomous turns run in the existing sandbox; surface observable). →
      `decisions.md` D6, D7.

## 2. System Designer + Backend — The harness registry Kind (18a, checklist step 5)

- [x] 2.1 Add Kind `harness` and a `harness_entry` table; seal/decode by content address like the other
      four Kinds. → `internal/registry/registry.go` (`KindHarness`, `tableHarness`), new
      `internal/registry/harness.go` (Test: `TestHarnessSealIsContentAddressed`).
- [x] 2.2 Define `HarnessSpec` with a params schema — `max_turns`, stop condition, optional critic model
      ref, retry budget — with params inapplicable to a strategy **inexpressible**, not ignored. →
      `internal/registry/harness.go` (Test: `TestHarnessParamsSchemaPerStrategy`).
- [x] 2.3 🔴 Validate params **at seal**: `max_turns` bounded positive, a declared critic ref resolves to
      a `model` entry, retry budget bounded. An out-of-range/unresolvable param fails registration, not
      resolution. → `internal/registry/harness.go` (Test: `TestHarnessParamsValidatedAtSeal`).
- [x] 2.4 Seed the five builtin strategies (`single-shot`, `react-loop`, `plan-execute`, `reflexion`,
      `critic-loop`); `critic-loop` carries a **separate** critic model ref. →
      `internal/registry/harness_builtins.go` (Test: `TestFiveBuiltinStrategiesRegister`).
- [x] 2.5 🔴 Enforce cross-registry uniqueness: a `harness` ref used in a non-harness dimension (or vice
      versa) fails closed. → `internal/registry/registry.go` (Test: `TestHarnessRefFailsClosedCrossKind`).

## 3. Backend — The Dimension, override, and resolution (18a, checklist steps 1–4)

- [x] 3.1 Add `DimHarness` to the closed `Dimension` enum. → `internal/variantspec/spec.go:42` (Test:
      `TestDimHarnessInClosedEnum`).
- [x] 3.2 Add additive `omitempty` `NodeOverride.HarnessRef` with `isEmpty`/`Refs`/`Validate`
      participation; 🚫 inline strategy definitions rejected (ref-only). →
      `internal/variantspec/spec.go:183` (Test: `TestHarnessRefIsRefOnly`).
- [x] 3.3 Add the `DimHarness` block to `resolveNode` and the `Dimensions()` entry: override → registry
      entry pinned by `version_id`; absent → discovered default pinned by `source_revision`. →
      `internal/variantspec/resolve.go:67,154` (Test: `TestResolveHarnessOverrideAndDefault`).
- [x] 3.4 Add the **auto-hashed** additive `omitempty`, nil-when-empty resolved harness field. →
      `internal/variantspec/resolved.go:46` (Test: `TestResolvedHarnessAutoHashed`).

      🔴 **CORRECTION to what this task first said** (`decisions.md` D-8). It said
      `ResolvedNode.HarnessRef` — the registry `version_id`. That contradicts the frozen doctrine of
      `resolved.go`: `config_hash` denotes a **configuration**, not a set of registry rows, so two entries
      spelling one strategy with one params set must share a hash. A `version_id` in the projection forks
      one configuration per entry, permanently. The field is therefore
      `ResolvedNode.Harness *ResolvedHarness{Strategy, Params}` — the shape `ResolvedMemory` already
      proved. The **spec** field stays `NodeOverride.HarnessRef string` (a ref), exactly as 3.2 says.
- [x] 3.5 🔴 **Test — backward-compatible identity**: a config declaring no harness hashes
      **byte-identically** to its pre-P18 form; every existing golden vector reproduces. →
      `internal/variantspec/p18_harness_resolve_test.go` (Test: `TestNoHarnessHashesByteIdentical`; the
      pre-existing golden vectors are re-asserted unchanged by `resolved_config_golden_test.go`).
- [x] 3.6 Test — a harness change is the **only** edit that changes an otherwise-identical `config_hash`;
      `single-shot` on a one-call node is a no-op on the hash. → (Test: `TestHarnessChangeChangesHashOnly`).
- [x] 3.7 🔴 Test — fail-static: an unresolvable/malformed `HarnessRef` fails the resolve closed naming the
      ref, never falling back to a different strategy. → (Test: `TestHarnessRefFailStaticNamesRef`).

## 4. Backend — Discovery frontend for the discovered default (18a, checklist step 6)

- [x] 4.1 Add the **additive** IR field recording a node's discovered default harness; a discovery
      frontend defaults it to `single-shot` unless a loop is proven at the call site. →
      `internal/discovery/emit.go` (`IRNode.Harness`, `HarnessDefault`, `omitDefaultHarness`) +
      `extract.go` (`deriveHarness`) (Test: `TestDiscoveredHarnessDefaultsSingleShot`).

      🔴 The floor is the identity because P1's evidence cannot prove a loop. `invocationFor` already
      records `loop` when a call sits inside one, and reaching for it here is the obvious move — but a
      `for` over a list of tickets fires one node many times with NO scaffold, while an agent loop is the
      MODEL choosing another turn. Loop depth cannot tell them apart, and emitting `react-loop` for a
      `for` would hash a configuration nobody authored. Asserted, not just argued: a call site at
      `loopDepth: 1` still derives `single-shot`.
- [x] 4.2 Test — the additive IR field breaks no existing IR consumer (absent field, byte-compatible). →
      (Test: `TestIRHarnessFieldAdditive`).

## 5. Backend — The refusing rewriter (18a, checklist step 7 — the honesty seam)

- [x] 5.1 🔴 Refuse a resolved config carrying a `HarnessRef` with a typed `unsafeRewrite` naming the
      strategy and the reason (materializing a control loop is code generation, not an argument swap), on
      **both** engines — mirroring `refuseSkills`. → `internal/transform/rewrite.go:54` + `rewrite_span.go:59`
      (`internal/transform/harnessrefuse.go` `refuseHarness`, dispatched via `materializeHarness` /
      `spanMaterializeHarness`) (Test: `TestHarnessRefusedAtTransform`).

      🔴 `single-shot` is the IDENTITY and is NOT refused — one turn is exactly the un-rewritten call
      site, so it emits nothing and a user selecting it is never told their no-op failed
      (Test: `TestSingleShotHarnessIsANoOpAtTransform`). And the three strategies needing a host
      service (`react-loop`, `plan-execute`, `critic-loop`) are refused with `CauseNotAtCallSite` and
      **no missing artifact**, in every language, because a call site has nowhere to inject a tool
      executor, a planner or a critic (Test: `TestHostServiceStrategiesRefusedByName`).
- [x] 5.2 🔴 **Test — refuse, never drop**: the resolved config still carries the `HarnessRef` and the
      transform **refuses** rather than emitting an incorrect loop or silently succeeding. The test must
      fail if the override is silently dropped. → (Test: `TestHarnessRefusedNotSilentlyDropped`).
- [x] 5.3 Refuse a **group** harness (a `HarnessRef` scoped to an ordered edge set) the same way, naming
      the edge set. → `internal/variantspec/spec.go` (`HarnessGroup`, and the 🔴 validator that rejects an
      edge the spec does not declare — which is how "compose with P15's wiring, never re-derive it"
      became mechanical), `internal/variantspec/resolved.go` (`ResolvedHarnessGroup`, additive+omitempty),
      `internal/transform/harnessrefuse.go` (`checkGroupHarness`, decided in `generate` **before** any
      file is read, so a refused group can never leave a partial diff behind)
      (Test: `TestGroupHarnessRefusedNamingEdgeSet`).

## 6. AI Engineer — The operator and admissibility (18b, checklist step 8)

- [x] 6.1 Add `OpHarnessStrategy` operator kind, a catalog row, and a prior for verification ordering. →
      `internal/proposal/operator.go` (`OpHarnessStrategy`, `SignalScaffoldMismatch`) + `catalog.go`
      (`harnessStrategyOp`, `setHarness`) + `menu.go` (`HarnessChoice`) + `gain.go` (prior 0.35, the
      highest in the table; order hint 6, the most expensive to verify — a heavier scaffold pays
      `max_turns` model calls per case)
      (Test: `TestOpHarnessStrategyInCatalog`, `TestHarnessProposeSetsOnlyTheHarnessRef`).

      🔴 **A defect found in P17's code and fixed here.** `cloneOverride` silently DROPPED `MemoryRef`,
      so any proposal derived from a baseline that bound a memory strategy also **un-bound** it — the
      candidate's `config_hash` then differed from the baseline in TWO dimensions while its
      `Dimensions()` claimed one, and the eval attributed the whole delta to the dimension the operator
      named. Fixed for both `MemoryRef` and `HarnessRef`, plus `VariantSpec.HarnessGroups` one level up
      (Test: `TestCloneOverrideCarriesEveryDimension`, `TestCloneSpecCarriesGroupHarnesses`).
- [x] 6.2 Emit harness-override Variant Specs routed through P5.5 verification; 🚫 no scaffold swap ships
      on an unverified opinion. → `internal/proposal/catalog.go` (the rationale states the trade-off and
      claims **no outcome in either direction** — the operator is pure, never sees the call site or the
      eval, and a claim it cannot check is a claim it should not make)
      (Test: `TestHarnessSwapIsVerificationGated`).
- [x] 6.3 🔴 **Admissibility gate**: a heavier harness is admitted over a lighter one **only** when the
      measured `task_success` gain outweighs its added `eval_cost_usd` and `eval_latency_ms`, computed on
      **held-out** cases. → `internal/proposal/harness_op.go` (`AdmitHarnessSwap`, and the named
      `HarnessQualityPerCostDoubling` exchange rate — a POLICY written as a readable constant rather than
      a threshold buried in an expression). Latency is judged alongside cost, taking the worse of the
      two, because a user waiting ten times as long is paying too
      (Test: `TestHarnessAdmissibleOnlyWhenCostEarned`).
- [x] 6.4 🔴 Test — a scaffold that raises cost/latency **without** a commensurate `task_success` gain is
      **rejected**; and the admissibility set is disjoint from the proposal's tuning cases (no leak). →
      (Test: `TestHeavierHarnessCostWinRejected`, `TestAdmissibilityHeldOutDisjoint`).

      🔴 The gate FAILS CLOSED on absent evidence: an empty held-out set is a refusal, not a pass, or
      forgetting to supply one would silently admit every scaffold. And one sub-test asserts a
      **positive but insufficient** gain is rejected — without it the whole suite could pass because
      every gain happened to be non-positive.

## 7. Backend + AI Engineer — Bounded autonomy and sandbox containment (18b)

- [x] 7.1 🔴 Enforce that every multi-turn strategy declares a bounded `max_turns` and stop condition; a
      run reaching the ceiling terminates and is recorded, never hangs. 🚫 No strategy can express an
      unbounded loop. → `internal/registry/harness_builtins.go` (`MaxTurnsCeiling`, expressed as each
      schema's own `maximum` so the bound is enforced at seal) + `internal/harnessruntime/run.go` (the
      ceiling is a `for` bound, not a break inside the body, so **no strategy can talk its way past it**)
      (Test: `TestMaxTurnsBoundedAndTerminates`, `TestBoundedByConstruction`,
      `TestCeilingIsRecordedDistinctly`).
- [x] 7.2 🔴 Guarantee autonomous turns run within the node's **existing** P3 sandbox and tool grant; the
      added turns reach no egress destination or tool outside that grant, and the enlarged turn/tool
      surface is **observable** in the trace. → (Test: `TestHarnessTurnsStayInExistingGrant`,
      `TestHarnessSurfaceObservableInTrace`).

## 8. QA — Acceptance gate (18a + 18b)

- [x] 8.1 Hash suite: harness-only change moves the hash; no-harness config byte-identical; `single-shot`
      no-op. → (Test: `TestHashParticipationSuite`).
- [x] 8.2 🔴 Refusal suite: `HarnessRef` refused with a typed error on both engines; refuse-never-drop
      asserted; group harness refused naming the edge set. → (Test: `TestRefusalSuite`).
- [x] 8.3 Scored-with-no-change suite: a harness variant is scored by the existing eval harness with no
      new metric and no scoring change. → (Test: `TestHarnessScoredByExistingHarness`).
- [x] 8.4 🔴 Admissibility suite: cost-only win rejected on held-out; disjoint held-out set. → (Test:
      `TestAdmissibilitySuite`).
- [x] 8.5 🔴 Safety suite: bounded `max_turns`; no new egress/tool scope; surface observable. → (Test:
      `TestBoundedAutonomyAndContainmentSuite`).
- [x] 8.6 Wiring-boundary suite: a group harness composes with P15's ordered edge set and never reorders.
      → (Test: `TestHarnessComposesWithWiringNoReorder`).

## 9. Documentation

- [x] 9.1 PRD (14 sections). → `docs/prd/P18-harness-strategy-optimization.md`.
- [x] 9.2 OpenSpec change set — proposal, tasks, design, decisions, and the two capability spec deltas. →
      `openspec/changes/p18-harness-strategy-optimization/`.
- [x] 9.3 Record the one-way-door contracts (new Kind, new Dimension, new DB table, interim-refusal,
      harness-vs-wiring boundary, cost/quality admissibility) with their 八级法则 tags. → `decisions.md`.
- [ ] 9.4 On deploy, fold the capability deltas into `openspec/specs/`. →
      `openspec/specs/{harness-strategy,agent-loop,harness-runtime,harness-materialization,harness-authoring}/spec.md`
      (deferred to the build round).
- [x] 9.5 **Wave 18c docs, authored before its code** (the same order §1 imposed on 18a). The PRD gains an
      addendum — goals G12–G17, FR22–FR45, NFR10–NFR15, acceptance A15–A26
      (`docs/prd/P18-harness-strategy-optimization.md` §15, §16); `decisions.md` gains D-8 … D-13
      (including the 🔴 D-8 correction to task 3.4); `design.md` gains Addendum Decisions 8–12; and three
      capability deltas are added — `harness-runtime`, `harness-materialization` (which **MODIFIES**
      `agent-loop`'s refusal requirement, narrowing it per cell), and `harness-authoring`.

---

# Wave 18c — the harness runtime, the call-site rewriter, and the authored change

Section 5 refuses a `HarnessRef` at transform because *"materializing a control loop is code generation."*
That sentence names exactly two missing artifacts: **a harness runtime** — a bounded loop, a stop
condition, a continuation rule — and **the call-site rewriter** that drives it. This wave builds both, and
adds the third thing the axis was missing: a way for a **user** to make an active change to their harness
strategy. Contracts of record: `decisions.md` D-8 … D-13; design reasoning: `design.md` Addendum
Decisions 8–12.

**The one decision everything else follows from: DRIVE AND DECIDE, OR REFUSE.**

A harness is a loop, and a loop is two separable capabilities: *driving* the call again, and *deciding*
whether to run again. Emitting only the drive half yields a fixed-turn loop that burns N calls and
discards N−1 answers — `single-shot` run N times and priced N times, reported under a `config_hash` that
claims a self-correcting scaffold. Emitting only the decide half yields a strategy that can tell it should
continue and cannot. Either way the node runs a behaviour its `config_hash` does not name, which is the
*"scored a configuration that never ran"* failure §5 exists to prevent, re-introduced one layer down.

**Standing constraints.** The refusal is **narrowed per cell, never removed** — every cell without a
materializer still returns a typed `unsafeRewrite`, and §5's canary still passes for it. `single-shot`
remains the identity and still emits nothing. `config_hash` is **untouched**: this wave changes what the
transform *emits* and what a surface *offers*, never what a configuration *is*, so every 18a hash
reproduces bit-for-bit. The generated artifact is **dependency-free and deterministic**, and it makes
**no provider call and dispatches no tool**.

`🔴` = a security/must-fail test. `🚫` = a banned action. `→` = evidence pointer.

## 10. Backend + AI Engineer — the harness runtime (the first missing artifact)

- [x] 10.1 Add `internal/harnessruntime`: the closed `StopReason` set, a `TurnRecord`, and a `Result`
      carrying the answer, the turn count, the stop reason and the per-turn trace. →
      `internal/harnessruntime/run.go` (Test: `TestCeilingIsRecordedDistinctly`,
      `TestHarnessSurfaceObservableInTrace`).
- [x] 10.2 Implement `Plan` for all five strategies as ONE dispatch over the closed set, so a sixth
      strategy cannot silently no-op into a single shot. → `internal/harnessruntime/strategy.go`
      (Test: `TestEveryBuiltinStrategyHasALoopDefinition`).
- [x] 10.3 🔴 **Bounded by construction.** No strategy and no params combination executes more than the
      sealed `max_turns`; a run that reaches the ceiling terminates, returns the last answer, and records
      `StopCeiling`. → `internal/harnessruntime/run.go`
      (Test: `TestMaxTurnsBoundedAndTerminates`, `TestCeilingIsRecordedDistinctly`).
- [x] 10.4 🔴 **Determinism.** The same strategy, params and answers produce the same turn count, stop
      reason and per-turn record on every execution; no clock, no random source. →
      `internal/harnessruntime/strategy_test.go` (Test: `TestLoopDeterministic`).
- [x] 10.5 🚫 **No provider call, no tool dispatch.** A planner, a tool executor and a critic are injected
      host services; the runtime reaches nothing else. →
      `internal/harnessruntime/host.go` (Test: `TestRuntimeMakesNoProviderCall`).
- [x] 10.6 🔴 A strategy whose host service is absent **refuses by name** rather than substituting a
      lighter loop — `critic-loop` without a critic is not `reflexion`. →
      `internal/harnessruntime/host.go` (Test: `TestMissingHostServiceRefusesByName`).
- [x] 10.7 🔴 Autonomous turns reach nothing outside the call they re-invoke, and the enlarged surface is
      observable in the trace. → (Test: `TestHarnessTurnsStayInExistingGrant`,
      `TestHarnessSurfaceObservableInTrace`).

## 11. Backend — the generated artifact and the call-site rewriter (the second missing artifact)

- [x] 11.1 Emit a **dependency-free** harness module per covered language, alongside the call-site edit in
      the SAME patch, so one revert restores both. → `internal/transform/harnessartifact.go`
      (Test: `TestHarnessArtifactShipsInTheSamePatch`, `TestHarnessArtifactIsDependencyFree`).
- [x] 11.2 🔴 **Byte-identical regeneration**, and params read **as data** from the binding document. →
      `internal/transform/harnessartifact_test.go`
      (Test: `TestHarnessArtifactRegeneratesByteIdentically`; the params-as-data half is asserted inside
      `TestHarnessMaterializesEndToEnd`, which checks the emitted module is byte-identical to the
      constant — nothing about the node or its params is templated in).

      🔴 And the emitted module is a SECOND implementation of the loop `internal/harnessruntime` defines,
      which is only safe while something proves the two agree: `TestHarnessArtifactMatchesTheRuntime`
      executes both over the same strategies, params and answer sequences and compares turn counts.
- [x] 11.3 **Python drive + decide**: replace the written call expression with a call into the generated
      module, passing the call as a re-invocable thunk and the written message list as the loop's input.
      ONE edit, and the file's line count is preserved EXACTLY. →
      `internal/transform/harnessloop_span.go` (Test: `TestPythonHarnessMaterializes`,
      `TestPythonHarnessEditIsMinimalAndReparses`).

      🔴 This needed the call's own extent, which no analyzer exposed — the memory rewriter worked
      around its absence by appending a second statement with a `;` and recorded that *"deriving one by
      scanning for balanced parens is precisely the guess rewrite_span.go declines"*, naming a call span
      as the clean follow-up. That follow-up landed here: `discovery.SpanCallSite.CallSpan`, read from
      the tree-sitter node the analyzer already had. It is a read, not a heuristic.

      🔴 The line count is preserved by ARITHMETIC rather than by luck: the written message text moves
      from inside the call to after it, so its newlines are removed once and re-added once. The rewriter
      checks that equality before emitting, so a future change that broke it stops there rather than at
      the minimality gate with a worse error.
- [x] 11.4 🔴 **DRIVE AND DECIDE OR REFUSE.** A call site that can be re-invoked but whose response cannot
      be evaluated against the stop condition is REFUSED whole, naming the missing half; both halves are
      resolved before the first edit is emitted. →
      `internal/transform/harnessloop_span.go` (Test: `TestHalfMaterializableHarnessRefusedWhole`).

      🔴 What the two halves are, at a Python call site: DRIVE is the call's own span (it can be
      re-invoked as a thunk); DECIDE is a WRITTEN message list (the continuation appends the previous
      answer to it). A `**kwargs` call has the first and not the second, and the loop it would allow is
      the identical question asked N times — `single-shot` at N times the price under a multi-turn
      `config_hash`. It refuses with the CALL-SITE cause, which stays true after every rewriter lands.
- [x] 11.5 🔴 A strategy needing a **host service** is refused at the call site naming the service —
      `react-loop` (tool execution), `plan-execute` (planning + step execution), `critic-loop` (a separate
      critic call) — and is never degraded to a strategy that does not need one. →
      `internal/transform/harnessmaterialize.go` (Test: `TestHostServiceStrategiesRefusedByName`).
- [x] 11.6 **Go**: `single-shot` is the identity and emits nothing; every multi-turn strategy refuses with
      `CauseNotAtCallSite` and **no missing artifact**, because a Go response is the customer's SDK type
      and its text is not readable without importing their SDK. 🚫 Not `CauseNoMaterializer` — naming an
      artifact would promise work that would not help. → `internal/transform/harnessmaterialize.go`
      (Test: `TestGoHarnessIdentityOnlyAndRefusalIsPermanent`).

      🔴 Go IS in `harnessMaterializers`, and that is deliberate rather than contradictory: the entry
      means Go's engine ANSWERS for the dimension — identity materializes, multi-turn refuses
      permanently — rather than having no answer. It is what routes Go's cells to
      `CauseNotAtCallSite` instead of a promise the platform cannot keep.
- [x] 11.7 `harnessCoverage` stops being uniform: it reads the materializer table, so a covered
      (language, strategy) cell reports `materializes` and every other still refuses with its own cause. →
      `internal/transform/coverage.go` (Test: `TestHarnessCoverageReflectsMaterializers`).
- [x] 11.8 🔴 **The refusal is narrowed, never removed.** Every cell without a materializer still returns a
      typed `unsafeRewrite`, and §5's totality canary still passes for those cells. →
      `internal/transform/p18_harness_test.go` (Test: `TestHarnessRefusalTotalityCanary`).
- [x] 11.9 🔴 `config_hash` is untouched: every 18a hash reproduces bit-for-bit. This wave changes what is
      EMITTED, never what a configuration IS. →
      `internal/variantspec/p18_harness_resolve_test.go` (Test: `TestSingleShotHarnessHashesAsAbsent`).

## 12. Product + Frontend — the authored change (users change their own scaffold)

- [x] 12.1 Add the **validate-without-register** path the surface calls on every keystroke: the strategy is
      in the closed set, the params are JSON, and they satisfy the strategy's schema — one validator, two
      callers, no write and no database round-trip. → `internal/registry/harness.go`
      (Test: `TestValidateHarnessParamsPerformsNoWrite`).
- [x] 12.2 🔴 **Clearing reproduces the prior hash byte-identically**, and `single-shot` with no params is
      indistinguishable from cleared. → `internal/variantspec/p18_harness_resolve_test.go`
      (Test: `TestClearingHarnessBacksOutWithNoResidue`).
- [x] 12.3 A `/app/harness` console surface with the new UI design: per-node strategy selection from the
      closed set, params from the schema, and a **clear** control. 🚫 No free-text strategy path. →
      `web/console/src/app/app/harness/` (`page.tsx`, `authoring.tsx`, `strategies.ts`), registered in
      the shell and the command path (`web/console/src/app/app/layout.tsx`, `src/lib/routes.ts`), plus
      the backend it mirrors — `internal/authoring/harness.go` and `Edit.HarnessRef`
      (Test: `web/console/tests/harness.test.mjs`, `TestHarnessStrategyOptionsAreTheClosedSet`,
      `TestValidateHarnessSelectionRejectsBeforeSealing`, `TestHarnessEditSetsAndClears`).

      🔴 **New UI, and the new thing is the TURN METER.** A number in a sentence is skippable; a filled
      bar beside a one-segment baseline is not. Every option carries one, drawn at the same scale, so
      "up to 16×" cannot read as the same size as "1 turn". 🚫 It is not a rating and carries no colour
      judgement — a longer bar is more expensive, not worse.

      🔴 **And a LANGUAGE SWITCH**, because the boundary genuinely differs per language. Verified in
      Chrome at `/app/harness` (dev-auth console, port 4398): switching Python→Go moves `reflexion` from
      **applies** to **not in this language**, while the three host-service strategies stay **not at a
      call site** in both — the per-cell answer, visible rather than asserted. A `/preview/p18` route
      renders the same component with no session, for the same reason `/preview/p15` exists.
- [x] 12.4 🔴 The **per-cell** boundary is stated BEFORE the choice, read from the engine's own coverage
      source rather than a second sentence — and `single-shot` is never presented as refused. →
      `web/console/src/app/app/harness/strategies.ts` (Test: `harness.test.mjs` — boundary-is-per-cell).
- [x] 12.5 🔴 The **added per-run cost** of a heavier scaffold is stated before the choice: a strategy whose
      ceiling exceeds one turn may multiply cost and latency up to that ceiling, and whether that is worth
      it is verification's answer, not the selection's. 🚫 No metric improvement is attributed to an
      unverified authored change. → `internal/authoring/harness.go` (`harnessCostWarning`, and
      `harnessCeilingFromSchema`, which reads the ceiling from the SCHEMA so the number a user is warned
      about is the number the seal enforces)
      (Test: `harness.test.mjs` — cost-stated-before-choice; `TestHarnessCostIsStatedBeforeTheChoice`).
- [x] 12.6 🚫 **No second apply path.** The authored change rides `NodeOverride.HarnessRef` through the
      same resolver, the same transform and the same admissibility gate a proposal passes. →
      (Test: `TestHarnessEditSetsAndClears`, `TestClearingHarnessBacksOutWithNoResidue`; and structurally
      — `Edit.HarnessRef` derives the SAME `NodeOverride.HarnessRef` the operator sets, so there is one
      resolver, one transform and one gate rather than a second path to keep true).

## 13. QA — the wave-18c acceptance gate

- [x] 13.1 Runtime suite: bounded, terminating, deterministic, ceiling recorded, host-service refusal by
      name, no provider call. → (Test: `TestHarnessRuntimeSuite`).
- [x] 13.2 🔴 Materialization suite: both-halves-or-refuse; `single-shot` emits nothing; host-service
      strategies refused by name; artifact dependency-free, byte-identical, same patch. →
      (Test: `TestHarnessMaterializationSuite`).
- [x] 13.3 🔴 Coverage suite: the coverage read and the transform agree for every cell; every uncovered
      cell still returns a typed `unsafeRewrite`. → (Test: `TestHarnessCoverageAgreesWithEngine`).
- [x] 13.4 🔴 Identity suite: every 18a `config_hash` reproduces bit-for-bit; clearing backs out with no
      residue. → (Test: `TestHarnessIdentityUntouchedSuite`).

## 14. Verification on a real repository

- [x] 14.1 Run the axis against the real `nousresearch/hermes-agent` and report what MOVED and what did
      not, counted by cause and by shape rather than by sample. → `cmd/p18hermes`.

**The finding: this axis REACHED THE SOURCE, and most of it still refuses — both counted, neither
claimed.** On hermes-agent@`528e335` (31 Python nodes, 6 sealed entries = **186** node × strategy
combinations):

| outcome | count | what it means |
|---|---|---|
| the IDENTITY, emitting nothing | 31 | `single-shot` is the un-rewritten call site. No diff is the CORRECT diff, not a missing one. |
| **a LOOP written into the source** | **2** | `reflexion` at `agent/bedrock_adapter.py:1516`, both entries, with `agentharness.py` + `agentharness.json` in the same patch. |
| refused, typed | 153 | 93 permanent-in-every-language + 60 on this repository's call-site shape. |
| refused for another reason | 0 | |

🔴 **This is the first axis since model and prompt to write real source into this repository** — the
memory phase's equivalent run was 186 refusals and 0 diffs. The 2 are counted as their own row, not
folded in with the identity's empty diff: a single "materialized" count over both would let one run
claim a loop was written when none was, and the next claim none was when one had been.

🔴 **ZERO of the 153 refusals is blamed on a missing materializer.** Not one is waiting on us. By cause:

| cause | count | nodes | the sentence |
|---|---|---|---|
| `not-expressible-at-a-call-site` | 93 | 31/31 | `react-loop` needs a tool executor, `plan-execute` a planner, `critic-loop` a separate critic model — **a call site has nowhere to inject one, in any language**, and the generated module makes no provider call by design. Permanent; no missing artifact named. |
| `call-site-cannot-carry-it` | 60 | 30/31 | *"this call site passes `**summary_kwargs`, so the request — including its message list — is assembled elsewhere… the only loop this engine could emit would re-ask the identical question, which is a single shot at N times the price under a multi-turn name."* Theirs, actionable, and true after every rewriter lands. |

🔴 **A defect the run found, fixed here.** The operator emitted 6 candidates against a node with no
harness ref, and the first resolved to the **baseline's own `config_hash`** — a proposal of nothing that
would occupy a verification slot measuring a configuration against itself, and whose verdict (necessarily
a tie) would be recorded as evidence about a change never made. `harnessStrategiesExcept` now excludes
the identity **only where it is already in force**; proposing `single-shot` to a node running a six-turn
loop is still offered, because that is often the cheapest correct answer
(Test: `TestHarnessOperatorNeverProposesTheBaseline`). 5 candidates now.

Also verified live on the real tree: `single-shot` ≡ absent (byte-identical canonical bytes and hash);
two `reflexion` entries differing only in `max_turns` hash differently; the coverage table's claim
matches the run cell for cell; the admissibility gate ADMITS a 3-turn loop that bought 18 points for
2.5× cost and REJECTS a 9-turn loop that bought 2 for 9×, a 4-turn loop that answered no better, and —
the one that matters most — **the best numbers on the table, for an EVIDENCE failure**, because part of
its measurement came from the tuning cases; clearing returns byte-exactly to the parent hash; and the
runtime the authored configuration would drive ran exactly 3 of 3 turns and recorded that it stopped at
its ceiling.

🚫 Deliberately NOT reported as "the harness axis works on hermes-agent". 2 of 186 did; the run says so
with a count rather than a claim.

## 15. Delivery cells on this axis (`harness-delivery`)

> Cross-axis rules come from **P13's `change-delivery`** and
> [ADR-010](../../../docs/adr/ADR-010-runtime-gradual-rollout.md); they are referenced, never restated.

**System Designer**

- [x] 15.1 🔴 **A scaffold is structure; its bounds are numbers.** A strategy swap changes how many calls
      the program makes and in what control flow → `notRuntimeResolvable`, permanent, naming the control
      loop. `max_turns`, retry budget and stop condition are parameters of a loop already written →
      `noRolloutBinding`, naming the absent field. Separate cells, neither cause inferred from the other.
      → `specs/harness-delivery/spec.md` (Test: `TestStrategyAndParamsCarryDifferentCauses`).
- [x] 15.2 🔴 **`hostAbsent` is not `notRuntimeResolvable`.** One says the strategy is deliverable but its
      host service is not running (and refuses rather than substituting); the other says it cannot be
      delivered as data at all, host or no host. Rendering them alike sends an operator to restart
      something that was never the problem. An absent host does **not** change delivery eligibility, and
      a delivery refusal 🚫 never offers starting a service as a remedy. → `specs/harness-delivery/spec.md`
      (Test: `TestHostAbsentAndNotRuntimeResolvableAreDistinct`).

**Backend**

- [x] 15.3 The strategy swap refuses the runtime route in every cell and in every apply mode; 🚫 no
      `bound` migration is suggested. → (Test: `TestHarnessSwapRefusesEveryRuntimeCell`).
- [x] 15.4 A rollout arm admits only values inside the strategy's declared `ParamsSchema` — an absent,
      unbounded, or non-positive turn ceiling is refused by the **same** validation the registry applies
      at seal, and a parameter the candidate strategy does not declare stays inexpressible rather than
      ignored. → (Test: `TestRolloutArmCannotRemoveABound`, `TestInapplicableParamStaysInexpressible`).
- [x] 15.5 🔴 Authoring a rollout whose candidate arm swaps the strategy is refused with the transform's
      typed cause, and no document carrying a harness strategy is written. The totality canary is
      extended to cover the second route, and a sabotaged refusal on either turns the cell red. →
      `internal/transform/p18_harness_test.go`
      (Test: `TestHarnessRefusalTotalityCanaryCoversBothRoutes`).

**Frontend + Product Designer**

- [x] 15.6 Render the strategy cell, the params cell, and the host condition as three distinct states
      from the shared source. → `web/console/src/app/app/harness/` (Test: `harness.test.mjs`).
- [x] 15.7 State the claim per cell: 🚫 never "we can change your agent's loop live" — the scaffold
      refuses the runtime route in **every** language. → PRD §9.2 Sales lens.
