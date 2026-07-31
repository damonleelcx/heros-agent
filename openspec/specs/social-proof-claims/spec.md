# Social-Proof Claims on the Public Surface — Spec (folded from P23)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR49–FR53) and §9.1 / §9.6 / §9.8 (Product Designer + DevOps + Sales Operations lenses). Technical
decisions: [`../../changes/p23-legal-and-docs/design.md`](../../changes/p23-legal-and-docs/design.md) Decision 15.

Covers the **GitHub link and star count on the public home page**, and — because this is the first of its
kind and will not be the last — the rule that governs any number the marketing surface states about the
world rather than about the product.

> A capability claim says *what the product does*, and `capabilities.ts` makes it checkable. A **star count
> says what the world thinks**, and it has the same failure mode with none of the machinery: a number on a
> page that no one re-checks, drifting from the truth, read by someone deciding whether to trust us. So it
> is governed the same way — **measured, stamped with when it was measured, and never typed by hand.**

> 🔴 **The constraint that decides the design.** The console's CSP is set per request in
> [`middleware.ts`](../../../web/console/src/middleware.ts): `default-src 'self'`,
> `connect-src 'self'`, `img-src 'self' data:`. A shields.io badge, a GitHub buttons widget and a
> browser-side `api.github.com` fetch are therefore **refused at runtime**, not merely discouraged — and
> that middleware's own comment names the public page's no-third-party-origin rule as the reason. This
> capability does not create that rule; it declines to be the exception to it.

## ADDED Requirements

### Requirement: The public home page SHALL link to the project's GitHub repository

The home page SHALL carry a link to the public repository, reachable without a session, marked as leaving
the site, and issuing **no network request until the reader clicks it**.

#### Scenario: The link is present and reachable with no session
- **WHEN** a visitor with no account opens the home page
- **THEN** a link to the project's GitHub repository is present and followable
- **AND** it is visibly marked as an external destination.

#### Scenario: The link costs nothing to render
- **WHEN** the home page renders
- **THEN** no request is made to GitHub or any other third-party origin
- **AND** the reader's browser contacts no third party until they choose to follow the link.

#### Scenario: The link target is public
- **WHEN** the linked repository is checked
- **THEN** it resolves for an anonymous visitor
- **AND** a link to a private or non-existent repository fails the build, under the same rule that forbids an
  install command that 404s.

### Requirement: A star count SHALL be measured at build time and stamped with its measurement date

If a star count is displayed, it SHALL be captured during the build and rendered together with **when it was
measured**. It SHALL NOT be fetched by the reader's browser.

#### Scenario: The number carries its own timestamp
- **WHEN** a star count is displayed
- **THEN** the page states when it was measured
- **AND** a reader can tell the number is a measurement rather than a live reading.

#### Scenario: The reader's browser never asks GitHub
- **WHEN** the home page is loaded and inspected
- **THEN** no request to `api.github.com` or any badge service is issued
- **AND** the count present in the markup came from the build.

### Requirement: Social proof SHALL NOT introduce a third-party origin or a CSP relaxation

No badge image, iframe, widget script or cross-origin fetch SHALL be used to render social proof, and the
`default-src 'self'` CSP SHALL remain unchanged. A change that relaxes it for social proof SHALL be refused.

#### Scenario: A badge service is refused
- **WHEN** a change adds a shields.io image, a GitHub buttons widget, or a browser-side call to
  `api.github.com`
- **THEN** the build fails on the external-origin check
- **AND** the runtime CSP would refuse it in any case, so the two enforcements agree.

#### Scenario: The air-gapped and offline rendering is unaffected
- **WHEN** the home page is served from a deployment with no outbound network access
- **THEN** it renders identically, including the link and any build-time count
- **AND** nothing on the page waits on, or degrades because of, an unreachable third party.

#### Scenario: The privacy posture is unchanged
- **WHEN** a visitor reads the home page
- **THEN** no third party observes that visit
- **AND** the page's existing no-cookie, no-tracking posture is preserved.

### Requirement: A star count SHALL never be hand-typed, and SHALL degrade to the plain link when unavailable

The count SHALL come from the build-time measurement. A literal count written into source SHALL fail the
build. When the measurement is unavailable, the element SHALL render as the plain repository link — never as
a zero, a placeholder, an error or a broken badge.

#### Scenario: A hand-typed count fails the build
- **WHEN** a number is written directly into the page source as a star count
- **THEN** the build fails naming the value
- **AND** the same rule that rejects a hand-typed release checksum rejects a hand-typed count.

#### Scenario: An unavailable measurement degrades to the link
- **WHEN** the build-time measurement cannot be taken (offline build, rate limit, API failure)
- **THEN** the build succeeds and the page renders the plain repository link
- **AND** no zero, placeholder or error state is shown to a reader.

### Requirement: Displaying the count SHALL be opt-in configuration; the link SHALL be unconditional

The repository link SHALL always render. Whether the count renders SHALL be a configuration decision,
defaulting to **off**.

#### Scenario: The default shows the link without a number
- **WHEN** no explicit choice has been configured
- **THEN** the home page shows the repository link and no count.

#### Scenario: Turning the count off never removes the link
- **WHEN** the count display is disabled
- **THEN** the repository link remains present and reachable.

#### Scenario: The decision is recorded rather than defaulted silently
- **WHEN** the count is enabled
- **THEN** the choice is an explicit configured value
- **AND** enabling it is a decision someone made, not a behavior that appeared because a number became
  available.
