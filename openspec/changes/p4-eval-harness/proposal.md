## Why

After P2 a Variant Spec *runs* and after P2.5 every run emits tagged OTel spans + operational
metrics, but there is still no way to answer the only question that matters: **is variant B actually
better than variant A, and by how much?** Comparisons today are single-run and eyeballed, so
stochastic noise is read as signal and reordering deltas inside the noise band ship as "wins."
Real workflows almost never come with adequate test data, so there is nothing coverage-complete to
run against. LLM-judge scores drive decisions with no calibration against human labels. This is the
amateur loop (*change → eyeball → ship*); P4 replaces it with the senior loop (*build eval set →
measure baseline → change one thing → re-run → keep only if the rise beats noise*).

P4 is the **precondition for the entire intelligence half** (P4.5 attribution, P5.5 verification,
P6 optimizer are all largely *consumers* of it). The AI Engineer's one law governs the phase:
**evals before optimization** — nothing downstream is trustworthy until this harness exists and is
honest about noise. It delivers three tightly-coupled capabilities: an **eval harness** (run any
Variant Spec multi-seed, pluggable metrics over traces, statistical rigor), an **eval-set
generator** (synthesize cases until *measured* coverage crosses thresholds; label gold-vs-weak;
track difficulty/diversity), and **composite scoring + a leaderboard** (normalize → gate →
weight → rank, with CIs and a tie rule).

