# Web Console — Spec Delta (P9)

Product rationale: [`../../../../../docs/prd/P9-web-console.md`](../../../../../docs/prd/P9-web-console.md)
§6 (FR9–FR17) and §5. Behaviors that must survive the port:
[`../../feature-inventory.md`](../../feature-inventory.md). UX reasoning:
[`../../ui-ux-plan.md`](../../ui-ux-plan.md).

Covers the **customer-facing** console surface — one navigable shell over the graph, configure/diff,
live-run, eval-board and (wave 9b) diagnosis and proposal-review views. It defines subject selection
without hand-typed identifiers, canonical shareable routes, the **no-feature-loss** obligation against
the inventory, the prohibition on deriving statistics client-side, legible entitlement gating, and the
rule that a surface does not ship before its backing API exists.

> **Surface boundary.** This is delivery surface #3, the **customer** dashboard, scoped to one tenant.
> It is not the P8 internal operator console: no cross-tenant read, no admin RBAC, no impersonation, no
> tenant lifecycle, no fleet control is reachable here.

## ADDED Requirements

### Requirement: The console SHALL present one navigable shell over every surface

The console SHALL present a single application shell whose navigation reaches every console surface.
A surface SHALL NOT be reachable only by knowing its URL.

#### Scenario: Every surface is reachable by navigation

- **WHEN** a signed-in user opens the console at its root
- **THEN** navigation is present that reaches each console surface
- **AND** no surface requires the user to type or edit a URL to discover it.

#### Scenario: Moving between related subjects does not require URL editing

- **WHEN** a user viewing a workflow's graph wants the run, the transform that produced it, or the
  board that scored it
- **THEN** each is reachable by navigation from the current view
- **AND** the user does not have to construct a URL to move between them.

### Requirement: The console SHALL let the user select a subject rather than type its identifier

The console SHALL let the user select a workflow, run, variant, board or transform from
platform-provided data. It SHALL NOT require a hand-typed identifier for a subject the platform can
enumerate.

#### Scenario: A subject is chosen from platform data

- **WHEN** a user needs to choose a workflow, run, variant, board or transform
- **THEN** the console offers a selection populated from platform data
- **AND** typing the identifier by hand is not the only way to reach the subject.

#### Scenario: Free-text identifier entry remains available but is not required

- **WHEN** a user already knows an identifier and wants to go straight to it
- **THEN** entering it is possible
- **AND** it is an accelerator, not the only path.

### Requirement: The console SHALL NOT substitute a default subject when none is supplied

When no subject is supplied, the console SHALL render a selection or empty state. It SHALL NOT
substitute a hardcoded or inferred default subject and render data for it.

#### Scenario: A route opened with no subject shows selection, not data

- **WHEN** a console route that displays a subject's data is opened with no subject supplied
- **THEN** a selection or empty state is rendered
- **AND** no data for any other subject is displayed.

#### Scenario: The legacy hardcoded default is not reproduced

- **WHEN** the eval-board route is opened with no workflow supplied
- **THEN** no workflow's board is rendered
- **AND** the previous behavior of defaulting to a fixed demonstration workflow identifier is absent.

### Requirement: Every legacy entry point SHALL resolve to a stable canonical route

Each entry point supported by the existing pages SHALL resolve to a stable canonical console route.
A canonical route SHALL be shareable and SHALL open exactly the subject it names.

#### Scenario: Legacy entry points resolve

- **WHEN** a user follows an entry point equivalent to the legacy `run`, `config_hash`+`source_revision`,
  `run_id`, `workflow_id`, or `workflow` parameters
- **THEN** the corresponding canonical console route opens
- **AND** it displays exactly the subject named, not a default or a list.

#### Scenario: A shared link opens the same subject for the recipient

- **WHEN** a user copies the URL of a view showing a specific run, transform, workflow or board and
  another authorized user opens it
- **THEN** the recipient sees the same subject
- **AND** the route did not depend on client-side state to identify the subject.

### Requirement: The console SHALL preserve every behavior recorded in the feature inventory

Every user-visible behavior enumerated in the P9 feature inventory SHALL be present in the console, or
SHALL be recorded in that inventory as deliberately dropped with a stated reason. A behavior SHALL NOT
be absent without appearing in one of those two states.

#### Scenario: An inventory item is present or explicitly dropped

- **WHEN** the inventory is evaluated against the console
- **THEN** each item is either demonstrably present or marked as deliberately dropped with a reason
- **AND** no item is absent while unmarked.

#### Scenario: The inventory gates legacy-page removal

- **WHEN** removal of a legacy page is proposed
- **THEN** every inventory item for that page is present or explicitly dropped, and its canonical route
  exists
- **AND** otherwise the page is not removed.

### Requirement: The live-run view SHALL stream over SSE and SHALL remain usable without it

The live-run view SHALL consume the run-monitor stream first, and SHALL fall back to polling the
snapshot when the stream is unavailable. Both paths SHALL render the same information.

#### Scenario: The stream drives the view when available

- **WHEN** the run-monitor stream is available
- **THEN** node metrics update from stream events as they arrive
- **AND** the view indicates that it is streaming.

#### Scenario: Polling takes over when the stream never delivers

- **WHEN** the stream fails before delivering any event
- **THEN** the view falls back to polling the snapshot
- **AND** it continues to render node metrics with no user action required.

#### Scenario: A closed stream is distinguished from a failed one

- **WHEN** the stream closes because the run reached a terminal state
- **THEN** the view indicates the stream closed and names the terminal status
- **AND** it does not present this as an error or start polling.

### Requirement: Run polling SHALL terminate on the run record's status, not on a node-derived condition

A view that polls a run SHALL stop polling when the **run record's** status is terminal. It SHALL NOT
infer termination from the node list, node count, or node states.

