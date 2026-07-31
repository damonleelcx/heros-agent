# Documentation Accuracy Fence — Spec (folded from P23)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR26–FR32), §7 (NFR12) and §9.7 / §9.8 (QA + Sales Operations lenses). Technical decisions:
[`../../changes/p23-legal-and-docs/design.md`](../../changes/p23-legal-and-docs/design.md) Decision 11.

Covers the build-time gates that turn documentation drift from something a customer finds into something a
build fails on. They extend the idiom `web/console/scripts/scan-claims.mjs` already establishes for the
public surface.

> The reasoning that produced the claims fence applies **more** strongly to documentation than to marketing:
> documentation is longer, is read later, is read by people making commitments, and is the file nobody
> re-reads after the week it was written. A page that describes a system that no longer exists is not a
> stale page — it is a false statement with the product's authority behind it.

## ADDED Requirements

### Requirement: The build SHALL reject documentation describing a capability that has not shipped

A documentation page SHALL NOT describe a capability that is absent from the capability manifest or listed
without `shipped: true` and a named owning phase.

#### Scenario: An unshipped capability cannot be documented
- **WHEN** a documentation page describes a capability that is not `shipped: true` in the manifest
- **THEN** the build fails naming the page and the capability
- **AND** the page does not reach a rendered surface.

#### Scenario: The rule follows the reader from marketing into docs
- **WHEN** the claim fence runs
- **THEN** it covers the documentation tree as well as the public surface.

### Requirement: The build SHALL reject a CLI invocation that does not exist

Every `heros …` invocation appearing in content SHALL resolve to a real subcommand with real flags in the CLI
command registry.

#### Scenario: An invented subcommand fails the build
- **WHEN** content contains a `heros` invocation naming a subcommand the registry does not have
- **THEN** the build fails naming the file and the invocation.

#### Scenario: A stale flag fails the build
- **WHEN** a flag is removed from a subcommand while documentation still uses it
- **THEN** the build fails
- **AND** the failure names the flag and the page.

### Requirement: The build SHALL reject a documented endpoint or field absent from the API artifact

Every documented endpoint, method and field SHALL resolve against the machine-readable API artifact. When the
artifact is absent, the fence SHALL refuse the page rather than pass vacuously.

#### Scenario: An undocumented endpoint fails the build
- **WHEN** content documents an endpoint, method or field that the API artifact does not contain
- **THEN** the build fails naming the page and the reference.

#### Scenario: A missing artifact does not silently pass everything
- **WHEN** no API artifact exists and content documents an endpoint
- **THEN** the fence fails rather than reporting success
- **AND** the honest alternative is the absent-tier rendering, not an unchecked page.

### Requirement: The build SHALL reject a metric definition that disagrees with the harness

Every metric or statistic defined in documentation SHALL match the harness's definition — name, unit and
computation — and SHALL cite where it is computed.

#### Scenario: A renamed statistic fails the build
- **WHEN** documentation calls a quantity by a name or unit the harness does not use
- **THEN** the build fails naming the metric, the page and the disagreement.

#### Scenario: An uncited definition fails the build
- **WHEN** documentation defines a metric without citing its computation site
- **THEN** the build fails
- **AND** a definition a reader cannot trace is not published.

### Requirement: The build SHALL reject an unresolvable link or anchor

Every internal link and anchor SHALL resolve. External links SHALL be allow-listed and visibly marked as
external. A removed or renamed slug SHALL fail unless the same change adds a redirect.

#### Scenario: A dead anchor fails the build
- **WHEN** content links to an anchor that no heading produces
- **THEN** the build fails naming the source page and the target.

#### Scenario: An unlisted external link fails the build
- **WHEN** content links to an external origin that is not allow-listed
- **THEN** the build fails
- **AND** an allow-listed external link renders visibly marked as external.

### Requirement: The build SHALL reject credential-shaped content and unsafe markup

Content matching a credential pattern — provider key prefixes, PEM blocks, bearer tokens — SHALL fail the
build. Content SHALL be Markdown with no raw HTML, no inline event handlers, and no external script, font or
stylesheet reference.

#### Scenario: A real key never reaches a published page
- **WHEN** an example contains a value matching a credential pattern
- **THEN** the build fails naming the file and the match
- **AND** the page is not published.

#### Scenario: Air-gapped parity is a machine check
- **WHEN** content references an external script, font or stylesheet
- **THEN** the build fails
- **AND** the no-external-request guarantee does not depend on anyone remembering it.

#### Scenario: Raw markup cannot enter the trust surface
- **WHEN** content contains a raw HTML block or an inline event handler
- **THEN** the build fails.

#### Scenario: A third-party origin in public-surface markup fails
- **WHEN** the public surface's markup names a third-party origin — a badge image, a widget script, a hosted
  font, or a cross-origin fetch
- **THEN** the build fails naming the origin
- **AND** the runtime CSP would refuse it independently, so the build-time and runtime enforcements agree.

### Requirement: The build SHALL reject hand-written release data and an install path that skips verification

Install content SHALL derive its asset filenames, versions and checksums from the published release. The
build SHALL fail on a hand-typed checksum, filename or version; on a documented install path that places the
binary on `PATH` before verifying checksum and signature; on a signing or notarization claim naming a step
the release pipeline does not perform; and on an install channel the pipeline does not publish.

#### Scenario: A hand-typed checksum fails the build
- **WHEN** install content contains a literal checksum, asset filename or version that is not generated from
  the release
- **THEN** the build fails naming the value
- **AND** a routinely-wrong checksum never gets the chance to teach readers that verification fails anyway.

#### Scenario: An unverified install path fails
- **WHEN** a documented install path places the binary on `PATH` before checksum and signature verification
- **THEN** the fence fails and the path is not published.

#### Scenario: A trust claim without a pipeline step fails
- **WHEN** content claims an artifact is signed or notarized
- **THEN** the build fails unless the release pipeline performs that step.

### Requirement: Each fence SHALL ship with a failing fixture and SHALL state what it does not check

Every fence SHALL be accompanied by a fixture that proves it fails, and its header SHALL state the coverage
it does **not** provide.

#### Scenario: Every fence is proven able to go red
- **WHEN** the fence fixtures run
- **THEN** each of the eight fences fails on its own fixture, individually
- **AND** a fence with no failing fixture is not counted as delivered.

#### Scenario: A fence does not imply coverage it lacks
- **WHEN** a reader opens a fence's source
- **THEN** its header states what it does not check
- **AND** tone, emphasis and omission are named as a human review responsibility rather than implied to be
  covered.
