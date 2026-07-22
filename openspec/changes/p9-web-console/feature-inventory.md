# Feature Inventory — the no-feature-loss checklist (P9)

> **Why this file exists.** A reference design is a demonstration of visual style; it is **never a
> complete functional spec**. Porting against a mockup instead of against the shipped behavior is how
> sorting, tooltips, chart series, keyboard paths and empty-state copy get deleted by omission — and
> nobody notices until a customer does. This file enumerates **what the current pages actually do**,
> so the port is checked against behavior rather than against a picture.
>
> **How it is used.** Every line is a checkbox executed as a test case (PRD FR12 / A5). A behavior may
> be **dropped only by being listed here with a reason** — never by not being mentioned. Anything
> unchecked at cutover blocks removal of the corresponding legacy page.

**Source of truth:** [`internal/api/static/`](../../../internal/api/static/) —
`index.html`, `p2.html`, `p25monitor.html`, `p35graph.html`, `p4board.html` — plus the Go handlers
`internal/api/{p2,monitor,p35,p4}.go` and the read models `internal/evalboard/view.go`,
`internal/patternclassifier` (`GraphView`), `internal/telemetry` (`RunMonitor`).

---

## 0. Cross-cutting behaviors (all pages)

- [ ] **X1.** Three failure classes render **three distinct messages**, everywhere:
      **503 subsystem-not-mounted** (`the P2 store is not mounted` / `the live monitor is not mounted` /
      `the pattern classifier is not mounted` / `p4 board is not mounted on this server`),
      **404 not-found** (`No such run.` / `No such workflow: {id}.` / `No transform for this
      config_hash at this revision.`), and **transport failure** (`Could not reach the API: …` /
      `Cannot reach the server. Retrying…`). A 404 is never rendered as a business state.
- [ ] **X2.** **Loading**, **empty**, and **error** are three distinct renderings, never collapsed.
- [ ] **X3.** Empty-state copy is **status-dependent**, not generic — e.g. a run with no nodes reads
      `No nodes have reported yet — the run is still starting.` while `running`, and `This run
      recorded no node executions.` otherwise.
- [ ] **X4.** Status values are rendered **verbatim from the record**, never re-derived client-side.
- [ ] **X5.** All values interpolated into the DOM are escaped. *(Improvement: `p25monitor.html` has no
      escaping helper and interpolates `node_id` raw — see [`ui-ux-plan.md`](ui-ux-plan.md) R7.)*
- [ ] **X6.** Deep-link entry points continue to work, mapped to canonical routes (see each page).

---

## 1. `/p2` — Variant run / review / inspect (`p2.html`, 544 lines)

**Entry points:** `?run=` (auto-loads the run), `?cfg=` + `?rev=` together (auto-loads the transform).

### Panel 1 — Configure a node
- [ ] **P2-1.** Four override dimensions displayed as chips: **Model / Prompt / Skills / Context**
      (`model_ref`, `prompt_ref`, `skill_refs`, `context_policy`), with the framing that an omitted
      dimension means *no override — call site unchanged*, which is the **default, not a missing
      value**.
      *(Note: per-dimension `hint` strings exist in the source array but are never rendered — see
      ui-ux-plan R12.)*
- [ ] **P2-2.** Variant Spec textarea (`spellcheck=false`, vertically resizable).
- [ ] **P2-3.** Inputs: `variant_id`, optional `label`, `seed` (defaults to `0`), with help copy
      explaining `variant_id` stability and `seed` identity.
- [ ] **P2-4.** **Reset to example** restores a working demo spec (`wf-demo` / `rev1`, two nodes, one
      data edge) and clears the previous result.
- [ ] **P2-5.** **Validate only** — local `JSON.parse` failure reports *the spec is not valid JSON*
      with the parser message; a server rejection shows `node_id` / `dimension` chips and the offending
      `ref`; success shows a `valid` chip, an `N node(s)` chip, and one chip per resolved ref — or the
      fallback *no overrides — every call site unchanged*.
- [ ] **P2-6.** **Submit & run** client-side pre-checks: empty `variant_id` and non-integer/negative
      `seed` are each rejected with their own message before any request.
- [ ] **P2-7.** Submit button is **disabled during the request** and re-enabled in `finally`.
- [ ] **P2-8.** Submit loading copy explains *why* it is slow (resolving refs, generating the
      transform, running a compiler).
- [ ] **P2-9.** **On HTTP 400 only**, the failure adds *Nothing was persisted: no spec, no transform,
      no run.* — deliberately withheld on 500/503, where persistence is unknown. **This conditional is
      load-bearing; do not generalize it.**
