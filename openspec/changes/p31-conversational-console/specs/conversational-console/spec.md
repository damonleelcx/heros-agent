# Conversational Console — Spec (P31)

## ADDED Requirements

### Requirement: The system SHALL accept a natural-language question scoped to one tenant and one run

A conversation is a view over a run. The run carries the ownership and the evidence; the conversation
carries the sequence of messages about it.

#### Scenario: A question starts a run
- **WHEN** an authenticated person submits a question on a conversation bound to a workflow their tenant owns
- **THEN** a run is created, owned by that tenant, and the conversation is bound to it
- **AND** the first message emitted is a `plan` describing the ordered steps the agent intends

#### Scenario: A question for another tenant's workflow
- **WHEN** a submitted question names a workflow the session's tenant does not own
- **THEN** the request is refused, and the refusal does not disclose whether the workflow exists

### Requirement: The system SHALL emit only messages from the closed message-kind vocabulary

The kinds are `plan`, `progress`, `finding`, `proposal`, `approval_request`, `result`, `refusal`, `answer`.

#### Scenario: An unknown kind cannot be emitted
- **WHEN** an emitter attempts to send a message whose kind is outside the vocabulary
- **THEN** the message is rejected before it reaches the transport
- **AND** an error event is recorded naming the offending kind

#### Scenario: A new kind added server-side without a client union member
- **WHEN** a message kind is added to the Go vocabulary and not to the generated console type union
- **THEN** the console type-check fails
- **AND** the build does not produce a console artifact

### Requirement: The system SHALL refuse a finding that carries no evidence reference

#### Scenario: Finding without evidence
- **WHEN** a `finding` message is constructed with an empty or absent evidence reference
- **THEN** it is refused server-side and never transmitted
- **AND** the refusal is recorded with the surface the finding claimed to describe

#### Scenario: Finding with an unmeasured surface
- **WHEN** a surface was examined but no measurement could be taken
- **THEN** a `finding` is emitted with state `not_measured` and the named missing input
- **AND** it is NOT omitted from the conversation

### Requirement: The system SHALL NOT allow a prose answer to assert a property of the customer's repository

#### Scenario: Prose restricted to capability questions
- **WHEN** the question is about what the platform can do, or what a term means
- **THEN** an `answer` message may be emitted

#### Scenario: Prose attempting a repository claim
- **WHEN** generated prose asserts a property of the customer's repository
- **THEN** it is refused as an `answer`, and the assertion is only expressible as a `finding` subject to the evidence requirement

### Requirement: The system SHALL stream messages and resume without duplication or gap

#### Scenario: Streaming
- **WHEN** a run is in progress
- **THEN** messages are delivered over `text/event-stream` as they are produced
- **AND** no interval longer than 15 seconds passes without a message while the run is non-terminal

#### Scenario: Reconnect mid-run
- **WHEN** the client disconnects and reconnects with its last acknowledged message id
- **THEN** delivery resumes from the message after that id
- **AND** no message is delivered twice and none is skipped

#### Scenario: Closed tab
- **WHEN** the client disconnects without cancelling
- **THEN** the run continues
- **AND** its messages remain retrievable on reconnect

#### Scenario: Explicit cancel
- **WHEN** the person cancels the run
- **THEN** the run stops at the next safe point and a terminal message records the cancellation

### Requirement: The system SHALL route an in-conversation approval to the existing approval gate

#### Scenario: Approval is the same act
- **WHEN** a person approves a proposal inside the conversation
- **THEN** the approval is recorded through `internal/approval` and is indistinguishable downstream from an approval given on any other surface

#### Scenario: Approval beyond entitlement
- **WHEN** the tenant's plan and automation level do not permit the proposed action
- **THEN** the `approval_request` is delivered already un-approvable, carrying the reason
- **AND** no approval control is rendered

#### Scenario: Distinct requests are not bundled
- **WHEN** an action would both open a pull request and merge it
- **THEN** two separate `approval_request` messages are emitted, each stating its own blast radius and reversibility

### Requirement: The system SHALL replay a pinned inference rather than re-running it

#### Scenario: Repeat question
- **WHEN** a question resolves to an inference already pinned for the same `(source_revision, agent config_hash)`
- **THEN** the pinned result is replayed
- **AND** no provider call is made

#### Scenario: Explicit re-run
- **WHEN** a person explicitly requests a re-run
- **THEN** the new result is presented as a diff against the pinned result
- **AND** the transcript records which messages came from a pin and which were generated in this turn

#### Scenario: Pinned result is stale
- **WHEN** the pinned inference was taken at a source revision earlier than the workflow's current one
- **THEN** the replayed messages are labelled stale and name the revision they describe

### Requirement: The system SHALL surface a lower-layer refusal with its own cause text

#### Scenario: Transform refusal reaches the conversation
- **WHEN** a lower layer refuses with a typed cause naming an axis and a node
- **THEN** a `refusal` message carries that axis, that node and that cause text unmodified
- **AND** the cause text is not re-worded or summarised by a model

#### Scenario: Unroutable question
- **WHEN** the question cannot be routed to a known intent with sufficient confidence
- **THEN** a `refusal` is emitted naming what this surface can do
- **AND** no run is started

### Requirement: The system SHALL keep the three failure classes distinguishable inside the conversation

