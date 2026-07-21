# Tasks — P3.5: Pattern Classifier (structural)

## 1. AI Engineer + System Designer — Taxonomy & label contract
- [x] 1.1 Encode the fixed **20-pattern taxonomy** as a closed enum with its four groups
      (control-flow / capability / coordination / governance); mark which eight ship a structural
      detector in P3.5 and which are behavioral (P5).
- [x] 1.2 Define the **`pattern_labels`** record shape written to the IR:
      `{pattern ∈ taxonomy, confidence ∈ [0,1], source ∈ rule|llm, subgraph_ref, detector_id?,
      llm_run_ref?, taxonomy_version}`. Reuse the field reserved in P0 — additive, no `ir_version`
      MAJOR bump.
- [x] 1.3 Reject any label whose `pattern` is not in the enum, at write time.

## 2. System Designer — Subgraph partitioning
- [x] 2.1 Define the subgraph-partition contract: how the IR is split into regions so classification
      is **per-subgraph**, not one label for the whole workflow (proposed: maximal region matching a
      single structural signature).
- [x] 2.2 Ensure two patterns on two different subgraphs are representable simultaneously without
      conflict (`subgraph_ref` on each label).
- [x] 2.3 Define precedence when signatures overlap on a region (control-flow pattern owns the
      subgraph; capability patterns like Tool Use co-exist on the node).

## 3. AI Engineer — Structural rule detectors (deterministic, rules-first)
- [x] 3.1 **Prompt Chaining** — ≥ 2 LLM nodes in a linear data-edge chain, no fan-out/fan-in/loop.
- [x] 3.2 **Routing** — one node with **control** edges fanning conditionally to N ≥ 2 specialists.
- [x] 3.3 **Parallelization** — fan-out to ≥ 2 independent nodes reconverging at a merge node.
- [x] 3.4 **Reflection** — output loops back (self-edge / cycle) to a generate node; emit as a
      **structural candidate** with capped confidence (iteration > 1 is behavioral → P5).
- [x] 3.5 **Tool Use** — node `tools_skills` non-empty and resolvable against the Skill Registry.
- [x] 3.6 **Multi-Agent Collaboration** — manager node dispatching (control edges) to role nodes over
      shared context.
- [x] 3.7 **Retrieval/RAG** — retriever + embed + rerank chain feeding a generator.
- [x] 3.8 **Resource-Aware Optimization** — control branch selecting among model tiers on a
      cost/complexity condition.
- [x] 3.9 Each detector is a **pure function** of IR topology + node metadata: same IR → identical
      labels across runs. Calibrate per-detector confidence against the hand-labeled fixture set.
- [x] 3.10 Near-miss guards: a linear chain is not Routing; an empty-`tools_skills` node is not Tool
      Use; a fan-out with no merge is not Parallelization.

## 4. AI Engineer — Constrained LLM-as-classifier fallback
- [x] 4.1 Trigger the fallback **only** on subgraphs where no rule detector fires with sufficient
      confidence (the ambiguous residue).
- [x] 4.2 Constrain the model to the **fixed 20-pattern taxonomy** via structured output — it selects
      from the enum, cannot emit free-text or out-of-taxonomy labels — and must return a `confidence`.
- [x] 4.3 **Rules-first precedence:** the LLM SHALL NOT override a confident rule label on a subgraph.
- [x] 4.4 Reject + drop any fallback output outside the taxonomy; log it as a diagnostic.
- [x] 4.5 Record `{model, seed, temperature, prompt_version, taxonomy_version}` per fallback run,
      keyed by `config_hash`, for reproducibility and audit.

## 5. AI Engineer + System Designer — Pattern → metric-set dispatch
- [x] 5.1 Author the **pattern→metric-set mapping table** (PRD §8.4): Routing→misroute-rate/routing-
      accuracy; RAG→relevance@k/faithfulness/recall/rerank-gain; Reflection→iteration-count/
      convergence/quality-gain-per-revision; Parallelization→merge-consistency; Tool Use→tool-success/
      wrong-tool/arg-validity; Multi-Agent→inter-agent-message-validity; Prompt Chaining→handoff-
      validity/contract-adherence; Resource-Aware→cost-per-case/tier-accuracy/Pareto.
- [x] 5.2 Expose `MetricSetFor(pattern) → MetricSet` as the stable seam P4's metric-set selection
      keys off.
- [x] 5.3 Confirm the dispatch: a RAG subgraph selects retrieval metrics; a Routing subgraph selects
      misroute-rate (the M4 exit behavior).

## 6. System Designer — IR write-back
- [x] 6.1 Write `pattern_labels` back to the IR additively; a labeled and an unlabeled IR both
      validate against `workflow-ir.schema.json` at the same `ir_version` MAJOR.
- [x] 6.2 Verify pre-P3.5 consumers still parse an IR that now carries `pattern_labels`.

## 7. Frontend — Graph UI annotation
- [x] 7.1 Surface each subgraph's pattern label(s) + confidence on the graph.
- [x] 7.2 Visually distinguish a **rule**-sourced label from an **llm**-sourced one; show confidence
      prominently for llm labels.
- [x] 7.3 First-class **no-label / empty** state — an unclassified subgraph reads as "not yet
      classified", never a silent blank or misleading default.
- [x] 7.4 Verify the annotation is legible on a large composite IR.

## 8. Testing, calibration & review
- [x] 8.1 Fixtures: one IR per structural signature in isolation; **a composite IR with two patterns
      on two subgraphs** (Routing on A, RAG on B); an **ambiguous IR** that drives the LLM fallback;
      a **Reflection-loop IR** (asserts structural candidate + confidence, not confirmed).
- [x] 8.2 Determinism test: classify the same IR twice → byte-identical labels; a fully rule-covered
      IR makes **zero** LLM calls.
- [x] 8.3 Per-subgraph test: the composite fixture emits **both** labels, each against the correct
      subgraph.
- [x] 8.4 Constrained-fallback test (stubbed model): only taxonomy patterns + confidence returned;
      an out-of-taxonomy/free-text output is rejected; the LLM does not override a rule label.
- [x] 8.5 Dispatch test: `MetricSetFor` selects retrieval metrics for the RAG subgraph and
      misroute-rate for the router.
- [x] 8.6 Deferral test: a Reflection loop is emitted as a structural **candidate** with capped
      confidence and is **not** asserted as confirmed (behavioral confirmation is P5).
- [x] 8.7 IR round-trip: labeled/unlabeled IR both validate at the same `ir_version` MAJOR.
- [x] 8.8 UI verification: render the composite fixture; confirm label + confidence, rule-vs-llm
      distinction, and the empty state.
- [x] 8.9 Calibration: rule-detector confidences checked against the hand-labeled fixture set;
      agreement reported.
- [x] 8.10 Confirm the M4 exit checklist (PRD §13) is green.
