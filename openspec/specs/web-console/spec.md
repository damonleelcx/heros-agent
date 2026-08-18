# Web Console — Spec (folded from P9)

Product rationale: [`../../../docs/prd/P9-web-console.md`](../../../docs/prd/P9-web-console.md)
NFR17. Extends the `web-console` capability with a viewport-first layout: the shell never page-scrolls,
and a page's sections become in-page tabs rather than a tall stack. Everything P9 says about the shell,
navigation, states, and accessibility still applies and is not restated.

## Requirements

### Requirement: The desktop shell SHALL occupy the viewport height and SHALL NOT page-scroll

On a desktop viewport the console shell — header, navigation rail, and main region — SHALL occupy
exactly the viewport height and SHALL NOT produce a page-level vertical scrollbar. The header, rail, and
a page's header and primary actions SHALL remain fixed while a user works.

#### Scenario: No page-level scrollbar on desktop

- **WHEN** any `/app/*` view is rendered at a standard desktop viewport
- **THEN** the document does not exceed the viewport height (`scrollHeight ≤ innerHeight`)
- **AND** the header and navigation rail do not move when the user interacts with the content.

#### Scenario: Mobile retains natural scroll

- **WHEN** the console is rendered on a small (mobile) viewport
- **THEN** natural page scrolling is permitted
- **AND** the fixed-height constraint applies to desktop, not to a phone.

### Requirement: A page's primary content and actions SHALL be visible without scrolling

Each view's primary content and primary actions SHALL be visible without scrolling. Content that
exceeds its region SHALL scroll inside its OWN bounded panel, and exactly one region SHALL own that
scroll.

#### Scenario: The primary surface is the landing view

- **WHEN** the studio is opened
- **THEN** the matrix (its primary surface) is on screen without scrolling
- **AND** it is not below a banner, a selector, and other sections stacked above it.

#### Scenario: Long content scrolls inside a bounded panel, not the page

- **WHEN** a view contains a table or list longer than its region
- **THEN** that table or list scrolls within its own bounded box
- **AND** the page, header, rail, and actions do not scroll with it.

### Requirement: Multi-section pages SHALL use in-page tabs rather than a vertical stack

A page with several sections that would otherwise stack into a tall scroll SHALL present them as in-page
tabs — one section visible at a time — so each fits the viewport. Switching tabs SHALL NOT reload the
page or lose the page's context, and SHALL be keyboard operable.

#### Scenario: The studio's sections are tabs

- **WHEN** the studio is displayed
- **THEN** its matrix, prompt library, and bound-node views are selectable tabs
- **AND** only the active tab's content occupies the content region.

#### Scenario: Tabs are keyboard operable and do not drop content

- **WHEN** a user switches tabs by keyboard
- **THEN** the newly-selected section is shown and focus is managed
- **AND** no section's content or actions are removed by the redesign — they move between tabs, they do
  not disappear.
