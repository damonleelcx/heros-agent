## ADDED Requirements

### Requirement: The Workflow IR SHALL be a versioned JSON graph of nodes and edges

The IR is the canonical output of Discovery and the input to the Config Layer, Pattern Classifier, and
graph UI. It SHALL be a JSON document carrying an explicit `ir_version` (semver) at its root, a
`workflow` object identifying the source repo (`url`, `commit_sha`) and language, a `nodes` array, and
an `edges` array. Cross-reference: `docs/prd/P0-foundations.md` §6 (FR1) and §8.3.

#### Scenario: A valid IR document declares its version and graph
- **WHEN** an IR document is emitted with `ir_version`, a `workflow` object, a `nodes` array, and an
  `edges` array
- **THEN** it validates against `workflow-ir.schema.json`
- **AND** a document missing `ir_version` is rejected as invalid.

#### Scenario: The IR is diffable across two Discovery runs of the same commit
- **WHEN** Discovery runs twice over the same repo `commit_sha` with no source change
- **THEN** the two emitted IR documents are byte-stable after canonicalization (same nodes, same
  `node_id`s, same edges), so a diff shows no change.

### Requirement: Each IR node SHALL carry call-site, model, prompt, tools, and context-assembly metadata

Every node SHALL include: `node_id` (stable, derived from the call site); `call_site` (`file`,
`symbol`, `line_start`, `line_end`); `model` (`provider`, `model_id`, inference `params`); `prompt`
(a template reference or inline text plus declared `variables`); `tools_skills` (the bound tool/skill
names); and `context_assembly` (the policy name + a description of how context is built).

#### Scenario: A node exposes every override dimension the Config Layer will target
- **WHEN** an IR node is emitted for a discovered LLM call site
- **THEN** it contains `node_id`, `call_site`, `model`, `prompt`, `tools_skills`, and
  `context_assembly`
- **AND** an IR node missing any of these required fields is rejected as invalid.

### Requirement: The IR SHALL distinguish static node definitions from runtime invocations

A **node** in the IR is a **static definition** (`kind: "static_definition"`). A **runtime
invocation** is a distinct concept that references a definition by `node_id` and carries an
`invocation_id`, a `run_id`, and an `invocation_index`. A single definition MAY correspond to many
runtime invocations.

#### Scenario: A looping agent is one definition, many invocations
- **WHEN** an agent node executes its LLM call 7 times in one run
- **THEN** the IR contains exactly **one** static definition for that node
- **AND** the runtime produces 7 runtime-invocation records, each referencing that definition's
  `node_id` with `invocation_index` 0..6.

### Requirement: Node count SHALL be reported per static definition, with variable runtime fan-out flagged

The IR's node count SHALL be the count of static definitions. A definition whose number of runtime LLM
calls is not statically fixed (loops, conditional agents) SHALL set
`invocation_semantics.variable_at_runtime = true` and SHALL NOT be expanded into multiple nodes.

#### Scenario: A repo with 20 call sites (one a loop) reports 20 nodes
- **WHEN** Discovery finds 20 static LLM call sites, one of which is an agent loop
- **THEN** the reported node count is **20**
- **AND** the loop node has `invocation_semantics.variable_at_runtime = true`
- **AND** the loop node is not represented as more than one node in the graph.

### Requirement: Each IR node SHALL carry a first-class typed I/O contract

Every node SHALL include a required `io_contract` object with `input_schema` and `output_schema`, each
a JSON Schema (draft 2020-12). The field is mandatory from IR v1 even though re-arrangement consumes it
only in P5. Early schemas MAY be permissive (e.g. `{"type": "object"}`) when static analysis cannot
fully infer the shape; precision may be refined additively without a schema-version change.

#### Scenario: The I/O contract is present on every node
- **WHEN** any IR node is emitted
- **THEN** it contains `io_contract.input_schema` and `io_contract.output_schema`
- **AND** a node missing `io_contract` is rejected as invalid — even in IR v1, before re-arrangement
  ships.

#### Scenario: A permissive early schema is still valid
- **WHEN** Discovery cannot statically infer a node's exact input shape and emits
  `io_contract.input_schema = {"type": "object"}`
- **THEN** the node validates
- **AND** later refinement to a stricter schema does not require an `ir_version` MAJOR bump.

### Requirement: Edges SHALL be typed as data or control flow and reference nodes by node_id

Each edge SHALL carry `from_node_id`, `to_node_id`, and `kind` ∈ {`data`, `control`}, where both
endpoints reference existing node `node_id`s.

#### Scenario: An edge with a dangling endpoint is rejected
- **WHEN** an edge references a `to_node_id` that is not present in the `nodes` array
- **THEN** the IR is rejected as invalid.

#### Scenario: Data and control flow are distinguishable
- **WHEN** node A's output feeds node B (data) and a router C conditionally activates B (control)
- **THEN** the A→B edge has `kind: "data"` and the C→B edge has `kind: "control"`.

### Requirement: The IR SHALL reserve an optional pattern_labels field for the Pattern Classifier

Nodes and/or subgraphs SHALL accept an optional `pattern_labels` array (each entry: a pattern name from
the fixed 20-pattern taxonomy plus a confidence). It is unset in P0/P1 and populated by the P3.5
classifier. Its absence SHALL NOT invalidate an IR.

#### Scenario: An IR without pattern labels is valid
- **WHEN** an IR is emitted in P1 with no `pattern_labels`
- **THEN** it validates
- **AND** when the P3.5 classifier later adds `pattern_labels`, the document still validates at the
  same `ir_version` MAJOR (additive field).

### Requirement: The IR schema SHALL evolve additively under semantic versioning

`ir_version` SHALL be semver. Adding an optional field SHALL NOT change the MAJOR version; removing or
renaming a field, or making an optional field required, SHALL bump the MAJOR version. A consumer
written against MAJOR *n* SHALL continue to validate documents that only add optional fields at
MAJOR *n*. (NFR1.)

#### Scenario: Adding an optional field does not break existing consumers
- **WHEN** a new optional node field is added and MINOR is bumped
- **THEN** an IR sample authored against the previous MINOR still validates
- **AND** a consumer pinned to the MAJOR still parses the new document.

### Requirement: A hand-written IR sample SHALL validate in CI and an invalid sample SHALL fail

The repo SHALL contain a valid IR sample fixture and at least one invalid fixture (e.g. missing
`io_contract` or a required node field); CI SHALL validate the former and assert the latter fails.
(NFR8, M0 exit.)

#### Scenario: CI enforces the schema as a gate
- **WHEN** CI runs the schema-validation job
- **THEN** the valid IR sample passes validation
- **AND** the invalid sample fails validation, failing the build.
