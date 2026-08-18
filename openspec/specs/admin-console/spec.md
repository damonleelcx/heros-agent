# Admin Console — Spec (folded from P8)

Product rationale: [`../../../docs/prd/P8-admin-console.md`](../../../docs/prd/P8-admin-console.md)
§7 (viewport-first NFR). Extends the `admin-console` shell with a fixed-height, tabbed layout. Mirrors
the customer console's P9 viewport-first delta. Everything P8 says about permissions, confirmations,
audit, and the alarm/impersonation chrome still applies and is not restated.

## Requirements

### Requirement: The operator shell SHALL occupy the viewport height and SHALL NOT page-scroll on desktop

On a desktop viewport the operator shell — chrome band, impersonation banner, and content — SHALL
occupy exactly the viewport height and SHALL NOT produce a page-level vertical scrollbar. The chrome,
the impersonation banner, and the alarm banner SHALL remain visible while an operator works.

#### Scenario: No page-level scrollbar on desktop

- **WHEN** any authenticated admin view is rendered at a standard desktop viewport
- **THEN** the document does not exceed the viewport height (`scrollHeight ≤ innerHeight`)
- **AND** the chrome band and the acting-principal / impersonation band do not scroll out of view.

#### Scenario: The alarm never scrolls away

- **WHEN** the global kill switch is armed and the alarm banner is shown
- **THEN** the alarm remains visible regardless of where the operator is in the page's content
- **AND** it is not below the fold of a scrolled document.

### Requirement: Multi-section admin pages SHALL use in-page tabs rather than a vertical stack

A page with several sections that would otherwise stack into a tall scroll (the tenant detail's 13
sections) SHALL present them as in-page tabs — one section visible at a time — each fitting the
viewport. Long content within a tab SHALL scroll inside its own bounded panel.

#### Scenario: The tenant detail is tabbed

- **WHEN** a tenant's detail view is opened
- **THEN** its sections are grouped into selectable tabs
- **AND** only the active tab's content occupies the content region.

#### Scenario: No control or audited action is removed

- **WHEN** the redesigned admin pages are enumerated
- **THEN** every permission-gated control, confirmation, and audited action that existed still exists
- **AND** it has moved into a tab or a bounded panel, not disappeared.

#### Scenario: Tabs are keyboard operable

- **WHEN** an operator switches tabs by keyboard
- **THEN** the newly-selected section is shown and focus is managed
- **AND** the tablist exposes `role="tab"`/`aria-selected` for assistive technology.