- [ ] **P2-10.** `transform_status === "build-rejected"` renders a distinct failure state explaining
      the transform was generated and reviewed but **does not build, so it was never run**, with the
      rejected node/dimension chips and a `config_hash_display` chip.
- [ ] **P2-11.** Success fills the transform panel's `config_hash`/`source_revision` and the run
      panel's `run_id`, then **auto-loads both** and starts watching.

### Panel 2 — Review the generated diff
- [ ] **P2-12.** Loads a transform by `config_hash` + `source_revision`; missing inputs produce their
      own message distinct from "not found".
- [ ] **P2-13.** Header chips: `status`, `verification_strength`, `config_hash_display`,
      `source_revision`, and `variant_branch` when present.
- [ ] **P2-14.** `requires_human_review === true` renders the note that the change was **only parsed,
      not type-checked**, and is never applied automatically at any automation level. **Read from the
      API — never recomputed client-side.**
- [ ] **P2-15.** Diff colorization: `+++`/`---` muted, `@@` hunk header, `+` add, `-` delete.
- [ ] **P2-16.** `diff sha256: {diff_hash}` footer.
- [ ] **P2-17.** An empty/whitespace diff renders *No changes — this is the baseline, applied
      unchanged.* (not an error, not blank).
- [ ] **P2-18.** `build-rejected` transforms show the **build log** as the failure detail.

### Panel 3 — Watch the run & inspect per-node I/O
- [ ] **P2-19.** Head chips: `status`, `config_hash_display`, `seed {n}`, `source_revision`.
- [ ] **P2-20.** `status === "halted"` renders the halt explanation — *this node's output violated its
      typed I/O contract, so it was never passed downstream* — with the node chip and `halted_reason`.
- [ ] **P2-21.** Node table columns: **node · attempt · status · input · output · idempotency key**;
      blob hashes truncated to 12 chars, absent values as `—`.
- [ ] **P2-22.** A node `error` renders on its own full-width row beneath the node.
- [ ] **P2-23.** **Watch** toggles polling at 1 s and re-labels itself **Stop watching**.
- [ ] **P2-24.** Polling **stops on the run record's `status`**, never on a node-derived condition.
- [ ] **P2-25.** A new watch always stops the previous one — a second submit cannot leave two timers
      running.
- [ ] **P2-26.** Only the **first** load shows the loading state; subsequent polls repaint in place
      without flashing.

---

## 2. `/p25/monitor` — Live run monitor (`p25monitor.html`, 181 lines)

**Entry point:** `?run_id=` only — there is no input field.

- [ ] **P25-1.** Header shows `run_id` in monospace, or *no run_id given*.
- [ ] **P25-2.** Status badge with the known set `running / succeeded / failed / halted /
      build-rejected`; an **unknown** value prints the raw string and falls back to the running style.
      *(Improvement: the fallback must be visually distinct rather than impersonating `running` — see
      ui-ux-plan R3.)*
- [ ] **P25-3.** Live line: spinner + *streaming metrics as they arrive…* while open; *● stream closed
      — run is {status}* when terminal.
- [ ] **P25-4.** Node table columns: **Node · State · Latency (ms) · Cost · Prompt tok · Completion
      tok**. Latency 0 dp, cost `$` 5 dp, tokens integer; null/undefined render `—`.
- [ ] **P25-5.** Per-row state chip (`ok` / `failed` / `timed_out`) **plus** a left inset marker on the
      row — state is not carried by chip color alone.
- [ ] **P25-6.** Halt note names the node and the reason.
- [ ] **P25-7.** Empty node list is status-dependent: terminal → *This run produced no node metrics.*;
      otherwise spinner + *Run in progress — waiting for the first node…*.
- [ ] **P25-8.** **SSE first** (`EventSource`), rendering each `data:` payload as it arrives, closing
      on `terminal`.
- [ ] **P25-9.** **Polling fallback** engages on stream error **only if no message ever arrived** —
      preserving the distinction between "stream never worked" and "stream ended". Polls until
      terminal.
- [ ] **P25-10.** Missing `run_id` → *No run_id in the URL. Append ?run_id=…*; server error →
      *Server error ({status}).* then **retry**; transport failure → *Cannot reach the server.
      Retrying…* then **retry**.
      *(Improvement: the console selects a run instead of demanding a URL parameter — R8.)*
- [ ] **P25-11.** `config_hash` is present in the payload and **currently unrendered** — see
      ui-ux-plan R12 (surface-or-drop).

