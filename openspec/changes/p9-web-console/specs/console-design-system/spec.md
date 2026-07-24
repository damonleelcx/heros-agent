# Console Design System — Spec Delta (P9)

Product rationale: [`../../../../../docs/prd/P9-web-console.md`](../../../../../docs/prd/P9-web-console.md)
§6 (FR18–FR24) and §7 (NFR7). Reasoning and the concrete defects behind each rule:
[`../../ui-ux-plan.md`](../../ui-ux-plan.md) R1, R3–R7, R11. Token reconciliation:
[`../../design.md`](../../design.md) Decision 2.

Covers the console's visual and interaction substrate: a **single token set** with no page-local
palettes, statuses that are **discriminable by word as well as color** with a visible fallback for
unmodelled values, **loading / empty / error as three distinct renderings** with the three error classes
preserved, an **accessibility floor** already demonstrated by `p4board.html` applied to every page,
**English strings with `en-US` formatting through one swap point**, and **escape-by-default** rendering.

> These are correctness requirements, not styling preferences. Each one exists because the current
> pages contain a specific defect: three forked palettes, an unknown status that renders invisibly,
> an unknown status that impersonates `running`, a page with no escaping helper at all, a Chinese-only
> page, and accessibility on one page of five.

## ADDED Requirements

### Requirement: All visual values SHALL resolve to a single token set

Color, radius, spacing, type scale, font stack and sizing unit SHALL be defined once in a single token
set and referenced from there. No route, component, inline style or graphic attribute SHALL define its
own value.

#### Scenario: No visual literal outside the token definition

- **WHEN** the console source is scanned for color, border-radius and font-family literals
- **THEN** none is found outside the token definition
- **AND** the scan is a build gate, so a literal fails the build.

#### Scenario: Domain tokens live in the shared set

- **WHEN** a surface needs a value specific to its domain, such as the graph's classification colors or
  the chart series palette
- **THEN** that value is added to the shared token set under its own name
- **AND** it is not defined locally to the surface that uses it.

#### Scenario: Distinct meanings keep distinct token names even at equal values

- **WHEN** two tokens denote different concepts but currently resolve to the same value
- **THEN** both names exist in the token set
- **AND** neither is replaced by the other, so they can diverge without a rename.

### Requirement: Every status SHALL be conveyed by a distinct color and a distinct word

A status SHALL never be conveyed by color alone. Every status rendering SHALL include a textual label,
and statuses SHALL be distinguishable when color is unavailable.

#### Scenario: Status is readable without color

- **WHEN** a status indicator is rendered and color is unavailable or indistinguishable
- **THEN** the status remains identifiable from its text
- **AND** the meaning does not depend on hue.

#### Scenario: Categorical marks differ by more than hue

- **WHEN** a chart encodes a categorical distinction such as frontier membership or edge kind
- **THEN** the distinction is also carried by shape, pattern, or marker
- **AND** it survives greyscale rendering.

### Requirement: Two conditions with different remedies SHALL NOT collapse into one rendering

Conditions that require different user action SHALL render distinguishably. A rendering that would be
true of every condition in a set SHALL NOT be used to represent that set.

#### Scenario: Distinct conditions render distinctly

- **WHEN** two conditions with different user remedies occur
- **THEN** each renders distinguishably from the other
- **AND** the rendering indicates which remedy applies.

#### Scenario: A non-measurable dimension is not rendered as a failure

- **WHEN** a measured dimension has no obligations and is therefore not measurable
- **THEN** it renders as not-measurable
- **AND** it does not render as zero achievement, which would read as failure.

### Requirement: An unmodelled status value SHALL render with a defined fallback and its raw value

A status value the design system does not model SHALL render with a defined fallback style **and** SHALL
display the raw value. It SHALL NOT render unstyled, and SHALL NOT adopt the styling of a modelled
status.

#### Scenario: An unknown status is visibly unknown

- **WHEN** the platform returns a status value the token set does not model
- **THEN** the fallback style is applied and the raw value is displayed
- **AND** the rendering is distinguishable from every modelled status.

#### Scenario: An unknown status does not impersonate a known one

