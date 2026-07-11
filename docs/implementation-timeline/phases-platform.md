# Phases 0 → 3.5 — Building the Platform

The platform half stands up the machinery: a canonical IR, node extraction, per-node
configuration, a sandboxed executor, the telemetry substrate, and pattern classification. By the
end of P3.5 you can discover a workflow, override any node, run it under full tracing, and know
what *kind* of workflow each subgraph is — the precondition for measuring it correctly.

Each phase below is decomposed by the responsible senior roles, applying their playbook
discipline. See [`roles-and-ownership.md`](roles-and-ownership.md) for how each role's internal
phases map on.

---

## Phase 0 — Foundations (IR + event schema) · ~Weeks 1–3 · **Milestone M0**

> Gate for everything. The two most-underestimated items in the whole plan — the metric tagging
> contract and the typed I/O contract — are *designed* here even though they are *built* later.

**Goal.** Freeze the two schemas every subsystem depends on, choose the storage shapes, and
scaffold the repo + CI so later phases add rather than re-litigate.

### System Designer (lead)
- **Clarify requirements.** Functional: discover static LLM nodes, override them, execute, score.
  Non-functional (the ones that shape architecture): how many repos/day, nodes/repo, runs ×
  seeds, traces/run, metric cardinality, reproducibility (exact-config replay), and cost
  sensitivity. Record explicit assumptions and scope boundaries.
- **Estimate scale.** Back-of-envelope the volumes that decide storage: e.g. *N* variants ×
  *K* cases × *S* seeds × *nodes* → metric event count and span volume per optimization run.
  This is what tells you TSDB vs. Postgres vs. span store, not reflex.
- **Design the two contracts:**
  1. **Workflow IR** — JSON graph of nodes with metadata: call site as a precise source span
     (file, line, AST path) so later phases can rewrite it, current model, prompt
     template, tools/skills, context-assembly logic; **static nodes** (definition) distinct from
     **runtime invocations** (execution instances). Node count reported *per-definition*.
  2. **Metric event schema** — every event tagged `{variant_id, run_id, node_id, case_id, seed,
     timestamp, config_hash}`. This tagging is the highest-leverage decision on the project.
- **Data model + storage:** three stores by shape — spans (OTel-compatible), metrics (TSDB),
  eval results (Postgres) — everything keyed by `config_hash`; large prompt/artifact blobs in
  object storage keyed by content hash for reproducibility.
- **Design the typed per-node I/O contract now** (input schema + output schema) even though
  re-arrangement ships in P5 — it must be a first-class field in the IR from day one.

### Backend (support)
- Model the invariants into the schema: `config_hash` uniqueness, FKs from eval results →
  variant/node/case, non-null tags. The DB enforces the tagging contract application code forgets.
- Choose migration strategy (expand-migrate-contract) so IR/registry schemas evolve safely.

### DevOps (support)
- Repo scaffold, CI (build/test/lint green), secrets management baseline, and the OTel
  conventions doc (GenAI semantic conventions) the whole team will emit against.

### Product (support)
- Draft the top-level user journey (import → inspect → configure → run → compare → diagnose →
  apply) and the automation-level model as a north star, so UI phases build toward it.

**Deliverables:** versioned `workflow-ir.schema.json`, `metric-event.schema.json`, config-hash
spec, storage decision record, repo scaffold + CI.
**Exit criteria (M0):** both schemas reviewed and frozen; a hand-written IR sample validates; CI
green.

---

## Phase 1 — Discovery MVP (Go, static) · ~Weeks 3–7 · **Milestone M1**

> Prove node extraction on a single language before generalizing. Static IR only.

**Goal.** From a Go repo, extract static LLM call sites into a valid Workflow IR.

### Backend (lead) — applies explore → design → implement → test → harden → review
- **Explore.** Study how target Go repos wrap LLM SDKs; catalog real call shapes.
- **Design.** A **signature registry** of known SDK entrypoints (`anthropic.messages.create`,
  `openai.chat.completions.create`, LangChain/LangGraph invoke, Bedrock converse) **plus**
  mandatory **user-declared entrypoints** via `llm-eval.yaml` — real codebases wrap the SDK, so
  signature matching alone misses nodes. This is not optional.
