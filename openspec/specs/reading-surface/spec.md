# Reading Surface — Spec (folded from P23)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR33–FR34), §7 (NFR1–NFR5) and §9.4 (Frontend lens). Technical decisions:
[`../../changes/archive/2026-08-01-p23-legal-and-docs/design.md`](../../changes/archive/2026-08-01-p23-legal-and-docs/design.md) Decision 7.

Covers the composition legal documents and developer documentation render in — a third one, beside the
dark-fixed marketing poster and the session-bound console shell.

> The console follows the reader's theme because it is sat in front of for an hour. The public surface is
> dark-fixed because it is a poster seen once. A legal document and a reference page are neither: they are
> read long, printed, searched with the browser's own find, and deep-linked. That is three sets of
> requirements, so it is three compositions — not one composition with conditionals, which is how a public
> page acquires a session call.

## Requirements

### Requirement: The reading surface SHALL be a separate composition that holds no session and makes no fetch

Legal and documentation routes SHALL render in their own layout, which SHALL NOT require a session, SHALL NOT
call the session store, and SHALL NOT fetch from the platform.

#### Scenario: The composition is structurally session-free
- **WHEN** any reading-surface route is rendered
- **THEN** no session lookup and no platform request occurs
- **AND** the harness's upstream-request counter does not move.

#### Scenario: It is not the console shell with parts hidden
- **WHEN** the reading layout is inspected
- **THEN** it is a distinct layout rather than a conditional branch of the console or public shells.

### Requirement: The reading surface SHALL follow the reader's theme and remain legible in both

The surface SHALL use the console's theme tokens rather than the fixed marketing tokens, and SHALL meet WCAG
2.2 AA in both light and dark.

#### Scenario: A reader's theme is honored
- **WHEN** a reader with a light theme preference opens a legal document or a docs page
- **THEN** the page renders in the light theme.

#### Scenario: Contrast holds in both themes
- **WHEN** the design-system checks run against the reading surface
- **THEN** text and interactive elements meet WCAG 2.2 AA contrast in both themes.

### Requirement: The reading surface SHALL scroll as a document, as a stated exemption from the viewport-first rule

The surface SHALL use document scroll rather than the console's bounded-`main` viewport-first layout, and the
exemption SHALL be recorded in the layout's own source alongside its reasoning.

#### Scenario: A long document scrolls as one document
- **WHEN** a reader opens a long legal document or reference page
- **THEN** the page scrolls as a document, with the browser's own scroll position and find behavior intact
- **AND** the text is not confined to a bounded inner scroll region.

#### Scenario: The exemption reads as a decision
- **WHEN** an engineer reads the reading layout's source
- **THEN** the viewport-first exemption and its reasoning are stated there
- **AND** it cannot be mistaken for a page somebody forgot to bound.

### Requirement: The surface SHALL offer a table of contents as a navigation landmark whose current section is named in words

The table of contents SHALL be a `nav` landmark, and the current section SHALL be marked by `aria-current`
and by a textual indication — never by colour alone.

#### Scenario: A screen-reader user knows where they are
- **WHEN** a reader navigates the table of contents with assistive technology
- **THEN** the contents are exposed as a navigation landmark
- **AND** the current section is announced.

#### Scenario: Colour is never the only signal
- **WHEN** the current section is rendered
- **THEN** it carries a word or symbol in addition to any colour difference.

### Requirement: The reading surface SHALL ship no client JavaScript beyond the table-of-contents and search islands

Content SHALL be server-rendered; only the table-of-contents behavior and search SHALL be client components,
and the console's bundle budget SHALL be unchanged.

#### Scenario: The bundle budget is unchanged
- **WHEN** the bundle scan runs after the reading surface is added
- **THEN** the budget is not exceeded
- **AND** no new client dependency is introduced for content rendering.

#### Scenario: Content renders without JavaScript
- **WHEN** a reading-surface page is opened with JavaScript disabled
- **THEN** the content and a browsable table of contents render in full.

### Requirement: The reading surface SHALL reuse the console's existing tabs component for multi-variant samples

Where a sample is shown in more than one language or variant, the surface SHALL use the console's existing
tabs component rather than introducing a second implementation.

#### Scenario: One tabs implementation exists
- **WHEN** a multi-language sample is rendered
- **THEN** it uses the console's existing tabs component
- **AND** its keyboard navigation and focus behavior match the rest of the console.
