# Tasks — P34: Harness / Loop / Graph

> **Nothing here is implemented.** Documents only, as the whole GEHA program is.
> **Task 1.1 is the fence for the entire phase.** If it goes red, nothing else in this list is safe.

## 1. Compatibility — do this first and keep it green

- [ ] 1.1 P0 golden vectors pass **unchanged**, before and after every task below.
- [ ] 1.2 A spec with no `loop_ref` and no `graph_groups` serialises **byte-identically** to its pre-P34 bytes.
- [ ] 1.3 A pre-P34 spec referencing a loop-bearing harness entry resolves and reproduces its prior `config_hash`. Fixture captured from before the change, not reconstructed after it.
- [ ] 1.4 Raising a turn ceiling changes no loop entry's content and no loop entry's `version_id` (design D2).
- [ ] 1.5 Rollback check: specs authored under the new binary still resolve under the previous one where they use no new field, and existing specs resolve under both.

## 2. System Designer

- [ ] 2.1 Answer PRD §14 Q1: do **spend** ceilings sit with harness or with loop.
- [ ] 2.2 Answer PRD §14 Q2: reuse the `expr` grammar for predicates, or narrow it.
- [ ] 2.3 Answer PRD §14 Q3: concurrent-group failure semantics — and where they are declared. Do not default them.
- [ ] 2.4 Answer PRD §14 Q4: three axis pages or one page with three sections.
- [ ] 2.5 Answer PRD §14 Q5: whether the legacy path gets an end-of-life date. ADR-014 says permanent; record the answer either way.
- [ ] 2.6 **Reconcile with the open P18 change.** P18's harness capabilities are not folded into `openspec/specs/`, and P18 defines the harness axis as carrying both the scaffold **and** the control loop — which this change splits. Whichever change folds second must reconcile them; folding P18 unchanged after this one would restore the conflation.
- [ ] 2.7 Record the coverage status (`EXISTS` / `PARTIAL` / `ABSENT`) of each of the three axes per language, through the existing coverage contract.

## 3. Backend Dev — the loop axis

- [ ] 3.1 `DimLoop` appended to the closed enum; wire value `loop`, pinned by a test because it is recorded on error records and spec rows.
- [ ] 3.2 `registry.KindLoop` sealing `(strategy, params)`; kind hashed into the `version_id`.
- [ ] 3.3 **Exhaustiveness**: every switch over `registry.Kind` must fail to build without the new case. A switch that compiles without it is a consumer that would silently mis-seal a loop.
- [ ] 3.4 A loop naming an unimplemented strategy fails to resolve — no fallback to `single-shot`.
- [ ] 3.5 `max_turns` < 1 refused, not defaulted; `single-shot` cannot express a turn count at all.
- [ ] 3.6 Refuse at resolve a spec setting both a loop-bearing `harness_ref` and a `loop_ref`, naming both.
- [ ] 3.7 New authoring surfaces write a loop entry and never a loop-bearing harness entry.

## 4. Backend Dev — the harness envelope

- [ ] 4.1 Re-scope `DimHarness` to sandbox posture, host-service provision, turn ceiling, spend ceiling, retries, timeouts, concurrency limit, guardrail and approval-gate bindings.
- [ ] 4.2 `max_turns` above the envelope ceiling refused at resolve, naming both values.
- [ ] 4.3 Host-service refusal moved **left**: `react-loop` without a tool executor, `plan-execute` without a planner, `critic-loop` without a critic — all refused at resolve, not at run.
- [ ] 4.4 Concurrency limit enforced by the sandbox at execution, independently of what the spec declared.

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
