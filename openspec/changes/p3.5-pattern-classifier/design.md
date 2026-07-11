# Design — P3.5: Pattern Classifier (structural)

Cross-reference: product rationale in [`../../../docs/prd/P3.5-pattern-classifier.md`](../../../docs/prd/P3.5-pattern-classifier.md).

## Context

By the end of P3 the platform can discover, override, run, and trace a workflow — but every subgraph
is undifferentiated to the machinery that follows. The eval harness (P4) needs to know *what kind*
of workflow each region is before it can measure it correctly: a router is scored on misroute-rate,
a RAG pipeline on retrieval relevance@k / faithfulness, a reflection loop on quality-gain-per-
revision. The classifier exists to make that decision **mechanical and per-subgraph**, and it does
so under two constraints that shape every decision below: (1) it runs on the **static IR only** (no
traces yet), so it can ship now but can only see topology; and (2) it follows the AI Engineer
discipline of **rules first, LLM for the fuzzy residue, never unverified** — the same shape as the
diagnosis engine.

## Decision 1 — The classifier is a dispatcher, not an annotation

**Decision.** The classifier's output is consumed through a stable seam `MetricSetFor(pattern) →
MetricSet`, not as decorative graph labels. The label *selects* the in-scope metric-set (and, later,
failure taxonomy, eval targets, improvement operators) per subgraph.

**Why.** This is the entire point of the phase: it stops P4 from evaluating a router as if it were a
RAG pipeline. Framing the output as a dispatcher interface (rather than a UI annotation that happens
to exist) forces the downstream contract to be first-class — P4 keys off the label mechanically, and
the M4 exit is defined by that behavior working (RAG subgraph → retrieval metrics; router →
misroute-rate).

**Alternative rejected.** Emitting labels purely for display and letting each consumer re-derive
metric relevance — that would re-litigate the classification decision in four downstream subsystems
and drift.

## Decision 2 — Classify per-subgraph, never one label for the whole workflow

**Decision.** Partition the IR into subgraphs and emit a *set* of `{pattern, confidence, source,
subgraph_ref}` labels, each tied to the region it applies to. A real workflow is a composition
("Routing → per-branch Tool Use → Reflection, under Guardrails, with Memory").

**Why.** A single whole-workflow label is almost always wrong and useless for dispatch: the same
workflow legitimately contains a router in one region and a RAG pipeline in another, and each region
needs a *different* metric-set. Per-subgraph labeling is the only representation that lets two
patterns coexist and drive different metrics on different regions (FR2).

**Subgraph boundary (proposed).** A subgraph is the **maximal region matching a single structural
signature**; overlaps are resolved by the precedence rule in Decision 4. (PRD Q1 — this is the
proposed resolution.)

## Decision 3 — Rules first, deterministic; LLM only for the ambiguous residue

**Decision.** Deterministic structural detectors are the **primary** layer and cover the eight
patterns whose topology fully (or almost fully) determines them. An LLM-as-classifier fallback runs
**only** on subgraphs no rule detector covers confidently, is **constrained to the fixed 20-pattern
taxonomy**, and must return a confidence. Rules take precedence — the LLM never overrides a confident
rule label.

**Why.** Topology is a cheap, reliable prior; the common cases should never pay an LLM call and never
vary run to run (determinism NFR). Admitting the LLM only for the fuzzy residue keeps outputs
aggregatable and spend proportional to ambiguity, not workflow size. This is the exact discipline the
diagnosis engine uses (rules-first, LLM for the residue, never unverified) applied to classification.

**Consequences.**
- A **fully rule-covered IR makes zero LLM calls** (tested).
- The LLM cannot invent a pattern the rest of the system can't consume — structured output over the
  enum; any out-of-taxonomy output is rejected and dropped (FR15).
- The LLM fallback is reproducible: `{model, seed, temperature, prompt_version, taxonomy_version}`
  recorded per run, keyed by `config_hash`.

**Alternative rejected.** LLM-first classification with rules as a sanity check — noisier, costlier,
non-deterministic on the common case, and it inverts the trust order the project mandates.

## Decision 4 — The eight structural signatures

Each detector is a **pure function of IR topology + node metadata**; identical IR yields identical
labels. Signatures:

| Pattern | Signature over the IR |
|---|---|
| Prompt Chaining | ≥ 2 LLM nodes, linear data-edge chain, no fan-out/fan-in/loop |
| Routing | one node, **control** edges fanning conditionally to N ≥ 2 specialists |
| Parallelization | fan-out to ≥ 2 independent nodes reconverging at a merge node |
| Reflection | output cycles back (self-edge / loop) to a generate node — **structural candidate** |
| Tool Use | node `tools_skills` non-empty and resolvable in the Skill Registry |
| Multi-Agent Collaboration | manager node dispatching (control edges) to role nodes over shared context |
| Retrieval / RAG | retriever + embed + rerank nodes chained into a generator |
| Resource-Aware Optimization | control branch selecting among model tiers on a cost/complexity condition |

**Precedence / overlap.** When signatures overlap on a region, the **control-flow** pattern owns the
subgraph; **capability** patterns (notably Tool Use) co-exist on the node inside it. So a Tool Use
node inside a Routing branch yields *both* labels — Routing on the subgraph, Tool Use on the node —
not a contest. (PRD Q3.)

**Near-miss guards** are part of each detector: a linear chain is not Routing; an empty-`tools_skills`
node is not Tool Use; a fan-out with no merge is not Parallelization.

## Decision 5 — Behavioral patterns are structural *candidates*, not confirmed facts

**Decision.** Patterns that require runtime evidence to confirm — Reflection (iterates > 1),
Planning (emits a consumed task list), Reasoning Techniques (sample-N-then-vote), Memory Management
(read/write between turns), Human-in-the-Loop (approval pause), and the rest of the ⏳ rows — are
**not asserted as confirmed** from structure. Where structure shows a *candidate* (a loop-back edge
for Reflection), the classifier MAY emit the label as a candidate with **capped confidence** that
reflects the missing behavioral confirmation; it never emits a false 1.0.

**Why.** A loop-back edge proves a loop *can* run, not that it *does* iterate or that iteration
improves quality. Asserting Reflection from structure alone would feed P4 a metric-set
(quality-gain-per-revision) for a loop that may fire once. Honesty about what static analysis can
prove is the same principle as "diagnosis proposes, verification decides." Full confirmation lands in
**P5** when dynamic tracing exists (iteration count, planning lists, voting, memory R/W, HITL pauses
are all trace signals). (PRD Q2 — the capped-confidence band is the proposed resolution.)

## Decision 6 — `pattern_labels` is an additive IR field reused from P0

**Decision.** Labels are written back into the IR via the **`pattern_labels`** field P0 already
reserved (nodes and/or subgraphs), additively. Absence does not invalidate an IR; presence does not
bump `ir_version` MAJOR.

**Why.** The slot was designed in P0 exactly so P3.5 could populate it without a schema break — "an
IR without pattern labels is valid; when the P3.5 classifier later adds `pattern_labels`, the
document still validates at the same `ir_version` MAJOR." This keeps the classifier a pure additive
consumer/producer of the IR contract (contracts outlive code). Pre-P3.5 consumers keep parsing.

## Decision 7 — The pattern→metric-set table is authored now, for the eight available labels

**Decision.** Deliver the full pattern→metric-set mapping table now, covering all 20 patterns, but
wire P4's selection to the **eight** structurally-available labels. The ⏳ patterns' metric-sets are
authored so they activate the moment behavioral classification (P5) supplies their labels.

**Why.** P4's metric-set selection is the M4 exit; it must key off real labels immediately. Authoring
the whole table now (rather than only the eight) means P5 wires behavioral labels into an existing
table instead of re-designing dispatch — the mapping is the durable artifact, the label source is
what grows.

## Data model sketch

```
pattern_label {
  pattern         : enum(20-pattern taxonomy)   -- closed vocabulary; rejected if outside
  confidence      : float [0,1]
  source          : enum(rule, llm)
  subgraph_ref    : id                          -- which region this label applies to
  detector_id?    : string                      -- set when source = rule
  llm_run_ref?    : id                          -- set when source = llm (reproducibility record)
  taxonomy_version: string                      -- pins the taxonomy the label was drawn from
}
-- written additively onto IR nodes and/or subgraphs; absence is valid.

