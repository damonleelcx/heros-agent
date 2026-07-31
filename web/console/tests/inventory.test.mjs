// inventory.test.mjs is §9.1 — `feature-inventory.md` as an executable suite.
//
// # Why this file, and not a PR description
//
// The inventory exists because a reference design demonstrates visual style and is never a complete
// functional spec. Porting against a picture is how a sort control, a tooltip, a keyboard path, a
// second chart series or one empty-state sentence gets deleted — each individually plausible, none
// noticed until a customer comes back for it.
//
// A checklist in a document cannot fail. This file is the same checklist as assertions, so "no feature
// was lost" is a thing the build can disagree with. One test per inventory item, named by its id, so a
// failure names the behaviour that went missing rather than the file it went missing from.
//
// # What these assert, and what they deliberately do not
//
// They assert the ported source still CARRIES the behaviour — the condition, the copy, the handler,
// the attribute. They are regression guards against deletion, which is the failure mode the inventory
// was written for.
//
// They are not a substitute for rendered evidence. §9.4 walks the pages in a browser against a real
// API response, because a green assertion here and a page that renders nothing are entirely
// compatible — a lesson this console has already paid for three times (see tasks.md's verification
// record). Both exist; neither replaces the other.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;

const SRC = {
  configure: "src/app/app/configure/configurator.tsx",
  // P15's declined-change card. It is its own file because the submit path and /preview/p15 render the
  // SAME element — a preview of a re-implementation would check a page no customer sees.
  axisRefusal: "src/components/axisRefusal.tsx",
  wiring: "src/app/app/wiring/page.tsx",
  shell: "src/app/app/layout.tsx",
  transform: "src/app/app/transforms/[configHash]/[sourceRevision]/page.tsx",
  run: "src/app/app/runs/[runId]/page.tsx",
  watch: "src/app/app/runs/[runId]/watch.tsx",
  live: "src/app/app/runs/[runId]/live/liveMonitor.tsx",
  livePage: "src/app/app/runs/[runId]/live/page.tsx",
  graph: "src/app/app/workflows/[workflowId]/graph/page.tsx",
  board: "src/app/app/workflows/[workflowId]/board/page.tsx",
  leaderboard: "src/app/app/workflows/[workflowId]/board/leaderboard.tsx",
  pareto: "src/app/app/workflows/[workflowId]/board/pareto.tsx",
  diff: "src/components/diff.tsx",
  primitives: "src/components/primitives.tsx",
  figure: "src/components/figure.tsx",
  status: "src/lib/status.ts",
  format: "src/lib/format.ts",
  view: "src/lib/view.ts",
  bff: "src/lib/bff.ts",
};

const cache = new Map();
async function source(key) {
  if (!cache.has(key)) cache.set(key, readFile(join(ROOT, SRC[key]), "utf8"));
  return cache.get(key);
}

/**
 * item declares one inventory line as one test.
 *
 * `needles` are all required — an item is present when every part of it is. A string matches
 * literally; a RegExp matches as written. Both forms are here because most behaviours are identifiable
 * by their copy (which must survive verbatim in meaning) while some are identifiable only by a
 * structural pattern.
 */
function item(id, description, key, ...needles) {
  test(`${id} — ${description}`, async () => {
    const text = await source(key);
    for (const needle of needles) {
      if (needle instanceof RegExp) {
        assert.match(text, needle, `${id}: ${SRC[key]} no longer matches ${needle}`);
      } else {
        assert.ok(text.includes(needle), `${id}: ${SRC[key]} no longer contains ${JSON.stringify(needle)}`);
      }
    }
  });
}

// ── 0 · Cross-cutting (X1…X6) ────────────────────────────────────────────────

