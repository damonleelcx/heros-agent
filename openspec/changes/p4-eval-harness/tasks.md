# Tasks — P4: Eval Harness + eval-set generation + composite scoring

## 1. AI Engineer — Evaluator plug-in framework
- [ ] 1.1 Define the `Evaluator(trace, case) → MetricValue` interface: declares output range and the
      set of P3.5 patterns it is admissible for.
- [ ] 1.2 Implement built-in evaluators: **exact-match**, **JSON-schema validity**, **regex**,
      **LLM-judge** (rubric-driven, structured output).
- [ ] 1.3 Implement the **custom-metric registry** — register a scoring function by name the same way
      skills are registered; validate its declared range at registration.
- [ ] 1.4 Implement **pattern-driven metric-set selection**: read each node's P3.5 label and apply
      only admissible metrics; refuse to compute an inadmissible metric on a node.
- [ ] 1.5 Compute the standard metric family — task success (rubric/judge/exact/regex per task),
      cost, latency, tokens, tool-error rate — from the P2.5 traces.
- [ ] 1.6 Implement **per-node contribution** decomposition of end-to-end success/cost/latency from
      the traces (the substrate P4.5 attribution consumes).
- [ ] 1.7 Author `evaluation.md`: every evaluator, its admissible patterns, output range, and
      calibration status, versioned alongside the specs.

## 2. AI Engineer — Statistical rigor (multi-seed, CI, tie rule)
- [ ] 2.1 Run each metric **multi-seed** (N configurable, default ≥ 5); persist per-seed values.
- [ ] 2.2 Compute **mean + confidence interval** per variant × metric (bootstrap over case-level
      scores with seeds as a variance component — see design Q1).
- [ ] 2.3 Implement `Stats.Compare(a, b, metric)` → significance test on the pairwise delta.
- [ ] 2.4 **Tie rule:** when the two variants' CIs on the comparison metric overlap, return
      `verdict = tie` and declare **no** winner. Wire this into the comparison primitive itself.
- [ ] 2.5 Test with a **true-zero-delta** pair (same config, different label): assert the result is
      `tie`, not a coin-flip winner. Test a **known-real-delta** pair: assert the correct winner with
      non-overlapping CIs.

