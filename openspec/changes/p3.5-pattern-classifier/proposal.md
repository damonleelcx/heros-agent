## Why

By the end of P3 we can discover a workflow, override any node, run it under tracing, and swap
context policies — but every subgraph looks identical to the machinery that comes next. Without a
pattern label, P4 would have to compute *every* metric on *every* node (wasteful and often
meaningless — retrieval relevance@k on a node with no retriever) or make the user hand-label each
region. A router, a RAG pipeline, and a reflection loop fail and get optimized in completely
different ways, and the rest of the system needs to know which is which.

P3.5 adds a **Pattern Classifier** that labels each **subgraph** of the Workflow IR with the
agentic pattern(s) it implements, each with a confidence. It is a **dispatcher, not decoration**:
its output selects which metrics, failure modes, eval cases, and improvement operators are in-scope
per subgraph — the mechanism that stops P4 from evaluating a router as if it were a RAG pipeline.
Classification is **structural** over the IR topology (a cheap, reliable prior): deterministic rule
detectors fire first for the eight structurally-detectable patterns; an **LLM-as-classifier**
fallback handles only the ambiguous residue, **constrained to the fixed 20-pattern taxonomy** and
required to return a confidence — the same discipline as the diagnosis engine (rules first, LLM for
the fuzzy residue, never unverified). **Behavioral** confirmation (iteration counts, planning lists,
voting, memory read/write, HITL pauses) needs dynamic tracing and is explicitly **deferred to P5**.

Depends on P0 (`workflow-ir.schema.json` with the reserved `pattern_labels` field and typed
`data`/`control` edges), P1 (a valid IR with `tools_skills` + framework-source metadata), and P2
registries (so "bound to registry tools" is a checkable structural fact). It consumes the **static
IR only** — no runtime traces — which is exactly why it can ship now.

## What Changes

- **New capability `pattern-classifier`.** Partitions the IR into subgraphs and classifies
  **per-subgraph**, emitting a *set* of `{pattern, confidence, source, subgraph_ref}` labels — never
  one label for the whole workflow. Two patterns on two different subgraphs of one workflow coexist,
  each against its own subgraph.
- **Eight structural detectors**, deterministic over IR topology: linear data chain → **Prompt
  Chaining**; conditional control fan-out to N specialists → **Routing**; fan-out→merge →
  **Parallelization**; output loop-back to a generate node → **Reflection** (structural candidate);
  node bound to registry tools → **Tool Use**; manager→role nodes over shared context →
  **Multi-Agent Collaboration**; retriever+embed+rerank→generator → **Retrieval/RAG**;
  cost/complexity-conditioned model selection → **Resource-Aware Optimization**.
- **Fixed 20-pattern taxonomy** (control-flow / capability / coordination / governance) as the
  closed vocabulary. No classifier — rule or LLM — may emit a label outside it.
- **Constrained LLM-as-classifier fallback** for ambiguous subgraphs only: selects from the
  enumerated taxonomy, returns a confidence, and **never overrides** a confident rule label
  (rules-first precedence). A fully rule-covered IR makes **zero** LLM calls.
- **Pattern→metric-set dispatch table** delivered so P4's metric-set selection keys off the label:
  a RAG subgraph selects retrieval metrics (relevance@k / faithfulness), a Routing subgraph selects
  misroute-rate, a Reflection subgraph selects iteration-count / convergence / quality-gain.
- **Labels written back to the IR** via the reserved `pattern_labels` field — additive, no
  `ir_version` MAJOR bump, no invalidation of pre-P3.5 consumers.
- **Graph UI** surfaces each subgraph's label(s) + confidence, distinguishing rule-sourced from
  llm-sourced labels, with a first-class no-label / empty state.
- **Explicitly deferred:** **behavioral** classification (iteration counts, planning lists, voting,
  memory R/W, HITL pauses) → **P5** (needs dynamic tracing); pattern → failure-taxonomy scoping and
  pattern → eval-case targeting end-to-end → **P5**; pattern → improvement-operator gating and
  anti-pattern diagnoses → **P5.5+**. Behavioral patterns MAY be emitted as low-confidence
  structural candidates but SHALL NOT be asserted as confirmed.

## Impact

- **Affected capabilities:** `pattern-classifier` (new). Consumes the `workflow-ir` contract
  (reserved `pattern_labels`, typed edges) from P0 and writes labels back into it.
- **Affected code/systems:** new Pattern Classifier service (subgraph partitioner + rule detectors +
  constrained LLM fallback), the pattern→metric-set mapping table, the IR `pattern_labels` writer,
  the graph UI annotation layer, a hand-labeled fixture/calibration set.
- **Dependencies:** requires **P0** (reserved `pattern_labels`, typed `data`/`control` edges), **P1**
  (valid IR with `tools_skills` + framework source), **P2** (registries for tool-binding checks).
  Unblocks **P4** (metric-set selection keys off the label — the M4 exit), **P4.5** (failure taxonomy
  scoped by pattern), **P5** (behavioral confirmation extends the labels), **P5.5+** (improvement-
  operator gating + anti-pattern diagnoses).
