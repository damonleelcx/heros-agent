# Forge Delivery — Delta (P35)

[ADR-005](../../../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md) made the customer's own
CI the default so that no write-scoped forge credential is held by the platform. Program ruling **R3**
amends that default **for console-driven runs only**: the hosted Git App opens the pull request, because a
console customer arrived with no CI integration and no CLI, and telling them to configure one is telling
them the product does not work.

> 🔴 **This delta changes exactly one requirement and adds two.** Everything else that makes delivery safe
> is **already folded into [`../../../../specs/forge-delivery/spec.md`](../../../../specs/forge-delivery/spec.md)
> and is not restated here** — restating a requirement as `ADDED` would make the delta claim to introduce
> behaviour that already exists, and a reader could not tell what this change actually does.
>
> Unchanged and load-bearing for this phase, by their folded headers:
> - *Only a change that passed the verification gate SHALL be delivered*
> - *Re-delivering the same change to the same target SHALL update the existing pull request*
> - *The platform SHALL NOT merge a pull request below the Autonomous automation level*
> - *A hosted application installation SHALL be per-repository, least-privilege, and customer-revocable*
> - *Every pull request SHALL carry its evidence*
> - *A forge credential SHALL NOT be logged, embedded, or transmitted outside the platform*
> - *Delivery SHALL be entitlement-gated server-side*
> - *An active halt SHALL stop delivery, and an unreadable halt state SHALL fail closed*
>
> `tasks.md` §7 re-runs the fences for each of these **through the conversational path**, because the
> requirement holding for one caller says nothing about a new one.

## MODIFIED Requirements

### Requirement: CI-mediated delivery SHALL be the default and SHALL NOT require a platform-held forge credential

The default becomes **per surface**. CLI- and CI-originated runs are unchanged: CI-mediated, with no
platform-held forge credential. Console-driven runs use the hosted Git App installation, because the
CI-mediated path requires an integration a console customer does not have.

#### Scenario: A CLI- or CI-originated run
- **WHEN** a run originates from the CLI or a CI job
- **THEN** delivery is CI-mediated
- **AND** the platform receives, stores and requests no forge credential for that run

#### Scenario: A console-driven run with an installation
- **WHEN** a run originates in the console and the tenant has a hosted Git App installation for the target repository
- **THEN** the platform opens the pull request using that installation

#### Scenario: A console-driven run with no installation
- **WHEN** a run originates in the console and no installation exists for the target repository
- **THEN** delivery is withheld and the surface states that an installation is required
- **AND** the verified diff and its evidence remain available

#### Scenario: The default changes the mode, not the scope
- **WHEN** the console default causes an installation to be used
- **THEN** that installation is still per-repository, least-privilege and customer-revocable
- **AND** no broader scope is requested because it became a default

## ADDED Requirements

### Requirement: The read connection and the write installation SHALL be separate grants with independent revocations

The read connection is new in [P32](../../../p32-repo-intake/); before it, there was no second grant to
confuse a write installation with.

#### Scenario: Two grants for one repository
- **WHEN** a tenant has both a source-read connection and a write installation for the same repository
- **THEN** they are two grants with two scopes
- **AND** revoking one does not revoke or degrade the other

#### Scenario: Neither grant implies the other
- **WHEN** a tenant authorizes a source-read connection
- **THEN** no write capability is created
- **AND** the converse also holds

### Requirement: Revoking a write installation SHALL stop pushes immediately rather than at the next token refresh

#### Scenario: Immediate revocation
- **WHEN** a customer revokes the hosted Git App installation
- **THEN** the platform cannot push to that repository from that moment
- **AND** an in-flight delivery that has not yet pushed does not push

#### Scenario: A revoked installation during a run
- **WHEN** an installation is revoked while a run is between approval and delivery
- **THEN** delivery is withheld with the revocation named as the cause
- **AND** the run does not retry against the revoked installation