#### Scenario: Polling stops on the record's terminal status

- **WHEN** the run record reports a terminal status
- **THEN** polling stops
- **AND** the stop was decided by the record's status field.

#### Scenario: An empty or complete-looking node list does not stop polling

- **WHEN** the node list is empty, or every listed node has finished, while the run record's status is
  still non-terminal
- **THEN** polling continues
- **AND** the view renders a status-appropriate in-progress state rather than a terminal one.

### Requirement: The console SHALL render statistics as received and SHALL NOT derive them

Composite scores, confidence intervals, tie determinations, ranks, gate outcomes, Pareto dominance,
coverage percentages and pattern confidences SHALL be rendered from server-computed values. The console
SHALL NOT compute or recompute them, SHALL NOT round before comparing, and SHALL NOT client-sort by a
server-ranked field.

#### Scenario: Rendered values equal response values

- **WHEN** a board response is rendered
- **THEN** each displayed score, interval bound, rank, gate outcome and coverage percentage equals the
  corresponding value in the response
- **AND** no displayed value was computed in the client.

#### Scenario: Server ordering is preserved

- **WHEN** the leaderboard is rendered
- **THEN** rows appear in the order the server ranked them
- **AND** the console does not re-sort by score, interval, or any other server-ranked field.

#### Scenario: A tie renders as a tie

- **WHEN** the response marks variants as tied because their intervals overlap
- **THEN** the tie is rendered as a tie rather than as a strict ordering
- **AND** the console does not break the tie by comparing rounded values.

### Requirement: Judge calibration and eval-set confidence signals SHALL be surfaced wherever the affected metric appears

Where the response flags a judge as uncalibrated or below its agreement floor, or flags an eval set as
weak, low-confidence, or below coverage threshold, the console SHALL surface that flag wherever the
affected metric or score is displayed.

#### Scenario: An uncalibrated judge is flagged at every appearance of its metric

- **WHEN** a response marks a judge as uncalibrated or below floor
- **THEN** every rendering of a metric that judge produced carries the flag
- **AND** the metric is not displayed anywhere as though it were calibrated.

#### Scenario: A weak eval set is surfaced alongside the score it produced

- **WHEN** a response flags the eval set as weak, low-confidence, or below coverage threshold
- **THEN** the console surfaces that flag together with the scores derived from it
- **AND** a high score on a flagged set does not read as an unqualified result.

### Requirement: A capability outside the tenant's entitlement SHALL render as gated with the unlocking plan named

A capability the tenant's plan or automation level does not include SHALL be rendered as gated, naming
the plan that unlocks it. It SHALL NOT be hidden without explanation, and SHALL NOT be rendered as an
error or a broken state.

#### Scenario: A gated capability names its unlocking plan

- **WHEN** a tenant whose plan does not include a capability reaches it
- **THEN** the capability renders as gated and names the plan that unlocks it
- **AND** it is not silently absent and does not render as a failure.

#### Scenario: Gating reflects both plan and automation level

- **WHEN** a capability requires both a plan and an automation level and the tenant satisfies only one
- **THEN** the capability renders as gated
- **AND** the unmet condition is the one named.

#### Scenario: The screen and the enforced gate agree

- **WHEN** the console decides what to render as gated
- **THEN** it uses the same entitlement facts the platform enforces
- **AND** a capability shown as available is not refused by the platform on use.

### Requirement: The proposal-review surface SHALL be specified and SHALL NOT ship before its API exists

The console SHALL provide a proposal-review surface presenting, for each pending proposal, its
rationale, its verified delta, and its full diff, with approve and reject actions, in English. This
surface SHALL NOT be shipped until the proposal API it depends on exists.

#### Scenario: A reviewer sees evidence before deciding

- **WHEN** a reviewer opens a pending proposal
- **THEN** the rationale, the verified delta, and the full diff are presented together with the approve
  and reject actions
- **AND** neither action is reachable without the evidence being displayed.

#### Scenario: An unverified proposal is not presented as evidence

- **WHEN** a proposal has no verified delta
- **THEN** it is rendered as unverified
- **AND** it is not displayed in a way that resembles a verified result.

#### Scenario: The surface is not shipped without a backing API

- **WHEN** the proposal API does not exist
- **THEN** the proposal-review surface is not merged into a shipping build
- **AND** no console route renders a queue against a non-existent endpoint.

### Requirement: Every read-model field SHALL be rendered or recorded as deliberately unrendered

For each field the platform read models return, the console SHALL either render it or record it in the
feature inventory as deliberately unrendered with a stated reason.

#### Scenario: No field is silently unread

- **WHEN** the platform read-model field set is compared against the console's rendered set and the
  inventory's deliberately-unrendered list
- **THEN** every field appears in one of those two lists
- **AND** a field in neither fails the check.

#### Scenario: A needed value is requested upstream, not computed locally

- **WHEN** the console needs a value the platform does not return
- **THEN** the gap is raised as a read-model change against the owning phase
- **AND** the console does not compute a substitute client-side.

### Requirement: The console SHALL NOT expose operator or cross-tenant capability

No console route SHALL read or write data belonging to a tenant other than the session's tenant, and no
operator capability (admin role administration, impersonation, tenant lifecycle, billing operations,
fleet or kill-switch control) SHALL be reachable from the console.

#### Scenario: Cross-tenant data is unreachable

- **WHEN** any console route is exercised with any parameters
- **THEN** only the session tenant's data is returned
- **AND** no aggregate spanning multiple tenants is rendered.

#### Scenario: Operator capabilities are absent

- **WHEN** the console's route surface is enumerated
- **THEN** it contains no operator capability
- **AND** an admin principal has no elevated capability through this surface.
