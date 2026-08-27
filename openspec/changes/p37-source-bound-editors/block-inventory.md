# Block Inventory — P37 §1 (Product Designer)

> **Status: agreed.** This is the input every later section is checked against. §4's pull request
> enumerates each moved block against this table, and a block that appears in a diff without a row here
> fails review (FR12, `working-surface-composition` — *Every moved block is accounted for*).

Covers tasks **1.1** (classify every block), **1.2** (name a destination for every reader-invariant
block), **1.3** (enumerate the protected text), **1.4** (the `not_connected` copy), **1.5** (the prose
budget numbers), **1.6** (the `/app/studio` scope decision).

---

## 0. The rule, and how it was applied

> **If a sentence is the same for every reader it is documentation. If it changes with the reader's
> data it is the product.** (PRD §1, design D2)

Applied per **block**, not per page. A block is one JSX text run or one display string literal — the
same unit `scripts/lib/prose.mjs` counts, so the classification below and the fence in §6 measure the
same thing rather than two things that happen to agree today.

Three columns, and only three verdicts are legal:

| Verdict | Meaning | What happens to it |
|---|---|---|
| `invariant` | identical on every tenant's screen | moves to a **named** reading-surface section |
| `varies` | produced from the reader's own data | **stays** on the working surface |
| `protected` | varies with the reader's data **and** is load-bearing (§6.4) | stays, verbatim, and §6's fences drive it |

`protected` is a subset of `varies`, listed separately because it is the set a layout rewrite deletes by
accident. It is enumerated again in §3 of this document, which is the input to QA fence 6.3.

### The one distinction that decides most of the table

A **worked example of a refusal is not a refusal.** The four decline cards on `/app/context` carry the
engine's real sentences, but they are the engine's sentences about *the platform's fixture node*, not
about the reader's node. They are `invariant` — identical on every screen — so they move, labelled as
the platform's fixture. What stays is the reader's **own** refusal, rendered verbatim when the platform
produces one for their subject.

Getting this backwards in either direction is the failure this phase is most likely to produce:

- treating the fixtures as protected → nothing moves, the phase delivers a shorter page and nothing else;
- treating the reader's refusal as a fixture → FR13 is violated and the product silently stops having a
  capability it still has.

---

## 1.1 / 1.2 — Every static block, classified, with its destination

Word counts are from `scripts/lib/prose.mjs` at `135ee62`. `→` names the destination section; a block
with no destination is **not cut**.

