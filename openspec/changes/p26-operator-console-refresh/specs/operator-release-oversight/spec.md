# Operator Release Oversight — Spec Delta (P26)

Product rationale: [`../../../../../docs/prd/P26-operator-console-refresh.md`](../../../../../docs/prd/P26-operator-console-refresh.md)
§6 (FR53–FR58), §9.6 (DevOps lens). Technical decisions: [`../../design.md`](../../design.md) D2, D9.

Covers the oversight the platform discovered it needed the hard way. P20 shipped a signing pipeline, five
install channels and a self-update path — and rotated the signing key mid-flight after the key was leaked.
"Which key is active, which are retired and when, and which published artefacts were signed with a retired
one" was an incident question with no surface behind it.

> Two asymmetries carry this capability. **A sequence is not a state** — publish, verify and smoke happen in
> order, and a release that publishes green and smokes red is precisely the state that reaches a stranger's
> laptop, so the surface shows where the sequence stopped rather than only its outcome. And **queued is not
> failed** — a retired runner label queues until timeout rather than failing, so rendering that as *failed*
> sends an engineer to debug a build that never ran. That is a measured lesson, not a hypothetical.

## ADDED Requirements

### Requirement: The console SHALL show published releases per install channel

The surface SHALL show, per channel, the published versions with their publication dates and the artefacts
published per platform.

#### Scenario: An operator sees what each channel serves
- **WHEN** an operator with the granted capability opens the release surface
- **THEN** each install channel shows its published versions and their dates
- **AND** each version shows the artefacts published per platform.

#### Scenario: A platform with no artefact is visible as such
- **WHEN** a version was published for some platforms and not others
- **THEN** the missing platform is shown as absent
- **AND** the version is not presented as complete.

### Requirement: The console SHALL show the active signing key and every retired key with its rotation date and reason

The surface SHALL identify the active signing key and every retired key, each with its rotation date and
the reason recorded for the rotation. It SHALL identify published artefacts that were signed with a
retired key.

#### Scenario: An incident question is answerable from the console
- **WHEN** an operator asks which signing key is active and which are retired
- **THEN** the surface answers with the active key and each retired key's rotation date and reason.

#### Scenario: Artefacts signed with a retired key are identifiable
- **WHEN** a key has been retired
- **THEN** the published artefacts signed with it are identifiable on the surface
- **AND** they are distinguishable from artefacts signed with the active key.

### Requirement: No key material SHALL appear on any surface, and no operation SHALL produce it

A key SHALL be identified by its identifier and fingerprint only. The surface SHALL offer no key
generation, no key export, and no operation whose output is key material.

#### Scenario: A key is identified without being disclosed
- **WHEN** a signing key is rendered
- **THEN** only its identifier and fingerprint appear
- **AND** no private or secret key material appears in the page, in a read model, or in a log line.

#### Scenario: The surface cannot become a disclosure path
- **WHEN** the surface's controls are enumerated
- **THEN** none generates, exports or otherwise emits key material
- **AND** the assertion establishing this fails if such a control is added.

### Requirement: Artefact verification SHALL have three states

The surface SHALL distinguish `verified`, `failed verification` and `not yet verified`.

#### Scenario: Unchecked is not passed
- **WHEN** an artefact's checksum and signature have not yet been verified
- **THEN** the surface shows `not yet verified`
- **AND** it does not show `verified`.

#### Scenario: Unchecked is not failed either
- **WHEN** an artefact has not yet been verified
- **THEN** the surface does not show `failed verification`.

### Requirement: Post-publish smoke SHALL have three states, including queued-until-timeout

The surface SHALL show the post-publish smoke result per platform image and SHALL distinguish `passed`,
`failed` and `queued until timeout`.

#### Scenario: A queued run is not rendered as a failure
- **WHEN** a smoke job never started because its runner label queued until timeout
- **THEN** the surface shows `queued until timeout`
- **AND** it does not show `failed`, which would send an engineer to debug a build that never ran.

#### Scenario: A green publish with a red smoke is visible
- **WHEN** a release published successfully and its smoke failed
- **THEN** the surface shows the failure against that release
- **AND** the release is not presented as successfully delivered.

### Requirement: The surface SHALL show where the publish sequence stopped

The surface SHALL render the publish → verify → smoke progression and where it stopped, not only its final
state.

#### Scenario: The stopping point is legible
- **WHEN** a release's sequence stopped partway
- **THEN** the surface shows which step completed and which did not
- **AND** an operator can tell a not-yet-run step from a failed one.

### Requirement: The release surface SHALL be read-only in this phase

No control on this surface SHALL halt a channel, unpublish an artefact, re-sign, re-publish or re-run a
smoke job. A halting control is a separate decision with its own design.

#### Scenario: No control changes what customers receive
- **WHEN** the release surface's controls are enumerated
- **THEN** none halts, unpublishes, re-signs or re-publishes anything
- **AND** the surface shows a problem it cannot act on, which is the deliberate boundary of this phase.
