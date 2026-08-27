# Tasks — P36: The Agent Is a Graph

> **Task 1.1's finding is recorded in [`decisions.md`](decisions.md) as D-36.0, and it decides the size of
> the phase.** A nested `nodes` array CANNOT preserve the hash — `confighash` canonicalises key order, not
> structure — so a compatibility encoding is required and IS implemented. With it the phase is **additive**:
> no pin is migrated and no `spec_json` row is rewritten.
>
> Answers to PRD §14 Q1–Q5 are in [`decisions.md`](decisions.md). Evidence is named per task below.

## 1. Hash compatibility — establish this first

- [x] 1.1 Prove that a definition with one node, no `order`, no `edges`, no `graph_groups` and no `loop_ref` marshals to **byte-identical** bytes and produces the same `config_hash` as its pre-P36 form. Establish whether a nested `nodes` array can do this or a compatibility encoding is required, **before building anything else**.
      **Finding: a nested array cannot; a compatibility encoding is required.** → [`decisions.md` D-36.0](decisions.md).
      Implemented as `Definition.MarshalJSON` / `canonical()` in `internal/herosagent/definition.go`, asserted by
      `TestPreP36ConfigHashesAreReproducedExactly`. Mutation drill: forcing `legacyShaped()` false makes 10 assertions red.
- [x] 1.2 Fixture captured from before the change — not reconstructed after it — that resolves and reproduces its hash.
      → `internal/herosagent/testdata/p36-pre-confighash.json`, recorded with `P36_RECORD_PRE=1` **by the pre-change tree**
      (commit `proof(p36): record the pre-P36 definition bytes, before any P36 code exists`), not reconstructed.
- [x] 1.3 An existing pinned inference remains readable and names the `config_hash` that produced it.
      → `TestAPreP36StoredDefinitionDecodesAndKeepsItsHash` (a literal pre-P36 `spec_json`, decoded and re-encoded to the
      same bytes) and `TestAnExistingPinRemainsReadableAndNamesItsProducingConfiguration`.
- [x] 1.4 Activating a new definition does **not** re-run pinned inferences; assert no provider call.
      → `TestActivatingANewDefinitionRunsNoInference` counts provider calls rather than asserting a nil error.
- [x] 1.5 A pin whose shape is no longer authorable renders **stale with its producing configuration named** — neither absent nor current.
      → `herosagent.ClassifyPin` (three-valued: `current` / `stale` / `unattributable`), asserted by
      `TestAPinFromAnUnauthorableShapeIsStaleAndNamesItsProducer`.

## 2. System Designer

- [x] 2.1 Answer PRD §14 Q1: per-node credentials or one per definition. `CriticModelRef` is the existing precedent for a second model.
      **Answer: per node.** → [`decisions.md` D-36.1](decisions.md). `Node.CredentialRef`; `Readiness` resolves every node's.
- [x] 2.2 Answer PRD §14 Q2: is the producing node ever shown to a customer, or operator-side only.
      **Answer: operator-side only.** → [`decisions.md` D-36.2](decisions.md). Fenced by `TestTheCustomerProjectionCarriesNoNodeAttribution`.
- [x] 2.3 Answer PRD §14 Q3: is `placement` per node. Note that this would turn a gate both runners call and neither can skip into a per-node decision.
      **Answer: no — placement stays per tenant.** → [`decisions.md` D-36.3](decisions.md). The multi-node case is REFUSED
      over the single-node customer link rather than flattened.
- [x] 2.4 Answer PRD §14 Q4: activation during an in-flight assessment.
      **Answer: it finishes under the definition it started with, and the report records which.** →
      [`decisions.md` D-36.4](decisions.md). Made structural by resolving the definition once into an `AssessmentBinding`.
- [x] 2.5 Answer PRD §14 Q5: does the rehearsal calibration set need to grow to exercise a fan-in and a conditional edge. A rehearsal that cannot fail on the new capability is not a rehearsal of it.
      **Answer: yes, and the requirement is a REFUSAL rather than a fixture count.** → [`decisions.md` D-36.5](decisions.md).
- [x] 2.6 Record D5 (no self-modification) where it will be found by whoever proposes it.
      → [`decisions.md` D-36.8](decisions.md), enforced by `TestNoProposalTargetsTheAgentsOwnDefinition`.

## 3. Backend Dev — the definition

