# Proposal Engine — Spec (folded from P5.5)

Product rationale: [`../../../docs/prd/P5.5-proposals-verification.md`](../../../docs/prd/P5.5-proposals-verification.md) §6 (FR1–FR5).

Covers the diagnosis→operator mapping (the change-operator catalog), grounded prompt optimization,
pattern- and contract-gated operator emission, the deterministic AST-level codemod that turns each
candidate Variant Spec into a **concrete source diff** (ADR-001), the **build-preserving** gate that
rejects a non-building diff before it is surfaced, ranking by expected gain / cost-of-change under
hard constraints, and reviewable-source-diff-with-evidence presentation.

## Requirements

### Requirement: Each diagnosis SHALL map to change operators that emit candidate Variant Specs and a concrete source diff

Each P4.5 diagnosis SHALL map to one or more **change operators** per the catalog, and each operator
SHALL emit one or more **candidate Variant Specs**, content-hashed and referencing registry entries
by ID, **and the concrete source diff produced by a deterministic AST-level codemod** that rewrites
the discovered call site(s) to the candidate's values (ADR-001). The same `config_hash` against the
same source SHALL produce a **byte-identical diff**, and the diff SHALL change only the configured
dimension(s) at the targeted call site(s). Diagnosis→operator mapping:
reasoning-heavy-node-on-weak-model → upgrade model / enable extended thinking;
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
- **AND** its codemod emits a concrete source diff that rewrites only node N's model argument at the
  discovered call site, leaving every other call site unchanged

#### Scenario: The codemod is deterministic

- **WHEN** the same candidate `config_hash` is compiled against the same source twice
- **THEN** both runs produce a byte-identical source diff (content-hashed to the same value)

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

### Requirement: A proposed source diff SHALL build before it is surfaced

Each candidate's source diff SHALL be applied to an **isolated worktree/branch** (never the user's
working tree in place) and SHALL **build/compile the target** before the candidate is surfaced.
A candidate whose diff fails to build SHALL be **rejected before surfacing** — it SHALL NOT be ranked,
verified, or presented as a recommendation.

#### Scenario: A proposed change that fails to build is rejected before surfacing

- **WHEN** an operator emits a candidate whose codemod produces a source diff that does not
  compile/build the target
- **THEN** the candidate is marked `build_failed` and is not ranked, verified, or surfaced
- **AND** the failure is recorded for diagnostics rather than shown to the user as a recommendation

#### Scenario: The transform is applied to an isolated worktree, not the user's tree

- **WHEN** a candidate's source diff is applied for the build check
- **THEN** it is applied to an isolated worktree/branch
- **AND** the user's working tree is not mutated in place

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

### Requirement: Each candidate SHALL be presented as a reviewable source diff with the diagnosis and failing cases as evidence

Each candidate SHALL be presented as a **reviewable source diff** (the codemod output against the
user's source), paired with the Variant-Spec diff against the baseline, with the originating diagnosis
and the **specific failing cases** attached as evidence.

#### Scenario: A candidate carries its source diff and evidence

- **WHEN** a candidate Variant Spec is prepared for presentation
- **THEN** the candidate is rendered as a reviewable source diff showing exactly which call-site
  code changed, paired with the Variant-Spec diff showing which node dimension(s) changed
- **AND** the originating diagnosis is attached
- **AND** the specific failing cases that motivated the diagnosis are attached as evidence
