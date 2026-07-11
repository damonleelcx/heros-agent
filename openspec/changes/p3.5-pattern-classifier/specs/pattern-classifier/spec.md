# Pattern Classifier — P3.5 Delta

Cross-reference: [`../../../../docs/prd/P3.5-pattern-classifier.md`](../../../../docs/prd/P3.5-pattern-classifier.md).

## ADDED Requirements

### Requirement: The Pattern Classifier SHALL classify per-subgraph, emitting a set of labels rather than one label for the whole workflow

The classifier SHALL partition the Workflow IR into subgraphs and emit a *set* of labels, each of the
form `{pattern, confidence, source, subgraph_ref}` and each tied to the specific subgraph it applies
to. It SHALL NOT collapse a multi-pattern workflow to a single whole-workflow label.

#### Scenario: A composite workflow gets one label per subgraph, not one for the whole

- **WHEN** an IR contains a conditional-fan Routing region in subgraph A and a
  retriever+embed+rerank→generator region in subgraph B
- **THEN** the classifier emits a **Routing** label with `subgraph_ref = A`
- **AND** emits a **Retrieval/RAG** label with `subgraph_ref = B`
- **AND** the two labels coexist without conflict — neither is a single label spanning the whole
  workflow.

#### Scenario: Each emitted label names its subgraph

- **WHEN** any label is emitted
- **THEN** it carries a `subgraph_ref` identifying the region it applies to
- **AND** a label with no `subgraph_ref` is invalid.

### Requirement: The Pattern Classifier SHALL draw every label only from the fixed 20-pattern taxonomy

The taxonomy is a closed vocabulary of 20 patterns across four groups — control-flow (Prompt
Chaining, Routing, Parallelization, Reflection, Planning, Prioritization, Exploration & Discovery),
capability (Tool Use, Retrieval/RAG, Memory Management, Reasoning Techniques), coordination
(Multi-Agent Collaboration, Inter-Agent Communication), and governance (Goal Setting & Monitoring,
Exception Handling & Recovery, Human-in-the-Loop, Evaluation & Monitoring, Guardrails & Safety,
Resource-Aware Optimization, Learning & Adaptation). No label — from the rule layer or the LLM
fallback — SHALL name a pattern outside this set.

#### Scenario: A pattern name outside the taxonomy is rejected

- **WHEN** any layer attempts to emit a label whose `pattern` is not one of the 20 taxonomy patterns
- **THEN** the label is rejected and not written to the IR
- **AND** the rejection is recorded as a diagnostic.

#### Scenario: Each label pins the taxonomy version it was drawn from

- **WHEN** a label is emitted
- **THEN** it carries a `taxonomy_version` so the label stays interpretable if the taxonomy is later
  extended.

### Requirement: The Pattern Classifier SHALL carry a confidence in [0,1] and a source on every label

Every label SHALL carry a `confidence` in the closed interval `[0,1]` and a `source ∈ {rule, llm}`
identifying which layer produced it.

#### Scenario: A rule-detected label reports rule source and confidence

- **WHEN** a structural rule detector fires on a subgraph
- **THEN** the emitted label has `source = rule` and a `confidence` in `[0,1]` calibrated for that
  detector.

#### Scenario: A label without a confidence is invalid

- **WHEN** a label is emitted without a `confidence` field
- **THEN** it is rejected as invalid.

### Requirement: The Pattern Classifier SHALL provide deterministic structural detectors for the eight structurally-detectable patterns

The classifier SHALL provide rule-based structural detectors for Prompt Chaining, Routing,
Parallelization, Reflection, Tool Use, Multi-Agent Collaboration, Retrieval/RAG, and Resource-Aware
Optimization. Each detector SHALL be a pure function of IR topology + node metadata and SHALL produce
identical labels on identical IR input across runs.

#### Scenario: A linear data chain is labeled Prompt Chaining

- **WHEN** a subgraph is ≥ 2 LLM nodes connected by data edges in a line, with no fan-out, fan-in, or
  loop
- **THEN** the classifier labels that subgraph **Prompt Chaining**.

#### Scenario: A conditional control fan-out to N specialists is labeled Routing

- **WHEN** a node has control edges that conditionally activate N ≥ 2 specialist nodes
- **THEN** the classifier labels that subgraph **Routing**.

#### Scenario: A fan-out that reconverges at a merge is labeled Parallelization

- **WHEN** a node fans out to ≥ 2 independent nodes that reconverge at a downstream merge node
- **THEN** the classifier labels that subgraph **Parallelization**.

#### Scenario: A node bound to registry tools is labeled Tool Use

- **WHEN** a node's `tools_skills` is non-empty and resolves against the Skill Registry
- **THEN** the classifier labels that node **Tool Use**.

#### Scenario: A retriever+embed+rerank→generator chain is labeled Retrieval/RAG

- **WHEN** a subgraph chains a retriever, an embed step, and a rerank step into a generator node
- **THEN** the classifier labels that subgraph **Retrieval/RAG**.

#### Scenario: A manager dispatching to role nodes over shared context is labeled Multi-Agent Collaboration

- **WHEN** a manager node dispatches via control edges to role nodes that share a common context
- **THEN** the classifier labels that subgraph **Multi-Agent Collaboration**.

#### Scenario: A cost/complexity-conditioned model selection is labeled Resource-Aware Optimization

- **WHEN** a control branch selects among model tiers on a cost or complexity condition
- **THEN** the classifier labels that subgraph **Resource-Aware Optimization**.

