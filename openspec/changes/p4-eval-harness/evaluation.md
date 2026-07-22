# evaluation.md — the evaluator catalogue (P4)

Versioned alongside the P4 spec deltas (task 1.7). This document answers, for every evaluator the
harness can run: **what does it output, where is it allowed to run, and should you believe it?**

Source of truth for the table below is `internal/evalharness` — the table is **generated** from the
registry and `TestEvaluationDocMatchesTheRegistry` fails if the shipped text and the code disagree in
either direction. A published range that no longer matches the enforced range is worse than no
document at all, because a reader has no way to tell.

## 1. What an evaluator is

An evaluator is a pure function over a completed run's **trace** and its **case**:

```
Evaluate(ctx, trace, case, target) -> float64
```

It declares three things, and all three are enforced:

| Declaration | Enforced where | What it prevents |
|---|---|---|
| **Output range** | `Compute` — every value, every time | A custom metric returning 4.2 on a `[0,1]` metric silently entering a weighted sum. The result is flagged **invalid** and discarded, not clamped. |
| **Admissible patterns** | `Admissible` — before the evaluator executes | Relevance@k computed on a router. A pattern-scoped evaluator refuses at run scope and on any other pattern. An inadmissible judge never costs a provider call. |
| **Metric name** | `Register` — at registration | Drift between a plug-in's claim and the P3.5 `metricSets` table. A plug-in declaring itself admissible for `Routing` while computing `relevance_at_k` is **refused at registration**. |

The seven P0 tags are supplied by the harness from the `RunContext`, never by the evaluator. An
evaluator therefore *cannot* emit an under-tagged result — this is the property P2.5's evaluator seam
(`telemetry.EvaluatorInterfaceVersion` 1.0.0) froze, and `AsTelemetryEvaluator` carries a P4
evaluator onto it rather than opening a second emission path.

## 2. Three outcomes, never conflated

| Outcome | Sentinel | What the harness records |
|---|---|---|
| Measured | — | The value. |
| Not admissible here | `ErrInadmissible` | **Nothing**, plus a recorded `Refusal` naming the reason. |
| Nothing to score | `ErrNotApplicable` | **Nothing**. A case with no reference, an uncompilable regex, or a broken output schema is the *eval set's* defect — scoring 0 would blame the variant for it. |
| Value escapes its range | `ErrOutOfRange` | **Nothing**, flagged invalid. |

A zero is only ever written when success was **measured and failed**. "Could not measure" and
"measured and failed" are different facts and a leaderboard that conflates them reports missing
instrumentation as poor quality.

## 3. The evaluator catalogue

<!-- BEGIN GENERATED: evaluator table (internal/evalharness) -->
| Evaluator | Metric | Range | Admissible patterns | Calibration |
|---|---|---|---|---|
| `exact_match` | `exact_match` | [0, 1] | any (pattern-agnostic) | n/a (deterministic oracle) |
| `json_schema_validity` | `schema_valid` | [0, 1] | any (pattern-agnostic) | n/a (deterministic oracle) |
| `regex` | `regex_match` | [0, 1] | any (pattern-agnostic) | n/a (deterministic oracle) |
<!-- END GENERATED -->

`llm_judge` is deliberately **absent from the built-in registry**. It requires a model and a
calibration record, both caller-supplied; a judge that registers itself with neither is exactly the
"uncalibrated judge silently gating" failure Decision 3 exists to prevent. Construct it with
`NewLLMJudge(name, metric, model, cfg, standing, patterns...)` and register it explicitly.

### 3.1 Built-in oracle semantics

- **`exact_match`** — canonical-JSON equality against `case.reference`, using the same canonicalizer
  `config_hash` uses. Two objects differing only in key order or whitespace are the **same answer**;
  calling them different would report a formatting difference as a quality regression.
- **`json_schema_validity`** — validates the output against `case.output_schema`. Remote `$ref` is
  refused: an oracle that reaches over the network is not reproducible, and an eval set whose oracle
  changes when a remote document changes is not an eval set.
- **`regex`** — matches `case.pattern` against the raw output bytes (the JSON string's own quoting
  included), because the pattern is authored against what the workflow actually emits.
- **`llm_judge`** — rubric-driven, structured output (`{score, rationale}`). `score` is a **pointer**
  in the raw verdict so a model answering *without* a score is distinguishable from a score of zero.
  The score is divided by the declared `scale_max`; a 7 on a 1–5 rubric is surfaced as
  `ErrOutOfRange`, never clamped to a perfect score.

## 4. Calibration status and gate eligibility

