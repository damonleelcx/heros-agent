# Tasks — P18: Harness Strategy Optimization

Two waves. **Wave 18a** = the strategy catalog and the harness dimension — modelled, resolved, hashed,
scored by the unchanged eval harness, and **refused** at transform. **Wave 18b** = the operator and the
cost/quality admissibility gate.

**Standing constraints.** The axis is added strictly through the canonical eight-step checklist
(authoring kit §"add an axis"). Harness is a **closed `Dimension`**, a **content-addressed registry
Kind**, and an **additive `omitempty` hashed field** — a no-harness config hashes **byte-identically** to
its pre-P18 form. A `HarnessRef` at transform is **refused with a typed `unsafeRewrite`, never silently
dropped** (mirrors `refuseSkills`, `internal/transform/rewrite.go:388`). **No new eval metric, no scoring
change** — the axis rides `task_success`/`eval_cost_usd`/`eval_latency_ms`. Autonomous turns run in the
node's **existing** P3 sandbox and grant. 🚫 No runtime topology engine is resurrected. 🚫 A harness
override never reorders nodes (that is P15).

This round ships **docs**: the doc tasks below are `[x]`; the eight checklist code tasks and their tests
are `[ ]` — greenfield, nothing is built yet.

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

- [ ] 2.1 Add Kind `harness` and a `harness_entry` table; seal/decode by content address like the other
      four Kinds. → `internal/registry/registry.go` (`KindHarness`, `tableHarness`), new
      `internal/registry/harness.go` (Test: `TestHarnessSealIsContentAddressed`).
- [ ] 2.2 Define `HarnessSpec` with a params schema — `max_turns`, stop condition, optional critic model
      ref, retry budget — with params inapplicable to a strategy **inexpressible**, not ignored. →
      `internal/registry/harness.go` (Test: `TestHarnessParamsSchemaPerStrategy`).
- [ ] 2.3 🔴 Validate params **at seal**: `max_turns` bounded positive, a declared critic ref resolves to
      a `model` entry, retry budget bounded. An out-of-range/unresolvable param fails registration, not
      resolution. → `internal/registry/harness.go` (Test: `TestHarnessParamsValidatedAtSeal`).
- [ ] 2.4 Seed the five builtin strategies (`single-shot`, `react-loop`, `plan-execute`, `reflexion`,
      `critic-loop`); `critic-loop` carries a **separate** critic model ref. →
      `internal/registry/harness_builtins.go` (Test: `TestFiveBuiltinStrategiesRegister`).
- [ ] 2.5 🔴 Enforce cross-registry uniqueness: a `harness` ref used in a non-harness dimension (or vice
      versa) fails closed. → `internal/registry/registry.go` (Test: `TestHarnessRefFailsClosedCrossKind`).

## 3. Backend — The Dimension, override, and resolution (18a, checklist steps 1–4)

- [ ] 3.1 Add `DimHarness` to the closed `Dimension` enum. → `internal/variantspec/spec.go:42` (Test:
      `TestDimHarnessInClosedEnum`).
- [ ] 3.2 Add additive `omitempty` `NodeOverride.HarnessRef` with `isEmpty`/`Refs`/`Validate`
      participation; 🚫 inline strategy definitions rejected (ref-only). →
      `internal/variantspec/spec.go:183` (Test: `TestHarnessRefIsRefOnly`).
- [ ] 3.3 Add the `DimHarness` block to `resolveNode` and the `Dimensions()` entry: override → registry
      entry pinned by `version_id`; absent → discovered default pinned by `source_revision`. →
      `internal/variantspec/resolve.go:67,154` (Test: `TestResolveHarnessOverrideAndDefault`).
- [ ] 3.4 Add the **auto-hashed** additive `omitempty`, nil-when-empty `ResolvedNode.HarnessRef`. →
      `internal/variantspec/resolved.go:46` (Test: `TestResolvedHarnessAutoHashed`).
- [ ] 3.5 🔴 **Test — backward-compatible identity**: a config declaring no harness hashes
      **byte-identically** to its pre-P18 form; every existing golden vector reproduces. →
      `internal/variantspec/resolved_config_golden_test.go` (Test: `TestNoHarnessHashesByteIdentical`).
- [ ] 3.6 Test — a harness change is the **only** edit that changes an otherwise-identical `config_hash`;
      `single-shot` on a one-call node is a no-op on the hash. → (Test: `TestHarnessChangeChangesHashOnly`).
- [ ] 3.7 🔴 Test — fail-static: an unresolvable/malformed `HarnessRef` fails the resolve closed naming the
      ref, never falling back to a different strategy. → (Test: `TestHarnessRefFailStaticNamesRef`).

## 4. Backend — Discovery frontend for the discovered default (18a, checklist step 6)

- [ ] 4.1 Add the **additive** IR field recording a node's discovered default harness; a discovery
      frontend defaults it to `single-shot` unless a loop is proven at the call site. →
      `internal/discovery/emit.go:92` + `extract.go` (Test: `TestDiscoveredHarnessDefaultsSingleShot`).
- [ ] 4.2 Test — the additive IR field breaks no existing IR consumer (absent field, byte-compatible). →
      (Test: `TestIRHarnessFieldAdditive`).

