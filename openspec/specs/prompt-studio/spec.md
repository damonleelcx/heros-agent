# Prompt Studio — Spec (folded from P10, P10 M-series)

Product rationale: [`../../../docs/prd/P10-prompt-model-studio.md`](../../../docs/prd/P10-prompt-model-studio.md)
§6 (FR27–FR33). Design reasoning: [`../../changes/archive/2026-08-01-p10-prompt-model-studio/design.md`](../../changes/archive/2026-08-01-p10-prompt-model-studio/design.md) Decision 8.

Covers the console surface where a prompt version is rendered, tried against a model, and compared —
and the boundary that keeps it from becoming a second, dishonest evaluator. The studio's value is
discarding the obviously-bad **cheaply**; that requires showing output, cost and latency, and it does
not require ranking anything.

> **Inherits P9 unchanged.** One token set, English strings with pinned `en-US` formatting,
> render-as-received, no credential in the browser, loading/empty/error as three distinct renderings,
> the accessibility floor, and browser-rendered acceptance all apply here and are not restated. Prompt
> bodies are customer content and are rendered as text, never as markup.

## Requirements

### Requirement: The console SHALL render a prompt version with supplied bindings and show the exact string that would be sent

The console SHALL render a selected prompt version against supplied sample bindings and display the
resulting string. The displayed string SHALL be **byte-identical** to what a run with those bindings
would send.

#### Scenario: The preview equals what a run sends

- **WHEN** a user previews a prompt version with a set of bindings
- **THEN** the displayed string is byte-identical to the string a run with those bindings would send
- **AND** it is not an approximation, a summary, or a re-formatted rendering.

#### Scenario: A missing binding names the slot rather than rendering a hole

- **WHEN** a preview is requested without a binding for a declared slot
- **THEN** the preview fails and names the unbound slot
- **AND** no partially-rendered string is displayed.

#### Scenario: An unknown binding is reported

- **WHEN** a preview supplies a binding for a name the template does not declare
- **THEN** the preview fails and names the unknown binding
- **AND** the extra binding is not silently ignored.

### Requirement: The console SHALL execute a prompt version against a selected model and record its cost, latency and tokens

The console SHALL execute a selected prompt version against a selected model version with supplied
bindings, and SHALL display the output together with the cost, latency and token counts of that
execution.

#### Scenario: A test-run reports its cost and latency

- **WHEN** a user runs a prompt version against a model version
- **THEN** the output is displayed together with the cost, latency and token counts of that execution
- **AND** those values come from the platform's telemetry, not from a client-side estimate.

#### Scenario: A failed test-run is distinguishable from an empty result

- **WHEN** a test-run fails
- **THEN** the failure is rendered as a failure naming its cause
- **AND** it is not rendered as an empty or successful output.

### Requirement: The console SHALL support side-by-side comparison of two prompt versions or one version across two models

The console SHALL support comparing two prompt versions, or one prompt version across two model
versions, over the same bindings, presenting both outputs together with each execution's cost, latency
and tokens.

#### Scenario: Two outputs are presented together

- **WHEN** a user compares two prompt versions over the same bindings
- **THEN** both outputs are presented together with each execution's cost, latency and tokens
- **AND** the bindings used are the same for both.

### Requirement: A studio result SHALL be labelled exploratory and SHALL NOT present a score, rank, winner, or interval

Every studio test and comparison SHALL be labelled as an **unranked, exploratory** result. It SHALL NOT
display a score, a rank, a winner, a statistical significance claim, or a confidence interval, and it
SHALL NOT offer a path to promote a configuration from its result.

#### Scenario: A comparison declares no winner

- **WHEN** a side-by-side comparison completes
- **THEN** both results are displayed without any indication that one is better
- **AND** no score, rank, or confidence interval appears anywhere in the result.

#### Scenario: A studio result is labelled exploratory

- **WHEN** any studio test or comparison result is displayed
- **THEN** it carries a label identifying it as unranked and exploratory
- **AND** the label is present on the result itself, not only in surrounding documentation.

#### Scenario: No configuration can be promoted from a studio result

- **WHEN** a user acts on a studio result
- **THEN** no action promotes, ranks, or marks a configuration as better on the basis of that result
- **AND** establishing that a configuration is better requires a multi-seed evaluation and, for a
  claim, a verified delta.

### Requirement: Studio execution SHALL be metered under its own spend kind

Cost incurred by studio previews, test-runs and comparisons SHALL be recorded under a spend kind
distinct from evaluation spend.

#### Scenario: Studio spend is separated from eval spend

- **WHEN** studio executions and evaluation runs have both incurred cost
- **THEN** the spend report attributes them to distinct kinds
- **AND** studio cost does not appear within evaluation cost.

#### Scenario: Studio spend is bounded

- **WHEN** studio execution reaches its configured spend cap
- **THEN** further studio execution stops and the cap is reported
- **AND** the stop is presented as the configured behavior, not as a failure.

### Requirement: The console SHALL let a user select a model version and a prompt version per node and show the resulting config_hash before submission

The console SHALL let a user select, per node, a model version with its inference parameters and a
prompt version, construct the resulting Variant Spec, and display the resulting configuration hash
**before** the specification is submitted.

#### Scenario: The configuration hash is shown before submission

- **WHEN** a user has selected a model version and a prompt version for a node
- **THEN** the resulting configuration hash is displayed before submission
- **AND** it is the hash the platform computed, not one derived in the browser.

#### Scenario: A specification that would be rejected is reported before submission

- **WHEN** the constructed specification contains a binding failure
- **THEN** the failure is reported naming the node, dimension and slot
- **AND** it is reported before the specification is submitted for transformation.

### Requirement: The console SHALL state per node which facts are runtime-changeable and which require a new change

For each node, the console SHALL state which configured facts can be changed without generating a new
source change and which require one.

#### Scenario: The boundary is stated per node

- **WHEN** a node's configuration is displayed
- **THEN** the console states which of its configured facts are runtime-changeable and which require a
  new source change
- **AND** the statement reflects that node's apply mode.

#### Scenario: An inline node states that every change requires a new source change

- **WHEN** a node applied in inline mode is displayed
- **THEN** the console states that changing any configured fact requires a new source change
- **AND** it does not imply runtime configurability the node does not have.

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
