# Agent Runtime Placement — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md`](../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md).

Covers WHERE a tenant's analysis runs — `platform`, `customer` or `disabled` — and the three things that
follow from it: one agent definition shared by both hosts, results entering through P29's existing
structure ingest rather than a second transport, and the surface saying which machine produced what it
shows.

> 🔴 **`disabled` is the default and is a real state.** A boolean would make "we deliberately turned it
> off" and "nobody has configured it" the same row, and the console distinguishes them because that is
> how an operator tells how much of a fleet anybody has actually reviewed.

## Requirements
### Requirement: Placement SHALL be a per-tenant setting with three named states
`disabled` is a real state, not the absence of `platform`. A boolean would make "we deliberately turned
it off" and "nobody has configured it" the same row.

#### Scenario: The three states
- **WHEN** an operator sets a tenant's placement
- **THEN** the value is one of `platform`, `customer`, `disabled`

#### Scenario: The default is `disabled`
- **WHEN** a tenant has never had a placement configured
- **THEN** its effective placement is `disabled`
- **AND** the console distinguishes "defaulted" from "explicitly set to `disabled`"

#### Scenario: A freshly migrated deployment analyses nothing
- **WHEN** this capability is deployed to an environment with existing tenants and no placement has been
  set for any of them
- **THEN** zero inferences run
- **AND** zero provider calls are made
- **AND** every tenant's surfaces render exactly the rule-derived facts they rendered before the deploy

#### Scenario: A disabled tenant is not analysed
- **WHEN** a tenant's placement is `disabled`
- **THEN** no platform-side inference runs for it
- **AND** any customer-side result submitted for it is refused with a stated reason
- **AND** its surfaces render rule-derived facts and report HEROS as disabled

#### Scenario: A customer-placed tenant is not analysed platform-side
- **WHEN** a tenant's placement is `customer`
- **THEN** the platform runs no inference for it
- **AND** the console states that results shown were produced on the customer's machine

### Requirement: Both placements SHALL share one agent definition
Two runners with two definitions are two agents, and they diverge in the first month.

#### Scenario: One config hash
- **WHEN** an inference is produced by either placement
- **THEN** it records the same `agent_config_hash` for the same active definition

#### Scenario: One context-assembly path
- **WHEN** either runner assembles the model input
- **THEN** it uses the same context-assembly code path

#### Scenario: Edge-set parity is asserted
- **WHEN** both placements analyse the same fixture repository at the same `agent_config_hash`
- **THEN** they produce the same set of edges
- **AND** the assertion is on the edge set, not on the narrative text

### Requirement: Customer-side results SHALL enter through the existing structure ingest
#### Scenario: A customer-side result is ingested
- **WHEN** the CLI submits a HEROS result for a workflow
- **THEN** it travels the P29 structure ingest path
- **AND** it carries provenance, confidence and the agent `config_hash`
- **AND** no second transport is introduced

#### Scenario: The confidence floor applies on ingest
- **WHEN** an ingested result contains a fact below the confidence floor
- **THEN** the fact is not written to the IR
- **AND** it is recorded as an abstention

#### Scenario: An unknown agent version is refused
- **WHEN** an ingested result names an `agent_config_hash` the platform has no version row for
- **THEN** the submission is refused naming the unknown hash
- **AND** nothing is written

### Requirement: The surface SHALL say which placement produced what it shows
#### Scenario: Placement is attributed on the graph
- **WHEN** a graph containing inferred facts is rendered
- **THEN** the surface states whether those facts were produced platform-side or on the customer's
  machine

### Requirement: Platform-side inference SHALL use the platform credential and customer-side the customer's
#### Scenario: Platform-side spends the platform's credential
- **WHEN** a platform-side inference runs
- **THEN** it resolves the credential reference on the active agent definition
- **AND** it does not read any customer-supplied provider credential

#### Scenario: The platform stores no customer provider key
- **WHEN** any placement is configured
- **THEN** no customer provider key value is accepted or stored by the platform
- **AND** this holds for `platform` placement as well, which spends the platform's own credential

#### Scenario: A customer who requires their own key has a supported path
- **WHEN** a tenant requires that inference spend their credential
- **THEN** placement `customer` is the supported answer
- **AND** the platform is not offered a way to hold that credential on their behalf
