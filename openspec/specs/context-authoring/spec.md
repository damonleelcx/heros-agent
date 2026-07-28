# Context Authoring — Spec (folded from P16)

Product rationale: [`../../../docs/prd/P16-context-strategy-optimization.md`](../../../docs/prd/P16-context-strategy-optimization.md)
§6 (FR15–FR23), §7. Design reasoning: [`../../changes/p16-context-strategy-optimization/design.md`](../../changes/p16-context-strategy-optimization/design.md) Decision 8.
**Shared contract:** [`../authored-change/spec.md`](../authored-change/spec.md)
— one spine two origins, origin-blind refusals, preflight, `unverified` labeling, conflicts, reversal,
audit, entitlement, offline parity, no-new-egress, and *a user may not author the evidence*. Every
requirement there applies here and is **not** restated.

Covers the user-initiated half of the context axis: choosing a node's context policy and its parameters,
declaring its drop tolerance, and tuning retrieval — instead of waiting for `OpContextPolicy` or
`OpRAGTune` to propose it.

> **Why this axis's authoring rules are about loss, not about permission.** Context is the axis where a
> change silently destroys information. Every policy other than "keep everything" *drops* something — that
> is what a policy is — and the amount dropped is measured, not asserted: `DropRatio` is telemetry, and
> the drop-tolerance gate is what turns it into an admissibility rule. So the interesting question for
> authoring is not "may the user pick a policy?" (they may) but "**what does the platform do when it does
> not yet know how much this policy would drop?**"
>
> The answer is the one the drop gate already ships: it **never refuses on ignorance**. An unmeasured node
> returns `not-yet-measurable`, naming what is missing — not `admissible`, which would pretend to a safety
> check that never ran, and not `refused`, which would block a user for a fact about the platform's
> measurement coverage rather than about their change. That third verdict is the single most important
> thing this capability adds, because both alternatives are lies of different signs.
>
> The second rule is that a user may set a policy but may not set the classifier. Retrieval parameters are
> admissible only on a node the classifier labels `RetrievalRAG`; a user cannot declare a node to be a
> retrieval node in order to unlock the parameters, any more than they can pick the held-out set that
> judges the result.

## Requirements

### Requirement: A user SHALL be able to author a node's context policy and its parameters

An authoring surface SHALL let a user select a registered context policy for a node and set its
parameters. The effect SHALL be expressed solely through the node's context policy override, so it
resolves into the node's frozen configuration and participates in `config_hash` through the existing
field, and a node carrying no override SHALL hash byte-identically to a pre-P16 node.

#### Scenario: An authored policy change resolves and re-hashes

- **WHEN** a user selects a different context policy for a node and submits it
- **THEN** the change resolves to a versioned registry entry frozen into the node's resolved configuration
- **AND** the resulting `config_hash` differs from the parent's.

#### Scenario: Only registered policies are offered

- **WHEN** a user opens the context policy choices for a node
- **THEN** every offered policy is one the registry holds behind the policy interface
- **AND** free-text entry of a policy identifier is not accepted as a selection.

### Requirement: An authored context change on a language with no landed materializer SHALL be refused at preflight, naming the node, the policy, and the language

Where a node's language has no landed context-materialization rewriter, an authored context policy change
SHALL be refused before submission, naming the node, the policy, and the language. The surface SHALL state
the boundary rather than presenting the control as silently unavailable, and the refusal SHALL carry the
same typed cause the transform raises for an operator-originated override.

#### Scenario: The language boundary is stated before the user chooses

- **WHEN** a user opens the context authoring controls for a node in a language with no landed rewriter
- **THEN** the surface states that this node's language cannot yet carry a context policy change
- **AND** it does not present the change as applicable.

#### Scenario: A submitted override for an unsupported language is refused, not dropped