- **Implement.** Parse with `go/ast` (tree-sitter as the language-agnostic path later). For each
  call site capture a precise source span (file, line, AST path) so later phases can rewrite it,
  and extract: model arg, messages/prompt construction, tools/skills passed, and upstream
  data flow feeding the prompt. Build the call graph: a node = an LLM-invoking function/agent
  step; edges = data/control flow. Special-case framework DAGs (LangGraph/CrewAI) by reading
  their declarative graph rather than inferring it.
- **Test.** Fixture repos with known node counts; assert extracted IR matches. Include the
  wrapper case (SDK hidden behind an in-house function) to prove user-declared entrypoints work.
- **Harden/Review.** Never execute the repo during static analysis; treat source as untrusted
  text. Report node count **per static definition** and flag loops/agents as variable-at-runtime.

### AI Engineer (support)
- Validate that extracted prompt-construction/context-assembly metadata is faithful enough to
  later drive overrides; flag call sites where static data-flow is ambiguous (candidates for P5
  dynamic tracing).

### DevOps / System Designer (support)
- CI job that runs Discovery on fixture repos and validates the emitted IR against the schema.

**Deliverables:** Discovery service (Go), signature registry, `llm-eval.yaml` entrypoint config,
IR emitter.
**Exit criteria (M1):** on a real Go repo, static nodes extracted, IR emitted and diffable,
wrapper nodes found via user-declared entrypoints.

---

## Phase 2 — Configuration Layer + Runtime · ~Weeks 6–11 · **Milestone M2**

> Make nodes configurable and executable by generating and applying a reviewable source change
> (patch/PR). Overlaps P1's tail.

**Goal.** Execute a (initially hardcoded) graph with per-node model/prompt overrides resolved
from registries and applied by rewriting the call sites via a deterministic AST transformation,
delivered as a reviewable diff.

### Backend (lead)
- **Configuration Layer / source-transformation engine.** The system rewrites the discovered
  call sites via a deterministic AST transformation, delivered as a reviewable diff, setting the
  hardcoded parameters at each call site to the Variant Spec's values. Per-node dimensions:
  **Model** (provider + id + params, backed by a Model
  Registry), **Prompt** (versioned templates with variable slots, git-like Prompt Registry),
  **Skills/Tools** (Skill Registry: name → schema + impl, JSON-schema contract), **Context
  strategy** (pluggable policy — full/sliding-window/summarization/RAG/compaction).
- **Variant Spec.** `{node_id → {model_ref, prompt_ref, skill_refs[], context_policy}}` + a node
  ordering/graph. Registries are shared, versioned, referenced by ID.
- **Runtime.** *Loader* resolves every `*_ref` against registries at invocation time; models via
  a unified provider gateway (LiteLLM-style so provider swaps are transparent); prompts rendered;
  skills bound; context policy instantiated. *Executor* runs the transformed working copy in an
  isolated sandbox, node I/O passing through the typed contract.
- **Idempotency & reproducibility.** Same `config_hash` + seed replays reproducibly; provider
  calls carry timeouts + backoff; no double-writes.
- **Storage.** Variant Specs + registries in Postgres; blobs content-hashed in object store.

### System Designer (support)
- Own the provider-gateway abstraction boundary and the executor's contract semantics.

### DevOps (support)
- Stand up the provider gateway with secrets from a manager (never in code/logs); run queue seed.

### Frontend (support, minimal)
- A bare run/inspect view: submit a Variant Spec, watch a run, see node I/O — loading/error/empty
  states first-class.

**Deliverables:** registries (model/prompt/skill/context), source-transformation engine (codemod),
Variant Spec type, Runtime loader+executor, provider gateway.
**Exit criteria (M2):** a hardcoded graph runs end to end with per-node model/prompt overrides
applied as a source transformation (reviewable diff), running the transformed working copy in a
sandbox.

---

## Phase 2.5 — Metrics & Observability substrate · ~Weeks 9–13 · **Milestone M3**

> Lands *right after* the first Runtime, not late — "if it isn't observable, it isn't done." This
> is shared infrastructure the eval harness and improvement engine both consume; design it once.

**Goal.** Every node execution emits tagged OTel spans + operational metrics into the trace and
metric stores.

