# Skill & Tool Authoring — Spec Delta (P14)

Product rationale: [`../../../../../docs/prd/P14-skills-tools-optimization.md`](../../../../../docs/prd/P14-skills-tools-optimization.md)
§6 (FR15–FR22), §7. Design reasoning: [`../../design.md`](../../design.md) Decision 8.
**Shared contract:** [`../../../p13-prompt-model-optimization/specs/authored-change/spec.md`](../../../p13-prompt-model-optimization/specs/authored-change/spec.md)
— one spine two origins, origin-blind refusals, preflight, `unverified` labeling, conflicts, reversal,
audit, entitlement, offline parity, no-new-egress, and *a user may not author the evidence*. Every
requirement there applies here and is **not** restated.

Covers the user-initiated half of the skills/tools axis: binding, unbinding and reordering a node's
skills, and pruning or restoring a node's tools, directly — rather than waiting for `OpAddSkill`,
`OpAddRerank` or `OpToolPrune` to propose one.

> **Why this axis needs authoring most, and why it refuses most.** The skill axis is the one the
> diagnosis catalog leans on hardest, which is exactly why an engineer who already knows *which* skill a
> node needs should not have to wait for a signal to fire before they can bind it. But it is also the
> axis with the sharpest asymmetry between what the platform can *model* and what it can *apply*:
> materialization is per language, so a node in a language whose materializer has not landed cannot carry
> a skill binding at all. For an operator that asymmetry is a refusal at transform. For a human it must be
> a refusal at **preflight**, because "which language is this node in" is knowable before the user picks
> anything, and letting them choose a skill, order it, and submit — only to be told the language cannot
> carry it — is withholding a fact the system already held.
>
> The second discipline is **fail-closed selection**. A skill is *bound* by constructing a value from a
> sealed, version-addressed contract; a tool is *selected* from what the node was **discovered** to offer.
> Neither is free text. A user may not bind a skill version the registry does not seal, and may not prune
> or restore a tool the frontend did not find at that call site — for the same reason a variant may not
> reference an environment variable outside `DeclaredEnv`. An authoring surface that let a user type a
> tool name would be inventing a call-site fact, and the resulting diff would delete something that is
> not there or offer something that does not exist.

## ADDED Requirements

### Requirement: A user SHALL be able to author a node's skill bindings, and skill order SHALL remain identity-bearing

An authoring surface SHALL let a user bind a skill to a node, unbind one, and change the order of a
node's bound skills. Each SHALL express its effect solely through the node's skill references, so an
add, a remove, and a **reorder** each yield a new `config_hash`, and a node with no skills SHALL hash
byte-identically to a node that was never authored.

#### Scenario: Binding, unbinding and reordering each change the hash

- **WHEN** a user binds a skill, unbinds a skill, or reorders two bound skills on a node
- **THEN** each authored change resolves to a `config_hash` different from its parent's
- **AND** the effect is expressed only through the node's skill references.

#### Scenario: A node with no skills is unaffected

- **WHEN** a node carries no skill binding
- **THEN** its `config_hash` is byte-identical to its value before the authoring capability existed.

### Requirement: An authored skill binding SHALL reference a sealed, version-addressed skill

An authoring surface SHALL offer only skills the registry seals, and SHALL bind a **pinned version
identifier**. A binding that names an unknown skill, or a skill without a pinned version, SHALL be refused
at preflight with the skill and the reason named.

#### Scenario: Only registry-sealed skills are offered

- **WHEN** a user opens the skill choices for a node
- **THEN** every offered skill is a registry-sealed entry with a resolvable version
- **AND** free-text entry of a skill identifier is not accepted as a binding.

#### Scenario: An unpinned or unknown skill is refused by name

- **WHEN** a binding naming an unknown skill or an unpinned version is submitted through the API or command line
- **THEN** preflight returns `refused`, naming the skill and the reason
- **AND** no diff is produced.

### Requirement: An authored skill binding on a language with no landed materializer SHALL be refused at preflight, naming the node and the language

Where a node's language has no landed skill materializer, an authored skill binding SHALL be refused
before submission, naming the node, the language, and the dimension. The authoring surface SHALL NOT
present the binding as an applicable change, and the refusal SHALL carry the same typed cause the
transform raises for an operator-originated binding.

#### Scenario: The language boundary is stated before the user chooses

- **WHEN** a user opens the skill authoring controls for a node in a language with no landed materializer
- **THEN** the surface states that this node's language cannot yet carry a skill binding
- **AND** it does not present skills as applicable choices for that node.

#### Scenario: A submitted binding for an unsupported language is refused, not dropped

- **WHEN** a skill binding for a node in an unsupported language is submitted through any surface
- **THEN** it is refused with the typed cause naming the node, the language and the dimension
- **AND** no diff is produced
- **AND** the binding is not silently omitted from an otherwise-applied change.

