# Proposal Engine — Spec Delta (P5.5)

Product rationale: [`../../../../docs/prd/P5.5-proposals-verification.md`](../../../../docs/prd/P5.5-proposals-verification.md) §6 (FR1–FR5).

Covers the diagnosis→operator mapping (the change-operator catalog), grounded prompt optimization,
pattern- and contract-gated operator emission, ranking by expected gain / cost-of-change under hard
constraints, and diff-with-evidence presentation.

## ADDED Requirements

### Requirement: Each diagnosis SHALL map to change operators that emit candidate Variant Specs

Each P4.5 diagnosis SHALL map to one or more **change operators** per the catalog, and each operator
SHALL emit one or more **candidate Variant Specs**, content-hashed and referencing registry entries
by ID: reasoning-heavy-node-on-weak-model → upgrade model / enable extended thinking;
cheap-task-on-expensive-model → downgrade; prompt/output-contract violation → rewrite prompt + add
format constraints/schema; context overflow / lost-in-middle → switch context policy (summarization
/ sliding window) or reorder; RAG relevance low → tune top-k / swap retriever/embedding / add
rerank; missing/erroring tool → add skill from registry / fix schema binding; redundant node →
prune / merge.

#### Scenario: A weak-model diagnosis emits a model-upgrade candidate

- **WHEN** a diagnosis names node N a reasoning-heavy node running on a weak model
- **THEN** the model-upgrade operator emits at least one candidate Variant Spec that differs from the
  baseline only in node N's `model_ref` (a stronger model and/or an enabled extended-thinking budget)
- **AND** the candidate is content-hashed with its own `config_hash`
- **AND** it references the upgraded model by registry ID, not by inlined configuration

#### Scenario: A RAG-relevance diagnosis emits a rerank candidate

- **WHEN** a diagnosis names a `Retrieval (RAG)` node as returning low-relevance chunks
- **THEN** the RAG-tune operator emits candidate Variant Specs that vary retrieval (e.g. increased
  top-k, a swapped retriever/embedding, or an added rerank node)
- **AND** each candidate is a valid Variant Spec the runtime can execute

### Requirement: Prompt-rewrite operators SHALL use a grounded optimizer traceable to the failing cases

A prompt-rewrite operator SHALL produce its edit with a DSPy-style / self-refine optimizer
**grounded in the specific failing cases** attached to the diagnosis — not a generic instruction —
and the produced edit SHALL be **traceable** to the failing cases that motivated it. An ungrounded
generic rewrite SHALL be rejected.

#### Scenario: Prompt rewrite is grounded in the attached failing cases

- **WHEN** a prompt/output-contract-violation diagnosis with three attached failing cases is passed
  to the prompt-rewrite operator
- **THEN** the operator produces a new `prompt_ref` (plus a format-constraint/schema where the
  contract was violated) derived from those three failing cases
- **AND** the produced edit records a traceable link to the failing cases that grounded it
- **AND** a rewrite produced without grounding in the attached cases is rejected rather than emitted

### Requirement: An operator SHALL be emitted only where valid for the node's pattern and typed I/O contract

An operator SHALL be emitted only where its candidate Variant Spec satisfies the P5 typed per-node
I/O contract (contract-valid, with any required adapters flagged as in P5) **and** the operator is
admissible for the node's pattern label. An inadmissible operator or a contract-violating candidate
SHALL NOT be emitted.

#### Scenario: Add-rerank is gated to RAG nodes

- **WHEN** the engine considers the `add rerank` operator for a node labeled `Routing`
- **THEN** no `add rerank` candidate is emitted for the `Routing` node
- **AND** for a node labeled `Retrieval (RAG)` with the same signal, an `add rerank` candidate is
  emitted

#### Scenario: A contract-violating candidate is not emitted

- **WHEN** an operator would produce a candidate whose changed node output no longer satisfies a
  downstream node's typed input contract, and no adapter can reconcile it
- **THEN** the candidate is not emitted
- **AND** the engine records the contract violation rather than surfacing a broken Variant Spec

### Requirement: Candidates SHALL be ranked by expected gain / cost of change and SHALL respect hard constraints

Candidates SHALL be ranked by **expected gain / cost of change**, and SHALL respect the user's hard
constraints (budget ceiling, latency SLA, provider allowlist). A candidate that would violate a hard
constraint SHALL NOT be ranked as a recommendation; it MAY be listed separately as
constraint-excluded with the violated constraint named.

#### Scenario: A budget-violating candidate is not ranked as a recommendation

- **WHEN** a model-upgrade candidate's projected cost per run exceeds the user's budget ceiling
- **THEN** the candidate is marked constraint-excluded and is not placed in the ranked recommendation
  order
- **AND** the budget ceiling is named as the violated constraint
- **AND** a cheaper, admissible candidate for the same diagnosis is ranked ahead of it

#### Scenario: Ranking orders by gain per unit cost-of-change

- **WHEN** two admissible candidates for a diagnosis have equal expected gain but different cost of
  change
- **THEN** the candidate with the lower cost of change is ranked ahead of the other

### Requirement: Each candidate SHALL be presented as a diff against the current Variant Spec with the diagnosis and failing cases as evidence

Each candidate SHALL be presented as a **diff against the current (baseline) Variant Spec**, with the
originating diagnosis and the **specific failing cases** attached as evidence.

#### Scenario: A candidate carries its diff and evidence

- **WHEN** a candidate Variant Spec is prepared for presentation
- **THEN** the candidate is rendered as a diff against the baseline Variant Spec showing exactly which
  node dimension(s) changed
- **AND** the originating diagnosis is attached
- **AND** the specific failing cases that motivated the diagnosis are attached as evidence
