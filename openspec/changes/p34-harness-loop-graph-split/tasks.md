# Tasks — P34: Harness / Loop / Graph

> **Task 1.1 is the fence for the entire phase.** If it goes red, nothing else in this list is safe.
>
> Answers to PRD §14 Q1–Q5, the P18 reconciliation and the per-axis coverage contract are recorded in
> [`decisions.md`](decisions.md). Evidence is named per task below.

## 1. Compatibility — do this first and keep it green

- [x] 1.1 P0 golden vectors pass **unchanged**, before and after every task below.
      → `internal/variantspec/p34_compat_test.go` (`TestP34_GoldenVectorsUnchanged`), plus P0's own
      `TestGolden_ResolvedConfigReproducesFrozenBytesAndHash` and `internal/confighash`. Re-run after every
      task below; green throughout.

- [x] 1.2 A spec with no `loop_ref` and no `graph_groups` serialises **byte-identically** to its pre-P34 bytes.
      → `testdata/p34-pre-confighash.json` row `no-scaffold-at-all`, asserted by
      `TestPreP34ConfigHashesAreReproducedExactly`.

- [x] 1.3 A pre-P34 spec referencing a loop-bearing harness entry resolves and reproduces its prior `config_hash`. Fixture captured from before the change, not reconstructed after it.
      → same fixture, rows `legacy-loop-bearing-{reflexion,react,group}`. **Recorded by the pre-change tree**
      (`P34_RECORD_PRE=1`, before any P34 code existed), not reconstructed.

- [x] 1.4 Raising a turn ceiling changes no loop entry's content and no loop entry's `version_id` (design D2).
      → `internal/registry/loop_test.go` (`TestRaisingTheTurnCeilingMovesNoLoopEntry`). Asserts both
      directions structurally: `turn_ceiling` is inexpressible on a loop entry and `max_turns` is
      inexpressible on an envelope, so the guarantee is not a property of the fixtures.

- [x] 1.5 Rollback check: specs authored under the new binary still resolve under the previous one where they use no new field, and existing specs resolve under both.
      → `TestP34_RollbackShapeIsReadableByThePreviousBinary` decodes this tree's canonical bytes into a
      hand-spelled mirror of the pre-P34 shape with `DisallowUnknownFields`.

## 2. System Designer

- [x] 2.1 Answer PRD §14 Q1: do **spend** ceilings sit with harness or with loop.
      **Answer: harness.** → [`decisions.md` D-34.1](decisions.md). The split line is *imposed vs chosen*,
      not *who consumes it*; spend is inexpressible on a loop entry.

- [x] 2.2 Answer PRD §14 Q2: reuse the `expr` grammar for predicates, or narrow it.
      **Answer: reuse `expr`.** → [`decisions.md` D-34.2](decisions.md). One grammar, one scope validator,
      one place to narrow it.

- [x] 2.3 Answer PRD §14 Q3: concurrent-group failure semantics — and where they are declared. Do not default them.
      **Answer: on the merge, required, closed set `{fail-fast, collect-partial}`.** →
      [`decisions.md` D-34.3](decisions.md). Not defaulted, and `collect-partial` is refused when the
      downstream input contract cannot admit a missing member.

- [x] 2.4 Answer PRD §14 Q4: three axis pages or one page with three sections.
      **Answer: three sibling axis pages**, confirmed with the user. →
      [`decisions.md` D-34.4](decisions.md).

- [x] 2.5 Answer PRD §14 Q5: whether the legacy path gets an end-of-life date. ADR-014 says permanent; record the answer either way.
      **Answer: permanent, no date.** → [`decisions.md` D-34.5](decisions.md); re-confirmed by task 11.2.

- [x] 2.6 **Reconcile with the open P18 change.** P18's harness capabilities are not folded into `openspec/specs/`, and P18 defines the harness axis as carrying both the scaffold **and** the control loop — which this change splits. Whichever change folds second must reconcile them; folding P18 unchanged after this one would restore the conflation.
      → [`decisions.md` D-34.6](decisions.md), and a reconciliation banner at the head of
      [`../p18-harness-strategy-optimization/proposal.md`](../p18-harness-strategy-optimization/proposal.md)
      so neither change can fold silently.

- [x] 2.7 Record the coverage status (`EXISTS` / `PARTIAL` / `ABSENT`) of each of the three axes per language, through the existing coverage contract.
      → [`decisions.md` D-34.7](decisions.md) and `internal/transform/coverage.go`: `loopCoverage`,
      `harnessCoverage` (re-scoped to the envelope) and `graphCoverage`, all three derived from the table
      their rewriter dispatches on and total over `RegisteredLanguages()`.

## 3. Backend Dev — the loop axis

- [x] 3.1 `DimLoop` appended to the closed enum; wire value `loop`, pinned by a test because it is recorded on error records and spec rows.
      → `variantspec.DimLoop` (`spec.go`), wire value `loop`, pinned by
      `TestDimLoopInClosedEnum` and `TestP34AppendsLoopAfterHarness`.

