# Authored Change — Spec (folded from P13)

Product rationale: [`../../../docs/prd/P13-prompt-model-optimization.md`](../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR21–FR33), §7 (NFR11–NFR18). Design reasoning: [`../../changes/p13-prompt-model-optimization/design.md`](../../changes/p13-prompt-model-optimization/design.md) Decisions 9–12.

This capability is the **cross-axis contract for user-initiated change**. It is defined once here and
referenced — never restated — by the per-axis authoring capabilities:
[`skill-tool-authoring`](../../changes/p14-skills-tools-optimization/specs/skill-tool-authoring/spec.md) (P14),
[`wiring-authoring`](../../changes/p15-workflow-wiring-optimization/specs/wiring-authoring/spec.md) (P15), and
[`context-authoring`](../context-authoring/spec.md) (P16).

> **The one sentence this capability exists to enforce: a user MAY author the change; a user MAY NOT
> author the evidence.**
>
> Until now every optimization axis had exactly one origin — a catalog operator nominates, verification
> decides. That is correct for *claims* and wrong for *control*: it leaves the engineer who owns the
> workflow unable to express an intent the catalog has no operator for, and unable to correct a proposal
> that is nearly right. The fix is **not** a second pipeline. An authored change is derived, resolved,
> hashed, gated, transformed and (optionally) scored by exactly the same machinery as a proposed one;
> the only differences are **who originated it** (recorded, never hashed) and **what it is allowed to
> claim** (nothing, until the harness says otherwise). A second apply path would be a second place for
> every safety gate to be wrong, which the ordering forbids at the stability level — so there is one
> spine and two origins.
>
> The corollary is the part that must not soften under product pressure: an authored change may be
> applied **without** a verification verdict, because it is the user's own repository and refusing to
> emit their edit would be the platform substituting its judgment for theirs. It is stamped
> `unverified`, it never enters the verified-delta ledger, it is never counted as a win, a regression,
> or a tie, and it never auto-merges. Honesty is preserved not by blocking the user but by **refusing to
> call their change a result**.

## Requirements

### Requirement: An authored change SHALL traverse the same pipeline as a proposed change

A change originated by a user SHALL be derived from a parent variant, resolved, hashed, gated,
transformed, and — when verification is requested — evaluated by the same components that process an
operator-originated candidate. The system SHALL NOT provide an authoring-only resolve path,
authoring-only transform path, or authoring-only gate.

#### Scenario: An authored change and an operator candidate producing the same configuration are indistinguishable downstream

- **WHEN** a user authors a node configuration that is byte-identical to one an operator proposed
- **THEN** both resolve to the same `config_hash`
- **AND** both are transformed by the same rewriter and subject to the same gates
- **AND** neither produces a diff the other would not.

#### Scenario: No second apply path exists

- **WHEN** the apply path is enumerated
- **THEN** exactly one transform entry point serves both origins
- **AND** no code path applies an authored change while bypassing a gate an operator candidate must pass.

### Requirement: An authored change SHALL record its origin and actor, and origin SHALL NOT participate in the configuration hash

Every change SHALL carry an origin of `operator` or `user`; a `user` origin SHALL additionally carry the
acting identity and tenant. Origin, actor, and authoring rationale SHALL be recorded on the candidate,
transform, and delivery records, and SHALL NOT be inputs to `config_hash`.

#### Scenario: Origin is recorded

- **WHEN** a user submits an authored change
- **THEN** the resulting candidate and transform records carry origin `user`, the acting identity, and the tenant
- **AND** the record is retrievable for audit.

#### Scenario: Origin does not perturb the hash

- **WHEN** the same resolved configuration is produced once by an operator and once by a user
- **THEN** the two `config_hash` values are byte-identical
- **AND** pre-existing golden hash vectors reproduce unchanged.

### Requirement: Every refusal that binds an operator-originated change SHALL bind an authored change identically

