# Design — P4.5: Attribution + rule-based Diagnosis + scorecard (read-only)

Cross-reference: product rationale in [`../../../docs/prd/P4.5-attribution-diagnosis.md`](../../../docs/prd/P4.5-attribution-diagnosis.md).

## Context

P4.5 turns the eval harness's numbers into *localized, named* failures — "**which node**, and
**why**" — without ever proposing or applying a change. It is a **consumer** of P4 (eval results +
per-node contribution + the multi-seed / CI / tie primitive), P2.5 (traces), and P3.5 (pattern
labels), plus a small amount of new machinery (clustering, ablation orchestration, rule detectors, a
constrained LLM-analyst). Three forces shape every decision: LLM-as-analyst is itself **noisy and
biased** (so it is constrained to a fixed taxonomy, calibrated, and its agreement reported), causal
claims are cheap to assert and expensive to prove (so causation is isolated by **ablation**, not
argued), and the whole phase must be **provably read-only** (so the boundary is structural, not a
convention). The governing law is the same one that runs through the intelligence half:
**diagnosis proposes; verification decides** — and in P4.5 there is no "decides" at all, only
"proposes to a human," read-only.

## Decision 1 — Read-only is structural, not a mode

**Decision.** The engine has **no write path** to Variant Specs, registries, or node configs. Its
report tables (`attribution`, `failure_cluster`, `ablation_result`, `bottleneck_flag`, `diagnosis`,
`analyst_cal`) are **append-only** and hold no FK that writes into a config store; the engine's DB
grant is *read* on traces + eval results and *write* only to the report tables. It emits **no
proposal object**. Ablation's re-runs are ephemeral measurement variants — enqueued, measured,
discarded — never persisted as user variants and never applied.

**Why.** "Read-only" enforced by developer discipline is one refactor away from a silent mutation.
Making it a property of the schema + the grant means the guarantee is testable and hard to violate by
accident: a full attribution + diagnosis run must leave every Variant Spec / registry / config
**byte-identical** (same `config_hash`) and produce **zero** proposal records. This is the phase's
load-bearing safety property and the reason P4.5 is safe to run against a user's real workflow before
any apply path exists (P5.5/P6). It also makes the P4.5→P5.5 boundary crisp: turning a diagnosis into
a change is a *different* capability behind a verification gate.

**Alternative rejected.** A shared "improvement engine" service that both diagnoses and proposes,
gated by a feature flag — one flag flip from shipping unverified changes; the boundary belongs in the
data model, not a config toggle.

## Decision 2 — Ablation is the only rigorous causal isolation; reuse the P4 harness

**Decision.** To attribute causal contribution to a single node, `Ablate(variant, node, config')`
holds **every other node's config fixed**, swaps exactly **one** node's config, and re-runs through
the **P4 harness** multi-seed, reporting the delta via the P4 `Stats.Compare` primitive
(mean ± CI, significance). A delta whose **CI overlaps zero** is verdict `inconclusive`; only a
non-overlapping delta names the node a `bottleneck`.

**Why.** Correlational attribution (per-node contribution, first-divergence) *localizes* but cannot
*prove* — the node that first diverges may be a symptom, not the cause. The only way to say "node 3's
prompt is the bottleneck" is the counterfactual: change only node 3 and measure. Reusing the P4
harness means the same statistical honesty (multi-seed, CI, tie-on-overlap) carries straight into
causal attribution — a single-run ablation delta is never a causal claim, exactly as a single-run
comparison is never a winner in P4. Reusing the variant machinery is also why this is cheap: no new
runner, just a config swap + the existing fan-out.

**Trade-off.** Ablating every node × every candidate config is combinatorial (Q3). The default seeds
the ablation set from the per-node contribution ranking — ablate the top-contribution nodes first —
and bounds N seeds per ablation delta on the P4 queue with a spend cap.

## Decision 3 — Rules-first, LLM-analyst for the fuzzy residue only

**Decision.** Deterministic `Detect(trace) → []TypedCause` rule detectors run **first** on every
failing case (context overflow/truncation, tool schema mismatch / repeated tool errors, retrieval
miss, prompt-format drift, lost-in-the-middle, model-capability mismatch), each emitting a **typed
cause** from the fixed failure taxonomy. The **LLM-as-analyst** is invoked **only on the residue** —
the failing cases no rule explained.