Every value an `llm_judge` produces carries a `JudgeStanding`:

```
{metric, agreement (Cohen's κ), percent_agreement, n_human, floor, calibrated}
```

`GateEligible()` is the **single predicate** the gate layer consults:

```
calibrated AND n_human > 0 AND agreement >= floor
```

An uncalibrated judge, or one below the floor, may still inform a **soft weighted term** with its
standing shown — it can never be a **hard-constraint gate input**. `n_human` is reported alongside
the agreement because an agreement of 0.9 over n=3 is not evidence, and hiding n is how it gets
treated as if it were.

## 4a. An oracle only counts if it can fail

`Case.HasOracle()` and `Case.HasDecisiveOracle()` answer different questions, and both are needed:

| Predicate | Question | Drives |
|---|---|---|
| `HasOracle` | Does the case carry a reference, schema, or regex? | The **gold/weak label rule**. A case carrying a weak schema is still carrying an oracle and must be labeled as such. |
| `HasDecisiveOracle` | Can that oracle ever return **fail**? | **Oracle coverage** — what the eval set's report card counts as evidence. |

Decisiveness is **probed, not declared**. A reference is decisive by construction (some other output
differs from it). A schema or regex is run against probe documents, and if it accepts all of them it
is not a decision procedure:

- Probes are drawn from **the type the contract declares**. Probing across all of JSON would score
  `{"type":"object"}` decisive because it rejects `null` — but a workflow that emits objects never
  emits `null`, and rejecting an output the workflow could never produce is not discriminating power.
- Probes are also derived from **the constraints the contract declares**: one wrong-typed value per
  declared property, an object omitting required properties, a value outside a declared enum.
  Without these, `properties: {a: {type: string}}` would score indecisive — it accepts every generic
  object probe, yet genuinely rejects `{"a": 42}`.

This is the same rule the scoring layer applies to metrics (`scoring.separates`): a metric on which
every variant's interval overlaps every other's decides nothing, and an oracle that admits every
output decides nothing either.

**Why it is here and not in the evaluator.** The schema-validity evaluator behaves correctly when
handed an unconstrained schema — it reports that the output is valid, which it is. The defect is in
counting that verdict as evidence. So the fix lives in the eval set's report card, not in the
evaluator.

## 5. The standard family (no configuration required)

Computed from the traces for every run, with no per-workflow instrumentation:

| Metric | Unit | Derived from |
|---|---|---|
| `task_success` | ratio | The case's success oracle (see §6). |
| `eval_cost_usd` | usd | Sum of node-span `cost_usd`. |
| `eval_latency_ms` | ms | The run span's wall-clock, falling back to the node-span envelope. **Not** a sum of node latencies — that would over-count a parallel fan-out and report a workflow as slower than a stopwatch says it is. |
| `eval_tokens_total` | tokens | Sum of node-span `gen_ai.usage.{input,output}_tokens`. |
| `tool_error_rate` | ratio | Failed tool spans / all tool spans. |
| `reliability` | ratio | 1 iff the run completed with no failed node, no failed tool call, and no contract violation. |

### Per-node contribution

| Metric | Unit | Meaning |
|---|---|---|
| `node_cost_share` | ratio | This node's share of the run's cost. |
| `node_latency_share` | ratio | This node's share of the summed node latency. |
| `node_success_impact` | ratio | 1 for the **first** failing node in execution order only. Crediting every downstream node would multiply one root cause into a cluster and send P4.5 attribution chasing symptoms. |
| `node_tool_error_count` | count | This node's failed tool calls. |

These are persisted as **rows**, not computed in a report, so per-node contribution is queryable per
case and per node — the substrate P4.5 attribution consumes.

## 6. Success-oracle precedence

When `case.success_oracle` is empty: `exact_match` → `json_schema_validity` → `regex` → `llm_judge`.
Deterministic oracles come first because they are gold (decidable, reviewable, free) and a judge call
a free oracle could have answered is spend with no added information. A case that genuinely wants the
judge **names it**, because a case carrying both a reference and a rubric is genuinely ambiguous and
guessing is how a cheap oracle silently replaces the judge the author paid for.

## 7. Pattern-driven metric-set selection

`BuildPlan(ir, registry)` walks the IR and, per node, resolves the winning P3.5 label (a **confirmed**
label beats a behavioral **candidate** at equal confidence — dispatching measurement off a hypothesis
is how a node gets scored on metrics its region does not implement), then selects only the evaluators
that label admits. Every refusal is **recorded with its reason**, so "why is there no relevance@k on
node `router`?" is answerable without re-running anything.
