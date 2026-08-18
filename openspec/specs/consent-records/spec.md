# Consent Records — Spec (folded from P23)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR10–FR16), §7 (NFR7–NFR9) and §9.3 (Backend lens). Technical decisions:
[`../../changes/archive/2026-08-01-p23-legal-and-docs/design.md`](../../changes/archive/2026-08-01-p23-legal-and-docs/design.md) Decisions 2, 3, 4 and 5.

Covers the record that answers, years later, *"what exactly did this customer accept, and when?"* — and the
gate that asks for acceptance without becoming a way to take the console down.

> Two asymmetries carry this capability. **Fail-closed on the commitment, fail-open on reading**: an
> unrecorded acceptance stops a checkout and never stops a reader. And **the direction of the error
> matters**: an acknowledged consent with no row is indistinguishable from consent that never happened, so
> the row is written before the acknowledgement, always.

## Requirements

### Requirement: Acceptance SHALL be recorded append-only against an immutable document identity

The platform SHALL record each acceptance as `(tenant_id, principal_id, document_kind, document_version,
content_hash, accepted_at, method)`. Records SHALL NOT be updated in place; a withdrawal or a re-acceptance
SHALL be a new record.

#### Scenario: An acceptance resolves to an exact text later
- **WHEN** an auditor asks what a tenant accepted on a given date
- **THEN** the record yields a document kind, version and content hash
- **AND** that hash resolves to an archived text that is still served.

#### Scenario: Re-acceptance does not overwrite history
- **WHEN** a principal accepts a later version of a document they previously accepted
- **THEN** a new record is written
- **AND** the earlier record remains readable and unmodified apart from its supersession marker.

### Requirement: Acceptance SHALL be idempotent, enforced by a database constraint

Re-submitting the same `(tenant_id, principal_id, document_kind, document_version)` SHALL create no second
record and SHALL return success. Idempotency SHALL be enforced by a uniqueness constraint in the schema, not
by application-level check-then-insert.

#### Scenario: A double-clicked button produces one record
- **WHEN** the same acceptance is submitted twice concurrently
- **THEN** exactly one record exists afterwards
- **AND** both requests return success.

#### Scenario: The guarantee is proven against a real database
- **WHEN** the idempotency test runs
- **THEN** it exercises the uniqueness constraint against a real Postgres instance
- **AND** a passing in-memory fake alone is not accepted as proof.

### Requirement: The platform SHALL validate the submitted content hash server-side

The submitted `content_hash` SHALL be checked against the legal manifest the server holds. A submission whose
hash does not match a published version of that kind SHALL be rejected.

#### Scenario: A client cannot invent what it accepted
- **WHEN** a client submits an acceptance with a content hash that matches no published version
- **THEN** the request is rejected and no record is written.

#### Scenario: A stale page cannot record acceptance of a version it did not show
- **WHEN** a client submits a hash for a version other than the one it was served
- **THEN** the mismatch is detected server-side and the request is rejected.

### Requirement: Only a MATERIAL new version SHALL require re-acceptance

Publishing a version declared `material: true` SHALL mark prior acceptances of that kind superseded and SHALL
require re-acceptance from each affected principal at their next commitment. Publishing a version declared
`material: false` SHALL require nothing.

#### Scenario: A material change is asked again
- **WHEN** a version declared material is published and an existing principal reaches a commitment
- **THEN** they are asked to accept the new version before that commitment proceeds.

#### Scenario: A typo fix asks nobody
- **WHEN** a version declared non-material is published
- **THEN** no principal is asked to re-accept
- **AND** no existing acceptance is marked superseded.

### Requirement: The gate SHALL block new commitments only, never reading or in-flight work

Acceptance SHALL be demanded at first sign-in for a principal with no acceptance, at checkout and at plan
change. It SHALL NOT block reading the console, an in-flight run, or a legal document itself. An existing
session SHALL receive a non-blocking notice naming the document and its effective date.

#### Scenario: A pending acceptance does not interrupt work
- **WHEN** a material version is published while a customer has a run in flight
- **THEN** the console remains usable and the run is unaffected
- **AND** a non-blocking notice names the document and its effective date.

#### Scenario: A pending acceptance never hides the document
- **WHEN** a principal with a pending acceptance opens the legal document itself
- **THEN** the document renders in full
- **AND** no acceptance is required to read it.

#### Scenario: A commitment is gated
- **WHEN** a principal with a pending acceptance begins checkout or a plan change
- **THEN** the commitment does not proceed until acceptance is recorded.

### Requirement: A failed write SHALL never render as acceptance

The acknowledgement SHALL follow the committed write. If the record cannot be written, the commitment SHALL
NOT proceed and the interface SHALL NOT display the acceptance as recorded.

#### Scenario: The store is unavailable
- **WHEN** the acceptance write fails
- **THEN** the interface states plainly that the acceptance was not recorded and nothing has been agreed
- **AND** it offers a retry
- **AND** the gated commitment does not proceed.

#### Scenario: No optimistic acknowledgement
- **WHEN** an acceptance request is in flight
- **THEN** no success state is displayed before the write is committed.

### Requirement: A tenant SHALL be able to read its own acceptance history

The console SHALL show the tenant's own acceptances — document, version, date and principal — each linking to
the exact archived text accepted. The endpoint SHALL expose only the calling tenant's records.

#### Scenario: An administrator answers their own audit
- **WHEN** an administrator opens the acceptance history in the console
- **THEN** each entry shows document, version, acceptance date and principal
- **AND** each entry links to the archived text carrying the accepted content hash.

#### Scenario: No cross-tenant read exists on this path
- **WHEN** any caller requests acceptances
- **THEN** only the calling tenant's records are returned
- **AND** no parameter on this path can widen the scope to another tenant.

### Requirement: Consent records SHALL outlive the identity and hold no personal data beyond an opaque principal

Records SHALL be retained for a configured statutory period, SHALL NOT be deleted by tenant deletion, and
SHALL survive identity erasure with the subject tombstoned. A record SHALL contain no email, no name and no
free text.

#### Scenario: Erasure leaves the evidence intact
- **WHEN** an identity erasure request is executed for a principal
- **THEN** the consent record retains document kind, version, content hash and timestamp
- **AND** the subject is tombstoned rather than the record deleted.

#### Scenario: The record is minimal by construction
- **WHEN** a consent record is inspected
- **THEN** it contains no email address, no personal name and no free-text field
- **AND** its subject is an opaque principal identifier.

#### Scenario: Retention is executable before it is executed
- **WHEN** the retention job is run in dry mode
- **THEN** it reports exactly which records it would remove and removes none.
