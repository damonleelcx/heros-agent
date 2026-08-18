# Chain Inference — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md`](../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md).

Covers the inference itself: the residue it is shown, the closed vocabularies it is validated against,
the confidence floor below which an answer becomes an abstention, and the content-addressed pinning that
makes "the same revision always shows you the same graph" a property of the store.

> 🔴 **Rejected, never repaired.** A validator that coerced a near-miss into the nearest legal value
> would turn a detectable failure into an undetectable one.

## Requirements
### Requirement: Inference SHALL run only on the residue a frontend could not resolve
The residue is what the rules could not establish: node pairs with no edge between them and node fields
left `unresolved`. Scoping to it is what makes cost proportional to the gap and what makes "a fully
rule-covered repository costs nothing" a property of the input type rather than a review comment.

#### Scenario: A fully rule-covered repository costs nothing
- **WHEN** an inference is requested for a Go repository whose frontend emitted a complete graph with
  no `unresolved` fields
- **THEN** zero provider calls are made
- **AND** the result records that the residue was empty

#### Scenario: A syntactically-ingested repository presents its whole node set as residue
- **WHEN** an inference is requested for a TypeScript repository with 22 nodes and 0 edges
- **THEN** the residue contains the node pairs with no edge and the nodes whose model is `unresolved`
- **AND** the input carries the rule-derived IR alongside it as established context

#### Scenario: The whole repository cannot be requested
- **WHEN** a caller constructs an inference input
- **THEN** there is no field by which a full-repository pass can be requested

### Requirement: Inferred output SHALL be validated against closed vocabularies and rejected, never repaired
The input is a customer's repository and can contain text addressed to a model. The defence is that the
only thing HEROS can express is a graph over nodes that already exist.

#### Scenario: An out-of-vocabulary edge kind is rejected
- **WHEN** the model returns an edge with a `kind` outside `{data, control}`
- **THEN** that edge is rejected and recorded as an invalid output
- **AND** it is not coerced to the nearest legal kind

#### Scenario: An edge naming an unknown node is rejected
- **WHEN** the model returns an edge whose endpoint is not a node id present in the IR
- **THEN** the edge is rejected
- **AND** no node is created to satisfy it

#### Scenario: A label outside the taxonomy is rejected
- **WHEN** the model returns a pattern name not in the closed 20-pattern taxonomy
- **THEN** the label is rejected and recorded
- **AND** no new taxonomy member is created

#### Scenario: Instructions embedded in repository content do not change behaviour
- **WHEN** the source under analysis contains text instructing the analyser to report a specific graph
  or to ignore its constraints
- **THEN** the output is still validated against the closed vocabularies and the node set
- **AND** any edge it produces is subject to the confidence floor and the provenance record like any
  other

### Requirement: Every inferred fact SHALL carry a confidence, and low-confidence output SHALL become a recorded abstention
An agent that cannot say "I do not know" will say something else.

#### Scenario: Below-floor output is recorded, not discarded
- **WHEN** an inferred edge's confidence is below the configured floor
- **THEN** the edge is not written to the IR
- **AND** an abstention is stored naming the subject, a reason from the closed reason enum, and the
  confidence

#### Scenario: An unresolvable identifier produces an abstention rather than a guess
- **WHEN** the only evidence for an edge is a single unresolved identifier with no reachable definition
- **THEN** the result records an abstention
- **AND** no edge is emitted

#### Scenario: Abstentions are visible
- **WHEN** an operator views a completed inference
- **THEN** the abstentions are listed with their subjects and reasons

### Requirement: HEROS SHALL NOT overwrite or delete a rule-derived fact
Rule-derived topology is immutable to the agent. This is what makes "no currently-correct graph can be
made worse" a byte comparison rather than an argument.

#### Scenario: A frontend edge wins
- **WHEN** the model proposes an edge between two nodes where a frontend already emitted one
- **THEN** the frontend's edge is kept unchanged
- **AND** the proposal is discarded and recorded

#### Scenario: No frontend edge is removed
- **WHEN** an inference completes
- **THEN** the served IR contains every edge the frontend emitted

#### Scenario: A Go workflow is unchanged by the feature existing
- **WHEN** the served IR for a Go fixture is produced with HEROS enabled and with HEROS disabled
- **THEN** the two are byte-identical

#### Scenario: A rule label is not overridden
- **WHEN** HEROS proposes a region label for a subgraph a detector already labelled
- **THEN** the proposal enters the existing partitioner and precedence rule
- **AND** the detector's label is the one that stands

### Requirement: An inference SHALL be pinned by content and served from storage
Determinism is a property of the cache key, not of the model.

#### Scenario: The second request makes no provider call
- **WHEN** an inference is requested twice for the same `(workflow_id, source_revision, agent_config_hash)`
- **THEN** the second request returns the stored result
- **AND** zero provider calls are made
- **AND** the response body is identical to the first

#### Scenario: A changed definition is a different key
- **WHEN** the active agent definition is changed and an inference is requested for an already-analysed
  revision
- **THEN** the stored result under the previous `config_hash` remains readable
- **AND** the new request is a distinct key

#### Scenario: Re-inference is explicit and diffed
- **WHEN** an operator requests re-inference of an already-stored key
- **THEN** the new result is presented as a diff against the stored one
- **AND** the stored result is replaced only on confirmation

#### Scenario: Byte-identical model output is not claimed
- **WHEN** the reproducibility of an inference is reported
- **THEN** the stated guarantee is that the same revision yields the same stored graph
- **AND** no surface or document asserts that the model reproduces its output

#### Scenario: A duplicate concurrent request writes once
- **WHEN** two requests for the same key arrive concurrently
- **THEN** exactly one inference row exists afterwards
- **AND** both callers receive the same result

### Requirement: An inference SHALL be bounded and SHALL record its own abort
#### Scenario: A token or wall-clock budget is exceeded
- **WHEN** an inference exceeds its configured token or wall-clock budget
- **THEN** it aborts
- **AND** the abort is stored with the budget that was exceeded
- **AND** partial output is not written to the IR

#### Scenario: A provider failure is loud
- **WHEN** the provider returns an error or times out
- **THEN** the surface reports `analysis failed` with the cause
- **AND** it does not render an empty graph

### Requirement: Rendering a surface SHALL NOT trigger an inference
#### Scenario: A page load on an unanalysed workflow
- **WHEN** a workflow with no stored inference is rendered
- **THEN** the surface shows `not analysed` with the action that starts one
- **AND** zero provider calls are made

### Requirement: Provider traffic SHALL go through the platform gateway
#### Scenario: No direct HTTP client
- **WHEN** the inference package is built
- **THEN** it reaches a provider only through `internal/providergateway`
- **AND** a static check fails the build on a bare `http.Client` or `http.Transport` in the package
