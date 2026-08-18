# Prompt Studio — Matrix Surface Spec Delta (P10 M-series)

Product rationale: [`../../../../../docs/prd/P10-prompt-model-studio.md`](../../../../../docs/prd/P10-prompt-model-studio.md)
§6 (FR34–FR40) and §8.2 D9. Extends the P10 `prompt-studio` capability with its primary surface — a
node × model matrix — without weakening any P10 requirement. Everything the base capability says about
preview fidelity, the exploratory label, no-ranking, the separate spend kind, text-not-markup, and the
per-node runtime-changeable statement applies here and is not restated.

## ADDED Requirements

### Requirement: The studio SHALL present a node × model matrix as its primary surface

The studio SHALL present a matrix whose **columns are the workflow's agent nodes** and whose **rows are
models** from the registry, and it SHALL be reachable as a top-level console destination.

#### Scenario: The matrix lays nodes against models

- **WHEN** the studio is opened for a workflow
- **THEN** its agent nodes are presented as columns and the available models as rows
- **AND** each intersection is a cell for that node and that model.

#### Scenario: The studio is a primary destination

- **WHEN** the console's navigation is enumerated
- **THEN** the studio is a top-level destination
- **AND** reaching it does not require navigating several pages deep.

#### Scenario: An empty axis is a distinct state, not an error

- **WHEN** the workflow has no discovered nodes, or no models are registered
- **THEN** the matrix renders an empty-axis state naming what is missing
- **AND** it is distinguishable from a failure to load the matrix.

### Requirement: A prompt SHALL be node-scoped, and editing it SHALL create a new immutable version

A prompt SHALL belong to a node (a column). Editing a prompt from any cell in a node's column SHALL
produce a new immutable version of that node's prompt; the model (the row) SHALL NOT fork the prompt.

#### Scenario: An edit is a new version of the node's prompt

- **WHEN** a user edits a prompt from a cell and saves it
- **THEN** a new immutable version of that node's prompt is created
- **AND** the prior version remains resolvable.

#### Scenario: The prompt is shared down a column, not per cell

- **WHEN** a node's prompt is edited from the cell for one model
- **THEN** the new version is the node's prompt for every model row
- **AND** no per-cell copy of the prompt is created.

### Requirement: Each cell SHALL support variable injection, preview, test-run, and save-and-bind

Each cell SHALL let a user inject sample variable bindings, preview the exact string that would be sent
(byte-identical), test-run the node's prompt against the cell's model recording cost/latency/tokens, and
save-and-bind the node to the cell's model and prompt version.

#### Scenario: A cell preview is byte-identical

- **WHEN** a user previews a cell with supplied bindings
- **THEN** the displayed string is byte-identical to what a run with those bindings would send.

#### Scenario: A cell test-run reports cost, latency and tokens

- **WHEN** a user test-runs a cell
- **THEN** the output is displayed with the cost, latency and token counts of that execution
- **AND** those figures come from the platform's telemetry, not a client estimate.

#### Scenario: A test-run is metered under the studio spend kind and bounded

- **WHEN** a cell test-run incurs cost
- **THEN** it is recorded under the studio spend kind, distinct from eval spend
- **AND** when the studio spend cap is reached the test-run stops and the cap is reported as configured
  behaviour, not a failure.

### Requirement: Saving a cell SHALL bind the node in bound mode, marked unverified

Saving-and-binding a cell SHALL bind the node to the cell's model and prompt version via `bound` apply
mode: it writes the node's binding-document entry and the resolution is **marked unverified** and
**refusable by automation level**. It SHALL offer no promotion path and assert no ranking.

#### Scenario: A bound cell writes the binding document with actual values

- **WHEN** a user saves-and-binds a cell
- **THEN** the node's binding-document entry contains the model, its parameters, and the prompt template
  as values
- **AND** the binding is marked unverified because a studio selection carries no verified delta.

#### Scenario: Binding offers no promotion and no ranking

- **WHEN** a user binds a cell
- **THEN** no action promotes, ranks, or marks the configuration as better
- **AND** establishing that it is better still requires a multi-seed evaluation and a verified delta.

#### Scenario: An unverified binding is refused at the highest automation level

- **WHEN** the automation level requires verified configurations and a bound cell carries no verified
  delta
- **THEN** the binding is refused with the reason reported, rather than silently applied.

### Requirement: At most one cell per column SHALL be the node's in-force configuration

At most one cell per column SHALL be the node's bound (in-force) runtime configuration; the others are
exploratory. The in-force cell SHALL be rendered distinctly from a *verified* configuration.

#### Scenario: Binding a second cell replaces the first

- **WHEN** a node already has a bound cell and the user binds a different cell in the same column
- **THEN** the node's in-force configuration becomes the newly bound cell
- **AND** the column still has at most one in-force cell.

#### Scenario: In force is not verified

- **WHEN** an in-force cell is displayed
- **THEN** it is marked as in force
- **AND** that marking is visually distinct from a verified marking, because "selected" and "proven
  better" are different states.

### Requirement: The matrix SHALL display no score, rank, winner, or best-cell highlight

The matrix SHALL display no aggregate score, no per-cell rank, no winner, and no best-cell highlight.
Per-execution cost, latency and token figures SHALL be the raw figures of that execution, never a
comparative judgement.

#### Scenario: No cell is marked best

- **WHEN** several cells have been test-run
- **THEN** no cell is highlighted, scored, ranked, or marked better than another
- **AND** the only distinctions rendered are "in force" (bound) and "unverified/verified".

#### Scenario: Figures are per-execution, not comparative

- **WHEN** a tested cell shows cost, latency and tokens
- **THEN** they describe that execution alone
- **AND** the matrix computes no cross-cell comparison, ratio, or ranking from them.