## 5. Backend — The refusing rewriter (18a, checklist step 7 — the honesty seam)

- [ ] 5.1 🔴 Refuse a resolved config carrying a `HarnessRef` with a typed `unsafeRewrite` naming the
      strategy and the reason (materializing a control loop is code generation, not an argument swap), on
      **both** engines — mirroring `refuseSkills`. → `internal/transform/rewrite.go:54` + `rewrite_span.go:59`
      (`refuseHarness`, via `internal/transform/edit.go:90` `unsafeRewrite`) (Test: `TestHarnessRefusedAtTransform`).
- [ ] 5.2 🔴 **Test — refuse, never drop**: the resolved config still carries the `HarnessRef` and the
      transform **refuses** rather than emitting an incorrect loop or silently succeeding. The test must
      fail if the override is silently dropped. → (Test: `TestHarnessRefusedNotSilentlyDropped`).
- [ ] 5.3 Refuse a **group** harness (a `HarnessRef` scoped to an ordered edge set) the same way, naming
      the edge set. → `internal/transform/rewrite.go` (Test: `TestGroupHarnessRefusedNamingEdgeSet`).

## 6. AI Engineer — The operator and admissibility (18b, checklist step 8)

- [ ] 6.1 Add `OpHarnessStrategy` operator kind, a catalog row, and a prior for verification ordering. →
      `internal/proposal/operator.go:34` + `catalog.go:18` + `gain.go:8,26` (Test: `TestOpHarnessStrategyInCatalog`).
- [ ] 6.2 Emit harness-override Variant Specs routed through P5.5 verification; 🚫 no scaffold swap ships
      on an unverified opinion. → `internal/proposal/harness_op.go` (Test: `TestHarnessSwapIsVerificationGated`).
- [ ] 6.3 🔴 **Admissibility gate**: a heavier harness is admitted over a lighter one **only** when the
      measured `task_success` gain outweighs its added `eval_cost_usd` and `eval_latency_ms`, computed on
      **held-out** cases. → `internal/proposal/harness_op.go` (Test: `TestHarnessAdmissibleOnlyWhenCostEarned`).
- [ ] 6.4 🔴 Test — a scaffold that raises cost/latency **without** a commensurate `task_success` gain is
      **rejected**; and the admissibility set is disjoint from the proposal's tuning cases (no leak). →
      (Test: `TestHeavierHarnessCostWinRejected`, `TestAdmissibilityHeldOutDisjoint`).

## 7. Backend + AI Engineer — Bounded autonomy and sandbox containment (18b)

- [ ] 7.1 🔴 Enforce that every multi-turn strategy declares a bounded `max_turns` and stop condition; a
      run reaching the ceiling terminates and is recorded, never hangs. 🚫 No strategy can express an
      unbounded loop. → `internal/registry/harness.go` + runtime bound (Test: `TestMaxTurnsBoundedAndTerminates`).
- [ ] 7.2 🔴 Guarantee autonomous turns run within the node's **existing** P3 sandbox and tool grant; the
      added turns reach no egress destination or tool outside that grant, and the enlarged turn/tool
      surface is **observable** in the trace. → (Test: `TestHarnessTurnsStayInExistingGrant`,
      `TestHarnessSurfaceObservableInTrace`).

## 8. QA — Acceptance gate (18a + 18b)

- [ ] 8.1 Hash suite: harness-only change moves the hash; no-harness config byte-identical; `single-shot`
      no-op. → (Test: `TestHashParticipationSuite`).
- [ ] 8.2 🔴 Refusal suite: `HarnessRef` refused with a typed error on both engines; refuse-never-drop
      asserted; group harness refused naming the edge set. → (Test: `TestRefusalSuite`).
- [ ] 8.3 Scored-with-no-change suite: a harness variant is scored by the existing eval harness with no
      new metric and no scoring change. → (Test: `TestHarnessScoredByExistingHarness`).
- [ ] 8.4 🔴 Admissibility suite: cost-only win rejected on held-out; disjoint held-out set. → (Test:
      `TestAdmissibilitySuite`).
- [ ] 8.5 🔴 Safety suite: bounded `max_turns`; no new egress/tool scope; surface observable. → (Test:
      `TestBoundedAutonomyAndContainmentSuite`).
- [ ] 8.6 Wiring-boundary suite: a group harness composes with P15's ordered edge set and never reorders.
      → (Test: `TestHarnessComposesWithWiringNoReorder`).

## 9. Documentation

- [x] 9.1 PRD (14 sections). → `docs/prd/P18-harness-strategy-optimization.md`.
- [x] 9.2 OpenSpec change set — proposal, tasks, design, decisions, and the two capability spec deltas. →
      `openspec/changes/p18-harness-strategy-optimization/`.
- [x] 9.3 Record the one-way-door contracts (new Kind, new Dimension, new DB table, interim-refusal,
      harness-vs-wiring boundary, cost/quality admissibility) with their 八级法则 tags. → `decisions.md`.
- [ ] 9.4 On deploy, fold the two capability deltas into `openspec/specs/`. →
      `openspec/specs/{harness-strategy,agent-loop}/spec.md` (deferred to the build round).
