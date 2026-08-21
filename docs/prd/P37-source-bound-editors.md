# PRD — P37: Source-Bound Editors

| | |
|---|---|
| **Phase** | P37 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p37-source-bound-editors`](../../openspec/changes/p37-source-bound-editors/) |
| **Lead roles** | Frontend Dev + Product Designer |
| **Support roles** | Backend Dev, System Designer, AI Engineer, QA, DevOps, Sales Operations |
| **Upstream** | P32 (a repository the platform can read) · P9 (console shell, BFF, viewport rule) · P23 (`reading-surface`) · P13–P18 (the axis registries and their vocabularies) · P31 (a conversation that links here) |
| **Unblocks** | P33's findings having a place to be acted on · P35 proposing a change against a node the reader has already seen |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

The customer console has thirteen working routes and **31,628 words** on them. `/app/context` alone
carries 1,995 — about eight minutes of reading — before the reader can change anything, and the page's
own doc comment is candid about why: *"this page is a capability explanation, not a live view"*. Its
coverage table is **transcribed from Go by hand**. Its diff is a **fixture**. `/app/memory` binds its
editor to `const NODE_ID = "recall"` — a demonstration node that belongs to nobody.

None of that was a mistake at the time. Each of those pages shipped in a phase where the *engine* had
landed and the reader's own source could not yet be read: there was no repository, so there was no node,
so the only honest thing to put on the page was an explanation of what would happen when there was one.
P32 removes that constraint. The moment the platform can read the reader's repository, a page that
explains what it *would* do to a hypothetical node — while the reader's real node sits one query away —
stops being honest and starts being filler.

P37 does one thing: **every working surface becomes an editor bound to a node in the reader's own
imported source, and the explanation moves to the reading surface that already exists for it.**

The rule that decides what moves is one sentence, and it is the whole design:

> **If a sentence is the same for every reader, it is documentation. If it changes with the reader's
> data, it is the product.**

That rule cuts cleanly through 31,628 words, and it also protects the things that must *not* be cut. A
refusal's cause text changes with the reader's data. `not_measured` and its named missing input change
with the reader's data. The four axis states change with the reader's data. Those stay — in fact they
become more visible, because they stop competing with eight paragraphs of preamble for the same screen.
What leaves is the part that is identical on every tenant's screen, and it leaves by moving to
[`reading-surface`](../../openspec/specs/reading-surface/spec.md) — a composition P23 already built, which
holds no session, makes no fetch, and scrolls as a document.

---

## 2. Problem & context

### 2.1 What is actually on these pages

Measured over `web/console/src/app/app/**`, counting on-screen text (JSX text nodes plus display string
literals; code comments excluded):

| Route | Words | What the reader can change there today |
|---|---:|---|
| `/app/context` | 1,995 | nothing — a transcribed coverage table, a fixture diff, sample decline cards |
| `/app/harness` | 1,723 | a demonstration authoring panel |
| `/app/delivery` | 1,185 | route configuration, plus a long explanation of the two modes |
| `/app/memory` | 1,152 | a demonstration authoring panel bound to `NODE_ID = "recall"` |
| `/app/authoring` | 798 | a preflight demonstration |
| `/app/wiring` | 656 | a demonstration editor |
| `/app/runs` | 408 | nothing (a list) |
| `/app/coverage` | 374 | nothing (a projection) |
| `/app` | 320 | nothing (a landing page) |
| `/app/transforms`, `/app/workflows`, `/app/variants`, `/app/studio` | 531 combined | list and detail views |
| **All of `/app` including sub-components** | **31,628** | |

Two of those numbers deserve to be read twice. The first is that the axis surfaces — the six pages whose
entire purpose is *changing how a node behaves* — carry 7,509 words between them and change nothing that
belongs to the reader. The second is 31,628: at an ordinary reading speed that is roughly two hours of
prose sitting in front of a product whose promise is that it does the reading for you.

### 2.2 Why it is like this, and why the reason has expired

Every one of these pages was written under a real constraint, and the constraint is stated in the source.
`/app/context`'s header says the coverage table *"is transcribed rather than fetched because this page is
a capability explanation, not a live view"*. `/app/memory`'s authoring panel explains that it is
interactive *"even though the change will not reach their source at this milestone"* — the codemod had
not landed.

So the pages are documentation because there was nothing else they could be. The engine existed; the
reader's node did not. Explaining the engine was the honest maximum.

P32 changes the input. Once a repository is connected, the platform holds the reader's IR: their nodes,
their call sites, their current context policy, their current memory strategy, their current loop. The
page can now answer *"what does **this** node do, and change it"* instead of *"here is what the platform
would do to a node like yours"*. Keeping the second answer once the first is available is not caution.
It is a page that has not been updated.

### 2.3 The failure this phase is most likely to produce

Deleting the wrong words. There are four kinds of text on these pages and only one of them is filler:

1. **Explanation** — the same for every reader. *("A sliding window keeps the most recent N turns.")*
   This is what moves.
2. **A refusal's cause text** — different for every reader, produced by the engine, and deliberately
   **not re-worded**. This is load-bearing and stays.
3. **A named missing input** — `not_measured` is a state, and the sentence naming what is missing is the
   only actionable part of it. Stays.
4. **A stated boundary** — *"authoring works, applying does not, and here is the artifact we owe you."*
   Stays, and stays **above** the control, not below the submit button.

A redesign optimising for a word count will delete 2, 3 and 4 along with 1, because they are all "text"
and only 1 is obviously disposable. The frontend lens's rule — *a redesign may not lose a feature* —
applies here even though none of these look like features: a refusal that is no longer rendered is a
capability the product silently stopped having. §6.4 exists to prevent exactly this, and §13's A4 is the
fence that catches it.

---

## 3. Goals & non-goals

### Goals

1. **G1** — On every axis surface, the reader edits **a node from their own repository**, named, with its
   current value shown, and never a demonstration node presented as theirs.
2. **G2** — The subject is chosen **once**, in the shell, not on each of six pages. The reader with one
   workflow and one node is asked nothing at all.
3. **G3** — Explanation moves to the reading surface and is reachable from the working surface by one
   link per section. Nothing is deleted outright; the reading surface is where it lands.
4. **G4** — Every refusal, `not_measured` state, and stated boundary survives the move, on the working
   surface, in its own visual state.
5. **G5** — One editor kit serves all six axes: a picker bound to the axis's closed vocabulary, a params
   form derived from that vocabulary's schema, validation at save, a preflight showing the resulting
   `config_hash` and the diff against the parent, and an `unverified` stamp until the harness has run.
6. **G6** — A working route carries at most one short lede. The static-prose budget is a fence, not a
   guideline.
7. **G7** — A reader with no connected repository sees `not_connected` naming the missing input, and is
   never shown a fixture in the place their data would go.

### Non-goals (with the phase that owns them)

- **Getting the repository in** — [P32](P32-repo-intake.md). P37 consumes the connection; it does not
  build it.
- **Deciding what is wrong with the reader's agent** — [P33](P33-surface-assessment.md).
- **Making the change reach the repository** — the axis materializers (P13–P18) and
  [P12](P12-forge-delivery.md). Where an axis cannot yet write, P37 renders the existing boundary; it
  does not build the codemod.
- **The operator console.** [P38](P38-agent-contract.md) is the same complaint about a different console
  and is a separate phase for a reason: the customer edits *their* configuration, the operator edits
  *the platform's*, and the two have different vocabularies, different gates and different blast radii.
- **A new visual language.** No new colours, radii, spacings or type scales. The editor kit is assembled
  from the components these pages already use.
- **Deleting the explanation.** It moves. A capability nobody can find is indistinguishable from one that
  does not exist, and that argument does not stop applying because the text was long.

---

## 4. Users & personas

| Persona | What they came to do | What the current page gives them |
|---|---|---|
| **Application engineer** (primary) | change the memory strategy on the node that is misbehaving | 1,152 words and a demo node called `recall` |
| **Staff / platform engineer** | audit which of their nodes have no context policy | a transcribed table of what policies exist |
| **Engineering manager** | see whether a change is safe to approve | a fixture diff |
| **First-time reader, nothing connected** | understand what this product does | the right page — but they should be on the reading surface, and the working surface should say so |

The fourth row is why §6.3 sends the disconnected reader to the reading surface rather than showing them
a working surface full of empty states. Today they get the explanation *instead of* the product; after
P37 they get the explanation *and* a named next step.

---

## 5. User stories

- **US1** As an engineer I open `/app/context`, see my own node's current policy, change it, and read the
  resulting diff — without reading a paragraph first.
- **US2** As an engineer with one workflow I am never asked which workflow.
- **US3** As an engineer with eleven workflows I choose a node once, and every axis page stays on it.
- **US4** As an engineer whose change is refused I read **the engine's own sentence** naming the axis, the
  node and the cause — not a paraphrase.
- **US5** As an engineer whose axis cannot yet be written into source I am told that **before** I compose
  the change, and I can still author and pin it.
- **US6** As a reader who wants to understand context policies I follow one link to a document that
  explains all eight of them properly — better than the paragraph that used to be squeezed onto the
  working page.
- **US7** As a reader with no repository connected I see what is missing and how to connect it, and I am
  not shown a sample node dressed as mine.

---

## 6. Functional requirements

### 6.1 The subject (capability `source-bound-editing`)

**FR1 — Every axis surface is bound to one `(workflow, node)` subject drawn from the reader's imported
source.** The surface renders that node's **current** value for its axis, resolved from the IR, and the
node is named on screen.

**FR2 — The subject is selected once, in the shell, and persists across axis surfaces.** A reader who
switches from `/app/context` to `/app/memory` is looking at the same node. Today's `loadProjection`
already picks the most recently reported *workflow* without asking; FR2 extends that resolution to the
node and makes it visible and changeable, because "the platform picked one for you" must never be
silent when the reader can act on the wrong one.

**FR3 — A reader with exactly one candidate node is asked nothing.** The picker renders as a name, not
as a control.

**FR4 — With no connected repository, an axis surface renders `not_connected`, names the missing input,
and links to the connection flow.** It SHALL NOT render a fixture, a sample node, or a demonstration
value in the position the reader's own data would occupy. A worked example, where one is still useful,
appears on the **reading surface**, labelled as the platform's fixture.

### 6.2 The editor kit

**FR5 — One kit, six axes.** Every axis editor is composed of the same five parts:

| Part | Behaviour | Why it is in the kit and not per page |
|---|---|---|
| **Picker** | choices are the axis's closed vocabulary at its recorded set version | six hand-written pickers drift into six vocabularies |
| **Params form** | derived from the selected entry's `ParamsSchema` | a hand-written form is a second, staler copy of the schema |
| **Validation at save** | refused at save, naming the entry and the parameter | a malformed value discovered at run time is discovered by the wrong person |
| **Preflight** | the resulting `config_hash` and the diff against the parent variant | a change whose effect you cannot see before saving is a change you approve blind |
| **Verification stamp** | `unverified` until the harness has run | a configuration is not an improvement until something measured it |

**FR6 — No axis is a text box.** This is the same rule the operator console owes
([`operator-agent-authoring`](../../openspec/specs/operator-agent-authoring/spec.md), and
[P38](P38-agent-contract.md) is where that debt is paid). A free-text field for a value with a closed
vocabulary eventually holds a value nothing can interpret, and the closed sets exist precisely so a
stored `config_hash` still means something months later.

**FR7 — An option the deployment cannot supply is shown, disabled, with the service it needs.** Never
hidden. A hidden option is indistinguishable from one that does not exist, and a reader who cannot see it
cannot ask for it.

**FR8 — Every axis renders its state from the same four-valued vocabulary** — `measured`, `observed`,
`not_measured`, `refused` — as P33 defines it. Six axes with six state vocabularies is six times the
copy and one reader's confusion.

### 6.3 What moves, and where it moves to

**FR9 — The moving rule.** A block of static text belongs on the reading surface when its content does
not vary with the reader's data. It belongs on the working surface when it does. There is no third
category and no judgement call at review time.

**FR10 — Each working surface carries at most one lede, of at most 60 words**, plus one link per section
to the reading-surface document that explains it. The budget is asserted by a fence (§13 A3).

**FR11 — Moved text lands on `reading-surface`, not in a modal, a tooltip, an accordion or a "learn
more" drawer.** Those are the three ways text gets hidden while appearing to be kept: a tooltip is
unreachable by keyboard on half the controls that have one, an accordion is a paragraph nobody opens,
and a modal is a paragraph that interrupts. The reading surface is a real document with a table of
contents, a search island and a URL that can be linked from a `finding`.

**FR12 — Nothing is deleted in this phase.** Every moved paragraph is either on the reading surface or in
the diff of the commit that moved it, and the PR body names each destination. A redesign that silently
loses an explanation has traded a documented capability for a shorter page.

### 6.4 🔴 What may not move, and may not be shortened

**FR13 — A refusal's cause text stays on the working surface, verbatim.** It is produced by the engine,
it names the axis and the node, and it is not re-worded — a re-worded safety boundary is a second, softer
statement of it.

**FR14 — `not_measured` stays, with its named missing input.** It is never rendered as zero, never as an
empty state, and never omitted. This is P29's `not reported` discipline and P33's assessment rule; P37
does not get to relax it for layout.

**FR15 — A stated boundary stays above the control it bounds.** Where an axis can be authored but not yet
written into source, the reader learns that **before** composing the change, not after pressing save.
Composing a change and only then meeting a wall is a technically honest bait-and-switch.

**FR16 — The `unverified` stamp stays.** A `config_hash` the reader can pin is real; an improvement is
not claimed until the harness measured it.

**FR17 — Coverage stops being a transcription and becomes a live per-node read.** Today `/app/context`
holds a hand-copied table of what each policy does, kept honest by
`TestContextCoverageTableMatchesEngine`. After P37 the surface reads coverage **for the reader's node**
from the engine at request time. The fence is then removed *because the artifact it guarded no longer
exists* — which is the only acceptable reason to remove a fence, and the PR that removes it says so.

### 6.5 The conversation's landing site

**FR18 — Every `finding` P31 emits links to the axis surface for the node it describes**, with the subject
pre-selected. The conversation says what is wrong; the surface is where it is changed. The conversation
never becomes a second editor, because a second authoring path is where the validation is missing.

---

## 7. Non-functional requirements

### 7.1 Viewport and density

P9's rule stands: the shell occupies the viewport, the page does not scroll, and multi-section pages use
in-page tabs. P37 makes that rule *achievable* rather than fought — six paragraphs and a table do not fit
above the fold, which is why today's pages either scroll or bury the control. An editor, its current
value and its preflight do fit.

The reading surface keeps its stated exemption: it scrolls as a document, because a document that pages
is a document nobody reads.

### 7.2 Accessibility, i18n, tokens

No new colour, spacing, type or radius literals; `npm run scan:tokens` stays green. The hazard palette
appears on `refused` only. Every picker is a real form control with a label, reachable by keyboard, and
the params form's validation errors are associated with their fields rather than announced as a banner.
All formatting goes through the single `en-US` swap point.

### 7.3 Determinism and the browser's role

Unchanged and non-negotiable: **the browser derives nothing.** The preflight's `config_hash`, the diff,
the coverage state and the verification stamp are computed server-side and rendered as received. An
editor is a place to *compose* a value, never a place to *compute* one.

### 7.4 Privacy

The reader's source is already held under P32's connection. P37 adds no new read of it and no new
retention: it renders what discovery already produced. The subject selection is per-person UI state and
carries no repository content.

---

## 8. System design summary

### 8.1 Shape

```
P32 connection ──► discovery IR ──► subject resolver (workflow, node)
                                          │
                    ┌─────────────────────┼─────────────────────┐
                    ▼                     ▼                     ▼
              axis surface          editor kit            reading surface
        (context/memory/harness/  picker · params ·     (moved explanation,
         wiring/authoring/studio)  validate · preflight   worked examples,
                    │               · unverified stamp     labelled fixtures)
                    ▼
            server-computed: config_hash, diff, coverage state, verification
```

### 8.2 Decisions

**D1 — The subject is resolved in the shell, not per page.** *Why:* six pickers on six pages ask the
same question six times, and the answer is the same every time. `interaction-simplicity-first`: the input
you can remove is the one you remove. *Rejected:* a picker per page — simpler to build, and it makes the
common reader (one workflow, one node) answer a question they do not have.

**D2 — The moving rule is mechanical, not editorial.** *Why:* "is this paragraph useful?" has a different
answer for every reviewer and every deadline, so it produces a page that grows back. "Does this sentence
change with the reader's data?" has one answer, and it can be argued about *once*, per sentence, in a PR.
*Rejected:* a style guide asking authors to "be concise" — which is what produced 31,628 words while
everyone involved believed they were being concise.

**D3 — A word budget with a fence, and its known weakness stated.** *Why:* a budget nobody measures is a
preference. *The weakness:* a word count is gameable — the same content survives as three shorter blocks,
or as a tooltip. So the fence is paired with FR11 (no tooltips, accordions or modals as destinations) and
with review, and the PRD says plainly that the fence cannot detect content that merely got rearranged.
A fence whose limits are unstated is a fence that will be trusted past them.

**D4 — Nothing is deleted; text moves.** *Why:* the frontend lens's rule against losing features in a
redesign applies to explanations too, and the cost of moving is one commit while the cost of a deletion
that mattered is a capability nobody notices is gone. *Consequence:* the reading surface grows
substantially in this phase, and that is the intended shape — P23 built it to hold documents.

**D5 — The coverage transcription is replaced by a live read, and only then is its fence removed.**
*Why:* removing a fence is normally forbidden. It is permitted here precisely because the fence guards a
transcription, and after FR17 there is no transcription — the surface reads the engine. Removing a fence
whose subject still exists would be the failure this repository has recorded more than once.

**D6 — `not_connected` is a rendered state, not an empty page.** *Why:* the three transport treatments
(`not-mounted`, `read-failed`, `not-reported`) already exist in `loadProjection` and are deliberately
distinct. `not_connected` is a fourth, and it is the customer's own boundary — it arrives as a 200
carrying that word, never as a 404, because a 404 would send the reader to look for a broken deployment.

### 8.3 Design key points

- The editor kit is **extracted from what already works**, not invented: `/app/memory`'s authoring panel
  already does picker → params → preflight → `unverified`. P37 generalises it and binds it to the
  reader's node. This is the third occurrence of the pattern, which is the point at which the repository's
  own rule says to abstract it.
- The subject resolver reuses `loadProjection`'s three-state discipline verbatim rather than inventing a
  fourth vocabulary for "which node am I looking at".
- Fences added: static-prose budget, no-fixture-in-reader-position, refusal-survives-the-move. Fences
  removed: exactly one, with the reason in §6.4 FR17.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

This phase is that motto twice. The input reduced is the subject question — asked once instead of six
times, and not at all for the common reader. The truth that must not reduce is everything in §6.4: the
refusal, the missing input, the boundary, the stamp. It is worth naming the temptation directly, because
it will arrive in review as a reasonable suggestion: *"the boundary banner is repeated on four pages,
can we move it to the docs?"* No. It changes with the reader's axis and node, and it is the sentence
that stops them composing a change that cannot land.

Scope fidelity: P37 restructures the customer console's working surfaces. It does not touch the operator
console (P38), does not change any engine behaviour, and does not remove an axis, a state or a control.
Where a page has a capability today, it has it after.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

Two ladder calls.

The first: **UX (3) versus implementation cost (8).** Building an editor kit and rewiring six surfaces is
more work than trimming paragraphs. Level 8 never outranks level 3, and "the pages are shorter" without
source binding would leave the reader with less explanation *and* still nothing of theirs to edit —
strictly worse than today.

The second, and it is the one to watch: **deleting text is a one-way door and moving it is not.** A
deleted explanation is recoverable from git in theory and irrecoverable in practice, because nobody knows
it is missing. D4 chooses the reversible act. This costs a larger reading surface, which is level 7 at
worst.

No new table, no new endpoint shape for the axis reads — the IR and the registries already answer every
question this phase asks. The one genuinely new persisted thing is the subject selection, and it is
per-person UI state, not a domain object.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

The backend work here is reads, with one exception: saving an axis value on the reader's node writes a
registry entry and a variant. That path gets the four-layer treatment — HTTP, then `SELECT` the registry
row, then `SELECT` the variant, then assert the surface renders the new `config_hash`. A save that
returns 200 and produces no row is exactly the failure `INSERT OR IGNORE` has produced in this repository
before.

The coverage read (FR17) must be per node and must not silently fall back to a default when the node's
policy cannot be resolved — a fallback here is a page that tells the reader their node has a policy it
does not have. Unresolvable is `not_measured`, named, with a WARN carrying `request_id` / `trace_id` /
`span_id`.

### 9.4 Senior Frontend Dev — *three states stay three; four states stay four*

The whole phase is this rule under pressure. Six surfaces are being rewritten at once, each carrying
states that look like decoration: `refused` with its verbatim cause, `not_measured` with its named input,
`unverified`, `not_connected`, and the disabled-with-a-reason option. Every one of them is a state the
reader acts on, and every one of them is a candidate for accidental deletion during a layout rewrite.

The other standing rule: no improvised styling. Every value comes from the existing tokens, and the
editor kit is assembled from `primitives.tsx`, `tabs.tsx` and the existing authoring components rather
than from a new set. If a needed component does not exist, the answer is to extract it from the page that
already draws it — not to invent a variant beside it.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

P37 ships no model. Its AI-lens obligation is the one thing it does inherit: the axis state shown per
node must never be an average across the reader's nodes. "Context: 80% covered" is a number that hides
the one node with no policy, and the one node with no policy is the reason the reader came. Six axes ×
N nodes is a matrix, and it renders as a matrix.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

Blast radius: six customer-facing routes rewritten in one phase is the largest single console change
since P9. It ships behind the existing route structure so each surface can land independently, and the
reading-surface destinations land **first** — moving text to a page that does not exist yet is how a link
becomes a 404 in production.

Observable: the subject resolver's failure modes (`not_connected`, `read-failed`, `not-mounted`) are
counted and readable from the health endpoint, not only logged. Least privilege: unchanged; P37 reads
what P32's connection already granted and asks for nothing more.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

Four fences, and each must be shown to fail:

1. **Prose budget** — add 70 words of static text to a working route; the build fails.
2. **No fixture in the reader's position** — render an axis surface with no connection and assert the
   subject region contains no sample node; mutate to render one and the test fails.
3. **Refusal survives** — drive a real engine refusal through a rewritten surface and assert the cause
   text appears **verbatim**; mutate the renderer to paraphrase and the test fails.
4. **Save writes** — HTTP → registry row → variant row → rendered `config_hash`, per axis.

The acceptance that matters is not a green build. It is a browser session on a connected repository where
a person changes their own node's memory strategy and sees their own diff. A1 is that session.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

Two boundaries must be said plainly and not softened. **First**: several axes can be authored but not yet
written into source. That is unchanged by P37 and P37 must not let a shorter page imply otherwise — a
page that used to spend four paragraphs explaining the boundary and now spends none has *promised more*
by saying less. FR15 is the mitigation and it is a customer-facing commitment, not a layout preference.

**Second**: the noun dictionary. `node`, `axis`, `policy`, `strategy`, `variant`, `config_hash` mean
exactly what they mean elsewhere in the product, and the reading surface is where they are defined once.
Six pages that each re-explain "strategy" in their own words is how a product acquires six meanings for
one noun.

---

## 10. Dependencies

| Dependency | What P37 needs from it | If it is not there |
|---|---|---|
| [P32](P32-repo-intake.md) | a connected repository and the IR derived from it | every axis surface renders `not_connected` — correct, but the phase delivers little |
| [P9](P9-web-console.md) | the shell, the BFF pass-through, the viewport rule | no change to any of them; P37 lives inside them |
| [P23](P23-legal-and-developer-docs.md) | `reading-surface` as the destination for moved text | the move has nowhere to land; this is why reading-surface pages ship first |
| P13–P18 | the axis vocabularies and their param schemas | the editor kit has nothing to bind pickers to |
| [P31](P31-conversational-console.md) | findings that link here | independent; P37 stands alone and P31 is better with it |
| [P33](P33-surface-assessment.md) | the four-state vocabulary | P37 would have to define its own, which is the outcome §6.2 FR8 exists to prevent |

---

## 11. Risks & mitigations

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| R1 | A load-bearing state is deleted during the layout rewrite and nobody notices, because its absence looks like a clean page | **High** | §6.4 enumerates them; fence 3 drives a real refusal through each rewritten surface |
| R2 | The prose budget is met by rearranging content into tooltips and accordions | Medium | FR11 forbids those destinations; D3 states the fence's blind spot rather than trusting it |
| R3 | The reading surface becomes a dumping ground and the moved text is worse there than it was inline | Medium | Moved text is edited into documents with a table of contents, not appended; the PR names each destination section |
| R4 | The subject resolver picks the wrong node and the reader edits something they did not mean to | **High** | The subject is always named on screen and always changeable; a reader with one candidate is told which one, not merely defaulted silently |
| R5 | Removing `TestContextCoverageTableMatchesEngine` removes real protection | Medium | Only after FR17's live read lands, and the PR states that the transcription it guarded no longer exists |
| R6 | Six surfaces rewritten at once produces a regression nobody can bisect | Medium | Each surface lands independently; reading-surface destinations first |
| R7 | A shorter page implies the platform can do more than it can | Medium | FR15 keeps the boundary above the control; §9.8 makes it a customer-facing commitment |

---

## 12. Rollout & test strategy

1. **Reading-surface destinations first.** The documents that will receive the moved text ship before
   anything is moved. A link to a page that does not exist yet is a 404 in production.
2. **The editor kit, extracted from `/app/memory`'s existing panel**, with its own tests, before any
   surface adopts it.
3. **The subject resolver**, with its four states, behind the existing projection code path.
4. **One surface at a time**, in order of ratio of prose to capability: `context` (1,995 words, nothing
   editable) first, then `harness`, `memory`, `wiring`, `authoring`, `delivery`.
5. **The coverage live read** and only then the fence removal.
6. **Browser acceptance on a connected repository** — a person edits their own node and sees their own
   diff — before the phase is called done.

Every step is independently verifiable and no step requires a mock: the IR, the registries and the
engine's coverage are all real by the time P37 starts.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | A person edits their own node's axis value and sees their own diff | browser acceptance on a connected repository — not a green build |
| A2 | Every axis surface names the node it is bound to | one case per surface |
| A3 | Static prose on a working route stays within budget | fence over the built routes; mutation-verified by adding 70 words |
| A4 | Every refusal, `not_measured` and boundary from §6.4 renders after the rewrite | drive a real engine refusal per surface; assert cause text verbatim |
| A5 | No fixture appears where the reader's data belongs | render every surface unconnected; assert `not_connected` and no sample node |
| A6 | A save writes | HTTP → registry row → variant row → rendered `config_hash`, per axis |
| A7 | The subject persists across axis surfaces and is always visible | navigate all six; assert the same node and a visible name |
| A8 | Every moved paragraph has a destination | PR body enumerates them; no paragraph is deleted without one |
| A9 | An unavailable option is shown disabled with the service it needs | one case per axis that has one |
| A10 | Axis state is per node, never averaged | fixture with one uncovered node among many; assert it is visible |

---

## 14. Open questions

| # | Question | Why it is open |
|---|---|---|
| **Q1** | Is the subject `(workflow, node)` or just `node`? | A node id is unique within a workflow, not across them. Carrying both is unambiguous and is one more thing to keep in sync. **Recommendation: carry both; display the node and disambiguate by workflow only when two nodes share a name.** |
| **Q2** | What is the prose budget number? §6.3 proposes 60 words per lede. | Too tight and authors will split blocks to evade it; too loose and it asserts nothing. **Recommendation: 60 for the lede, no other static block over 25, tuned once against the six rewritten surfaces before the fence is enforced.** |
| **Q3** | Do `/app/runs`, `/app/variants`, `/app/workflows`, `/app/transforms` — the list and detail views — get the same treatment? | They carry far less prose (141–408 words) and their subject is chosen by navigation, not by a resolver. **Recommendation: budget applies to them, subject resolver does not.** |
| **Q4** | Where does `/app/studio` sit? It is the prompt-and-model surface and is already interactive. | It may already satisfy most of P37, in which case it gets the subject binding and nothing else. **Needs an inspection before it is scoped in.** |
| **Q5** | Does the subject selection persist server-side (per person) or in the browser? | Server-side survives a device change and is a new per-person record. Browser-side is free and forgets. **Recommendation: browser-side for this phase; it is UI state, and D1's benefit does not depend on it surviving devices.** |
| **Q6** | Does the reading surface's moved content need i18n treatment now, given MVP is English-only? | Moving text is the cheapest moment to structure it for translation, and doing it later means editing every document twice. **Open — depends on whether translation is on the roadmap within two phases.** |
