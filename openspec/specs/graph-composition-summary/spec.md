# Graph Composition Summary — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/p30-heros-platform-agent/design.md`](../../changes/p30-heros-platform-agent/design.md).

Covers what a workflow is MADE OF — every pattern present, the nodes it covers, the unlabelled remainder
and the provenance of each label — answered by enumerating rather than by collapsing.

> 🔴 **The composition is not a dispatcher.** The per-region labels remain the only dispatcher, because a
> graph containing both a router and a RAG pipeline needs two metric sets and a workflow-level label
> silently picks one.

## Requirements
### Requirement: The graph view SHALL report a composition of the regions present
The classifier deliberately emits per-subgraph labels and never one label for the whole workflow,
because the label is the metric-set dispatcher and a graph containing both a router and a RAG pipeline
needs two metric sets. The composition answers "what is this workflow" without breaking that.

#### Scenario: A composition is reported
- **WHEN** a workflow's graph carries one or more region labels
- **THEN** the view reports each pattern present, the number of nodes it covers, the unlabelled
  remainder, and the provenance of each label

#### Scenario: The composition is not a dispatcher
- **WHEN** the composition is computed
- **THEN** no code path reads it to select a metric set, a failure taxonomy or an improvement operator
- **AND** the per-region labels remain the only dispatcher

#### Scenario: A single-pattern workflow is still a composition
- **WHEN** every labelled region in a workflow carries the same pattern
- **THEN** the composition reports that one pattern with its node coverage and the unlabelled remainder
- **AND** it is not restated as a workflow-level label

### Requirement: A narrative SHALL be marked as assessed and SHALL be at most one paragraph
#### Scenario: The narrative is attributed
- **WHEN** HEROS contributes a composition narrative
- **THEN** it is marked `assessed`
- **AND** it is visually distinct from measured facts

#### Scenario: No narrative without an agent
- **WHEN** HEROS is disabled or unavailable
- **THEN** the composition still reports patterns, coverage and the remainder from rule-derived labels
- **AND** the narrative is absent rather than fabricated

### Requirement: A graph with no edges SHALL state that fact and its cause, and SHALL NOT draw a structure
A positional drawing of unconnected nodes implies an ordering the data does not contain.

#### Scenario: Zero edges renders a statement
- **WHEN** a workflow's IR has nodes and zero edges
- **THEN** the view states that no dependencies were mapped
- **AND** it names the cause from the discovery diagnostics — for example that the language's frontend
  is syntactic and emits no edges
- **AND** it does not render a positional node drawing

#### Scenario: The action is offered
- **WHEN** a graph has zero edges and HEROS is available for the tenant
- **THEN** the view offers the analysis action
- **AND** where HEROS is disabled it says so rather than offering an action that cannot run

### Requirement: Zero LLM calls SHALL NOT be reported as coverage
`llm_calls == 0` means the fallback did not run. Whether the rules covered everything is a different
question with a different source.

#### Scenario: Zero calls with zero labels
- **WHEN** a graph has zero region labels and zero LLM fallback calls
- **THEN** the surface reads that nothing was classified and no model was consulted
- **AND** it does not read "fully rule-covered"

#### Scenario: Zero calls with full coverage
- **WHEN** every subgraph carries a rule label and zero LLM fallback calls were made
- **THEN** the surface may report that the graph was fully rule-covered

#### Scenario: Partial coverage is neither
- **WHEN** some subgraphs are labelled and some are not, with zero LLM calls
- **THEN** the surface reports the labelled and unlabelled counts
- **AND** it makes no coverage claim

### Requirement: An unlabelled region SHALL state which of its causes applies
"Not yet classified" is currently one sentence for several different situations.

#### Scenario: The cause is named
- **WHEN** a region carries no label
- **THEN** the surface names why: no structural signature matched, the model was not consulted, the
  model was consulted and abstained, or the graph has no topology to match against
- **AND** these are distinct sentences