## 3. AI Engineer — Judge calibration
- [ ] 3.1 Accept a **human-labeled calibration subset** per LLM-judge metric.
- [ ] 3.2 Compute judge **agreement** (e.g. Cohen's κ / % agreement) against the human subset and
      persist it with `n_human`.
- [ ] 3.3 Report agreement **alongside every score** the judge produces.
- [ ] 3.4 **Gate-eligibility floor:** a judge whose agreement is below the configured floor, or that
      is uncalibrated, is **flagged and barred from being a gate input**; assert a below-floor judge
      cannot gate.

## 4. AI Engineer — Eval-set generation & coverage
- [ ] 4.1 Implement **coverage measurement**: path (every IR edge, every branch/router outcome,
      loops at min/typical/max iterations), node (each node across its input schema), edge cases
      (empty/malformed, tool-returns-nothing, retrieval-miss, context-overflow, adversarial/
      injection, boundaries). Emit a `CoverageReport` (achieved vs. target).
- [ ] 4.2 Implement the four **layered generators** behind one `Generator(ir, gap) → []EvalCase`
      interface: seed-from-real-traces (interface only, active in P5), schema-driven (property/fuzz
      from typed I/O contracts), LLM-driven (**targets uncovered paths** + fixed failure taxonomy),
      adversarial perturbation.
- [ ] 4.3 Implement the **gap-filling loop**: measure → target gaps → regenerate, iterating until
      path/node/edge thresholds are met or a max-iteration bound is hit (report residual if unmet).
- [ ] 4.4 Test: the loop **iterates until measured path coverage ≥ threshold**; on an unreachable
      path it terminates at max iterations and reports the residual gap (no false "100%").
- [ ] 4.5 Implement **reference labeling**: auto-label where an oracle exists (exact-match/schema/
      deterministic tool); else LLM-generated + human-review-subset, or reference-free metric. Tag
      each case **gold** vs. **weak**; block weak references from silently gating.
- [ ] 4.6 Implement **difficulty + diversity** metrics over the eval set; **dedupe** near-identical
      cases; surface a below-floor set as low-confidence.

## 5. AI Engineer + System Designer — Composite scoring & gates
- [ ] 5.1 **Normalize** each metric to [0,1] (min-max / z-score across the variant set) before it
      enters the weighted sum.
- [ ] 5.2 Implement the composite:
      `Score = w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability − penalties`.
- [ ] 5.3 Define **named weight profiles** (quality-first / cost-optimized / balanced); **cache**
      per-variant normalized metric values so a profile switch recomputes only the weighted sum.
- [ ] 5.4 **Re-rank without re-executing:** switching profiles enqueues **zero** new runs and
      re-ranks < 200 ms for ≤ 500 variants; assert both.
- [ ] 5.5 Implement **hard-constraint gates** (max cost/run, latency SLA, min quality, provider
      allowlist): a violating variant is **disqualified** (excluded from the ranked order), not
      penalized. Evaluate gates **before** weighting; apply soft weights **only** to gate-passers.
- [ ] 5.6 Test **gate/weight separation:** a cheapest-but-below-min-quality variant is disqualified
      and listed separately with the failed gate named — **not** ranked #1 on the cost-optimized
      profile.
- [ ] 5.7 Report each composite score with a **CI**; two variants with overlapping composite-score
      CIs are shown **tied**.

## 6. System Designer + DevOps — Run fan-out & data path
- [ ] 6.1 Queue-driven **fan-out** for multi-seed sweeps and generation: bounded concurrency,
      backpressure, idempotent re-delivery (inherits P2 idempotency — no double-charge on redelivery).
- [ ] 6.2 Persist eval results with the **full P0 tag set** so every leaderboard slice (per pattern,
      per failure cluster, per node, per seed) is a query, not a re-run. Assert **tag completeness**.
- [ ] 6.3 Content-hash the eval set (`eval_set_hash`) and version it; make every leaderboard
      attributable to an exact `{eval_set_hash, config_hash}`.
- [ ] 6.4 **Meter and cap judge/generation spend** per eval run; surface spend so measuring a
      workflow doesn't silently blow a budget.
- [ ] 6.5 Ensure adversarial/injection cases execute **only in the P3 sandbox** with no ambient
      credentials; store judge prompts/references as content-hashed blobs, never inline in logs.

## 7. Frontend + Product — Leaderboard, Pareto, coverage report
- [ ] 7.1 Product: design the **eval-run journey** (choose variants → generate/select eval set → run
      → compare) and the **"is this eval set good enough?"** coverage screen as a first-class moment.
      Design the unhappy path first (coverage loop can't reach threshold; judge fails calibration;
      all-tie board).
- [ ] 7.2 Frontend: **leaderboard** — rank gate-passers under the active profile; each row shows
      **score ± CI, component-metric breakdown, gate pass/fail, `config_hash` lineage**; disqualified
      variants in a separate section naming the failed gate.
- [ ] 7.3 Frontend: **Pareto view** — quality/cost/latency frontier; re-render instantly on weight
      change (reads the score cache, never re-runs).
- [ ] 7.4 Frontend: **coverage-report screen** — achieved vs. target path/node/edge, residual
      uncovered paths, difficulty/diversity read.
- [ ] 7.5 First-class states: loading / error / empty / **partial** (fan-out in progress) / **tie
      (CIs overlap)** / **disqualified** / **weak-labeled** — each visually distinct; read terminal
      aggregate status from persisted results (no derived state that drifts).
- [ ] 7.6 **Accessibility & performance:** virtualize large variant lists; keyboard-operable rows;
      score/frontier color via the **dataviz** skill for contrast + light/dark consistency.

## 8. Testing & review
- [ ] 8.1 Fixtures: a multi-pattern workflow (Routing → per-branch Tool Use → Reflection); a
      hand-authored eval set + a generated one; a true-zero-delta variant pair + a known-real-delta
      pair; a human-labeled calibration subset for one judge metric.
- [ ] 8.2 Statistical-honesty tests: true-zero-delta → tie; known-real-delta → correct winner,
      non-overlapping CIs; composite CI overlap → tied on the board.
- [ ] 8.3 Gate/weight tests: below-min-quality cheapest variant disqualified (not #1); profile switch
      re-ranks with zero new runs, < 200 ms.
- [ ] 8.4 Judge-calibration test: below-floor judge flagged + barred from gating; calibrated judge
      reports agreement per score.
- [ ] 8.5 Coverage test: gap-filling loop iterates until path coverage ≥ threshold; unreachable path
      → residual reported, no false 100%. Weak-labeled reference cannot silently gate.
- [ ] 8.6 Pattern-scoping test: router scored with misroute metrics, not relevance@k; RAG node scored
      with relevance@k, not misroute.
- [ ] 8.7 UI verification: drive leaderboard + Pareto + coverage screens against a live
      (stubbed-provider) fan-out; confirm all states render, instant re-rank on profile switch,
      virtualization on a many-variant board.
- [ ] 8.8 Confirm the M5 exit checklist (PRD §13) is green.