- **WHEN** an unmodelled status is rendered
- **THEN** it does not take the styling of any modelled status, including the in-progress style
- **AND** a reader cannot mistake it for a state the system understands.

### Requirement: Loading, empty and error SHALL be three distinct renderings on every view

Every data view SHALL render loading, empty and error states distinguishably. An error SHALL NOT be
rendered as empty, and a loading state SHALL NOT persist as the representation of a failure.

#### Scenario: The three states are distinguishable

- **WHEN** a view is loading, then resolves with no data, then fails
- **THEN** each of the three produces a distinguishable rendering
- **AND** none is represented by the same output as another.

#### Scenario: Empty copy reflects the subject's status

- **WHEN** a view resolves with no data and the subject's status implies why
- **THEN** the empty copy reflects that status
- **AND** an in-progress subject with no data yet does not read the same as a finished subject that
  produced none.

#### Scenario: An error preserves the surrounding controls

- **WHEN** a data view fails
- **THEN** the controls needed to retry or change subject remain rendered
- **AND** the page does not collapse to the error alone.

### Requirement: The three error classes SHALL render three distinct messages

**Subsystem-not-mounted**, **not-found**, and **transport failure** SHALL each render distinct copy on
every view. A not-found SHALL NOT be rendered as a business state, and a transport failure SHALL NOT be
rendered as an empty result.

#### Scenario: Three classes, three messages

- **WHEN** a view encounters a not-mounted subsystem, a missing subject, and an unreachable server in
  turn
- **THEN** each produces distinct copy
- **AND** each names the condition the user can act on.

#### Scenario: Not-found is not converted into a business state

- **WHEN** a request for a subject returns not-found
- **THEN** the view renders not-found
- **AND** it does not render a business conclusion about the subject, such as that it exists but has no
  content.

#### Scenario: Transport failure is not rendered as absence

- **WHEN** the server is unreachable
- **THEN** the view renders a transport failure
- **AND** it does not render an empty result that would imply the data does not exist.

### Requirement: Every interactive element SHALL be keyboard-reachable with a visible focus indicator

All interactive elements SHALL be operable by keyboard alone, in a sensible order, with a visible focus
indicator.

#### Scenario: The full interface is operable by keyboard

- **WHEN** a user navigates a view using only the keyboard
- **THEN** every interactive element can be reached and activated
- **AND** the focused element is visibly indicated at all times.

#### Scenario: Content revealed on hover is reachable by focus

- **WHEN** content is revealed by pointer hover, such as a chart tooltip
- **THEN** the same content is revealed by keyboard focus
- **AND** it is dismissed on blur.

### Requirement: Graphical data representations SHALL carry text alternatives and tabular fallbacks

Every graphical data representation SHALL carry a text alternative conveying the values it encodes, and
every chart SHALL provide an accessible tabular representation of the same data.

#### Scenario: A graphical mark announces its values

- **WHEN** an assistive technology encounters a graphical data mark such as an interval bar or a scatter
  point
- **THEN** a text alternative names the series and the values encoded
- **AND** the information is not available only visually.

#### Scenario: A chart has a tabular equivalent

- **WHEN** a chart is rendered
- **THEN** an accessible table of the same data is available
- **AND** it contains the same rows the chart plots.

#### Scenario: A diagram is not an unlabelled graphic

- **WHEN** a workflow graph is rendered
- **THEN** its nodes, edges and region labels are available as text
- **AND** the diagram is not exposed as a single unlabelled graphic.

### Requirement: Data tables SHALL use scoped column headers

Every data table SHALL associate its cells with column headers through scoped header markup.

#### Scenario: Cells announce their column

- **WHEN** an assistive technology reads a data-table cell
- **THEN** the associated column header is announced
- **AND** every data table in the console behaves this way.

### Requirement: Contrast SHALL meet WCAG 2.1 AA on every page

Text and meaningful non-text contrast SHALL meet WCAG 2.1 AA across the token set, in every state
including hover, focus, disabled and error.

#### Scenario: Every page passes the contrast audit

- **WHEN** each console page is audited for contrast
- **THEN** it meets WCAG 2.1 AA
- **AND** no page is exempted.

### Requirement: UI strings SHALL be English and locale formatting SHALL be pinned through one swap point

