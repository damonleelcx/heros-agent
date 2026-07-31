# Console Marketing Site — Spec Delta (P23)

Product rationale: [`../../../../../docs/prd/P23-legal-and-developer-docs.md`](../../../../../docs/prd/P23-legal-and-developer-docs.md).
Technical decisions: [`../../design.md`](../../design.md). Related capability:
[`../social-proof-claims/spec.md`](../social-proof-claims/spec.md) — "The repository link SHALL always
render."

This delta clarifies a requirement that ships today in the P9 change
([`../../../p9-web-console/specs/console-marketing-site/spec.md`](../../../p9-web-console/specs/console-marketing-site/spec.md)).

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

## MODIFIED Requirements

### Requirement: The public surface SHALL meet the console's floor and SHALL cause no third-party request

The public surface SHALL use the single token set, English strings with `en-US` formatting through the
locale swap point, keyboard reachability with visible focus, WCAG 2.1 AA contrast, and text alternatives
on graphical content.

It SHALL NOT load a third-party **subresource** — no external font, script, tracker, image host or
stylesheet — so that loading the page causes no request to any origin but the console's own.

It MAY reference a third-party origin as the `href` of an `<a>` element, which issues no request until
the visitor chooses the destination. Such an anchor SHALL carry `rel="noreferrer noopener"`, SHALL be
marked as external in its accessible name, and SHALL be served under the console's
`Referrer-Policy: no-referrer`, so that the destination learns nothing about the visitor until they act
and nothing about where they came from when they do.

#### Scenario: No third-party subresource is loaded

- **WHEN** the public surface is loaded and its network traffic is inspected
- **THEN** every request targets the console's own origin
- **AND** the page satisfies the console's `default-src 'self'` policy without relaxation.

#### Scenario: The repository link is present and leaks nothing

- **WHEN** an anonymous visitor loads the public surface
- **THEN** the repository link renders in the header and the footer
- **AND** loading the page causes no request to the repository host
- **AND** the anchor carries `rel="noreferrer noopener"` and is announced as opening an external site
- **AND** the response carries `Referrer-Policy: no-referrer`, so a visitor who clicks is not announced
  to the destination.

#### Scenario: An external subresource is still refused

- **WHEN** any element other than an `<a>` names a third-party origin in a `src` or `href`
- **THEN** the assertion fails and names the element, whether or not the same origin is already
  permitted as an anchor destination.
