## Why

The console is built as a **document**: each page renders a `PageFrame` that stacks its sections
vertically (`flex flex-col gap-10`), and the shell uses `min-h-screen` so the whole page grows and the
browser scrolls. Measured at a 1280×800 desktop viewport: the studio overflows by **~2600px** (4× the
screen), configure by ~290px, overview by ~200px. To reach the studio's matrix — its primary surface —
a user scrolls past a banner, a workflow selector, and then the grid pushes the cell panel and an entire
second page below it. "The studio is too deep down" is the symptom; a document layout is the cause.

A dashboard is not a document. The fix is **viewport-first**: the shell occupies exactly the viewport
and never page-scrolls, and a page's several sections become **in-page tabs** — one section on screen at
a time — instead of a tall stack. Genuinely long content (a table, a list, a long output) scrolls inside
its own bounded panel, so exactly one region owns the scroll and the header, rail, tab strip and page
actions never move.

## What Changes

- **The app shell owns a fixed viewport height.** `/app` becomes `h-dvh` with `overflow-hidden` on
  desktop; the header and rail are fixed, and `main` is a bounded region pages lay out within. Mobile
  keeps natural scroll (a small screen legitimately scrolls).
- **`PageFrame` becomes a viewport-fitting frame:** a compact, `shrink-0` header and a `flex-1 min-h-0`
  body, so a page fills the main region rather than growing past it. Reduced vertical padding.
- **A reusable `Tabs` primitive** splits a page's sections into tabs. Applied first to the studio
  (**Matrix** · **Prompt library** · **Bound nodes**), so its primary surface — the matrix — is the
  landing tab and nothing stacks below it. Other multi-section pages adopt tabs where they stack.
- **One scroll owner per region.** Long tables/lists/outputs get `overflow-y-auto` inside a bounded box;
  the page itself does not scroll on desktop.
- **A machine-checked guard (NFR17):** browser-measured `documentElement.scrollHeight ≤ innerHeight` for
  every `/app/*` view at a standard desktop viewport, plus a source guard that the shell uses the height
  model and no page reintroduces a page-level `min-h-screen` growth.

## Impact

- **Affected capability:** `web-console` (P9) — the shell/navigation/layout surface. ADDED requirements
  (viewport-first) below. No data, BFF, or platform change.
- **Affected code:** `web/console/src/app/app/layout.tsx` (the shell height model),
  `src/components/primitives.tsx` (`PageFrame`, new `Tabs`), and each `/app/*` page that stacked
  sections (studio first; workflows/runs/variants/transforms/configure/account/overview as needed).
  `web/design-system/DESIGN-BRIEF.md` gains the viewport-first principle; P9 PRD gains NFR17.
- **Dependencies:** P9 (the console). Purely a layout/UX change.
- **Breaking:** none functional — every page keeps all its content and behavior (the
  `ui-redesign-feature-and-visual-consistency` rule: a redesign must not drop a feature). Content moves
  from a vertical stack into tabs/bounded panels; no capability is removed.
