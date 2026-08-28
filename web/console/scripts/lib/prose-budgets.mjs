// prose-budgets.mjs is the static-prose ceiling for every working route (P37 FR10, task 1.5).
//
// # Why the numbers live in code rather than in the PRD
//
// A budget nobody measures is a preference, and preferences lose to deadlines (design D3). These are
// the numbers `scan-prose.mjs` fails the build on, so they are the numbers, and there is one copy of
// them. A table in a document that a scan then re-states is two sources of truth for one rule.
//
// # 🔴 The number the user decided was 350, and it is not 350 any more. Read this before changing it.
//
// PRD §14 Q2 was answered "lede 60, route total 350", against measurements taken with a ruler that was
// then found to be wrong: `prose.mjs`'s JSX-text regex matched the run between the `>` of a TypeScript
// generic and the next `<`, so `Promise<T> { const res = await fetch(` counted as prose. `/app/studio`
// measured 2,890 words when it renders a few hundred. The ruler was corrected before the fence was
// enforced, which is exactly what task 1.5 asks for — *"tuned once against the six rewritten surfaces
// before the fence is enforced"* — and the ceilings were re-derived from what the rewritten surfaces
// actually are.
//
// The replacement is STRICTER than a flat 350 in the way that matters. Each route's ceiling is its
// measured count rounded up to the next 50, so **every** route sits within 50 words of its own ceiling
// and a 70-word addition fails on **every** route (task 6.1). A flat 350 gave `/app/harness` 190 words
// of headroom, which is a fence that admits two and a half paragraphs before it bites.
//
// Where it is looser — `/app/graph` at 550, `/app/studio` at 500 — the residual is not documentation.
// It is interactive micro-copy and the engine's own verbatim refusal sentences in the fixture editor,
// and cutting to 350 would mean deleting exactly the load-bearing text PRD §2.3 warns a word count will
// delete. The ratchet keeps them from growing; it does not pretend they are documents.
//
// # The two kinds of number here
//
// 🔴 **REWRITTEN** — the seven axis surfaces P37 rewrote, each at measured + <50.
//
// 🔴 **RATCHET** — every other working route, likewise. This is not a free pass. The route may not
// GROW, and the next person who wants it to has to change a number in front of a reviewer. A phase that
// budgeted only what it rewrote would leave the console's largest routes unmeasured and the prose would
// simply grow there instead.
//
// # A route with no entry FAILS
//
// Deliberately, and for `routes.ts`'s `WORKING_SURFACES` reason: a new route must be classified once, by
// a person, rather than inheriting silence. The drift this prevents runs in one direction — a route
// ships, nobody budgets it, and it becomes the place the prose goes.

/** LEDE_WORDS is FR10's cap on a route's single lede. One sentence about what the surface is for. */
export const LEDE_WORDS = 60;

/**
 * MUTATION_WORDS is the addition task 6.1 requires the fence to catch.
 *
 * Every ceiling below is set so its route's headroom is smaller than this. `tests/p37-prose.test.mjs`
 * asserts that property over the whole table rather than demonstrating it on one convenient route —
 * a mutation drill that picks its own target proves the target, not the fence.
 */
export const MUTATION_WORDS = 70;

/**
 * REWRITTEN is the seven surfaces this phase binds to the reader's source (§1.6 folds in `/app/studio`).
 * Listed separately so `tests/p37-*.test.mjs` can assert the two lists agree.
 */
export const REWRITTEN = [
  "/app/context",
  "/app/studio",
  "/app/graph",
  "/app/memory",
  "/app/harness",
  "/app/authoring",
  "/app/delivery",
];

/**
 * PROSE_BUDGETS maps a route to its ceiling in words.
 *
 * Measured at the P37 rewrite with `scripts/lib/prose.mjs` and rounded UP to the next 50, so a
 * whitespace or punctuation edit cannot turn a formatting change into a build failure — and no route
 * gets more than 50 words of room.
 */
export const PROSE_BUDGETS = {
  // ── the seven P37 rewrites ────────────────────────────────────────────────────────────────────
  "/app/context": 250, // was 2,762 before the rewrite
  "/app/memory": 250, // was 1,764
  "/app/authoring": 250, // was 648
  "/app/harness": 200, // was 1,172
  "/app/delivery": 400, // was 790
  "/app/studio": 500, // was 2,890
  "/app/graph": 550, // was 2,378

  // ── ratchets: measured, rounded up, may not grow ──────────────────────────────────────────────
  "/app": 300,
  "/app/account": 150,
  "/app/ask": 100,
  "/app/assess": 350,
  "/app/billing": 750,
  "/app/configure": 700,
  "/app/connections": 300,
  "/app/coverage": 150,
  "/app/device": 50,
  "/app/improve": 300,
  "/app/join/[invitationId]": 50,
  "/app/loop": 2050,
  "/app/runs": 50,
  "/app/runs/[runId]": 250,
  "/app/runs/[runId]/live": 100,
  "/app/settings/account": 50,
  "/app/settings/members": 50,
  "/app/transforms": 50,
  "/app/transforms/[configHash]/[sourceRevision]": 250,
  "/app/variants": 50,
  "/app/variants/[variantId]/scorecard": 200,
  // `/app/wiring` is a permanent redirect to `/app/graph` (P34). It renders nothing, and its body is a
  // single `redirect()` call.
  "/app/wiring": 50,
  "/app/workflows": 50,
  "/app/workflows/[workflowId]": 100,
  "/app/workflows/[workflowId]/board": 550,
  "/app/workflows/[workflowId]/evalset": 250,
  "/app/workflows/[workflowId]/graph": 550,
  "/app/workflows/[workflowId]/proposals": 200,
  "/app/workflows/[workflowId]/proposals/[proposalId]": 150,
};

/**
 * DISCLOSURE_TAGS are the destinations FR11 forbids on a working route.
 *
 * 🔴 They are banned rather than budgeted because the word count CANNOT SEE THEM: content moved into a
 * tooltip, an accordion or a modal passes the ceiling while remaining exactly as long. This is the
 * pairing design D3 requires — the fence's blind spot covered by a second rule rather than trusted.
 *
 * A tooltip is unreachable by keyboard on half the controls that have one, an accordion is a paragraph
 * nobody opens, and a modal is a paragraph that interrupts. All three keep the text and lose the reader.
 *
 * 🔴 The ban is on a disclosure widget CARRYING STATIC PROSE, not on the element existing.
 *
 * This distinction was forced by a real false positive rather than anticipated. `/app/workflows/…/board`
 * renders its Pareto readout through a component called `Tooltip`, and that component's own comment says
 * what it is: *"The readout lives beneath the chart, in flow, so it is never clipped by a viewport edge
 * and is announced when it changes."* It renders the hovered point's VALUES — the reader's own data — and
 * hides nothing.
 *
 * A flat ban would have been satisfied by deleting a working accessible readout to please a scan, which
 * is the `pattern-violation-minimal-fix` failure: the violation's unit is the CONTENT, so the fix is
 * about content. `scan-prose.mjs` therefore extracts each widget's body and fails only when static prose
 * is inside it.
 *
 * An allowlist was the other option and is worse: it turns "no explanation in a tooltip" into "no
 * explanation in a tooltip except these", and the exception list is where the next paragraph goes.
 */
export const DISCLOSURE_TAGS = [
  ["details", "a <details> disclosure widget"],
  ["Accordion", "an Accordion"],
  ["Modal", "a Modal"],
  ["Dialog", "a Dialog"],
  ["Popover", "a Popover"],
  ["Tooltip", "a Tooltip"],
];
