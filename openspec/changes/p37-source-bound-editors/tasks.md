# Tasks — P37: Source-Bound Editors

> **Nothing here is implemented.** This change set is documents only, as the whole GEHA program is.
> Every box is unchecked and stays unchecked until the code lands.

## 1. Product Designer — decide what moves before anything is written

- [ ] 1.1 Walk all six axis surfaces and classify **every** static block as `varies-with-reader` or `same-for-every-reader`, one line per block, in a table reviewed before any code changes.
- [ ] 1.2 For each `same-for-every-reader` block, name its destination section on the reading surface. A block with no destination is not cut.
- [ ] 1.3 Enumerate the protected text per surface — refusal cause, `not_measured` + named input, stated boundary, `unverified` stamp, disabled-with-reason option — as the input to QA fence 6.3.
- [ ] 1.4 Write the `not_connected` copy: names the missing input, links to the connection flow **and** to the reading surface.
- [ ] 1.5 Decide the prose budget numbers (PRD §14 Q2) against the six real surfaces before the fence is enforced.
- [ ] 1.6 Confirm with the user whether `/app/studio` is in scope (PRD §14 Q4) after inspecting what it already does.

## 2. System Designer — the subject, and the one conflict

- [ ] 2.1 Define the subject as a type in one place — `(workflow_id, node_id)` — and record why both are carried (PRD §14 Q1).
- [ ] 2.2 Record the resolution order: explicit selection → most recently reported workflow's sole node → ambiguous, ask.
- [ ] 2.3 Reconcile with `axis-node-projection`'s folded requirement *"The worked examples on each axis surface SHALL be retained"*: this change modifies it to require a **named destination**, not the same page. Restate the header verbatim in the delta.
- [ ] 2.4 Confirm no new table is required, and record the subject selection as per-person UI state (PRD §14 Q5).
- [ ] 2.5 Record `not_connected` as a fourth state alongside the three `loadProjection` already distinguishes, and why it is a 200 and not a 404.

## 3. Frontend Dev — the kit, then the surfaces

- [ ] 3.1 Extract the editor kit from `/app/memory`'s authoring panel: picker, params form, validate-at-save, preflight, `unverified` stamp. Preserve its three rules — boundary above the choice, control live rather than disabled, refusal never rendered as success.
- [ ] 3.2 Params form derives its fields from the selected entry's `ParamsSchema`. No hand-written field lists.
- [ ] 3.3 Picker binds to the axis's closed vocabulary **at its recorded set version**; an unavailable option renders disabled with the service it needs, never hidden.
- [ ] 3.4 Subject resolver in the shell; the resolved node is named on screen even when there was only one candidate.
- [ ] 3.5 `not_connected` rendered on every axis surface, with no sample node anywhere in the reader's data position.
- [ ] 3.6 Rewrite surfaces in this order, one at a time: `context`, `harness`, `memory`, `wiring`, `authoring`, `delivery`.
- [ ] 3.7 Axis state rendered from the shared four-valued vocabulary (`measured` / `observed` / `not_measured` / `refused`); no per-surface state words.
- [ ] 3.8 A `finding` link target: each axis surface accepts a subject in its URL and pre-selects it.
- [ ] 3.9 No new colour / spacing / type / radius literals; `npm run scan:tokens` stays green. Hazard palette on `refused` only.
- [ ] 3.10 Every picker is a labelled form control reachable by keyboard; validation errors are associated with their fields, not announced as a page banner.
- [ ] 3.11 `Intl` through the single `en-US` swap point; no new formatting call sites.

## 4. Frontend Dev — the reading surface, first

- [ ] 4.1 Author the destination documents **before** any text is moved, with tables of contents.
- [ ] 4.2 Move the classified blocks into them, edited into documents rather than appended in cut order.
- [ ] 4.3 Worked examples land here, labelled as the platform's fixture, never as the reader's data.
- [ ] 4.4 Every working surface links to its destination section — one link per section, no tooltips, accordions or modals as destinations.
- [ ] 4.5 PR body enumerates every moved block and its destination. A block with no destination fails review.

## 5. Backend Dev — reads, and one write path

- [ ] 5.1 Per-node axis read: current policy / strategy / loop for the subject, resolved from the IR.
- [ ] 5.2 Live per-node coverage read replacing the transcription on `/app/context`.
- [ ] 5.3 An unresolvable node policy is `not_measured` with a named missing input — **never** a silent fallback to a default, which would show the reader a policy their node does not have.
- [ ] 5.4 Save path: registry entry + variant, with the resulting `config_hash` returned for the preflight.
- [ ] 5.5 Every WARN/ERROR on these paths carries `request_id` / `trace_id` / `span_id`.
- [ ] 5.6 Central event names — `console.subject.resolved`, `console.subject.ambiguous`, `console.axis.saved`, `console.axis.save_refused` — in the central enum, no literals.

## 6. QA — fences that can go red

- [ ] 6.1 Static-prose budget fence over the built working routes. **Add 70 words to a route; the build must fail.**
- [ ] 6.2 No-fixture fence: render every axis surface unconnected and assert the reader's data position contains no sample node. **Mutate to render one; the test must fail.**
- [ ] 6.3 Refusal-survives fence: drive a **real** engine refusal through each rewritten surface and assert the cause text appears verbatim. **Mutate the renderer to paraphrase; the test must fail.**
- [ ] 6.4 `not_measured` renders with its named missing input on every axis, after the rewrite.
- [ ] 6.5 Stated boundary renders **above** its control, per axis that has one.
- [ ] 6.6 Save writes: HTTP 200 → `SELECT` the registry row → `SELECT` the variant row → assert the surface renders the new `config_hash`. A 200 is not evidence of a write.
- [ ] 6.7 Subject persists across all six surfaces and is visible on each.
- [ ] 6.8 Axis state is per node: fixture with one uncovered node among many; assert it is visible and not averaged away.
- [ ] 6.9 An unavailable option renders disabled with its required service, per axis that has one.
- [ ] 6.10 Every reading-surface destination link resolves. **Break one; the test must fail.**
- [ ] 6.11 Browser acceptance for A1 — on a connected repository, a person changes their own node's memory strategy and reads their own diff. A green build is not acceptance.

## 7. DevOps

- [ ] 7.1 Subject-resolver outcome counts (`resolved`, `ambiguous`, `not_connected`, `read-failed`, `not-mounted`) exposed on a readable health endpoint, not only in logs.
- [ ] 7.2 Reading-surface destinations deploy before the surfaces that link to them, in every topology.
- [ ] 7.3 Each surface behind its own independently revertible change, so a regression is bisectable.

## 8. Sales Operations

- [ ] 8.1 The boundary copy for axes that can be authored but not written into source is reviewed as a **customer-facing commitment**, not as layout text.
- [ ] 8.2 Noun dictionary: `node`, `axis`, `policy`, `strategy`, `variant`, `config_hash` defined once on the reading surface and used unchanged on all six surfaces.
- [ ] 8.3 Nothing in the shortened copy promises an axis can reach source when it cannot.

## 9. Sign-off

- [ ] 9.1 PRD §14 Q1–Q6 answered and folded into this change set.
- [ ] 9.2 The `axis-node-projection` modification (2.3) reviewed by whoever signed off P29, because it changes a requirement that phase wrote to protect against exactly this kind of rewrite.
- [ ] 9.3 The fence removal in 5.2 reviewed on its own, separately from the surface rewrite that motivates it.
