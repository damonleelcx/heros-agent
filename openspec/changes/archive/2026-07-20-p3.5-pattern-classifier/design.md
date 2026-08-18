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

## Resolution — the subgraph-partition contract (closes PRD Q1 and Q3)

Design Decision 2 proposed "the maximal region matching a single structural signature" as the
subgraph boundary, and Decision 4 proposed control-flow-owns / capability-co-exists for overlaps.
Both are now **resolved and implemented** (`internal/patternclassifier/partition.go`,
`precedence.go`). The contract, precisely:

**Regions are proposed by detectors, not carved before detection.** There is no meaningful way to
partition an IR *before* knowing which signatures match it, so each detector emits a
`RegionProposal` — "this node set matches my signature" — and a single arbitration pass turns
proposals into subgraphs. Detection stays a local, pure predicate per detector; precedence lives in
exactly one place and no detector looks at another.

**A region's identity is content-addressed:** `subgraph_id = "sg_" + sha256(sorted node_ids)[:12]`.
This is the same discipline as `node_id`. A counter would depend on detector execution order, which
would make labels non-diffable and put "byte-identical across runs" out of reach. Content-addressing
also means the *same* region found by two detectors gets the *same* id — which is what makes "two
patterns co-exist on one subgraph" representable at all.

**Scope, not group, is the axis overlap turns on.** A *region-scoped* pattern claims a multi-node
shape and can contest nodes; a *node-scoped* pattern (Tool Use) claims one node's capability and
contests nothing. A node-scoped label's `subgraph_ref` is the `node_id` itself, so its write-back
target is unambiguous (the node's own `pattern_labels`).

**Overlap resolution, in the order checked:**

| Relation between two region claims | Resolution | Why |
|---|---|---|
| One is node-scoped | Both kept; the capability co-exists on the node | Tool Use inside a Routing branch is *both*, not a contest (PRD Q3) |
| Identical node sets | Both kept, sharing one `subgraph_id` | Two true statements about one region; suppressing one deletes a legitimate composite |
| One strictly contains the other | Both kept, as two subgraphs | Nesting is real composition; each level dispatches its own metric-set |
| Partial overlap, neither contains the other | One owner: lower group rank (control-flow → coordination → capability → governance), then more nodes, then lexical `subgraph_id`. Loser **dropped with a diagnostic** | The only genuine ambiguity. "Maximal region matching a single signature" picks the owner; the drop is never silent because a dropped label is a metric-set that will not be computed |

**The residue** — nodes no proposal covers — is split into weakly-connected components, and only
those components are shown to the LLM fallback. Components rather than one bag: two unclassified
islands are two different questions, and merging them would ask the model to name one pattern
spanning nodes that never touch.

## What these tests do NOT measure

The P3.5 suite is green and the calibration table reports 1.00 recall and 1.00 precision across the
hand-labeled fixture set. That number is a statement about a fixture set, not about the world, and
the following gaps are stated here rather than left to be discovered later.

**The fixture set is synthetic and small.** Seventeen hand-built IRs, authored by the same person who
wrote the detectors. They encode what the signatures were *designed* to match, so they cannot show
that the signatures match what real discovered workflows look like. The first real P1 output run
through the classifier is the measurement that matters, and it has not happened. Perfect precision on
a set you wrote is the weakest possible evidence of precision; the near-miss fixtures are the only
reason the number means anything at all.

**Three discriminators are inferences, not facts the IR states.**
- *Multi-Agent's "shared context"* is read off the role nodes sharing one `context_assembly.policy`
  name. Two agents on the same policy do not provably share a conversation.
- *Resource-Aware's "cost/complexity condition"* is inferred from "same prompt, different model". The
  IR never states why the branch exists; a branch that varies the model for a non-cost reason (an
  A/B test, a fallback on provider outage) reads identically.
- *Routing vs Multi-Agent* turns on `invocation_semantics.type == "conditional"`, which is Discovery's
  best-effort classification of the call site, not ground truth.

These carry `ConfidenceTopologyStrong` (0.80) rather than the topology-determined band for exactly
this reason, but a discounted confidence is not the same as a measured error rate. Nobody has
measured how often these three are wrong.

**Retrieval roles depend on caller-supplied configuration.** `SkillRoles` maps registered skills to
retriever/embed/rerank. With it empty, RAG detection falls back to the `rag-retrieval` context policy
alone and a real retrieval pipeline bound via tools will be missed silently. There is no diagnostic
for "you probably meant to configure this", because the classifier cannot tell an unconfigured
deployment from one with no retrieval.

**The LLM fallback has never been run against a real model.** Every fallback test uses a stub. What is
proven is that the *classifier* rejects out-of-taxonomy, free-text, missing-confidence and
out-of-range answers, and that it records a reproducibility key. What is NOT proven is how often a
real model returns a useful label on a genuinely ambiguous subgraph, or whether the enumerated prompt
is good enough to make its answers worth having. Fallback quality is unmeasured.

**Determinism is proven for the rule layer only.** "Classify the same IR twice → byte-identical" is
asserted over the rule path. With a real model and a non-zero temperature it is false, which is why
that configuration emits a diagnostic rather than being silently accepted.

**Reflection is a candidate, and that is a floor not a ceiling.** The detector fires on any cycle. It
cannot distinguish a genuine critique-revise loop from a retry loop or a paginating fetch; all three
are cycles. The capped confidence encodes "a loop exists", which is all that is true.

**Confidence is calibrated, not probabilistic.** The bands are documented decisions with stated
reasons, checked for consistency by the calibration test. A label at 0.80 does not mean "correct 80%
of the time" — no frequency has been measured, and reading the numbers that way would be a mistake.

**UI verification was manual.** The three visual states (rule / llm / unclassified) and legibility on
a 22-node composite IR were checked in a real browser and are recorded in the change; the automated
tests assert the underlying DATA, not the rendering. Three defects — `null` collections blanking the
page, region boxes overlapping so a router branch drew inside the RAG box, and an invisible
loop-back edge — were found only by looking, and only the first two now have regression tests. There
is no screenshot-diff gate.

## Correction — one canonical numbering (2026-07-20)

**What was wrong.** Not the set: the taxonomy always held exactly the 20 patterns, verified by
diffing it against the canonical list — nothing missing, nothing extra. What was wrong was that
there were **two numberings of those 20**. The PRD §8.3 table numbered rows group-by-group
(control-flow 1–7, then capability 8–11, …), while the canonical sequence numbers them 1–20 in an
order that interleaves the groups. So "Pattern 5" meant **Planning** in the PRD and **Tool Use**
everywhere else, and "Pattern 13" was **Inter-Agent Communication** or **Retrieval/RAG** depending on
which document the reader had open.

**Why it is worth fixing rather than noting.** A missing pattern is loud — a detector has nothing to
fire on, a metric-set row is absent, something fails. A duplicate numbering is silent: every document
is internally consistent, every test passes, and the set is complete. The damage only appears in
conversation, where an ordinal is what people actually say ("implement Pattern 7"), and it lands as a
person building the wrong thing from a correctly-written spec.

**The resolution.** The canonical ordinal is now first-class data, not a document convention:

- `PatternInfo.Ordinal` (1–20) carries it, and `ByOrdinal(n)` resolves it, so "Pattern 13" is a
  lookup rather than something counted off a table by hand.
- `Patterns()` returns the taxonomy in canonical ordinal order, which is what the LLM fallback's
  enumerated prompt is built from — so the list the model is shown is the list the PRD publishes.
  (The prompt text changed, so its derived `prompt_version` changed with it: the mechanism doing
  exactly what it was built for.)
- The ordinal is surfaced wherever a human reads a pattern: the graph UI chips and region captions
  (`#13 Retrieval / RAG 0.95 [rule]`) and the stage dump.

**The fences.** Three, because a prose table cannot be kept in sync by intention:

- `TestCanonicalOrdinalsArePinned` — the whole 1–20 list written out longhand by number and name, plus
  no-gaps/no-duplicates. Longhand on purpose: a test that derived the expected list from the taxonomy
  would agree with any renumbering, including a wrong one.
- `TestPRDTaxonomyTableMatchesTheCode` — parses the shipped PRD §8.3 table and asserts its row numbers
  ARE the code's ordinals, and that its ✅/⏳ column matches which detectors actually ship.
- `TestOpenSpecTaxonomyListMatchesTheCode` — the same for the spec's prose enumeration, since prose is
  precisely where the second numbering was free to grow.

All three were red-checked: renumbering one PRD row makes the fence fail naming both values.

**Group membership is unchanged** by the renumbering — control-flow 7, capability 4, coordination 2,
governance 7 — as is which eight patterns ship a structural detector. Nothing about detection,
dispatch, or the IR contract moved; `taxonomy_version` stays `1.0.0` because the vocabulary itself did
not change, only the identifiers used to refer to its members were made single-valued.
