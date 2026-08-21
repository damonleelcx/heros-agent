# PRD — P39: The Conversation-Only Console

| | |
|---|---|
| **Phase** | P39 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p39-conversation-only-console`](../../openspec/changes/p39-conversation-only-console/) |
| **Lead roles** | Product Designer + Backend Dev |
| **Support roles** | Frontend Dev, System Designer, AI Engineer, QA, DevOps, Sales Operations |
| **Upstream** | [P31](P31-conversational-console.md) (the conversation exists) · P9 (console + BFF) · P29 (linked runs) · P12 (forge delivery) |
| **Unblocks** | P40 (the act path: the conversation proposes, approves, and delivers) |
| **Status** | Proposed — awaiting sign-off on §13 |

---

## 1. Summary

P31 built a conversation that **routes to** the console. P39 makes it a conversation that **replaces** it.

Today the left navigation carries twelve task-domain pages. A person who asks the agent about one of
them gets a correct, evidence-backed, one-sentence *summary* and a link to the page. The page is still
where the answer lives. The conversation is a table of contents.

P39 inverts that for the ten **read-only** domains: the agent returns the page's content as messages,
the evidence expands inside the message, and the ten routes are deleted. The two **act-bearing**
domains (Studio, Author) and the proposal route keep their pages until P40 wires the act path — because
deleting a surface whose capability has no conversational replacement removes the capability from the
product, and no gate in this repository would go red when it happened.

**The one-line scope statement:** P39 deepens reads and deletes ten routes. It adds no ability to change
anything. `proposal`, `approval_request` and prose `answer` remain unemitted in production, exactly as
they are today.

---

## 2. Problem & context

### 2.1 What P31 actually shipped, measured rather than summarised

Read from the code on 2026-08-21, not from P31's report:

| Layer | State |
|---|---|
| Routing | **Works.** Deterministic lexical router, no model call. 14/14 intents at 100% recall over 76 held-out questions; abstention precision 100% (15/15); redirection 7/7. |
| Intent ↔ surface coverage | **Complete and fenced.** `internal/conversation/intent.go` is a closed table of 14; the 12 route-backed entries are exactly the 12 nav items, and `TestIntentSetEqualsTheWorkingSurfaceSet` fails the build on drift. |
| Reads | **Shallow.** One `SurfaceReading` per turn, carrying one aggregate `Claim` string. |
| Node scoping | **Absent.** `SurfaceReader.Read(ctx, tenantID, workflowID, spec)` takes no node. Every answer is workflow-wide. |
| Conversation history | **Absent.** `Router.Route(question)` sees one sentence. No follow-ups, even within one conversation. |
| Acting | **Absent.** `Runner.Run` emits five kinds: `plan`, `progress`, `finding`, `result`, `refusal`. |
| `assess` / `improve` | **Never mounted.** `platformSurfaceReader.Mounted` returns `false` unconditionally for both. Owned by P33/P35, not P39. |

### 2.2 🔴 The conversation currently depends on the pages existing

This is the fact that makes "delete the pages" a design problem rather than a deletion:

1. **Three link-outs.** A `plan` step links to `step.surface`; a `finding` links to `surface_href`; a
   `refusal` links to `surface_href` (`web/console/src/components/conversation/messages.tsx`).
2. **One route hardcoded into prose.** `internal/api/conversationreader.go:308` renders
   `"%d runs are available to compare on /app/variants"`. Delete the route and the agent recommends a 404.
3. **The fence is anchored to the route table.** `RouteBackedSurfaces()` must equal the console's
   `WORKING_SURFACES` (`web/console/src/lib/routes.ts:83`), and a route-backed intent must have a
   surface beginning `/app/`. Deleting ten routes breaks the anchor of the one fence that stops the
   intent set drifting away from the product.

### 2.3 🔴 The failure this phase is most likely to produce

**A page is deleted, its content is not returned, and nothing goes red.**

The mechanism is precisely §2.2's third point. When ten routes are deleted,
`TestIntentSetEqualsTheWorkingSurfaceSet` fails — correctly, loudly, and *for a reason that looks like
bookkeeping*. The cheapest way to make it pass is to delete the intents with it, or relax the fence to
skip removed routes. Either one restores green and silently removes the drift protection that P26's
fourteen phases of operator-console rot produced.

The second-cheapest way is to keep the intents, re-point `Surface` at something that is not a route, and
leave the reader returning the same aggregate sentence. That produces a console with no pages and no
depth: every question answered with a count, and no way to see the grid behind it. The customer loses
the product and the build stays green.

So P39's first requirement is not a feature. It is: **the fence must be re-anchored to something a
deleted route cannot satisfy**, and that re-anchoring must land *before* the first route is deleted.

---

## 3. Goals & non-goals

### Goals

| # | Goal | Done when |
|---|---|---|
| G1 | A finding can carry a **surface's content**, not a summary of it | The coverage grid, the run table, the receipt diffstat and the delivery route are each returned inside a message |
| G2 | A question can be scoped to **one node** | "what does the retriever node remember" answers about that node, and an unresolvable node name refuses by name |
| G3 | Evidence expands **in place** | No message links to `/app/*` for its own evidence |
| G4 | A follow-up works **within one conversation** | "and what about context?" after a memory question routes on the prior turn's subject, or refuses saying it could not |
| G5 | Ten read-only routes are **deleted** | They 404, they are absent from the nav, and no message references them |
| G6 | The drift fence survives the deletion **with more force, not less** | The fence anchors on a registered reader and its declared detail shape; a reader-less intent fails the build |

### Non-goals (with the phase that owns them)

| Not in P39 | Owner |
|---|---|
| The agent proposing a change, requesting approval, or opening a PR | **P40** |
| Deleting `/app/studio`, `/app/authoring`, `/app/workflows/[id]/proposals` | **P40** |
| Prose `answer` messages | **P40** — an unemitted kind is safer than an unfenced one |
| `assess` / `improve` becoming mounted | P33 / P35 |
| Edge/ordering data so `graph_order` can be measured | P34 |
| A model-based router | Out of program — FR11's pin requires determinism before spend |

---

## 4. Users & personas

| Persona | What changes for them |
|---|---|
| **The engineer who linked a repo** | Stops learning a twelve-item navigation. Asks in English, receives the grid. |
| **The engineer who already knows the console** | 🔴 Loses ten bookmarks. §12 covers the redirect window. |
| **The operator diagnosing a customer** | Unaffected — the admin console (`web/admin-console`) is out of scope. |
| **The evaluator on a trial** | The first screen is a text box, not a menu. This is the persona the phase is for. |

---

## 5. User stories

1. As an engineer, I ask *"what did you measure, and what did you not?"* and get the node × axis grid in
   the message — every cell, with its state — not "N of M pairs can be edited".
2. As an engineer, I ask *"what does the reranker node remember between calls?"* and get that node's
   memory verdict, or a refusal naming the node I typed and the nodes that do exist.
3. As an engineer, I read a `not_measured` finding and the next action named is one I have not already
   run.
4. As an engineer, I ask a follow-up without repeating the subject, and it either works or tells me it
   could not carry the subject forward.
5. As an engineer with a bookmark to `/app/coverage`, I land somewhere that asks my question for me.

---

## 6. Functional requirements

### 6.1 Backing becomes the reader, not the route (capability `conversational-console`)

**FR39-1** — `Backing` SHALL gain `BackedByReader`, and the ten deleted surfaces SHALL move to it. A
reader-backed intent names a **reader key**, not a path.

**FR39-2** — Every intent SHALL resolve to exactly one registered reader, and the registry SHALL be the
fence's anchor. An intent with no reader fails the build; a reader with no intent fails the build.

**FR39-3** — `RouteBackedSurfaces()` SHALL continue to exist and continue to equal `WORKING_SURFACES`,
covering only the intents whose surfaces survive P39 (`prompt_model`, `author`). 🔴 The fence is
narrowed, never relaxed: the surviving routes are still checked, and the departed ones are checked more
strictly by FR39-2.

### 6.2 A finding carries a detail payload

**FR39-4** — `FindingPayload` SHALL gain one optional field, `Detail`, carrying a **typed, closed** union
of detail shapes. Each reader declares exactly one shape.

**FR39-5** — The shapes SHALL be closed and few: `grid` (node × axis cells), `table` (rows with named
columns), `diffstat` (per-file counts plus the unified diff for one file), `record` (ordered key/value
facts). A fifth shape requires a spec delta.

**FR39-6** — 🚫 `Detail` SHALL NOT be free-form JSON, and SHALL NOT be prose. A shape the console cannot
type is a shape the console renders as `[object Object]` to a customer.

**FR39-7** — A finding whose reader declares a shape and returns none SHALL be refused by the emitter,
in the same way an evidence-less finding is refused today. An empty grid renders as "no cells", never as
a card with a claim and nothing under it.

### 6.3 Node scoping

**FR39-8** — `SurfaceReader.Read` SHALL take a `Subject` carrying an optional node identifier.

**FR39-9** — When a question names a node the reported IR does not contain, the turn SHALL refuse
naming **the string the person typed** and listing the node identifiers that do exist. 🚫 It SHALL NOT
fall back to the workflow-wide answer: a broader answer to a narrower question is a wrong answer that
reads as a right one.

**FR39-10** — When a question names no node, the reading is workflow-wide and the finding SHALL say so.

### 6.4 Evidence is inline

**FR39-11** — No `plan`, `finding`, `result` or `refusal` message SHALL carry an `/app/*` href for a
surface deleted by this phase.

**FR39-12** — `EvidenceRef` SHALL remain an opaque identifier (`axis-projection:<wf>@<rev>`) and SHALL
render as a copyable chip, not a link.

**FR39-13** — A refusal MAY still redirect to an out-of-scope surface that still exists
(`/app/billing`, `/app/settings/members`). `TestEveryOutOfScopeRedirectionNamesARealSurface` continues
to guard this and SHALL be extended to the deleted set.

### 6.5 Follow-up within one conversation

**FR39-14** — The router SHALL receive the prior turn's resolved `(intent, subject)` and MAY use it to
resolve a question that names an axis but no subject.

**FR39-15** — 🔴 Carried-forward subject SHALL be stated in the `plan` message. A turn that silently
inherits a subject is a turn whose answer the reader cannot attribute.

**FR39-16** — When no prior turn exists and the question is unresolvable alone, the turn SHALL abstain
saying the question depends on an earlier one. This is the behaviour the Ask page already promises in
copy; P39 makes the promise conditional on there being no history, rather than absolute.

**FR39-17** — Carry-forward SHALL NOT cross conversations, and SHALL NOT survive a `pin` replay whose
pinned subject differs.

### 6.6 Reader depth, per surface

Each reader's minimum content. 🔴 A row that ships without its detail shape is a page that was deleted
for nothing.

| Intent | Shape | Must carry |
|---|---|---|
| `graph` | `record` | node count, revision, per-language counts, and the node identifiers |
| `graph_order` | `record` | `not_measured` naming the absent edges (unchanged; P34 owns the fix) |
| `coverage` | `grid` | every (node × axis) cell with its state and cause |
| `context`, `memory`, `harness`, `prompt_model`, `author` | `grid` | the cells for that intent's axes only |
| `run_history` | `table` | one row per linked run — id, workflow, revision, when, headline numbers |
| `compare` | `table` | two runs side by side, per-metric delta. 🚫 Never a route reference |
| `preview_change` | `diffstat` | per-node outcome and the diffstat for one `(config_hash, source_revision)` |
| `deliver` | `record` | the route condition, its target, and the last delivery's outcome |

### 6.7 Deletion set

**FR39-18** — These SHALL be removed from the navigation and their routes deleted:
`/app/workflows` (list, `[id]`, `graph`, `board`, `evalset`), `/app/runs` (incl. `[runId]`, `live`),
`/app/variants` (incl. scorecard), `/app/transforms` (incl. `[configHash]/[sourceRevision]`),
`/app/delivery`, `/app/wiring`, `/app/context`, `/app/memory`, `/app/harness`, `/app/coverage`.

**FR39-19** — These SHALL remain, unchanged, until P40: `/app/studio`, `/app/authoring`,
`/app/workflows/[workflowId]/proposals` and `.../proposals/[proposalId]`.

**FR39-20** — 🔴 A deleted route SHALL 404, not redirect to `/app/ask`. §12 covers the transition
window separately; a permanent silent redirect teaches nobody where the answer went.

---

## 7. Non-functional requirements

### 7.1 Performance

A `grid` for a 500-node workflow across 7 axes is 3,500 cells. **NFR39-P1**: the detail payload SHALL be
bounded by a declared cell/row ceiling, and a truncated payload SHALL carry the count it omitted and the
narrowing that would show them. 🚫 Silent truncation renders "everything is fine" over the part nobody
sent.

**NFR39-P2**: the ceiling SHALL be enforced in the reader, before serialisation — not in the console.

### 7.2 Privacy and credential posture

Unchanged from P31 §7.2. **NFR39-S1**: a detail payload SHALL carry no repository content beyond what
the corresponding page renders today. The diffstat shape is the one that could regress this; it is
bounded to the receipt the platform already stores.

### 7.3 🔴 Untrusted input, restated because the surface widens

P31 §7.3's boundary holds: repository content is untrusted input. P39 widens what reaches the browser —
node identifiers, cell causes, file paths, diff hunks — all authored in the customer's repository.

**NFR39-S2**: every string in a detail payload SHALL be rendered as text, never as markup, and the fence
for it SHALL run with injection detection disabled, matching P31 §6.3's method.

**NFR39-S3**: the effect table (`internal/conversation/effects.go`) SHALL be unchanged by this phase.
Adding a detail payload to `finding` — a kind with no effect — SHALL NOT give it one.

### 7.4 Accessibility, i18n, tokens

**NFR39-A1**: the grid SHALL be a real `<table>` with header scope, not a div lattice.
**NFR39-A2**: cell state SHALL NOT be conveyed by colour alone.
**NFR39-A3**: all copy through the existing i18n point; all colour through project CSS variables. 🔴 No
`@apply` of a project class — see the Tailwind v4 defect in P31's evidence.

---

## 8. System design summary

### 8.1 Shape

```
question ─▶ Router(question, priorSubject) ─▶ Intent + Subject
                                                │
                                       ReaderRegistry[Intent]      ◀── the new fence anchor
                                                │
                                    Read(ctx, tenant, workflow, Subject, spec)
                                                │
                                    SurfaceReading{Claim, Evidence, State, Detail}
                                                │
                                          Emitter (refuses a declared-but-absent Detail)
                                                │
                                        finding message ─▶ console renders the shape inline
```

### 8.2 Decisions

Each arbitrated by the eight-level law: **security > stability > UX > operability > non-evolvable >
non-extensible > maintenance > implementation cost**.

**D1 — The fence anchors on a reader registry, not on the route table.**
*Problem:* deleting routes removes the anchor of the only fence stopping intent/product drift.
*Alternatives:* (a) relax the fence to skip removed routes; (b) keep the routes mounted but unlinked;
(c) re-anchor on a registry of readers.
*Arbitration:* (a) loses the fence — level 2 (stability of the contract) traded for level 8. Refused by
L1. (b) is the option the user explicitly declined, and leaves ten unmaintained routes — level 7.
(c) costs more code and keeps the guarantee. **Chosen: (c).**
*Effect:* an intent without a reader, or a reader without an intent, fails the build — a strictly
stronger statement than "a route exists".

**D2 — `Detail` is one optional field carrying a closed union, not four new payloads.**
*Problem:* four new message payloads is four new wire contracts (careful-api-creation).
*Alternatives:* (a) four new message kinds; (b) free-form JSON blob; (c) one optional field, closed union.
*Arbitration:* (a) expands the vocabulary the effect table is checked against — level 1 adjacency,
avoid. (b) is unevolvable in the worst way: no consumer can be type-checked, and the console renders
whatever arrives — level 5. (c) adds one field and a union the code generator already knows how to
project into TypeScript. **Chosen: (c).**

**D3 — A node the IR does not contain refuses; it does not widen.**
*Arbitration:* level 3. A workflow-wide answer to a node-scoped question is indistinguishable from a
correct one, which is the exact failure class this program was built to eliminate.

**D4 — Carry-forward is explicit in the `plan`, or it does not happen.**
*Arbitration:* level 3 against level 8. Implicit state in a conversation is state the reader cannot
audit; stating it costs one line in a payload already emitted.

**D5 — Deleted routes 404; the transition uses a dated, removable redirect.**
*Arbitration:* level 3 vs level 4. A permanent redirect is operationally free and teaches nothing; a
404 with no transition strands live bookmarks. §12 resolves it with a bounded window.

**D6 — The act path is not in this phase.**
*Arbitration:* level 2. Studio and Author are the only surfaces through which a customer changes
anything. Deleting them before `proposal`/`approval_request` are emitted removes the capability with
nothing going red — the failure class of §2.3, at its most expensive.

### 8.3 Design key points

- The reader registry is a `map[Intent]Reader` with a declared shape per entry, in **one file**, so a
  reviewer answers "does every intent have depth?" by reading a table rather than a call graph.
- `Subject` is a struct from the start, not a `nodeID string`. The next scoping axis (a run id, a file
  path) is a field, not a signature change across every reader.
- Truncation is a property of the reading, not of the renderer, so the count that was omitted travels
  with the data that was not.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The twelve-item nav is twelve decisions before the first question. P39 removes ten of them. The risk it
introduces is the opposite one: a blank text box is **zero** affordance, and a person who does not know
what to ask learns nothing from an empty page. The Ask surface's four example chips are therefore not
decoration in P39 — they are the navigation, and they SHALL expand to name every reader-backed intent's
question verbatim from the intent table (the `Question` field already exists for exactly this).

🔴 **Terminology.** "Surface" currently means *a console route*. After P39 it means *a thing the agent
can read*. That is a dictionary change with user-visible consequences (refusal copy says "this surface
can do…"), and it SHALL be mirrored into the requirement spec, not just the code.

**Dual-colour decision point:** does the left nav survive at all in P39? Ten of twelve task items leave;
Install / Documentation / Configure / Billing / Organization / Members / Account remain. Recommendation:
yes, keep it — those are out-of-scope surfaces the refusals already redirect to, and deleting them is
not what was asked. Written landing point: §13.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The one-way door in P39 is D2's union. A wire shape published to a customer console is a contract; a
fifth shape added carelessly becomes a shape nobody can remove. Hence four, declared, with a spec delta
required for a fifth.

The reversible part is the deletion itself — routes are recoverable from git, and §12's window makes the
rollback a redirect flip rather than a revert.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

The reader change is read-only, so the usual write-side hazard does not apply. The one that does:
`readAxes` builds the whole projection today and then reduces it to five integers. P39 stops discarding
it. That is a **latency and payload** change on a path that currently ships ~200 bytes, and NFR39-P1's
ceiling is enforced server-side for that reason.

🔴 `conversationreader.go:308` — the hardcoded `/app/variants` — is deleted as part of `compare`'s
`table` shape, not patched. A route reference in prose is the same defect class as an aggregate hiding a
grid: the reader is describing the console instead of answering.

### 9.4 Senior Frontend Dev — *three states stay three; four states stay four*

`messages.tsx` already switches on all eight kinds with no `default:` arm, and the union is generated
from Go. The detail union SHALL be generated the same way, so an unrendered shape is a type error rather
than a blank card.

The eight kinds are unchanged. `proposal`, `approval_request` and `answer` keep their render paths and
keep receiving nothing — 🔴 stated explicitly so nobody deletes them as dead code before P40 needs them.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

Routing is at 100% on 76 questions, and three term weights were tuned after seeing holdout failures.
That is an upper bound, and `author` rests on three labelled questions. P39 adds a new routing
dimension — subject extraction and carry-forward — so the holdout SHALL be extended with:

- node-named questions per reader-backed intent, including a node that does not exist;
- follow-up pairs, where turn 2 is unresolvable alone;
- 🔴 **negative carry-forward**: turn 2 names its own subject and must NOT inherit turn 1's.

Floors stay `MinIntentRecall = 0.80` and `MinAbstentionPrecision = 0.90`, per-intent, no mean.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

Blast radius: every logged-in customer's navigation, in one deploy. Reversible: yes, within §12's
window. Observable: the deleted routes SHALL emit a counted event while the window is open, so the
decision to close it is a number rather than a guess — `console.route.retired_hit`, per route.

Event names `<service>.<area>.<state>`; error codes `UPPER_SNAKE_CASE`; both from
`internal/eventname`, no literals.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

Every fence in this phase SHALL be mutation-verified: the assertion is proven to fail against a
deliberately broken implementation, run with `-count=1`. The specific ones that must be shown red:

1. Delete a reader → the registry fence fails (not just the route fence).
2. Return a declared shape as empty → the emitter refuses the finding.
3. Ask about a non-existent node → refusal, and the assertion matches the **reason**, not just non-200.
4. Exceed the cell ceiling → truncation count present and non-zero.
5. Carry a subject forward without stating it in the `plan` → fails.
6. Reference a deleted `/app/*` route in any emitted message → fails.

🔴 Acceptance is a live event, not a read-only check: a real question against a real linked repository,
followed by reading the emitted messages back out of the store.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

The claims document (`docs/sales/P31-conversational-console-claims.md`) SHALL be updated in the same PR.
The maturity ladder for P39:

| Claim | Rung |
|---|---|
| "Ask about your workflow in English and get the measurement back, with its evidence" | ✅ shipped |
| "Scope a question to one node" | ✅ shipped |
| "No navigation to learn" | 🟡 ten of twelve task pages removed; Studio and Author remain until P40 |
| "The agent changes your configuration" | ⛔ not shipped — P40 |
| "The agent assesses your repository and opens a PR" | ⛔ not shipped — P33/P35 |

🔴 The 🟡 row is the one that must not be rounded up in a deck.

---

## 10. Dependencies

| Depends on | For | Risk if late |
|---|---|---|
| P31 (shipped) | vocabulary, emitter, runner, budget, transport | none |
| P29 linked runs | `run_history` / `compare` table content | those two readers ship shallow; the routes stay |
| P12 forge delivery | `deliver` record content | same |
| P34 | edges for `graph_order` | `graph_order` stays `not_measured` — already true today |
| Type generator (P31) | detail union projected into TypeScript | blocks FR39-4 |

---

## 11. Risks & mitigations

| # | Risk | Level | Mitigation |
|---|---|---|---|
| R1 | The fence is relaxed instead of re-anchored | 🔴 stability | D1 lands, with its mutation drill, **before** any route is deleted. Ordered in `tasks.md`. |
| R2 | A page is deleted whose reader is still shallow | 🔴 UX | FR39-18's list is gated per-row on §6.6's shape shipping; a row without depth does not get deleted. |
| R3 | Payload size regression on large workflows | stability | NFR39-P1 ceiling, enforced in the reader, with a perf assertion using an in-process ruler rather than wall-clock. |
| R4 | Untrusted repository strings reach the DOM as markup | 🔴 security | NFR39-S2 fence, detection disabled. |
| R5 | Carry-forward answers the wrong subject | UX | FR39-15 states it; 9.5's negative carry-forward case fences it. |
| R6 | Live bookmarks break | UX | §12's window plus `console.route.retired_hit`. |
| R7 | `proposal`/`answer` render paths deleted as dead code | maintenance | 9.4 states them as reserved; a comment at each `case` naming P40. |

---

## 12. Rollout & test strategy

**Order is load-bearing.** Wave 1 must land before Wave 3 or R1 materialises.

| Wave | Contents | Exit |
|---|---|---|
| 1 | Reader registry + `BackedByReader` + re-anchored fence, **no routes deleted** | fence mutation-verified red; build green with all 12 routes still mounted |
| 2 | `Subject`, node scoping, detail union + generator, emitter refusal for empty declared shape | drills 2–4 red on mutation |
| 3 | Per-reader depth, §6.6 row by row | each row browser-verified against the page it replaces, **before** that page is deleted |
| 4 | Carry-forward + extended holdout | per-intent floors hold; negative carry-forward red on mutation |
| 5 | Delete the ten routes; nav change; retirement window opens | no emitted message references a deleted route; `console.route.retired_hit` emitting |
| 6 | Close the window after the counter is quiet; docs + claims | routes 404 |

**Retirement window:** 30 days from Wave 5 deploy. During it, a deleted route serves a page that states
the surface has moved into the conversation and pre-fills the Ask box with that surface's question from
the intent table. After it, 404. The close decision reads the counter, not the calendar alone.

**Acceptance** is a live run against a real linked repository (the `nousresearch/hermes-agent` intake
used in P31's evidence), with the four-layer assertion: pre-state, action, post-state, and a read-back
of the emitted messages from the store. HTTP 200 is not evidence.

---

## 13. Open decisions requiring sign-off

| # | Decision | Recommendation |
|---|---|---|
| Q1 | Does the left nav survive P39 with its non-task items (Install, Documentation, Configure, Billing, Organization, Members, Account)? | **Keep.** They are out-of-scope surfaces the refusals redirect to; removing them was not asked for and would break FR39-13. |
| Q2 | Retirement window length — 30 days, or delete immediately? | **30 days.** Level 3 over level 4; the counter makes the close a measurement. |
| Q3 | Do `run_history` / `compare` / `preview_change` / `deliver` ship depth in P39, or stay shallow with their routes retained? | **Ship depth.** Otherwise four of ten deletions do not happen and the phase delivers six. |
| Q4 | Cell ceiling for the `grid` shape | **2,000 cells** with truncation metadata. Covers a 285-node workflow across 7 axes; needs a number, not a principle. |
| Q5 | Does P40 follow immediately, or does Studio/Author sit in a two-surface nav indefinitely? | **Immediately after.** A two-item nav beside a conversation is a worse shape than either endpoint. |

---

## 14. Sign-off

| Role | Confirms | Status |
|---|---|---|
| Product Designer | §9.1 terminology change mirrored to the requirement spec; nav decision Q1 | ⛔ pending |
| System Designer | D1–D6 arbitration; the union in D2 is the only one-way door | ⛔ pending |
| Backend Dev | reader registry shape; NFR39-P1 enforced server-side | ⛔ pending |
| Frontend Dev | detail union generated, no `default:` arm, reserved kinds retained | ⛔ pending |
| AI Engineer | holdout extension covers subject extraction and negative carry-forward | ⛔ pending |
| QA Engineer | all six drills mutation-verified with `-count=1` | ⛔ pending |
| DevOps | retirement counter; event names from the central enum | ⛔ pending |
| Sales Operations | claims ladder, 🟡 row not rounded up | ⛔ pending |