- [x] 3.2 `registry.KindLoop` sealing `(strategy, params)`; kind hashed into the `version_id`.
      → `registry.KindLoop` + `internal/registry/loop.go` + migration
      `0051_p34_loop_registry`. Kind-in-address pinned by `TestLoopKindIsPartOfTheContentAddress`.

- [x] 3.3 **Exhaustiveness**: every switch over `registry.Kind` must fail to build without the new case. A switch that compiles without it is a consumer that would silently mis-seal a loop.
      → `internal/registry/kinds.go`. `kindAnswers` takes one POSITIONAL argument per Kind, so an eighth
      Kind fails to **build** at every call site (drilled: `not enough arguments in call to kindAnswers`).
      Consumer-side, `variantspec.Registries` gained `ResolveLoop`, which broke the build of all 12
      implementers until each answered. Ordering pinned by `TestKindsAndTablesAreTotal`.

- [x] 3.4 A loop naming an unimplemented strategy fails to resolve — no fallback to `single-shot`.
      → `Store.bindLoopStrategy`; `TestLoopNamingAnUnimplementedStrategyFailsClosed` also asserts the
      message SAYS it is not falling back.

- [x] 3.5 `max_turns` < 1 refused, not defaulted; `single-shot` cannot express a turn count at all.
      → `validateLoopDependencies` + `SingleShotLoop.ParamsSchema`;
      `TestLoopRefusesAnInvalidTurnCount`. `LoopEntry.MaxTurns` returns `(int, bool)` so "chose nothing"
      is never read as "chose 1".

- [x] 3.6 Refuse at resolve a spec setting both a loop-bearing `harness_ref` and a `loop_ref`, naming both.
      → `resolveNode` in `internal/variantspec/resolve.go`, checked BEFORE the loop resolves so the error
      does not depend on which ref was broken. `TestBothRefsSetIsRefusedNamingBoth`.

- [x] 3.7 New authoring surfaces write a loop entry and never a loop-bearing harness entry.
      → `internal/authoring/loop.go`: `LoopEdit` writes `loop_ref`, `EnvelopeOptions` offers only
      `envelope`, and `HarnessStrategyOptions` offers the LOOP vocabulary. The two option lists are
      asserted DISJOINT by `TestNewAuthoringCannotCreateALoopBearingHarnessEntry` — no path, not a rule.

## 4. Backend Dev — the harness envelope

- [x] 4.1 Re-scope `DimHarness` to sandbox posture, host-service provision, turn ceiling, spend ceiling, retries, timeouts, concurrency limit, guardrail and approval-gate bindings.
      → `registry.EnvelopeHarness` (`internal/registry/harness_envelope.go`) — a sixth HARNESS strategy,
      not a new field and not a new Kind, because either of those would have moved existing entries'
      `version_id`s. Its schema REQUIRES sandbox posture, turn ceiling and spend ceiling: an omitted
      ceiling reads as "unbounded" to a person and has to be read as some number by the code, and those
      two readings differing is how a policy stops being one. Decoded once onto
      `ResolvedOverride.Envelope`, because four consumers decoding it is four chances to read an absent
      field as permissive. Surface: `internal/authoring/loop.go` `EnvelopeOptions`.

- [x] 4.2 `max_turns` above the envelope ceiling refused at resolve, naming both values.
      → `internal/variantspec/envelope.go` `checkTurnCeiling` → `ErrCeilingExceeded`, naming both numbers.
      `TestMaxTurnsAboveTheEnvelopeCeilingIsRefused`, plus `TestMaxTurnsAtTheCeilingIsAdmitted` so an
      off-by-one cannot make every declared policy one turn tighter than it reads.

- [x] 4.3 Host-service refusal moved **left**: `react-loop` without a tool executor, `plan-execute` without a planner, `critic-loop` without a critic — all refused at resolve, not at run.
      → `checkHostServices` → `ErrMissingHostService` at RESOLVE.
      `TestMissingHostServiceIsRefusedAtResolve` drives all three strategies against an envelope that
      grants ONE service, so the check is proved to read the set rather than its emptiness. Deliberately
      asymmetric with 4.2: a missing ceiling leaves the platform ceiling standing, a missing second actor
      has no fallback at all. The run-time refusal stays where it is — this is a second gate in front of
      it, not a replacement.

- [x] 4.4 Concurrency limit enforced by the sandbox at execution, independently of what the spec declared.
      → `internal/sandbox/concurrency.go`. The sandbox does NOT trust the width it is handed: the number
      enforced is `min(declared, SandboxConcurrencyCeiling)`, keyed by (run, group) so two tenants do not
      contend. `ConcurrencyHealth.Capped` publishes narrowings, which is the signal that the resolve-time
      gate was bypassed. Blocks rather than refuses — a node that failed because its siblings were still
      running would be a flake with a plausible error message.
      Also: the ENVELOPE's spend ceiling is checked BEFORE each provider call
      (`internal/harnessruntime`), reported as the named stopping condition `StopSpendCeiling` rather
      than as an error, and a declared ceiling with no meter is refused at preflight.