#### Scenario: Subsystem not mounted
- **WHEN** a required subsystem is not mounted in this deployment
- **THEN** the message states that it is unavailable in this deployment

#### Scenario: Subject not found
- **WHEN** the named subject does not exist
- **THEN** the message states that it was not found, and this is never rendered as a business state

#### Scenario: Transport failure
- **WHEN** the request could not be completed
- **THEN** the message states a transport failure and offers retry, distinct from both cases above

### Requirement: A turn SHALL advance through named phases and SHALL carry the phase it is in

The phases are `understand`, `plan`, `act`, `verify`, `respond`. A long-running turn whose phase is not
observable is indistinguishable from a hung one.

#### Scenario: The phase is carried, not inferred
- **WHEN** a turn is in progress
- **THEN** every `progress` message carries the phase the turn is in
- **AND** the phase advances monotonically through the five, never skipping `verify` before `respond`

#### Scenario: A turn that cannot name its phase
- **WHEN** an emitter produces a `progress` message with no phase
- **THEN** the message is refused before the transport
- **AND** the refusal is recorded naming the run

### Requirement: A plan SHALL declare its budget envelope before the first step runs

#### Scenario: The envelope is stated up front
- **WHEN** a `plan` message is emitted
- **THEN** it carries a turn ceiling, a token budget, a tool-call ceiling and a wall-clock ceiling
- **AND** the surface displays them before any step has executed

#### Scenario: A plan with no envelope
- **WHEN** a `plan` is constructed without all four limits
- **THEN** it is refused server-side and the run does not start

### Requirement: A run stopped by a limit SHALL name the limit and SHALL NOT render as complete

#### Scenario: Token budget exhausted mid-task
- **WHEN** a turn exhausts its token budget before its plan is finished
- **THEN** the terminal message carries a stop reason naming the token budget
- **AND** the steps that did not run are reconciled as `not_measured`, each naming its missing input
- **AND** the message is NOT rendered as a completed result

#### Scenario: Each limit is separately attributable
- **WHEN** a run stops on the turn ceiling, the tool-call ceiling, or the wall-clock ceiling
- **THEN** the stop reason names that specific limit, distinct from the other three and from `satisfied`

#### Scenario: The stop vocabulary is not duplicated
- **WHEN** a stop reason is recorded for a conversation turn
- **THEN** it comes from the same closed vocabulary the harness runtime uses
- **AND** a reason absent from that vocabulary fails the build

### Requirement: A result SHALL reconcile every step its plan declared

#### Scenario: Full reconciliation
- **WHEN** a `result` is emitted for a turn whose `plan` declared N steps
- **THEN** the result carries exactly N reconciliation entries
- **AND** each resolves to exactly one of `done`, `skipped`, `refused`, `not_measured`
- **AND** every entry that is not `done` names its reason

#### Scenario: A missing reconciliation entry
- **WHEN** a `result` carries fewer entries than its plan declared steps
- **THEN** it is refused before the transport, naming the unreconciled step

### Requirement: A result carrying a claim SHALL cite the verification record that supports it

#### Scenario: A delivered change cites its verdict
- **WHEN** a `result` reports that a change was verified
- **THEN** it carries the reference to the verification record that produced the verdict
- **AND** a result whose reference does not resolve is refused before the transport

### Requirement: Task state SHALL live on the run, and the conversation SHALL hold none

#### Scenario: Resume reads the run
- **WHEN** a client reconnects to an in-progress turn
- **THEN** the remaining budget, the completed steps and the current phase are read from the run
- **AND** no state is reconstructed from the client's message history

#### Scenario: Memory across conversations is refused
- **WHEN** a question depends on what was asked in an earlier conversation
- **THEN** a `refusal` states that the surface does not carry memory across conversations
- **AND** no cross-conversation store is read or written

### Requirement: A plan step SHALL NOT be re-entered past a fixed ceiling

#### Scenario: A loop whose stop condition never fires
- **WHEN** a plan step is re-entered more times than the ceiling allows
- **THEN** the run terminates with a stop reason naming the ceiling and the step
- **AND** the ceiling is a constant, not a value an operator or a person can raise

### Requirement: Every turn SHALL be traceable by the person who ran it

#### Scenario: Trace id is shown and resolves
- **WHEN** a turn completes or refuses
- **THEN** the surface displays a trace id for that turn
- **AND** that id retrieves the turn's tool calls, refusals and retries for the tenant that owns the run

#### Scenario: Trace id does not cross tenants
- **WHEN** a trace id belonging to another tenant's run is submitted
- **THEN** the request is refused without disclosing whether the id exists

### Requirement: The intent set SHALL be equal to the set of working surfaces

#### Scenario: Every intent resolves to a surface
- **WHEN** the intent table is enumerated
- **THEN** each entry names exactly one working surface that exists in the console's route table

#### Scenario: A route without an intent
- **WHEN** a working route is added to the console with no corresponding intent
- **THEN** the fence fails the build, naming the route

#### Scenario: An intent without a route
- **WHEN** an intent is added that names no existing working surface
- **THEN** the fence fails the build, naming the intent

#### Scenario: Account and billing are out of scope by name
- **WHEN** a question asks to change a plan, a password, or a member's access
- **THEN** a `refusal` names the surface that performs it
- **AND** no run is started
