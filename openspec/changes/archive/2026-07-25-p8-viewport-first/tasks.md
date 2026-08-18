# Tasks — P8 Viewport-First Operator Console

Apply the customer console's viewport-first pattern to the operator console: fixed-height shell that
never page-scrolls, multi-section pages split into in-page tabs, long content in bounded panels. The
kill-switch alarm and the acting-principal band must never scroll away. No control or audited action is
removed — sections move into tabs.

---

## A1. Product Designer — research + PRD

- [x] A1.1 Identify the offenders (tenant detail = 13 sections; overview/fleet stack) and decide the
      principle: fixed-height shell + in-page tabs; the alarm/impersonation band never scrolls away.
- [x] A1.2 Add the viewport-first NFR to the P8 admin-console PRD.

## A2. System Designer — layout contract + openspec

- [x] A2.1 Write the openspec change (proposal, spec delta).
- [x] A2.2 Decide the contract: `body` fixed-height flex column on desktop; chrome/banner `shrink-0`;
      `main` the single bounded scroll owner; a reusable `Tabs` primitive.

## A3. Frontend — shell + tabs + page redesign

- [x] A3.1 Make the operator shell own a fixed viewport height on desktop (`body` flex column, `100dvh`,
      `overflow: hidden`), `main` the scroll owner; mobile keeps natural scroll.
- [x] A3.2 Compact the `PageFrame` header (reduced vertical rhythm).
- [x] A3.3 Add a reusable, accessible `Tabs` primitive (keyboard operable, `role="tablist"`).
- [x] A3.4 Split the **tenant detail** (13 sections) into tabs; tab or bound-scroll any other stacking
      page (overview, fleet, billing, audit).
- [x] A3.5 🔴 No control/audited action dropped: every permission gate, confirmation, and alarm still
      renders — moved into a tab or bounded panel.
- [x] A3.6 Browser-check reachable admin views in Chrome at 1280×800; confirm no page-level scroll.

## A4. QA — no-scroll acceptance gate

- [x] A4.1 Source guard (failing test): the shell uses the fixed-height model; `Tabs` is a real
      tablist; the tenant-detail page uses tabs.
- [x] A4.2 Browser-rendered acceptance where reachable: `scrollHeight ≤ innerHeight` for admin views.

## A5. DevOps — guards + build green

- [x] A5.1 `tsc` + `scan:tokens` + `node --test` pass after the layout change.
- [x] A5.2 Add the layout guard to the admin test suite.

## A6. Sales Operations — one-screen note

- [x] A6.1 Note the operator console fits one screen — the alarm and acting-principal band are always
      visible during an incident demo.

## A7. Run for hermes

- [x] A7.1 Confirm the operator console (against the hermes-backed platform) fits one screen.
