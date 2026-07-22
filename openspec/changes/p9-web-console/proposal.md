## Why

The platform reaches customers through three surfaces, and one of them has never been specified. The
[root README](../../../README.md) lists a **Web dashboard (hosted SaaS)** as delivery surface #3 and
the [timeline](../../../docs/implementation-timeline/README.md) assigns it to Frontend + Product — but
no phase owns it. **P8 is explicitly the internal operator console**, not this. So the customer
dashboard has shipped, phase by phase, as **demo pages**: five hand-written HTML files with inline
`<style>` and inline `<script>`, four of them embedded with `go:embed` and served from unlinked routes
(`/p2`, `/p25/monitor`, `/p35/graph`, `/p4/board`). They were the correct call for proving P2–P4 — no
build step, no dependency tree, and they work — but they cannot become the product, for four reasons
that are structural rather than cosmetic.

**First, a browser cannot authenticate.** The four page routes are public (they are not under `/api/`,
so `IsPublicPath` never gates them), while every `/api/*` call those pages make requires an
`X-API-Key` header. Under `auth_mode=required`, **all four pages load and then fail every fetch with
401**. There is no session, no cookie, and no token exchange anywhere in the repository. The two
available shortcuts are both unacceptable: run the console with auth off (the read models expose a
tenant's prompts, diffs, costs and provider spend), or ship a long-lived platform credential to the
browser (exfiltrable by any XSS, with no per-user revocation). Closing this requires a server-side
credential holder between the browser and the platform API — which is the single decision this change
is organized around.

**Second, the pages are undiscoverable and demand hand-typed identifiers.** There is no index, no
navigation, and no link between the four surfaces. Each entry point is a bare query parameter the user
must already know (`?run=`/`?cfg=`/`?rev=`, `?run_id=`, `?workflow_id=`, `?workflow=`), and
`p4board.html` defaults to the hardcoded string `'wf-demo'` — so a user who opens it without a
parameter is shown a confidently-rendered board for a workflow that is not theirs. **Third, the design
language has already forked three ways** across four files (`--muted`, `--line`, card radius, chip
radius, px-vs-rem sizing, font stack, and an entire second status vocabulary in `p4board.html`) —
which is the absence of a token system, and it compounds with every page added. **Fourth,
accessibility exists on exactly one page of five**: `p4board.html` has focus rings, ARIA-labelled
charts, keyboard row navigation and a tabular chart fallback; the other four have none of it.

There is also a dead page. `internal/api/static/index.html` is a Chinese-only (`lang="zh-CN"`)
approval queue with **no Go handler serving it** and **three endpoints that do not exist**
(`/api/proposals/pending|approve|reject`). It is pre-pivot legacy and violates the English-UI rule —
but its *shape* is exactly the human-in-the-loop surface **P5.5** will need, so it is **forward-ported,
not deleted**.

P9 delivers the console as a **Next.js (App Router, TypeScript) application fronted by its own Node
BFF**, so the platform credential stays server-side and the browser only ever holds a session. On that
boundary it adds one app shell with real navigation, one reconciled token set, the accessibility level
`p4board.html` already proves, plan-tier gating wired to P7, and an explicit **no-feature-loss**
guarantee against a written inventory of every current behavior.

## What Changes

- **New capability `console-bff`.** A Next.js server process that is the **only** origin the browser
  calls for tenant data. It holds the platform API key **server-side** and exchanges it for an
  `HttpOnly`, `SameSite` browser **session** bound to a tenant; the browser never receives platform
  credentials, in the bundle, a readable cookie, `localStorage`, a URL, a log, or a telemetry
  attribute. Console data routes **fail closed** — an unauthenticated request redirects to sign-in
  rather than rendering a shell that 401s. Sessions are bounded and revocable, with revocation
  effective at the **next** request. The BFF is a **pass-through**: it returns platform read models
  **unmodified** and holds no business rules. It preserves the upstream failure taxonomy so
  **503 not-mounted**, **404 not-found** and **transport failure** stay three distinguishable outcomes
  at the browser, proxies the run-monitor **SSE** stream with flush semantics intact and without
  batching, and carries an explicit timeout on every upstream call so a hung dependency surfaces as a
  transport failure rather than an unbounded spinner. Request scope comes from the session's tenant,
  **never** from a client-supplied tenant identifier.
- **New capability `web-console`.** One app shell with navigation across every surface, replacing four
  unlinked pages. The user **selects** a workflow, run, variant, board or transform from
  platform-provided data instead of typing an identifier, and **no route substitutes a hardcoded
  default subject** — the `'wf-demo'` behavior is removed, not ported. Every current entry point maps
  to a stable, shareable **canonical route**. **No feature is lost**: SSE-first with polling fallback,
  **record-driven** poll termination (a run's poll stops on the run record's status, never on a
  node-derived condition), row virtualization above the row threshold, keyboard row navigation with
  wrap-around, expandable per-row score breakdowns, the focus-reachable Pareto tooltip, an accessible
  tabular fallback for every chart, and the graph's back-edge routing, region rectangles and
  control-edge styling all carry forward, enumerated in `feature-inventory.md`. The console
  **renders statistics as received** and computes none of them. A capability outside the tenant's plan
  or automation level renders as **gated with the unlocking plan named** — not hidden, not an error.
  The **proposal-review surface** (queue → rationale → verified delta → diff → approve/reject, in
  English) is specified here and **does not ship before the P5.5 API exists**. Every read-model field
  is either rendered or **listed as deliberately unrendered with a reason** — no field is left
  silently unread.
- **New capability `console-design-system`.** A **single token set** derived from the existing dark
  palette (`#0f1419` surface / `#1a2332` card / `#e7ecf3` text / `#3d8bfd` accent), reconciling the
  three current forks with a recorded winner per token; no route or component defines a page-local
  palette. Every status is carried by **a distinct color and a distinct word** — never color alone —
  and two conditions with different user remedies never collapse into one rendering. A status the
  console does not model renders with a **defined fallback style and the raw value visible**, closing
  the `state-${status}` interpolation hazard that currently renders unknown statuses invisibly.
  **Loading, empty and error are three distinct renderings** on every view, preserving the distinct
  per-error-class copy the current pages already get right. The accessibility level `p4board.html`
  demonstrates becomes the **floor for every page**: keyboard reachability with visible focus, text
  alternatives on graphical data, scoped table headers, tabular chart fallbacks. UI strings are
  **English**, `Intl` formatting is pinned to **`en-US`** through a single swap point, and all
  interpolated values are escaped by default.
- **Operability.** The platform readiness signal **aggregates the console**: a healthy Go service in
  front of an unreachable BFF does not report ready, and the degraded component is named on a readable
  endpoint. The console ships as one declared, supervised, health-checked component with a pinned
  runtime and a lockfile-reproducible dependency tree. BFF logs and traces correlate with the
  platform's `trace_id` and carry no prompt text, diff content, or credential.
- **Contract.** TypeScript types are **generated from the Go view structs** with a CI drift gate, so a
  read-model change cannot silently reach the browser as `undefined`.
- **Not changed here.** No new platform endpoint, table, queue or statistic. The existing `go:embed`
  pages keep working unchanged; their removal is a **separate, inventory-gated cutover step**, not a
  side effect of the port. **Nothing is deleted in this change.**

## Impact

- **Affected capabilities:** `web-console` (new), `console-bff` (new), `console-design-system` (new).
  Read-model **consumers** of `config-layer`/`runtime` (P2), `metrics-observability` (P2.5),
  `pattern-classifier` (P3.5), `eval-harness`/`scoring` (P4), `entitlements` (P7) — consumed, not
  modified.
- **Affected code/systems:** a new `web/` application (Next.js App Router + TypeScript) and its BFF
  process; `internal/api/server.go` readiness aggregation; the deploy unit gains one supervised
  component. `internal/api/static/*.html` and their `go:embed` handlers (`p2.go`, `monitor.go`,
  `p35.go`, `p4.go`) are **untouched until the cutover step**.
- **Dependencies:** requires **P2** (spec resolve/submit, transform+diff, run record), **P2.5**
  (monitor snapshot + SSE), **P3.5** (classified graph read model), **P4** (eval board read model),
  **P7** (tenant identity + entitlement facts). Wave **9b** additionally requires **P4.5** (diagnosis
  views) and **P5.5** (the proposal API — the review surface is blocked on it).
- **Unblocks:** the Team+ commercial surface has something to show; P6 budget and automation-level
  governance gets a home a non-CLI persona can reach; and removal of the legacy `go:embed` pages
  becomes possible once inventory parity is demonstrated.
- **Breaking:** none in this change. The cutover step that removes the legacy pages **is** breaking
  for anyone bookmarking `/p2`, `/p25/monitor`, `/p35/graph` or `/p4/board`, and is gated on canonical
  routes existing for each (FR11) — it is scheduled explicitly, with an owner, in `tasks.md`.