- **WHEN** a context policy override for a node in an unsupported language is submitted through any surface
- **THEN** it is refused with the typed cause naming the node, the policy, and the language
- **AND** no diff is produced
- **AND** the override is not silently omitted from an otherwise-applied change.

### Requirement: The drop-tolerance gate SHALL run at preflight and SHALL NOT refuse on ignorance

An authored context change SHALL be evaluated against the node's declared drop tolerance before
submission and before any evaluation spend. Where the resolved policy's drop ratio for that node has not
been measured, preflight SHALL return `not-yet-measurable` naming the missing measurement. It SHALL NOT
return `admissible`, and it SHALL NOT return `refused`.

#### Scenario: A policy that would exceed the node's tolerance is refused before eval spend

- **WHEN** a user authors a context policy whose measured drop ratio for that node exceeds the node's declared tolerance
- **THEN** preflight returns `refused`, naming the node, the tolerance, and the measured drop ratio
- **AND** no evaluation run is enqueued.

#### Scenario: An unmeasured drop ratio yields the third verdict

- **WHEN** a user authors a context policy whose drop ratio for that node has never been measured
- **THEN** preflight returns `not-yet-measurable`, naming the missing measurement
- **AND** it does not return `admissible`
- **AND** it does not return `refused`.

#### Scenario: A node that declares no tolerance is unaffected

- **WHEN** a node declares no drop tolerance
- **THEN** the gate does not refuse the authored change on tolerance grounds
- **AND** the node's `config_hash` remains byte-identical to a pre-P16 node's.

### Requirement: A user SHALL be able to declare a node's drop tolerance, and it SHALL be additive

An authoring surface SHALL let a user declare or clear a node's drop tolerance. The attribute SHALL be
additive and omitted when absent, so declaring one changes the node's `config_hash` and clearing it
returns the node to its prior hash byte-identically.

#### Scenario: Declaring and clearing a tolerance is reversible

- **WHEN** a user declares a drop tolerance on a node and later clears it
- **THEN** declaring it produces a `config_hash` different from the parent's
- **AND** clearing it reproduces the pre-declaration `config_hash` byte-identically.

#### Scenario: A tolerance a current policy already exceeds is reported, not silently accepted

- **WHEN** a user declares a drop tolerance stricter than the node's current measured drop ratio
- **THEN** preflight reports that the node's current policy already exceeds the declared tolerance
- **AND** the report names the current policy and the measured ratio.

### Requirement: Authored retrieval parameters SHALL be admissible only on a classifier-labelled retrieval node, and the user SHALL NOT set the label

An authoring surface SHALL offer retrieval parameters — top-k, chunk size, rerank, embedding model — only
on a node the pattern classifier labels as retrieval. A user SHALL NOT be able to set, override, or
declare that label in order to make the parameters available.

#### Scenario: Retrieval parameters are not offered on a non-retrieval node

- **WHEN** a user opens the context authoring controls for a node the classifier does not label retrieval
- **THEN** retrieval parameters are not offered
- **AND** the reason is stated.

#### Scenario: A submitted retrieval parameter on a non-retrieval node is refused

- **WHEN** retrieval parameters for a node the classifier does not label retrieval are submitted through any surface
- **THEN** they are refused with the node and the reason named
- **AND** no request parameter or role sets the classifier label.

### Requirement: An authored retrieval change SHALL be pinned and deterministic when measured

When an authored retrieval change is verified, the retriever, its parameters, and the seed SHALL be
pinned, so re-running the same `config_hash` issues the identical resolved retrieval request including any
rerank. The held-out set that judges the change SHALL be disjoint from any cases the authoring surface
displayed as motivation, and SHALL be platform-derived.

#### Scenario: An authored retrieval measurement is reproducible

- **WHEN** an authored retrieval change is verified and the same `config_hash` is re-run
- **THEN** the identical resolved retrieval request is issued, including any rerank
- **AND** the measurement is reproducible across runs.

#### Scenario: The user does not choose the held-out set for their own retrieval change

