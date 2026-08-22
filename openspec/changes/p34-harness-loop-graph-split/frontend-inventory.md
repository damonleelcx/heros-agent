# P34 §7.2 — the re-cut inventory

> **This file is a gate, not a checklist.** PRD §9.4 names the risk: *"the standing failure mode of a UI
> revision is that something on the old page has no destination on any new one."* So every item on
> `/app/harness` and `/app/wiring` is listed here with a **named destination**, and the confirmed rule
> (D-34.4) is *carry everything; ask before removing anything*.
>
> **Nothing in this inventory is removed.** The Removals section is empty, and it is empty because
> nothing needed removing — not because removals were skipped.

## The shape

| before | after | why |
|---|---|---|
| `/app/harness` — scaffold: strategies, ceilings, cost | `/app/harness` — the **execution envelope** | the axis narrowed (FR5); the page narrows with it |
| — | `/app/loop` — the **iteration policy** | new axis, `DimLoop` |
| `/app/wiring` — the shape of the workflow | `/app/graph` — the shape of the workflow | renamed and widened: it now carries the `wiring` axis **and** the new `graph` axis |

🔴 **`/app/wiring` redirects; it does not 404.** A bookmark that stops working is indistinguishable from
a feature that was withdrawn, and the reader who most needs this page is the one who saved a link to it
while trying to understand a refusal.

🔴 **`/app/graph` carries TWO backend axes, and says so.** `transform.AxisCoverage()` still reports
`wiring` (P15: reorder / merge / prune / parallelize over `Order`) as a separate axis from `graph`
(P34: concurrency / conditional routing / fan-in merge). They are different capabilities with different
coverage, and collapsing them into one badge would claim the console can apply a concurrency change
because it can apply a transposition. One page, two clearly-labelled axes.

---

## `/app/harness` — every item, and where it goes

| # | item today | destination | note |
|---|---|---|---|
| H1 | Tab **“Set a scaffold”** → `<HarnessAuthoring/>` (strategy picker, params form, cost warning) | **`/app/loop`**, tab “Set a control loop” | the picker offers the five strategies, which are the LOOP vocabulary now. The component moves **unchanged** — it already reads `HarnessStrategyOptions()`, which was re-pointed at `BuiltinLoopStrategies()` in §3. |
| H2 | Tab **“What applies where”** → `COVERAGE` table (per-strategy × language) | **`/app/loop`**, tab “What applies where” | derived from `CoverageFor("loop")` since §2.7 |
| H3 | `BOUNDARY_ROWS` — the Context / Memory / Harness comparison table | **`/app/loop`**, same tab, **with a fourth row added** | the table exists because readers conflate these axes. P34 created a fourth thing to conflate, so the table grows rather than moving: Context (within one call) · Memory (across invocations) · **Loop** (how many calls) · **Harness** (inside what walls). |
| H4 | Section **“Why every strategy has a ceiling”** (`MAX_TURNS_CEILING`, blast radius, observability) | **split across both** | the *ceiling* half is the envelope's (`/app/harness`); the *what a loop costs* half is the loop's (`/app/loop`). Neither half is dropped — this is the one item whose destination is two places, because the split it describes is the phase. |
| H5 | Section **“Why a refusal is the safe half”** (materialized / refused / equivalent, no “partially applied”) | **`/app/loop`** | it is about materializing a LOOP at a call site |
| H6 | Tab **“Proposals”** — the catalog proposes a scaffold swap; the cost/quality admissibility gate | **`/app/loop`** | the operator moved to the loop axis in §6.1 |
| H7 | Section **“What you can do with an unapplied change”** | **`/app/loop`** | unchanged in meaning |
| H8 | Tab **“Your nodes”** → `<AxisProjectionPanel axis="harness"/>` | **both pages get one** — `axis="harness"` on `/app/harness`, `axis="loop"` on `/app/loop` | the projection is per-axis by construction; two axes, two panels |
| H9 | Page lede — “how many calls this node makes, and what makes it stop” | **`/app/loop`** verbatim | it describes the loop |

**New on `/app/harness`** (nothing here was carried from anywhere; it is the axis's new content): the
envelope's required fields and what each one bounds, the ceiling/value split, the two places the
concurrency limit is enforced, and the resolve-time refusals.

---

## `/app/wiring` — every item, and where it goes

| # | item today | destination | note |
|---|---|---|---|
| W1 | Tab **“The axis”** → `OPERATORS` (Merge / Reorder / Parallelize / Prune) | **`/app/graph`**, tab “The axis”, under a **“Wiring”** heading | kept verbatim. 🔴 Note the collision this inventory exists to catch: `Parallelize` here means *drop a sequencing edge*, and P34's `concurrent group` means *declare overlap*. Both stay, on one page, with the difference stated. |
| W2 | Tab **“Rearrange the graph”** → `<WiringEditor/>` | **`/app/graph`**, unchanged | |
| W3 | `<WiringBoundaries/>` | **`/app/graph`**, unchanged | |
| W4 | Tab **“An applied reorder”** → `APPLIED_DIFF`, the engine's real diff | **`/app/graph`**, unchanged | leads the outcomes for its original reason: four refusals in a row teaches the wrong thing about an axis |
| W5 | Four `EXAMPLES` refusal tabs (reorder / merge / edge / another-axis) | **`/app/graph`**, unchanged | verbatim engine sentences |
| W6 | Section **“What is delivered, and what is not”** | **`/app/graph`**, unchanged | |
| W7 | Tab **“Your nodes”** → `<AxisProjectionPanel axis="wiring"/>` | **`/app/graph`**, plus a second panel for `axis="graph"` | two axes, two denominators |
| W8 | Page lede + the “worked examples, not your data” hint | **`/app/graph`**, extended to name both axes | |

**New on `/app/graph`**: the three topology forms, the merge declaration and its two required fields,
the predicate's `expr` rule, and the per-language coverage — which is `ABSENT` today and says which of
the frontend, the analysis or the language support is missing (FR18).

---

## Removals

**None.** Every item above has a named destination. If a later pass wants to remove one, D-34.4's rule
stands: ask first.

---

## What §7.3 adds that neither page had

An axis unavailable in this build renders **read-only with its reason**. `/app/graph` is the first page
where that is the ordinary case rather than the exception — `graph` is `ABSENT` in every language today
— so the page leads with the axis's own declared status rather than presenting a picker that cannot
produce a diff. HEROS's own axis editor argues why: *"a hidden axis is indistinguishable from one that
does not exist."*
