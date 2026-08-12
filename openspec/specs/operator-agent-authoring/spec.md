# Operator Agent Authoring — Spec (folded from P30)

Product rationale: [`../../../docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md) §6, §8.2 and §9.
Design reasoning: [`../../changes/p30-heros-platform-agent/design.md`](../../changes/p30-heros-platform-agent/design.md).

Covers the six axis editors, each bound to its existing vocabulary, and the refusals that fire at SAVE.

> 🚫 **No axis is a text box.** A strategy whose host service this runner does not supply is shown as
> unavailable WITH the service it needs — never hidden, because a hidden option is indistinguishable from
> one that does not exist — and selecting it is refused rather than degraded to a neighbour.

## Requirements
The operator console surface through which HEROS's prompt, skills, tools, context, memory and harness
are managed. Every axis binds to the vocabulary that axis already has; none of them is a text box.

### Requirement: Each axis SHALL be edited against its existing vocabulary and never as free text
A free-text field for a value that has a closed vocabulary is a field that eventually holds a value
nothing can interpret. Every axis below already has a registry or a versioned builtin set; the console
binds to it.

#### Scenario: The prompt is authored as a template with parsed slots
- **WHEN** an operator edits HEROS's prompt
- **THEN** the body is parsed into a template and its slots are extracted
- **AND** saving registers a new prompt version whose id is derived from its content
- **AND** a slot referenced by the spec's bindings but absent from the template is refused at save,
  naming the slot

#### Scenario: Skills are selected from the skill registry
- **WHEN** an operator edits HEROS's skills
- **THEN** the choices are registered skill versions, each with its `impl_handle` and its compiled input
  and output schemas
- **AND** a skill whose schema fails to compile is not selectable
- **AND** a schema carrying a remote `$ref` is rejected, not fetched

#### Scenario: Tools are selected from the tool index with their scope and risk tier
- **WHEN** an operator edits HEROS's tools
- **THEN** the choices are indexed tools showing tenant scope, description, risk tier and approval state
- **AND** an unapproved tool is not bindable
- **AND** the scope of each bound tool is displayed, because a `_global` tool and a tenant-scoped tool of
  the same name are different bindings

#### Scenario: The context policy is one of the named policies with validated params
- **WHEN** an operator selects a context policy
- **THEN** the choices are the named policies of the context vocabulary
- **AND** the params form is derived from that policy's `ParamsSchema`
- **AND** params failing the schema are refused at save, naming the policy and the parameter

#### Scenario: The memory strategy is one of the named strategies
- **WHEN** an operator selects a memory strategy
- **THEN** the choices are the named strategies of the memory vocabulary at its recorded set version
- **AND** params are validated against that strategy's schema at save

#### Scenario: A memory strategy whose host service is unsupplied is refused at selection
- **WHEN** an operator selects a memory strategy requiring a summarizer or an embedder the HEROS runner
  does not supply
- **THEN** the selection is refused at save naming the missing service
- **AND** it is not degraded to a strategy that retains less

#### Scenario: A recall strategy without a pinned embedding is refused
- **WHEN** an operator selects a similarity-recall memory strategy without a pinned embedding reference
- **THEN** the save is refused, because recall is only reproducible against a pinned embedding

#### Scenario: A memory strategy that costs a model call says so
- **WHEN** an operator selects a memory strategy whose host service performs a model call
- **THEN** the console states that it adds a second spend line
- **AND** the resulting spend is metered and attributed separately

#### Scenario: The harness strategy is one of the named strategies with a bounded loop
- **WHEN** an operator selects a harness strategy that runs more than one turn
- **THEN** `max_turns` is required
- **AND** a value above the vocabulary's ceiling is refused at save
- **AND** a retry budget that could multiply turns past the ceiling is refused with the same reasoning

#### Scenario: Wiring is not offered
- **WHEN** an operator views the axis editors
- **THEN** wiring is shown as fixed for HEROS and is not editable

### Requirement: A strategy whose host service the runner cannot supply SHALL be refused at selection, not at run
The harness runtime refuses rather than degrading when a required host service is absent — `react-loop`
needs a tool executor, `plan-execute` needs a planner, `critic-loop` needs a separate critic. A console
that lets an operator select one the HEROS runner cannot serve turns a save into a failure discovered by
whoever next triggers an analysis.

#### Scenario: An unsupplied service blocks selection
- **WHEN** an operator selects a harness strategy whose required host service the HEROS runner does not
  supply
- **THEN** the selection is refused at save
- **AND** the message names the missing service and what supplying it would mean
- **AND** it does not say only that the strategy is unsupported

#### Scenario: The console states which strategies are available and why the others are not
- **WHEN** an operator opens the harness editor
- **THEN** each strategy is shown as available or unavailable
- **AND** an unavailable strategy names the host service it needs

#### Scenario: Degradation to a neighbouring strategy is never offered
- **WHEN** a strategy is unavailable for want of a host service
- **THEN** the console does not offer to run a similar strategy instead
- **AND** no saved definition results in one strategy executing under another's `config_hash`

#### Scenario: A critic strategy's second model is resolved and attributed
- **WHEN** a strategy requiring a separate critic model is available and selected
- **THEN** the critic model is chosen from the operator model registry
- **AND** its credential resolves as a reference like the primary model's
- **AND** its spend is metered and attributed alongside the primary model's

### Requirement: HEROS's memory SHALL be scoped to a single inference and SHALL NOT span tenants
The memory runtime never invents a session id, so the caller's choice sets the blast radius. A platform
agent reads many customers' repositories, and memory carried between analyses would both create a
cross-tenant path and add an invisible fourth input to a result the cache key claims to determine.

#### Scenario: The session id is the inference id
- **WHEN** HEROS records or recalls memory during an analysis
- **THEN** the memory key's session scope is that inference's id

#### Scenario: Memory does not survive the inference
- **WHEN** an inference completes
- **THEN** its memory entries are discarded
- **AND** a subsequent inference for the same workflow and revision starts with no entries

#### Scenario: No key can join two tenants
- **WHEN** two tenants' workflows are analysed
- **THEN** no memory key is shared between them
- **AND** no recall in one tenant's analysis can return an entry recorded in another's

#### Scenario: An invalid or defaulted scope is refused
- **WHEN** a memory operation is attempted without both scopes of the key
- **THEN** it fails closed
- **AND** it does not read from or write to a shared scope

#### Scenario: The console states the capability given up
- **WHEN** an operator views the memory axis
- **THEN** the surface states that HEROS does not learn across analyses and that a repository analysed
  twice starts cold both times
- **AND** it presents this as a deliberate scope, not as a defect

### Requirement: Params SHALL be validated at save, against the schema the vocabulary declares
Validating at run means the operator learns of a malformed strategy when an analysis reaches it, by
whoever was unlucky.

#### Scenario: Malformed params are refused at save
- **WHEN** an operator saves an axis whose params do not satisfy the declared schema
- **THEN** the save is refused naming the axis and the failing parameter
- **AND** no version row is written

#### Scenario: The vocabulary version is recorded on the definition
- **WHEN** a definition is published
- **THEN** it records the set version of each closed vocabulary it references
- **AND** a stored `config_hash` remains interpretable against the vocabulary it was written under

### Requirement: An edit SHALL show its effect before it is published
#### Scenario: The resolved diff is shown before publish
- **WHEN** an operator has changed one or more axes and requests publication
- **THEN** the console shows the diff of the resolved spec against the active definition and the
  `config_hash` that would result
- **AND** publication happens only on confirmation

#### Scenario: An edit that resolves to no change is named as such
- **WHEN** an edit produces a resolved spec identical to the active definition
- **THEN** the console reports that the `config_hash` is unchanged
- **AND** no new version is created

### Requirement: The surface SHALL distinguish a configured value from a default and from one not in effect
A value that was set but is not being used is the failure this requirement exists to make visible.

#### Scenario: Defaults are marked
- **WHEN** an operator views the active definition
- **THEN** each axis shows whether its value was explicitly set or is the vocabulary's default

#### Scenario: A published but unrehearsed definition is not shown as in effect
- **WHEN** a definition is published and has not passed rehearsal
- **THEN** the console shows the edited values as pending
- **AND** it shows which definition is actually serving inference

#### Scenario: An axis the runner ignores is marked
- **WHEN** an axis value is stored but the HEROS runner does not consume it for the active placement
- **THEN** the console marks it as not in effect and states why
- **AND** it does not render it as active configuration

### Requirement: A tool bound to HEROS SHALL NOT create a new egress path
HEROS reads a source snapshot. A tool that reaches the network from inside the analysis loop would be an
outbound surface created by a console selection.

#### Scenario: A network-reaching tool is not bindable
- **WHEN** an operator attempts to bind a tool whose declared capability includes outbound network access
- **THEN** the binding is refused naming the reason
- **AND** the refusal is not overridable from the console

#### Scenario: The bound tool set is auditable
- **WHEN** a definition is published
- **THEN** its bound tools are recorded with their scope, risk tier and approval state as at publication
- **AND** an inference records the definition `config_hash` that names them
