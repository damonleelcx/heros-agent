# Admin Console Surface — Spec Delta (P8)

Product rationale: [`../../../../../docs/prd/P8-admin-console.md`](../../../../../docs/prd/P8-admin-console.md)
§6 (FR19–FR28 the floor, FR29–FR37 the surface above it), §7 and §8.4. The customer-facing counterpart — and the source of the interface rules
this capability inherits rather than restates — is
[`../../../p9-web-console/`](../../../p9-web-console/).

Covers the operator console **as an application**: a separate Next.js application on its own origin with
its own BFF, admin credential custody, the disjointness of admin and tenant session domains, rendering
capability from the same permission map the backend enforces, distinct operator chrome, friction
proportional to blast radius, and the interface floor — which is **not** lowered because the audience is
internal — **and the craft above that floor**: one design language, glanceable live state, operator
velocity, truthful feedback, and evidence that the danger path's friction survived every one of them.

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

> **Why craft requirements live in a security capability.** Everything above this line is the interface
> **floor**: keyboard reach, four distinct states, contrast, escaped values, no hardcoded number. A floor
> is what a surface must not fall below; it is not a description of the surface. The requirements below
> are the surface itself, and they are here for the reason the whole phase exists rather than for an
> aesthetic one. P8's stated purpose is to **retire the ad-hoc production shell** — the unaudited,
> unscoped, over-privileged path (proposal.md §Why). A console that is slower to answer a question than a
> `psql` prompt does not retire it: an operator under incident pressure routes around the surface that
> slows them down, and the credentialled shell stays open next to it. Craft is the mechanism by which
> this console is **preferred**, and being preferred is the only thing that makes it the **audited** path.
> The split that keeps that safe is stated as a requirement below — **delight on the read path, friction
> on the write path** — so "faster" never means "fewer deliberate steps to a destructive effect."

### Requirement: Meeting the interface floor SHALL NOT by itself constitute an acceptable view

A view that satisfies every accessibility, state, contrast and escaping requirement above SHALL still
be rejected if it fails the composition, legibility, velocity, feedback or evidence requirements
below. The floor SHALL be treated as the minimum below which a view cannot ship, never as the target
a view is built to.

#### Scenario: A floor-compliant view is still assessed against the craft requirements

- **WHEN** a view passes the accessibility audit, renders four distinct states, and hardcodes no number
- **THEN** it is assessed against the composition, legibility, velocity, feedback and evidence
  requirements as well
- **AND** passing the floor alone is not accepted as passing.

### Requirement: Every view SHALL be composed from one documented design language and a closed set of primitives

The console SHALL define one design language — an editorial type hierarchy, a single spacing rhythm, a
single radius and elevation model, and a stated composition grid — as a documented extension of the
shared token system, and SHALL compose every view from a **closed, documented set of primitives** (page
frame, section, data table, stat, timeline, drawer, confirmation sheet, receipt, state block). A view
SHALL NOT introduce a bespoke layout, a raw color value, or an off-scale spacing, type or radius value.

#### Scenario: Visual values resolve to tokens rather than literals

- **WHEN** the console's stylesheets and components are scanned for color, spacing, type-size and radius
  literals outside the token definitions
- **THEN** none is found
- **AND** every visual value resolves to a token in the documented scale.

#### Scenario: A new view adds no new primitive

- **WHEN** a new operator view is built
- **THEN** it composes from the existing primitive set
- **AND** adding a primitive is a deliberate, documented extension of the language rather than a
  side effect of building a page.

#### Scenario: Density is an operator choice the console remembers

- **WHEN** an operator selects a display density
- **THEN** the console renders at that density and retains the choice across views and sessions
- **AND** no information present at one density is absent at the other.

### Requirement: Numeric data SHALL be rendered for comparison at a glance

Numerals in tables and stats SHALL render in tabular (fixed-width) figures, aligned on their digits
within a column, with the unit and scale stated once in the column header or stat label rather than
repeated per value. A value's magnitude SHALL be distinguishable without counting digits.

#### Scenario: A column of numbers aligns for scanning

- **WHEN** a column of numeric values is rendered
- **THEN** the digits align vertically in fixed-width figures
- **AND** a larger value is visually distinguishable from a smaller one without reading it.

#### Scenario: Unit and scale are stated once, not per cell

- **WHEN** a numeric column or stat is rendered
- **THEN** its unit and scale appear in the header or label
- **AND** the same unit is not repeated on every value.

#### Scenario: One quantity uses one scale within a view

