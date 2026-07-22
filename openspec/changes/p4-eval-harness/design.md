# Design — P4: Eval Harness + eval-set generation + composite scoring

Cross-reference: product rationale in [`../../../docs/prd/P4-eval-harness.md`](../../../docs/prd/P4-eval-harness.md).

## Context

P4 is where the AI Engineer playbook's one law — **evals before optimization** — becomes
infrastructure. It precedes P4.5/P5.5/P6 on the critical path precisely because none of them is
trustworthy without it: attribution, diagnosis, verification, and the autonomous optimizer are all
largely *consumers* of the harness plus a set of operators. Three forces shape every decision here:
LLM outputs are **stochastic** (so single-run comparison lies), synthetic eval sets inherit their
generator's **blind spots** (so coverage must be measured and difficulty tracked), and LLM-as-judge
is itself **noisy and biased** (so it must be calibrated and never allowed to gate uncalibrated).
The phase reuses machinery already built: the IR graph drives coverage, the P2.5 metrics substrate
drives scoring, the P2 run queue + idempotency drive fan-out, and the P3.5 pattern label drives
metric-set selection.

## Decision 1 — Evaluators are pluggable functions over traces, pattern-scoped

**Decision.** An evaluator is a pure function `(trace, case) → MetricValue` that declares its output
range and the set of P3.5 patterns it is admissible for. Built-ins (exact-match, JSON-schema, regex,
LLM-judge) and user-registered custom metrics share one interface and one registry (the skill-
registry pattern). The harness selects each node's metric-set from that node's **pattern label** and
refuses to compute an inadmissible metric on a node.

**Why.** Computing every metric everywhere is both wasteful (judge calls cost money) and *wrong* — a
router scored with relevance@k or a RAG node scored with misroute-rate produces meaningless numbers.
Pattern-scoping (from P3.5) is the dispatcher that keeps the harness honest per node. Evaluators over
*traces* (not over a bespoke eval hook) means every evaluator sees the same tagged substrate P2.5
already emits, and custom metrics need no harness change.

**Alternative rejected.** A fixed metric enum — cannot express domain-specific quality and forces a
harness change per new metric. Computing all metrics then filtering in the UI — pays the judge cost
for numbers no one should read.

## Decision 2 — Statistical honesty is a primitive, not a report

**Decision.** Multi-seed (default N ≥ 5), mean + CI, significance test, and the **tie-on-overlapping-
CIs** rule are wired into the single comparison primitive `Stats.Compare(a, b, metric) →
{mean_a±ci, mean_b±ci, sig, verdict ∈ {a>b, b>a, tie}}`. Every consumer (leaderboard, P5.5
verification, P6 optimizer) goes through it, so no consumer can accidentally rank noise.

**Why.** Reordering deltas are usually within noise; the classic failure is reading a stochastic
blip as a win. Making "tie" a first-class verdict of the comparison function — rather than a UI
annotation — means the honesty cannot be bypassed. The tested claim: a **true-zero-delta** pair
(same config, different label) over ≥ 5 seeds returns `tie`, not a coin-flip winner.

**CI method (Q1/Q2).** Proposed default: **bootstrap** over case-level scores with seeds as a
variance component, and **bootstrap the composite score directly** rather than propagating per-metric
CIs analytically, so normalization + gate interactions are captured. Left as an open question pending
the fixture's noise profile.

## Decision 3 — Judge calibration gates judge trust

**Decision.** Every LLM-judge metric is calibrated against a **human-labeled subset**; its agreement
(κ / % agreement) is computed, persisted with `n_human`, and reported **alongside every score** it
produces. A judge below the agreement floor, or uncalibrated, is **flagged and barred from being a
gate input**.

**Why.** An uncalibrated judge metric is decoration — and letting one drive a disqualifying gate
would let an unverified LLM opinion silently kill a variant. This is the same discipline enforced in
P4.5 diagnosis and P5.5 verification (*no single unverified LLM opinion drives a change*), applied
here at the measurement source. A judge may still *inform* a soft weighted term with its agreement
shown, but it cannot *gate* until calibrated.