#### Scenario: Classification of the same IR is deterministic

- **WHEN** the same IR is classified twice
- **THEN** the two emitted label sets are byte-identical
- **AND** a subgraph a rule detector covers is never sent to the LLM fallback.

#### Scenario: A near-miss topology does not misfire

- **WHEN** a subgraph is a linear data chain (no conditional fan-out)
- **THEN** it is labeled Prompt Chaining and is **not** labeled Routing
- **AND** a node with an empty `tools_skills` is **not** labeled Tool Use.

### Requirement: The Pattern Classifier SHALL fall back to an LLM-as-classifier constrained to the taxonomy, returning confidence, only on ambiguous subgraphs

When no structural rule detector fires with sufficient confidence on a subgraph, the classifier SHALL
invoke an LLM-as-classifier fallback that selects from the fixed 20-pattern taxonomy (structured
output, never free-text) and returns a confidence per label. Rule detectors SHALL take precedence:
the LLM fallback SHALL NOT override a confident rule label.

#### Scenario: The LLM fallback is constrained to the taxonomy and returns a confidence

- **WHEN** a subgraph matches no structural signature and the LLM fallback classifies it
- **THEN** the returned label's `pattern` is one of the 20 taxonomy patterns
- **AND** the label carries `source = llm` and a `confidence` in `[0,1]`
- **AND** an attempt by the model to emit a free-text or out-of-taxonomy label is rejected and dropped.

#### Scenario: The LLM fallback does not run on a rule-covered subgraph

- **WHEN** a subgraph is confidently labeled by a structural rule detector
- **THEN** the LLM fallback is not invoked for that subgraph
- **AND** the rule label is not overridden.

#### Scenario: A fully rule-covered workflow makes zero LLM calls

- **WHEN** every subgraph of an IR is covered by a confident structural rule detector
- **THEN** classification completes with zero LLM calls.

#### Scenario: The LLM fallback run is reproducible

- **WHEN** the LLM fallback classifies a subgraph
- **THEN** its `{model, seed, temperature, prompt_version, taxonomy_version}` are recorded and keyed
  by `config_hash`, so the same IR + classifier config reproduces the same classification.

### Requirement: The Pattern Classifier SHALL dispatch a pattern to its metric-set so downstream metric selection keys off the label

The classifier SHALL deliver a pattern→metric-set mapping such that P4's metric-set selection keys
off a subgraph's `pattern_labels` — a subgraph's pattern selects the metrics computed on it, so the
harness measures each region correctly instead of computing every metric everywhere.

#### Scenario: A RAG subgraph selects retrieval metrics

- **WHEN** a subgraph is labeled **Retrieval/RAG**
- **THEN** `MetricSetFor(Retrieval/RAG)` selects retrieval metrics (relevance@k, faithfulness/
  groundedness, recall, rerank gain)
- **AND** it does not select router metrics.

#### Scenario: A Routing subgraph selects misroute-rate

- **WHEN** a subgraph is labeled **Routing**
- **THEN** `MetricSetFor(Routing)` selects routing metrics including **misroute-rate** and
  routing-accuracy
- **AND** it does not select retrieval relevance@k.

#### Scenario: A Reflection subgraph selects iteration/convergence metrics

- **WHEN** a subgraph is labeled **Reflection**
- **THEN** `MetricSetFor(Reflection)` selects iteration-count, convergence, and
  quality-gain-per-revision.

### Requirement: The Pattern Classifier SHALL treat behavioral patterns as structural candidates at most and defer their confirmation to P5

Patterns requiring runtime evidence to confirm — iteration count > 1 (Reflection), a consumed
planning list (Planning), sample-N-then-vote (Reasoning Techniques), memory read/write between turns
(Memory Management), and a human-approval pause (Human-in-the-Loop) — SHALL NOT be asserted as
confirmed from structure alone. Where structure shows a candidate, the classifier MAY emit the label
as a structural candidate with a capped confidence reflecting the missing behavioral confirmation;
full confirmation is deferred to P5 dynamic tracing.

#### Scenario: A loop-back edge is a Reflection candidate, not a confirmed iterating loop

- **WHEN** a node's output loops back to a generate node (a self-edge / cycle) but no runtime trace
  is available
- **THEN** the classifier emits **Reflection** as a structural candidate with a capped confidence
- **AND** it does NOT assert that the loop iterates more than once
- **AND** the metric-set and any confirmation await P5 behavioral classification.

#### Scenario: Purely behavioral patterns are not asserted from structure

- **WHEN** an IR has no structural signature for Planning, Memory Management, or Human-in-the-Loop
- **THEN** the classifier does not emit a confirmed label for those patterns from structure alone.

### Requirement: The Pattern Classifier SHALL write labels back to the IR additively without invalidating it

The classifier SHALL populate the reserved `pattern_labels` field on nodes and/or subgraphs. Writing
labels SHALL be additive: an IR with `pattern_labels` and an IR without SHALL both validate against
`workflow-ir.schema.json` at the same `ir_version` MAJOR, and pre-P3.5 consumers SHALL continue to
parse a labeled IR.

#### Scenario: A labeled IR still validates at the same schema major

- **WHEN** the classifier writes `pattern_labels` onto an IR that previously had none
- **THEN** the labeled IR validates against `workflow-ir.schema.json`
- **AND** it validates at the same `ir_version` MAJOR as the unlabeled IR
- **AND** a consumer written before P3.5 still parses it.
