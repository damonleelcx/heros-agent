# Legal Documents — Spec (folded from P23, P24)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR1–FR9), §7 (NFR1, NFR6, NFR7, NFR11) and §9.8 (Sales Operations lens). Technical decisions:
[`../../changes/archive/2026-08-01-p23-legal-and-docs/design.md`](../../changes/archive/2026-08-01-p23-legal-and-docs/design.md) Decisions 1, 2, 3 and 10.

Covers the two documents the product cannot be sold without — a **Terms of Service** and a **Privacy
Notice** — published as versioned artifacts rather than as pages.

> The distinction that carries the whole capability: a legal document is not a URL, it is a **text**. A
> consent record that points at a URL records nothing, because the URL's contents are free to change. So a
> document's identity is `(kind, version, content_hash)`, every version is served forever, and deleting one
> is a build failure rather than a cleanup.

## Requirements

### Requirement: The console SHALL publish a Terms of Service and a Privacy Notice readable with no session and no platform call

Both documents SHALL be served from the console at stable public routes, SHALL require no session, and SHALL
make **no request to the platform** to render.

#### Scenario: The documents serve during a platform outage
- **WHEN** the platform is unreachable and a reader opens the Terms of Service or the Privacy Notice
- **THEN** the page renders in full with a 200 response
- **AND** the test harness records **zero** upstream requests for that render.

#### Scenario: No session is required or created
- **WHEN** a reader with no session and no cookies opens either document
- **THEN** the document renders in full
- **AND** the response sets no session cookie and triggers no redirect to sign-in.

### Requirement: Every legal document SHALL declare its identity in machine-readable front matter

Each document SHALL declare `kind`, `version`, `effective_date`, `authoritative_language`, `supersedes` and
`material`. A document missing any of these fields SHALL fail the build.

#### Scenario: A missing declaration fails the build
- **WHEN** a legal document is added or edited without a `material` declaration (or any other required field)
- **THEN** the build fails naming the document and the missing field
- **AND** the document does not reach a rendered page.

#### Scenario: The declared identity is visible to the reader
- **WHEN** a reader opens a legal document
- **THEN** the kind, version, effective date and authoritative language are shown on the page
- **AND** the authoritative language is stated explicitly, so a future translation cannot be mistaken for the
  governing text.

### Requirement: A legal document's identity SHALL include a content hash computed at build time

The content hash SHALL be computed over **normalized source** (front matter excluded; line endings and
trailing whitespace normalized), SHALL be displayed on the page and in the print rendering, and SHALL change
if and only if the words change.

#### Scenario: A word change changes the hash
- **WHEN** any word of a published document's body is changed
- **THEN** the computed content hash differs from the previously published hash.

#### Scenario: A reformat that changes no words does not change the hash
- **WHEN** a document's source is re-wrapped or its line endings change, with no change to its words
- **THEN** the computed content hash is unchanged.

#### Scenario: Editing a body without bumping the version fails the build
- **WHEN** a document's body changes while its `version` stays the same as an already-published version
- **THEN** the build fails, naming the document and the version whose hash no longer matches.

### Requirement: Every superseded version SHALL remain permanently addressable

Each published version SHALL be served at its own permanent route. Removing a published version SHALL fail
the build, because every consent record referencing it would be orphaned.

#### Scenario: An old version still resolves after a new one is published
- **WHEN** a new version of the Terms is published and a reader opens the route of a superseded version
- **THEN** the superseded text renders in full at its own route
- **AND** the page states that it is superseded, names the current version and links to it
- **AND** the reader is **not** redirected to the current version.

#### Scenario: Deleting an archived version fails the build
- **WHEN** a published version's source file is removed while the manifest still lists it
- **THEN** the build fails naming the unresolvable version.

### Requirement: The console SHALL serve a static legal manifest

A machine-readable manifest SHALL list every legal document — current and historical — with kind, version,
effective date, content hash, route and materiality, and SHALL be resolvable with no session.

#### Scenario: The manifest resolves without a session
- **WHEN** the legal manifest route is requested with no cookies
- **THEN** it returns a JSON document listing every kind with all its versions
- **AND** each entry carries the effective date, content hash, route and materiality flag.

#### Scenario: An operator can tell which text is live
- **WHEN** an operator fetches the manifest from a running deployment
- **THEN** the current version and content hash of each document are readable from the response
- **AND** answering "which text is live on this deployment" requires no database query.

### Requirement: Legal documents SHALL print as self-identifying documents

The print rendering SHALL be paginated without console chrome and SHALL carry kind, version, effective date
and content hash in a running footer.

#### Scenario: A printed copy identifies itself
- **WHEN** a reader prints a legal document
- **THEN** the output is paginated, free of navigation chrome, and every page footer carries the kind,
  version, effective date and content hash
- **AND** the archived file is identifiable without reference to the page it came from.

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

### Requirement: Legal documents SHALL be reachable from every place a commitment is made or reviewed

Both documents SHALL be linked from the public footer, the sign-in page, the console shell, the account
surface and the checkout flow.

#### Scenario: A prospective customer can read the Terms before creating anything
- **WHEN** a visitor with no account browses the public surface or the sign-in page
- **THEN** both legal documents are reachable by link from that page
- **AND** neither link requires a session to follow.

#### Scenario: A customer can find what governs their plan
- **WHEN** a signed-in customer opens the account surface or begins checkout
- **THEN** both legal documents are reachable from that surface.

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