- [x] 3.1 `NodeID` from package constant to data; move `definition.go`, `axiseditor.go`, `inferencestore.go`, `placement.go`, `caps.go` and the fences **together**.
      → `DefaultNodeID` is now only the DEFAULT a single-node definition gets; identity is `Node.NodeID`.
      All five moved: `definition.go` (the shape), `axiseditor.go` (loop params + `NodeLabel`), `inferencestore.go`
      (`nodes_json`), and `placement.go` / `caps.go` — which were **reviewed and deliberately unchanged**, with the
      reason recorded in each file's header (D-36.3, D-36.6) rather than left for the next reader to re-derive.
- [x] 3.2 `AuthorableAxes()` returns nine; `loop` and `graph` are registry references, never inlined.
      → derived from `variantspec.Dimensions()` + `graph`, so it cannot silently miss one.
      `TestAuthorableAxesAreTheProductsNine`, `TestLoopAndGraphAreReferencesNeverInlined`.
- [x] 3.3 Extend the **reflective credential fence** to every new field. A fence enumerating the old shape passes vacuously on the new one — add a key-shaped field to the new struct and require the fence to fail.
      → the walk is extracted to `keyShapedOffenders` so the fence and the drill run the SAME code;
      `TestTheCredentialFenceFiresOnAKeyAddedToTheNewShape` adds `APIKey` to a mirror of `Node` — at top level AND
      nested inside a definition — and requires it to fire. Anti-vacuity floor raised 20 → 60 fields, plus a
      by-name assertion that `Node`, `NodeEdit`, `TopologyEdit` and `canonicalNode` were actually reached.
- [x] 3.4 `ErrWiringOverride` narrows: still refused for a single-node definition, authorable for multi-node.
      → `Definition.validateTopologyShape`. `TestTheOrderingRefusalNarrowsRatherThanDisappearing` asserts BOTH
      halves plus anti-vacuity (a bad multi-node ordering is still refused). The legacy `wiring` SPELLING is
      refused BY NAME with the rename to `graph` stated, never translated — `TestTheLegacyWiringSpelling…`.
- [x] 3.5 `ErrHostServiceMissing` extends to the loop axis, refusing at **publish** rather than at run.
      → `Publisher.checkLoopAxis` / `refuseMissingLoopHosts`, reading `registry.HostServicesForLoop` — the
      registry's rule, not a second one. A loop bound with NO axis registry wired is refused too, because a loop
      nobody could validate defers the check to whoever the run reaches.
      `TestALoopNeedingAnUnavailableHostServiceIsRefusedAtPublish`, `TestALoopIsRefusedWhenNothingCanValidateIt`.
- [x] 3.6 Loop turns validated against the node's harness envelope ceiling at publish, naming both values.
      → `refuseOverCeiling` at publish and `ValidateLoopParams` at save. Both name the chosen value AND the
      ceiling, because "too many turns" sends the reader to guess which of two people to ask.
      `TestALoopOverItsEnvelopeCeilingIsRefusedNamingBothValues`.
- [x] 3.7 `ErrNoChange` still refuses to mint a duplicate version.
      → unchanged, and now asserted for multi-node as well: `TestRepublishingAnIdenticalDefinitionCreatesNoSecondVersion`.
- [x] 3.8 Per-node attribution on every inference: node id and definition version.
      → `Stored.Nodes []NodeRun` + `ProvenancedEdge.ProducedByNode`, stamped by `Runner.runNode` where the
      producer is known. 🚫 NOT stamped on the customer-side path (`BindHash`), because nobody there observed it —
      absent is the honest value. `TestAnInferenceNamesTheNodeAndTheDefinitionVersionThatProducedIt`.
- [x] 3.9 Migration for the definition store: repeatable, success on a second run, idempotency guard named in the commit body, and existing single-node rows read back byte-identically (1.1).
      → `0052_p36_node_attribution` adds ONE nullable column and **does not touch `spec_json`**, because D-36.0
      establishes no rewrite is needed. Guards: `ADD COLUMN IF NOT EXISTS` + `ON CONFLICT (id) DO NOTHING`.
      `internal/pgmigrate/p36_node_attribution_test.go` (4 fences), plus the P26 no-new-table ledger entry.

## 4. Backend Dev — topology

- [x] 4.1 Route the agent's topology through the **same** typed-contract validator a customer's Variant Spec uses. Assert one code path; a lookalike is the failure design D1 is about.
      → `variantspec.ValidateTopology` is EXPORTED and `variantspec.Resolve` now calls it too, so there is one
      function to point at. `TestTheAgentsTopologyGoesThroughTheCustomersValidator` asserts the agent's refusal
      **contains the customer's verbatim** — two implementations do not produce the same sentence by coincidence.
      Drill: replacing the wrapped sentence with a lookalike ("the agent's topology is invalid") turns it red.
      🔴 It paid for itself immediately — see [`decisions.md` D-36.0b](decisions.md): the agent's first fan-in
      found that the node IO contract was wrong, which a private validator would have enshrined instead.