### `/app/context` — 2,762 words (`page.tsx`, `authoring.tsx`)

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| C1 | `PageFrame` lede — "What each node is given to read…" (51w) | `invariant` | rewritten to ≤60w as the route's single lede; the removed half → `concepts/context-policies` §What a context policy decides |
| C2 | hint — "The outcome panels are worked examples…" (36w) | `invariant` | **deleted with the fixtures it labels**; the label moves with them → `concepts/context-policies` §Worked examples |
| C3 | §"What a context policy decides" ¶1 (57w) | `invariant` | → `concepts/context-policies` §What a context policy decides |
| C4 | §"What a context policy decides" ¶2 — "a policy is not an argument you can swap" (62w) | `invariant` | → `concepts/context-policies` §Why applying one is a code rewrite |
| C5 | §"Two different reasons a policy is declined" — `not in source` card (54w) | `invariant` | → `concepts/context-policies` §Two different reasons a policy is declined |
| C6 | same §, `not yet — ours` card (38w) | `invariant` | → same section |
| C7 | same §, footnote on unpacked arguments (35w) | `invariant` | → same section |
| C8 | §"What this platform writes into your source" ¶ (49w) | `invariant` | → `concepts/context-policies` §What reaches your source |
| C9 | `COVERAGE` table — 8 policies × mode × description (**369w**) | `invariant` | → `concepts/context-policies` §What each policy does at a call site. **FR17**: the working surface replaces it with a **live per-node read**, so this is a move *and* a replacement, not a deletion. |
| C10 | hint — "Partial coverage is stated rather than smoothed over" (23w) | `invariant` | → same section |
| C11 | §"Why a decline is the safe half" banner (109w) | `invariant` | → `concepts/refusals` §A refused change is never a dropped one |
| C12 | `AppliedTab` §"What was submitted" (63w) + `dl` | `invariant` | → `concepts/context-policies` §Worked examples › an applied window |
| C13 | `APPLIED_DIFF` fixture + `AxisApplied` `what`/`invariant` prose (79w) | `invariant` | → same section, labelled **the platform's fixture** |
| C14 | §"Why the change is in the diff and not behind a handle" (54w) | `invariant` | → `concepts/context-policies` §Why context is applied inline |
| C15 | `DECLINES[0..3]` `submitted` prose (4 × ~60w = 244w) | `invariant` | → `concepts/context-policies` §Worked examples › four declines |
| C16 | `DECLINES[0..3]` `message` — engine's verbatim cause (4 × ~80w = 331w) | `invariant` *(fixture)* | → same section, **verbatim and labelled the platform's fixture**. Not paraphrased on the way. |
| C17 | `DropTab` — 4 sections + `TOLERANCE_DEFAULTS` table (**419w**) | `invariant` | → `concepts/context-policies` §What it costs to lose context |
| C18 | `RetrievalTab` — 4 sections (**383w**) | `invariant` | → `concepts/context-policies` §Retrieval tuning |
| C19 | `ContextAuthoring` §"Choose a context policy" ¶ (39w) | `invariant` | → `concepts/context-policies` §Choosing a policy; the **picker** stays and binds to the live vocabulary |
| C20 | `ContextAuthoring` `POLICIES` chip list | `varies`→ | **replaced**: the picker binds to the axis vocabulary at its recorded set version (FR5), not to a literal array |
| C21 | `ContextAuthoring` four `PreflightResult` fixtures + their prose (**302w**) | `invariant` *(fixture)* | → `concepts/context-policies` §Worked examples › the three verdicts. The **live** preflight for the reader's subject replaces them in place. |
| C22 | §"Retrieval tuning is gated by the classifier" banner (86w) | `invariant` | → `concepts/context-policies` §Retrieval tuning |
| C23 | §"What a smaller context claims" banner (78w) | `invariant` | → `concepts/context-policies` §A drop ratio is not a saving |
| C24 | `AxisProjectionPanel` copy — counts, denominators, per-node rows | `varies` | **stays** |
| C25 | `not-reported` / `read-failed` / `not-mounted` copy in the panel | `protected` | **stays** — see §1.3 |

### `/app/graph` (the `wiring` axis) — 2,378 words (`page.tsx`, `editor.tsx`, `boundaries.tsx`)

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| G1 | `PageFrame` lede | `invariant` | ≤60w lede; remainder → `concepts/graph-and-wiring` §The shape of a workflow |
| G2 | §"What the wiring axis proposes" (4 operations, ~120w) | `invariant` | → `concepts/graph-and-wiring` §The four wiring operations |
| G3 | §"What is delivered, and what is not" banner (~95w) | `invariant` | → `concepts/graph-and-wiring` §What reaches your source |
| G4 | §"What the graph axis declares" + `FORMS` table (~210w) | `invariant` | → `concepts/graph-and-wiring` §The three topology forms |
| G5 | §"Two words that mean two things on this page" banner (~90w) | `invariant` | → `concepts/graph-and-wiring` §Wiring and topology are not synonyms |
| G6 | §"Why a fan-in has to declare its merge" banner (~105w) | `invariant` | → `concepts/graph-and-wiring` §Why a fan-in declares its merge |
| G7 | worked reorder / merge / rewire / declined tabs — `submitted` + engine `message` (~460w) | `invariant` *(fixture)* | → `concepts/graph-and-wiring` §Worked examples — verbatim, and labelled as the platform's fixture |
| G8 | `editor.tsx` `GESTURES` (5 × label + prose, ~230w) | `invariant` | → `concepts/graph-and-wiring` §The five gestures for the PROSE. 🔴 **The editor itself stays**, in a tab labelled *"the platform's fixture"* — a rearrangement is a change BETWEEN nodes, so it has no node-bound form to convert to, and a markdown document cannot hold an interactive control. FR4 forbids a fixture in the position the reader's own data occupies; the reader's own node is the first tab, so it is not in that position. |
| G9 | `editor.tsx` §"Every gesture gets its verdict as you make it" ¶ | `invariant` | → same section |
| G10 | `editor.tsx` §"A refused rearrangement is not a variant" banner (~80w) | `invariant` | → `concepts/refusals` §A refusal is not a queue |
| G11 | `boundaries.tsx` §"Before you move anything" | `protected` | **stays** — this is the stated boundary, above the control (FR15) |
| G12 | live gesture verdicts, adapter insertion preview, node/edge names | `varies` | **stays** |

