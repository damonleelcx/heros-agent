# Tasks — P9 Viewport-First Console Layout

Make the console fit one screen: the shell owns a fixed viewport height and never page-scrolls; a page's
stacked sections become **in-page tabs**; long content scrolls inside its own bounded panel. Measured:
`documentElement.scrollHeight ≤ innerHeight` for every `/app/*` view (NFR17). No feature is removed —
content moves from a vertical stack into tabs (`ui-redesign-feature-and-visual-consistency`).

---

## N1. Product Designer — research + PRD

- [x] N1.1 Measure current per-page overflow (studio ~2600px, configure ~290px, overview ~200px at 800px).
- [x] N1.2 Decide the principle: fixed-height shell, in-page tabs, one bounded scroll owner per region.
- [x] N1.3 Add P9 NFR17 and the design-brief "console fits one screen" principle.

## N2. System Designer — layout contract + openspec

- [x] N2.1 Write the openspec change (proposal, spec delta).
- [x] N2.2 Decide the layout contract: `h-dvh` shell + `overflow-hidden` on desktop; compact `PageFrame`
      (`shrink-0` header + `flex-1 min-h-0` body); a reusable `Tabs` primitive.
- [x] N2.3 Constrain the redesign to not drop any feature (content moves to tabs, never disappears).

## N3. Frontend — viewport-first shell + tabs + page redesign

- [x] N3.1 Make the app shell own a fixed viewport height on desktop (`h-dvh`, `overflow-hidden`),
      mobile keeps natural scroll.
- [x] N3.2 Make `PageFrame` a viewport-fitting frame: compact `shrink-0` header, `flex-1 min-h-0` body,
      reduced padding.
- [x] N3.3 Add a reusable, accessible `Tabs` primitive (keyboard operable, `role="tablist"`).
- [x] N3.4 Redesign the **studio** with tabs: **Matrix** (landing) · **Prompt library** · **Bound
      nodes** — nothing stacks below the matrix; the cell panel sits beside the grid, not below it.
- [x] N3.5 Reduce overflow on the other `/app/*` pages (overview, workflows, runs, variants, transforms,
      configure, account): compact headers + bounded inner-scroll for long lists/tables; tabs where a
      page still stacks.
- [x] N3.6 🔴 No feature dropped: every section that existed still exists, moved into a tab or a bounded
      panel. Keep the exploratory/no-ranking labels and all controls.
- [x] N3.7 Browser-test every `/app/*` page in Chrome at 1280×800; confirm no page-level scroll.

## N4. QA — no-scroll acceptance gate

- [x] N4.1 A source guard (failing test) that the shell uses the fixed-height model and no page
      reintroduces page-level growth.
- [x] N4.2 Browser-rendered acceptance: `documentElement.scrollHeight ≤ innerHeight + tolerance` for
      every `/app/*` view at 1280×800.
- [x] N4.3 Assert the studio's sections are tabs and the matrix is the landing tab.

## N5. DevOps — guards + build stay green

- [x] N5.1 `tsc` + the 4 design scanners + `node --test` all pass after the layout change.
- [x] N5.2 Add the layout guard to the console test suite so it runs in CI.

## N6. Sales Operations — one-screen demo note

- [x] N6.1 Note in the claims/demo guidance that the console fits one screen — a demo needs no scrolling
      to reach the primary surface.

## N7. Run for hermes

- [x] N7.1 Re-run the studio matrix against `github.com/nousresearch/hermes-agent` and confirm the
      viewport-first studio fits one screen with the 40 real node columns.
