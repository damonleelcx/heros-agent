# Phases 4 → 6 — Measuring, Diagnosing & Optimizing

The intelligence half turns the platform from "it runs variants" into "it tells you which variant
is better, *why* the loser fails, *what* to change, *proves* the change works, and — under hard
constraints — applies it autonomously." The AI Engineer playbook governs this half; its one law
holds throughout: **evals before optimization, and verification decides.**

Each phase is decomposed by role, applying playbook discipline. See
[`roles-and-ownership.md`](roles-and-ownership.md) for the mapping.

---

## Phase 4 — Eval Harness + eval-set generation + composite scoring · ~Weeks 16–22 · **Milestone M5**

> The precondition for everything after it. Amateur loop: change → eyeball → ship. Senior loop:
> build eval set → measure baseline → change one thing → re-run → keep only if the number rose.

**Goal.** Run any Variant Spec over an eval set, multi-seed, and produce comparable, CI-bounded
composite scores on a leaderboard.

### AI Engineer (lead)

**Eval harness.**
- Run each Variant Spec over an eval dataset (user-provided inputs + optional reference outputs).
- Metrics: task success (rubric / LLM-as-judge / exact-match / regex per task), cost, latency,
  token usage, tool-error rate, per-node contribution.
- Evaluators are pluggable functions over traces: built-in (exact-match, JSON-schema, regex,
  LLM-judge) + **user-defined custom metrics** (registered like skills). Map metric-sets to node
  types via the P3.5 pattern label — don't compute everything everywhere.

**Eval-set generation** (the system synthesizes cases; real workflows rarely come with adequate
test data). Coverage is *measured*, not assumed:
- **Coverage targets:** path (every IR edge, every branch/router outcome, loops at min/typical/
  max iterations), node (each node across its input schema), edge cases (empty/malformed input,
  tool-returns-nothing, retrieval-miss, context-overflow, adversarial/injection, boundaries).
- **Layered generation:** seed from real traces (once P5 tracing exists) → schema-driven
  synthesis from typed I/O contracts (property/fuzz style) → LLM-driven synthesis targeting
  *uncovered* paths and a fixed failure taxonomy → adversarial perturbation of existing cases.
- **Reference outputs:** auto-label where an oracle exists (exact-match/schema/deterministic
  tool); else LLM-generated reference + human review of a subset, or reference-free metrics.
  Flag gold vs. weak-labeled; never let unverified synthetic references silently drive scoring.
- **Coverage report + gap-filling loop:** measure achieved path/node/edge coverage; the generator
  iterates until thresholds met. Track eval-set difficulty/diversity as a metric itself — a
  passing score on a weak eval set is worthless.

**Statistical rigor (enforced, not optional).** Multi-seed runs, mean + confidence intervals,
significance tests on variant deltas. When two variants' CIs overlap, declare a **tie**, not a
false winner — reordering deltas are often within noise.