- [x] 4.2 Concurrency declared over the ordering; ordering still contains every node.
      → `Definition.validateTopologyShape` + `variantspec.validateGraph`.
      `TestConcurrencyIsDeclaredOverTheOrderingRatherThanInsteadOfIt`.
- [x] 4.3 Fan-in without a declared merge refused at publish.
      → `TestAFanInWithoutAMergeIsRefusedAtPublish`, which also asserts **no version row is written** — a
      refused publish that wrote one would leave a `config_hash` pointing at something nothing can run.
- [x] 4.4 Conditional edges validated at publish through the existing expression path.
      → `AgentIR` RECORDS the closed predicate vocabulary as the call site's `in_scope`, so
      `variantspec.validatePredicates` — the same check that governs a prompt slot's `expr` — refuses an
      unknown symbol with no new code. `TestAConditionalEdgeIsValidatedAtPublishByTheExpressionPath` and
      `TestTheAgentIRRecordsItsPredicateVocabularyRatherThanDeferring`.
      Drill: recording a NIL scope makes the check DEFER rather than pass — 3 assertions red.
- [x] 4.5 Pinned result must not depend on interleaving — anything order-dependent in a merge is a defect.
      → `mergeOutputs` walks the ORDERING and emits edges sorted by content; nothing is appended in completion
      order anywhere. `TestARepeatedPinnedInferenceUnderConcurrencyIsByteIdentical` runs the same pinned
      inference **40 times** against jittering models. Drill: iterating the output map instead of the ordering
      turns it red.
- [x] 4.6 In-flight assessments complete under the definition they started with, and the report records which.
      → `AssessmentBinding`, resolved once and carried as a value; there is no read of "the active definition"
      inside a run. `TestAnInFlightAssessmentFinishesUnderTheDefinitionItStartedWith` **stages an activation
      from inside the first node's provider call** and asserts both nodes still ran under the original.

## 5. Cost and control

- [x] 5.1 `CapChecker` ceiling scoped **per assessment, not per node**; adding a node does not raise the budget.
      → nothing in `caps.go` is keyed by node, and that absence is the mechanism: there is no node parameter to
      pass. `TestAddingANodeDoesNotRaiseTheAssessmentBudget` asserts the three things this can actually mean —
      the ceiling reads the same for 1, 4 and 8 nodes; spend does not grow past it; and the overshoot is bounded
      by ONE call rather than by N. (No pre-call cap can promise "never exceeds": the call's cost is unknown
      until it returns, and the single-node runner has always had that property.)
- [x] 5.2 Ceiling enforced before every provider call on every node.
      → 🔴 **This found a real defect in the first implementation.** The check ran per node and learned nothing
      between runs: the meter is written ONCE per assessment (`Spend` is keyed by inference, and a half-finished
      assessment has no inference id), so every node read the same stale total and passed. A four-node
      definition under a ten-token ceiling spent thirty-two. `CapChecker.Check` now takes `pendingTokens` as a
      REQUIRED parameter, so the compiler makes every caller answer "what have I spent that the meter cannot
      see" — a `CheckWithPending` alongside `Check` would have left the old call sites answering `nothing` by
      omission, which is the answer that was already wrong.
      `TestTheCeilingIsCheckedBeforeEveryNodesProviderCall` asserts the run stops PART WAY — not at all, and not
      at the start. Drill: setting `pending` back to 0 turns it red.
- [x] 5.3 Ceiling exhaustion degrades to `not_measured` with `budget exhausted`, the state P33 already defines.
      → `internal/assessment/runner.go` recognises `herosagent.ErrCapReached` and `ErrInferenceBudgetExhausted`
      and degrades the axis, instead of falling through to the outage branch that leaves the STRUCTURAL finding
      standing. `TestACeilingInsideTheAgentDegradesToBudgetExhausted` + the counter-case
      `TestAnOrdinaryInferenceFailureIsNotReportedAsABudgetCeiling`.
      The drill's own output is the argument: with the branch disabled, the report claims *"no topology was
      read, and that is a limit of our python frontend"* — an absence rendered as a measurement — while
      `Partial()` returns false and nothing looks wrong.
- [x] 5.4 Rehearsal required before activating a multi-node definition.
      → unchanged in force; the refusal now names the node count and the blast radius, because a graph is where
      somebody wants an exception. `TestActivatingAMultiNodeDefinitionRequiresARehearsal`, with the anti-vacuity
      half (it activates once passed). Drill: disabling the publisher's gate still leaves the STORE's
      independent gate refusing — which is `Activate`'s documented "they fail independently on purpose",
      observed rather than assumed.
