# **LLM Agentic Workflow Evaluation & Configuration System --- Implementation Plan**

## **1. System Overview**

A platform that ingests a codebase, discovers the LLM call graph,
exposes each call site (\"node\") as a configurable unit, and lets users
remix models/prompts/skills/context strategies, re-order nodes, then
execute and score variants.

Four subsystems:

  ---------------------------------------------------------------------
  **Subsystem**     **Responsibility**
  ----------------- ---------------------------------------------------
  **Discovery       Static + dynamic analysis to extract LLM nodes and
  Engine**          their DAG

  **Configuration   Per-node overrides (model, prompt, skill,
  Layer**           context) + registries

  **Runtime**       Dynamic loader that executes a workflow spec
                    against live providers

  **Evaluation      Runs variants, collects traces/metrics, compares
  Harness**         
  ---------------------------------------------------------------------

## **2. Discovery Engine**

The hardest and most under-specified part. Do not rely on a single
technique.

**Static analysis (primary)**

- Parse the repo with tree-sitter (language-agnostic) or
  language-specific ASTs (Go Lang ast, Go go/ast).

- Detect LLM call sites by matching against a **signature registry**:
  known SDK entrypoints (anthropic.messages.create,
  openai.chat.completions.create, LangChain/LangGraph invoke, Bedrock
  converse, custom internal wrappers).

- Support user-declared entrypoints for in-house wrappers via a config
  file (llm-eval.yaml) --- most real codebases wrap the SDK, so
  signature matching alone will miss nodes.

- For each call site extract: model arg, messages/prompt construction,
  tools/skills passed, and upstream data flow (what feeds the prompt).

**Graph reconstruction**

- Build a call graph; a \"node\" = an LLM-invoking function/agent step.
  Edges = data/control flow between them (output of node A parsed into
  input of node B).

- Frameworks like LangGraph/CrewAI already encode the DAG declaratively
  --- special-case these by reading their graph definition rather than
  inferring it.

**Dynamic tracing (validation)**

- Static analysis produces a *candidate* graph. Confirm it by
  instrumenting a run: monkey-patch/wrap SDK entrypoints with an
  OpenTelemetry-style interceptor that logs every real LLM call, its
  inputs, and its stack. Reconcile against static candidates.

- This resolves runtime-dynamic dispatch (loops, conditional routing)
  that static analysis can\'t see.

**Output:** a canonical **Workflow IR** --- a JSON graph of nodes with
metadata (call site, current model, prompt template, tools, context
assembly logic).

> **Correction to the premise:** \"how many nodes make LLM requests\" is
> only well-defined for static call sites. Agents with loops make a
> *variable* number of requests at runtime. The IR should distinguish
> **static nodes** (definition) from **runtime invocations** (execution
> instances), and node count should be reported per-definition.

## **3. Configuration Layer**

Each node in the IR becomes an override target. Introduce an
**adapter/shim layer**: the system can\'t edit arbitrary source safely,
so discovered call sites are wrapped so their parameters resolve from a
config store at runtime rather than from hardcoded values.

**Per-node configurable dimensions**

- **Model**: provider + model ID + inference params (temp, max_tokens,
  thinking budget). Backed by a **Model Registry**.

- **Prompt**: versioned prompt templates with variable slots. Backed by
  a **Prompt Registry** (git-like versioning, so variants are diffable
  and reproducible).

- **Skills/Tools**: a **Skill Registry** mapping skill name → schema +
  implementation. Node config selects a subset. Registry entries carry a
  JSON-schema contract so runtime can validate tool availability before
  execution.

- **Context strategy**: pluggable policies --- full history / sliding
  window / summarization / RAG retrieval / semantic compaction. Each is
  a named strategy with its own params (window size, top-k, summarizer
  model).

**Registries** are shared, versioned, and referenced by ID from node
configs. A full workflow config is a **Variant Spec**: {node_id →
{model_ref, prompt_ref, skill_refs\[\], context_policy}} plus a node
ordering/graph.

**Storage:** Variant Specs and registries in Postgres; large
prompt/artifact blobs in object storage keyed by content hash for
reproducibility.

## **4. Runtime**

A dynamic executor that takes a Variant Spec + input and runs the
workflow --- without regenerating source.

- **Loader**: resolves every \*\_ref in the spec against registries at
  invocation time. Models loaded via a unified provider gateway
  (LiteLLM-style abstraction so provider swaps are transparent); prompts
  rendered from templates; skills bound from registry; context policy
  instantiated.

- **Executor**: walks the node graph, executing each node through the
  shim. Node I/O passes through a typed **contract** so re-ordered nodes
  still receive valid inputs (see §5 caveat).

- **Isolation**: run each node in a sandbox (subprocess/container) since
  skills may execute arbitrary tool code from the target repo. Never run
  discovered code with ambient credentials.

- **Full tracing**: emit an OpenTelemetry trace per run --- every
  request, response, token count, latency, cost, tool call.

## **5. Node Re-arrangement & Evaluation**

**Re-arrangement**

- The UI exposes the graph; users add/remove/reorder/swap nodes and
  produce a new Variant Spec.

- **Critical caveat:** arbitrary re-ordering is not free. Node B often
  depends on B\'s parsing of A\'s output. Enforce a **typed I/O contract
  per node** (input schema, output schema) so the system can validate
  whether a proposed ordering is coherent, and flag/auto-insert adapters
  where schemas don\'t match. Without this, \"drag to reorder\" silently
  produces broken workflows. This is the piece most likely to be
  underestimated.

**Evaluation harness**

- Run each Variant Spec over an **eval dataset** (user-provided inputs +
  optional reference outputs).

- Metrics: task success (via graded rubric / LLM-as-judge / exact-match
  / regex depending on task), cost, latency, token usage, tool-error
  rate, per-node contribution.

- **Comparison**: side-by-side variant diff, with statistical treatment
  (multiple seeds/runs, confidence intervals) since LLM outputs are
  stochastic --- single-run comparisons are misleading.

- **Optimization loop**: given the eval harness, layer automated search
  on top --- grid/Bayesian search over model×prompt×context combinations
  per node, or DSPy-style prompt optimization. Manual re-arrangement is
  the baseline; automated search is the payoff.

## **6. Architecture**

Repo → Discovery Engine ──→ Workflow IR ──→ Config Layer ──→ Variant
Spec

↑ registries (model/prompt/skill/context)

Variant Spec + eval data → Runtime (loader+executor+sandbox) → Traces

↓

Evaluation Harness → Metrics → UI compare/optimize

**Stack suggestion:** tree-sitter + language ASTs (discovery), Go Gin
backend, Postgres + object store, LiteLLM/provider gateway,
OpenTelemetry (tracing), a queue (execution), React + graph library
(UI).

## **7. Phased Delivery**

1.  **Discovery MVP** --- single language (Go Lang), signature +
    user-declared entrypoints, static IR only. Prove node extraction.

2.  **Config + Runtime** --- registries, shim layer, execute a hardcoded
    graph with per-node model/prompt overrides.

3.  **Context strategies + Skill registry** --- pluggable policies,
    sandboxed tool execution.

4.  **Eval harness** --- datasets, metrics, multi-run statistics,
    side-by-side compare.

5.  **Re-arrangement with typed contracts** --- validated reordering,
    adapter insertion.

6.  **Dynamic tracing + automated optimization** --- runtime graph
    validation, search-based tuning.

## **Key additions/corrections to your spec**

- **Static vs. runtime node count** must be distinguished (loops/agents
  make variable calls).

- **User-declared entrypoints** are mandatory --- real codebases wrap
  SDKs, defeating pure signature matching.

- **Dynamic tracing** is needed to validate the static graph, not
  optional.

- **Typed per-node I/O contracts** are the precondition for safe
  re-arrangement --- the biggest gap in the original design.

- **Sandboxing** --- you\'re executing discovered code and repo tools;
  treat it as untrusted.

- **Statistical evaluation** --- stochastic outputs require multi-run
  comparison, not single passes.

- **Automated optimization** is the natural payoff of the eval harness
  and should be designed in, not bolted on.

Want me to turn any section (e.g., the Discovery Engine or the Workflow
IR schema) into a concrete spec or code?\
\
**Addition: Analysis & Improvement Engine**

Extends the eval harness from \"here are the numbers\" to \"here\'s
what\'s wrong, why, and what to change.\" This is a distinct subsystem
that consumes traces + metrics and emits diagnoses + actionable variant
proposals.

## **1. Where it sits**

Evaluation Harness → Traces + Metrics ──→ Analysis & Improvement Engine

├─ Attribution (what/where is failing)

├─ Diagnosis (why)

├─ Proposals (what to change → new Variant Specs)

└─ Verification (auto-run proposals, prove the gain)

↓

feeds back into the optimization loop (§5 of main plan)

The key design principle: **the engine must close the loop** --- every
proposed improvement is itself a Variant Spec that the runtime can
execute, so recommendations are *verified*, not just asserted.

## **2. Attribution --- localize the problem**

Before suggesting fixes, pinpoint *which node* and *which dimension* is
responsible. Aggregate metrics hide this.

- **Per-node contribution**: using the OpenTelemetry traces, decompose
  end-to-end failure/cost/latency to individual nodes. A workflow that
  scores 60% --- which node\'s output first diverges from success on
  failing cases?

- **Failure clustering**: group failing eval cases by signature (embed
  failing inputs + traces, cluster) so you address *categories* of
  failure, not one-offs. \"Fails on multi-hop questions\" vs. \"fails
  when tool returns empty\" are different fixes.

- **Ablation / counterfactuals**: hold every node fixed, swap one
  node\'s config, re-run. This isolates causal contribution --- the only
  rigorous way to say \"node 3\'s prompt is the bottleneck.\" Reuses the
  variant machinery you already have.

- **Bottleneck flags**: cost/latency Pareto --- which node dominates
  spend, which is on the critical path.

## **3. Diagnosis --- explain why**

Turn localized failures into named, typed causes. Two complementary
methods:

**Rule-based detectors** (fast, deterministic, cheap) over traces:

- Context overflow / truncation before a failing node

- Tool schema mismatch or repeated tool errors

- Retrieval miss (RAG node returned low-relevance chunks --- measurable)

- Prompt-format drift (model ignored output contract → downstream parse
  failure)

- Over-long context degrading later nodes (lost-in-the-middle signal)

- Model-capability mismatch (cheap model on a reasoning-heavy node)

**LLM-as-analyst** (for the fuzzy residue rules can\'t catch):

- Feed a failing case\'s full trace (inputs, per-node prompts, outputs,
  reference) to an analyst model with a structured rubric; ask for a
  categorized diagnosis + confidence. Constrain it to a **fixed failure
  taxonomy** so outputs are aggregatable, not free-text.

> **Caveat, stated plainly:** LLM-as-judge and LLM-as-analyst are
> themselves noisy and biased. Calibrate the judge against a
> human-labeled subset, report judge agreement, and never let a single
> unverified LLM opinion drive an automated change. Diagnosis proposes;
> verification (§5) decides.

## **4. Proposals --- generate concrete changes**

Each diagnosis maps to a **change operator** that emits one or more
candidate Variant Specs:

  ----------------------------------------------------------------------
  **Diagnosis**              **Proposed operator**
  -------------------------- -------------------------------------------
  Reasoning-heavy node on    Upgrade model / enable extended thinking on
  weak model                 that node

  Cheap task on expensive    Downgrade --- cost win at equal quality
  model                      

  Prompt/output-contract     Rewrite prompt (LLM-driven), add format
  violations                 constraints/schema

  Context overflow /         Switch context policy → summarization or
  lost-in-middle             sliding window; reorder

  RAG relevance low          Tune top-k, swap retriever/embedding, add
                             rerank

  Missing/erroring tool      Add skill from registry, fix schema binding

  Redundant node             Prune / merge nodes
  ----------------------------------------------------------------------

- **Prompt improvement** specifically: use a DSPy-style or self-refine
  optimizer that proposes prompt edits *grounded in the failing cases*,
  not generic \"make it better.\"

- Rank proposals by **expected gain / cost of change**, respecting
  user-set constraints (budget ceiling, latency SLA, allowed providers).

- Present as a **diff** against the current Variant Spec, with the
  diagnosis and evidence (the specific failing cases) attached --- so
  the user sees *why*, not just *what*.

## **5. Verification --- prove it before recommending**

This is what separates the engine from a plausible-sounding suggestion
box.

- Auto-execute each proposal against the **same eval dataset**, held-out
  where possible to avoid overfitting the recommendation to the cases
  that generated it.

- **Statistical gate**: multi-seed runs, confidence intervals,
  significance test vs. baseline. Only surface changes whose improvement
  is real, not noise.

- **Regression check**: confirm the fix didn\'t degrade other case
  clusters or blow the cost/latency budget (a common failure --- fixing
  accuracy by silently 3×-ing cost).

- Emit a verdict: *proposed change, measured delta (with CI),
  cost/latency impact, cases fixed / cases broken.*

## **6. Reporting**

- **Per-run scorecard**: overall metrics, per-node breakdown, top
  failure clusters.

- **Ranked recommendation list**: each = diagnosis + evidence + proposed
  diff + *verified* delta.

- **Trend view** across variants over time (did the workflow actually
  get better across iterations, or just move problems around?).

- Human-readable synthesis (LLM-generated summary over the *structured*
  results above --- narration only, never the source of truth).

## **7. Automation levels**

Let the user choose how much authority to grant:

1.  **Advisory** --- engine reports diagnoses + verified proposals;
    human applies.

2.  **Assisted** --- one-click apply a verified proposal.

3.  **Autonomous** --- engine runs the full analyze → propose → verify →
    apply loop under hard constraints (budget, provider allowlist,
    min-improvement threshold, max iterations), with a full audit trail
    and rollback. This is your closed-loop optimizer from §5 of the main
    plan, now driven by *diagnosis* rather than blind search --- far
    more sample-efficient than grid/Bayesian search alone.

## **Delivery**

Slot after the eval harness:

- **Phase 4.5** --- attribution + rule-based diagnosis + scorecard
  (read-only).

- **Phase 5.5** --- proposal operators + verification gate
  (advisory/assisted).

- **Phase 6** --- autonomous loop, folded into the optimization engine.

**The one thing to get right:** analysis without verification is just
confident guessing, and LLM-generated analysis is *especially* prone to
it. The architecture\'s backbone is that every diagnosis is falsifiable
and every proposal is re-run and measured before it reaches the user.
The eval harness you already built is what makes this possible --- the
improvement engine is largely a consumer of it plus a set of change
operators.

Want me to draft the failure taxonomy, the change-operator catalog, or
the Analysis Engine\'s data schema in detail?

# **Addition: Metrics & Observability Subsystem**

A first-class telemetry layer that collects, stores, tracks over time,
and evaluates metrics across every node, run, and variant. The eval
harness and improvement engine are both *consumers* of this --- so it
should be designed as shared infrastructure, not bolted onto either.

## **1. Where it sits**

Runtime (every node execution) ──emits──→ Metrics Pipeline

├─ Collection (instrumentation → raw events)

├─ Storage (traces, time-series, eval results)

├─ Computation (derived + custom metrics, evaluators)

└─ Tracking (dashboards, trends, regressions, alerts)

↓

consumed by → Eval Harness, Improvement Engine, UI

Backbone: **OpenTelemetry** (already in the plan for tracing) extended
to metrics --- one instrumentation standard, GenAI semantic conventions,
no bespoke logging.

## **2. Metric taxonomy --- what to collect**

Organize by layer so metrics are addressable at the right granularity
(node / run / variant / system).

**Operational** (per LLM request, auto-collected from the provider
gateway)

- Latency: total, TTFT, tokens/sec, per-node and end-to-end

- Cost: input/output/cache tokens × price, per node, per run, cumulative

- Tokens: prompt / completion / thinking / cache-hit, context-window
  utilization

- Reliability: error rate, timeout rate, retry count, rate-limit hits

- Throughput / concurrency

**Quality** (per eval case, computed by evaluators)

- Task success: exact-match, regex, schema-valid, rubric score,
  LLM-as-judge

- Faithfulness / groundedness (esp. RAG nodes), hallucination rate

- Output-contract adherence (did the node emit parseable,
  downstream-valid output)

- Retrieval quality: relevance@k, recall, rerank gain

- Tool metrics: call success rate, wrong-tool rate, arg-validity

**Agent/workflow-specific**

- Node invocation count (the static-vs-runtime distinction from the main
  plan --- loops make this variable)

- Loop/iteration count, early-termination rate, path taken through the
  graph

- Per-node contribution to end-to-end success (from the attribution
  engine)

- Trajectory metrics: steps-to-completion, redundant-step rate

**Safety/governance** (optional but cheap to add here)

- Refusal rate, policy-flag rate, PII-in-output detection

> Not every metric fits every task. The system should map metric-sets to
> node types (a RAG node gets retrieval metrics; a router node gets
> routing-accuracy) rather than computing everything everywhere.

## **3. Collection**

- **Auto-instrumented** at the shim/gateway layer --- operational
  metrics require zero user effort; every request through the provider
  gateway is measured.

- **Evaluators** are pluggable functions run over traces to produce
  quality metrics: built-in (exact-match, JSON-schema, regex,
  LLM-judge) + **user-defined custom metrics** (register a scoring
  function, same pattern as the skill registry).

- **Structured events**: every metric is a typed event tagged with
  {variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}
  --- this tagging is what makes slicing, comparison, and trend-tracking
  possible later. Get it right at emission time.

## **4. Storage**

Three stores, different shapes:

- **Traces** → span store (OTel-compatible, e.g. Tempo/Jaeger or a
  trace-native tool) for per-run drill-down.

- **Metrics** → time-series DB (Prometheus/ClickHouse) for aggregation
  and trends over time.

- **Eval results** → Postgres (structured, queryable by
  variant/node/case) for comparison tables.

Everything keyed by config_hash so metrics are **reproducible and
attributable to an exact configuration** --- critical for \"did variant
B actually beat A.\"

## **5. Evaluation of metrics (not just collection)**

Raw numbers aren\'t enough --- the point is judgment:

- **Statistical rigor**: multi-seed runs, mean + confidence intervals,
  significance tests on variant deltas. Stochastic outputs make
  single-run metrics misleading --- already flagged in the main plan,
  enforced here.

- **Aggregation across levels**: roll up per-case → per-node →
  per-variant → system, and slice back down (per failure-cluster, per
  input category).

- **Composite scores**: user-weighted objective (e.g. 0.6·quality −
  0.3·cost − 0.1·latency) so multi-objective tradeoffs are explicit and
  optimization has a target.

- **Judge calibration**: for any LLM-as-judge metric, track agreement
  against a human-labeled subset and report it alongside --- an
  uncalibrated judge metric is decoration.

## **6. Tracking over time**

- **Dashboards**: per-variant scorecard, per-node breakdown,
  cost/latency Pareto, live run monitoring.

- **Trend view**: metric trajectories across variant iterations --- is
  the workflow actually improving, or are problems just moving around?
  (This is what makes the improvement engine\'s claims auditable.)

- **Regression detection**: flag when a new variant degrades any metric
  beyond threshold vs. the current best --- catches the classic \"fixed
  accuracy, tripled cost.\"

- **Alerts/budgets**: hard ceilings (cost/run, latency SLA, error-rate)
  that gate promotion of a variant or halt an autonomous optimization
  loop.

- **Leaderboard**: rank variants by composite score with full config
  lineage.

## **7. Delivery**

Introduce **earlier** than the analysis engine, since everything depends
on it:

- **Phase 2.5** --- OTel instrumentation + operational metrics +
  trace/metric storage (runs alongside the first runtime).

- **Phase 4** --- evaluator framework, quality metrics, custom-metric
  registry, statistical layer (this *is* much of the eval harness).

- **Phase 4.5+** --- trend tracking, regression detection, dashboards,
  budget gates.

## **Integration note**

This subsystem isn\'t a fourth thing bolted on --- it\'s the **shared
substrate** under the eval harness (§5), improvement engine\'s
attribution/verification (analysis addition §2, §5), and autonomous
loop\'s budget gates. Design the event schema and tagging
(variant/run/node/case/seed/config_hash) **first and once**; every
downstream feature reads from it. The most common failure mode is
emitting under-tagged metrics you can\'t later slice --- so the tagging
contract is the highest-leverage decision here.

Want me to draft the metric event schema, the evaluator-plugin
interface, or the composite-scoring/config-lineage model in detail?\
\
\
\
**Addition: Eval Set Generation & Weighted Workflow Scoring**

Two gaps closed: (1) automatically producing an eval set with enough
coverage to exercise the whole workflow *and* its edge cases, and (2)
scoring different node arrangements against a weighted objective.

## **Part A --- Eval Set Generation**

The system must synthesize eval cases, not just accept user-provided
ones --- real workflows rarely come with adequate test data.

**Coverage targets (what \"enough\" means)**

- **Path coverage** --- every edge in the Workflow IR exercised; every
  branch/router outcome hit; loops driven to min, typical, and max
  iterations. Use the IR graph directly to enumerate paths, then
  generate an input that forces each.

- **Node coverage** --- each node reached with inputs spanning its
  expected input schema.

- **Edge cases** --- empty/malformed input, tool-returns-nothing,
  retrieval-miss, context-overflow-inducing long inputs,
  ambiguous/adversarial prompts, unicode/injection, boundary values.

**Generation methods (layered)**

- **Seed from real traces** --- if dynamic tracing ran, mine actual
  inputs as the realistic baseline.

- **Schema-driven synthesis** --- from each node\'s typed I/O contract,
  generate valid + boundary + invalid inputs (property-based / fuzzing
  style).

- **LLM-driven synthesis** --- prompt a generator model to produce
  diverse, realistic cases per path and per failure category from a
  fixed taxonomy; force it to target uncovered paths specifically.

- **Adversarial/perturbation** --- mutate existing cases (paraphrase,
  inject noise, truncate) to probe robustness.

**Reference outputs**

- Where an oracle exists (exact-match, schema, deterministic tool
  result) --- auto-label.

- Where not --- LLM-generated reference + human review of a subset; or
  reference-free metrics (faithfulness, contract-adherence). Flag which
  cases are gold vs. weak-labeled; never let unverified synthetic
  references silently drive scoring.

**Coverage report + gap-filling loop**

- Measure achieved path/node/edge coverage; the generator iterates to
  fill gaps until thresholds met. This *is* the definition of
  \"enough.\"

> **Caveat:** LLM-synthesized eval sets inherit the generator\'s blind
> spots and can be unrealistic or trivially easy. Calibrate against the
> real-trace subset, dedupe near-identical cases, and track eval-set
> difficulty/diversity as a metric itself --- a passing score on a weak
> eval set is worthless.

**Slot:** Phase 4 (eval harness) needs this to have anything to run;
design it *with* the harness, seed from Phase 5\'s dynamic traces once
available.

## **Part B --- Weighted Workflow Arrangement Scoring**

After evaluating each arrangement (Variant Spec), reduce its metrics to
a single comparable score under user-controlled weights.

**Composite score**

- User defines a weighted objective over normalized metrics, e.g. Score
  = w_q·quality + w_c·(1−cost̂) + w_l·(1−latencŷ) + w_r·reliability −
  penalties

- **Normalize** each metric to \[0,1\] (min-max or z-score across the
  variant set) before weighting --- raw cost in \$ and quality in %
  aren\'t comparable otherwise.

- Weights are a **named profile** (e.g. \"quality-first\",
  \"cost-optimized\", \"balanced\") so the same runs can be re-ranked
  under different priorities without re-executing.

**Constraints vs. weights (keep separate)**

- **Hard constraints** = gates (max cost/run, latency SLA, min quality,
  provider allowlist). A variant that violates any is **disqualified**,
  not merely penalized --- prevents a cheap-but-broken arrangement from
  topping a cost-weighted leaderboard.

- **Soft preferences** = the weighted terms above, applied only to
  variants that pass gates.

**Statistical honesty**

- Score each arrangement over multi-seed runs → report **score ±
  confidence interval**, not a point value.

- When two arrangements\' CIs overlap, declare a tie rather than a false
  winner --- reordering deltas are often within noise.

**Ranking output**

- **Leaderboard** of arrangements ranked by composite score under the
  active weight profile, each row showing: score + CI, the component
  metric breakdown, gate pass/fail, and full config lineage
  (config_hash).

- **Pareto view** for the multi-objective case --- surface the
  quality/cost/latency frontier so users see tradeoffs directly rather
  than trusting one collapsed number.

- Re-rank instantly when the user changes weights (scores cached per
  variant; only the weighted sum recomputes).

**Feeds** the autonomous optimizer (Phase 6): the composite score *is*
the objective function the search maximizes, and the gates are its hard
constraints.

## **Timeline placement**

- **Phase 4** --- add **eval-set generation** (schema + LLM synthesis,
  coverage report) alongside the harness; add **normalization +
  composite scoring + leaderboard**.

- **Phase 5** --- enrich generation with dynamic-trace seeds and
  per-path targeting once the typed contracts and real-run traces exist.

- **Phase 6** --- composite score + gates become the optimizer\'s
  objective/constraints; Pareto-guided search.

**Two things to get right:** coverage must be *measured*
(path/node/edge), not assumed --- an eval set is only \"enough\" when
the coverage report says so; and scoring must **gate hard constraints
separately from weighted soft preferences**, over multi-seed runs with
confidence intervals, or the leaderboard will confidently rank noise.
Both reuse machinery you already have --- the IR graph drives coverage,
the metrics substrate drives scoring.

Want me to draft the coverage-report schema or the
composite-scoring/weight-profile data model?I\'ve already covered that
in my previous response --- eval set generation is in Part A and
weighted arrangement scoring is in Part B above.

If you\'d like to go deeper, I can draft one of these concretely:

- **Coverage-report schema** --- how path/node/edge coverage is measured
  and reported, plus the gap-filling loop that decides when the eval set
  is \"enough.\"

- **Composite-scoring / weight-profile data model** --- normalization,
  gate-vs-weight separation, named profiles, and the leaderboard/Pareto
  output structure.

- **Eval-case generator interface** --- the plugin contract for
  seed/schema/LLM/adversarial generators and how they target uncovered
  paths.

Or if you\'d prefer, I can pull the whole plan (Discovery → Config →
Runtime → Metrics → Eval → Analysis → Improvement → Eval-gen → Scoring,
with the phased timeline) into a single shareable document.

Which would be most useful?

**Implementation Timeline**

**Critical-path dependencies:** IR + event schema (Phase 0) gate
everything → Discovery (P1) → Config/Runtime (P2) → Metrics (P2.5) →
Eval (P4) → Analysis (P4.5) → Improvement (P5.5) → Autonomous (P6).
Metrics and typed I/O contracts are the two most commonly underestimated
items --- front-load their design.

Want this as a Gantt-style visual or a file you can share?

# Addition: Agentic Pattern Classification

A subsystem that inspects the discovered Workflow IR and classifies
which agentic pattern(s) it implements. This isn\'t cosmetic labeling
--- the detected pattern drives which metrics apply, which failure modes
to check, and which improvement operators are valid. A router node and a
reflection loop fail and get optimized in completely different ways.

## 1. Where it sits

Workflow IR + Traces ──→ Pattern Classifier

├─ Structural analysis (graph topology)

├─ Behavioral analysis (runtime traces)

└─ Pattern label(s) + confidence

↓

drives → metric-set selection, failure-taxonomy scoping,

improvement-operator gating, eval-case generation targets

Runs after Discovery (needs the IR) and benefits from dynamic tracing
(needs real runs to confirm behavioral patterns). A workflow is usually
multi-pattern --- classify per-subgraph, not one label for the whole
thing.

## 2. How it detects --- two signals

Structural (from the IR graph) --- topology is a strong prior:

- Linear chain of LLM nodes → Prompt Chaining

- One node fanning to N specialists by a conditional → Routing

- Fan-out to parallel nodes → merge → Parallelization

- Node whose output loops back to a generate node → Reflection

- Node bound to tools in the skill registry → Tool Use

- Manager node dispatching to role nodes + shared context → Multi-Agent
  Collaboration

- Retriever + embed + rerank nodes feeding a generator → Retrieval (RAG)

- Cost/complexity-conditioned model selection → Resource-Aware
  Optimization

Behavioral (from traces) --- confirms what topology can\'t:

- Iteration count \> 1 on a self-edge → confirms Reflection vs. a
  one-shot

- A planning node emitting a task list consumed downstream → Planning

- Repeated tool-call → observe → retry trajectory → Tool Use /
  Exception-Handling

- Sampling the same node N times then voting → Self-Consistency

- Memory read/write against a store between turns → Memory Management

- Human-approval pause in the trace → Human-in-the-Loop

Classifier itself: rule-based detectors over topology + trace signatures
(fast, deterministic) as the primary layer, with an LLM-as-classifier
fallback for ambiguous graphs --- constrained to the fixed pattern
taxonomy below, with confidence scores. Same discipline as the diagnosis
engine: rules first, LLM for the fuzzy residue, never unverified.

## 3. The pattern taxonomy

Use the 20-pattern set as the fixed vocabulary. Group them by what they
govern, because they operate at different layers (they\'re not mutually
exclusive):

Control-flow patterns (graph shape): Prompt Chaining, Routing,
Parallelization, Reflection, Planning, Prioritization, Exploration &
Discovery\
Capability patterns (what a node can do): Tool Use, Retrieval/RAG,
Memory Management, Reasoning Techniques
(CoT/ToT/self-consistency/debate)\
Coordination patterns (multi-agent): Multi-Agent Collaboration,
Inter-Agent Communication\
Governance patterns (cross-cutting): Goal Setting & Monitoring,
Exception Handling & Recovery, Human-in-the-Loop, Evaluation &
Monitoring, Guardrails & Safety, Resource-Aware Optimization, Learning &
Adaptation

A real workflow is a composition: e.g. \"Routing → per-branch Tool Use →
Reflection, under Guardrails, with Memory.\" The classifier emits a set
with per-pattern confidence and the subgraph each applies to.

## 4. Why the label matters --- it drives the rest of the system

This is the payoff; classification feeds four subsystems:

- Metric selection → pattern picks the metric-set. Routing gets
  routing-accuracy/misroute-rate; RAG gets retrieval
  relevance@k/faithfulness; Reflection gets
  iteration-count/convergence/quality-gain-per-revision; Parallelization
  gets merge-consistency; Multi-Agent gets inter-agent-message-validity.

- Failure-taxonomy scoping → only check failure modes the pattern can
  exhibit. Routing → misroutes; Planning → infeasible/circular plans;
  Tool Use → wrong-tool/schema errors; Reflection → non-convergence or
  degradation-on-revision.

- Improvement-operator gating → propose only valid changes. \"Add a
  critic node\" makes sense for a chain missing Reflection; \"add
  rerank\" only for a RAG subgraph; \"route cheap cases to a smaller
  model\" only where Resource-Aware routing exists.

- Eval-case targeting → generate cases that stress the pattern: routers
  need boundary/ambiguous inputs across all branches; reflection loops
  need cases that require multiple revisions; parallel merges need
  conflicting-branch inputs.

Anti-pattern detection falls out naturally: a Reflection loop that never
improves quality across iterations, a Router that sends everything to
one branch, Parallelization with no real independence, a plan never
followed. These become diagnoses the improvement engine acts on.

## 5. Timeline placement

- Phase 3.5 (after Discovery + basic Runtime, alongside Skills/Context)
  --- structural classifier over the IR: control-flow + capability
  patterns from topology. Low effort, high leverage, and it immediately
  sharpens metric selection in Phase 4.

- Phase 5 --- add behavioral classification once dynamic tracing exists
  (confirms Reflection, Planning, Memory, HITL from real runs) and wire
  pattern → metric-set / failure-taxonomy / eval-targeting.

- Phase 5.5+ --- pattern → improvement-operator gating + anti-pattern
  diagnoses folded into the improvement engine.

The key idea: pattern classification is a *dispatcher*, not a
decoration. Its output selects which metrics, failure modes, eval cases,
and improvement operators are in-scope for each subgraph --- which is
what stops the rest of the system from evaluating a router as if it were
a RAG pipeline. Detect structurally first (topology is cheap and
reliable), confirm behaviorally from traces, keep the LLM classifier
constrained to the fixed 20-pattern taxonomy with confidence scores.

Want me to draft the structural-signature rules for each of the 20
patterns, or the pattern→metric-set / pattern→failure-mode mapping
tables?
