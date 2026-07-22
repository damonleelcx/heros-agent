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
