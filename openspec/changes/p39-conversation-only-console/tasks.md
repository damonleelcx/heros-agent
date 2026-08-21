# Tasks — P39: The Conversation-Only Console

> **Status: 0 of 46 done.** Nothing is implemented. This is the plan, awaiting sign-off on PRD §13.
>
> 🔴 **Wave order is load-bearing, not preference.** Wave 1 re-anchors the drift fence. If any route is
> deleted before it lands and is proven able to go red, the cheapest repair for the resulting failure is
> to delete the intents alongside the routes — which restores green by removing the protection.
> **No box in Wave 5 may be checked while any box in Wave 1 is open.**
>
> A box is checked only when the code, its fence, and (for UI) a browser acceptance have landed and the
> section's tests are green. Every drill below is run with `-count=1`: a cached PASS reports a fence as
> proven when it was never executed.

---

## Wave 1 — Re-anchor the fence. Delete nothing.

- [ ] 1.1 Add `BackedByReader` to `Backing` in `internal/conversation/intent.go`; leave all fourteen intents on their current backing.
- [ ] 1.2 Add the reader registry: `map[Intent]ReaderEntry`, one file, each entry naming a reader and its declared `DetailShape`. One table a reviewer can read top to bottom.
- [ ] 1.3 Add `TestEveryReaderBackedIntentHasARegisteredReader` and `TestEveryRegisteredReaderIsNamedByAnIntent`.
- [ ] 1.4 🔴 **Drill:** delete one registry entry → both assertions red, naming the intent. Delete one intent → red, naming the reader. Register a reader with no declared shape → red. Three mutations, three reds, `-count=1`, and each mutation must **compile** — a build failure is `exit != 0` for the wrong reason.
- [ ] 1.5 Narrow `TestIntentSetEqualsTheWorkingSurfaceSet` to route-backed intents only, and assert in the same test that the reader-backed set is non-empty — so the narrowing cannot later be widened into vacuity.
- [ ] 1.6 Move the ten read-only intents to `BackedByReader`, naming reader keys. **All twelve routes remain mounted and linked.** Build green.
- [ ] 1.7 Confirm `TestEveryIntentIsBackedByExactlyOneThing` still holds across three backings, and that a capability-backed intent still cannot carry an `/app/` surface.

## Wave 2 — Subject and the detail union. Still delete nothing.

- [ ] 2.1 Add `Subject{NodeID string}`; change `SurfaceReader.Read` to take it. Every existing reader compiles and ignores it.
- [ ] 2.2 Add `DetailShape` (`grid`, `table`, `diffstat`, `record`) and the `Detail` field on `SurfaceReading` and `FindingPayload`.
- [ ] 2.3 Extend the Go→TypeScript generator to project the detail union. Console `switch` over shapes with **no `default:` arm**.
- [ ] 2.4 Emitter: refuse a finding whose reader declared a shape and whose reading carries none. Reuse the evidence-less refusal path — 🚫 do not add a second refusal mechanism.
- [ ] 2.5 Emitter: refuse a truncated payload carrying no omitted count.
- [ ] 2.6 Router: extract a node identifier from the question. Refuse-by-name when the IR does not contain it; list the identifiers that do exist.
- [ ] 2.7 🔴 **Drill:** (a) declared shape returned empty-of-payload → emitter refuses; (b) zero-cell grid → emitted, NOT refused; (c) unknown node → refusal whose assertion matches the **reason**, not merely a non-200; (d) truncation without a count → refused. Four reds, `-count=1`.
- [ ] 2.8 🔴 **Drill:** add a shape server-side without the console union member → console type-check fails and no artifact is produced.
- [ ] 2.9 Cell ceiling (PRD Q4: 2,000) enforced in the reader before serialisation, with an in-process ruler for the perf assertion — 🚫 never a wall-clock budget on CI, which measures the runner.

## Wave 3 — Depth, reader by reader. Each row is gated on its own browser comparison.

> 🔴 A row here is checked only after the returned detail has been compared **in a browser** against the
> page it replaces, with that page still mounted. This is a human comparison; no fence substitutes.

- [ ] 3.1 `coverage` → `grid`: every (node × axis) cell with state and cause.
- [ ] 3.2 `context`, `memory`, `harness` → `grid`, that intent's axes only.
- [ ] 3.3 `prompt_model`, `author` → `grid`. (Their routes survive this phase; the depth does not wait for P40.)
- [ ] 3.4 `graph` → `record`: node count, revision, per-language counts, node identifiers.
- [ ] 3.5 `graph_order` → `record`, still `not_measured`. 🔴 Unchanged on purpose: the IR carries nodes and no edges, and inferring order from array position is a confident wrong answer about the customer's code. P34 owns the fix.
- [ ] 3.6 `run_history` → `table`: one row per linked run — id, workflow, revision, when, headline numbers.
- [ ] 3.7 `compare` → `table`: two runs side by side with per-metric delta. **Delete the hardcoded `/app/variants` string at `internal/api/conversationreader.go:308`** — it is not patched, it is removed with the shallow claim it belonged to.
- [ ] 3.8 `preview_change` → `diffstat`: per-node outcome plus the diffstat for one `(config_hash, source_revision)`.
- [ ] 3.9 `deliver` → `record`: route condition, target, last delivery outcome.
- [ ] 3.10 Four console renderers (`grid`, `table`, `diffstat`, `record`). Real `<table>` with header scope; state never conveyed by colour alone; all copy through the existing i18n point; all colour through project CSS variables.
- [ ] 3.11 🔴 **No `@apply` of a project class** — Tailwind v4 compiles it to nothing and drops the surrounding `@layer`, leaving a green build and an unstyled product. The `scan-tokens.mjs` rule added in P31 covers this; confirm it runs on the new stylesheets.
- [ ] 3.12 Evidence reference renders as a copyable identifier, not a link.

