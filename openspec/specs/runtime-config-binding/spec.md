# Runtime Config Binding — Spec (folded from P10)

Product rationale: [`../../../docs/prd/P10-prompt-model-studio.md`](../../../docs/prd/P10-prompt-model-studio.md)
§6 (FR15–FR26) and §8.1. Architecture decision:
[`../../../docs/adr/ADR-004-runtime-config-binding.md`](../../../docs/adr/ADR-004-runtime-config-binding.md).
Design reasoning: [`../../changes/archive/2026-08-01-p10-prompt-model-studio/design.md`](../../changes/archive/2026-08-01-p10-prompt-model-studio/design.md) Decisions 1, 2, 4, 5, 6, 7.

Covers the second apply mode. The codemod still produces a reviewable source diff; in `bound` mode it
writes an indirection plus a generated artifact plus the **resolved binding document**, all in one
change, after which the model, its inference parameters, the prompt version and `literal`/`env`
bindings are **data** that can change without a new codemod. Wiring, skills, context policy and
`expr`/`input` bindings remain **code**, because they name things in the program's lexical scope.

> **ADR-001 is amended, not superseded.** Transformations stay AST-level and deterministic,
> build-preserving, behavior-preserving-except-intended, worktree-isolated, reviewable, and revertible
> by a single revert. The four hazards this mode creates — review hollowing, reproducibility,
> verified-vs-running drift, and blast radius in the customer's production process — are contained by
> requirements below rather than by caveats in prose.

## Requirements

### Requirement: Apply mode SHALL be selectable per node and SHALL default to inline

Apply mode SHALL be selectable per node as **`inline`** or **`bound`**, and SHALL default to
**`inline`**. A node SHALL NOT acquire a binding indirection unless its apply mode explicitly selects
one.

#### Scenario: A node with no stated apply mode is inline

- **WHEN** a node override states no apply mode
- **THEN** the node is applied inline, with the configured values written at the call site
- **AND** its diff is the same one it would have produced before this capability existed.

#### Scenario: Apply mode is per node, not per specification

- **WHEN** a specification sets one node to `bound` and leaves another at the default
- **THEN** only the first node is applied with an indirection
- **AND** the second is applied inline.

#### Scenario: The apply mode is visible in the change

- **WHEN** a node is applied in `bound` mode
- **THEN** the mode is evident from the generated change
- **AND** a reviewer does not have to infer it from the absence of an inline value.

### Requirement: A bound transformation SHALL emit the call site, the generated artifact, and the resolved values in one change

A `bound` transformation SHALL emit, in the **same** change: the rewritten call site, the generated
binding artifact, and the resolved binding document containing the **actual configured values**.

#### Scenario: One change carries all three parts

- **WHEN** a node is transformed in `bound` mode
- **THEN** the resulting change contains the rewritten call site, the generated artifact, and the
  resolved binding document
- **AND** none of the three is delivered separately from the others.

#### Scenario: The resolved values are present, not referenced

- **WHEN** the resolved binding document is inspected in the change
- **THEN** it contains the model identifier, the inference parameters, the prompt template, and the
  `literal` and `env` bindings as values
- **AND** it does not identify them only by a reference resolvable elsewhere.

### Requirement: A transformation that introduces an indirection without its resolved values SHALL be rejected

A transformation that introduces a binding indirection **without** the corresponding resolved values in
the same change SHALL be rejected before it is proposed or run, on the same footing as a transformation
that fails to build.

#### Scenario: An indirection without resolved values does not ship

- **WHEN** a generated transformation rewrites a call site to a binding indirection but the change does
  not contain the resolved values
- **THEN** the transformation is rejected
- **AND** it is neither proposed to a user nor executed.

#### Scenario: Rejection is a gate, not an advisory

- **WHEN** such a transformation is generated
- **THEN** it is refused rather than surfaced with a warning
- **AND** no automation level permits it to proceed.

### Requirement: A pull request SHALL render the effective resolved values for every bound node

A pull request containing a `bound` change SHALL render the **effective resolved values** for each
bound node, not only the indirection.

#### Scenario: A reviewer sees the configuration, not the pointer

- **WHEN** a reviewer opens a pull request containing a `bound` change
- **THEN** the effective model, inference parameters, prompt version and bindings for each bound node
  are rendered
- **AND** determining what the change configures does not require resolving the indirection by hand.

### Requirement: The resolver SHALL read the embedded document first, then a configured local override, then a remote source only if explicitly enabled

At run time the resolver SHALL consult, in order: the binding document **embedded in the built
artifact**, then a **local override document** if one is configured, then a **remote document** only if
remote resolution is explicitly enabled.

#### Scenario: The embedded document requires nothing external

- **WHEN** no override source is configured
- **THEN** the resolver uses the embedded document
- **AND** resolution succeeds with no external dependency.

#### Scenario: Remote resolution is off unless enabled

- **WHEN** remote resolution has not been explicitly enabled
- **THEN** no remote source is contacted
- **AND** the running program has no runtime dependency on the platform.

### Requirement: Resolution SHALL be fail-static and SHALL NOT block process startup