**Why.** Rules are fast, free, and reproducible; the analyst is slow, metered, and variable. Running
rules first means most diagnoses cost nothing and are deterministic (the same trace → the same cause
every time), and the analyst spend is bounded to the genuinely fuzzy cases. It is the same discipline
the Pattern Classifier uses (rules over topology first, LLM for ambiguous graphs) — rules-first, LLM
for the residue, never unverified. When a rule and the analyst both fire on a case with different
codes, the **deterministic rule wins** and the analyst's disagreement is logged for calibration (Q6).

**Alternative rejected.** Send every failing trace to the analyst — pays LLM cost for cases a cheap
deterministic rule already explains, and replaces a reproducible cause with a variable one.

## Decision 4 — The analyst is constrained to a fixed taxonomy, calibrated, and its agreement reported

**Decision.** The analyst receives a failing case's full trace + a structured rubric and emits a
diagnosis **constrained to the fixed failure taxonomy + a confidence score**. An off-taxonomy /
free-text label is **rejected, not recorded**. The analyst is **calibrated against a human-labeled
subset**; its **agreement** (κ / % agreement, with `n_human`) is reported **alongside every
diagnosis**; an uncalibrated or below-floor analyst is **flagged**. No single unverified analyst
diagnosis drives a change (there is no apply path here; the constraint binds P5.5).

**Why.** LLM-as-analyst is noisy and biased — this is stated plainly in the source plan. Three
guardrails make its output usable rather than decorative: (1) the **fixed taxonomy** makes outputs
*aggregatable* — a hundred cases resolve into a handful of named causes instead of a hundred free-text
essays, and each cause maps deterministically to a P5.5 operator; (2) **calibration + agreement
reporting** tells a human whether to trust it, exactly as P4's judge calibration does; (3) the
**confidence** score plus the "rule wins on conflict" rule keep a low-confidence opinion from
masquerading as a fact. This is the same law as P4 judge calibration and P5.5 verification, applied at
the diagnosis source.

**Trade-off.** Requires a human-labeled subset (curation cost) and a maintained taxonomy; acceptable
because the subset is small and is the only thing that makes the analyst's automated diagnoses
trustworthy, and the taxonomy is shared with the P5.5 operator map (one versioned artifact, Q5).

## Decision 5 — Pattern-scoped failure modes: the P3.5 label is the dispatcher

**Decision.** Both rules and the analyst check only the failure modes a node's **P3.5 structural
pattern** admits — Routing → misroutes; Planning → infeasible/circular plans; Reflection →
non-convergence / degradation-on-revision; RAG → retrieval miss; Tool Use → wrong-tool/schema — and
refuse to diagnose a node with a failure mode its pattern cannot exhibit.

**Why.** Diagnosing a router for a RAG failure mode produces noise. The pattern label (already
computed in P3.5) is the dispatcher that keeps diagnosis in-scope per node, the same way it scopes
metric-set selection in P4. It also bounds the analyst's rubric to the relevant taxonomy subset,
cutting both cost and false positives.

## Decision 6 — First-divergence and clustering localize; the scorecard is the read-only surface

**Decision.** Attribution has two correlational localizers feeding a read-only scorecard: (1)
**per-node contribution + first-divergence** — per failing case, the node whose output first diverges
from success (contract-violation first, then reference-mismatch where a gold reference exists, Q1);
(2) **failure clustering** — embed failing inputs + traces, cluster into **named categories** with
sizes + representative cases, so failures are addressed as categories not one-offs (Q2). The
**per-run scorecard** (overall metrics + per-node breakdown + top clusters + diagnosis cards) is the
sole output surface, and every diagnosis card carries the **specific failing cases as evidence**.

**Why.** Aggregate scores hide locality; the two localizers answer "which node" and "which category"
cheaply from data already on hand, and the ablation (Decision 2) upgrades a localization to a proven
cause where it matters. Attaching failing cases as evidence is the Product/Frontend discipline —
diagnosis cards show *why* with the cases, not a bare label — which is also what makes a human able to
judge whether to act (there being no automated apply).

**Alternative rejected.** A single collapsed "health score" per node — hides whether the node fails on
one category or many, and gives a human nothing to act on.

## Data model sketch