**Trade-off.** Requires a human-labeled subset up front (curation cost); acceptable because the
subset is small and is the only thing that makes the judge's thousands of automated scores
trustworthy.

## Decision 4 — Coverage is measured; the generator is a gap-filling loop

**Decision.** "Enough" is defined by a **coverage report**, not by case count. The generator measures
achieved **path** (every IR edge, every branch/router outcome, loops at min/typical/max iterations),
**node** (each node across its input schema), and **edge-case** coverage, then runs a loop —
measure → target the gap → regenerate — until thresholds are met or a max-iteration bound is hit
(reporting the residual on unreachable paths rather than a false "100%").

**Why.** LLM-synthesized sets inherit the generator's blind spots and can be trivially easy; a
passing score on a weak set is worthless. Measuring coverage against the IR graph turns "is this eval
set good enough?" from a vibe into a checkable predicate. The layered generators escalate in cost and
specificity: **seed-from-real-traces** (realistic baseline, active in P5) → **schema-driven**
property/fuzz from typed I/O contracts (cheap, deterministic) → **LLM-driven** targeting the
*specific uncovered paths* the report names + a fixed failure taxonomy → **adversarial perturbation**
of existing cases (robustness). Cheaper layers run first; the LLM is pointed only at the residual.

**Alternative rejected.** "Generate 500 cases and hope" — no guarantee any branch, loop bound, or
edge case is hit; the leaderboard would confidently rank on an eval set that never exercises the
failing path.

**Correction from implementation — "nothing to measure" is not "everything measured".** The first
implementation computed each dimension's achieved fraction as covered/total, which is 1.0 for an
empty set. Running against a real repository whose IR carried **zero edges** (P1 static discovery
finds call sites; inter-node flow is P5) therefore reported *path coverage 100%* for a workflow whose
control flow had never been observed — the same false-100% this decision exists to prevent, reached
from the opposite direction: not by dropping obligations from the denominator, but by never having
any. A dimension with no obligations is now **not measurable**: achieved 0, never met, named in the
report.

## Decision 5 — Gold vs. weak labels; difficulty/diversity as a metric

**Decision.** Each case's reference is labeled **gold** (oracle-derived — exact-match/schema/
deterministic tool — or human-reviewed) or **weak** (LLM-generated, unreviewed). Weak references
**never silently drive** a scoring or gating decision; they are surfaced as weak. The generator also
computes **difficulty** and **diversity** over the set and dedupes near-identical cases; a set below
a difficulty/diversity floor is surfaced as low-confidence.

**Why.** Unverified synthetic references driving scoring is exactly the "confident guessing" the
platform is built to avoid. Flagging gold-vs-weak lets the UI and the gates treat them differently.
Tracking difficulty/diversity as a first-class metric prevents a passing score on a weak set from
being mistaken for a real one. Difficulty operationalization (Q6) is proposed as baseline-model
pass-rate + embedding-space spread for diversity, pending P5 real-trace calibration.

**Correction from implementation — difficulty and diversity describe the INPUTS, and are not enough.**
Both floors passed on a generated set where 12 of 17 cases carried no oracle at all: task success
rested on five cases, and a genuinely broken variant topped the board. Neither metric says whether the
set can answer the question it exists to answer, so a third floor — **oracle coverage** — was added.

**Second correction — an oracle that cannot fail is not an oracle.** Oracle coverage initially counted
oracle PRESENCE. A real repository supplied the counterexample: `{"type": "object"}`, emitted as the
I/O contract for all 40 of its nodes by a frontend that does not resolve types, accepts every possible
output. Schema validity returned 1 for every output of every variant, so a variant answering wrong 70%
of the time scored task success 1.000 and passed the min-quality gate, while coverage reported a
comfortable-looking 4%. The truthful figure was 0.