## 5. Backend Dev — graph topology

- [ ] 5.1 `GraphGroup` on the spec, `omitempty`; members must all appear in `Order`.
- [ ] 5.2 Concurrency declared **over** `Order`; `Order` still contains every node and replay follows it.
- [ ] 5.3 Predicate edge kind; predicates validated through the ADR-004 `expr` path — one grammar, one validator.
- [ ] 5.4 Merge declaration required on a fan-in; refused at validate when absent, never defaulted.
- [ ] 5.5 Merge validated against the downstream node's typed input contract.
- [ ] 5.6 Every new form gated by `internal/typedcontract`, unchanged, before any codemod is generated.
- [ ] 5.7 An adaptable mismatch previews the adapter **and** the source diff; the adapter is an explicit node in the spec.
- [ ] 5.8 Where a language's transform cannot carry a form, refuse with a typed `unsafeRewrite` naming node and axis. Assert the override is **not** silently dropped.

## 6. AI Engineer — operators and attribution

- [ ] 6.1 Proposal operator for the loop axis.
- [ ] 6.2 Proposal operator for the graph axis, including the reserved-and-unimplemented `OpMerge`.
- [ ] 6.3 Measure each operator by **per-axis** pass rate through the P5.5 verification gate. No mean across axes — it would hide an operator that is not working.
- [ ] 6.4 Prove on a holdout that attribution does not degrade under overlapping spans, **before** concurrency ships. No pure-refactor exemption.
- [ ] 6.5 Confirm no eval, scorer, oracle or metric change is needed. An axis needing a bespoke oracle is designed wrong.

## 7. Frontend Dev

- [ ] 7.1 Per PRD §14 Q4, re-cut `/app/harness` and `/app/wiring` into the agreed shape.
- [ ] 7.2 **Inventory the existing pages first.** Every item either has a named destination or is deliberately removed with the user's agreement; nothing evaporates in the re-cut.
- [ ] 7.3 An axis unavailable in this build renders read-only **with its reason** — a hidden axis is indistinguishable from one that does not exist.
- [ ] 7.4 Refusals render with the axis and node they name, verbatim.
- [ ] 7.5 Reuse the existing axis-page structure; no improvised styling, `scan:tokens` stays green.

## 8. DevOps

- [ ] 8.1 Migration adds a kind and `omitempty` fields; it adds no column to a deployed table for the legacy path. Repeatable, returns success on a second run, and the commit body names the idempotency guard.
- [ ] 8.2 Migration runs only for the components the edition actually deploys.
- [ ] 8.3 Sandbox concurrency limit enforced and observable; peak resource use per run exposed on a readable health endpoint.
- [ ] 8.4 Rollback needs no migration, because nothing is removed — assert this rather than assume it.

## 9. QA — fences that can go red

- [ ] 9.1 Golden vectors unchanged (1.1). The most important fence in the phase.
- [ ] 9.2 Byte-identical serialisation for a no-override spec (1.2).
- [ ] 9.3 Pre-P34 loop-bearing spec resolves and reproduces its `config_hash` (1.3).
- [ ] 9.4 Both refs set → refused, naming both.
- [ ] 9.5 `max_turns` above ceiling → refused, naming both values.
- [ ] 9.6 Missing host service → refused at **resolve**, not at run.
- [ ] 9.7 Fan-in without merge → refused at validate.
- [ ] 9.8 Out-of-scope predicate → refused via the ADR-004 path, naming the symbol.
- [ ] 9.9 Unsupported language → typed `unsafeRewrite`; assert the override was not dropped.
- [ ] 9.10 `registry.Kind` switch missing the new case → build fails.
- [ ] 9.11 Attribution under overlapping spans does not degrade (holdout).
- [ ] 9.12 Concurrent group wider than the envelope limit → refused at resolve, **and** capped by the sandbox at execution. Both, because the second is what holds when the first is bypassed.

## 10. Sales Operations

- [ ] 10.1 Sayable on ship: three named axes; parallel steps and conditional routing configurable and verifiable for the first time.
- [ ] 10.2 Not sayable: that the platform "orchestrates" anything. It configures and verifies the customer's own graph.
- [ ] 10.3 State the compatibility promise out loud — specs authored before this change keep working and keep their measurements — and the honest half, that a legacy path exists permanently as its price.

## 11. Sign-off

- [ ] 11.1 PRD §14 Q1–Q5 answered and folded in.
- [ ] 11.2 ADR-014's refusal of the contract half re-confirmed at the end of the phase, when the residue is visible and the temptation to "finish the job" is highest.