A transform, resolve, or gate refusal SHALL be raised for an authored change under exactly the same
conditions, with the same typed cause, as for an operator-originated change. The system SHALL NOT expose
a flag, role, plan, or parameter that materializes a configuration the transform refuses.

#### Scenario: A user cannot force a refused materialization

- **WHEN** a user authors a change whose materialization the transform refuses
- **THEN** the refusal is raised with the same typed cause an operator candidate would receive
- **AND** no diff is produced
- **AND** no request parameter, entitlement, or role suppresses the refusal.

#### Scenario: The refusal cause text is identical across origins

- **WHEN** the same refusable configuration is submitted by an operator and by a user
- **THEN** the named cause reported to each is the same.

### Requirement: An authoring surface SHALL preflight a draft and name any refusal before submission

Before a draft is submitted, an authoring surface SHALL evaluate it against resolution, the applicable
gates, and materializability, and SHALL return one of `admissible`, `refused` with the named cause and
the offending node or field, or `not-yet-measurable` where an admissibility input is unknown. Preflight
SHALL NOT produce a diff, publish a version, or consume evaluation budget.

#### Scenario: A refusable draft is named before submission

- **WHEN** a user drafts a change the transform would refuse
- **THEN** preflight returns `refused` with the named cause and the offending node or field
- **AND** the surface does not offer the draft as an applicable change.

#### Scenario: Preflight spends nothing

- **WHEN** preflight runs on any draft
- **THEN** no evaluation run is enqueued, no prompt version is published, and no diff is written.

#### Scenario: Unknown admissibility inputs are reported as unknown, not as a pass

- **WHEN** an admissibility input required to decide the draft has never been measured
- **THEN** preflight returns `not-yet-measurable` naming the missing input
- **AND** it does not return `admissible`
- **AND** it does not return `refused`.

### Requirement: An applied authored change without a verification verdict SHALL be labeled unverified and SHALL NOT be reported as a result

An authored change MAY be applied and produce a reviewable diff without a verification verdict. Such a
change SHALL carry a durable `unverified` verification state; it SHALL NOT be admitted to the verified
delta ledger, SHALL NOT be reported as a win, regression, or tie, and SHALL NOT contribute to any
aggregate improvement, savings, or quality figure.

#### Scenario: An unverified authored change is applied and labeled

- **WHEN** a user applies an authored change without requesting verification
- **THEN** a reviewable diff is produced
- **AND** the change's verification state is `unverified`
- **AND** it does not appear in the verified delta ledger.

#### Scenario: An unverified change is excluded from aggregates

- **WHEN** an aggregate improvement or cost figure is computed
- **THEN** unverified authored changes contribute nothing to it
- **AND** the figure's basis excludes them explicitly rather than by omission.

#### Scenario: The label travels with the change

- **WHEN** an authored change is displayed anywhere a verified delta is also displayed
- **THEN** its `unverified` state is displayed with it
- **AND** it is not presented in the same visual class as a verified delta.

### Requirement: An unverified authored change SHALL NOT be auto-merged

Delivery of an authored change SHALL obey the existing automation-level and verdict rules unchanged. A
change whose verification state is `unverified` SHALL NOT be merged automatically at any automation
level.

#### Scenario: Auto-merge is refused for an unverified authored change

- **WHEN** delivery is attempted for an unverified authored change under the highest automation level
- **THEN** the merge is refused with a named cause
- **AND** the pull request, if opened, remains for human decision.

### Requirement: An authoring surface SHALL NOT compute a score, rank, winner, or interval

An authoring surface SHALL render verification outcomes produced by the evaluation harness and SHALL NOT
derive, recompute, or approximate a score, rank, winner, confidence interval, tie determination, or
promotion decision.

#### Scenario: The editor renders but does not derive

- **WHEN** an authoring surface displays a verification outcome
- **THEN** every displayed statistic was received from the harness
- **AND** no statistic is computed by the authoring surface.