### `/app/memory` — 1,764 words (`page.tsx`, `authoring.tsx`, `strategies.ts`)

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| M1 | `PageFrame` lede | `invariant` | ≤60w lede; remainder → `concepts/memory-strategies` |
| M2 | §"Memory is not context" + table (~150w) | `invariant` | → `concepts/memory-strategies` §Memory is not context |
| M3 | §"What this platform writes into your source" + coverage table (~230w) | `invariant` | → `concepts/memory-strategies` §What reaches your source |
| M4 | §"Why a refusal is the safe half" banner (~105w) | `invariant` | → `concepts/refusals` §A refused change is never a dropped one |
| M5 | §"The platform can propose a memory change too" + banner (~140w) | `invariant` | → `concepts/memory-strategies` §Proposals on this axis |
| M6 | §"What you can do with an unapplied change" (~95w) | `invariant` | → `concepts/authored-changes` §What an unapplied change is worth |
| M7 | `STRATEGIES[].tradeoff` — 5 × ~35w = 175w | `invariant` | → `concepts/memory-strategies` §The five strategies. The **picker keeps the title and the wire name**; the tradeoff paragraph becomes a link. |
| M8 | `BOUNDARY` banner in `authoring.tsx` (~150w) | `protected` | **stays, above the picker** (FR15). Shortened only by moving its *general* half; the named missing artifact and the preconditions stay verbatim. |
| M9 | `const NODE_ID = "recall"` and every sentence built on it | `invariant` *(fixture)* | **removed from the reader's data position** (FR4). The demonstration node moves to `concepts/memory-strategies` §Worked example, labelled. |
| M10 | `hashFor()` pseudo-hash + its caveat prose | — | **deleted, not moved**: FR-NFR7.3 — the browser derives nothing. The real `config_hash` comes from the server preflight. Its caveat paragraph has no destination because the thing it warned about stops existing. |
| M11 | §"Back out" prose (~60w) | `invariant` | → `concepts/authored-changes` §Every change has an exact undo. The **button stays**. |
| M12 | params-form schema prose (~55w) | `invariant` | → `concepts/authored-changes` §Validation happens at save |

### `/app/harness` — 1,172 words (`page.tsx`, `envelope.ts`)

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| H1 | `PageFrame` lede | `invariant` | ≤60w lede; remainder → `concepts/execution-envelope` |
| H2 | hint — "This page described a node's control loop until the axis was split…" (~55w) | `invariant` | → `concepts/execution-envelope` §Why the loop lives elsewhere |
| H3 | §"What an envelope declares" ¶ + `ENVELOPE_FIELDS` table (**~430w**) | `invariant` | → `concepts/execution-envelope` §The nine fields |
| H4 | §"The ceiling is imposed; the value is chosen" + `SPLIT_ROWS` + banner (~230w) | `invariant` | → `concepts/execution-envelope` §Imposed versus chosen |
| H5 | §"What this platform writes into your source" banner (~150w) | `protected` | **stays** — this is the stated boundary for an axis that reaches source **nowhere, permanently**. §8.1 reviews it as a customer-facing commitment. |
| H6 | §"Why the concurrency limit is checked twice" banner (~150w) | `invariant` | → `concepts/execution-envelope` §Why the concurrency limit is checked twice |
| H7 | §"When the loop needs something the envelope does not grant" banner (~150w) | `invariant` | → `concepts/execution-envelope` §When a loop needs a second actor |
| H8 | per-node envelope values, refusals | `varies` | **stays** (new — this surface has none today) |

