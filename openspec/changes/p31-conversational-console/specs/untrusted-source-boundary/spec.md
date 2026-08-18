# Untrusted Source Boundary — Spec (P31)

The chain this capability defends did not exist before the GEHA program: **customer source → agent
reasoning → proposal → approval → commit → push**. Repository content is now input to a system with
write capability, so text inside a repository can attempt to cause an action.

The structural defence is that effect-bearing artifacts cannot be minted by a model. Detection is
defence in depth and is never the thing being relied on.

## ADDED Requirements

### Requirement: The system SHALL deliver repository content to a model as data, distinguishable from instruction

#### Scenario: Source is framed as data
- **WHEN** repository content is included in a model request
- **THEN** it is carried in a channel the request format distinguishes from the agent's own instructions
- **AND** findings derived from it are expressed as claims about the text, never as actions requested by it

### Requirement: The system SHALL NOT construct an effect-bearing message from model output alone

Effect-bearing kinds are `proposal`, `approval_request` and `result`.

#### Scenario: Model emits text shaped like a proposal
- **WHEN** a model turn produces output resembling a proposal, including a well-formed identifier
- **THEN** no proposal is created
- **AND** the conversation emits no `proposal` message

#### Scenario: A proposal requires a ledger artifact
- **WHEN** a `proposal` message is emitted
- **THEN** it references a `proposal_id` that exists in the verification ledger
- **AND** a reference that does not resolve in the ledger causes the message to be refused before transmission

#### Scenario: A result requires a delivery record
- **WHEN** a `result` message reports a delivery
- **THEN** it references a delivery record that exists
- **AND** a pull-request URL is carried only when the delivery record carries it

### Requirement: The system SHALL accept an approval only from the authenticated session's person

#### Scenario: Approval cannot originate in content
- **WHEN** repository content, tool output, or a model turn contains an approval instruction
- **THEN** no approval is recorded
- **AND** the run remains blocked awaiting a person

#### Scenario: Approval is attributed
- **WHEN** an approval is recorded
- **THEN** it carries the person and tenant from the authenticated session, not from any request-supplied field

### Requirement: The system SHALL NOT follow a network destination or command found in repository content

#### Scenario: URL in source
- **WHEN** repository content contains a URL, endpoint, webhook or callback
- **THEN** the agent does not request it
- **AND** egress remains confined to the constructed allowlist

#### Scenario: Command in source
- **WHEN** repository content instructs the agent to run a command outside the sandboxed evaluation it was already going to run
- **THEN** the command is not executed

### Requirement: The system SHALL report a detected instruction attempt as a finding rather than ignoring it

#### Scenario: Injection attempt surfaced
- **WHEN** content in the repository is detected as an attempt to instruct the agent
- **THEN** a `finding` is emitted describing what was found and where, with its evidence reference
- **AND** the run continues without acting on it

#### Scenario: Detection failure does not become an effect
- **WHEN** an instruction attempt is present and is NOT detected
- **THEN** no effect-bearing message can still be produced from it, because those require artifacts a model cannot mint
- **AND** this scenario is exercised with detection deliberately disabled
