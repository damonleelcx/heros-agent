# Console Marketing Site — Spec Delta (P24)

Product rationale: [`../../../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../../../docs/prd/P24-analytics-and-error-monitoring.md)
§2.3, §6 (FR34–FR36). Technical decisions: [`../../design.md`](../../design.md) D4.

This delta amends a requirement that ships today in the P9 change
([`../../../p9-web-console/specs/console-marketing-site/spec.md`](../../../p9-web-console/specs/console-marketing-site/spec.md)),
enforced by two live assertions that the shipped policy contains no `https://` origin at all
(`web/console/tests/security.test.mjs:286`, `web/console/tests/public-surface.test.mjs:156`) and by the
`default-src 'self'` policy whose own comment states that an analytics tag "does not render, it is
REFUSED".

**This delta exists so that the change is announced rather than smuggled.** A phase that installed
analytics by widening those two regular expressions would have removed the guarantee from the tenant
surface, which is the surface the guarantee was for. What happens instead: the absolute rule survives
verbatim on every prefix that renders customer or operator data and *gains a specific assertion it did
not have*; the public prefix — a page with no session, no tenant data and no upstream platform call —
becomes bounded by a checked-in allowlist instead of by nothing.

The scenario **"A visitor is not tracked before they consent to anything" is preserved unchanged.** It
survives because consent defaults to denied for every non-essential category, so a visitor who takes no
action is in exactly the state that scenario describes.

## MODIFIED Requirements

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