## Wave 4 — Carry-forward.

- [ ] 4.1 Thread the prior turn's resolved `(intent, subject)` into `Router.Route`.
- [ ] 4.2 Use it only when the question resolves an intent but no subject. State the carried subject and its source turn in the `plan` payload.
- [ ] 4.3 Abstain — no run started — when a question depends on an earlier turn and none exists.
- [ ] 4.4 Do not carry across conversations; do not use a pin whose subject differs.
- [ ] 4.5 Extend the holdout with node-named questions per reader-backed intent (including a non-existent node), follow-up pairs, and 🔴 **negative carry-forward** cases where turn 2 names its own subject and must not inherit.
- [ ] 4.6 Per-intent floors hold (`MinIntentRecall` 0.80, `MinAbstentionPrecision` 0.90). 🚫 No mean reported.
- [ ] 4.7 🔴 Record in the spike document which term weights, if any, were tuned after seeing holdout failures. Tuned weights make the reported recall an upper bound, and that must be stated where the number is read.
- [ ] 4.8 Update the Ask page's standing copy: it currently promises no memory between conversations, absolutely. It becomes conditional. 🔴 A product that contradicts its own printed statement is worse than one with less capability.

## Wave 5 — Delete. Only now.

- [ ] 5.1 Verify every Wave 1–4 box is checked. 🔴 This is the gate, written as a task so skipping it is visible.
- [ ] 5.2 Delete the ten route trees: `/app/workflows` (list, `[id]`, `graph`, `board`, `evalset`), `/app/runs` (incl. `[runId]`, `live`), `/app/variants` (incl. scorecard), `/app/transforms` (incl. `[configHash]/[sourceRevision]`), `/app/delivery`, `/app/wiring`, `/app/context`, `/app/memory`, `/app/harness`, `/app/coverage`.
- [ ] 5.3 🔴 **Retained, not deleted:** `/app/studio`, `/app/authoring`, `/app/workflows/[workflowId]/proposals`, `.../proposals/[proposalId]`. Leave a comment at each naming P40 as the phase that removes them.
- [ ] 5.4 Narrow `WORKING_SURFACES` in `web/console/src/lib/routes.ts` to the surviving routes.
- [ ] 5.5 Remove the ten nav entries. Keep Install / Documentation / Configure / Billing / Organization / Members / Account (PRD Q1).
- [ ] 5.6 Expand the Ask page's example chips to every reader-backed intent's `Question`, verbatim from the intent table. 🔴 With the nav gone these chips *are* the navigation — a blank text box is zero affordance.
- [ ] 5.7 Assertion: no emitted message carries a path matching the removed set. Extend `TestEveryOutOfScopeRedirectionNamesARealSurface` to the removed set.
- [ ] 5.8 🔴 **Drill:** reintroduce a `/app/coverage` href in a message payload → red.
- [ ] 5.9 Retirement window: a removed route serves a page naming its surface, saying it moved into the conversation, and pre-filling the Ask box with that surface's `Question`. Emit `console.route.retired_hit` per route, from `internal/eventname` — 🚫 no string literals.
- [ ] 5.10 Retained render paths for `proposal`, `approval_request`, `answer` confirmed present and reachable by type, with a comment naming P40.

## Wave 6 — Close the window, and the documents.

- [ ] 6.1 Read `console.route.retired_hit` after 30 days. Close on the counter going quiet, not on the date alone.
- [ ] 6.2 Remove the retirement pages; the routes 404.
- [ ] 6.3 Update `docs/sales/P31-conversational-console-claims.md` with the P39 ladder. 🔴 "No navigation to learn" is 🟡, not ✅ — Studio and Author remain. It must not be rounded up.
- [ ] 6.4 Mirror the terminology change into the requirement spec: "surface" stops meaning *a console route* and starts meaning *a thing the agent can read*. It is user-visible — refusal copy says "this surface can do…".
- [ ] 6.5 Write `docs/release/p39-evidence.md`: a live run against a real linked repository with the four-layer assertion (pre-state, action, post-state, read-back of emitted messages from the store). 🚫 HTTP 200 is not evidence of a write.
- [ ] 6.6 Fold this delta into `openspec/specs/conversational-console/spec.md` and archive the change.

---

## Acceptance

🔴 Live, not read-only. A real question against a real linked repository — the `nousresearch/hermes-agent`
intake used in P31's evidence — with messages read back out of the store, not merely observed streaming.

## Explicitly out of scope

| Not here | Owner |
|---|---|
| `proposal` / `approval_request` / prose `answer` emitted in production | P40 |
| Deleting `/app/studio`, `/app/authoring`, the proposal routes | P40 |
| `assess` / `improve` becoming mounted | P33 / P35 |
| Edges so `graph_order` can be measured | P34 |
| A model-based router | out of program — the pin requires determinism before spend |