### DevOps (lead)
- **Collection.** Auto-instrument at the provider gateway so operational metrics require zero
  user effort: latency (total/TTFT/tokens-per-sec), cost (in/out/cache tokens × price), tokens
  (prompt/completion/thinking/cache-hit, context-window utilization), reliability (error/timeout/
  retry/rate-limit), throughput/concurrency.
- **Storage.** Spans → span store (Tempo/Jaeger); metrics → TSDB (Prometheus/ClickHouse); eval
  results → Postgres. Everything keyed by `config_hash` for reproducibility and attribution.
- **Structured events.** Every metric is a typed event carrying the full tag set from P0. Get
  tagging right at emission — the top failure mode is under-tagged metrics you can't later slice.

### AI Engineer / System Designer (support)
- Define the evaluator-plugin interface stub (built-in + user-defined) so P4 slots in; confirm
  the tag set supports every slice P4/P4.5 will need (per-cluster, per-input-category).

**Deliverables:** OTel instrumentation at the gateway, three stores wired, tagged-event emitter.
**Exit criteria (M3):** a run produces drillable spans and queryable operational metrics, all
tagged and keyed by `config_hash`.

---

## Phase 3 — Context strategies + Skill registry + Sandbox · ~Weeks 12–16

> Pluggable context policies and safe tool execution. Two roles co-lead: Backend (policies) and
> DevOps (isolation).

**Goal.** Ship the context-strategy plugins and execute skills/tools from the target repo safely.

### Backend (lead)
- Implement context policies as named strategies with params: full history / sliding window
  (window size) / summarization (summarizer model) / RAG retrieval (top-k) / semantic compaction.
- Flesh out the Skill Registry: each entry carries a JSON-schema contract; the runtime validates
  tool availability against it **before** execution.

### DevOps (co-lead)
- **Sandbox/isolation.** Skills may execute arbitrary tool code from the target repo — treat it
  as untrusted. Run each node in a subprocess/container sandbox; **never** run discovered code
  with ambient credentials; least-privilege network + filesystem. This is the project's sharpest
  security boundary.

### AI Engineer (support)
- Advise on context-policy semantics (compaction, just-in-time retrieval, sub-agent isolation)
  from the context-engineering discipline; these become P5.5 change operators.

**Deliverables:** context-policy plugins, hardened Skill Registry, sandbox runner.
**Exit criteria:** a node using a repo tool executes inside the sandbox with no ambient creds; a
context policy is swappable per node via config.

---

## Phase 3.5 — Pattern Classifier (structural) · ~Weeks 15–17 · **Milestone M4**

> Low effort, high leverage. Runs after Discovery; sharpens metric selection immediately in P4.
> A classifier is a *dispatcher*, not decoration — it selects which metrics, failure modes, eval
> cases, and improvement operators are in-scope per subgraph.

**Goal.** Label each subgraph's agentic pattern(s) from IR topology, with confidence.

### AI Engineer (lead)
- **Structural detectors** over the IR graph (topology is a strong, cheap prior):
  linear chain → Prompt Chaining; conditional fan to N specialists → Routing; fan-out→merge →
  Parallelization; output loops back to a generate node → Reflection; node bound to registry
  tools → Tool Use; manager dispatching to role nodes + shared context → Multi-Agent; retriever+
  embed+rerank→generator → RAG; cost/complexity-conditioned model selection → Resource-Aware.
- Classify **per-subgraph**, not one label for the whole workflow; emit a set with per-pattern
  confidence and the subgraph each applies to. Use the fixed **20-pattern taxonomy** as the
  vocabulary (control-flow / capability / coordination / governance groups).
- Rule-based detectors first (deterministic); an LLM-as-classifier fallback for ambiguous graphs,
  **constrained to the fixed taxonomy** with confidence scores — same discipline as diagnosis:
  rules first, LLM for the fuzzy residue, never unverified.
- Behavioral confirmation (iteration counts, planning lists, voting, memory R/W, HITL pauses)
  is deferred to **P5** once dynamic tracing exists.

### System Designer / Frontend (support)
- Define the pattern-label field on the IR; surface labels on the graph UI.

**Deliverables:** structural classifier, 20-pattern taxonomy, pattern→metric-set mapping table.
**Exit criteria (M4):** subgraphs receive pattern labels with confidence; P4's metric-set
selection keys off the label (a RAG node gets retrieval metrics, a router gets misroute-rate).
