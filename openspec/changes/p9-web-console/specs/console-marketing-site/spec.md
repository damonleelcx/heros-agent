# Console Marketing Site — Spec Delta (P9)

Product rationale: [`../../../../../docs/prd/P9-web-console.md`](../../../../../docs/prd/P9-web-console.md)
§3 (G18) and §6 (FR32–FR35). Architecture decision: [`../../design.md`](../../design.md) Decision 10.
Reasoning: [`../../ui-ux-plan.md`](../../ui-ux-plan.md) R15.

Covers the **public surface** — the page a prospect meets before a session exists. It renders with no
session, no tenant data and no upstream platform call; every capability it claims resolves through a
checked-in manifest whose unshipped entries fail the build; plans are named and never priced; and it
sits under the same token set, string rules, accessibility floor and content-security policy as every
other console page.

> This capability exists because a marketing surface is the one page in a repository that no test reads
> and no engineer re-checks. It is therefore the page most likely to describe the roadmap in the present
> tense — and the drift is not caught internally, it is caught by a customer after the sale.

## ADDED Requirements

### Requirement: The public surface SHALL render without a session, tenant data, or an upstream platform call

The console's public entry SHALL be servable to an anonymous visitor. It SHALL NOT read tenant data,
SHALL NOT require or create a session in order to render, and SHALL NOT cause the BFF to make an
upstream call using the server-held credential.

#### Scenario: An anonymous visitor renders the page

- **WHEN** a request with no session arrives at the public entry
- **THEN** the page renders in full
- **AND** no redirect to sign-in occurs, because the page carries no tenant data.

#### Scenario: The page serves while the platform API is unreachable

- **WHEN** the platform API is stopped and the public entry is requested
- **THEN** the page still renders in full
- **AND** no upstream call was attempted.

#### Scenario: The public surface is not a way into tenant data

- **WHEN** any navigation from the public surface leads to a route that renders tenant data
- **THEN** that route requires a session and fails closed
- **AND** the public surface itself returns no tenant data under any parameters.

### Requirement: Every capability claim SHALL resolve to a shipped entry in a checked-in capability manifest

Each capability the public surface asserts SHALL correspond to an entry in a checked-in manifest naming
the claim, its owning phase and its shipped state. A claim absent from the manifest, or present but not
shipped, SHALL fail the build.

#### Scenario: An unshipped claim fails the build

- **WHEN** the public surface asserts a capability whose manifest entry is not marked shipped
- **THEN** the build fails
- **AND** the failure names the claim and its owning phase.

#### Scenario: An unlisted claim fails the build

- **WHEN** the public surface asserts a capability that has no manifest entry at all
- **THEN** the build fails
- **AND** the claim cannot reach the rendered page.

#### Scenario: A shipped claim names what backs it

- **WHEN** a capability claim is rendered
- **THEN** the surface is able to state which part of the platform delivers it
- **AND** the claim's wording describes behavior the platform performs rather than an intention.

### Requirement: The public surface SHALL name plans and SHALL NOT carry a price value

Plans SHALL be referred to by name (Free / Team / Business / Enterprise). No price value, percentage,
or other business number SHALL appear in the repository or in the shipped bundle.

#### Scenario: No priced literal ships

- **WHEN** the shipped client bundle is scanned
- **THEN** it contains no currency amount, percentage, or price-band literal
- **AND** the scan is a build gate.

#### Scenario: A gated capability names its plan rather than its price

- **WHEN** the public surface describes a capability included at a higher plan
- **THEN** it names the plan that unlocks it
- **AND** it does not state what that plan costs.

### Requirement: The public surface SHALL state the product's boundary beside its benefit

The public surface SHALL state what the platform does not do, what still requires a human decision, and
which claims are measured rather than asserted.

#### Scenario: The boundary is stated, not buried

- **WHEN** the public surface describes an optimization capability
- **THEN** it states that changes arrive as reviewable proposals a human merges, at the automation
  levels the platform actually offers
- **AND** it does not describe an automation level the platform does not offer by default.

#### Scenario: A measured claim is distinguishable from an asserted one

- **WHEN** the public surface presents a claim about outcomes
- **THEN** a claim the platform measures is distinguishable from a claim about how the product works
- **AND** no outcome figure is presented without the evidence that produced it.

### Requirement: The public surface SHALL meet the console's floor and SHALL reference no third-party origin

The public surface SHALL use the single token set, English strings with `en-US` formatting through the
locale swap point, keyboard reachability with visible focus, WCAG 2.1 AA contrast, and text
alternatives on graphical content. It SHALL NOT reference a third-party origin — no external font,
script, tracker, image host or stylesheet.

#### Scenario: No third-party origin is referenced

- **WHEN** the public surface is loaded and its network traffic is inspected
- **THEN** every request targets the console's own origin
- **AND** the page satisfies the console's `default-src 'self'` policy without relaxation.

#### Scenario: The public surface passes the same floor as a data view

- **WHEN** the public surface is audited
- **THEN** it passes the automated accessibility audit and a keyboard-only pass
- **AND** every visual value on it resolves to the token set.

#### Scenario: A visitor is not tracked before they consent to anything

- **WHEN** an anonymous visitor loads the public surface
- **THEN** no analytics, tag manager, or third-party beacon is loaded
- **AND** no request carries the visitor's address to a party other than the console's own origin.
