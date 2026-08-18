# Source Acquisition — Spec (P32)

Implements [ADR-013](../../../../../docs/adr/ADR-013-source-acquisition-posture.md). Three modes behind one
interface: a pushed bundle (default), a per-repository read grant (opt-in), and a local path (no
transfer).

## ADDED Requirements

### Requirement: The system SHALL produce a source snapshot that carries no trace of which mode produced it

#### Scenario: Identical IR across modes
- **WHEN** the same tree at the same revision is ingested by bundle push, by clone, and by the local bridge
- **THEN** discovery over each snapshot produces an identical Workflow IR
- **AND** no consumer of `sourceingest.Source` can determine which mode was used

#### Scenario: A snapshot without a revision
- **WHEN** a snapshot is requested without a revision
- **THEN** it is refused
- **AND** the refusal states that "the latest source" is not a reproducible subject

#### Scenario: No source pushed or connected
- **WHEN** a tenant has provided no source for a workflow at a revision
- **THEN** `ErrNoSource` is returned as a first-class state
- **AND** it is distinguishable from a store that could not be read

### Requirement: The system SHALL scope a repository connection to exactly one repository, read-only

#### Scenario: One repository per connection
- **WHEN** a customer authorizes a connection
- **THEN** the resulting grant permits reading exactly the repository named
- **AND** an authorization that would cover repositories the customer did not name is refused

#### Scenario: No write scope
- **WHEN** a connection is established
- **THEN** the grant carries no scope that can write a ref, open a pull request, or change a repository setting

#### Scenario: Read and write grants are separate installations
- **WHEN** a tenant has both a read connection and a hosted Git App write installation for the same repository
- **THEN** they are two separate grants with two separate scopes and two separate revocations

### Requirement: The system SHALL disclose what a connection permits before it is authorized

#### Scenario: Consent states unattended use
- **WHEN** a customer is asked to authorize a connection
- **THEN** they are shown what the grant permits, that it is usable when they are not present, and how to revoke it
- **AND** authorization cannot be completed without that disclosure having been displayed

### Requirement: The system SHALL make revocation delete the grant and every tree derived from it

#### Scenario: Revocation cascades
- **WHEN** a customer revokes a connection
- **THEN** the stored grant is deleted
- **AND** every source snapshot derived from that connection is deleted from storage
- **AND** a subsequent read returns `ErrNoSource` rather than a cached answer

### Requirement: The system SHALL append a record for every clone

#### Scenario: Per-use record
- **WHEN** a clone is performed
- **THEN** a record of `(tenant, repository, revision, actor, reason)` is appended
- **AND** the record is readable by the customer

#### Scenario: Attended and unattended reads are distinguishable
- **WHEN** a clone is initiated by a scheduled or autonomous process rather than by a person
- **THEN** the record's actor distinguishes it from a person-initiated read

### Requirement: The system SHALL apply the same hostile-input defences to a cloned tree as to an uploaded archive

#### Scenario: Escaping symlink in a cloned repository
- **WHEN** a cloned repository contains a symlink whose target resolves outside the tree
- **THEN** it is refused on the clone path
- **AND** the refusal is the same class of refusal the bundle path produces

#### Scenario: Size and entry ceilings
- **WHEN** a cloned repository exceeds the entry-count, per-file size, or total-bytes ceiling
- **THEN** the ingest is refused before discovery walks it

### Requirement: The system SHALL report a clone failure by cause and SHALL NOT fall back to an older snapshot

#### Scenario: Four causes stay four
- **WHEN** a clone fails
- **THEN** the reported cause is exactly one of credential rejected, repository not found, revision not found, or network failure
- **AND** each renders a distinct message

#### Scenario: No silent degradation
- **WHEN** a clone fails and an older snapshot exists for the same workflow
- **THEN** the older snapshot is NOT served in its place
- **AND** the surface reports the failure

### Requirement: The system SHALL NOT gate any feature on a repository connection

#### Scenario: Bundle path loses nothing
- **WHEN** a tenant uses only bundle push
- **THEN** every capability available to a connected tenant is available to them

#### Scenario: A surface with no snapshot
- **WHEN** a surface would need a snapshot the tenant has not provided
- **THEN** it renders `not reported`
- **AND** it does not prompt for a connection as a precondition

### Requirement: The system SHALL read a local repository without transmitting its contents

#### Scenario: No repository content leaves the machine
- **WHEN** a local repository is assessed through the local bridge
- **THEN** no file content, prompt text, or diff is transmitted
- **AND** an egress capture during the assessment shows none

#### Scenario: Deployment applicability is stated up front
- **WHEN** the local mode is offered in the console
- **THEN** the console states which deployments it works against
- **AND** it does not fail at the end of the flow for a deployment it never supported

### Requirement: The system SHALL enforce retention on a cloned snapshot identically to a pushed one

#### Scenario: Expired snapshot removed
- **WHEN** a snapshot passes its retention window
- **THEN** it is deleted regardless of the mode that produced it

#### Scenario: Retention is observable
- **WHEN** the retention job runs
- **THEN** its last successful run is readable from a health endpoint
- **AND** consecutive failures escalate rather than remaining at WARN

### Requirement: The system SHALL report ingest outcomes broken out by forge and by failure cause

#### Scenario: No hiding a broken adapter in an aggregate
- **WHEN** ingest metrics are reported
- **THEN** they are broken out per forge and per failure cause
- **AND** a single aggregate success rate is not the only figure available