---

## 3. `/p35/graph` — Pattern-classified workflow graph (`p35graph.html`, 327 lines)

**Entry point:** `?workflow_id=`.

- [ ] **P35-1.** Meta line: `workflow_id · ir {ir_version} · taxonomy {taxonomy_version}`.
- [ ] **P35-2.** LLM-call counter: `0 LLM calls — fully rule-covered`, else `N LLM fallback call(s)`
      with correct pluralization.
- [ ] **P35-3.** Deterministic layout: x from `node.layer`, y from `node.order`. **Reproducible — no
      heuristic auto-layout.**
- [ ] **P35-4.** Node box shows truncated `node_id`, truncated `model`, and a third line listing
      node-scoped label titles when present.
- [ ] **P35-5.** **Data vs. control edges** distinguished by **both** dash pattern and arrow marker
      (survives greyscale), not by hue alone.
- [ ] **P35-6.** **Back edges route under the row**, dipping below both endpoints, so a Reflection loop
      is visible instead of hidden behind forward edges. **Deliberate — do not lose.**
- [ ] **P35-7.** Region rectangles drawn **beneath** nodes, computed from member bounding boxes, styled
      by source: rule (solid), llm (dashed), unclassified (grey dashed).
- [ ] **P35-8.** Region caption: `#{ordinal} {title|pattern} {confidence}` + `(candidate)` when
      applicable, joined with the source in brackets; unlabelled regions read *not yet classified*.
- [ ] **P35-9.** Graph container **scrolls, never shrinks** — a large graph is never silently
      compressed.
- [ ] **P35-10.** Legend with five entries (data edge, control edge, rule-labelled, llm-labelled, not
      yet classified). *(Improvement: legend colors are currently hardcoded rather than tokens — R1.)*
- [ ] **P35-11.** Subgraph label cards: per-label chip with ordinal, uppercase source badge, title, a
      **confidence bar** with a numeric readout and a `title` tooltip, and rule/llm styling that differs
      in **border style** as well as color.
- [ ] **P35-12.** `candidate: true` adds a CANDIDATE badge explaining that structure shows the shape
      but confirming it needs runtime traces (P5), and that confidence is capped accordingly.
- [ ] **P35-13.** Dispatch line: `dispatches {primary_metric} + {others}`, or *dispatches: no metric-set
      mapped*.
- [ ] **P35-14.** Unclassified regions explain **why** — no structural signature matched, and either
      *no model was consulted* or *the fallback returned nothing in the taxonomy* — and state
      explicitly that this is **unlabelled, not "no pattern"**.
- [ ] **P35-15.** Whole-workflow empty state repeats that distinction: *That is a state, not an error…
      It does not mean the workflow implements no patterns.*
- [ ] **P35-16.** Diagnostics card, hidden unless non-empty: `[{stage}] {subgraph_ref} {raw_pattern} —
      {reason}`.
- [ ] **P35-17.** **An error hides the graph, the label card AND the diagnostics card** — deliberately,
      because an empty label card under an error would read as a claim about the workflow.
- [ ] **P35-18.** 404 copy distinguishes *no such workflow* from *exists but unclassified*, and the
      transport error says explicitly *this is a transport failure, not an empty classification*.
- [ ] **P35-19.** Unrendered read-model fields: `ViewNode.symbol`, `.policy`, `.tools`, `.region_ids`;
      `ViewLabel.group`, `.provenance`; `Diagnostic.source`. Also `.nodebox.hl` is styled but never
      applied — a hint at intended selection. See ui-ux-plan R12.

---

## 4. `/p4/board` — Eval board (`p4board.html`, 557 lines)

**Entry point:** `?workflow=` — **defaulting to the hardcoded `'wf-demo'`**.
- [ ] **P4-0.** ⛔ **Deliberately dropped.** The hardcoded `'wf-demo'` default is **not ported**: it
      renders a confident board for a workflow that is not the user's. Replaced by selection + an empty
      state (PRD FR10 / ui-ux-plan R8). *This is the only behavior in this document intentionally
      removed.*

### Header & profiles
- [ ] **P4-1.** Header shows `workflow_id · eval_set {hash…}` (truncated), or just the workflow when no
      hash.
- [ ] **P4-2.** **Weight-profile selector** populated from `profiles`, with the active `profile`
      pre-selected, and the help text *re-ranks from cache · 0 new runs* — which communicates that
      switching profiles costs nothing. Changing it re-reads the board with `?profile=`.

