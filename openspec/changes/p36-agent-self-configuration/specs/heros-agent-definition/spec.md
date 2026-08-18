# HEROS Agent Definition — Delta (P36)

The platform's own agent stops being one node. Its definition grows a node list, an ordering and a
topology, and two of its axes — `loop` and `graph` — arrive from
[P34](../../../p34-harness-loop-graph-split/).

The wiring refusal in the folded requirement below is **narrowed, not deleted**. Its stated reason —
*"there is no second node to order it against"* — remains true for a single-node definition, which stays
the default.

## MODIFIED Requirements

### Requirement: The platform agent's definition SHALL be a Variant Spec resolved against the P2 registries

HEROS is configured through the same axis vocabulary the product sells, not through a parallel settings
store. Its identity is a `config_hash` computed by `internal/confighash` over the resolved spec, so the
agent that produced a stored inference is always nameable.

The vocabulary is now **nine** axes rather than six, and a definition describes **one or more nodes**
rather than exactly one.

#### Scenario: A definition resolves and is identified by its content
- **WHEN** an operator publishes a definition naming, for each of its nodes, a prompt version, a model ref, a skill set, a context policy, a memory strategy, a harness envelope and a loop strategy
- **THEN** the system resolves every ref against the P2 registries
- **AND** computes a `config_hash` with `internal/confighash`
- **AND** two publications with identical resolved content produce the identical `config_hash`

#### Scenario: An unresolvable ref is refused at publish
- **WHEN** a definition names a prompt version that does not exist in the registry
- **THEN** publication fails naming the axis, the node, and the missing ref
- **AND** no version row is written

#### Scenario: Wiring is not an editable axis for a single-node definition
- **WHEN** an operator submits a definition declaring one node and carrying an ordering
- **THEN** publication fails stating that there is no second node to order it against
- **AND** the other axes are unaffected by the refusal

#### Scenario: Wiring is an editable axis for a multi-node definition
- **WHEN** an operator submits a definition declaring more than one node with an ordering
- **THEN** the ordering is validated rather than refused

#### Scenario: A single-node definition keeps its identity across this change
- **WHEN** a definition declares one node, no loop ref and no graph declaration
- **THEN** it serialises byte-identically to its pre-P36 form
- **AND** it produces the same `config_hash`
- **AND** inferences pinned under that hash remain reachable

## ADDED Requirements

### Requirement: A definition SHALL declare its nodes as data rather than inheriting a fixed node identity

#### Scenario: Nodes are declared
- **WHEN** a definition is authored
- **THEN** it declares one or more nodes, each with its own node id and its own axis bindings

#### Scenario: Single node remains the default
- **WHEN** an operator authors a definition without declaring additional nodes
- **THEN** it is a valid single-node definition and no topology is required

### Requirement: A pinned inference SHALL remain readable and attributable across a definition shape change

#### Scenario: Existing pins survive
- **WHEN** the definition shape changes
- **THEN** inferences pinned under the previous shape remain readable
- **AND** each names the `config_hash` of the definition that produced it

#### Scenario: A configuration change does not re-run pins
- **WHEN** a new definition is activated
- **THEN** pinned inferences are not silently re-run
- **AND** re-inference is an explicit act whose output is presented as a diff

#### Scenario: A pin from a shape no longer authorable
- **WHEN** a stored inference refers to a definition shape that can no longer be authored
- **THEN** it renders as stale with its producing configuration named
- **AND** it renders as neither absent nor current

### Requirement: An inference SHALL record the node that produced it

#### Scenario: Node-level attribution
- **WHEN** an inference is recorded
- **THEN** it names the node and the definition version that produced it
- **AND** an operator can resolve a customer-visible finding to that node

### Requirement: The agent SHALL NOT be the subject of its own proposals

#### Scenario: No self-authored change
- **WHEN** the agent produces a proposal
- **THEN** its subject is a customer workflow
- **AND** no proposal targets the agent's own definition

#### Scenario: The operator remains the author
- **WHEN** the agent's definition changes
- **THEN** the change was authored by an operator and recorded against them