Decisiveness is therefore **probed, not declared** — the same discipline the rest of the phase applies
to everything else. Two refinements were needed to get the probe right, and both are worth recording
because each looked correct until tested:

- Probing across all of JSON scores `{"type":"object"}` decisive, because it rejects `null`. But a
  workflow that emits objects never emits `null`; discriminating power must be measured **within the
  type the contract declares**.
- Probing the declared shape alone misses that `properties: {a: {type: string}}` IS decisive, because
  it rejects `{"a": 42}`. Probes must also be derived from the **constraints** the contract declares.

This is the same defect class as Decision 6's degenerate metric: an oracle that admits every output
separates nothing, exactly as a metric whose variants all overlap separates nothing.

## Decision 6 — Normalize → gate → weight, with a per-variant score cache

**Decision.** Scoring is a strict pipeline: (1) **normalize** each metric to [0,1] across the variant
set; (2) evaluate **hard-constraint gates** and **disqualify** violators; (3) apply the **weighted
sum** — `Score = w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability − penalties` — only
to gate-passers. Per-variant normalized metric values are **cached** so switching the named weight
profile recomputes only step 3 and **re-ranks without re-executing** any run.

**Why.** Three failures this prevents:
- *Raw units aren't comparable* — $ cost and % quality can't be summed; normalization to [0,1] fixes
  it (Q4: normalization is across the on-board variant set; adding a variant may re-normalize —
  flagged as an open question).
- *Gates as penalties let a cheap-but-broken arrangement top a cost-weighted board* — so gates
  **disqualify** (remove from the ranked order) rather than subtract points. A variant below the
  min-quality gate is excluded even if its weighted score would be highest. Soft preferences are only
  meaningful among variants that already clear the hard floor.
- *Re-scoring to change priorities is slow and expensive* — caching normalized values means a
  profile switch is a weighted sum over cached numbers (< 200 ms for ≤ 500 variants, zero new runs),
  so users explore quality-first vs. cost-optimized freely.

**Trade-off.** Named profiles are a fixed vocabulary (quality-first / cost-optimized / balanced) plus
user-defined; the cache is keyed by `{variant, eval_set_hash}` and invalidated when either changes.

**Correction from implementation — normalization must not amplify noise.** Min-max divides by the
observed spread across the variant set. When that spread is comparable to the measurement noise —
variants that are statistically indistinguishable on a metric — dividing by it spreads pure noise
across the whole [0,1] axis and hands the composite an interval half the board wide. A metric on which
every variant's interval overlaps every other's is now **degenerate**: it normalizes to 1 for everyone
and decides nothing. The predicate is the same one `Stats.Compare` uses for its tie rule, so "these
cannot be told apart" means the same thing in the normalizer as in the comparison primitive.

**Correction — penalties must enter the interval, not just the point.** A penalty derived from a
measured quantity was subtracted as that variant's exact mean, which shifts the composite without
widening it. On a board where every metric was degenerate — hence every interval zero-width — three
indistinguishable variants were ranked 1-2-3 on a difference of 1e-5 that came entirely from penalty
noise, with no tie flag anywhere. Measurement-derived penalties now enter the bootstrap per replicate;
count-derived ones (the weak-labeled fraction) remain exact constants.

## Decision 7 — Leaderboard is a view; Pareto shows the frontier

**Decision.** The leaderboard is a **computed view** over `score_cache` + the active profile + the
gate set, not a materialized table — so it re-ranks instantly and is always consistent with the
cache. Rows show **score ± CI, component-metric breakdown, gate pass/fail, and `config_hash`
lineage**; disqualified variants are a separate section naming the failed gate; CI-overlapping pairs
render as tied. A **Pareto view** surfaces the quality/cost/latency frontier so multi-objective
tradeoffs are visible without collapsing to one number.