- **WHEN** the same quantity appears more than once in a view
- **THEN** it is rendered at the same scale and precision throughout
- **AND** two renderings of one quantity cannot be misread as different quantities.

### Requirement: The hazard palette SHALL be reserved for hazard

The console's danger and warning colors SHALL denote destructive scope, an armed halt, or an alarming
state only. They SHALL NOT be used for emphasis, branding, decoration, or a non-hazardous status.

#### Scenario: Danger color appears only on hazard

- **WHEN** the console's rendered views are inspected for use of the danger and warning colors
- **THEN** each occurrence denotes a destructive control, an armed kill switch, an active
  impersonation, or an alarming state
- **AND** none is decorative or emphatic.

#### Scenario: Hazard remains salient because it is rare

- **WHEN** an operator scans a view containing exactly one destructive control
- **THEN** that control is the only element carrying the hazard palette
- **AND** it is locatable without reading the view's text.

### Requirement: Every capability the operator's role grants SHALL be reachable from a command palette on every view

A command palette SHALL be reachable by a single keystroke from any view and SHALL address every
capability the operator's role grants and every recently-viewed target by name. It SHALL be driven by
the same permission map the backend enforces, so it SHALL NOT offer a capability the gate would refuse.

#### Scenario: The palette is one keystroke from anywhere

- **WHEN** an operator presses the palette keystroke on any authenticated view
- **THEN** the palette opens with focus in its input
- **AND** it is dismissible by keyboard without leaving the current view.

#### Scenario: The palette offers only granted capabilities

- **WHEN** an operator whose role does not grant a capability searches for it in the palette
- **THEN** no entry for it is offered
- **AND** the palette and the gate do not disagree.

#### Scenario: A subject is found by name rather than recalled as an identifier

- **WHEN** an operator needs to act on a tenant, job, or model
- **THEN** the subject is selectable by type-ahead on its name
- **AND** the operator is not required to recall an opaque identifier in order to reach it.

### Requirement: Every view's state SHALL be addressable by URL and restorable

A view's subject, filters, time window, and selected tab SHALL be represented in its URL. The URL SHALL
reproduce the view for another authorized operator, and browser back and forward SHALL restore the
previous view state exactly. No personal or sensitive value SHALL be placed in the URL.

#### Scenario: A view pasted into an incident channel reproduces itself

- **WHEN** an operator copies a view's URL and another authorized operator opens it
- **THEN** the same subject, filters, time window and tab are restored
- **AND** the second operator's own permissions still govern what is rendered.

#### Scenario: Back and forward restore view state

- **WHEN** an operator changes a filter and then navigates back
- **THEN** the previous filter state is restored
- **AND** the view does not reset to its default.

#### Scenario: Sensitive values are not carried in the URL

- **WHEN** view state is encoded into the URL
- **THEN** it contains no session material, no impersonation credential, and no subject personal data.

### Requirement: The live operating picture SHALL be readable without interaction

The console's operating view SHALL show, without any interaction, whether anything is halted and
whether anything is wrong — global and per-tenant kill-switch state, fleet and queue health, active
impersonations, and unresolved anomalies. Each live figure SHALL carry the time it is current as of.

#### Scenario: Halted and healthy are distinguishable at a glance

- **WHEN** an operator opens the operating view with a kill switch armed
- **THEN** the armed state is apparent without interaction and without reading prose
- **AND** it is distinguishable from the unarmed state by more than color alone.

#### Scenario: Stale data announces itself rather than presenting as current

- **WHEN** a live figure cannot be refreshed within its expected interval
- **THEN** the view states the time the figure is current as of and marks it stale
- **AND** it is not presented as the current state.

### Requirement: A live update SHALL NOT shift layout, reorder under the operator, or blank correct data

Updating a live view SHALL preserve its layout and the position of the row the operator is pointing at
or focused on. A refresh in flight SHALL be indicated without replacing already-correct data with a
loading state.

#### Scenario: An update does not move the target under the pointer

- **WHEN** a live table updates while the operator's pointer or focus is on a row
- **THEN** that row does not move
- **AND** an action begun before the update lands on the subject it was begun on.

#### Scenario: A refresh does not blank a correct view

- **WHEN** a view holding current data refreshes
- **THEN** the existing data remains rendered while the refresh is in flight
- **AND** the refresh is indicated without a full-view loading state.

### Requirement: Motion SHALL carry meaning, stay within budget, and never gate an action

Motion SHALL be used only to preserve continuity or to mark a state change — where a surface came from,
which value changed, what arrived. Transitions SHALL complete within a documented duration budget and
SHALL NOT delay a navigation, confirmation, or command. Under `prefers-reduced-motion` every animation
SHALL be replaced by an instant equivalent.

