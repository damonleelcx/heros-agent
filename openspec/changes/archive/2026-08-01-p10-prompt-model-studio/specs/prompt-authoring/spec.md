# Prompt Authoring — Spec Delta (P10)

Product rationale: [`../../../../../docs/prd/P10-prompt-model-studio.md`](../../../../../docs/prd/P10-prompt-model-studio.md)
§6 (FR1–FR6) and §7.

Covers the missing half of "prompt versioning **and edits**": an authenticated write path to publish a
prompt version, a legible version history, a diff that reports the **slot-set change separately from
the body text**, and an **impact analysis before publish** so a user learns that an added variable
would un-apply a node while still in the editor rather than at transform time.

> **Extends, does not replace, P2 `registries`.** Content addressing, envelope sealing, immutability
> and template parsing are already specified there and implemented in `internal/registry`. This
> capability adds the human-facing operations over them and adds no registry table — the timeline,
> diff and impact analysis are **read models over rows that already exist**.

## ADDED Requirements

### Requirement: The platform SHALL expose an authenticated write path that publishes a prompt version

The platform SHALL provide an authenticated interface that publishes a prompt version using the
existing content-addressed registry semantics. Publishing content identical to an existing version
SHALL return that version's identifier rather than creating a duplicate.

#### Scenario: Publishing new content creates a new version

- **WHEN** an authenticated user publishes a prompt body that differs from every existing version of
  that name
- **THEN** a new version is created with its own content-addressed identifier
- **AND** the identifier is derived from the content, not assigned sequentially.

#### Scenario: Publishing identical content is idempotent

- **WHEN** a user publishes a prompt body byte-identical to an existing version of the same name
- **THEN** the existing version's identifier is returned
- **AND** no duplicate version is created.

#### Scenario: Publishing requires authentication and is tenant-scoped

- **WHEN** an unauthenticated request attempts to publish a prompt version
- **THEN** the request is refused and no version is created
- **AND** an authenticated request is scoped to the caller's tenant, which cannot be widened by a
  client-supplied identifier.

### Requirement: A published prompt version SHALL never be mutated or deleted through any interface

No interface — HTTP, CLI, UI, or library — SHALL express mutation or deletion of a published prompt
version. An edit SHALL produce a new version, and every prior version SHALL remain resolvable by any
Variant Spec that pins it.

#### Scenario: Editing produces a new version and leaves the prior one intact

- **WHEN** a user edits a prompt and publishes the result
- **THEN** a new version is created
- **AND** the prior version remains resolvable and renders exactly as it did before the edit.

#### Scenario: No interface offers mutation or deletion

- **WHEN** the platform's prompt operations are enumerated
- **THEN** none of them mutates or deletes a published version
- **AND** immutability does not depend on callers choosing not to attempt it.

#### Scenario: A specification pinning an older version keeps resolving

- **WHEN** a newer version of a prompt exists and a Variant Spec pins an older one
- **THEN** the spec resolves to the pinned version
- **AND** the newer version does not affect its resolution.

### Requirement: A malformed prompt template SHALL be rejected at publish time with its offending position identified

A template that does not parse SHALL be rejected when it is published, and the rejection SHALL identify
where the template is malformed. A malformed template SHALL NOT be stored and deferred to render time.

#### Scenario: A malformed slot is rejected at publish

- **WHEN** a user publishes a body containing a slot expression that does not parse
- **THEN** publication fails and the failure identifies the offending position
- **AND** no version is created.

#### Scenario: A parse failure is not deferred to render time

- **WHEN** a body would fail to parse
- **THEN** the failure occurs at publish
- **AND** it is not possible to store a version whose template fails only when it is rendered.

### Requirement: The platform SHALL return the version timeline for a prompt name

The platform SHALL return, for a prompt name, its versions in a defined order, each with its version
identifier, its slot set, and its creation metadata.

#### Scenario: The timeline lists every version with its slot set

- **WHEN** the timeline for a prompt name is requested
- **THEN** every version of that name is returned in a defined order
- **AND** each entry carries its identifier, its slot set, and its creation metadata.

#### Scenario: A name with no versions is an empty timeline, not an error

- **WHEN** the timeline is requested for a name that has no versions
- **THEN** an empty timeline is returned
- **AND** it is distinguishable from a failure to retrieve the timeline.

### Requirement: The platform SHALL diff two prompt versions and report the slot-set change separately from the body change

The platform SHALL produce a diff between any two versions of a prompt covering the body text **and**
the slot set. Slots added and slots removed SHALL be reported **explicitly**, not left to be inferred
from the body diff.

#### Scenario: An added slot is reported explicitly

- **WHEN** two versions are diffed and the later declares a slot the earlier did not
- **THEN** that slot is reported as added, separately from the body text difference
- **AND** identifying it does not require reading the body diff.

#### Scenario: A removed slot is reported explicitly

- **WHEN** two versions are diffed and the earlier declared a slot the later does not
- **THEN** that slot is reported as removed, separately from the body text difference.

#### Scenario: A wording change with an unchanged slot set is distinguishable

- **WHEN** two versions differ in body text but declare the same slots
- **THEN** the diff reports a body change and an unchanged slot set
- **AND** it is distinguishable from a diff that also changes the slot set.

### Requirement: The platform SHALL report which nodes a proposed prompt edit would prevent from transforming, before it is published

For a proposed prompt body, the platform SHALL report which nodes currently pinning that prompt would
**fail to transform** under the proposed slot set, and the reason for each. The analysis SHALL be
available **before** the version is published, and SHALL **name any node it could not analyze**.

#### Scenario: Adding an unbindable slot is reported before publish

- **WHEN** a proposed body adds a slot that a node pinning that prompt cannot satisfy
- **THEN** that node is reported as blocked, with the reason
- **AND** the report is available before the version is published.

#### Scenario: A safe edit reports no blocked nodes

- **WHEN** a proposed body changes only wording and leaves the slot set unchanged
- **THEN** no node is reported as blocked.

#### Scenario: Nodes that could not be analyzed are named, not omitted

- **WHEN** the analysis cannot determine the outcome for a node
- **THEN** that node is reported as unanalyzed with the reason
- **AND** it is not silently excluded, because an absent entry would read as a clean result.