test("X1 — three failure classes render three distinct messages", async () => {
  const primitives = await source("primitives");
  // Three renderings, not three strings in one box. Collapsing them turns three remedies — mount the
  // subsystem, check the identifier, check the network — into none.
  for (const kind of ["not-mounted", "not-found", "transport", "upstream"]) {
    assert.ok(primitives.includes(`"${kind}"`), `the ${kind} failure class is gone`);
  }
  assert.match(primitives, /not mounted on this deployment/);
  assert.match(primitives, /No such \$\{subject\}|No such subject/);
  assert.match(primitives, /Could not reach the platform/);
  // 🔴 The load-bearing sentence: a 404 is a routing fact, not a measurement.
  assert.match(primitives, /routing fact, not a measurement/);
  assert.match(primitives, /transport failure, not an empty result/);
});

test("X2 — loading, empty and error are three distinct renderings", async () => {
  const primitives = await source("primitives");
  for (const name of ["export function Loading(", "export function Empty(", "export function Failure("]) {
    assert.ok(primitives.includes(name), `${name} is gone — the three states have collapsed`);
  }
  // The skeleton occupies the SHAPE of the content, so arrival changes values and never structure.
  assert.match(primitives, /rows = 3/, "the skeleton no longer takes a shape");
});

test("X3 — empty-state copy is status-dependent, not generic", async () => {
  const live = await source("live");
  assert.ok(live.includes("This run produced no node metrics."));
  assert.ok(live.includes("Run in progress — waiting for the first node."));
  assert.match(live, /monitor\.terminal \?/, "the copy no longer branches on the record's terminal flag");
});

test("X4 — status values are rendered verbatim, never re-derived", async () => {
  const status = await source("status");
  // An unmodelled value renders the RAW string and a distinct treatment. `p25monitor.html` falls back
  // to the `running` style, so an unmodelled state impersonates a known one.
  assert.match(status, /raw/, "the raw value is no longer carried");
  assert.match(status, /known/, "the modelled/unmodelled distinction is gone");
});