#### Scenario: No action waits on an animation

- **WHEN** an operator confirms an action or navigates while a transition is running
- **THEN** the action is issued immediately
- **AND** no animation is on the path between the operator's intent and the command.

#### Scenario: Reduced motion loses no information

- **WHEN** the console renders under `prefers-reduced-motion`
- **THEN** every state change conveyed by motion is conveyed by a static means
- **AND** no information exists only in an animation.

#### Scenario: Motion is not decorative

- **WHEN** an animation is present
- **THEN** it corresponds to a state change, an arrival, or a spatial relationship
- **AND** no element animates without such a referent.

### Requirement: Every privileged command SHALL resolve to a receipt naming its audit entry

On completion, a privileged command SHALL render a receipt stating what was done, to which target,
under which recorded reason, at what time, and the reference of the audit entry it wrote. The receipt
SHALL offer the reversing action or state explicitly that none exists.

#### Scenario: A receipt names the audit entry

- **WHEN** a privileged command completes
- **THEN** the receipt states the action, the target, the recorded reason, the time, and the audit
  reference
- **AND** the operator can reach the audit entry from the receipt.

#### Scenario: Irreversibility is stated rather than implied

- **WHEN** a completed command has no reversing action
- **THEN** the receipt says so explicitly
- **AND** the absence of an undo control is not the only indication.

### Requirement: The console SHALL NOT render an optimistic success, and SHALL distinguish in-flight, failed, and unknown outcomes

A state change SHALL be rendered only after the backend confirms it, including its write-ahead audit. A
command in flight, a command that failed, and a command whose outcome could not be determined SHALL be
three distinct renderings.

#### Scenario: Success is not shown before it is confirmed

- **WHEN** an operator issues a privileged command
- **THEN** the console renders it as in flight until the backend confirms the effect
- **AND** it does not render the changed state in anticipation.

#### Scenario: An indeterminate outcome is not rendered as either success or failure

- **WHEN** a command's outcome cannot be determined — the response is lost or the audit write is
  unconfirmed
- **THEN** the console renders the outcome as unknown and states how to verify it
- **AND** it renders neither success nor failure.

### Requirement: No visual, motion, or velocity affordance SHALL reduce dangerous-action friction

Every requirement above applies to the read path. On the write path, the friction defined for
dangerous actions SHALL remain intact: no transition, shortcut, palette entry, default, or restyle
SHALL make a destructive action reachable in fewer deliberate steps, and the command palette SHALL be
able to navigate to a dangerous action but SHALL NOT execute one.

#### Scenario: The palette navigates to a dangerous action but does not perform it

- **WHEN** an operator selects a destructive capability from the command palette
- **THEN** the console opens that action's confirmation, with the reason and any typed-target
  requirement intact
- **AND** the action is not performed by the palette selection.

#### Scenario: A restyle leaves the friction unchanged

- **WHEN** the console's visual language, motion, or navigation changes
- **THEN** the dangerous-action path still requires a typed reason, still requires the target to be
  typed where the action is irreversible, and still distinguishes global from per-tenant scope
- **AND** the number of deliberate steps to a destructive effect has not decreased.

#### Scenario: No smart default pre-fills a destructive intent

- **WHEN** a destructive action's confirmation is opened
- **THEN** the reason field is empty and any typed-target field is empty
- **AND** neither is pre-filled from context, history, or a previous action.

### Requirement: Craft acceptance SHALL be evidenced across a defined rendering matrix

Acceptance evidence for a console view SHALL cover light and dark, a narrow and a wide viewport, 200%
zoom, `prefers-reduced-motion`, both density modes, and the loading, empty, denied and degraded states.
A visual-regression baseline SHALL gate unintended visual change, and a new view SHALL carry a recorded
design review by the console's product designer.

#### Scenario: The rendering matrix is covered by evidence

- **WHEN** a console view is submitted for acceptance
- **THEN** rendered evidence exists for each cell of the matrix
- **AND** a cell without evidence blocks acceptance rather than being assumed to pass.

#### Scenario: An unintended visual change is caught

- **WHEN** a change alters a view's rendering in a way not described by the change
- **THEN** the visual-regression baseline fails
- **AND** the difference is reviewed rather than silently absorbed into the baseline.

#### Scenario: A new view carries a named design review

- **WHEN** a new operator view reaches acceptance
- **THEN** a design review is recorded with its reviewer named
- **AND** acceptance without one is not granted.
