# Eval Harness — Spec Delta (P4)

Product rationale: [`../../../../docs/prd/P4-eval-harness.md`](../../../../docs/prd/P4-eval-harness.md) §6 (FR1–FR7).

Covers pluggable evaluators (built-in + custom), pattern-driven metric-set selection, per-node
contribution, multi-seed statistical rigor with confidence intervals and the tie rule, and
LLM-judge calibration.

## ADDED Requirements

### Requirement: The harness SHALL run a Variant Spec over an eval set for N seeds and persist fully-tagged per-case results

The harness SHALL execute a Variant Spec over every case in an eval set for a configurable number of
seeds N (default N ≥ 5), fanned out through the run queue, and SHALL persist per-case, per-node, and
per-run results tagged with the full P0 tag set `{variant_id, run_id, node_id, case_id, seed,
config_hash}`.

#### Scenario: Multi-seed fan-out produces tagged results

- **WHEN** a Variant Spec is run over a 50-case eval set with N = 5 seeds
- **THEN** 250 runs are fanned out through the run queue and executed
- **AND** every persisted result row carries a non-null `variant_id`, `run_id`, `node_id`,
  `case_id`, `seed`, and `config_hash`
- **AND** results aggregate incrementally so partial progress is queryable before the fan-out
  completes

### Requirement: Evaluators SHALL be pluggable functions over traces, with built-in and user-registered custom metrics

An evaluator SHALL be a function over a run trace and its case producing a metric value, declaring
its output range. The harness SHALL provide built-in evaluators exact-match, JSON-schema validity,
regex, and LLM-judge, and SHALL allow **user-defined custom metrics** to be registered by name (the
same mechanism as the skill registry) and used identically.

#### Scenario: Built-in and custom evaluator both compute over the same trace

- **WHEN** a built-in exact-match evaluator and a user-registered custom metric are both applied to
  a completed run's trace
- **THEN** each produces a metric value within its declared range for each applicable case
- **AND** the custom metric requires no change to the harness to run

#### Scenario: Custom metric is rejected if its value escapes its declared range

- **WHEN** a registered custom metric returns a value outside the range it declared at registration
- **THEN** the harness flags the result as invalid rather than recording an out-of-range score

### Requirement: The harness SHALL select each node's metric-set from its P3.5 pattern label and SHALL NOT compute an inadmissible metric on a node

Each evaluator declares the set of P3.5 patterns it is admissible for. The harness SHALL apply to a
node only the metrics admissible for that node's pattern label, and SHALL NOT compute a metric on a
node whose pattern does not admit it.

#### Scenario: Router is not scored as a RAG node

- **WHEN** the harness evaluates a workflow containing a node labeled `Routing` and a node labeled
  `Retrieval (RAG)`
- **THEN** the `Routing` node is scored with misroute-rate / routing-accuracy metrics
- **AND** relevance@k is computed on the `Retrieval (RAG)` node and **not** on the `Routing` node
- **AND** misroute-rate is **not** computed on the `Retrieval (RAG)` node

### Requirement: The harness SHALL compute the standard metric family and per-node contribution from traces

The harness SHALL compute task success (via rubric / LLM-judge / exact-match / regex per task),
cost, latency, token usage, and tool-error rate, and SHALL decompose end-to-end success, cost, and
latency into a **per-node contribution** using the OTel traces.

#### Scenario: Per-node contribution decomposes an end-to-end failure

- **WHEN** a workflow scores 60% task success over an eval set
- **THEN** the harness attributes, per node, that node's contribution to the end-to-end
  success/cost/latency from the traces
- **AND** the per-node contribution is queryable per case and per node for the failing cases

### Requirement: The harness SHALL run multi-seed and report every metric as a mean with a confidence interval, with a significance test on pairwise deltas

For every metric the harness SHALL aggregate across the N seeds and report a mean and a confidence
interval, and SHALL run a significance test on each pairwise variant delta.

#### Scenario: Metric is reported with a CI, not a point value

- **WHEN** a metric is aggregated across 5 seeds for a variant
- **THEN** the harness reports the metric as a mean with a confidence interval and the seed count `n`
- **AND** a bare point value with no CI is never surfaced as the comparison result

### Requirement: When two variants' confidence intervals overlap the harness SHALL declare a tie and not a winner

When the confidence intervals of two variants on the comparison metric overlap, the harness SHALL
label the pair a **tie** and SHALL NOT declare either the winner.

#### Scenario: True-zero-delta pair is a tie, not a coin-flip winner

- **WHEN** two variants with a true delta of zero (identical configuration, different label only) are
  compared over 5 seeds
- **THEN** their confidence intervals overlap
- **AND** the comparison returns `verdict = tie`
- **AND** neither variant is declared the winner

#### Scenario: Known-real-delta pair yields a winner with non-overlapping CIs

- **WHEN** two variants with a large, real quality delta are compared over 5 seeds
- **THEN** their confidence intervals do not overlap and the significance test fires
- **AND** the comparison returns the correct variant as the winner

### Requirement: Every LLM-judge metric SHALL report its agreement against a human-labeled subset, and an uncalibrated or below-floor judge SHALL be barred from gating

For every LLM-judge metric the harness SHALL compute the judge's agreement (e.g. Cohen's κ / %
agreement) against a human-labeled calibration subset and report it alongside every score the judge
produces. A judge whose agreement is below the configured floor, or that is uncalibrated, SHALL be
flagged and SHALL NOT be usable as an input to a hard-constraint gate.

#### Scenario: Judge score is reported with its calibration agreement

- **WHEN** an LLM-judge metric produces a score for a case
- **THEN** the judge's agreement against the human-labeled subset (with `n_human`) is reported
  alongside that score

#### Scenario: Below-floor judge cannot gate

- **WHEN** an LLM-judge metric's agreement against the human-labeled subset is below the configured
  floor
- **THEN** the judge metric is flagged as uncalibrated-for-gating
- **AND** any attempt to use it as a hard-constraint gate input is refused
- **AND** a gate configured on that judge does not disqualify any variant
