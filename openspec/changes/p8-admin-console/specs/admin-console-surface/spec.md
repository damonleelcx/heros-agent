# Admin Console Surface — Spec Delta (P8)

Product rationale: [`../../../../../docs/prd/P8-admin-console.md`](../../../../../docs/prd/P8-admin-console.md)
§6 (FR19–FR28), §7 and §8.4. The customer-facing counterpart — and the source of the interface rules
this capability inherits rather than restates — is
[`../../../p9-web-console/`](../../../p9-web-console/).

Covers the operator console **as an application**: a separate Next.js application on its own origin with
its own BFF, admin credential custody, the disjointness of admin and tenant session domains, rendering
capability from the same permission map the backend enforces, distinct operator chrome, friction
proportional to blast radius, and the interface floor — which is **not** lowered because the audience is
internal.

> **Why a separate application.** This is the platform's highest-blast-radius surface: one action here
> crosses tenant boundaries and can halt the autonomous fleet. In a single application with role-gated
> routes, the separation between a tenant session and a cross-tenant capability would be a property of
> **routing correctness**. As two origins it is a property the **browser enforces** — separate cookie
> jars, separate bundles — so a cross-site-scripting or routing defect on the customer side cannot reach
> an admin capability at all.

## ADDED Requirements

### Requirement: The operator console SHALL be a separate application on an origin distinct from the customer console

The operator console SHALL be a separate application, deployed as an independent unit, served from an
origin distinct from the customer console's, with its own backend-for-frontend. It SHALL NOT share an
origin, a session cookie, or a client bundle with the customer console.

#### Scenario: The two consoles do not share an origin

- **WHEN** the operator console and the customer console are both deployed
- **THEN** they are served from different origins
- **AND** neither can read the other's cookies or storage, because the browser isolates them by origin.

#### Scenario: The consoles are deployed independently

- **WHEN** either console is deployed
- **THEN** the other continues serving its previous version unaffected
- **AND** neither deployment requires the other to be redeployed.

#### Scenario: Isolation does not depend on routing correctness

- **WHEN** a routing defect in the customer console would expose an unintended route
- **THEN** no operator capability becomes reachable
- **AND** the boundary holds because it is enforced by origin, not by the customer console's routing.

### Requirement: The admin credential SHALL be held server-side and SHALL never reach the browser

The admin backend-for-frontend SHALL hold the platform credential server-side only and SHALL issue the
browser an `HttpOnly`, `SameSite` session bound to an admin principal. No credential SHALL appear in the
client bundle, a script-readable cookie, browser storage, a URL, a log line, or a telemetry attribute.

#### Scenario: No credential in the shipped client bundle

- **WHEN** the operator console's client bundle is built and scanned for credential material
- **THEN** none is found
- **AND** the scan is a build-time gate, so a bundle containing credential material fails the build.

#### Scenario: An unauthenticated route redirects rather than rendering

- **WHEN** a request without a valid admin session reaches any console route
- **THEN** it is redirected to sign-in
- **AND** no shell is rendered that would then fail each of its requests.

#### Scenario: The browser cannot read its own admin session

- **WHEN** page script attempts to read the admin session cookie
- **THEN** the cookie is not readable, because it is set `HttpOnly` and `SameSite`.

### Requirement: The admin and tenant session domains SHALL be disjoint

A customer (tenant) session SHALL NOT authorize any admin capability, and an admin session SHALL NOT be
presentable to the customer console.

#### Scenario: A tenant session is refused by the admin BFF

- **WHEN** a tenant session is presented to the admin backend-for-frontend
- **THEN** it is refused
- **AND** it is not treated as an admin session regardless of the tenant's plan or role.

#### Scenario: An admin session grants nothing on the customer console

- **WHEN** an admin session is presented to the customer console
- **THEN** it does not authorize any tenant data access
- **AND** it is not accepted as a tenant session.

### Requirement: The console SHALL render capability from the same permission map the backend enforces

The console SHALL determine which capabilities to render from the same permission map the backend
authorization gate enforces. A capability the operator's role does not grant SHALL NOT be rendered as an
available control.

#### Scenario: An ungranted capability is not rendered as a control

- **WHEN** an operator whose role does not grant a capability views a page containing it
- **THEN** no control for that capability is rendered as available
- **AND** there is nothing for the operator to activate and have refused.

#### Scenario: The screen and the gate do not disagree

- **WHEN** the console renders a capability as available and the operator activates it
- **THEN** the backend gate permits it
- **AND** a capability shown as available is not refused by the gate.

#### Scenario: A denial names who holds the permission and how to escalate

- **WHEN** an operator reaches an action their role does not grant
- **THEN** the console states which role holds it and how to request it
- **AND** it does not render a bare refusal.

### Requirement: Every view SHALL carry operator chrome that distinguishes the console from the customer console at a glance

Every view SHALL carry distinct operator chrome — a distinct accent and persistent identification naming
the console and the acting admin principal — so the operator console cannot be mistaken for the customer
console.

#### Scenario: Operator identification is present on every view

- **WHEN** any operator-console view is rendered
- **THEN** it identifies the console and the acting admin principal
- **AND** the identification is persistent rather than shown only on entry.

#### Scenario: The two consoles are distinguishable at a glance

