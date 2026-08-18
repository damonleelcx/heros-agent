# Run Linking — Spec (folded from P11, P29)

Product rationale: [`../../../docs/prd/P11-cli-ci-integration.md`](../../../docs/prd/P11-cli-ci-integration.md)
§6 (FR9–FR19), §7 and §8.1. Design reasoning: [`../../changes/archive/2026-07-25-p11-cli-ci-integration/design.md`](../../changes/archive/2026-07-25-p11-cli-ci-integration/design.md) Decisions 2, 3, 4, 6, 7.

Covers the egress boundary — the only path by which anything from a customer's environment reaches the
platform. Linking is explicit, authenticated, and disclosed before it happens; the payload is
**constructed from an allowlist**; content never crosses while structure does; metering counts **only
what it observed** and reports its own coverage.

> **Why the boundary is designed this way.** This runs over proprietary source with the customer's own
> provider keys. A denylist — serialize the run object, strip the sensitive fields — fails **silently**
> the first time someone adds a field and forgets the stripper, and the discovery happens externally.
> An allowlist fails toward omission: a new field is simply absent, and the discovery is a missing
> feature. Only one of those directions is acceptable for a boundary carrying customer source.

## Requirements

### Requirement: Transmitting run data SHALL require an explicit command and an authenticated identity

Run data SHALL be transmitted to the platform only by a command whose purpose is to transmit it, and
only under an authenticated identity. No other command SHALL transmit run data.

#### Scenario: Only the linking command transmits

- **WHEN** any command other than the linking command is executed
- **THEN** no run data is transmitted to the platform.

#### Scenario: Linking without an authenticated identity does not transmit

- **WHEN** the linking command is invoked without an authenticated identity
- **THEN** nothing is transmitted
- **AND** the command reports that authentication is required.

#### Scenario: There is no ambient or background transmission

- **WHEN** the CLI runs any command, including on first use
- **THEN** it performs no background or usage reporting
- **AND** the only outbound run data is that sent by an explicit linking command.

### Requirement: The CLI SHALL render the exact payload without transmitting it

The CLI SHALL provide a mode that renders the exact payload that the linking command would transmit,
without transmitting it.

#### Scenario: A reviewer can inspect the payload before any transmission occurs

- **WHEN** the linking command is invoked in render-only mode
- **THEN** the exact payload that would be transmitted is rendered
- **AND** nothing is transmitted.

#### Scenario: The rendered payload equals what is sent

- **WHEN** a payload is rendered in render-only mode and the same run is then linked
- **THEN** the transmitted payload is identical to the rendered one
- **AND** the rendering is not an approximation or a summary.

### Requirement: The payload SHALL be constructed from an allowlist, not filtered from a larger object

The transmitted payload SHALL be constructed field by field from an explicit list of permitted fields.
It SHALL NOT be produced by serializing a larger object and removing fields from it.

#### Scenario: A newly added field is absent by default

- **WHEN** a field is added to the internal run representation and the allowlist is not changed
- **THEN** that field does not appear in a transmitted payload
- **AND** its absence is the default outcome rather than the result of an exclusion rule.

#### Scenario: Only allowlisted fields appear

- **WHEN** a transmitted payload is compared against the allowlist
- **THEN** every field in the payload appears in the allowlist
- **AND** no field outside the allowlist is present.

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

### Requirement: Content SHALL NOT cross the boundary on any path

Prompt text, source code, file contents, generated diffs, environment variable values, and provider
credentials SHALL NOT be transmitted. This SHALL hold on every path, including error reporting,
diagnostics, and elevated verbosity.

#### Scenario: No content in a linked payload

- **WHEN** a run whose prompts, source and diff are all present locally is linked
- **THEN** none of that content appears in the transmitted payload.

#### Scenario: Elevated verbosity does not widen the boundary

- **WHEN** the linking command is run at the highest verbosity or with diagnostics enabled
- **THEN** the transmitted payload still contains only allowlisted fields
- **AND** no additional field becomes transmissible because of the verbosity setting.

#### Scenario: Error reporting carries no content

- **WHEN** an error occurs during linking and is reported to the platform
- **THEN** the report contains no prompt text, source, file content, diff, environment value, or
  credential.

### Requirement: Linking SHALL be idempotent

Linking the same run more than once SHALL NOT cause its metrics to be counted more than once.

#### Scenario: Re-linking does not double-count

- **WHEN** a run that has already been linked is linked again
- **THEN** its metrics contribute to derived figures exactly once
- **AND** no meter is incremented a second time.

#### Scenario: A retried transmission is safe

- **WHEN** a linking attempt fails partway and is retried
- **THEN** the resulting contribution is the same as a single successful attempt.

### Requirement: Linked events SHALL enter the existing telemetry substrate with the standard tag set

Linked events SHALL be recorded in the platform's existing telemetry substrate carrying the standard
tag set. A separate collection pipeline, cost model, or store SHALL NOT be introduced for linked runs.

#### Scenario: Linked events are indistinguishable in kind from platform-executed ones

- **WHEN** a linked run's events are recorded
- **THEN** they carry the standard tag set and reside in the existing substrate
- **AND** figures derived from them use the same derivation as any other event.

#### Scenario: No second pipeline exists

- **WHEN** the platform's ingestion paths are enumerated
- **THEN** linked runs use the existing substrate
- **AND** no parallel collection path or second cost model exists for them.

### Requirement: Spend under management SHALL be derived only from linked runs, with no inference of unlinked spend

Spend under management SHALL be derived only from runs that were linked. The platform SHALL NOT infer,
extrapolate, or estimate spend for runs that were not linked.

#### Scenario: Unlinked activity contributes nothing

- **WHEN** a customer executes runs locally and links only some of them
- **THEN** only the linked runs contribute to the derived spend figure
- **AND** no estimate is added for the unlinked ones.

#### Scenario: No extrapolation path exists

- **WHEN** the derivation of the spend figure is examined
- **THEN** it contains no step that estimates or projects unobserved spend
- **AND** the figure is a sum of observed events only.

### Requirement: Link coverage SHALL be reported wherever a derived spend figure is displayed

The platform SHALL expose how much of a customer's activity is linked, and SHALL display that coverage
wherever a spend figure derived from linked runs is shown.

#### Scenario: A spend figure is shown with its coverage

- **WHEN** a spend figure derived from linked runs is displayed
- **THEN** the coverage of that figure is displayed with it
- **AND** the figure is not presented as though it reflected all activity.

#### Scenario: Full coverage is stated rather than assumed

- **WHEN** every reported run has been linked
- **THEN** coverage is displayed as complete
- **AND** the display distinguishes complete coverage from unknown coverage.

### Requirement: A successful link SHALL yield a route to that run in the console

On a successful link the CLI SHALL emit a reference that opens that run in the web console.

#### Scenario: The link command prints a route to the run

- **WHEN** a run is linked successfully
- **THEN** the CLI emits a reference that opens that specific run in the console
- **AND** the reference resolves to that run rather than to a list or a default.

### Requirement: A failed link SHALL NOT invalidate the local result

A failure to link SHALL NOT cause the underlying command to fail, and SHALL NOT discard or invalidate
the local result. The failure SHALL be reported distinguishably from a failure of the run itself.

#### Scenario: The local result survives a link failure

- **WHEN** a run completes locally and the subsequent link fails
- **THEN** the local result remains valid and available
- **AND** the command does not report the run as failed.

#### Scenario: A link failure is distinguishable from a run failure

- **WHEN** a link fails
- **THEN** the reported condition identifies the transmission as the failure
- **AND** it is not conflated with a failure of discovery, apply, or eval.

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
