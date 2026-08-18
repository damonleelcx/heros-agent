# HEROS Agent Definition — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md`](../../changes/archive/2026-08-12-p30-heros-platform-agent/design.md).

Covers the agent as an ordinary Variant Spec over the six authorable axes, its content-addressed
`config_hash`, and the rehearsal gate that stands between publishing one and it serving anything.

> 🔴 **Every field is a reference, never an inlined value** — and none of them can hold a provider key.
> The credential is a provider NAME resolved at use through the deployment's own secrets source.

## Requirements
### Requirement: The platform agent's definition SHALL be a Variant Spec resolved against the P2 registries
HEROS is configured through the same six-axis vocabulary the product sells, not through a parallel
settings store. Its identity is a `config_hash` computed by `internal/confighash` over the resolved
spec, so the agent that produced a stored inference is always nameable.

#### Scenario: A definition resolves and is identified by its content
- **WHEN** an operator publishes a definition naming a prompt version, a model ref, a skill set, a
  context policy, a memory strategy and a harness strategy
- **THEN** the system resolves every ref against the P2 registries
- **AND** computes a `config_hash` with `internal/confighash`
- **AND** two publications with identical resolved content produce the identical `config_hash`

#### Scenario: An unresolvable ref is refused at publish
- **WHEN** a definition names a prompt version that does not exist in the registry
- **THEN** publication fails naming the axis and the missing ref
- **AND** no version row is written

#### Scenario: Wiring is not an editable axis for HEROS
- **WHEN** an operator submits a definition carrying a wiring (P15) override
- **THEN** publication fails stating that HEROS's wiring is fixed
- **AND** the other five axes are unaffected by the refusal

### Requirement: A published definition SHALL be immutable
Editing publishes a new version. This is the P2 registry rule applied to the platform's own agent, and
it is what lets a stored inference point at the exact definition that produced it months later.

#### Scenario: Editing mints a new version
- **WHEN** an operator changes the model on the active definition and saves
- **THEN** a new version with a new `config_hash` is created
- **AND** the previous version's row is unchanged
- **AND** inferences stored under the previous `config_hash` still resolve to it

#### Scenario: No mutation API exists
- **WHEN** any caller attempts to alter a published definition's spec, model ref or credential ref
- **THEN** no such operation is offered by the store's API

### Requirement: A definition SHALL NOT become active until a rehearsal passes on every fixture
An activation gate that reads the mean would let an agent that is excellent on four languages and
connects everything it sees on the fifth reach that fifth language's customers.

#### Scenario: Rehearsal gates activation
- **WHEN** a newly published definition is submitted for activation
- **THEN** it runs against the pinned fixture repositories
- **AND** activation proceeds only if precision and recall meet the configured floor on **each**
  fixture individually
- **AND** the per-fixture report is stored on the version

#### Scenario: One failing fixture blocks activation and is named
- **WHEN** a rehearsal meets the floor on the mean but falls below it on one language's fixture
- **THEN** activation is refused
- **AND** the response names the failing fixture, its language, and its measured precision and recall

#### Scenario: A pending definition is not shown as active
- **WHEN** a definition is published but has not passed rehearsal
- **THEN** the console shows it as `pending rehearsal`
- **AND** the previously active definition remains the one used for inference

#### Scenario: At most one definition is active
- **WHEN** a definition is activated
- **THEN** any previously active definition is deactivated in the same transaction

### Requirement: The model SHALL be selected from the operator model registry
A free-text model string is a model that eventually names something the deployment cannot price or
call.

#### Scenario: Only registered models are selectable
- **WHEN** an operator opens the model field
- **THEN** the choices are the entries of the operator model registry
- **AND** a definition naming a model absent from the registry is refused at publish

#### Scenario: A deprecated model is visible as deprecated
- **WHEN** the active definition names a model the registry has since marked deprecated
- **THEN** the console shows the definition as active with a deprecation notice naming the model
- **AND** inference continues, because silently switching models would change results without a record

### Requirement: The provider credential SHALL be held as a reference and never as a value
The console has no field to type a key into. This is level 1 on the arbitration ladder and is not
traded against the convenience of a text input.

#### Scenario: The console cannot accept a key
- **WHEN** an operator configures HEROS's credential
- **THEN** the input is a selection of a provider reference resolvable by the deployment's configured
  `Secrets` source
- **AND** no request body, log line, audit record or rendered page contains a key value

#### Scenario: An unresolvable reference fails closed
- **WHEN** the configured credential reference does not resolve in the active `Secrets` source
- **THEN** HEROS reports state `credential_unresolved`
- **AND** no provider call is attempted
- **AND** surfaces fall back to rule-derived facts and say that HEROS is unavailable
- **AND** the system does not substitute another provider or another reference

#### Scenario: The reference is reported without the value
- **WHEN** an operator views the active definition
- **THEN** the credential reference name and its resolution state are shown
- **AND** the value is not shown, not partially shown, and not masked-but-present
