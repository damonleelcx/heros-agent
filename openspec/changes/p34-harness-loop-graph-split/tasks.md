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

- [x] 5.1 `GraphGroup` on the spec, `omitempty`; members must all appear in `Order`.
      → `internal/variantspec/graph.go` `GraphGroup` (`graph_groups,omitempty`) + `validateGraph`.
      🔴 The wire name is `nodes`, not `members`: P27's ownership-vocabulary fence bans `member` from
      anything hashed, and the graph axis yielded rather than narrowing a fence built to catch unwritten
      fields (recorded in `decisions.md` D-34.3).

- [x] 5.2 Concurrency declared **over** `Order`; `Order` still contains every node and replay follows it.
      → `Order` is untouched. `TestAWellFormedFanInResolves` asserts all four nodes stay in the walk, in
      sequence, with a group declared over two of them — which is the replay determinism design D4 keeps.

- [x] 5.3 Predicate edge kind; predicates validated through the ADR-004 `expr` path — one grammar, one validator.
      → `Edge.Kind == "predicate"` + `Edge.Predicate`; `validatePredicates` in `graphresolve.go` calls
      `CallSite.HasInScope` — the SAME method `validateBindings` calls for a `BindExpr` — and reports the
      SAME sentinel `ErrBindingOutOfScope` naming the symbol. The scope checked is the PRODUCER's,
      because the edge is taken after it runs. An IR with no recorded scope DEFERS rather than refusing.

- [x] 5.4 Merge declaration required on a fan-in; refused at validate when absent, never defaulted.
      → Refused at `Validate`, reachable with no IR and no registry, so a surface can refuse a draft on a
      keystroke. The refusal offers the closed vocabulary — an author told only that something is missing
      has to read the source to learn what to type.

