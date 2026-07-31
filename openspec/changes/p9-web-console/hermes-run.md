# Run record — the console against `nousresearch/hermes-agent`

> **What this is.** The whole console, driven in a browser against a real
> [`github.com/NousResearch/hermes-agent`](https://github.com/nousresearch/hermes-agent) checkout — not
> a fixture. A fixture proves the code path and nothing about any real codebase, and this phase's
> standing rule is that acceptance is **rendered-browser evidence, never a green build**.
>
> **Date:** 2026-07-24 · **Stack:** `cmd/proof/customerconsole` on `:4321` over a shallow clone, `web/console` on
> `:4320`.

## What is real here, and what is not mounted — stated, not implied

**Real.** P1 discovery over the checkout, the P3.5 classifier over that IR, the write-back into the IR
document, and the `GraphView` built from the **written** document — so the console renders what a
consumer would actually read out of a stored IR, not an in-memory result that never survived a round
trip.

**Not mounted.** P2 (configure / diff / run), P2.5 (live monitor), P4 (eval board), P4.5 (scorecard),
P5.5 (proposals) and P7 (billing). Each needs a fan-out against a provider, and this command does not
stub one — a board assembled from invented numbers is exactly the demo that overstates.

That is not a gap in the run. It is half of what the run demonstrates.

---

## 1. The screen agrees with the API, field for field

The platform's answer for `nousresearch/hermes-agent`:

```
nodes: 40   edges: 0   llm_calls: 0   regions: 0   unclassified: 40   ir: 1.0.0   taxonomy: 1.0.0
```

Read back out of the rendered DOM:

| On screen | Value | From |
|---|---|---|
| `NODES` | **40** | `nodes.length` |
| `EDGES` | **0** | `edges.length` |
| `LLM FALLBACK CALLS` | **0**, with *Fully rule-covered* beside it | `llm_calls` |
| unclassified region cards | **40** | `unclassified.length` |
| meta line | `ir 1.0.0 · taxonomy 1.0.0` | `ir_version`, `taxonomy_version` |

The graphic's text alternative is generated from the same data, not written by hand:

> *"40 nodes across 1 layer, joined by 0 edges of which 0 are control edges. 0 regions carry pattern
> labels; 40 regions are not yet classified. The values are in the table below this graphic."*

**40 unclassified regions is the honest answer**, and the console says so in as many words rather than
rendering an empty view: *no structural signature matched, and no model was consulted*. A tool that
reported "0 patterns" here would be claiming a measurement it never made.

## 2. 🔴 The hierarchy, measured rather than admired

The defect this change set out to fix, measured in the browser with `getComputedStyle`:

| Element | Before | After |
|---|---|---|
| the stat's **value** (`40`) | `--text-xs` — 12px, inside a grey chip | **36px** |
| the section heading (*"This graph"*) | `--text-lg` — 18px | 18px, unchanged |
| the stat's **label** (`NODES`) | — | 12px |

**36 > 18 > 12.** The number the reader came for is now the largest thing on the view, and the words
that introduce it are subordinate. Before, the least informative text on the page was among the largest
and the most informative was the smallest.

The graph view also lost roughly a screenful of pure frame: the page frame was being applied **twice**
(the shell and the `PageFrame` both carried `.page`), and each section's head carried full card padding
on both edges.

## 3. The failure taxonomy survives two real hops

Five subsystems are genuinely unmounted on this deployment, so this is not simulated:

| Route | Rendering | The platform's own message, carried through |
|---|---|---|
| board | `state--not-mounted` | *p4 board is not mounted on this server* |
| run | `state--not-mounted` | *the P2 store is not mounted* |
| scorecard | `state--not-mounted` | *the p4.5 scorecard is not mounted on this server* |
| account | `state--not-mounted` | *the p7 billing surface is not mounted on this server* |
| proposals | `state--not-mounted` | *the p5.5 surface is not mounted on this server* |

Each keeps its frame and its `<h1>` naming the subject, so a reader can still see **what** they opened
while being told why it has no data. None was rendered as empty, as a 404, or as a transport failure.

## 4. Theme, end to end

`data-theme` is present in the **first byte** of HTML (verified with `curl`, not inferred from the
DOM). Switching System → Light re-rendered the graph view in the light palette **and stayed on the page
being read**.

That last clause is not incidental — it is the fix for a defect this run found. See below.

## 5. Accessibility, across ten routes

Exactly one `<h1>` per route naming its subject; **0** unlabelled graphics (the SVG is `aria-hidden`
inside a `role="img"` whose label is generated from the data, with a `<details>` tabular fallback);
**0** unscoped `<th>`; **0** uncaptioned tables; **0** unnamed controls; skip link and `lang="en"`
everywhere. A keyboard-only pass on the graph route reached **19 of 19** focusable elements with the
skip link first.

Contrast was **computed from the live values in both themes** — every text pair ≥ 4.5:1, every non-text
boundary ≥ 3:1.

---

## 🔴 What rendering found that the build could not

Four defects, none of which any type check, scan or unit test could see:

1. **The theme control silently returned the reader to `/`.** It redirected to `Referer`, and this
   console sends `Referrer-Policy: no-referrer`. The endpoint answered a perfect `303` with a correct
   `Set-Cookie` every time. Fixed with an `x-pathname` header set in middleware.
2. **A control boundary at 1.83 : 1** in the shipped dark palette — the token drawing the button edge,
   the palette edge and the skip link, against a 3:1 floor. Now 3.50 : 1, and computed by a test.
3. **The configurator could disable itself permanently.** `busy` is derived from the request outcome
   and neither `fetch` was wrapped, so a transport failure left every control disabled with no message.
   That is the `finally` the inventory's **P2-7** exists to protect, lost in the port.
4. **A 200 of the wrong shape destroyed the whole page** — a nested dereference threw during server
   render and Next replaced the entire view, frame and heading included. Now a fourth failure state at
   the `load()` boundary, naming the missing fields.

Each is recorded in full in [`acceptance-record.md`](acceptance-record.md).