### Banners
- [ ] **P4-3.** Error banner with the server `error` in monospace.
- [ ] **P4-4.** **All-tie banner**: *No winner.* — all variants' CIs overlap on this profile, the ranks
      are **ordering, not evidence**, plus how to resolve (more seeds, stronger eval set).
- [ ] **P4-5.** **Partial banner**: `units_completed / units_planned` complete; rows readable; intervals
      below the seed floor marked `provisional`.
- [ ] **P4-6.** Each server `note` renders as its own banner.
- [ ] **P4-7.** **Unmeasured variants** in a collapsed `<details>` with a **Variant / Reason** table —
      excluded variants are *explained*, not silently absent.

### Leaderboard
- [ ] **P4-8.** Columns **# · Variant · Score ± CI · Gate · State**, all with scoped headers.
- [ ] **P4-9.** A tied rank renders **de-emphasized** (muted, non-bold) — a tie does not look like a
      win.
- [ ] **P4-10.** Variant cell shows the label plus the short `config_hash` with the full hash as a
      `title`.
- [ ] **P4-11.** Score cell shows `score ± [ci_low, ci_high]`, a **CI bar** scaled to the table's global
      min/max with a minimum visible width and a mean tick, and `n={n_seeds} seeds · {n_cases} cases`.
- [ ] **P4-12.** The CI bar carries `role="img"` and an `aria-label` naming score and interval.
- [ ] **P4-13.** Gate cell: pass chip, or fail chip **naming the failed gates**.
- [ ] **P4-14.** State cell renders one chip per flag: `tie`, `disqualified`, `weak-labeled`,
      `provisional`, `low-confidence` — each visually distinct.
- [ ] **P4-15.** **Expandable breakdown row** per variant: per-component `metric · role · raw ·
      normalized → contribution`.
- [ ] **P4-16.** A judged component shows `judge κ {agreement} · n={n_human}`, and an **uncalibrated**
      judge is flagged and styled as weak — *wherever its metric appears*.
- [ ] **P4-17.** Breakdown also shows penalties, the full `config_hash` + `method`, the tie line
      (*overlapping confidence intervals are not an ordering*), and `gate_reasons` in the critical color.
- [ ] **P4-18.** **Keyboard navigation:** each row focusable; Enter/Space toggles the breakdown with
      `preventDefault`; ArrowDown/ArrowUp move focus **with wrap-around**.
- [ ] **P4-19.** Row hover and expanded-row highlighting are distinct.
- [ ] **P4-20.** **Virtualization above 60 rows**: scroll container with spacer rows, window recomputed
      on scroll, plus a `{n} variants · virtualized` footer so the user knows why the DOM is partial.
- [ ] **P4-21.** Distinct empty copy: *No variant passed every gate.* vs. *No variants on this board
      yet. Add a variant to compare.*
- [ ] **P4-22.** **Disqualified table** in its own section, titled *excluded from the ranked order, not
      ranked last* — disqualification is not a bad rank.

### Pareto frontier
- [ ] **P4-23.** Quality vs. cost scatter, **marker size = latency**.
- [ ] **P4-24.** Non-dominated points are **diamonds**, dominated are **circles** — the distinction is
      carried by **shape**, not only hue.
- [ ] **P4-25.** Domain padded so extreme markers are never clipped, including the degenerate
      single-point case.
- [ ] **P4-26.** Every point is focusable with `role="img"` and an `aria-label` naming label, quality,
      cost, latency and frontier status.
- [ ] **P4-27.** Tooltip is bound to **both** `mousemove` and `focus` (hidden on `mouseleave`/`blur`) —
      **keyboard-reachable, not hover-only** — and carries `role="status"`.
- [ ] **P4-28.** Axis labels and min/max ticks on both axes.
- [ ] **P4-29.** Legend: diamond = on the frontier, circle = dominated, *marker size = latency*.
- [ ] **P4-30.** **Accessible `<details>` table fallback**: Variant / Quality / Cost / Latency /
      Frontier.
- [ ] **P4-31.** Empty state: *No gate-passing variants to plot.*

### Coverage
- [ ] **P4-32.** Unmeasured coverage says so explicitly, and says what it costs: the board *cannot say
      whether the failing path was ever exercised*.
- [ ] **P4-33.** Below-threshold banner carries `stopped_because`.
- [ ] **P4-34.** Per-dimension meter with `{achieved} of {target} target ({covered} of {total})`, and a
      distinct short/unmet style.
- [ ] **P4-35.** A **vacuous** dimension renders *not measurable — no obligations on this axis* — not
      0%, which would read as failure.