test("X5 — values are escaped on render", async () => {
  // React escapes by default; the property is that nothing reaches for the escape hatch.
  for (const key of ["primitives", "leaderboard", "board", "graph", "live", "diff"]) {
    assert.doesNotMatch(await source(key), /dangerouslySetInnerHTML=\{/, `${SRC[key]} renders raw markup`);
  }
});

test("X6 — legacy deep-link entry points resolve to canonical routes", async () => {
  // Each legacy path is a real route that redirects, so a bookmarked URL keeps working.
  for (const rel of ["src/app/p2/route.ts", "src/app/p4/board/route.ts", "src/app/p35/graph/route.ts", "src/app/p25/monitor/route.ts"]) {
    const text = await readFile(join(ROOT, rel), "utf8");
    assert.match(text, /redirect/i, `${rel} no longer redirects`);
  }
});

// ── 1 · /p2 — configure, diff, run (P2-1…27) ─────────────────────────────────

item("P2-1", "four override dimensions, an omission meaning no override", "configure",
  "model_ref", "prompt_ref", "skill_refs", "context_policy", "call site unchanged");
item("P2-2", "spec textarea, spellcheck off and resizable", "configure",
  /spellCheck=\{false\}/, "textarea");
item("P2-3", "variant_id, optional label, seed defaulting to 0", "configure",
  "variant_id", "label", "seed");
item("P2-4", "reset to a working example clears the previous result", "configure",
  "wf-demo", /[Rr]eset/);
item("P2-5", "validate-only reports a parse failure, a server rejection, or a resolved-ref summary", "configure",
  "not valid JSON", "dimension", "no overrides");
item("P2-6", "client-side pre-checks reject an empty variant_id and a bad seed, each with its own message", "configure",
  "A variant id is required", "The seed must be a non-negative integer");
item("P2-7", "🔴 the controls are re-enabled on a transport failure, never left disabled forever", "configure",
  // Found by this suite: neither validate() nor submit() wrapped its fetch, and `busy` is DERIVED from
  // the outcome — so a rejected fetch left every control disabled with no message, permanently.
  "disabled={busy}", "} catch {", "transport failure, not a rejection", "outcome of this submission is unknown");
item("P2-8", "the loading copy explains why submitting is slow", "configure",
  /resolv/i, /compil|transform/i);
item("P2-9", "🔴 the nothing-was-persisted message appears on HTTP 400 ONLY", "configure",
  "Nothing was persisted", /=== 400|status === 400/);
item("P2-10", "build-rejected is its own failure state, generated and reviewed but never run", "configure",
  "build-rejected", /never run|not run|does not build/i);
item("P2-11", "success carries the reader into the transform and the run", "configure",
  "config_hash", "run_id");

// P15 adds an outcome the console did not previously distinguish: a REFUSAL. A 400 that names a
// dimension means the platform read the spec, understood exactly what was asked for, and declined that
// one axis — which is a different thing from a submission that failed, and invites a different next
// step. Rendering it as a generic failure sends the reader to re-submit something that will never be
// accepted; the wiring axis (P15) will produce this outcome for every rearrangement until a source
// rewriter lands, so it is the axis's primary user-facing state, not an edge case.
item("P15-1", "a dimension-named 400 is a refusal with its own state, not a generic failure", "configure",
  /kind: "refused"/, /body\.dimension/, "declined");
item("P15-2", "the refusal names the axis and says nothing was persisted and a retry will not help", "axisRefusal",
  "Declined, not attempted", "Nothing was persisted", /declined again/i);
item("P15-3", "the wiring axis explains why a rearrangement is declined rather than applied as a no-op", "axisRefusal",
  "AXIS_NOTE", "wiring", /no-op/);
item("P15-4", "an axis with no console note still renders the platform's own sentence", "axisRefusal",
  /note \? <p>\{note\}<\/p> : null/, "verbatim");
item("P15-5", "the wiring axis has a surface in the console app, reachable from the navigation", "shell",
  '{ href: "/app/wiring", label: "Wiring"', '"s:wiring"');
item("P15-6", "the wiring surface splits its sections into a real tablist, not a stack", "wiring",
  /<Tabs tabs=\{tabs\}/, /id: "axis"/, /EXAMPLES\.map/);
item("P15-7", "the wiring surface renders the SAME refusal card the submit path renders", "wiring",
  "AxisRefusal", '@/components/axisRefusal');
item("P15-8", "the wiring surface says its examples are not the tenant's data", "wiring",
  /worked examples/i, /this tenant/i);
// 15c — the axis stopped being wholly declined, and the surface has to show that or it teaches the
// wrong thing: a page of four refusals reads as a broken feature however carefully each one is worded.
item("P15-9", "the wiring surface shows an APPLIED reorder, not only declined ones", "wiring",
  "AxisApplied", /id: "applied"/, /An applied reorder/);
item("P15-10", "the applied state carries the engine's REAL diff, not a description of one", "wiring",
  "APPLIED_DIFF", "--- a/wiring.go", "+++ b/wiring.go");
// P16 moved this sentence from the COMPONENT to the CALLER, and the move is the point. The invariant
// was hard-coded to the wiring axis, so the first applied change from another axis (a context window,
// which DELETES list elements) rendered "the file's lines were reordered" over a diff where that is
// simply false. A safety claim asserted on behalf of a change the component knows nothing about is the
// sentence a reviewer stops reading the diff because of. The wiring page still states it — that is what
// this item guards — and `AxisApplied` now REQUIRES each caller to supply its own.
item("P15-11", "the applied card states the permutation invariant that makes the change safe", "wiring",
  "reordered and nothing else changed", /same lines/i);
item("P16-1", "the applied card takes its invariant from the caller rather than asserting one", "axisRefusal",
  "invariant: ReactNode");

item("P2-12", "a transform loads by config_hash + source_revision", "transform",
  "config_hash", "source_revision");
item("P2-13", "header chips including variant_branch when present", "transform",
  "verification_strength", "variant_branch");
item("P2-14", "🔴 requires_human_review is READ from the API, never recomputed", "transform",
  "requires_human_review", /parsed/i);
item("P2-15", "diff colorization by line class", "diff",
  // The class is built from classOf(), so the five kinds live in the function rather than in markup.
  "diff__line--", '"file"', '"hunk"', '"add"', '"del"', '"ctx"');
item("P2-16", "the diff hash footer", "transform", "diff sha256");
item("P2-17", "an empty diff reads as the baseline, not as an error", "transform",
  "baseline, applied unchanged");
item("P2-18", "a build-rejected transform shows the build log", "transform",
  "build-rejected", /build_log|buildlog/);

item("P2-19", "run head chips: status, config, seed, revision", "run",
  "config_hash", "seed", "source_revision");
item("P2-20", "a halted run explains the typed I/O contract violation", "run",
  "typed I/O contract", "halted_reason");
item("P2-21", "node table columns, truncated hashes and an explicit null placeholder", "run",
  "idempotency", "attempt");
item("P2-22", "a node error renders on its own full-width row", "run", "row-error");
item("P2-23", "watch toggles polling and re-labels itself", "watch",
  "Stop watching", /setInterval/);
item("P2-24", "🔴 polling stops on the RUN RECORD's status, not on a node-derived condition", "watch",
  /terminal|status/, /TERMINAL|stop/i);
item("P2-25", "a new watch always stops the previous one", "watch", /clearInterval/);
item("P2-26", "only the first load shows a loading state", "watch", /first|initial/i);

test("P2-27 — variant_commit is recorded as deliberately unrendered, with its reason", async () => {
  // The decision, not the field: an unrendered field is legitimate when it has an owner and a reason.
  const decisions = await readFile(
    join(ROOT, "..", "..", "openspec", "changes", "p9-web-console", "surface-or-drop.md"),
    "utf8",
  );
  assert.match(decisions, /variant_commit/, "the surface-or-drop decision for variant_commit is gone");
});

// ── 2 · /p25/monitor — live run monitor (P25-1…11) ───────────────────────────

item("P25-1", "the run id in monospace", "live", /className="mono">\{monitor\.run_id\}/);
item("P25-2", "an unknown status prints raw and is visually DISTINCT, never impersonating running", "live",
  "stateClass", '"unknown"');
item("P25-3", "the live line: streaming while open, stream closed when terminal", "live",
  "Streaming metrics as they arrive.", "Stream closed.");
item("P25-4", "node columns with their own precisions", "live",
  "Latency", "Cost (USD)", "Prompt tokens", "Completion tokens", "usd5", "integer");
item("P25-5", "per-row state chip PLUS an inset row marker — never colour alone", "live",
  "node-row--", "<Status value={node.state}");
item("P25-6", "the halt note names the node and the reason", "live",
  "monitor.halted", "node_id", "reason");
item("P25-7", "status-dependent empty copy", "live",
  "This run produced no node metrics.", "Run in progress — waiting for the first node.");
item("P25-8", "SSE first, rendering each payload as it arrives, closing on terminal", "live",
  "new EventSource(", "source.onmessage", "source.close()");
item("P25-9", "🔴 polling engages ONLY if no message ever arrived", "live",
  "sawMessage", /if \(sawMessage\.current\)/, "startPolling()");
item("P25-10", "three failure classes on the monitor route too", "livePage", /Failure|outcome/);
item("P25-11", "config_hash is surfaced per its surface-or-drop decision", "live", "monitor.config_hash");

// ── 3 · /p35/graph — pattern-classified graph (P35-1…19) ─────────────────────

item("P35-1", "the meta line: ir version and taxonomy version", "graph",
  "ir_version", "taxonomy_version");
item("P35-2", "the LLM-call counter with correct pluralisation", "graph",
  "llm_calls", 'plural(view.llm_calls, "call", "calls")', "Fully rule-covered");
item("P35-3", "🔴 deterministic layout from layer and order — no heuristic auto-layout", "graph",
  /node\.layer/, /node\.order/);
item("P35-4", "the node box carries id, model and node-scoped label titles", "graph",
  "nodebox__id", "nodebox__model", "nodebox__labels");
item("P35-5", "data vs control edges by dash AND marker, not hue alone", "graph",
  "edge--data", "edge--control", "marker-data", "marker-control");
item("P35-6", "🔴 back edges route UNDER the row so a Reflection loop stays visible", "graph",
  /back/i, /dip|under|below/i);
item("P35-7", "region rectangles beneath the nodes, styled by label source", "graph",
  "region region--${source}", "regions={regions}");
item("P35-8", "region caption with ordinal, title, confidence and candidate", "graph",
  "candidate", "confidence");
item("P35-9", "🔴 the container SCROLLS, never shrinks", "graph", "graph-scroll");
item("P35-10", "a five-entry legend", "graph",
  "legend__swatch--data", "legend__swatch--control", "legend__swatch--rule",
  "legend__swatch--llm", "legend__swatch--none");
item("P35-11", "label cards: ordinal, source badge, title, confidence bar, and border-style difference", "graph",
  "label-row label-row--${label.source}", "confidence");
item("P35-12", "a candidate label explains that structure shows the shape but traces have not confirmed it", "graph",
  /candidate/i, /runtime traces|traces have not/i);
item("P35-13", "the dispatch line, including the no-metric-set case", "graph",
  "dispatches", /no metric-set|no metric set/i);
item("P35-14", "🔴 unclassified explains why, and says unlabelled is not 'no pattern'", "graph",
  /not yet classified|unclassified/i, /no structural signature/i);
item("P35-15", "the whole-workflow empty state repeats the distinction", "graph",
  "That is a state, not an error", "does not mean the workflow implements no patterns");
item("P35-16", "the diagnostics card, hidden unless non-empty", "graph",
  "diagnostics", "diagnostics.length > 0");
item("P35-17", "🔴 an error hides the graph, the label cards AND the diagnostics together", "graph",
  /!outcome\.ok \?/, "<Failure");
item("P35-18", "404 copy distinguishes no-such-workflow from exists-but-unclassified", "graph",
  'subject="workflow"');

test("P35-19 — the unrendered graph fields each have a recorded decision", async () => {
  const decisions = await readFile(
    join(ROOT, "..", "..", "openspec", "changes", "p9-web-console", "surface-or-drop.md"),
    "utf8",
  );
  for (const field of ["symbol", "policy", "tools"]) {
    assert.match(decisions, new RegExp(field), `no surface-or-drop decision for ViewNode.${field}`);
  }
});

// ── 4 · /p4/board — eval board (P4-1…44) ─────────────────────────────────────

test("P4-0 — ⛔ the hardcoded 'wf-demo' board default is NOT ported", async () => {
  // The only behaviour in the inventory deliberately removed. A confident, fully-populated board for a
  // workflow that is not the reader's is strictly worse than an empty state: an empty state tells the
  // truth, a wrong default asserts a falsehood with the full authority of a populated UI.
  const board = await source("board");
  assert.doesNotMatch(board, /"wf-demo"|'wf-demo'/, "the wf-demo default has been re-introduced");
});

item("P4-1", "the header names the workflow and its eval set", "board", "eval_set_hash");
item("P4-2", "the weight-profile selector, and copy saying re-ranking costs nothing", "board",
  "board.profiles", "It enqueues no runs and costs nothing.");
item("P4-3", "the error banner carries the server's own message", "board", /error/);
item("P4-4", "🔴 the all-tie banner: no winner, ranks are ordering not evidence", "board",
  "No winner", /ordering, not evidence|not evidence/);
item("P4-5", "the partial banner with units completed and the provisional note", "board",
  "units_completed", "units_planned", "provisional");
item("P4-6", "each server note renders as its own banner", "board", /notes/);
item("P4-7", "unmeasured variants are EXPLAINED in a collapsed table, not silently absent", "board",
  "Not measured", /reason/i);
item("P4-8", "leaderboard columns with scoped headers", "leaderboard",
  "Variant", "Score", "Gate", "State");
item("P4-9", "🔴 a tied rank renders de-emphasized — a tie does not look like a win", "leaderboard",
  /flags/, "Value");
item("P4-10", "the variant cell carries the short hash with the full hash as a title", "leaderboard",
  "config_hash");
item("P4-11", "the score cell: score ± interval, a scaled CI bar with a mean tick, seeds and cases", "leaderboard",
  "ci_low", "ci_high", "n_seeds", "n_cases", "mean={");

test("P4-11 — and the bar's mean tick is drawn by the shared primitive, from a scaled position", async () => {
  // The bar moved into `primitives.tsx` when the design system was rebuilt: it is the same graphic on
  // the board and anywhere else an interval is shown, and one implementation is the point. The
  // leaderboard's job is to SCALE the three positions against the whole board's range — which is the
  // half that makes two rows comparable — and it is asserted above.
  const primitives = await source("primitives");
  assert.match(primitives, /--mean/, "the mean tick is gone from the interval bar");
  assert.match(primitives, /--left/);
  assert.match(primitives, /--width/);
});

test("P4-12 — the CI bar carries role=img and an aria-label naming score and interval", async () => {
  const primitives = await source("primitives");
  assert.match(primitives, /role="img"/, "the interval bar is not exposed as a graphic");
  assert.match(primitives, /aria-label=\{label\}/, "the interval bar has no text alternative");

  const leaderboard = await source("leaderboard");
  assert.match(
    leaderboard,
    /label=\{`score \$\{score\(row\.score\)\}, interval/,
    "the alternative no longer names the score and its interval",
  );
});
item("P4-13", "the gate cell names the failed gates", "leaderboard", "gate");
item("P4-14", "one chip per state flag, each visually distinct", "leaderboard", "flags");
item("P4-15", "the expandable per-component breakdown", "leaderboard",
  "components", "contribution");
item("P4-16", "an uncalibrated judge is flagged wherever its metric appears", "leaderboard",
  "judge", "uncalibrated");
item("P4-17", "the breakdown also carries penalties, method, the tie line and gate reasons", "leaderboard",
  "penalt", "method", "gate_reasons");
item("P4-18", "🔴 keyboard row navigation with WRAP-AROUND, and Enter/Space toggling", "leaderboard",
  "ArrowDown", "ArrowUp", "preventDefault", /%\s*rows\.length|% total|wrap/i);
item("P4-19", "hover and expanded-row highlighting are distinct", "leaderboard", "lb-row--expanded");
item("P4-20", "🔴 virtualization above 60 rows, with the footer that says why", "leaderboard",
  /60/, "virtualized");
item("P4-21", "the two empty states are distinct", "board",
  "No variant passed every gate.", "No variants on this board yet.");
item("P4-22", "🔴 disqualified variants sit in their own section, not ranked last", "board",
  "Disqualified", "excluded from the ranked order, not ranked last");
item("P4-23", "the quality/cost scatter sizes its markers by latency", "pareto",
  "latency", /r=|radius/);
item("P4-24", "🔴 frontier membership is carried by SHAPE, not only hue", "pareto",
  "mark--frontier", "mark--dominated");
item("P4-25", "the domain is padded so extreme markers are never clipped", "pareto", /pad/i);
item("P4-26", "every point is focusable with an aria-label naming its measures", "pareto",
  'role="img"', "aria-label", "tabIndex");
item("P4-27", "🔴 the tooltip is bound to FOCUS as well as hover", "pareto",
  "onFocus", "onBlur", "onMouseMove", "<Tooltip");

test('P4-27 — and the tooltip itself is announced (role="status")', async () => {
  // The binding is the caller's; the announcement is the primitive's. Both are required, and asserting
  // only one of them is how a keyboard-reachable tooltip ends up silent.
  const figure = await source("figure");
  assert.match(figure, /role="status"/);
  assert.match(figure, /aria-live="polite"/);
});
item("P4-28", "axis labels and min/max ticks on both axes", "pareto", "axis__label", "axis__tick");
item("P4-29", "the legend explains both shapes and the size encoding", "pareto",
  "legend__mark--frontier", "legend__mark--dominated", "latency");
item("P4-30", "the accessible <details> tabular fallback", "figure", "figure__table", "<details");
item("P4-31", "the scatter's own empty state", "board", "No gate-passing variants to plot.");
item("P4-32", "unmeasured coverage says what it costs", "board", /cannot say whether/);
item("P4-33", "the below-threshold banner carries stopped_because", "board", "stopped_because");
item("P4-34", "per-dimension meters with a distinct short/unmet style", "board",
  "meter__bar", "meter__bar--short");
item("P4-35", "🔴 a vacuous dimension reads 'not measurable', never 0% — which would read as failure", "board",
  "vacuous", "not measurable");
item("P4-36", "the coverage stat grid, including the indecisive-oracle caveat", "board",
  "oracle", "diversity");
item("P4-37", "coverage reasons are listed", "board", "reasons");
item("P4-38", "🔴 the residual table, and its framing sentence's meaning", "board",
  "unreachable", "These stay in the denominator.", "Dropping them would raise the percentage by deleting the");
item("P4-39", "the spend table with a total row", "board", "Kind", "Calls", /total/i);
item("P4-40", "🔴 the budget-cap banner presents a stop as CORRECT behaviour", "board",
  "stopped rather than overspending");
item("P4-41", "the spend empty state", "board", "No spend recorded");
item("P4-42", "an errored board hides all sections rather than rendering empty scaffolding", "board",
  "This board could not be computed");
item("P4-43", "an empty board still renders its sections in their own empty states", "board", /empty/);

test("P4-44 — every unrendered board field has a recorded surface-or-drop decision", async () => {
  const decisions = await readFile(
    join(ROOT, "..", "..", "openspec", "changes", "p9-web-console", "surface-or-drop.md"),
    "utf8",
  );
  for (const field of ["gate_set", "seed_floor", "raw_ci_low", "percent_agreement", "composite", "uncovered", "low_confidence"]) {
    assert.match(decisions, new RegExp(field), `no surface-or-drop decision for ${field}`);
  }
});

// ── 5 · index.html — the orphaned approval queue (IDX-1…5) ───────────────────
//
// 🚧 These are requirements for the P5.5 review surface, not behaviours to port literally. The tests
// assert the SHAPE is specified and that the orphan's three defects are recorded as dropped — not that
// the surface ships, which it must not until P5.5 has an API.

test("IDX-1…5 — the review surface's shape is specified, and the orphan's defects are recorded as dropped", async () => {
  const inventory = await readFile(
    join(ROOT, "..", "..", "openspec", "changes", "p9-web-console", "feature-inventory.md"),
    "utf8",
  );
  for (const id of ["IDX-1", "IDX-2", "IDX-3", "IDX-4", "IDX-5"]) {
    assert.match(inventory, new RegExp(`\\*\\*${id}\\.\\*\\*`), `${id} has fallen out of the inventory`);
  }
  // The three deliberate drops, each with a reason — a capability removed by decision, not by omission.
  assert.match(inventory, /Deliberately dropped — Chinese UI strings/);
  assert.match(inventory, /Deliberately dropped — 15 s unconditional polling/);
  assert.match(inventory, /Deliberately dropped — `alert\(\)`/);
});

test("🚧 IDX — the proposal surface does not ship before the P5.5 API exists", async () => {
  // The orphan was created exactly this way: a surface with no backing endpoint. The console's
  // proposal route may exist as a shell, but it must not claim to review what it cannot fetch.
  const page = await readFile(
    join(ROOT, "src/app/app/workflows/[workflowId]/proposals/page.tsx"),
    "utf8",
  );
  assert.match(page, /unverified|not verified|withheld|blocked/i, "the proposal surface has lost its honesty rule");
});
