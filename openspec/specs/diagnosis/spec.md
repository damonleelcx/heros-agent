# Diagnosis — Spec (folded from P4.5)

Product rationale: [`../../../docs/prd/P4.5-attribution-diagnosis.md`](../../../docs/prd/P4.5-attribution-diagnosis.md) §6 (FR6–FR12).

Covers the enumerated rule-based detectors (deterministic, rules-first), the LLM-as-analyst
constrained to a fixed failure taxonomy + confidence with calibration and agreement reporting,
pattern-scoped failure modes, failing-case evidence on every diagnosis, and the phase's read-only
guarantee.

## Requirements

### Requirement: The engine SHALL provide deterministic rule-based detectors emitting a typed cause from the fixed failure taxonomy

The engine SHALL provide rule-based detectors over traces, each deterministic (the same trace yields
the same result) and each emitting a **typed cause** from the fixed failure taxonomy. The detectors
SHALL include at least: context overflow/truncation before a failing node; tool schema mismatch /
repeated tool errors; retrieval miss (low-relevance chunks); prompt-format drift (output contract
ignored → downstream parse failure); lost-in-the-middle (over-long context degrading a later node);
and model-capability mismatch (cheap model on a reasoning-heavy node).

#### Scenario: The prompt-format-drift detector fires deterministically

- **WHEN** a failing case's trace shows a node emitting output that violates its output contract,
  causing a downstream parse failure
- **THEN** the prompt-format-drift detector emits the corresponding typed cause from the fixed taxonomy
- **AND** re-running the detector on the same trace emits the identical typed cause

#### Scenario: The context-overflow detector fires on truncation before a failing node

- **WHEN** a failing case's trace shows the context truncated before the node that then fails
- **THEN** the context-overflow/truncation detector emits the corresponding typed cause

### Requirement: The rule detectors SHALL run first and the LLM-analyst SHALL be invoked only on the unexplained residue

The engine SHALL run the deterministic rule detectors first on every failing case, and SHALL invoke
the LLM-as-analyst **only on the cases the rules did not explain** (the residue).

#### Scenario: The analyst is called only on the residue

- **WHEN** 100 cases fail and the rule detectors explain 80 of them
- **THEN** the LLM-analyst is invoked on the remaining 20 cases only
- **AND** the analyst is not invoked on the 80 cases a rule already explained

### Requirement: The LLM-analyst SHALL emit a diagnosis constrained to the fixed failure taxonomy plus a confidence score, and SHALL reject off-taxonomy output

The LLM-as-analyst SHALL receive a failing case's full trace and a structured rubric and SHALL emit a
diagnosis constrained to the **fixed failure taxonomy** together with a **confidence score**. An
off-taxonomy or free-text label SHALL be rejected rather than recorded.

#### Scenario: A valid analyst diagnosis carries a taxonomy code and confidence

- **WHEN** the analyst diagnoses a residue case
- **THEN** the recorded diagnosis carries a taxonomy code drawn from the fixed failure taxonomy and a
  confidence score

#### Scenario: An off-taxonomy analyst response is rejected

- **WHEN** the analyst returns a free-text label or a code not in the fixed failure taxonomy
- **THEN** the engine rejects the response and records no diagnosis for it
- **AND** the out-of-taxonomy label is never surfaced as a diagnosis

### Requirement: The LLM-analyst SHALL be calibrated against a human-labeled subset with its agreement reported alongside every diagnosis, and no unverified analyst opinion SHALL drive a change

