## Why

The operator console (P8, `web/admin-console`) is built as a document, like the customer console was:
`body { min-height: 100vh }`, a sticky chrome band, and `main` in normal flow with stacked `Section`
panels. The tenant-detail page stacks **13 sections**; the overview and fleet views stack several. An
operator mid-incident scrolls past panels to reach the one that matters, and — worse for this console —
the **kill-switch alarm** and the **acting-principal / impersonation band** can scroll out of view,
which for a surface whose whole job is blast-radius awareness is a real defect, not a cosmetic one.

The customer console fixed this (P9 NFR17) with a **viewport-first** layout: a fixed-height shell that
never page-scrolls, and multi-section pages split into **in-page tabs**. This change applies the same
pattern to the operator console.

## What Changes

- **The operator shell owns a fixed viewport height on desktop.** `body` becomes a fixed-height flex
  column (`100dvh`, `overflow: hidden`); the chrome band and the impersonation banner are `shrink-0` and
  never scroll away; `main` is the single bounded scroll region. Mobile keeps natural scroll.
- **A compact page header** (`PageFrame`) with reduced vertical rhythm, so the page spends less height
  on its title band.
- **A reusable, accessible `Tabs` primitive** (a real ARIA `tablist`, keyboard operable), used to split
  the tenant-detail page's 13 sections into a handful of tabs (e.g. Overview · Billing & entitlements ·
  Activity · Compliance), and any other page that stacks. One section on screen at a time.
- **One scroll owner per region.** Long tables/lists (audit, fleet) scroll inside their own bounded box;
  the page does not scroll on desktop.
- **A guard (NFR):** browser-measured `documentElement.scrollHeight ≤ innerHeight` for every admin view,
  plus a source guard that the shell uses the fixed-height model and the `Tabs` primitive is a real
  tablist.

## Impact

- **Affected capability:** `admin-console` (P8) — the shell/layout surface. ADDED requirement
  (viewport-first) below. No data, permission, audit, or backend change.
- **Affected code:** `web/admin-console/src/app/globals.css` (the height model), `src/components/
  primitives.tsx` (`PageFrame`) + a new `src/components/tabs.tsx`, and the pages that stack sections
  (tenant detail first). P8 PRD gains the viewport-first NFR.
- **Dependencies:** P8 (the operator console). A layout/UX change only.
- **Breaking:** none — no capability, control, or audited action is removed; sections move from a stack
  into tabs (`ui-redesign-feature-and-visual-consistency`). Every permission gate, confirmation, and
  alarm renders exactly as before, just within a fixed shell.