- [ ] **P4-36.** Stat grid: cases, rounds, difficulty (or *not measured*), diversity, oracle coverage
      (with the indecisive-oracle caveat), and gold/weak/none reference counts as distinct chips.
- [ ] **P4-37.** Coverage `reasons[]` listed.
- [ ] **P4-38.** **Residual table** — obligation / dimension / why, with an `unreachable` flag — plus
      the framing sentence: *These stay in the denominator. Dropping them would raise the percentage by
      deleting the evidence of failure.* **Preserve this sentence's meaning.**

### Spend
- [ ] **P4-39.** Table **Kind · USD · Calls**, kinds sorted, with a bold total row.
- [ ] **P4-40.** **Budget-cap banner**: *Measurement stopped rather than overspending* — a stop is
      presented as correct behavior, not failure.
- [ ] **P4-41.** Empty: *No spend recorded.*

### Board-level states
- [ ] **P4-42.** `state === 'error'` **hides all sections** rather than rendering empty scaffolding.
- [ ] **P4-43.** `state === 'empty'` still renders Pareto/coverage/spend in their own empty states.
- [ ] **P4-44.** Unrendered read-model fields: `gate_set`, `progress.seed_floor`, `Row.variant_id`,
      `ComponentView.raw_ci_low`/`raw_ci_high`/`unit`, `judge.percent_agreement`/`floor`,
      `ParetoPoint.composite`, `DimensionView.uncovered[]`, `spend.eval_run_id`, `spend.budget`,
      `coverage.low_confidence`, and `state === 'complete'` having no distinct rendering. See
      ui-ux-plan R12 — each needs a surface-or-drop decision.

---

## 5. `index.html` — orphaned approval queue (**forward-ported, not deleted**)

No Go handler serves this file; its three endpoints (`/api/proposals/pending`,
`/api/proposals/{id}/approve`, `/api/proposals/{id}/reject`) **do not exist** in the Go code; and its
UI is Chinese-only (`lang="zh-CN"`), violating the English-UI rule.

Its **shape** is what P5.5 needs, so the behaviors are recorded here as **requirements for the future
review surface**, not as things to port literally:

- [ ] **IDX-1.** A queue of pending proposals, each showing its **layer**, **title**, **rationale** and
      full **diff**.
- [ ] **IDX-2.** Per-proposal **approve** and **reject** actions.
- [ ] **IDX-3.** A count of pending items, and a distinct *nothing pending* empty state.
- [ ] **IDX-4.** A distinct *cannot reach the API* transport state.
- [ ] **IDX-5.** Refresh after an action.
- [ ] ⛔ **Deliberately dropped — Chinese UI strings.** Rewritten in English (PRD FR23).
- [ ] ⛔ **Deliberately dropped — 15 s unconditional polling that never stops.** Replaced by a bounded
      strategy; an always-on timer on a queue with no terminal state is a cost with no owner.
- [ ] ⛔ **Deliberately dropped — `alert()` for action failures.** Replaced by an in-page error state.
- [ ] 🚧 **Blocked.** This surface **does not ship until the P5.5 proposal API exists** (PRD FR16). A
      surface with no backing endpoint is exactly how the current orphan was created.

---

## Surfaces added after this inventory

This document enumerates what the **five legacy pages** already do, so the port loses nothing. It is
not a list of everything the console will contain. Surfaces introduced by later phases carry their own
requirements and are not inventoried here:

| Surface | Owned by |
|---|---|
| Prompt browser, version timeline, version diff, editor, binding editor, preview / test-run, comparison, per-node model + prompt selector | [`../p10-prompt-model-studio/`](../p10-prompt-model-studio/) — hosted in this shell per §11b of [`tasks.md`](tasks.md) |
| Attribution / diagnosis views | P4.5, via §11.1 |
| Proposal review | P5.5, via §11.2 (blocked on its API) |

## Cutover gate

A legacy page may be removed **only** when every unchecked item above for that page is either checked
or explicitly listed as deliberately dropped with a reason, and its canonical route exists. Removal
takes the HTML file **and** its Go handler together, so no route is left serving a stale asset.

| Page | Canonical route exists | Inventory parity | Handler removed |
|---|:---:|:---:|:---:|
| `p2.html` | ☐ | ☐ | ☐ |
| `p25monitor.html` | ☐ | ☐ | ☐ |
| `p35graph.html` | ☐ | ☐ | ☐ |
| `p4board.html` | ☐ | ☐ | ☐ |
| `index.html` (orphan) | ☐ | n/a — forward-ported to the P5.5 surface | n/a — no handler exists |