The engine SHALL calibrate the analyst against a human-labeled subset, compute its agreement (e.g.
Cohen's κ / % agreement, with `n_human`), and report that agreement **alongside every diagnosis** the
analyst emits. An uncalibrated or below-floor analyst SHALL be flagged. No single unverified analyst
diagnosis SHALL drive a change — the engine has no apply path, and the diagnosis is a report only.

#### Scenario: Analyst agreement is reported with every analyst diagnosis

- **WHEN** the analyst emits a diagnosis for a case
- **THEN** the analyst's agreement against the human-labeled subset (with `n_human`) is reported
  alongside that diagnosis

#### Scenario: A below-floor analyst is flagged and drives nothing

- **WHEN** the analyst's agreement against the human-labeled subset is below the configured floor
- **THEN** the analyst is flagged as uncalibrated-for-trust
- **AND** its diagnoses are surfaced only as flagged, low-trust reports
- **AND** no analyst diagnosis triggers any change to a Variant Spec, registry, or config

### Requirement: Diagnosis SHALL degrade explicitly for an unclassified node, running only pattern-agnostic detectors

When a node carries **no P3.5 pattern label** (an unclassified node — e.g. a hand-rolled agent's call
site with no discovered graph), the engine SHALL check **only the pattern-agnostic** failure modes
(context overflow / truncation, prompt-format drift, lost-in-the-middle, model-capability mismatch,
and the tool-schema / retrieval detectors where their trace signal is present), SHALL **refuse** the
pattern-scoped modes (misroute, infeasible/circular plan, non-convergence / degradation-on-revision),
and SHALL surface the node as **"not classified"** — the reduced coverage stated, never hidden behind
a silent default pattern. A silent fallback to a default pattern SHALL be logged (WARN), not applied.

#### Scenario: An unclassified node runs pattern-agnostic detectors only

- **WHEN** the engine diagnoses a node that has no P3.5 pattern label and whose trace shows a
  context-overflow signal
- **THEN** the context-overflow (pattern-agnostic) typed cause is emitted
- **AND** no pattern-scoped cause (misroute, non-convergence, infeasible/circular plan) is emitted for
  that node
- **AND** the node is surfaced as **"not classified"** with its coverage limited to pattern-agnostic
  checks

#### Scenario: A pattern label sharpens diagnosis but is not required

- **WHEN** the same failing case is diagnosed once with the node's P3.5 pattern label present and once
  with it absent
- **THEN** with the label, the pattern-scoped modes the pattern admits are additionally checked
- **AND** with no label, only the pattern-agnostic modes are checked and the node reads "not
  classified"
- **AND** both runs produce a diagnosis with evidence rather than refusing outright

### Requirement: The engine SHALL diagnose only the failure modes admitted by a node's pattern label

The engine SHALL read each node's P3.5 structural pattern label and check **only** the failure modes
that pattern can exhibit — Routing → misroutes; Planning → infeasible/circular plans; Reflection →
non-convergence / degradation-on-revision — and SHALL NOT diagnose a node with a failure mode its
pattern cannot exhibit.

#### Scenario: A router is not diagnosed with a RAG failure mode

- **WHEN** the engine diagnoses a node labeled `Routing` and a node labeled `Reflection`
- **THEN** the `Routing` node is checked for misroutes and not for a retrieval-miss (RAG) failure mode
- **AND** the `Reflection` node is checked for non-convergence / degradation-on-revision

### Requirement: Every diagnosis SHALL attach the specific failing cases as evidence

Each diagnosis SHALL attach the specific failing cases (the `case_id`s and/or trace excerpts that
triggered it) as evidence — a diagnosis SHALL NOT be surfaced as a bare label.

#### Scenario: A diagnosis card carries its failing-case evidence

- **WHEN** a diagnosis is surfaced on the scorecard
- **THEN** it names the typed cause, its source (rule or analyst), and its confidence/agreement
- **AND** it attaches the specific failing `case_id`s that triggered it as evidence

### Requirement: The engine SHALL be read-only — it SHALL mutate nothing and SHALL emit no proposal

The engine SHALL mutate no Variant Spec, registry, node config, or workflow, and SHALL emit **no
proposal**. Its only outputs SHALL be reports (attributions, clusters, ablation deltas, bottleneck
flags, diagnoses, scorecard).

#### Scenario: A full diagnosis run changes nothing and proposes nothing

- **WHEN** a full attribution + diagnosis run completes over a failing variant
- **THEN** every Variant Spec, registry entry, and node config is byte-identical to before the run
  (same `config_hash`)
- **AND** zero proposal records are created
- **AND** the only outputs are read-only reports (attributions, clusters, ablation deltas, bottleneck
  flags, diagnoses, scorecard)