### `/app/authoring` — 648 words

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| A1 | `PageFrame` lede (49w) | `invariant` | ≤60w lede |
| A2 | §"One spine, two origins" (~85w) | `invariant` | → `concepts/authored-changes` §One spine, two origins |
| A3 | §"You author the change. You do not author the evidence." (~90w) | `invariant` | → `concepts/authored-changes` §You do not author the evidence |
| A4 | §"Applied is not verified" (~95w) | `invariant` | → `concepts/authored-changes` §Applied is not verified |
| A5 | §"Every change has an exact undo" (~55w) | `invariant` | → `concepts/authored-changes` §Every change has an exact undo |
| A6 | §"There is no override" banner (~60w) | `invariant` | → `concepts/refusals` §A refusal is not a permission problem |
| A7 | `ADMISSIBLE` / `REFUSED` / `NOT_YET` fixtures + §Verdicts prose (~150w) | `invariant` *(fixture)* | → `concepts/authored-changes` §Worked examples › the three verdicts. The **live** preflight replaces them. |
| A8 | `AuthoredChangeSummary` fixture rows (`ac_4f19c2ab…`) | `invariant` *(fixture)* | **removed from the reader's data position**; the reader's own authored changes render here |
| A9 | `ApplyModeNote`, `ProviderBoundary` prose | `invariant` | → `concepts/prompt-and-model-studio` §Bound and inline, and §Why the model list is short |

### `/app/delivery` — 790 words

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| D1 | `PageFrame` lede (40w) | `invariant` | ≤60w lede |
| D2 | §"What each route carries" + legend prose (~180w) | `invariant` | → `concepts/delivery-routes` §The two routes |
| D3 | §"Delivery states" legend prose (~120w) | `invariant` | → `concepts/delivery-routes` §Delivery states |
| D4 | "A rollout is evidence, not delivery" banner (~70w) | `invariant` | → `concepts/delivery-routes` §A rollout is not a delivery |
| D5 | `DeliverySourceReality` explanation prose (~90w) | `invariant` | → `concepts/delivery-routes` §Where the source actually comes from |
| D6 | route ledger rows, `undeliverable` count and its denominator | `varies` | **stays** |
| D7 | "The route ledger is unavailable" banner | `protected` | **stays** — a read failure is not an empty ledger |

### `/app/studio` — 2,890 words (`page.tsx`, `studio.tsx`, `matrix.tsx`, `authoring.tsx`, `boundmode.tsx`)

**In full scope** — see §1.6.

