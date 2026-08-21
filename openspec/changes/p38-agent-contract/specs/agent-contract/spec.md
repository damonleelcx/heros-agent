# Agent Contract — Spec (P38)

Product rationale: [`../../../../../docs/prd/P38-agent-contract.md`](../../../../../docs/prd/P38-agent-contract.md) §6.
Design reasoning: [`../../design.md`](../../design.md) D1, D2, D5.

The operator surface describing the platform agent as the twenty dimensions that decide whether an agent
is reliable — not only the axes that decide what it runs.

> 🔴 **The contract is a view, never a new identity.** Every pinned inference is keyed by
> `(source_revision, agent config_hash)`. A dimension added to the hashed definition orphans every pin
> while nothing errors, the console keeps rendering, and assessments silently re-run at provider cost
> weeks later.

## ADDED Requirements

### Requirement: The surface SHALL render every dimension of the agent contract, always

No dimension is omitted because it is empty, unavailable in this deployment, or not editable. A hidden
control is indistinguishable from one that does not exist, and an operator who cannot see a mechanism
cannot ask for it to change.

#### Scenario: All dimensions render
- **WHEN** an operator with read capability opens the contract surface
- **THEN** every dimension of the contract is rendered
- **AND** each carries its current value in a form a person can read, not only as an identifier

#### Scenario: A dimension unavailable in this deployment
- **WHEN** a dimension depends on a service this deployment does not supply
- **THEN** it is rendered, naming the service it would need
- **AND** it is not omitted

#### Scenario: A dimension added in Go without a renderer
- **WHEN** a dimension is added to the contract vocabulary and not to the generated console type union
- **THEN** the console type-check fails
- **AND** the build does not produce a console artifact

### Requirement: Each dimension SHALL carry exactly one of three states

The states are `authorable`, `observable` and `fixed`.

#### Scenario: An authorable dimension carries its editor and its vocabulary
- **WHEN** a dimension is `authorable`
- **THEN** the surface renders an editor bound to that dimension's closed vocabulary
- **AND** the current value is shown beside it

#### Scenario: An observable dimension names where it is decided
- **WHEN** a dimension is `observable`
- **THEN** the surface renders its current value and names where it is decided
- **AND** it offers no control that would change it

#### Scenario: A fixed dimension names its reason and its escape hatch
- **WHEN** a dimension is `fixed`
- **THEN** the surface renders the reason it is fixed, taken from the decision that fixed it
- **AND** it renders what would change it
- **AND** a fixed dimension with an empty reason or an empty escape hatch fails the build

#### Scenario: The state vocabulary is closed
- **WHEN** a dimension is rendered with a state outside the three
- **THEN** the build fails

### Requirement: A dimension that is not part of the definition's identity SHALL NOT change the config_hash

#### Scenario: An operating-policy change preserves identity
- **WHEN** an operator changes a dimension recorded as operating policy
- **THEN** the active `config_hash` is unchanged
- **AND** every inference pinned against it still resolves
- **AND** no new agent version is created

#### Scenario: An axis change changes identity
- **WHEN** an operator changes a dimension that participates in `config_hash`
- **THEN** a new agent version is created with a new `config_hash`
- **AND** it lands pending, serving nothing

#### Scenario: The boundary is asserted in both directions
- **WHEN** the boundary list is altered so that an operating-policy dimension enters the hash
- **THEN** a fence fails
- **AND** when it is altered so that an axis leaves the hash, a fence fails

### Requirement: The surface SHALL state the consequence of a change before it is saved

#### Scenario: A change that creates a version says so
- **WHEN** an operator edits a dimension that participates in `config_hash`
- **THEN** the surface states, before the save, that this creates a new agent version and requires rehearsal
- **AND** it states how many pinned inferences would require re-inference, and at what cost

#### Scenario: A change that does not create a version says so
- **WHEN** an operator edits an operating-policy dimension
- **THEN** the surface states, before the save, that this changes operating policy and creates no version

### Requirement: No dimension SHALL be edited as free text where it has a closed vocabulary

#### Scenario: An axis is bound to its registry
- **WHEN** an operator edits an axis
- **THEN** the control is bound to that axis's vocabulary at its recorded set version
- **AND** no free-text input accepts a value belonging to that vocabulary

#### Scenario: A free-text axis input fails the build
- **WHEN** a free-text input is introduced for an axis
- **THEN** the build fails, naming the axis

#### Scenario: Params are validated at save
- **WHEN** an operator submits parameters for a selected entry
- **THEN** they are validated against that entry's declared schema at save
- **AND** a failure names the entry and the parameter

### Requirement: Guardrail and validation dimensions SHALL be rendered and SHALL NOT be weakened from this surface

#### Scenario: The mechanisms are visible
- **WHEN** an operator opens the contract surface
- **THEN** the validation, guardrail, sandbox and untrusted-source dimensions render with what they currently enforce and where each is decided

#### Scenario: No route weakens them
- **WHEN** a request attempts to disable or relax one of them through this surface
- **THEN** no route exists that accepts it
- **AND** a route added that would accept it fails a fence

### Requirement: A stopping condition that is a constant SHALL be rendered as fixed and SHALL NOT be settable

#### Scenario: The turn ceiling
- **WHEN** the stopping-conditions group is rendered
- **THEN** the turn ceiling is shown with its value and with the reason it is a constant
- **AND** no route accepts a value for it

#### Scenario: A cap states its window
- **WHEN** a token cap is rendered or edited
- **THEN** the rolling window it is measured over is stated on the control
- **AND** the cap is enforced before the provider call, never after

### Requirement: Every change to the contract SHALL carry a reason and SHALL be attributed

#### Scenario: A policy change is audited like a definition change
- **WHEN** an operator changes any dimension, hashed or not
- **THEN** an audit entry records the dimension, the previous and new value, the actor and the reason
- **AND** "it created no version" does not exempt it from the audit

#### Scenario: A change without a reason
- **WHEN** a change is submitted with no reason
- **THEN** it is refused

### Requirement: Publishing SHALL serve nothing, and activation SHALL remain a separate act

#### Scenario: A published definition is inert
- **WHEN** a definition is published from this surface
- **THEN** it lands pending and analyses no customer's source
- **AND** it serves nothing until it meets its floor on every calibration fixture individually and is activated

#### Scenario: An edit resolving to an existing version
- **WHEN** an edit resolves to a definition that already exists
- **THEN** the outcome is reported as no change
- **AND** no version is created

### Requirement: The surface SHALL distinguish what is serving from what is being viewed

#### Scenario: Serving is stated by name
- **WHEN** the contract surface renders
- **THEN** the definition currently serving inference is named from the platform's own serving field
- **AND** it is never derived from recency, from rehearsal state, or from which version the operator is viewing

### Requirement: A rehearsal result SHALL be rendered per fixture and SHALL NOT be reduced to a single figure

#### Scenario: One fixture fails among several
- **WHEN** a rehearsal passes on some fixtures and fails on others
- **THEN** the failing fixtures are named
- **AND** no bare percentage is rendered in place of the per-fixture outcomes