- **WHEN** a user requests verification of an authored retrieval change
- **THEN** the held-out set is platform-derived and disjoint from the cases shown as motivation
- **AND** a win measured only on the cases the user was shown is not reported as a verified delta.

### Requirement: An authored context change SHALL be applicable while unverified and SHALL NOT be reported as a token or quality result

A user MAY apply a materializable authored context change without verification. Such a change SHALL be
`unverified`, and no token reduction, cost saving, or quality effect SHALL be attributed to it until the
harness has run. A measured `DropRatio` SHALL be reported as observed loss, never as a saving.

#### Scenario: An authored policy change applies without a claim

- **WHEN** a user applies an authored context policy change without requesting verification
- **THEN** a diff is produced
- **AND** the change is `unverified`
- **AND** no token or cost saving is attributed to it.

#### Scenario: Drop ratio is reported as loss, not as reduction

- **WHEN** an authored context change's drop ratio is displayed
- **THEN** it is presented as information the policy discarded
- **AND** it is not presented as a token or cost saving.

#### Scenario: Pure augmentation is recorded as retrieval, not as loss

- **WHEN** an authored retrieval change adds chunks without discarding conversation content
- **THEN** the drop ratio is zero
- **AND** a positive retrieved-chunk count is recorded.

### Requirement: The not-yet-measurable verdict SHALL read as a statement about the platform, and SHALL name what would resolve it

Where the drop-tolerance gate cannot decide because a measurement is absent, the surface SHALL present
that as a gap in the platform's measurements rather than as a fault in the user's change. It SHALL name
the missing measurement and the way to obtain it, and it SHALL NOT use the wording or the visual
treatment reserved for a refusal.

#### Scenario: The third verdict says the gap is the platform's

- **WHEN** the drop gate returns `not-yet-measurable`
- **THEN** the surface states that the measurement is missing on the platform's side
- **AND** it states explicitly that this is neither a refusal nor an approval
- **AND** it names the measurement and how to collect it.

#### Scenario: The third verdict is not drawn as a refusal

- **WHEN** the `not-yet-measurable` state is rendered alongside a refusal
- **THEN** the two are visually and textually distinguishable
- **AND** the `not-yet-measurable` state does not carry the hazard treatment.

### Requirement: A drop ratio SHALL be described as information discarded, and a smaller context SHALL NOT be described as cheaper before a verdict exists

Any presentation of `context_drop_ratio`, or of a token reduction produced by a context change, SHALL
describe it as information the policy discarded. It SHALL NOT be described as a saving, a reduction in
cost, or an efficiency gain on a change the evaluation harness has not judged.

#### Scenario: The drop ratio is presented as loss

- **WHEN** a drop ratio is displayed for an authored context change
- **THEN** it is described as information the policy discarded
- **AND** it is not paired with a saving, a cost figure, or an efficiency claim.

#### Scenario: An unverified smaller context claims nothing

- **WHEN** an authored context change that reduces tokens has not been verified
- **THEN** no cost, latency, or quality benefit is attributed to it
- **AND** it contributes nothing to any aggregate savings figure.

#### Scenario: Pure augmentation is not described as loss

- **WHEN** a retrieval change adds chunks without discarding conversation content
- **THEN** the drop ratio is reported as zero with a positive retrieved-chunk count
- **AND** the change is not described as having lost context.

### Requirement: The drop gate SHALL be described as a measurement, never as a guarantee

Any description of the drop-tolerance gate, on any surface or in any customer-facing material, SHALL
state that it judges on a measurement and that it reports when no measurement exists. It SHALL NOT be
described as a guarantee that context will not be lost.

#### Scenario: The gate's limits are stated where it is described

- **WHEN** the drop gate is described to a user
- **THEN** the description says it decides on measured evidence
- **AND** it says that where no measurement exists the gate reports rather than guesses
- **AND** it does not promise that no context will be lost.