When a configured override source is unreachable, unparseable, or fails validation, the resolver SHALL
retain the last known-good document, SHALL report a **degraded** state, SHALL NOT fall back to an
arbitrary, empty, or default configuration, and SHALL NOT prevent the process from starting.

#### Scenario: An unreachable override leaves the last known-good configuration in force

- **WHEN** a configured override source cannot be reached
- **THEN** the last known-good document remains in force
- **AND** the resolver reports a degraded state.

#### Scenario: An invalid override is not adopted

- **WHEN** a configured override source returns a document that is unparseable or fails validation
- **THEN** the document is not adopted
- **AND** the last known-good document remains in force and the degraded state is reported.

#### Scenario: Startup succeeds with every override source unavailable

- **WHEN** the process starts and every configured override source is unavailable
- **THEN** the process starts using the embedded document
- **AND** resolution failure does not become a startup failure.

#### Scenario: Degradation is reported, not silent

- **WHEN** the resolver is in a degraded state
- **THEN** that state is observable on a readable endpoint and in telemetry
- **AND** it is not inferable only from the configuration's contents.

### Requirement: The resolver SHALL emit the config_hash of the document it actually resolved on every invocation

The resolver SHALL emit, on **every** node invocation, the configuration hash of the document it
actually resolved, as part of the standard telemetry tag set.

#### Scenario: Every invocation carries its resolved hash

- **WHEN** a node is invoked
- **THEN** the emitted telemetry carries the configuration hash of the document actually in force
- **AND** the value reflects what was resolved, not what was requested.

#### Scenario: The tag is present in the degraded state

- **WHEN** a node is invoked while the resolver is degraded
- **THEN** the emitted hash is that of the document actually in force
- **AND** the degraded state is emitted alongside it.

### Requirement: A run whose resolved config_hash differs from the requested one SHALL fail rather than be scored

The evaluation harness SHALL compare the resolved configuration hash observed on a run's invocations
against the configuration hash requested for that run, and SHALL **fail** the run on any mismatch
rather than recording results under the requested hash.

#### Scenario: A mismatched run fails

- **WHEN** a run's invocations report a resolved configuration hash different from the requested one
- **THEN** the run fails
- **AND** no results are recorded under the requested configuration hash.

#### Scenario: Reconciliation covers every invocation

- **WHEN** any single invocation in a run reports a differing resolved hash
- **THEN** the run fails
- **AND** it is not partially scored from the invocations that matched.

#### Scenario: A matching run is scored normally

- **WHEN** every invocation reports the requested configuration hash
- **THEN** the run proceeds to scoring.

### Requirement: Evaluation and verification runs SHALL resolve with override sources disabled

During evaluation and verification runs the resolver SHALL be **pinned**: it SHALL read only the
embedded document, and override sources SHALL be disabled.

#### Scenario: A measurement run ignores an override source

- **WHEN** an evaluation or verification run executes with an override source configured
- **THEN** the override is not consulted
- **AND** the run resolves the embedded document.

#### Scenario: A measurement run cannot silently measure a different configuration

- **WHEN** a measurement run is executed
- **THEN** the configuration it measures is the one shipped in the built artifact
- **AND** no runtime source can substitute a different configuration for it.

### Requirement: Resolving to a configuration with no verified delta SHALL be permitted, marked unverified, and refusable by automation level

The binding document SHALL record the configuration hash that carried a **verified delta**. Resolving
to a configuration with no verified-delta record SHALL be permitted, SHALL be **marked unverified** on
every invocation and wherever the configuration is displayed, and SHALL be **refusable by automation
level**.

#### Scenario: An unverified configuration is marked, not blocked

- **WHEN** the resolver resolves a configuration that carries no verified-delta record
- **THEN** invocations proceed
- **AND** each is marked unverified in telemetry.

#### Scenario: Unverified is visible where the configuration is displayed

- **WHEN** a configuration with no verified-delta record is displayed
- **THEN** it is shown as unverified
- **AND** it is visually distinguishable from a configuration that carries a verified delta.

#### Scenario: The highest automation level refuses an unverified resolution

- **WHEN** the automation level is one that requires verified configurations and the resolver resolves
  one carrying no verified-delta record
- **THEN** the resolution is refused
- **AND** the refusal is reported rather than silently downgraded.

### Requirement: The generated binding artifact SHALL be deterministic and byte-identically regenerable

The generated binding artifact and document SHALL be deterministic: regenerating them from the same
configuration SHALL produce byte-identical output. The artifact SHALL carry no external dependencies.

#### Scenario: Regeneration is byte-identical

- **WHEN** the artifact and document are regenerated from the same configuration
- **THEN** the output is byte-identical to the previous generation.

#### Scenario: The artifact introduces no dependency

- **WHEN** the generated artifact is inspected
- **THEN** it declares no external dependency
- **AND** building it requires nothing that the target did not already require.

### Requirement: A bound change SHALL be revertible by a single revert

A `bound` change SHALL be revertible in a **single** version-control revert covering the rewritten call
site, the generated artifact, and the binding document together.

#### Scenario: One revert restores the prior configuration

- **WHEN** a `bound` change is reverted
- **THEN** the call site, the generated artifact, and the binding document are all restored to their
  prior state
- **AND** no part of the change survives the revert.