- [x] 5.5 Rollback = activating a previous version, as **one act**; never re-authoring the older shape.
      → `Publisher.Rollback(ctx, configHash)`. One call, one argument, and it is a HASH rather than a
      definition. `TestRollbackIsOneActAndRequiresNoReAuthoring` asserts no version row is created and exactly
      one version is active afterwards. 🚫 It does not re-run the rehearsal (the verdict is on the immutable
      row, and re-measuring during an incident spends tokens to reproduce a recorded number) and does not
      re-run pins.

## 6. Frontend Dev — the operator surface

- [x] 6.1 **Inventory the current axis editor first.** Every control has a named destination or is deliberately removed with agreement. This is the highest-risk UI revision in the program.
      → [`axis-editor-inventory.md`](axis-editor-inventory.md), written BEFORE the redesign. 26 controls, each with a
      destination. **Nothing is deliberately removed.** The one thing that changes rather than moves is the axis
      NAME `wiring` → `graph` (task 10.3), and the old spelling is refused by name with the new one stated.
- [x] 6.2 Nine axes and a node dimension. Collapse, do not omit — a hidden axis is indistinguishable from one that does not exist.
      → the Configuration tab groups by node: every node's eight rows are rendered, plus one definition-level
      `graph` row. `groupByNode` preserves the definition's own ordering — the sequence the runner walks — rather
      than sorting. Drill: `.slice(0, 1)` on the groups (render only the first node) turns the fence red.
- [x] 6.3 Wiring becomes editable for multi-node and keeps its refusal text for single-node (3.4). Do not delete the reason; render it conditionally.
      → `graphAxisRow` renders the axis in BOTH states, and the topology fieldset carries the reason. Drill:
      rewording "no second node to order it against" out of the page turns the fence red.
- [x] 6.4 Node-level view: which node produced which inference.
      → the **Nodes** tab. Per node: inferences, tokens, latency, failures and **skips** — skips in their own
      column, never folded into failures, because a node a predicate routed around did not fail. 🔴 Three
      distinct renderings for zero: `unknown` (no source wired), `not yet run`, and a measured number.
- [x] 6.5 Extend P26's operator build fence to every new axis and node kind — remove an operator surface and the build must fail.
      → `agent-publish.test.mjs` gains three fences that DERIVE the nine axes from `variantspec.Dimensions()` +
      `AxisGraph` and assert each has a surface, that the node dimension is rendered, and that the `graph` axis
      appears in both states. 🔴 The old parser broke LOUDLY when `AuthorableAxes()` became derived — on its
      anti-vacuity assertion rather than by returning an empty set and passing.
- [x] 6.6 `scan:tokens` stays green; operator chrome unchanged so the two consoles are never confused.
      → `npm run build` (which runs `scan:origins`, `scan:events`, `scan:tokens`, `scan:ledger` and
      `scan:bundle`) passes; 130/130 console tests pass, including the P26 floor, craft and viewport suites.
      No dependency was added and the chrome is untouched.

**Verified in Chrome against a live stack** (`cmd/proof/operatorconsole` + the console BFF), not only in tests:

- the header reads `serving 3cf6e2892ed4` **3 NODES**;
- Configuration renders 3 nodes × 8 axes + one `graph` row reading
  `heros_triage → heros_analyst → heros_critic; 2 edge(s); concurrent group [heros_triage, heros_analyst]`;
- Nodes shows the three distinct situations — a node that ran, one that **failed once**, and one a predicate
  **skipped twice**;
- Versions shows the DISTINCT model and credential sets (`anthropic, openai`) and the shape per version;
- a **two-node definition was published through the form** — "TARGET: 2-node definition … Published as
  a21081d5… It is PENDING and serving nothing";
- a **fan-in with no merge was refused**, and the operator was shown the shared validator's own sentence
  verbatim: *"variantspec: spec is malformed: node \"merge\", dimension \"graph\": graph_groups[0]: 2 nodes
  converge on \"merge\" and no merge is declared…"* under **"NOTHING HAPPENED"**. That is D1 end-to-end: the
  agent's topology went through the customer's validator and the customer's words reached the operator.

## 7. AI Engineer

