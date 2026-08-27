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

- [ ] 4.1 Route the agent's topology through the **same** typed-contract validator a customer's Variant Spec uses. Assert one code path; a lookalike is the failure design D1 is about.
- [ ] 4.2 Concurrency declared over the ordering; ordering still contains every node.
- [ ] 4.3 Fan-in without a declared merge refused at publish.
- [ ] 4.4 Conditional edges validated at publish through the existing expression path.
- [ ] 4.5 Pinned result must not depend on interleaving — anything order-dependent in a merge is a defect.
- [ ] 4.6 In-flight assessments complete under the definition they started with, and the report records which.

## 5. Cost and control

- [ ] 5.1 `CapChecker` ceiling scoped **per assessment, not per node**; adding a node does not raise the budget.
- [ ] 5.2 Ceiling enforced before every provider call on every node.
- [ ] 5.3 Ceiling exhaustion degrades to `not_measured` with `budget exhausted`, the state P33 already defines.
- [ ] 5.4 Rehearsal required before activating a multi-node definition.
- [ ] 5.5 Rollback = activating a previous version, as **one act**; never re-authoring the older shape.

## 6. Frontend Dev — the operator surface

- [ ] 6.1 **Inventory the current axis editor first.** Every control has a named destination or is deliberately removed with agreement. This is the highest-risk UI revision in the program.
- [ ] 6.2 Nine axes and a node dimension. Collapse, do not omit — a hidden axis is indistinguishable from one that does not exist.
- [ ] 6.3 Wiring becomes editable for multi-node and keeps its refusal text for single-node (3.4). Do not delete the reason; render it conditionally.
- [ ] 6.4 Node-level view: which node produced which inference.
- [ ] 6.5 Extend P26's operator build fence to every new axis and node kind — remove an operator surface and the build must fail.
- [ ] 6.6 `scan:tokens` stays green; operator chrome unchanged so the two consoles are never confused.

## 7. AI Engineer

- [ ] 7.1 Evaluate a multi-node definition **per node**, not as one agent. A definition whose critic never disagrees scores well as a whole and is broken in the half that matters.
- [ ] 7.2 Rehearsal compares against the active definition per node and per axis.
- [ ] 7.3 Determinism check: execute the same pinned inference repeatedly under a concurrent definition and assert byte-identical output. Run it repeatedly — this failure is intermittent.
- [ ] 7.4 Establish whether the calibration set exercises a fan-in and a conditional edge at all (2.5).

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
