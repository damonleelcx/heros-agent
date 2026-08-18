# Tasks — P36: The Agent Is a Graph

> **Nothing here is implemented.** Documents only, as the whole GEHA program is.
> **Task 1.1 comes before everything.** If a single-node definition cannot be made hash-identical, this is
> a migration of every pinned inference rather than an additive change, and the phase is a different size.

## 1. Hash compatibility — establish this first

- [ ] 1.1 Prove that a definition with one node, no `order`, no `edges`, no `graph_groups` and no `loop_ref` marshals to **byte-identical** bytes and produces the same `config_hash` as its pre-P36 form. Establish whether a nested `nodes` array can do this or a compatibility encoding is required, **before building anything else**.
- [ ] 1.2 Fixture captured from before the change — not reconstructed after it — that resolves and reproduces its hash.
- [ ] 1.3 An existing pinned inference remains readable and names the `config_hash` that produced it.
- [ ] 1.4 Activating a new definition does **not** re-run pinned inferences; assert no provider call.
- [ ] 1.5 A pin whose shape is no longer authorable renders **stale with its producing configuration named** — neither absent nor current.

## 2. System Designer

- [ ] 2.1 Answer PRD §14 Q1: per-node credentials or one per definition. `CriticModelRef` is the existing precedent for a second model.
- [ ] 2.2 Answer PRD §14 Q2: is the producing node ever shown to a customer, or operator-side only.
- [ ] 2.3 Answer PRD §14 Q3: is `placement` per node. Note that this would turn a gate both runners call and neither can skip into a per-node decision.
- [ ] 2.4 Answer PRD §14 Q4: activation during an in-flight assessment.
- [ ] 2.5 Answer PRD §14 Q5: does the rehearsal calibration set need to grow to exercise a fan-in and a conditional edge. A rehearsal that cannot fail on the new capability is not a rehearsal of it.
- [ ] 2.6 Record D5 (no self-modification) where it will be found by whoever proposes it.

## 3. Backend Dev — the definition

- [ ] 3.1 `NodeID` from package constant to data; move `definition.go`, `axiseditor.go`, `inferencestore.go`, `placement.go`, `caps.go` and the fences **together**.
- [ ] 3.2 `AuthorableAxes()` returns nine; `loop` and `graph` are registry references, never inlined.
- [ ] 3.3 Extend the **reflective credential fence** to every new field. A fence enumerating the old shape passes vacuously on the new one — add a key-shaped field to the new struct and require the fence to fail.
- [ ] 3.4 `ErrWiringOverride` narrows: still refused for a single-node definition, authorable for multi-node.
- [ ] 3.5 `ErrHostServiceMissing` extends to the loop axis, refusing at **publish** rather than at run.
- [ ] 3.6 Loop turns validated against the node's harness envelope ceiling at publish, naming both values.
- [ ] 3.7 `ErrNoChange` still refuses to mint a duplicate version.
- [ ] 3.8 Per-node attribution on every inference: node id and definition version.
- [ ] 3.9 Migration for the definition store: repeatable, success on a second run, idempotency guard named in the commit body, and existing single-node rows read back byte-identically (1.1).

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
