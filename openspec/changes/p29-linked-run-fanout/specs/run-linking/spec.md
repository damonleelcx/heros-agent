# Run Linking — Delta (P29)

Product rationale: [`../../../../docs/prd/P29-linked-run-fanout.md`](../../../../docs/prd/P29-linked-run-fanout.md)
§6 (FR9–FR20). Design reasoning: [`../../design.md`](../../design.md) D2, D6.

This delta widens what the **opt-in** structure payload may carry and adds a third opt-in payload. It
changes nothing about the default run link, and it changes nothing about the construction discipline:
the payload is still built field by field from a ratified list, and a field added to an internal
representation is still absent until somebody adds it to that list on purpose.

## MODIFIED Requirements

### Requirement: The allowlist SHALL be limited to metrics, structure, hashes, scores, and run metadata

The permitted fields SHALL be limited to cost, latency and token metrics; the intermediate
representation's **structure** (node identifiers, edges, model references, pattern labels, the node's
source language, and per-node axis applicability verdicts); the configuration hash and source revision;
evaluation scores and their intervals; transform outcomes expressed as per-node verdicts and diff
statistics; and run metadata including timestamps, seeds, and tool version.

#### Scenario: Structure crosses the boundary

- **WHEN** a run is linked
- **THEN** the payload carries node identifiers, edges, model references and pattern labels
- **AND** the platform can render the workflow's shape from them.

#### Scenario: Metrics and scores cross the boundary

- **WHEN** a run is linked
- **THEN** the payload carries cost, latency and token metrics, and evaluation scores with their
  intervals
- **AND** the platform can derive spend and comparison from them.

#### Scenario: The added fields are identifiers, counts or enumerations

- **WHEN** the ratified list is inspected
- **THEN** every field added by this change is an identifier, a count, or a value from a closed set
- **AND** none of them can hold free text originating in the customer's repository.

## ADDED Requirements

### Requirement: The opt-in structure payload SHALL carry each node's source language

The structure payload SHALL carry, per node, the language of the call site as reported by the discovery
frontend that found it.

#### Scenario: A polyglot workflow reports per node

- **WHEN** a workflow's nodes are found by more than one discovery frontend
- **THEN** each node carries its own language
- **AND** no workflow-wide language is inferred from the majority.

#### Scenario: An unknown language is absent, not guessed

- **WHEN** a frontend does not report a language for a node
- **THEN** the field is absent for that node
- **AND** it is not derived from the file path or extension.

### Requirement: The opt-in structure payload SHALL carry per-node axis applicability verdicts computed locally

For each node and each optimization axis, the payload SHALL carry a verdict computed on the customer's
machine by the transform engine against the source that node lives in. A verdict SHALL be one of
`applies` or `refused`, and a refusal SHALL carry the same stable cause identifier the engine emits.

#### Scenario: The verdict comes from the engine, not from a table lookup

- **WHEN** a node's call site is refused for its own shape while its language and form are covered
- **THEN** the transmitted verdict is `refused` with the call-site cause
- **AND** it is not `applies`.

#### Scenario: A verdict is an identifier

- **WHEN** a verdict is transmitted
- **THEN** its cause is a stable identifier
- **AND** no sentence, message or prose explanation is transmitted with it.

#### Scenario: The verdicts agree with what the CLI reports locally

- **WHEN** the same repository is inspected with the local coverage command and then linked
- **THEN** the per-node verdicts transmitted are the ones the local command reports
- **AND** a divergence is a defect, not a per-surface behaviour.

### Requirement: The opt-in structure payload SHALL name the coverage table its verdicts were computed against

The payload SHALL carry the version identifier of the coverage table the verdicts were computed from.

#### Scenario: The version travels with the verdicts

- **WHEN** a structure payload is transmitted
- **THEN** it carries the coverage table version
- **AND** the platform stores it with the structure.

#### Scenario: An absent version is not defaulted

- **WHEN** a payload carries no coverage table version
- **THEN** the platform records its absence
- **AND** it does not substitute the version of the table the platform itself is running.

### Requirement: A transform receipt SHALL be transmissible as a third opt-in payload

The CLI SHALL provide an explicit, named opt-in that transmits a transform receipt: the configuration
hash, the source revision, the workflow identity, the per-node outcome of the transform with its cause,
and the diff's statistics.

#### Scenario: A receipt carries statistics, never a diff

- **WHEN** a transform receipt is transmitted
- **THEN** it carries counts of files changed and lines added and removed
- **AND** it carries no diff, no file content, and no line of source.

#### Scenario: A receipt is transmitted only on request

- **WHEN** a run is linked without the transform-receipt opt-in
- **THEN** no transform receipt is transmitted.

#### Scenario: A receipt is idempotent by configuration and revision

- **WHEN** the same transform receipt is transmitted more than once
- **THEN** the platform holds one record for that configuration and revision
- **AND** the later transmission replaces the earlier rather than appending a second.

### Requirement: The structure opt-in SHALL NOT require a separately produced artifact

The flag that opts in to transmitting a workflow's structure SHALL be usable without a previously
written intermediate representation file.

#### Scenario: The bare flag discovers in place

- **WHEN** the structure opt-in is given with no path
- **THEN** the CLI discovers the workflow's structure from the configured repository
- **AND** transmits it in the same invocation.

#### Scenario: A pre-computed artifact is still accepted

- **WHEN** the structure opt-in is given a path to a previously written representation
- **THEN** that representation is transmitted
- **AND** the behaviour is unchanged from before this requirement.

#### Scenario: Opting in remains an explicit act

- **WHEN** a run is linked with no structure opt-in
- **THEN** no structure is transmitted
- **AND** the default payload is byte-identical to what it was before this change.

### Requirement: The render-only mode SHALL render every widened payload exactly

The mode that renders what would be transmitted SHALL render the structure payload and the transform
receipt with the same fidelity it renders the run link.

#### Scenario: All three payloads are rendered

- **WHEN** the linking command is invoked in render-only mode with both opt-ins
- **THEN** the run link, the structure payload and the transform receipt are all rendered
- **AND** nothing is transmitted.

#### Scenario: What is rendered is what is sent

- **WHEN** a payload is rendered and the same invocation is then performed for real
- **THEN** the transmitted bytes match the rendered ones
- **AND** the rendering is not a summary.

### Requirement: An absent widened field SHALL be accepted and SHALL NOT be inferred

The platform SHALL accept a payload that omits any field added by this change, and SHALL record its
absence as absence.

#### Scenario: An older client is accepted

- **WHEN** a client that predates this change links a run and transmits a structure
- **THEN** the transmission is accepted
- **AND** the structure is stored with the added fields absent.

#### Scenario: An absent verdict is never rendered as a verdict

- **WHEN** a stored node carries no verdict for an axis
- **THEN** every surface renders that cell as not reported
- **AND** it is rendered neither as applying nor as refused nor as not applicable.

### Requirement: The linking command SHALL report which surfaces its transmission filled and which it did not

On success the CLI SHALL name the console surfaces the transmission populated, and for each surface it
did not populate, SHALL name the one command or option that would.

#### Scenario: A default link names what it left empty

- **WHEN** a run is linked with no opt-in
- **THEN** the output names the surfaces that now have data
- **AND** for each surface still without data, it names the option that would fill it.

#### Scenario: A fully opted-in link names no remaining gap it could close

- **WHEN** a run is linked with every opt-in this release provides
- **THEN** the output names no option the reader has not already used
- **AND** any surface still empty is reported with the reason it cannot be filled by linking.
