# Conversational Console — Spec (P39 delta)

## ADDED Requirements

### Requirement: The system SHALL resolve every intent to exactly one registered reader

A console route was only ever a proxy for "the platform can answer this". After the read-only routes are
removed the proxy is gone, so the assertion is made directly against the readers.

#### Scenario: An intent with no reader
- **WHEN** the intent table names an intent for which no reader is registered
- **THEN** the build fails, naming the intent
- **AND** the failure states that the conversation would answer that intent with a refusal indistinguishable from the surface not existing

#### Scenario: A reader with no intent
- **WHEN** a reader is registered that no intent names
- **THEN** the build fails, naming the reader

#### Scenario: A registered reader that declares no detail shape
- **WHEN** a reader is registered without declaring one of the closed detail shapes
- **THEN** the build fails, naming the reader
- **AND** no route deletion is permitted while this assertion is failing

#### Scenario: The surviving routes are still checked
- **WHEN** an intent is `route`-backed
- **THEN** its surface SHALL begin `/app/` and SHALL appear in the console's declared working-surface set
- **AND** the set equality assertion covers the surviving routes only, narrowed rather than relaxed

### Requirement: A finding SHALL be able to carry its surface's content

#### Scenario: A reader declaring a shape returns it
- **WHEN** a reader declared to return `grid` produces a reading
- **THEN** the emitted `finding` carries a `grid` detail payload
- **AND** the console renders it inline, without a link to any console route

#### Scenario: A declared shape arrives absent
- **WHEN** a reader declared to return a shape produces a reading with no detail payload
- **THEN** the emitter refuses the finding before it reaches the transport
- **AND** the step reconciles as `refused` carrying that cause

#### Scenario: A measured surface with nothing in it
- **WHEN** a reader declared to return `grid` produces a reading whose grid holds zero cells
- **THEN** the finding is emitted with a zero-cell grid
- **AND** it is NOT refused — an empty measurement is a measurement, and is rendered as one

#### Scenario: A detail payload outside the closed shape set
- **WHEN** a detail shape is added server-side and not to the generated console type union
- **THEN** the console type-check fails
- **AND** the build does not produce a console artifact

### Requirement: The system SHALL bound a detail payload and declare what it omitted

#### Scenario: A reading exceeding the cell ceiling
- **WHEN** a reader's detail payload would exceed the declared cell ceiling
- **THEN** the payload is truncated in the reader, before serialisation
- **AND** it carries the count omitted and the narrowing that would show the remainder

#### Scenario: Truncation is never silent
- **WHEN** a truncated payload is emitted without an omitted count
- **THEN** the emitter refuses it

### Requirement: The system SHALL scope a question to a single node when one is named

#### Scenario: A named node that exists
- **WHEN** a question names a node present in the workflow's reported structure
- **THEN** the reading describes that node only
- **AND** the finding states the node it describes

#### Scenario: A named node that does not exist
- **WHEN** a question names a node absent from the workflow's reported structure
- **THEN** the turn refuses, quoting the string the person typed
- **AND** the refusal lists the node identifiers that do exist
- **AND** the workflow-wide answer is NOT returned in its place

#### Scenario: No node named
- **WHEN** a question names no node
- **THEN** the reading is workflow-wide
- **AND** the finding states that it describes the whole workflow

### Requirement: The system SHALL carry a subject forward within one conversation only when it states that it did

#### Scenario: A follow-up that names an axis but no subject
- **WHEN** a turn's question resolves to an intent but no subject, and a prior turn in the same conversation resolved one
- **THEN** the prior subject is used
- **AND** the `plan` message states the subject carried and the turn it came from

#### Scenario: A follow-up with no prior turn
- **WHEN** a question depends on an earlier one and no prior turn exists in this conversation
- **THEN** the turn abstains, stating that the question depends on an earlier one
- **AND** no run is started

#### Scenario: A turn naming its own subject
- **WHEN** a turn's question names a subject and a prior turn resolved a different one
- **THEN** the turn's own subject is used
- **AND** the prior subject is NOT inherited

#### Scenario: Carry-forward does not cross conversations
- **WHEN** a question is submitted on a conversation with no prior turns, while another conversation in the same tenant has resolved subjects
- **THEN** no subject is carried

#### Scenario: Carry-forward against a differing pin
- **WHEN** a pinned inference is resolved whose subject differs from the carried one
- **THEN** the pin is not used, and the turn generates

## MODIFIED Requirements

### Requirement: The system SHALL emit only messages from the closed message-kind vocabulary

The kinds are unchanged — `plan`, `progress`, `finding`, `proposal`, `approval_request`, `result`,
`refusal`, `answer`. This phase changes what a `finding` may carry, not which kinds exist.

🔴 `proposal`, `approval_request` and `answer` remain unemitted in production and their console render
paths are retained deliberately. They are P40's, not dead code.

#### Scenario: An unknown kind cannot be emitted
- **WHEN** an emitter attempts to send a message whose kind is outside the vocabulary
- **THEN** the message is rejected before it reaches the transport
- **AND** an error event is recorded naming the offending kind

#### Scenario: A finding gaining a payload does not gain an effect
- **WHEN** the effect-bearing kind table is evaluated
- **THEN** `finding` is absent from it
- **AND** the table's three entries are unchanged by this phase

### Requirement: The system SHALL refuse a finding that carries no evidence reference

Unchanged in force. Changed in rendering: the evidence reference is an opaque identifier displayed as a
copyable value, and is no longer a link to a console route.

#### Scenario: Finding without evidence
- **WHEN** a `finding` message is constructed with an empty or absent evidence reference
- **THEN** it is refused server-side and never transmitted

#### Scenario: Evidence is not a route link
- **WHEN** a finding is rendered
- **THEN** its evidence reference is presented as a copyable identifier
- **AND** it does not navigate to any console route

## REMOVED Requirements

### Requirement: A message MAY direct the reader to the console route that owns its surface

**Reason:** the ten read-only routes no longer exist, so a message that names one sends a person to a
404 — strictly worse than naming nothing. Evidence now expands inside the message.

**Migration:** `plan.step.surface`, `finding.surface_href` and `refusal.surface_href` cease to carry
`/app/*` values for reader-backed intents. Out-of-scope redirection to surfaces that still exist
(`/app/billing`, `/app/settings/members`, `/app/studio`, `/app/authoring`) is unaffected and remains
guarded by the existing assertion that a redirection names a real surface.

#### Scenario: No emitted message references a deleted route
- **WHEN** any message is emitted for a reader-backed intent
- **THEN** no field carries a path matching a route removed by this phase
- **AND** an assertion over the removed set fails the build if one does