| # | Block | Verdict | Destination / disposition |
|---|---|---|---|
| S1 | `PageFrame` lede (46w) | `invariant` | ≤60w lede |
| S2 | matrix explanation prose, "nothing is ranked" (~180w) | `invariant` | → `concepts/prompt-and-model-studio` §Nothing here is ranked |
| S3 | `boundmode.tsx` bound/inline explanation (~260w) | `invariant` | → `concepts/prompt-and-model-studio` §Bound and inline |
| S4 | prompt-library / diff / impact explanation (~420w) | `invariant` | → `concepts/prompt-and-model-studio` §The prompt library |
| S5 | `authoring.tsx` §"Set a model yourself" prose, §"The models offered", §"Provider parameters", §"What this tab is not" (~330w) | `invariant` | → `concepts/prompt-and-model-studio` §Authoring a model |
| S6 | matrix cells, node columns, bound state, cost | `varies` | **stays** |
| S7 | `unverified` stamp on a bound cell | `protected` | **stays** (FR16) |
| S8 | the matrix's own workflow picker | — | **replaced** by the shell-resolved subject (FR2). Not deleted — the picker becomes the shell's. Stronger than P29 §4.7 asked for: that fence required the picker to render enumeration ROWS rather than ids (rendering a row object as a React child throws #31 at hydration); after P37 the matrix holds no workflow list at all, so there is nothing left to get wrong. |

### Totals

Measured **after** the rewrite, with the corrected ruler (see §1.5):

| Surface | Before | After | Ceiling | Headroom |
|---|---:|---:|---:|---:|
| `/app/context` | 2,762 | **211** | 250 | 39 |
| `/app/memory` | 1,764 | **218** | 250 | 32 |
| `/app/authoring` | 648 | **203** | 250 | 47 |
| `/app/harness` | 1,172 | **160** | 200 | 40 |
| `/app/delivery` | 790 | **366** | 400 | 34 |
| `/app/studio` | 2,890 | **490** | 500 | 10 |
| `/app/graph` | 2,378 | **517** | 550 | 33 |
| **All seven** | **12,404** | **2,165** | | |

The "before" column is the original measurement; the "after" column and the ceilings are the corrected
ruler's. The two are not directly comparable as ratios — the corrected ruler counts less of everything —
which is why the ceilings were re-derived rather than scaled.

---

## 1.3 — The protected text, per surface

This is the input to **QA fence 6.3**. Every row is text that varies with the reader's data and that a
layout rewrite deletes by accident, because on the page it looks like decoration.

| Surface | Protected text | Which §6.4 rule | Fence |
|---|---|---|---|
| `context` | the reader's own refusal cause, rendered **verbatim** from the engine | FR13 | 6.3 |
| `context` | `not_measured` + the named missing input (`context_drop_ratio` for policy *X* on node *Y*) | FR14 | 6.4 |
| `context` | drop tolerance stated **above** the policy picker | FR15 | 6.5 |
| `context` | `unverified` stamp on a saved policy | FR16 | 6.6 |
| `memory` | the reader's own refusal cause, verbatim | FR13 | 6.3 |
| `memory` | `BOUNDARY` — the named missing artifact + the two preconditions, **above** the picker | FR15 | 6.5 |
| `memory` | `not_measured` when the node's current strategy cannot be resolved, naming the missing input | FR14 | 6.4 |
| `memory` | `unverified` stamp | FR16 | 6.6 |
| `harness` | "Nothing — in any language, permanently. That is not the same as unenforced." | FR15 | 6.5 |
| `harness` | the reader's own resolve-time refusal (turn ceiling, host service), verbatim | FR13 | 6.3 |
| `harness` | `not_measured` for a node with no reported envelope | FR14 | 6.4 |
| `graph` | `boundaries.tsx` — "Before you move anything", above the editor | FR15 | 6.5 |
| `graph` | the reader's own incoherence refusal naming consumer, producer and field | FR13 | 6.3 |
| `graph` | `not_measured` for an unreported node | FR14 | 6.4 |
| `authoring` | all three preflight verdicts, `not_yet_measurable` included, in their own visual state | FR13/FR14 | 6.3, 6.4 |
| `authoring` | `unverified` on every authored change | FR16 | 6.6 |
| `delivery` | "The route ledger is unavailable" — a read failure, never an empty ledger | FR14 | 6.4 |
| `delivery` | `undeliverable` **with its denominator** | FR14 | 6.8 |
| `studio` | `unverified` on a bound cell; "selected, not proven best" | FR16 | 6.6 |
| `studio` | a model unavailable in this deployment rendered **disabled with the service it needs** | FR7 | 6.9 |
| **all seven** | `not_connected` naming the missing input | FR4 | 6.2 |
| **all seven** | the four-valued axis state, per node, never averaged | FR8 | 6.8 |

---

## 1.4 — The `not_connected` copy

Approved wording. Implemented **once**, in `src/lib/subject.ts`, and rendered by every axis surface —
because a sentence written seven times is seven sentences that drift.

> ### This axis has nothing of yours to show yet
>
> **The missing input is a connected repository.** Until the platform can read your source it has no
> nodes of yours, so there is no current policy to render and nothing to change. It will not show you a
> sample node in this position — a demonstration node dressed as yours is worse than an empty screen,
> because you cannot tell which one you are looking at.
>
> [Connect a repository](/app/connections) · [Read what this axis does](/docs/concepts/{axis})

Three obligations, all three met above and all three asserted by fence 6.2:

1. **it names the missing input** — "a connected repository", not "no data";
2. **it links to the connection flow** — `/app/connections`;
3. **it links to the reading surface** — the disconnected reader *is* the first-time reader (PRD §4 row
   4), and sending them to the document is what makes moving the explanation an improvement for them
   rather than a loss.

It is delivered as a **200 carrying the word** `not_connected`, never a 404 (design D6). A 404 would send
the reader to look for a broken deployment when the truth is that they have not opted in.

---

## 1.5 — The prose budget

**Decided: a lede of at most 60 words, and a PER-ROUTE ceiling at the measured count rounded up to the
next 50.**

### 🔴 The answer to Q2 was "350", and it is not 350. This is the correction and its reason.

The 350 was chosen against measurements from a ruler that was then found to be **wrong**.
`scripts/lib/prose.mjs` extracted JSX text with `>([^<>]+)<`, which matches the run between the `>` of a
TypeScript generic and the next `<` — so `Promise<T> { const res = await fetch(` counted as eleven words
of prose. `/app/studio` measured 2,890 words in files that render a few hundred, and most of what the
counter called "prose" on any interactive surface was arrow functions.

Task 1.5 says the numbers are *"tuned once against the six rewritten surfaces before the fence is
enforced"*. The fence was not enforced yet, so the ruler was fixed first (`CODE_SIGNALS`, and a
five-word `LABEL_WORDS` floor so a button reading `Save` is not counted as documentation) and the
ceilings were re-derived from what the rewritten surfaces actually are.

