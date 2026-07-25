# Prompt Rewrite — Spec Delta (P13)

Product rationale: [`../../../../../docs/prd/P13-prompt-model-optimization.md`](../../../../../docs/prd/P13-prompt-model-optimization.md)
§6 (FR1–FR8), §7. Design reasoning: [`../../design.md`](../../design.md) Decisions 1, 2, 3, 5, 8.

Covers the deeper prompt operators — **instruction hardening**, **few-shot exemplar curation**, **prompt
compression / token-reduction**, and **redundancy removal** — that extend the single grounded-rewrite
operator into a family, each verification-gated, each publishing a new immutable version, each refused
when it would un-apply a node.

> **Why grounded-or-silent, and why a new version every time.** The prompt axis is *applied* — its
> proposals reach a diff — so a bad proposal is a real change, not a harmless suggestion. Two disciplines
> keep it honest. An operator that cannot ground its rewrite in the cases it addresses emits **nothing**,
> because an ungrounded "improve this prompt" occasionally ties or wins by chance and would ship as a
> result. And every rewrite publishes a **new content-addressed version** through P10's immutable
> registry, so the prior stays resolvable and *resolved values ship in the same diff, or the transform is
> rejected* — a compression that drops a live slot is refused, never silently applied.

## ADDED Requirements

### Requirement: The catalog SHALL provide distinct grounded prompt operators added as rows, not branches

The proposal catalog SHALL provide separate operators for instruction hardening, few-shot exemplar
curation, prompt compression / token-reduction, and redundancy removal. Each SHALL be a catalog row with
its own admissibility (diagnosis codes / signals handled and admissible patterns), added to the catalog
rather than as a branch inside an existing operator.

#### Scenario: Each strategy is an independently admissible operator

- **WHEN** the catalog is enumerated
- **THEN** instruction hardening, few-shot curation, prompt compression, and redundancy removal each
  appear as a distinct operator with its own admissibility
- **AND** none is expressed as a mode of another operator.

#### Scenario: An operator fires only on nodes it is admissible for

- **WHEN** a diagnosed node's pattern is not in an operator's admissible-pattern set
- **THEN** that operator proposes no candidate for the node.

### Requirement: A prompt operator SHALL emit only candidates, decided by verification

Each prompt operator SHALL emit **candidate** Variant Specs only. A candidate SHALL become an applicable
change solely by passing the verification gate; no operator output SHALL be treated as a result or
applied directly.

#### Scenario: A candidate is not a change until verified

- **WHEN** a prompt operator emits a candidate
- **THEN** the candidate is not applied to any working copy
- **AND** it becomes an applicable change only after the verification gate admits it.

#### Scenario: No path applies a candidate without the gate

- **WHEN** the paths from a candidate to an applied diff are enumerated
- **THEN** every path passes through the verification gate
- **AND** no path applies a raw candidate.

### Requirement: A prompt operator SHALL decline when it cannot ground its rewrite

An operator that cannot ground its rewrite in the failing or target cases it addresses SHALL emit **no
candidate** rather than a generic rewrite. Declining SHALL NOT be reported as an error.

#### Scenario: An ungrounded request yields no candidate

- **WHEN** an operator is asked to rewrite but has no grounding for the change
- **THEN** it emits zero candidates
- **AND** it does not emit an error.

#### Scenario: A grounded request yields a candidate carrying its grounding

- **WHEN** an operator has grounding for a rewrite
- **THEN** it emits a candidate
- **AND** the candidate carries the cases the rewrite addresses.

### Requirement: Every rewrite SHALL publish a new content-addressed prompt version

A rewrite SHALL be published as a new content-addressed prompt version using the existing registry
semantics. The prior version SHALL remain resolvable, and no operator SHALL express or perform an
in-place mutation of a version.

#### Scenario: A rewrite creates a new version and leaves the parent intact

- **WHEN** an operator rewrites a prompt
- **THEN** a new version with its own content-addressed identifier is created
- **AND** the version the rewrite was derived from remains resolvable and unchanged.

#### Scenario: No operator mutates a version in place

- **WHEN** the operator's effect on the registry is examined
- **THEN** it only publishes new versions
- **AND** it performs no in-place edit of an existing version.

### Requirement: A rewrite that would un-apply a node SHALL be refused, not dropped

A rewrite whose change to a prompt's slot set would leave a call site's supplied value unbound SHALL be
refused at resolve/transform, naming the slot that no longer binds. The resolved values ship in the same
diff, or the transform is rejected; such a rewrite SHALL NOT be silently dropped or partially applied.

#### Scenario: A compression that removes a live slot is refused naming the slot

- **WHEN** a rewrite removes a slot that a call site still supplies a value for
- **THEN** the transform is refused
- **AND** the refusal names the slot that no longer binds
- **AND** no diff is produced.

#### Scenario: An added slot with no call-site value is refused

- **WHEN** a rewrite adds a slot that the call site does not supply
- **THEN** the node is reported as un-applied at resolve time
- **AND** the change is refused rather than emitted as an un-bindable diff.

### Requirement: A prompt candidate's only hashed effect SHALL be its PromptRef

A prompt candidate SHALL express its effect solely as a changed prompt reference on the affected node, so
it participates in the configuration hash through the existing resolved-node field with no change to the
hash contract.

#### Scenario: A prompt change yields a new config hash through the existing field

- **WHEN** a prompt candidate changes the prompt on a node
- **THEN** the node's resolved prompt reference changes and the configuration hash changes with it
- **AND** no new hashed field is introduced.

#### Scenario: The golden hash vectors still reproduce

- **WHEN** the configuration-hash golden vectors are recomputed after this change
- **THEN** they reproduce bit-for-bit
- **AND** a configuration that declares no prompt change hashes byte-identically to before.

### Requirement: A shorter prompt SHALL NOT be a win unless it holds quality within confidence intervals

A compression or redundancy-removal candidate SHALL be judged on the same task-success and cost metrics
as any other candidate. A shorter prompt whose task-success does not hold within confidence-interval
overlap of the incumbent SHALL NOT be ranked a win, regardless of its token reduction.

#### Scenario: A shorter-but-worse prompt fails

- **WHEN** a compressed prompt reduces tokens but its task-success confidence interval falls below the
  incumbent's without overlap
- **THEN** the candidate is not ranked a win.

#### Scenario: Token reduction is not itself a goal metric

- **WHEN** a compression candidate is scored
- **THEN** it competes on the standard metric family
- **AND** no token-count target is treated as a success criterion in place of verified quality.

### Requirement: Prompt operators SHALL NOT introduce evaluation or studio state

The prompt operators SHALL NOT introduce a new evaluation oracle, scoring metric, or dimension, and SHALL
NOT introduce a score, rank, winner, interval, or promotion path into the authoring studio.

#### Scenario: The eval harness remains axis-agnostic

- **WHEN** a prompt candidate is scored
- **THEN** the harness consumes only the configuration hash and the trace
- **AND** no operator label or new metric is required to score it.

#### Scenario: The studio gains no evaluator behavior

- **WHEN** the authoring studio is inspected after this change
- **THEN** it exposes no score, rank, winner, or promotion path
- **AND** the operators live in the proposal/verification engine, not the studio.