Depends on P0 (`workflow-ir.schema.json` typed I/O contract + `config_hash`; `metric-event.schema.json`
tag set), P2 (reproducible Runtime + run queue + idempotency), P2.5 (spans + cost/latency/token/
error metrics the evaluators read), P3 (sandbox for adversarial/injection cases), and P3.5 (the
structural pattern label that selects each node's metric-set). Real-trace *seeding* of the generator
and per-path targeting from dynamic traces depend on P5 and are deferred — P4 ships the seed-generator
interface but wires it in P5.

## What Changes

- **New capability `eval-harness`.** Runs a Variant Spec over an eval set for **N seeds** (default
  ≥ 5), fanned out through the run queue, persisting per-case/per-node/per-run results with the full
  P0 tag set. **Evaluators are pluggable functions over traces**: built-in (exact-match,
  JSON-schema, regex, LLM-judge) plus **user-defined custom metrics** registered like skills. The
  **metric-set applied to each node is selected by its P3.5 pattern label** — the harness does not
  compute every metric everywhere (a router isn't scored as a RAG node). Computes task success,
  cost, latency, tokens, tool-error rate, and **per-node contribution** to end-to-end success/cost/
  latency. **Statistical rigor is enforced:** multi-seed, mean + confidence intervals, significance
  tests on deltas, and a **tie when two variants' CIs overlap** — never a false winner. Every
  **LLM-judge** metric reports **agreement against a human-labeled subset**; an uncalibrated or
  below-floor judge is flagged and barred from gating.
- **New capability `eval-set-generation`.** **Measures** achieved **path** (every IR edge, every
  branch/router outcome, loops at min/typical/max iterations), **node** (each node across its input
  schema), and **edge-case** (empty/malformed, tool-returns-nothing, retrieval-miss,
  context-overflow, adversarial/injection, boundaries) coverage, emitting a **coverage report**.
  Runs **layered generators**: seed-from-real-traces (interface present, active in P5), schema-driven
  synthesis (property/fuzz), LLM-driven synthesis **targeting uncovered paths** + a fixed failure
  taxonomy, and adversarial perturbation. A **gap-filling loop** iterates until path/node/edge
  thresholds are met (or reports the residual on unreachable paths). References are labeled **gold**
  (oracle/human-reviewed) or **weak** (LLM-generated, unreviewed); weak references never silently
  drive scoring. **Eval-set difficulty and diversity** are tracked as metrics and near-identical
  cases deduped — a passing score on a weak set is surfaced as low-confidence.
- **New capability `scoring`.** Each metric is **normalized to [0,1]** before weighting;
  `Score = w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability − penalties`. Weights are
  a **named profile** (quality-first / cost-optimized / balanced); per-variant normalized values are
  **cached** so a profile switch **re-ranks without re-executing**. **Hard constraints are
  disqualifying gates, not penalties** — a variant violating max cost/run, latency SLA, min quality,
  or the provider allowlist is **disqualified**; soft weighted preferences apply only to gate-passers.
  Each composite score carries a **CI** (overlap → tie). A **leaderboard** ranks gate-passers with
  **score ± CI, component breakdown, gate pass/fail, and `config_hash` lineage** (disqualified
  variants listed separately with the failed gate named); a **Pareto view** surfaces the
  quality/cost/latency frontier and re-renders on weight change.
- **UI.** Leaderboard + Pareto view + a first-class **coverage report** ("is this eval set good
  enough?") screen; large lists **virtualized**; chart color via the **dataviz** skill; loading /
  error / empty / **partial** (fan-out in progress) / **tie** / **disqualified** states first-class.
- **Deferred:** attribution/clustering/ablation/diagnosis/scorecard (**P4.5**); change operators +
  held-out verification gate (**P5.5**); real-trace seeding + per-path targeting from dynamic traces
  (**P5**); automated search / autonomous optimizer (**P6**); trend view + regression detection +
  budget alerts (**P4.5+**).

## What implementation changed about this proposal

Three claims above were written before the code existed and needed correcting once it did. They are
recorded here rather than silently edited, because the corrections are the useful part:

- **"reporting the residual rather than a false 100%"** understated the failure mode. A false 100%
  arrives two ways: by dropping unreachable obligations from the denominator (anticipated), and by
  having no obligations at all (not anticipated). A real repository supplied the second — static
  discovery finds call sites, not the flow between them, so an IR with zero edges has nothing to
  cover and an empty-set fraction of 1.0 read as complete. A dimension with no obligations is now
  **not measurable**, which is a third state alongside met and unmet.
- **"references are labeled gold or weak"** treated an oracle as a *reference*. An oracle is a
  reference, a decidable schema, or a regex — and, more importantly, only counts as evidence if it
  can **fail**. An unconstrained contract that accepts every output is worse than no oracle: it looks
  measured and decides nothing.
- **"eval-set difficulty and diversity are tracked"** is necessary and not sufficient. Both describe
  the *inputs*; neither says whether the set can answer the question it exists to answer. A third
  floor — **oracle coverage** — was added.

Two questions the same run raised are left open rather than answered unilaterally: whether a board
should **refuse to rank** on an axis that was never measured, and whether **gate eligibility should
generalize** from judges to any gate input whose evidence base is below floor. Both are product
calls; see PRD §14 Q8–Q10.

## Impact

- **Affected capabilities:** `eval-harness` (new), `eval-set-generation` (new), `scoring` (new).
  Consumes `workflow-ir` + `config_hash`/tag contracts (P0), the Runtime + run queue (P2), the
  metrics substrate (P2.5), the sandbox (P3), and the pattern label (P3.5).
- **Affected code/systems:** new eval-harness runner + evaluator plug-in framework (built-in +
  custom registry), eval-set generator (four generator kinds + coverage measurer + gap-filling
  loop), statistics module (multi-seed, CI, significance, tie rule), judge-calibration module,
  composite-scoring engine (normalization + gates + weight profiles + score cache), Postgres schema
  (eval sets/cases/results, metric stats, judge calibration, score cache), object store (case
  inputs/references/judge prompts, content-hashed), run-queue fan-out for seed sweeps + generation,
  and a React leaderboard + Pareto + coverage-report UI.
- **Dependencies:** requires **P0**, **P2**, **P2.5**, **P3**, **P3.5**. Unblocks **P4.5**
  (attribution/diagnosis consume per-node contribution + eval results), **P5** (dynamic traces seed
  the generator), **P5.5** (the harness *is* the verification gate), **P6** (composite score =
  optimizer objective, gates = hard constraints).
