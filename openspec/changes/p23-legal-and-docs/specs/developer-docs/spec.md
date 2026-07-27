# Developer Documentation — Spec Delta (P23)

Product rationale: [`../../../../../docs/prd/P23-legal-and-developer-docs.md`](../../../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR17–FR25), §7 (NFR2, NFR4, NFR10) and §9.1 / §9.5 (Product Designer + AI Engineer lenses). Technical
decisions: [`../../design.md`](../../design.md) Decisions 6, 8 and 9.

Covers the surface a developer learns the product from: install to a first real result, then task-shaped
guides, then reference generated from the artifacts the code already produces.

> The rule that shapes the whole tier structure: **hand-written reference is a copy of the truth that starts
> drifting the day it is written.** Where a generator can produce it, it is generated. Where the artifact
> does not exist, the tier says so — an absent tier is honest, a hand-written one is a fiction with a table
> of contents.

## ADDED Requirements

### Requirement: Documentation SHALL ship in three tiers — quickstart, guides and generated reference

The surface SHALL provide a **Quickstart** (one path, install to first result), **Guides** (task-shaped, one
job each) and **Reference** (CLI, HTTP API, schemas, metrics, glossary).

#### Scenario: A reader can tell which tier answers their question
- **WHEN** a reader opens the documentation index
- **THEN** the three tiers are distinguished by name and purpose
- **AND** the quickstart is the first thing offered to a reader with no prior state.

#### Scenario: The quickstart offers one path
- **WHEN** a reader opens the quickstart
- **THEN** it presents a single path with no branch decisions
- **AND** alternatives (other languages, providers, CI wiring, the hosted console) live in Guides.

### Requirement: The quickstart SHALL reach a real result without editing a configuration file

Following the quickstart SHALL produce a discovery graph on the reader's **own repository**, with no
configuration-file edit — consistent with the P20 first-run contract.

#### Scenario: A stranger reaches a first result unaided
- **WHEN** a developer on a clean machine follows only the published quickstart
- **THEN** they obtain a discovery graph for their own repository
- **AND** they do not open or edit a configuration file to get there
- **AND** they do not need to read source or ask a question.

#### Scenario: The first result is real, not a sample
- **WHEN** the quickstart completes
- **THEN** the output described is the output of the reader's own run
- **AND** no fixture or canned result is presented as if it were theirs.

### Requirement: Reference tiers SHALL be generated from shipped artifacts, and marked absent when the artifact does not exist

CLI, schema and HTTP-API reference SHALL be generated from the CLI command registry, the committed JSON
schemas and a machine-readable API artifact respectively. A reference tier whose source artifact does not
exist SHALL be **rendered as absent with the reason**, and SHALL NOT be hand-written.

#### Scenario: Adding a CLI flag updates the reference
- **WHEN** a flag is added to a CLI subcommand and the build runs
- **THEN** the generated CLI reference includes it
- **AND** no hand edit to documentation was required.

#### Scenario: An absent artifact yields an absent tier, not prose
- **WHEN** no machine-readable HTTP API artifact exists
- **THEN** the API reference tier renders as absent, naming the missing artifact as the reason
- **AND** no hand-written endpoint list is published in its place.

### Requirement: Every code sample SHALL be executable as written or explicitly marked a fragment

Samples SHALL carry placeholder credentials only, and SHALL be either runnable verbatim or visibly labelled
as fragments.

#### Scenario: A copied sample runs
- **WHEN** a reader copies a sample that is not marked a fragment
- **THEN** it executes as written against the documented prerequisites.

#### Scenario: A fragment is not mistaken for a program
- **WHEN** a sample omits surrounding context
- **THEN** it is visibly marked a fragment.

### Requirement: Every documentation page SHALL state the platform version it documents and the boundary of the capability

Each page SHALL name the platform version it was generated against and SHALL state what the described
capability deliberately does **not** do.

#### Scenario: A boundary is on the page, not in a sales conversation
- **WHEN** a reader opens a page describing a capability
- **THEN** the page states the capability's boundary alongside the description.

#### Scenario: A reader can tell whether the page matches their build
- **WHEN** a reader opens any documentation page
- **THEN** the platform version the page documents is stated on it.

### Requirement: Refusals SHALL be documented as first-class outcomes

The platform's honest refusals — an unclassified graph region, a `BuildStatus` refusal, an axis marked
absent — SHALL be documented as behavior the product exhibits by design, not as errors or omissions.

#### Scenario: A refusal is recognizable before it is met in production
- **WHEN** a reader follows the quickstart or the relevant guide
- **THEN** the refusals they may encounter are described, with what each means and what to do next
- **AND** none is presented as a failure of the platform.

### Requirement: Sample outputs SHALL be captured and labelled, or marked illustrative

A sample output SHALL be either captured from a real run and labelled with the version that produced it, or
clearly marked illustrative. A model-generated example SHALL NOT be presented as a real run.

#### Scenario: An example result is attributable
- **WHEN** documentation shows a scorecard, graph or metric value
- **THEN** it is either labelled with the platform version that produced it, or marked illustrative.

### Requirement: Anchors SHALL be a published contract

Every heading SHALL have a stable slug published in a slug manifest generated by the same render pass as the
pages. Removing or renaming a slug SHALL fail the build unless the same change adds a redirect.

#### Scenario: A deep link that shipped in a binary keeps working
- **WHEN** a heading referenced by a CLI error message is renamed
- **THEN** the build fails unless the same change adds a redirect from the old slug
- **AND** with the redirect in place, the old link resolves to the new location.

#### Scenario: The manifest cannot drift from the pages
- **WHEN** the documentation is built
- **THEN** the slug manifest is emitted by the same pass that renders the headings.

### Requirement: Documentation SHALL be reachable by navigation with no orphan pages

Every page SHALL be reachable from the documentation navigation, from the console shell and from the public
surface.

#### Scenario: No page exists only by URL
- **WHEN** the link-coverage check runs over the documentation tree
- **THEN** every page is reachable by following navigation from the index
- **AND** a page reachable only by typing its URL fails the check.

### Requirement: Search SHALL be a static in-console index with a stated ranking limit and a no-JavaScript fallback

Search SHALL use a build-time index served by the console with **no third-party service**. Its ranking scope
SHALL be stated. With JavaScript disabled the surface SHALL degrade to a browsable table of contents.

#### Scenario: Search makes no third-party request
- **WHEN** a reader searches the documentation
- **THEN** no request leaves the console's own origin.

#### Scenario: A zero-result search says what it searched
- **WHEN** a search returns nothing
- **THEN** the surface states the scope it searched (titles, headings and lead paragraphs)
- **AND** the reader is not left to conclude the answer does not exist.

#### Scenario: The surface works without JavaScript
- **WHEN** documentation is opened with JavaScript disabled
- **THEN** the pages and a browsable table of contents render
- **AND** the reader sees neither a blank page nor a permanent spinner.

### Requirement: Documentation SHALL render fully in an air-gapped deployment

No external font, script, image, stylesheet or analytics request SHALL be required to render any
documentation or legal page.

#### Scenario: No egress is needed to read the docs
- **WHEN** documentation is served from a deployment with no outbound network access
- **THEN** every page renders identically to the hosted deployment
- **AND** no request is attempted to any origin other than the console's own.