**Why.** A single collapsed score hides tradeoffs; the Pareto frontier lets a user see that variant X
is cheaper and variant Y is higher-quality and neither dominates. Making the board a view (not a
table) is what lets the weight-profile switch re-rank without re-execution (Decision 6). Config
lineage on every row is what makes a "win" attributable to an exact configuration — the property
P4.5/P5.5/P6 build on.

## Data model sketch

```
eval_set(eval_set_hash PK, ir_ref, thresholds_json, difficulty, diversity, created_at)
eval_case(case_id PK, eval_set_hash FK, input_blob_hash, reference_blob_hash,
          label ENUM('gold','weak'), path_tags_json, edge_case_kind, created_at)
eval_result(result_id PK, variant_id, config_hash, case_id FK, node_id, seed,
            metric_name, metric_value,
            -- full P0 tag set on every row for sliceability
            run_id, timestamp)
metric_stat(variant_id, config_hash, metric_name, mean, ci_low, ci_high, n_seeds,
            PRIMARY KEY(variant_id, metric_name))
judge_cal(judge_metric_name PK, agreement, n_human, calibrated BOOL, floor)
score_cache(variant_id, eval_set_hash, metric_name, normalized_value,
            PRIMARY KEY(variant_id, eval_set_hash, metric_name))
weight_profile(name PK, w_quality, w_cost, w_latency, w_reliability, penalties_json)
gate_set(name PK, max_cost_per_run, latency_sla, min_quality, provider_allowlist_json)
-- leaderboard is a VIEW: score_cache ⨝ weight_profile, gate_set applied, ranked
```
Case inputs, reference outputs, rendered judge prompts, and per-node I/O live in the object store
keyed by content hash; DB rows hold only the hash and the tags.

## Key interfaces

```
Evaluator(trace, case) -> MetricValue          // declares range + admissible P3.5 patterns
RegisterMetric(name, fn, range, patterns)      // custom metrics, skill-registry pattern
Generator(ir, coverage_gap) -> []EvalCase      // seed | schema | llm | adversarial
Coverage(ir, eval_set) -> CoverageReport       // path/node/edge achieved vs. target
Harness.Run(variant, eval_set, seeds) -> EvalResults      // queue fan-out; pattern-scoped
Stats.Compare(a, b, metric) -> {mean_a±ci, mean_b±ci, sig, verdict∈{a>b,b>a,tie}}
Score(variant, profile) -> {composite±ci, components, gate_status, config_hash}  // gates→cache→sum
```

## Risks

- **False winner across overlapping CIs** — mitigated by making `tie` a verdict of the comparison
  primitive (Decision 2); tested with a true-zero-delta pair.
- **Uncalibrated judge gates a variant** — mitigated by barring below-floor/uncalibrated judges from
  gate inputs (Decision 3).
- **Weak synthetic set / weak labels drive scoring** — mitigated by measured coverage + difficulty/
  diversity + gold-vs-weak flags (Decisions 4, 5).
- **Cheap-but-broken variant tops a cost board** — mitigated by disqualifying gates evaluated before
  weighting (Decision 6).
- **Re-ranking re-executes runs** — mitigated by the per-variant normalized-value cache (Decision 6).
- **A vacuous coverage report reads as complete** — a dimension with no obligations is not-measurable,
  never met (Decision 4 correction). Surfaced by a real repository whose IR carried zero edges.
- **A powerless oracle is counted as evidence** — decisiveness is probed, not declared (Decision 5
  correction). Surfaced by a real repository whose I/O contracts constrained nothing.
- **A board ranks on an axis that was never measured** — currently flagged low-confidence but still
  ranked; whether it should REFUSE to rank is PRD Q8, and whether gate eligibility should generalize
  beyond judges to any low-evidence gate input is PRD Q9. Both are open product calls, not oversights.
- **Normalization set-dependence** — adding a variant may re-normalize existing ones (Q4); flagged,
  with a proposed pinned reference scale for cross-board comparison.
- **Fan-out blows provider budget** — mitigated by queue backpressure + a judge/generation spend cap
  per eval run (DevOps).