- [x] 5.5 Merge validated against the downstream node's typed input contract.
      → `checkMerge`: collision under `all-fields` refused (precedence would be the platform choosing
      which of the author's two values is real, and under concurrency it would depend on scheduling);
      `collect-partial` against a REQUIRED field refused (D-34.3's enforced consequence); then
      satisfaction against the downstream input contract.

- [x] 5.6 Every new form gated by `internal/typedcontract`, unchanged, before any codemod is generated.
      → `typedcontract.Satisfies` and `Catalog.FindAdapter`, **unchanged**, called from `Resolve` — which
      is before any codemod exists by construction, since `Generate` takes a `*Resolved`.

- [x] 5.7 An adaptable mismatch previews the adapter **and** the source diff; the adapter is an explicit node in the spec.
      → An adaptable mismatch produces an `InsertedAdapter` on `Resolved.MergeAdapters`, with its own
      `io_contract` and a deterministic node id — the same explicit-node mechanism a re-arrangement's
      adapter uses (P5 Decision 3), never a hidden runtime coercion.

- [x] 5.8 Where a language's transform cannot carry a form, refuse with a typed `unsafeRewrite` naming node and axis. Assert the override is **not** silently dropped.
      → `internal/transform/graphrefuse.go` `checkGraphTopology`, dispatched from `Generate` before any
      file is read. `TestEveryLanguageRefusesTopologyByName` runs all 7 languages × 3 forms and asserts
      the axis, the anchor node, the form, `CauseNoMaterializer`, and the words **"not dropped"**.
      Drilled: removing the check turns 10 assertions red.

## 6. AI Engineer — operators and attribution

- [x] 6.1 Proposal operator for the loop axis.
      → `internal/proposal/p34_operators.go` `OpLoopStrategy` + `loopStrategyOp`, writing `loop_ref`.
      🔴 It SUPERSEDES `harnessStrategyOp` rather than joining it: that operator wrote a loop strategy
      into `harness_ref`, which is the legacy shape FR9 forbids — and a proposal IS new authoring.
      `OpHarnessStrategy` stays in the enum as a reserved wire value so stored rows keep decoding;
      nothing emits it, asserted by `TestOpLoopStrategyInCatalog`.

- [x] 6.2 Proposal operator for the graph axis, including the reserved-and-unimplemented `OpMerge`.
      → `OpGraphTopology` + `graphTopologyOp`: declares an independent sibling pair concurrent.
      Eligibility is narrow on purpose — no path in either direction **transitively** (a two-hop
      dependency is the one a direct-edge check misses and that only shows up under load), and a shared
      predecessor. 🚫 It never declares a merge: how a fan-in combines is the author's decision (D6).
      🔴 **`OpMerge` turned out to be implemented, not reserved** — P15 landed it as node FUSION. That is
      the opposite operation from P34's `Merge` (fan-in combination), and
      `TestTheGraphOperatorNeverFusesNodes` is the fence keeping the two apart.

- [x] 6.3 Measure each operator by **per-axis** pass rate through the P5.5 verification gate. No mean across axes — it would hide an operator that is not working.
      → `internal/proposal/axis_passrate.go`. `PassRatesByAxis` publishes rate AND denominator per axis;
      there is deliberately no `Overall()`, and `TestThereIsNoAggregatePassRate` bans the vocabulary.
      `TestPassRatesAreReportedPerAxis` builds the exact shape PRD §9.5 warns about and logs it: a 61%
      aggregate that looks healthy while the graph axis sits at 5%.

- [x] 6.4 Prove on a holdout that attribution does not degrade under overlapping spans, **before** concurrency ships. No pure-refactor exemption.
      🔴 **The holdout found a real defect.** With one guilty node per case attribution scored 60/60 under
      overlap and proved nothing — walk order cannot matter when there is only one node to find. The arm
      that can catch it is BOTH concurrent nodes diverging: there, `executionOrder`'s start-time walk
      localized to alpha or beta depending on a **nanosecond of scheduling**.
      Fixed at the cause: `AttributeWithOrder` walks the spec's DECLARED order (design D4 keeps `Order`
      precisely so a replay has one), and `PerNodeContribution` now reports `OverlappingSpans` /
      `OrderedByDeclaration` so a consumer can tell a replay-consistent localization from a scheduling
      artifact. `make attribution-holdout` prints defect and fix side by side; the test asserts the
      defect still reproduces on the clock path, or the fix would be unfalsifiable.

- [x] 6.5 Confirm no eval, scorer, oracle or metric change is needed. An axis needing a bespoke oracle is designed wrong.
      → `internal/evalharness/axisagnostic_test.go` `TestP34AddedNoEvalSurface` / `TestP34AddedNoOracle`.
      A VOCABULARY ban (loop, harness, graph, topology, concurrent, scaffold, max_turns, predicate,
      fan_in, envelope) over the shipped metric family and the shipped oracle set, because this
      requirement fails as a `MetricLoopTurns` added one afternoon for a dashboard, not as a recorded
      decision. No eval, scorer, oracle or metric changed.

## 7. Frontend Dev

- [x] 7.1 Per PRD §14 Q4, re-cut `/app/harness` and `/app/wiring` into the agreed shape.
      → three sibling pages, per D-34.4. `/app/harness` (envelope), `/app/loop` (new, carrying P18's
      strategy picker unchanged), `/app/graph` (supersedes `/app/wiring`). 🔴 `/app/wiring` REDIRECTS
      rather than 404s — a bookmark that stops working is indistinguishable from a feature that was
      withdrawn, and the reader most likely to have saved that link saved it while trying to understand
      a refusal. Nav, `routes.ts`, the conversational intent table and `heros link`'s surface report all
      updated. Verified in Chrome at desktop and mobile width; no console errors.

- [x] 7.2 **Inventory the existing pages first.** Every item either has a named destination or is deliberately removed with the user's agreement; nothing evaporates in the re-cut.
      → [`frontend-inventory.md`](frontend-inventory.md). 17 items, every one with a named destination;
      **Removals: none**. Two findings the inventory existed to catch: the Context/Memory/Harness
      boundary table had to GROW to four rows rather than move (P34 created a fourth thing to conflate),
      and `Parallelize`/`Merge` mean OPPOSITE things on the wiring axis and the graph axis — so
      `/app/graph` states the collision rather than leaving a reader to meet it in a refusal.

- [x] 7.3 An axis unavailable in this build renders read-only **with its reason** — a hidden axis is indistinguishable from one that does not exist.
      → `/app/graph`'s Topology tab LEADS and opens with the reason: declarable, resolvable, hashed,
      validated — and written into source in no language, with the missing artifact named per form.
      `/app/harness` does the same for an axis that is refused at every call site **permanently**, and
      says in the same breath that refused is not unenforced, because a reader who drew that conclusion
      would be wrong about their own blast radius.

- [x] 7.4 Refusals render with the axis and node they name, verbatim.
      → the existing `AxisRefusal` / `AxisApplied` components, unchanged, still carry the engine's verbatim
      sentences on `/app/graph`. The topology forms name the axis and the form; the engine's own refusal
      names the axis, the anchor node and the form (§5.8).

- [x] 7.5 Reuse the existing axis-page structure; no improvised styling, `scan:tokens` stays green.
      → `PageFrame` + `Tabs` + `DataTable` + `Banner` + `Chip` + `AxisProjectionPanel` on all three, the
      same structure every other axis page uses. `npm run scan:tokens` green (232 files, no literal).
      `npm test` 693/693. Production build clean; bundle scan under ceiling.

## 8. DevOps

- [x] 8.1 Migration adds a kind and `omitempty` fields; it adds no column to a deployed table for the legacy path. Repeatable, returns success on a second run, and the commit body names the idempotency guard.
      → `db/migrations/postgres/0051_p34_loop_registry.{up,down}.sql`. **Idempotency guards, named:**
      `CREATE TABLE IF NOT EXISTS`; `DROP TRIGGER IF EXISTS` + `CREATE TRIGGER` (not
      `CREATE OR REPLACE TRIGGER`, which is PG14+ and this targets 11+); `INSERT ... ON CONFLICT (id)
      DO NOTHING`. **No `ALTER TABLE` anywhere and `harness_entry` is not named** — altering it would be
      ADR-014's orphaning chain arriving through the database instead of the seal path.
      `internal/pgmigrate/p34_loop_registry_test.go` asserts all of it; drilled both ways.

- [x] 8.2 Migration runs only for the components the edition actually deploys.
      → enforced by a **dependency**, not a list: the migration attaches 0002's
      `registry_verify_envelope` / `registry_reject_mutation`, so a component that never ran 0002 cannot
      run this one and fails loudly rather than creating a table nobody uses. A hand-maintained
      edition list would be a second source of truth about deployment topology, and the copy goes stale.
      The file also states its scope in prose, so a deployment planner need not reverse-engineer the DDL.

- [x] 8.3 Sandbox concurrency limit enforced and observable; peak resource use per run exposed on a readable health endpoint.
      → `sandbox.ConcurrencyHealth` on `/readyz` as `sandbox_concurrency`: `active`, **`peak`**,
      `peak_group_width`, `ceiling`, `capped`. Peak because a current gauge structurally cannot answer
      "how loaded did this get" — by the time anybody looks the moment has passed. 🔴 `capped` is the
      field worth an alert: non-zero means a spec reached execution asking for a wider group than the
      sandbox allows, i.e. the resolve-time gate was bypassed — invisible in every aggregate, because
      nothing errors and the work simply runs narrower than it asked to.

- [x] 8.4 Rollback needs no migration, because nothing is removed — assert this rather than assume it.
      → `TestRollbackNeedsNoMigration`, which is the task's own wording (*"assert this rather than
      assume it"*) taken literally. Reverting the BINARY needs nothing from the database: a `loop_entry`
      the previous binary never reads is inert, exactly as `harness_entry` was before P18's code was
      enabled. The down-migration exists, states that it is the more destructive option, states what it
      costs (specs pinning a `loop_ref` stop resolving — loudly, at resolve), and does not touch
      `harness_entry` in either direction.

## 9. QA — fences that can go red

> **`make p34-fence-redcheck` — 14 mutations, all proven.** Each breaks a rule, asserts the package still
> COMPILES (a mutation that does not build exits non-zero for a reason that has nothing to do with the
> fence), and asserts the named test goes red. It refuses to run on a dirty tree and restores in a
> `finally`, because a weakened compatibility check left in the working tree is worse than the failure
> it prevents.
>
> 🔴 It caught one of its own mutations as non-compiling on the first run — the harness working before
> the fences did.


- [x] 9.1 Golden vectors unchanged (1.1). The most important fence in the phase.
      → `TestP34_GoldenVectorsUnchanged` · drilled: removing `omitempty` from the predicate field turns it red.

- [x] 9.2 Byte-identical serialisation for a no-override spec (1.2).
      → `TestPreP34ConfigHashesAreReproducedExactly` (row `no-scaffold-at-all`) · drilled: an always-present
      `graph_groups` key turns it red. **This is the mutation the phase is most exposed to — it is one word.**

- [x] 9.3 Pre-P34 loop-bearing spec resolves and reproduces its `config_hash` (1.3).
      → same recording, rows `legacy-loop-bearing-*`, captured by the pre-change tree · plus
      `TestLegacyLoopBearingHarnessAloneStillResolves` at the value level.

- [x] 9.4 Both refs set → refused, naming both.
      → `TestBothRefsSetIsRefusedNamingBoth` · drilled: dropping the harness ref from the message turns it red.

- [x] 9.5 `max_turns` above ceiling → refused, naming both values.
      → `TestMaxTurnsAboveTheEnvelopeCeilingIsRefused` · drilled: clamping instead of refusing turns it red.

- [x] 9.6 Missing host service → refused at **resolve**, not at run.
      → `TestMissingHostServiceIsRefusedAtResolve` · drilled: moving the check back to run time turns it red.

- [x] 9.7 Fan-in without merge → refused at validate.
      → `TestAFanInWithNoMergeIsRefusedAtValidate` and `TestCollectPartialAgainstARequiredFieldIsRefused` ·
      both drilled.

- [x] 9.8 Out-of-scope predicate → refused via the ADR-004 path, naming the symbol.
      → `TestAnOutOfScopePredicateIsRefusedNamingTheSymbol` · 🔴 drilled by ADDING A SECOND, LOOSER RULE
      rather than deleting the check, because that is how this actually fails: nobody deletes a scope
      check, somebody adds "…or it looks like a literal", and the second grammar is born.

- [x] 9.9 Unsupported language → typed `unsafeRewrite`; assert the override was not dropped.
      → `TestEveryLanguageRefusesTopologyByName` (7 languages × 3 forms) · drilled: dropping the topology
      check turns 10 assertions red. Asserts the words **"not dropped"** appear in the refusal.

- [x] 9.10 `registry.Kind` switch missing the new case → build fails.
      → 🔴 **INVERTED, and run separately.** Its claim is that a missing case fails to BUILD, so a
      successful compile is the failure. Expressing it in the mutation table would make the table's own
      compile-check mean the opposite thing for one row, which is how a harness starts lying. Drilled:
      an eighth Kind produces `not enough arguments in call to kindAnswers`.

- [x] 9.11 Attribution under overlapping spans does not degrade (holdout).
      → `TestBothNodesDivergingIsWhereOrderActuallyDecides` · drilled by reverting to clock ordering, which
      is the pre-fix behaviour — so if the defect ever stops reproducing, the fix becomes unfalsifiable
      and the drill says so.

- [x] 9.12 Concurrent group wider than the envelope limit → refused at resolve, **and** capped by the sandbox at execution. Both, because the second is what holds when the first is bypassed.
      → **BOTH gates drilled separately.** `TestAGroupWiderThanTheEnvelopeLimitIsRefused` (resolve) and
      `TestTheSandboxCapsEvenWhenTheSpecAsksForMoreThanTheCeiling` (sandbox). The second is the one that
      holds when the first is bypassed, so proving them together would prove neither.

## 10. Sales Operations

- [x] 10.1 Sayable on ship: three named axes; parallel steps and conditional routing configurable and verifiable for the first time.
      → [`docs/sales/P34-harness-loop-graph-claims.md`](../../../docs/sales/P34-harness-loop-graph-claims.md) §2,
      plus two `shipped: true` entries in the console's capability manifest (`axis-split`,
      `envelope-ceilings`) — which is what makes the claim renderable at all, since `scan-claims.mjs`
      fails the BUILD on a claim that is unlisted.
      🔴 Topology materialization is deliberately ABSENT from the manifest, so the public surface
      physically cannot say the platform applies a concurrent group. That is the gate working, not an
      oversight, and §5 of the doc tells a seller to say it before a demo rather than after.

- [x] 10.2 Not sayable: that the platform "orchestrates" anything. It configures and verifies the customer's own graph.
      → §3, with the specific words: an orchestrator is a DEPENDENCY — in the customer's request path, a
      reason their product can be down at 3am, and a procurement conversation about lock-in. This is a
      tool that reads a repository, proposes a change and proves whether it was better; the change runs
      on their infrastructure whether we exist or not. Four do-not-say / say-instead pairs.

- [x] 10.3 State the compatibility promise out loud — specs authored before this change keep working and keep their measurements — and the honest half, that a legacy path exists permanently as its price.
      → §4, both halves in one breath. The promise — specs authored before this change keep working and
      keep their measurements — is stronger than most vendors can make, and the price is a legacy path
      with **no deprecation date**. The answer to *"when does the old way stop working?"* is **"it
      doesn't"**, and that is the good answer: a date would hand the orphaning problem to somebody else
      on a day when the reasoning has been forgotten.

## 11. Sign-off

- [ ] 11.1 PRD §14 Q1–Q5 answered and folded in.
- [ ] 11.2 ADR-014's refusal of the contract half re-confirmed at the end of the phase, when the residue is visible and the temptation to "finish the job" is highest.