#### Scenario: No promotion path from an authoring surface

- **WHEN** the authoring surface is inspected for promotion actions
- **THEN** no control promotes an exploratory authored result to a claim.

### Requirement: A draft SHALL NOT mutate its parent, and a stale submission SHALL be refused by name

An authored draft SHALL reference an immutable parent variant and SHALL be submitted with a concurrency
token. Submission of a draft whose parent has advanced SHALL be refused with a named conflict. The system
SHALL NOT overwrite a concurrent change.

#### Scenario: Two users editing one parent produce two variants

- **WHEN** two users author changes from the same parent variant and both submit
- **THEN** two distinct variants exist, each with the same `ParentVariantID`
- **AND** neither user's change is lost.

#### Scenario: A stale draft is refused, not silently applied

- **WHEN** a draft is submitted against a parent that has since advanced
- **THEN** submission is refused with a named conflict identifying the parent
- **AND** no diff is produced.

### Requirement: An authored change derived from a proposal SHALL record the originating proposal and SHALL NOT credit the operator

When a user edits an operator-produced proposal and submits it, the result SHALL be an authored change
recording the originating proposal's identifier. The originating operator SHALL NOT be credited with the
authored change's outcome in any operator-performance figure.

#### Scenario: A forked proposal keeps both lineages

- **WHEN** a user modifies a proposal and submits it
- **THEN** the resulting change has origin `user` and records the originating proposal identifier
- **AND** both lineages are retrievable.

#### Scenario: Operator statistics are not inflated by human correction

- **WHEN** an operator-performance figure is computed
- **THEN** outcomes of authored changes forked from that operator's proposals are excluded from it.

### Requirement: Authoring SHALL be entitlement-gated and recorded append-only

The ability to author a change SHALL be checked as a plan feature and against the acting identity's
permissions before a draft is submitted. Every submitted authored change SHALL be recorded append-only
with actor, tenant, timestamp, parent variant, axis, resolved `config_hash`, and the diff reference.

#### Scenario: A read-only identity cannot author

- **WHEN** an identity without authoring permission submits a draft
- **THEN** submission is refused with a named authorization cause
- **AND** no draft, variant, or diff is created.

#### Scenario: The audit record is append-only

- **WHEN** an authored change is superseded or reverted
- **THEN** its original record remains retrievable unchanged
- **AND** the reversal is recorded as an additional entry.

### Requirement: Every authored change SHALL have a reversal that reproduces the parent configuration hash

Reverting an authored change SHALL produce a new variant derived from the recorded parent whose resolved
`config_hash` is byte-identical to the pre-edit configuration. Reversal SHALL NOT restore a variant in
place.

#### Scenario: Reverting reproduces the parent hash

- **WHEN** an authored change is reverted
- **THEN** the resulting variant's `config_hash` equals the pre-edit `config_hash` byte-for-byte
- **AND** the reverted-from variant remains resolvable.

### Requirement: The command-line surface SHALL author offline with identical gates, refusals, and cause text

Authoring through the command line SHALL work with no account and no network against a local spec, SHALL
run the same resolution, gates, and materializability checks, and SHALL produce the same typed cause for
a refusal as the hosted surface.

#### Scenario: Offline authoring refuses identically

- **WHEN** a change that the hosted surface would refuse is authored offline through the command line
- **THEN** the same typed cause is reported
- **AND** no diff is produced
- **AND** no network call is required to reach that outcome.

#### Scenario: An offline-authored change carries its origin when linked

- **WHEN** a change authored offline is later linked to an account
- **THEN** its origin, actor and parent are preserved in the recorded lineage.

### Requirement: Authoring SHALL introduce no new egress

The authoring path SHALL transmit nothing across the platform boundary that the existing allowlist does
not already permit. Prompt text, source, diffs, environment values, and credentials SHALL NOT cross the
boundary on any authoring path, including preflight and diagnostics.

