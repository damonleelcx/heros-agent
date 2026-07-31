# Legal Documents — Spec Delta (P24)

Product rationale: [`../../../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR40–FR41), §9.8 (Sales lens). Technical decisions: [`../../design.md`](../../design.md) D7.

This delta amends the P23 legal surface
([`../../../p23-legal-and-docs/specs/legal-documents/spec.md`](../../../p23-legal-and-docs/specs/legal-documents/spec.md))
for one reason: before this phase the platform had **no** third-party processors on any customer path, so
"the stores that exist" was a complete description of where data goes. It no longer is. Three processors
now receive something, and a data-protection reviewer's first question is who they are.

## ADDED Requirements

### Requirement: The legal surface SHALL publish a versioned sub-processor document

The document SHALL name each third-party processor, the data categories it receives, the surfaces it runs
on, and its jurisdiction. It SHALL carry the same identity front matter, build-time content hash and
permanent addressability as every other legal document, and it SHALL be reachable from the Privacy
Notice.

#### Scenario: A reviewer can enumerate every processor
- **WHEN** a data-protection reviewer opens the sub-processor document
- **THEN** each processor is named with its data categories, the surfaces it runs on and its jurisdiction
- **AND** no processor receiving anything on a customer path is absent from the list.

#### Scenario: A superseded version stays addressable
- **WHEN** a new version of the sub-processor document is published
- **THEN** the prior version remains permanently addressable at its content hash
- **AND** the current version states its effective date.

#### Scenario: A named surface matches what ships
- **WHEN** the document states which surfaces a processor runs on
- **THEN** that statement matches the origin allowlist and the per-prefix policies that ship
- **AND** a mismatch fails the build rather than reaching the page.

### Requirement: A material sub-processor version SHALL invalidate the affected analytics-consent grants

Publishing a sub-processor version declared material SHALL return the affected non-essential consent
categories to `not-asked` and stop their collection until a new grant is given. A version declared
non-material SHALL ask nobody.

#### Scenario: Adding a processor re-asks
- **WHEN** a sub-processor version declared material is published because a processor was added
- **THEN** the affected categories return to `not-asked` for every visitor
- **AND** collection for those categories stops until re-granted.

#### Scenario: A wording fix asks nobody
- **WHEN** a sub-processor version declared non-material is published
- **THEN** no visitor is re-asked and every existing grant remains in force.

## MODIFIED Requirements

### Requirement: The Privacy Notice SHALL describe the stores that exist and assert only rights with an implemented route

The Privacy Notice's substance SHALL derive from a committed **data inventory** naming actual stores, data
categories, retention and processors, each checkable against the repository or explicitly marked external.
It SHALL assert **only** data-subject rights for which an implemented request route exists, SHALL name
that route, and SHALL state the response commitment.

It SHALL additionally describe, per **deployment shape**, which third-party processors receive anything —
naming them for the platform's own hosted deployment, and stating that a customer-installed deployment
transmits to none of them. The claim about a customer-installed deployment SHALL be the one the package
build asserts, not a separate assurance.

#### Scenario: Every named store is checkable
- **WHEN** the Privacy Notice names a store that holds customer-derived data
- **THEN** that store appears in the committed data inventory
- **AND** the inventory entry either resolves to a migration in the repository or is explicitly marked
  external.

#### Scenario: No right is asserted without a route
- **WHEN** the Privacy Notice states a data-subject right
- **THEN** it names the request route by which that right is exercised and the response commitment
- **AND** it does not imply a self-serve mechanism that does not exist.

#### Scenario: The deployment shape changes the answer, and the notice says so
- **WHEN** a reader asks which third parties receive data
- **THEN** the Privacy Notice answers separately for the platform's hosted deployment and for a
  customer-installed deployment
- **AND** the customer-installed answer is the zero-external-origin property the package build asserts.

#### Scenario: A stale tracking claim fails the build
- **WHEN** any published claim about tracking, analytics or third-party code contradicts the shipped
  origin allowlist and per-prefix policies
- **THEN** the claims fence fails the build, naming the claim
- **AND** the claim is corrected rather than the fence being relaxed.
