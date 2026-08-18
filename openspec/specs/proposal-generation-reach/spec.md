# Proposal Generation Reach — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md`](../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md).

Covers the proposal surface's honesty: the state of the last generation pass, its sentence, and the action
that follows — instead of one "Nothing is pending." over every outcome.

> `empty` is reserved for *a pass ran and found nothing*. A workflow nobody has ever analysed is
> `never_analysed`, and the two carry opposite next actions.

## Requirements
### Requirement: The proposals surface SHALL carry the generator's state rather than collapsing it to `empty`
`proposalgen` distinguishes `no_linked_runs`, `no_per_node_metrics`, `no_discovered_graph`,
`no_model_menu` and `no_bottleneck` — five answers with five different next actions. The surface
currently reads the store, finds no rows, and says `empty`.

#### Scenario: The last pass's state is rendered
- **WHEN** a generation pass has run for a workflow
- **THEN** the surface renders that pass's state and its sentence
- **AND** it renders the action that state implies

#### Scenario: `empty` means a pass found nothing
- **WHEN** a pass ran and produced no candidates because nothing is a bottleneck
- **THEN** the surface reports that, distinctly from a workflow no pass has ever run against

#### Scenario: Never analysed is its own state
- **WHEN** no generation pass has ever run for a workflow
- **THEN** the surface says so
- **AND** it does not say "Nothing is pending"

#### Scenario: A read failure is not a state about the workflow
- **WHEN** the proposal store cannot be read
- **THEN** the surface reports a read failure
- **AND** it does not report any state about the workflow's proposals

### Requirement: The last pass SHALL be recorded
#### Scenario: Timestamp and outcome are stored
- **WHEN** a generation pass completes
- **THEN** its timestamp, state and sentence are stored against the workflow
- **AND** they survive a restart

### Requirement: A generation pass SHALL be triggerable from the console
The endpoint has existed since P5.5 and nothing has ever called it.

#### Scenario: The action exists
- **WHEN** a reader with the right to act opens the proposals surface
- **THEN** an action to run a generation pass is offered
- **AND** its result updates the surface without a manual reload

#### Scenario: An unavailable generator is named
- **WHEN** the deployment does not mount the generator
- **THEN** the action reports that this deployment does not generate proposals
- **AND** it does not report that the workflow has no proposals

### Requirement: The generate action SHALL be reachable through a flat published edge path
The route is currently `POST /api/v1/workflows/{workflow_id}/proposals/generate` and the production
Ingress publishes eleven `Exact` paths, none of which match it. A `Prefix` rule under
`/api/v1/workflows/` would publish `commit`, `orderings` and `validate` alongside it.

#### Scenario: A flat path carries the identifier in the body
- **WHEN** a generation pass is requested from outside the cluster
- **THEN** it addresses a flat path with the workflow identifier in the request body
- **AND** the path is publishable as an `Exact` Ingress rule

#### Scenario: No prefix rule is added
- **WHEN** the production Ingress is rendered
- **THEN** it contains no `Prefix` rule under `/api/v1/workflows/`

#### Scenario: The fence covers the new path
- **WHEN** the edge-reach fence runs
- **THEN** the generate path is among the paths it checks
- **AND** removing the Ingress rule makes the fence fail

#### Scenario: Scope is the authenticated tenant
- **WHEN** a generation request names a workflow
- **THEN** the pass runs against the authenticated tenant's workflow
- **AND** no tenant identifier from the request body is honoured