#### Scenario: Preflight transmits no restricted content

- **WHEN** preflight runs against the hosted surface
- **THEN** the transmitted payload contains no prompt text, source, diff, environment value, or credential
- **AND** the transmitted fields are drawn from the existing allowlist.

### Requirement: A user SHALL NOT author the evidence for a verification run

Case selection, held-out splits, seeds, and the number of evaluation repetitions for a verification run
SHALL be derived by the platform. An authoring surface SHALL NOT let a user choose which cases judge
their own change, which cases are held out, or which seeds are used.

#### Scenario: A user requests verification but does not choose its cases

- **WHEN** a user requests verification of an authored change
- **THEN** the held-out split and seeds are platform-derived
- **AND** no user-supplied parameter alters which cases judge the change.

#### Scenario: An authored retrieval or model change cannot select its own held-out set

- **WHEN** an authored change is verified on an axis with a held-out admissibility rule
- **THEN** the held-out set is disjoint from the cases the authoring surface displayed as motivation
- **AND** the disjointness is asserted rather than assumed.

### Requirement: Each preflight verdict SHALL be presented as its own state, with its own next step

An authoring surface SHALL present `admissible`, `refused`, and `not-yet-measurable` as three distinct
states. It SHALL NOT render two of them with the same control, the same tone, or the same wording. The
`not-yet-measurable` state SHALL be presented as a statement about the platform's measurement coverage
and SHALL name what would make it measurable; it SHALL NOT be presented as a refusal of the change.

#### Scenario: The three verdicts are three states

- **WHEN** an authoring surface renders each of the three verdicts
- **THEN** each is visually and textually distinguishable from the other two
- **AND** the refused state names the node and the field, and the not-yet-measurable state names the
  missing measurement.

#### Scenario: Not-yet-measurable does not wear the refusal's tone

- **WHEN** the `not-yet-measurable` state is rendered
- **THEN** it does not use the hazard treatment reserved for refusals and destructive controls
- **AND** its text states that the gap is in the platform's measurements rather than in the user's change
- **AND** it states that it is neither a refusal nor an approval.

### Requirement: A refusal SHALL name the legitimate path where one exists

Where a refused change has a supported route to the same intent, the refusal SHALL name that route.
Where no such route exists, the refusal SHALL say so rather than implying one.

#### Scenario: An un-carryable parameter names the mode that would carry it

- **WHEN** a parameter override is refused because the node applies inline
- **THEN** the refusal states that switching the node to bound apply mode would carry it.

#### Scenario: A cross-provider swap names where routing belongs

- **WHEN** a cross-provider model change is refused
- **THEN** the refusal states that provider routing is a gateway concern rather than a call-site edit
- **AND** it does not imply that a plan, role, or setting would permit the call-site swap.

#### Scenario: A refusal with no route says so

- **WHEN** a change is refused and no supported route to the same intent exists
- **THEN** the refusal states the limitation plainly
- **AND** it does not suggest a route that does not exist.

### Requirement: An authored change SHALL be described as applied, never as verified, improved, optimized, or safe

Interface text, machine payloads, and generated narration SHALL describe an unverified authored change
as *applied*. They SHALL NOT attach *verified*, *improved*, *optimized*, *cheaper*, *faster*, or *safe*
to a change the evaluation harness has not judged. The verification state, the record's field name, and
the code identifier SHALL remain three separate layers.

#### Scenario: An unverified change carries no quality or cost word

- **WHEN** an unverified authored change is described on any surface
- **THEN** the description says it was applied
- **AND** it attaches no quality, cost, latency, or safety claim.

#### Scenario: A measured outcome may use the harness's own words

- **WHEN** an authored change has been evaluated and the harness produced a verdict
- **THEN** the description may state that verdict
- **AND** the words used are the harness's classification rather than a surface's paraphrase.