llm_classification_run {                         -- reproducibility record, keyed by config_hash
  model, seed, temperature, prompt_version, taxonomy_version
}
```

## Key interfaces

```
Classify(IR) -> []PatternLabel        // partition → rule detectors → constrained LLM residue
MetricSetFor(pattern) -> MetricSet     // the dispatch seam P4's metric-set selection keys off
```

## Risks

- **LLM emits out-of-taxonomy / free-text** — constrained structured output over the enum; reject +
  drop + diagnostic (Decision 3). Tested with an adversarial prompt.
- **Structural label asserted as behavioral fact** — behavioral patterns are candidates with capped
  confidence, confirmation deferred to P5 (Decision 5); spec requirement, tested.
- **Whole-workflow single label** — per-subgraph partition + `subgraph_ref`; composite fixture
  asserts two patterns on two subgraphs (Decision 2).
- **LLM leaks into rule-covered subgraphs (non-determinism)** — rules-first precedence; determinism
  test asserts zero LLM calls on a fully rule-covered IR (Decision 3).
- **Metric dispatch mismatch** — single source-of-truth table; test asserts RAG→retrieval metrics,
  router→misroute-rate (Decision 7).
- **Overlap ambiguity** — control-flow owns the subgraph, capability co-exists on the node
  (Decision 4).
