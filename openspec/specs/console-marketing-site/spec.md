# Console Marketing Site — Spec (folded from P23, P24)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md).
Technical decisions: [`../../changes/archive/2026-08-01-p23-legal-and-docs/design.md`](../../changes/archive/2026-08-01-p23-legal-and-docs/design.md). Related capability:
[`../social-proof-claims/spec.md`](../social-proof-claims/spec.md) — "The repository link SHALL always
render."

This delta clarifies a requirement that ships today in the P9 change
([`../../changes/p9-web-console/specs/console-marketing-site/spec.md`](../../changes/p9-web-console/specs/console-marketing-site/spec.md)).

**Why it is needed.** P23 task 7.1 puts the project's public repository link in the public header and
footer, and `social-proof-claims` makes that link a SHALL. The P9 requirement's prose says the surface
"SHALL NOT reference a third-party origin", and the live assertion in
`web/console/tests/public-surface.test.mjs` enforced that by requiring every `src` and `href` in the
document to be same-origin. Those two shipped requirements contradict each other, and the contradiction
surfaced as a failing test rather than as a decision.

**What the requirement was always about.** P9's own enumeration is subresources without exception — "no
external font, script, tracker, image host or stylesheet" — and its scenario is written in terms of
requests: "every request targets the console's own origin." An `<a href>` issues no request until the
visitor chooses the destination. The public surface as it ships today therefore already satisfies both
the enumeration and the scenario: it carries exactly two external references, the repository link in the
header and in the footer, and loads no external subresource of any kind.

So the fix is to say *request* where the prose said *reference*, and to write down the guards that make
a navigation safe — which the old assertion did not check at all.

**What was rejected.** Proxying the link through a first-party route to keep the rendered href
same-origin. P24's design already rejects that manoeuvre for the analytics case on honesty grounds — "it
would make a third-party flow look first-party, and the CSP's whole value is that it is a readable
statement of where data goes" — and the same objection applies here with an extra edge: the repository
is the project's trust anchor for a self-serve install, and disguising it as first-party hides the one
fact a reader should be able to check. Also rejected: widening the assertion's pattern until the link
passes, which removes the guarantee instead of scoping it.

**Composition with P24.** P24 amends the same P9 requirement along a different axis — a checked-in
allowlist of analytics *subresource* origins, each gated on a consent grant. This delta does not touch
that axis, and the two are additive: after both, an external subresource still fails unless it is on
P24's allowlist, and an external navigation is permitted only under the conditions below.

## Requirements

### Requirement: The public surface SHALL meet the console's floor and SHALL reference no third-party origin

The public surface SHALL use the single token set, English strings with `en-US` formatting through the
locale swap point, keyboard reachability with visible focus, WCAG 2.1 AA contrast, and text alternatives
on graphical content.

It SHALL reference **no third-party origin other than an origin present on the checked-in analytics
origin allowlist**, and each such origin SHALL be contacted only after an explicit grant for the consent
category that gates it. No external font and no external stylesheet SHALL be referenced under any
consent state. A visitor who has granted nothing SHALL cause **zero** third-party requests, which is the
condition the surface is in by default.

The tenant prefix, the BFF data prefix and every operator-console route SHALL continue to reference no
third-party origin whatsoever except the error-reporting origin under the connect directive, and that
rule SHALL be asserted **per prefix** rather than by a global assertion over the application.

#### Scenario: No unlisted third-party origin is referenced
- **WHEN** the public surface is loaded with every consent category granted and its network traffic is
  inspected
- **THEN** every request targets either the console's own origin or an origin present on the allowlist
- **AND** no external font and no external stylesheet is requested
- **AND** the page satisfies the public prefix's policy without any relaxation of the nonce,
  `'strict-dynamic'`, `'unsafe-inline'` or `'unsafe-eval'` rules.

#### Scenario: With nothing granted, the surface contacts nobody but itself
- **WHEN** the public surface is loaded by a visitor who has granted no category
- **THEN** every request targets the console's own origin
- **AND** the page satisfies a `default-src 'self'` policy with no third-party origin contacted.

#### Scenario: The absolute rule survives where the data is
- **WHEN** a tenant-prefixed route or an operator-console route is loaded
- **THEN** its policy contains `default-src 'self'` and names no analytics or session-replay origin
- **AND** the assertion establishing this names the prefix, so it cannot be satisfied by an
  application-wide check that a later change quietly widens.

#### Scenario: The public surface passes the same floor as a data view
- **WHEN** the public surface is audited
- **THEN** it passes the automated accessibility audit and a keyboard-only pass
- **AND** every visual value on it resolves to the token set.

#### Scenario: A visitor is not tracked before they consent to anything
- **WHEN** an anonymous visitor loads the public surface
- **THEN** no analytics, tag manager, or third-party beacon is loaded
- **AND** no request carries the visitor's address to a party other than the console's own origin.

#### Scenario: The public surface still serves with the platform stopped
- **WHEN** the platform API is unreachable and the public surface is loaded
- **THEN** it renders in full
- **AND** a failed or slow third-party script does not prevent or delay that rendering.