All user-facing strings SHALL be English. All locale-sensitive date, time and number formatting SHALL
resolve to `en-US` through a single swap-point function, and SHALL NOT depend on the browser locale.

#### Scenario: No non-English UI string ships

- **WHEN** the console's user-facing strings are scanned
- **THEN** all are English
- **AND** the scan covers labels, buttons, tooltips, placeholders, empty states, error copy, table
  headers and null placeholders.

#### Scenario: Formatting does not follow the browser locale

- **WHEN** the console renders a formatted date, time or number in a browser configured for a non-English
  locale
- **THEN** the output is formatted as `en-US`
- **AND** it does not produce a value in the browser's language beside an English label.

#### Scenario: Locale resolution has exactly one source

- **WHEN** the console constructs a locale-sensitive formatter
- **THEN** the locale comes from the single swap-point function
- **AND** no formatter resolves its locale independently.

### Requirement: Values SHALL be escaped on render

All values rendered into the document SHALL be escaped by default. Raw markup injection SHALL NOT be
used to render platform-supplied or user-supplied values.

#### Scenario: A value containing markup renders as text

- **WHEN** a platform-supplied value such as a node identifier contains markup characters
- **THEN** it renders as literal text
- **AND** no markup from the value is interpreted.

#### Scenario: Raw-markup rendering is not used for data

- **WHEN** the console source is scanned for raw-markup injection
- **THEN** none is used to render platform-supplied or user-supplied values
- **AND** any exception is explicitly reviewed and allowlisted.

### Requirement: Acceptance of a user-visible behavior SHALL require rendered-browser evidence

A change to a user-visible behavior SHALL be accepted only on evidence from rendering it in a real
browser against a real API response, with the error path exercised. A successful build, a passing type
check, or passing unit tests SHALL NOT by themselves constitute acceptance.

#### Scenario: A green build is not acceptance

- **WHEN** a change to a user-visible behavior is proposed with only build, type-check and unit-test
  evidence
- **THEN** it is not accepted
- **AND** rendered-browser evidence is required.

#### Scenario: The rendering is checked against the response

- **WHEN** rendered-browser evidence is produced
- **THEN** it demonstrates that the rendered output agrees with the API response that produced it
- **AND** it is captured at a fixed viewport so it is reproducible.

#### Scenario: The error path is exercised, not only the happy path

- **WHEN** acceptance evidence is produced for a data view
- **THEN** it covers the failure states as well as the populated state
- **AND** the three error classes are shown to render distinctly.

### Requirement: Every view SHALL have exactly one subject, rendered before its data resolves

Every view SHALL carry exactly one display-level heading naming its subject, and that subject SHALL be
present in the first paint — before its data resolves. A view SHALL NOT open as an undifferentiated
loading indicator, and the arrival of data SHALL NOT change the view's structure.

#### Scenario: The subject is on screen before the data

- **WHEN** a view is opened for a known subject and its data request has not yet resolved
- **THEN** the subject is already named on screen
- **AND** the view's structure is the structure the populated view will have.

#### Scenario: Data arrival does not reflow the view

- **WHEN** a view's data resolves and it repaints
- **THEN** the structural signature of the view is unchanged from its loading render
- **AND** only values change.

#### Scenario: One subject per view

- **WHEN** any console route is rendered
- **THEN** it carries exactly one display-level heading
- **AND** that heading names the route's subject.

### Requirement: Depth, motion and emphasis SHALL be hierarchy signals drawn from the token set

Elevation, motion and emphasis SHALL come from the token set and SHALL carry hierarchy or continuity.
Every duration SHALL be a motion-budget token, no transition SHALL sit between a user's intent and the
resulting action, and `prefers-reduced-motion` SHALL lose no information.

#### Scenario: No duration outside the motion budget

- **WHEN** the console's styles are scanned for transition and animation durations
- **THEN** every duration resolves to a motion-budget token
- **AND** a literal duration fails the build.

#### Scenario: Reduced motion loses no information

- **WHEN** the viewer's system requests reduced motion
- **THEN** every state a transition would have communicated is still communicated statically
- **AND** no information is available only to a viewer who accepts motion.