| | Value | Why this number |
|---|---:|---|
| Lede | **60** words | Unchanged. PRD §6.3 FR10. |
| Route total | **measured + <50** | Every route sits within 50 words of its own ceiling. |
| Mutation margin | 70 words | Task 6.1 requires +70 to fail. With ≤50 words of headroom **everywhere**, +70 fails on **every route**, not just on one the drill picked. |

**The replacement is stricter where it matters.** A flat 350 gave `/app/harness` — now 160 words — a
hundred and ninety words of headroom, which is a fence that admits two and a half paragraphs before it
bites. `tests/p37-inventory.test.mjs` asserts the headroom property over the whole table, so the drill in
`p37-prose.test.mjs` is a statement about every route rather than about its chosen target.

**Where it is looser it is honest.** `/app/graph` at 550 and `/app/studio` at 500 are not carrying
documentation: the residual is interactive micro-copy and the engine's own verbatim refusal sentences in
the fixture editor. Cutting those to 350 would delete exactly the load-bearing text PRD §2.3 warns a word
count will delete. The ratchet stops them growing; it does not pretend they are documents.

The list and detail routes are budgeted the same way (PRD §14 Q3), each with a stated number in
`scripts/lib/prose-budgets.mjs` rather than an exemption.

### 🔴 What the budget does not check, stated here so nobody cites it past its limits

It measures **volume**. It cannot tell moved text from rearranged text. The same content survives a word
count as three shorter blocks — and, since the `LABEL_WORDS` floor, as a run of four-word fragments.
That is a real widening of design D3's stated blind spot and it is recorded in `prose.mjs` rather than
left to be discovered.

So it is paired rather than trusted: FR11 forbids tooltips, accordions and modals as destinations, and
`scan-prose.mjs` fails a working route where one of those **carries static prose** — the body is
extracted and measured, so a live data readout that happens to be called `Tooltip` is not caught while
an explanation moved into one is. §4.5's PR enumeration and fence 6.10's link check cover the rest.

A fence whose weakness is undocumented is a fence that will be cited as proof of something it never
checked (design D3).

---

## 1.6 — `/app/studio` is in full scope

**Inspected first, as the task requires.** What it already does: reads the reader's own nodes from
`GET /api/v1/workflows/{id}/nodes` (the columns are real call sites, with their current provider and
model), edits model and prompt per node, and stamps a bound cell `unverified`. What it does **not** do:
take its subject from the shell — it carries its own workflow picker in the matrix — and it is the
console's largest working route at 2,890 words.

**Decision (user, 2026-08-27): full scope.** The studio is rewritten with the editor kit like the other
six.

- It becomes the **seventh** surface in §3.6's ordering, placed **second** — it has the worst
  prose-to-capability ratio after `context`, and its per-node data is already real, which makes it the
  cheapest place to prove the kit against live data rather than against a fixture.
- Its matrix is **not** deleted. `ui-redesign-feature-and-visual-consistency`: a redesign may not lose a
  feature. The matrix keeps every column, every cell state and its cost figure; what changes is that its
  **workflow picker is replaced by the shell-resolved subject** (S8) and its explanation moves (S2–S5).
- Its model picker adopts FR7 — an unavailable model renders **disabled, naming the service it needs**,
  never hidden. This is `studio`'s row in §1.3 and QA 6.9's per-axis case.

---

## Cross-references

- Rule and its blind spot — [`design.md`](design.md) D2, D3
- What may not move — [PRD §6.4](../../../docs/prd/P37-source-bound-editors.md)
- Destinations authored before anything moves — [`design.md`](design.md) D4, task 4.1
- The fence and its per-route table — `web/console/scripts/lib/prose-budgets.mjs`