### Requirement: An authored skill binding's arguments SHALL be validated against the skill's compiled input contract

Arguments supplied for an authored skill binding SHALL be validated against the bound version's compiled
input contract before the change is admissible. A binding whose arguments do not satisfy that contract
SHALL be refused at preflight, naming the field that failed.

#### Scenario: Invalid arguments are named before submission

- **WHEN** a user supplies arguments that do not satisfy the bound skill version's input contract
- **THEN** preflight returns `refused`, naming the failing field
- **AND** no diff is produced.

#### Scenario: Validation follows the pinned version

- **WHEN** a binding pins one version of a skill and a newer version relaxes the contract
- **THEN** validation is performed against the pinned version's contract, not the newest one.

### Requirement: A user SHALL be able to prune or restore a tool, selected fail-closed from the node's discovered tool set

An authoring surface SHALL let a user prune a tool the node was discovered to offer, and restore a
previously pruned one. The selection SHALL be validated **fail-closed** against the node's discovered tool
set: a tool the frontend did not locate at that call site SHALL NOT be selectable, and a submitted
selection naming one SHALL be refused with the tool and the reason named.

#### Scenario: Only discovered tools are selectable

- **WHEN** a user opens the tool selection for a node
- **THEN** every selectable tool is one discovery located at that call site
- **AND** free-text entry of a tool name is not accepted as a selection.

#### Scenario: A tool outside the discovered set is refused

- **WHEN** a tool selection naming a tool absent from the node's discovered set is submitted through any surface
- **THEN** preflight returns `refused`, naming the tool and the reason
- **AND** no diff is produced.

#### Scenario: Restoring a pruned tool returns the prior hash

- **WHEN** a user restores every tool they pruned on a node
- **THEN** the resulting `config_hash` is byte-identical to the pre-prune configuration.

### Requirement: An authored tool selection over a dynamically-assembled tool set SHALL be refused, not guessed

Where a node's tool set is assembled dynamically and the frontend cannot locate the tool as a call-site
value, an authored tool selection SHALL be refused at preflight naming the node and the reason. The system
SHALL NOT infer the intended deletion site.

#### Scenario: A dynamic tool set refuses rather than guesses

- **WHEN** a user authors a tool prune on a node whose tool set is assembled dynamically
- **THEN** preflight returns `refused`, naming the node and the reason
- **AND** no deletion site is inferred and no diff is produced.

### Requirement: An authored skill or tool change SHALL be applicable while unverified and SHALL NOT be reported as an efficiency or quality result

A user MAY apply an authored skill or tool change without verification. Such a change SHALL be
`unverified`, and its observed effect on declared-tool tokens, tool error rate, or task success SHALL NOT
be reported as a saving, an improvement, or a regression until the harness has run.

#### Scenario: An authored prune applies without a claim

- **WHEN** a user prunes a tool and applies the change without requesting verification
- **THEN** a diff is produced
- **AND** the change is `unverified`
- **AND** no token, cost, or error-rate saving is attributed to it.

#### Scenario: A verified authored prune is judged by the unchanged harness

- **WHEN** a user requests verification of an authored tool prune
- **THEN** the outcome is produced by the existing axis-agnostic harness from `config_hash` and trace
- **AND** a prune that does not hold task success within confidence intervals is not reported as a win.

### Requirement: Each refusal class on this axis SHALL name its own subject, and the legitimate path where one exists

A refusal on the skills or tools axis SHALL name the specific thing that caused it: the node and the
language for a missing materializer, the skill for an unknown or unpinned binding, the argument field for
a contract violation, the tool for a selection outside the discovered set, and the node for a
dynamically-assembled tool set. A refusal SHALL NOT report only the dimension.

#### Scenario: A missing materializer names the language and what is covered

- **WHEN** a skill binding is refused because the node's language has no materializer
- **THEN** the refusal names the node and the language
- **AND** it lists the languages that are covered today
- **AND** it states that the gap is the platform's rather than the user's catalogue.

#### Scenario: An unpinned binding is refused for its own reason

- **WHEN** a skill reference names no version
- **THEN** the refusal states that an unpinned binding leaves the constructed value's shape undetermined
- **AND** the wording is distinguishable from the unknown-skill refusal.

#### Scenario: A contract violation names the failing argument

- **WHEN** authored skill arguments violate the pinned version's compiled input contract
- **THEN** the refusal names the failing field
- **AND** the reader is pointed at an argument to correct rather than at the binding as a whole.

#### Scenario: A tool outside the discovered set names both the tool and what was found

- **WHEN** a tool selection names a tool the node was not discovered to offer
- **THEN** the refusal names the offending tool
- **AND** it lists the tools the node does offer.

#### Scenario: A dynamically-assembled tool set is distinguished from an empty one

- **WHEN** a node's tool set is assembled at run time
- **THEN** the refusal states that there is no static declaration to prune
- **AND** it is distinguishable from a node that was discovered to offer no tools.