#### Scenario: Motion is never on the action path

- **WHEN** a control is operated while a transition is in flight
- **THEN** the control responds immediately
- **AND** no confirmation, navigation or data request waits for an animation to finish.

### Requirement: The confidence treatment SHALL be reserved for values the server did not qualify

Emphasis reserved for a settled result — accent color, elevation above peers, entrance animation, or
display-weight type — SHALL NOT be applied to a value the server marked `provisional`, `tie`,
`disqualified`, `low-confidence`, uncalibrated, `withheld`, `candidate`, unverified, or gated by
entitlement. Two values with different certainty SHALL NOT render with the same emphasis.

#### Scenario: A tied leader does not render as a winner

- **WHEN** the board's top-ranked row carries a `tie` flag
- **THEN** the row does not carry the confidence treatment
- **AND** its rank renders de-emphasized, because overlapping intervals are an ordering the server
  declined to assert.

#### Scenario: A qualified value never carries the settled-result emphasis

- **WHEN** a value is marked `provisional`, `disqualified`, `low-confidence`, `withheld`, `candidate`,
  unverified, or gated
- **THEN** it is rendered without the confidence treatment
- **AND** its qualifier is rendered beside it rather than only in a tooltip.

#### Scenario: The reservation is machine-checked

- **WHEN** a component applies the confidence treatment to a qualified value
- **THEN** a test fails
- **AND** the failure names the qualifier that was overridden.

### Requirement: The console SHALL anticipate the next move rather than re-ask for what it knows

The console SHALL offer subjects this session has already visited before requiring an identifier, SHALL
provide a keyboard command path to every subject and surface, and SHALL carry the current subject across
surfaces.

#### Scenario: Already-visited subjects are offered first

- **WHEN** a selection surface is opened after the session has visited subjects
- **THEN** those subjects are offered without the user typing an identifier
- **AND** no subject is substituted as a default for one that was not chosen.

#### Scenario: Every surface is reachable from the keyboard

- **WHEN** the keyboard command path is opened
- **THEN** every surface and every already-visited subject is reachable from it
- **AND** it is dismissible without leaving the current view.

#### Scenario: The subject survives a surface change

- **WHEN** the user moves from one surface to another while a subject is selected
- **THEN** the destination opens on the same subject
- **AND** the user is not asked to identify it again.

### Requirement: Numbers SHALL be rendered for comparison and never without their qualifier

Numerals in tables and stats SHALL use tabular figures, numeric columns SHALL be digit-aligned, unit and
scale SHALL be stated once per column or stat, and a figure SHALL be rendered together with the
qualifier the server attached to it.

#### Scenario: A figure carries its qualifier

- **WHEN** the server returns a figure with an interval, a seed count, a coverage fraction or a
  calibration flag
- **THEN** the figure is rendered together with that qualifier
- **AND** the qualifier is not deferred to a tooltip or a secondary view.

#### Scenario: Columns are comparable at a glance

- **WHEN** a table of numerals is rendered
- **THEN** the numerals are tabular-figure and right-aligned
- **AND** the unit and scale appear once in the header rather than repeated per cell.

### Requirement: The measured value SHALL outrank its frame

In every **summary block** — a block whose purpose is to present a small fixed set of headline
quantities — the quantity SHALL be the visually dominant element. Its label, unit and provenance SHALL
be subordinate to it, and no section heading, card border, chip or other chrome SHALL carry more
visual weight than the values that block exists to present.

This does **not** govern a table. A table is a comparison surface, and its power is that many values
are legible in one plane at one size; setting its cells at display scale would destroy the comparison
it exists to enable. Tables remain governed by the tabular-figure and unit-once requirement.

This requirement composes with — and never overrides — the confidence reservation. Size reads as
certainty, so emphasis applied to a qualified value is a larger defect at display scale than at body
scale.

#### Scenario: The number is the largest thing in its block

- **WHEN** a view renders a summary block of measured quantities
- **THEN** the quantity's rendered type size exceeds that of its own label, of the heading of the
  section containing it, and of every chrome element in that block
- **AND** its unit and scale are stated once rather than repeated per value.

#### Scenario: A table keeps one size for comparison