- **WHEN** the operator console and the customer console are viewed side by side
- **THEN** they are distinguishable without reading their content
- **AND** the distinction does not rely on the browser's address bar.

#### Scenario: Distinctness does not fork the token system

- **WHEN** the operator console's visual values are inspected
- **THEN** its scale, spacing, type and accessibility primitives come from the shared token system
- **AND** only the accent and chrome differ, rather than a second independent visual language.

### Requirement: Dangerous-action friction SHALL be proportional to blast radius and rendered as such

A destructive action SHALL require a typed reason. An irreversible action SHALL additionally require the
operator to type the target's identifier. A control whose scope is global SHALL be visually distinct
from, and higher-friction than, its per-tenant counterpart.

#### Scenario: An irreversible action requires the target to be typed

- **WHEN** an operator initiates an irreversible action
- **THEN** confirmation requires typing the target's identifier as well as a reason
- **AND** the action does not proceed on a single acknowledgement.

#### Scenario: A global control cannot be mistaken for a per-tenant one

- **WHEN** a global control and its per-tenant counterpart are both available
- **THEN** the global control is visually distinct and requires more deliberate confirmation
- **AND** halting one tenant cannot be confused with halting every tenant.

#### Scenario: A destructive action without a reason does not proceed

- **WHEN** an operator confirms a destructive action without supplying a reason
- **THEN** the action does not proceed
- **AND** the console states that a reason is required.

### Requirement: An active impersonation SHALL be continuously visible with an always-available exit

While an impersonation session is active, the console SHALL display a persistent banner naming the
tenant, the scope, the expiry, and that every action is logged, together with an always-visible control
to end the session. Entering write scope SHALL require a second confirmation.

#### Scenario: The banner is visible throughout the session

- **WHEN** an impersonation session is active and the operator navigates between views
- **THEN** the banner remains visible on every view
- **AND** it names the tenant, the scope, the expiry, and that actions are logged.

#### Scenario: Ending impersonation is always one control away

- **WHEN** an impersonation session is active
- **THEN** a control to end it is visible without navigating elsewhere.

#### Scenario: Entering write scope requires a second confirmation

- **WHEN** an operator elevates a read-scoped impersonation to write scope
- **THEN** a second confirmation is required
- **AND** the elevation is recorded.

### Requirement: Loading, empty, denied, and degraded SHALL be four distinct renderings on every view

Every data view SHALL render loading, empty, denied, and degraded states distinguishably. A permission
denial SHALL NOT render as an empty result, and a transport failure SHALL NOT render as absence of data.

#### Scenario: A denial is not rendered as emptiness

- **WHEN** an operator lacks permission for a view's data
- **THEN** the view renders a denial
- **AND** it does not render an empty result, which would misstate that no such data exists.

#### Scenario: A transport failure is not rendered as absence

- **WHEN** the console cannot reach its backend
- **THEN** the view renders a transport failure
- **AND** it does not render an empty result.

#### Scenario: Degraded is distinguishable from healthy

- **WHEN** a view's underlying subsystem is degraded
- **THEN** the view says so
- **AND** partial data is not presented as complete.

### Requirement: The operator console SHALL meet the same interface floor as the customer console

Every interactive element SHALL be keyboard-reachable with a visible focus indicator; every data table
SHALL use scoped column headers; every chart SHALL have an accessible tabular fallback; contrast SHALL
meet WCAG 2.1 AA; UI strings SHALL be English with locale formatting pinned through a single swap point;
and values SHALL be escaped on render. The floor SHALL NOT be lowered because the audience is internal.

#### Scenario: The console is fully operable by keyboard

- **WHEN** an operator drives the console using only the keyboard
- **THEN** every interactive element can be reached and activated
- **AND** the focused element is visibly indicated at all times.

#### Scenario: Charts carry a tabular equivalent

- **WHEN** a cross-tenant chart is rendered
- **THEN** an accessible table of the same data is available.

#### Scenario: The internal audience is not an exemption

- **WHEN** an accessibility or interface requirement is evaluated for this console
- **THEN** it applies at the same level as for the customer console
- **AND** the audience being internal is not accepted as a reason to relax it.

### Requirement: No plan price or numeric plan limit SHALL be present in the console client

The console SHALL resolve plan names and price references from configuration. No price value or numeric
plan limit SHALL be present in the client bundle or in the console's source.

#### Scenario: Prices resolve from configuration

- **WHEN** the console displays plan or price information
- **THEN** the values come from configuration at request time
- **AND** they are not compiled into the client bundle.

#### Scenario: The client bundle contains no price value

- **WHEN** the console's client bundle is scanned for price values or numeric plan limits
- **THEN** none is found.

### Requirement: Acceptance of a user-visible console behavior SHALL require rendered-browser evidence

A change to a user-visible console behavior SHALL be accepted only on evidence from rendering it in a
real browser against a real API response, with the denied and degraded paths exercised. A successful
build, type check, or unit-test run SHALL NOT by itself constitute acceptance.

#### Scenario: A green build is not acceptance

- **WHEN** a change to a user-visible console behavior is proposed with only build and unit-test evidence
- **THEN** it is not accepted
- **AND** rendered-browser evidence is required.

#### Scenario: The denied and degraded paths are exercised

- **WHEN** acceptance evidence is produced for a console view
- **THEN** it covers the denied and degraded states as well as the populated one
- **AND** each is shown to render distinctly.