```
attribution(variant_id, eval_set_hash, config_hash, node_id, case_id,
            contrib_success, contrib_cost, contrib_latency, first_divergence BOOL,
            PRIMARY KEY(variant_id, eval_set_hash, node_id, case_id))          -- append-only
failure_cluster(cluster_id PK, variant_id, eval_set_hash, label, size,
                representative_case_id, member_case_ids_json)                  -- append-only
ablation_result(ablation_id PK, variant_id, eval_set_hash, node_id,
                swapped_config_ref, delta_mean, ci_low, ci_high, n_seeds,
                verdict ENUM('bottleneck','inconclusive'))                     -- ephemeral runs
bottleneck_flag(variant_id, eval_set_hash, node_id, dimension ENUM('cost','latency'),
                dominance, PRIMARY KEY(variant_id, eval_set_hash, node_id, dimension))
diagnosis(diag_id PK, variant_id, eval_set_hash, node_id,
          taxonomy_code, source ENUM('rule','analyst'), confidence,
          evidence_case_ids_json)                                             -- append-only
analyst_cal(analyst_metric PK, agreement, n_human, calibrated BOOL, floor)
-- NO table here holds an FK-write into variant_spec / registry / node_config.
-- Engine DB grant: READ traces + eval_result; WRITE only the tables above.
```
Trace excerpts, analyst prompts/rubrics, and cluster embeddings live in the object store keyed by
content hash; DB rows hold only the hash and the tags.

## Key interfaces

```
Attribute(variant, eval_set) -> PerNodeContribution        // per-node + first-divergence
Cluster(failing_cases)       -> []FailureCluster           // embed inputs+traces; named categories
Ablate(variant, node, config') -> AblationResult{delta±ci, verdict}  // P4 harness re-run; ephemeral
Bottleneck(variant)          -> []BottleneckFlag           // cost/latency Pareto across nodes
Detect(trace)                -> []TypedCause                // deterministic; fixed taxonomy; rules-first
Analyze(trace, rubric)       -> Diagnosis{taxonomy_code, confidence, agreement}  // residue only; constrained
Scorecard(variant, eval_set) -> Report                     // overall + per-node + clusters + cards
// No interface returns or accepts a mutation to a Variant Spec, registry, or config.
```

## Risks

- **Engine mutates a workflow / emits a proposal despite "read-only"** — mitigated by removing the
  write path from the schema + the DB grant (Decision 1); tested by asserting byte-identical
  specs/config (same `config_hash`) and zero proposal records after a full run.
- **Unverified analyst opinion drives a decision** — mitigated by fixed-taxonomy + confidence +
  human-subset calibration + agreement-per-diagnosis + the no-apply-path guarantee (Decision 4).
- **Analyst free-texts off-taxonomy → outputs don't aggregate** — mitigated by rejecting
  off-taxonomy/free-text output rather than recording it (Decision 4).
- **Ablation delta read as causal from a single run** — mitigated by reusing the P4 multi-seed + CI
  primitive; CI-overlaps-zero → inconclusive (Decision 2).
- **Node diagnosed with a failure mode its pattern can't exhibit** — mitigated by pattern-scoping off
  the P3.5 label (Decision 5).
- **Analyst spend blows a budget** — mitigated by rules-first (most cases cost nothing) + an analyst /
  ablation spend cap per run on the bounded P4 queue (Decision 3, DevOps).
- **Diagnosis is a bare label** — mitigated by attaching the specific failing cases as evidence to
  every card (Decision 6).
- **Ablation re-runs execute discovered code with credentials** — mitigated by running only in the P3
  sandbox with no ambient credentials.

## Open questions

- **Q1. First-divergence definition** — contract-violation first, then reference-mismatch where a gold
  reference exists; how is divergence defined on a node with no reference?
- **Q2. Clustering method** — input-only vs. trace-only vs. joint embedding; HDBSCAN vs. k-selection;
  cluster naming LLM-labeled (itself calibrated) vs. rule-derived from the dominant trace signature.
- **Q3. Ablation candidate selection** — seed the ablation set from the per-node contribution ranking;
  default N seeds for an ablation delta's CI.
- **Q4. Analyst agreement floor** — default κ / %-agreement floor; per-taxonomy-code or global; shared
  with the P4 judge floor or independent.
- **Q5. Taxonomy source of truth** — one flat list vs. partitioned by pattern; kept in sync with the
  P5.5 change-operator map (proposed: one versioned taxonomy tagged with admissible patterns per code).
- **Q6. Rule/analyst overlap** — deterministic rule wins on conflict; analyst disagreement logged for
  calibration.
- **Q7. Scorecard-to-P5.5 handoff** — freeze the diagnosis record schema (taxonomy code + node +
  evidence + confidence + agreement) as the exact contract P5.5's operators consume; where it is
  versioned.