- **WHEN** a table of many values is rendered
- **THEN** its cells share one type size so the column can be compared at a glance
- **AND** the summary-block rule is not applied to them.

#### Scenario: A section frame never dominates its content

- **WHEN** a section is rendered around one or more quantities
- **THEN** no part of the section's frame — heading, border, or padding-created emphasis — reads as
  more prominent than the quantities inside it.

#### Scenario: Display scale does not confer confidence on a qualified value

- **WHEN** the server marks a value provisional, tied, disqualified, low-confidence, uncalibrated,
  unverified, withheld, candidate or gated
- **THEN** rendering that value at display scale does not apply the confidence treatment to it
- **AND** its qualifier is rendered beside it at that scale, not deferred to a tooltip.

### Requirement: Theme SHALL be chosen rather than assumed

The console SHALL offer an explicit theme control with follow-system, dark and light settings, SHALL
persist the choice, and SHALL resolve it server-side so the first paint is already in the chosen theme.

#### Scenario: The first paint is already correct

- **WHEN** a user with a persisted theme choice loads any route
- **THEN** the first paint renders in that theme
- **AND** no theme flash and no post-hydration reflow occurs.

#### Scenario: Both themes meet the contrast floor

- **WHEN** the token set is evaluated in each resolved theme
- **THEN** every foreground/background pair meets WCAG 2.1 AA at its intended size in both
- **AND** no information is carried by a hue present in only one theme.

#### Scenario: Following the system is a real setting

- **WHEN** the control is set to follow-system and the operating system preference changes
- **THEN** the rendered theme changes with it without an explicit reload.

### Requirement: The shipped client payload SHALL have an enforced ceiling

The build SHALL fail when the shipped client payload exceeds a stated byte budget, naming the budget
and the overage. No rendering runtime SHALL be shipped for decorative purposes.

#### Scenario: An over-budget bundle fails the build

- **WHEN** the shipped client payload exceeds the stated ceiling
- **THEN** the build fails
- **AND** the failure names both the budget and the measured overage.

#### Scenario: No decorative runtime ships

- **WHEN** the shipped bundle is inspected
- **THEN** it contains no 3D, WebGL or animation runtime included for visual effect.

### Requirement: Acceleration SHALL NOT become the only path and the console SHALL NOT act for the user

Every capability reachable from the keyboard command path SHALL also be reachable by navigation, and no
surface SHALL pre-fill, infer, or auto-submit an input that carries the user's intent.

#### Scenario: The command path is additive

- **WHEN** a capability is offered in the command path
- **THEN** the same capability is reachable by navigating the shell
- **AND** no capability exists that only the command path can reach.

#### Scenario: Intent is never supplied by the console

- **WHEN** a surface presents an input whose value expresses the user's intent
- **THEN** it opens empty rather than pre-filled from context or history
- **AND** it is not auto-submitted on the user's behalf.

### Requirement: A gated capability SHALL be rendered as gated, never as an error

A capability the tenant's plan does not include SHALL be rendered as a distinct state naming the plan
that unlocks it, using the entitlement decision the platform returned. It SHALL NOT be rendered as an
error, SHALL NOT be hidden without explanation, and SHALL NOT borrow the hazard palette.

#### Scenario: An entitlement refusal is not an error

- **WHEN** the platform refuses a request because the capability is outside the tenant's plan
- **THEN** the console renders a gated state distinct from every failure class
- **AND** it states that this is a plan boundary rather than a failure.

#### Scenario: The unlocking plan is the platform's answer, not the console's guess

- **WHEN** a gated state is rendered
- **THEN** the plan it names is the one carried in the platform's entitlement decision
- **AND** the console does not substitute a plan name resolved from its own capability table.

#### Scenario: The gated state does not spend the reserved palette

- **WHEN** the gated state is rendered
- **THEN** it does not use the hazard hues reserved for hazard
- **AND** it remains distinguishable from a not-mounted, not-found, transport or upstream failure.

#### Scenario: A gated view keeps its frame

- **WHEN** a view's data is refused on entitlement grounds
- **THEN** the view still renders its single display-level heading naming the subject
- **AND** the reader can confirm which subject they opened.