- [x] 7.1 Evaluate a multi-node definition **per node**, not as one agent. A definition whose critic never disagrees scores well as a whole and is broken in the half that matters.
      → `RehearsalReport.Nodes []NodeScore`, and the gate reads the minimum across NODES as well as across
      fixtures. `TestADefinitionWhoseSecondNodeContributesNothingFailsTheGate` first asserts the MERGED numbers
      are still good — that is the point — and then that the gate refuses anyway and names the node.
      🔴 Four distinct node failures, because they send somebody to four different places: skipped on every
      fixture (a predicate that never held), failed on every fixture (a node that cannot complete), never
      entered at all, and ran-and-contributed-nothing.
      Anti-vacuity: `TestADefinitionWhoseNodesBothContributePasses`.
- [x] 7.2 Rehearsal compares against the active definition per node and per axis.
      → `TestTheRehearsalReportsPerNodeAndPerFixture`, including that the stored JSON carries both and that the
      node order is the definition's ordering rather than a map's.
- [x] 7.3 Determinism check: execute the same pinned inference repeatedly under a concurrent definition and assert byte-identical output. Run it repeatedly — this failure is intermittent.
      → `TestARepeatedPinnedInferenceUnderConcurrencyIsByteIdentical` (§4.5), **40 runs** against models with
      varying delays. Drill: iterating the output map instead of the declared ordering turns it red.
- [x] 7.4 Establish whether the calibration set exercises a fan-in and a conditional edge at all (2.5).
      **Established, and the answer was not the expected one.** → `TestTheCalibrationSetExercisesAConditionalEdgeAndAFanIn`.
      A conditional edge IS exercised, by an accident of the existing set worth writing down: the near-miss
      fixtures have an EMPTY true edge set, so a truthful analyst produces nothing on those and edges on the
      others — which takes a `produced_edges` predicate in **both** directions in one run. The set was not
      designed for that. The test pins it, so removing the empty-truth fixtures fails HERE, naming the
      consequence, rather than silently making every conditional-edge rehearsal one-directional.
      A fan-in is exercised too, and that one is not an accident.
      🔴 Where the set CANNOT reach a capability, the rehearsal is **REFUSED** rather than passed with a
      warning (`RehearsalCoverage.Gaps` → `Failures`): `RehearsalPassed` arms the activation gate, so a warning
      beside a passing verdict is a warning that arms the gate.
      `TestARehearsalThatCannotExerciseTheCapabilityIsRefused`, with the no-gap anti-vacuity half.

## 8. DevOps

- [ ] 8.1 Per-node inference counts, latency, spend and failure rates on a **readable health endpoint** — per node, because an aggregate over a graph says the agent is slow, not which node is.
- [ ] 8.2 Staged rollout and kill switch exercised against a multi-node definition; blast radius here is every tenant at once.
- [ ] 8.3 Readiness reflects a definition that cannot be executed by the deployed build.

## 9. QA — fences that can go red

- [ ] 9.1 Single-node definition hashes byte-identically to pre-P36 (1.1). **The most important fence in the phase.**
- [ ] 9.2 Existing pins readable and attributable after the shape change.
- [ ] 9.3 A definition change does not silently re-run pins; assert no provider call.
- [ ] 9.4 A stale pin renders stale with its producing configuration named.
- [ ] 9.5 Credential fence covers new fields: add a key-shaped field; the fence must fail.
- [ ] 9.6 Single-node definition still refuses an ordering.
- [ ] 9.7 Fan-in without merge refused at publish.
- [ ] 9.8 Loop with an unavailable host service refused at publish, not at run.
- [ ] 9.9 Rehearsal required before activating multi-node.
- [ ] 9.10 Adding a node does not raise the assessment budget.
- [ ] 9.11 Repeated pinned inference under concurrency → byte-identical, run repeatedly.
- [ ] 9.12 P26 build fence covers new axes and node kinds.
- [ ] 9.13 Agent and customer specs share one validator — assert the code path.
- [ ] 9.14 No proposal targets the agent's own definition (D5).
- [ ] 9.15 Rollback to a previous version is one act and requires no re-authoring.

## 10. Sales Operations

- [ ] 10.1 Sayable: the platform's own agent is configured through the same nine axes we expose to you — including its topology — and it is rehearsed and version-pinned before activation.
- [ ] 10.2 Not sayable: that it optimizes itself. State the reason out loud — an evaluator that grades its own configuration is not an evaluator — because naming the circularity is more credible than marketing past it.
- [ ] 10.3 Noun dictionary: the nine axes are named identically on the operator console, the customer console, the CLI and the docs.

## 11. Sign-off

- [ ] 11.1 PRD §14 Q1–Q5 answered and folded in.
- [ ] 11.2 Task 1.1's finding reviewed **before** the phase is scheduled — it decides whether this is additive or a migration of every pin.
- [ ] 11.3 D5 re-confirmed at the end of the phase, when a self-optimizing agent looks like the obvious next step.