**Composite scoring.**
- Normalize each metric to [0,1] before weighting (raw $ and % aren't comparable).
- `Score = w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability − penalties`.
- Weights are a **named profile** (quality-first / cost-optimized / balanced) so the same runs
  re-rank under different priorities without re-executing (scores cached per variant; only the
  weighted sum recomputes).
- **Hard constraints are gates, not penalties** — max cost/run, latency SLA, min quality,
  provider allowlist. A violating variant is **disqualified**, keeping a cheap-but-broken
  arrangement off the top of a cost-weighted board. Soft preferences (the weighted terms) apply
  only to variants that pass gates.

### Frontend (co-lead)
- **Leaderboard:** rank variants by composite score under the active profile; each row shows
  score ± CI, component-metric breakdown, gate pass/fail, full config lineage (`config_hash`).
- **Pareto view** for the multi-objective case — surface the quality/cost/latency frontier so
  users see tradeoffs directly rather than trusting one collapsed number. Re-rank instantly on
  weight change. (Use the dataviz skill for accessible, consistent chart color.)

### Product (support)
- Design the eval-run journey and the "is this eval set good enough?" moment (coverage report as
  a first-class screen). Content is the interface: a variant marked "tie (CIs overlap)" must say
  so plainly.

### System Designer / DevOps (support)
- Queue-driven run fan-out for seed sweeps; ensure the tag set supports every leaderboard slice.

**Deliverables:** eval harness, evaluator plugins, eval-set generator + coverage report,
normalization + composite scoring + weight profiles, leaderboard + Pareto view.
**Exit criteria (M5):** two variants run over a generated + user eval set, multi-seed, and appear
on a leaderboard with CI-bounded composite scores and gate status.

---

## Phase 4.5 — Attribution + rule-based diagnosis + scorecard (read-only) · ~Weeks 21–25 · **Milestone M6**

> From "here are the numbers" to "here's *which node* is failing and *why*." Read-only: it reports,
> it does not change anything yet.

**Goal.** Localize failures to a node+dimension and attach a named, typed cause.

### AI Engineer (lead)

**Attribution — localize.**
- **Per-node contribution:** decompose end-to-end failure/cost/latency to individual nodes using
  the OTel traces. For a 60%-scoring workflow, which node's output first diverges on failing cases?
- **Failure clustering:** embed failing inputs + traces and cluster, so you fix *categories*
  ("fails on multi-hop" vs. "fails when a tool returns empty"), not one-offs.
- **Ablation / counterfactuals:** hold every node fixed, swap one node's config, re-run — the only
  rigorous way to say "node 3's prompt is the bottleneck." Reuses the variant machinery.
- **Bottleneck flags:** cost/latency Pareto — which node dominates spend / sits on the critical path.

**Diagnosis — explain why.** Two complementary methods:
- **Rule-based detectors** (fast, deterministic, cheap) over traces: context overflow/truncation
  before a failing node; tool schema mismatch / repeated tool errors; retrieval miss (low-relevance
  chunks); prompt-format drift (output contract ignored → downstream parse fail); lost-in-the-
  middle from over-long context; model-capability mismatch (cheap model on a reasoning-heavy node).
- **LLM-as-analyst** for the fuzzy residue: feed a failing case's full trace to an analyst model
  with a structured rubric, constrained to a **fixed failure taxonomy** so outputs aggregate.
  *Caveat enforced:* calibrate the analyst against a human-labeled subset, report agreement, and
  never let one unverified opinion drive a change. Diagnosis proposes; verification (P5.5) decides.

**Pattern-scoped failure modes** (from P3.5/P5 classifier): only check failure modes the pattern
can exhibit — Routing → misroutes; Planning → infeasible/circular plans; Reflection → non-
convergence / degradation-on-revision.

### Frontend / Product (support)
- **Per-run scorecard:** overall metrics, per-node breakdown, top failure clusters. Diagnosis
  cards show *why* with the specific failing cases attached as evidence, not just a label.

**Deliverables:** attribution engine (per-node contribution, clustering, ablation), rule-based
diagnostic detectors, LLM-analyst with fixed taxonomy + calibration, read-only scorecard.
**Exit criteria (M6):** for a failing variant, the system names the responsible node(s), the
failure cluster(s), and a typed cause — all read-only.

---

## Phase 5 — Typed contracts + Re-arrangement + Dynamic tracing + behavioral classification · ~Weeks 24–30 · **Milestone M7**

> The biggest gap in the naïve design. Four leads (System Designer, Backend, Frontend, Product)
> because safe re-arrangement is simultaneously a schema problem, a runtime problem, a UI problem,
> and a UX problem.

**Goal.** Let users re-arrange the graph *safely*, and validate/repair the static graph against
real runs.

### System Designer + Backend (co-lead) — safe re-arrangement
- **Typed per-node I/O contract** (designed in P0, enforced here): input schema + output schema
  per node. The system validates whether a proposed ordering is coherent and **flags / auto-
  inserts adapters** where schemas don't match. Without this, "drag to reorder" silently produces
  broken workflows — this is the piece most likely to be underestimated.
- Node I/O passes through the typed contract so re-ordered nodes still receive valid inputs.

### Backend + AI Engineer — dynamic tracing (validation)
- Static analysis produced a *candidate* graph; confirm it by instrumenting a run: wrap SDK
  entrypoints with an OTel-style interceptor logging every real LLM call, its inputs, and its
  stack; reconcile against the static candidates. This resolves runtime-dynamic dispatch (loops,
  conditional routing) static analysis can't see, and distinguishes static nodes from runtime
  invocations concretely.

### AI Engineer — behavioral pattern classification
- Now that real traces exist, confirm what topology couldn't: iteration count >1 on a self-edge →
  Reflection; a planning node emitting a task list consumed downstream → Planning; sample-N-then-
  vote → Self-Consistency; memory R/W between turns → Memory Management; human-approval pause →
  HITL. Wire pattern → metric-set / failure-taxonomy / eval-targeting. **Anti-pattern detection**
  falls out: a reflection loop that never improves, a router sending everything one way,
  parallelization with no real independence — these become diagnoses for P5.5.

### Frontend + Product (co-lead) — the graph editor
- The UI exposes the graph; users add/remove/reorder/swap nodes and produce a new Variant Spec.
- **Design the unhappy path first** (Product): an invalid reordering must be legible — surface the
  contract mismatch, show the auto-inserted adapter, explain what would break. Keyboard-operable,
  accessible, and responsive on large IRs (Frontend).

### AI Engineer — eval-set generation enrichment
- Seed generation from the new dynamic traces; add per-path targeting now that typed contracts and
  real runs exist (feeds back into P4's generator).

**Deliverables:** typed-contract validator + adapter insertion, dynamic-tracing interceptor +
reconciler, behavioral classifier, interactive graph editor with invalid-state UX.
**Exit criteria (M7):** a user re-orders a graph; incoherent orderings are flagged/adapted rather
than silently broken; dynamic tracing reconciles against the static IR.

---

## Phase 5.5 — Proposal operators + Verification gate (advisory/assisted) · ~Weeks 29–34 · **Milestone M8**

> What separates the engine from a plausible-sounding suggestion box: every proposal is re-run and
> measured before it reaches the user.

**Goal.** Turn diagnoses into concrete Variant-Spec changes and **prove** each one before surfacing.

### AI Engineer (lead)

**Proposals — change operators.** Each diagnosis maps to an operator emitting candidate Variant
Specs:

| Diagnosis | Operator |
|---|---|
| Reasoning-heavy node on weak model | Upgrade model / enable extended thinking on that node |
| Cheap task on expensive model | Downgrade — cost win at equal quality |
| Prompt/output-contract violations | Rewrite prompt (LLM-driven, grounded in failing cases), add format constraints/schema |
| Context overflow / lost-in-middle | Switch context policy → summarization or sliding window; reorder |
| RAG relevance low | Tune top-k, swap retriever/embedding, add rerank |
| Missing/erroring tool | Add skill from registry, fix schema binding |
| Redundant node | Prune / merge nodes |

- Prompt improvement uses a DSPy-style / self-refine optimizer grounded in the failing cases, not
  generic "make it better."
- **Rank by expected gain / cost of change**, respecting user constraints (budget ceiling, latency
  SLA, provider allowlist). Present each as a **diff** against the current Variant Spec, with the
  diagnosis and the specific failing cases attached as evidence.

**Verification — prove it before recommending.**
- Auto-execute each proposal against the **same eval dataset, held-out where possible** to avoid
  overfitting the recommendation to the cases that generated it.
- **Statistical gate:** multi-seed runs, CIs, significance vs. baseline — only surface changes
  whose improvement is real, not noise.
- **Regression check:** confirm the fix didn't degrade other case clusters or blow the cost/latency
  budget (catch "fixed accuracy, tripled cost").
- **Verdict:** proposed change, measured delta (with CI), cost/latency impact, cases fixed / broken.

### Frontend / Product (support)
- **Ranked recommendation list:** each = diagnosis + evidence + proposed diff + *verified* delta.
- **Trend view** across variants over time — did the workflow actually improve, or did problems
  just move around? Human-readable synthesis is narration over the structured results, never the
  source of truth.
- **Automation levels UX (Advisory / Assisted):** Advisory = report, human applies; Assisted =
  one-click apply a verified proposal. Product designs how authority is granted and shown.

**Deliverables:** change-operator catalog, proposal ranker, verification gate (held-out + stats +
regression), ranked recommendation UI, trend view.
**Exit criteria (M8):** the engine emits a proposed diff with a *verified* delta (CI + cost/latency
impact + cases fixed/broken); nothing unverified reaches the user.

---

## Phase 6 — Autonomous optimizer · ~Weeks 33–40 · **Milestone M9**

> The closed loop: analyze → propose → verify → apply, driven by *diagnosis* rather than blind
> search — far more sample-efficient than grid/Bayesian alone. Two leads: AI Engineer (objective)
> and DevOps (guardrails), with Product owning the trust/authority UX.

**Goal.** Run the full loop under hard constraints, with audit trail and rollback.

### AI Engineer (lead) — the optimizer
- The **composite score is the objective** the search maximizes; the **gates are its hard
  constraints**. Layer automated search (grid/Bayesian over model×prompt×context per node, and
  DSPy-style prompt optimization) on top of the eval harness, but *guided by diagnosis* rather
  than blind — the improvement engine points the search at the node+dimension attribution found.
- Feedback loop: production failures become new eval cases and re-enter at P4. The eval set is the
  living memory of the system.

### DevOps (co-lead) — operational guardrails
- **Hard constraints as gates:** budget ceiling, provider allowlist, min-improvement threshold,
  max iterations. **Kill switch**, full **audit trail**, and **rollback** are prerequisites before
  the loop is allowed to apply anything (blast radius + reversibility directives).
- **Regression detection & budget alerts** halt the loop if any metric degrades beyond threshold
  or a budget is breached.

### Product (co-lead) — automation-level governance UX
- **Autonomous** level: the engine runs the full loop under the hard constraints, with the audit
  trail and rollback visible. Design how a user grants this authority, monitors it live, sets the
  constraints, and stops it. Each automation level is a distinct trust contract.

### System Designer (support)
- The failure story: how the loop degrades safely, queue semantics for the run fan-out it
  generates, and no single point of failure on the apply path.

**Deliverables:** diagnosis-guided search, constraint/gate engine, audit trail + rollback, kill
switch, autonomous-level UX.
**Exit criteria (M9):** the system autonomously analyzes → proposes → verifies → applies under
hard constraints, with every applied change auditable and reversible.

---

## The through-line

The eval harness (P4) is what makes P4.5, P5.5, and P6 possible — the improvement engine is
largely a *consumer* of it plus a set of change operators. The metrics substrate (P2.5) is the
shared substrate under all of them. And the two P0 contracts — event tagging and typed I/O — are
what stop the whole intelligence half from confidently ranking noise or silently shipping broken
graphs. Design them once, early, right.
