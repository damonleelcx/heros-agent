# Acceptance record — P9 §9

> **What this file is.** The evidence behind §9's checkboxes. It exists because "acceptance" written in
> a task list is a claim, and this phase's standing rule is that acceptance for any user-visible
> behavior is **rendered-browser evidence, never a green build** — a rule this console has already
> earned three times over.
>
> **Date:** 2026-07-24. **Against:** `cmd/proof/customerconsole` over a real `github.com/NousResearch/hermes-agent`
> checkout (40 discovered call sites), plus a stub platform for the responses a real deployment cannot
> be made to produce on demand.

---

## The machine-checked half

| Gate | Result |
|---|---|
| `npm run build` — token · string · markup · claim scans, `next build`, then the bundle scan | pass |
| `npm test` | **214 / 214**, five consecutive runs |
| `tests/inventory.test.mjs` — §9.1, one case per feature-inventory item | 111 / 111 |
| `tests/acceptance.test.mjs` — §9.3, §9.5, §9.7, §9.8, §9.9 against rendered HTML | 15 / 15 |
| `tests/craft.test.mjs` — §5b.8 (R16–R20) incl. computed contrast | 20 / 20 |
| Shipped client payload | 828,317 bytes — 571,683 under the stated ceiling |

## The rendered half

### 9.4 · Browser-rendered acceptance

Walked in Chrome against the live stack.

| Property | What was seen |
|---|---|
| The graph renders real data | 40 nodes discovered from the actual repository, `0 edges`, `0 LLM fallback calls`, 40 regions not yet classified. The screen agreed with the API response field for field. |
| 🔴 The hierarchy is now right | `40` / `0` / `0` render at `--text-stat` with subordinate labels. Before this change they were `--text-xs` chips beneath an `--text-lg` heading reading *"This graph"*. |
| The qualifier stays with its figure | *Fully rule-covered* renders beside the LLM-call count, not inside it. |
| Theme is a real choice | System / Dark / Light, and switching **stays on the page being read**. `data-theme` is present in the first byte of HTML — verified with `curl`, not inferred. |
| A tenant route fails closed | `/app/…` without a session redirects to sign-in rather than rendering a shell that then fails every request. |
| The form measure holds | The sign-in credential field is sized to its content. It previously spanned 1440px for a ~40-character token. |

### 9.5 · Degradation

Each failure class was produced deliberately and read off the screen.

| Condition | Rendering |
|---|---|
| **503 not-mounted** | *"This subsystem is not mounted on this deployment"*, carrying the platform's own message, on `state--not-mounted`. Verified live on the board, run, live-monitor, scorecard and account routes — the hermes deployment genuinely does not mount P2/P2.5/P4/P4.5/P7. |
| **404 not-found** | *"a routing fact, not a measurement"*, on `state--not-found` — never as an empty result. |
| **transport** | Platform stopped outright: *"transport failure, not an empty result"*, on `state--transport`. |
| 🔴 **200, unusable body** | A **fourth** state. See the defect below — this one did not exist before today. |
| SSE disabled | The monitor's `sawMessage` condition is asserted in `inventory.test.mjs` (**P25-9**): a stream that never delivered falls back to polling; a stream that worked and then ended does not. |

### 9.6 · Accessibility

Automated structural audit across **ten routes** — `/`, `/signin`, `/app`, `/app/configure`, graph,
board, run, live, scorecard, account:

- exactly **one** `<h1>` per route, naming the subject, on every one;
- **0** unlabelled graphics — the graph's SVG is `aria-hidden` inside a
  `role="img"` whose label is generated from the data (*"40 nodes across 1 layer, joined by 0 edges…"*),
  with a `<details>` tabular fallback;
- **0** `<th>` without `scope`, **0** tables without a caption;
- **0** unnamed controls;
- skip link and `lang="en"` present on all ten.

**Keyboard-only pass** on the graph route: 19 focusable elements, **0** unreachable, skip link first in
order, `<details>` summary reachable.

**Contrast, computed from the live values in both themes** — every text pair ≥ 4.5:1 and every
non-text boundary ≥ 3:1, now enforced by a test rather than asserted in a comment.

---

## 🔴 Three defects the gate found that a green build could not

Recorded because the point of a gate is what it catches, and all three were invisible to the type
checker, the scans, and every test that existed this morning.

### 1. The configurator could disable itself permanently

`validate()` and `submit()` did not wrap their `fetch`, and the panel's `busy` flag is *derived* from
the request outcome. A transport failure — a dropped network, a restarting BFF — left the outcome on
`submitting` forever: **every control on the page disabled, with no message, until a reload the reader
had no reason to try.**

The legacy page re-enabled its button in a `finally`. **P2-7** enumerates exactly this, and the port had
dropped it with nothing to notice.

On the write path the copy now says the outcome is **unknown** rather than failed — a request that never
got an answer did not fail, and calling it a failure would assert that nothing was persisted, which
**P2-9** reserves for the HTTP 400 where the platform actually said so.

### 2. A 200 of the wrong shape destroyed the whole page

Found by the §9.8 "exactly one display-level heading" case. The console trusted any 200 to match its
read model. Given a body that did not — a rolled-back platform, a proxy substituting a response, a
subsystem answering for a different view — a page dereferenced a nested field, threw during server
render, and Next replaced **the entire view** with its error output. No frame, no heading, no subject:
the reader could not even confirm which variant they had opened, which is the precise failure FR27
exists to prevent.

Guarding each dereference was the first attempt and the wrong shape — there are dozens, and a new one
arrives with every read-model change. The fix is at the boundary: `load()` takes the fields a view
cannot render without, and a 200 missing one becomes an **upstream** failure naming the missing fields.
It is deliberately not one of the other three classes: the platform answered (not transport) and
answered 200 (neither not-mounted nor not-found), and rendering it as *empty* would be worst of all —
an empty board and a board the console could not read are different facts about the world.

### 3. A control boundary below the contrast floor, in the shipped dark palette

`--border-strong` on `--surface` measured **1.83 : 1** in the dark theme — the token that draws the
button edge, the command palette's edge, the skip link and the unknown-status chip. WCAG 2.1 SC 1.4.11
requires 3:1 for a control's boundary. Lifted to `#6b7887`, which measures **3.50 : 1**; the light theme
was already at 3.64.

This is the same category as the status foregrounds the palette already documents adjusting: an
accessibility floor is not a fork. Every other reconciled value is untouched, and the ratio is now
computed by a test in both themes rather than claimed in a comment.

---

## Two test-suite defects fixed along the way

Both were introduced by this work and both produced *intermittent* failures, which is the worst kind.

1. **The payload probe corrupted a live server.** It appended to a chunk in `.next/` in place, while
   other test files — which run **concurrently** — were serving those exact chunks. Seven unrelated
   security tests failed depending on timing. The probe now runs against a copied sandbox.
2. **The harness allocated ports from a 90-slot space** (`4400 + hrtime % 90`). Three test files now
   start a console each, and one starts a fourth mid-run. Collisions were regular, and the failure mode
   was worse than a crash: the loser's health check could succeed against the *winner's* console, so a
   test silently asserted against another file's server. Ports are now allocated by the OS, with one
   retry on a bind failure.

Five consecutive full runs are green.
