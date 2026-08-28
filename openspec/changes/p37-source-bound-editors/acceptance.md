# Acceptance — P37 §6.11 (A1)

> **A green build is not acceptance.** This file records the browser session task 6.11 requires: a person,
> on a connected repository, changing their own node's memory strategy and reading the result.

**Run:** 2026-08-27 · Chrome, 1280×720 · console `npm run dev:browser` on `:4320` ·
platform `node web/console/scripts/dev-p37.mjs` on `:4399` (the same connected fixture
`tests/support/connected.mjs` the automated acceptance uses, so the session and the suite cannot
disagree about what a connected platform looks like).

---

## What was done, in order, and what the screen said

| # | Action | What rendered | Which requirement |
|---|---|---|---|
| 1 | Signed in, declined optional cookies | console shell | — |
| 2 | Opened `/app/memory` with **no** node chosen | *"Which node should these surfaces be about? This workflow reports 2 nodes and none has been chosen. Pick one and every axis surface stays on it — you are asked once, not on each page."* with a labelled `<select>` and a Switch button | **FR2** — asked once, in the shell |
| 3 | Chose `handleAnswer · agent/answer.py` | subject strip: `editing · handleAnswer · agent/answer.py`, with the switcher still present | **FR1**, **FR2** |
| 4 | Read the surface | boundary banner **above** the picker: *"Where a memory change reaches your source, and where it does not"*, naming the two preconditions and saying the controls stay usable | **FR15**, and rule 2 (live, not disabled) |
| 5 | Read the current value | `not measured` · *"Missing: `not_visible_in_static_ir` — a memory strategy is a store read and written BETWEEN turns, and the reported structure describes one call site at a time"* | **FR14** — absence drawn, input named |
| 6 | Read the picker | two strategies from **the platform's own vocabulary**, headed `memory strategy vocabulary · registry builtins` | **FR5**, set version stated |
| 7 | Selected `scratchpad` | *Parameters for Scratchpad* appeared with `max_entries`, a `required` chip, and the **schema's own description** as both hint and placeholder | **FR5** — params derived from `ParamsSchema`, not hand-written |
| 8 | Left `max_entries` empty | **Save disabled**; **Check** still enabled | validation at save |
| 9 | Entered `20`, pressed **Check this change** | the platform's verdict, rendered as received: *"This change can be applied"* · `7c2f91ab04de` · `memory` · *"…the change is recorded as **unverified** until a multi-seed evaluation runs."* | preflight, NFR7.3 |
| 10 | Pressed **Save** | *What your change produced* — `CONFIG_HASH 7c2f91ab04de`, `STATE unverified` | **FR16** |
| 11 | Navigated to `/app/context` | **the same subject**, unasked: `editing · handleAnswer · agent/answer.py` | **FR2** — the subject persists |
| 12 | Read `/app/context`'s current value | `observed` · `sliding-window` · `python` — the node's own policy, read from the IR | **FR1** |
| 13 | Read `/app/context`'s picker | `full`, `sliding-window` selectable; **`summarization` rendered DISABLED** with `needs a rewriter this policy will never have at a call site` | **FR7** — shown, not hidden |
| 14 | Navigated to `/app/harness` | the boundary leads: *"Written into your source: nothing, in any language, permanently. **That is not the same as unenforced.**"*, then `not measured` with its named input | **FR15**, **FR14**, P34 §7.3 |
| — | Console errors, throughout | **none** | — |

---

## 🔴 What this session proves, and what it does not

**It proves** that the console binds to the reader's node, names it, asks once, renders the node's own
value or a named absence, offers the platform's own vocabulary at its recorded version, derives the
params form from the schema, disables an unavailable option **with its reason**, sends a real preflight,
and renders the platform's `config_hash` and `unverified` stamp as received.

**It does not prove** that the platform's answers are right — the fixture is a stub. That claim belongs
to `internal/api`'s own tests and, for the write specifically, to
`internal/api/p37_save_proof_test.go`, which drives the **real** `authoring.Submitter` and then reads the
row back: HTTP → the record row → the variant the draft derives to → the hash the surface renders. **A 200
is not evidence of a write**, and neither is a screenshot.

Neither half is the acceptance on its own. Both ran.

---

## Two defects this session found that nothing else did

**1. `paramsFromSchema` was called from the server while living in a `"use client"` module.**
`/app/memory` answered **500 for every reader**. `tsc` was green, `next build` was green, and every
source-reading test passed. Next.js fails that at REQUEST time:

```text
Attempted to call paramsFromSchema() from the server but paramsFromSchema is on the client.
```

Fixed by moving the pure half to `src/lib/axisKit.ts`, which carries no directive. 🔴 The first fix —
re-exporting it *through* the client module — did not work and looked like it had: a re-export does not
launder a directive. `tests/memory.test.mjs` now pins the location, and
`tests/p37-acceptance.test.mjs` is the run that catches it if the pin is removed.

**2. The whole of `/app/delivery` and `/app/authoring` was gated behind a resolved subject.**
Found by P12's and P13's own acceptance runs going red. A reader with pull requests and no reported IR
structure still *has* pull requests, and a change they authored still exists — FR4 governs the position a
**fixture** may not occupy, not the rest of the page. The axis half is inside `AxisFrame`; the reader's
own deliveries and authored changes are not.

Both are recorded here rather than only in a commit message, because "the build was green and the page
was broken" is the exact failure this file exists to make impossible to repeat.

## Reproducing it

```bash
cd web/console && node scripts/dev-p37.mjs &
cd web/console && npm run dev:browser
# then: http://localhost:4320/signin → credential `local-dev-assertion` → /app/memory
```

The automated half, which needs no browser:

```bash
cd web/console && npm test -- tests/p37-acceptance.test.mjs
```
