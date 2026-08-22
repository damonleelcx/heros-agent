// conversation.test.mjs is P31's console-side fence set (§6.5, §6.9, §6.10, §6.15, §7.4).
//
// # What it can prove without a browser, and what it deliberately leaves to one
//
// It proves STRUCTURE: that the generated union carries every Go kind, that every (kind × state) pair
// has a renderer and a copy string, that the three failure classes stay three, and that the intent
// table and the route table are the same set. Those are properties of the source, and reading the
// source is the only way to catch the ones that fail silently.
//
// It does NOT prove that a question reaches an answer. That is §6.11, it needs a real browser against a
// real platform, and `scripts/p31-acceptance.mjs` is where it lives — because a green build is not
// acceptance, and this file passing while the surface is blank is exactly the outcome that rule exists
// to prevent.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import { WORKING_SURFACES, OUT_OF_SCOPE_SURFACES, SHELL_SURFACES, routes } from "../src/lib/routes.ts";
import {
  FAILURE_COPY,
  FINDING_STATE_COPY,
  PHASE_COPY,
  STEP_STATE_COPY,
  STOP_COPY,
  unapprovableCopy,
} from "../src/lib/conversationCopy.ts";

const CONSOLE_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const REPO_ROOT = join(CONSOLE_ROOT, "..", "..");

/** goVocabulary reads a closed set out of its Go accessor's backing slice. */
async function goVocabulary(file, sliceName) {
  const src = await readFile(join(REPO_ROOT, file), "utf8");
  const start = src.indexOf(`var ${sliceName} = `);
  assert.ok(start >= 0, `${sliceName} was not found in ${file} — this fence's scan is broken, not the code`);
  const block = src.slice(start, src.indexOf("}", start));
  return [...block.matchAll(/\b(Kind|Provenance|Phase|Step|Finding|Failure|Stop)[A-Za-z]+\b/g)].map(([m]) => m);
}

/** unionMembers reads a generated string-literal union's members out of types.generated.ts. */
async function unionMembers(name) {
  const src = await readFile(join(CONSOLE_ROOT, "src", "lib", "types.generated.ts"), "utf8");
  const start = src.indexOf(`export type ${name} =`);
  assert.ok(start >= 0, `the generated contract has no union called ${name}`);
  const end = src.indexOf(";", start);
  return [...src.slice(start, end).matchAll(/"([^"]+)"/g)].map(([, m]) => m);
}

// ── §6.5 · a Go kind with no TSX union member fails the build ────────────────────────────────────

/**
 * 🔴 This is the fence, and the mechanism is worth stating because the test looks weaker than it is.
 *
 * The union is GENERATED from `conversation.Kinds()`, so it cannot drift by hand — `make
 * console-types-check` fails when the checked-in artifact and the Go vocabulary disagree. What THIS
 * asserts is the other half: that the members the browser has are exactly the members Go declares, so a
 * regeneration that silently dropped one is caught here rather than at run time as a blank card.
 *
 * The renderer's exhaustiveness is enforced by `tsc`: `MessageCard`'s switch has no `default:` arm, so
 * a new member with no case is a type error. That is asserted below by reading the source, because a
 * `default: return null` added in a hurry would restore the silent gap and nothing else would notice.
 */
test("§6.5 every message kind Go declares has a union member and a renderer", async () => {
  const goKinds = await goVocabulary("internal/conversation/vocabulary.go", "kinds");
  const union = await unionMembers("ConversationKind");
  assert.equal(
    union.length,
    goKinds.length,
    `Go declares ${goKinds.length} message kinds and the generated union carries ${union.length}. ` +
      "Run `make console-types`. A kind present in Go and absent from the union renders as NOTHING — " +
      "a blank card in a transcript is indistinguishable from a message that was never sent.",
  );

  const renderer = await readFile(join(CONSOLE_ROOT, "src", "components", "conversation", "messages.tsx"), "utf8");
  for (const kind of union) {
    assert.ok(
      renderer.includes(`case "${kind}":`),
      `no renderer for the "${kind}" message kind in messages.tsx`,
    );
  }
});

test("§6.5 the message switch has no default arm, so a new kind is a type error", async () => {
  const renderer = await readFile(join(CONSOLE_ROOT, "src", "components", "conversation", "messages.tsx"), "utf8");
  assert.ok(
    !/\n\s*default:/.test(renderer),
    "messages.tsx has a `default:` arm. It makes the switch exhaustive to the type-checker while " +
      "swallowing any kind added later — which converts a red build into a silent gap in the transcript, " +
      "and is precisely the failure task 4.5 is about.",
  );
});

// ── §6.9 · one case per (message kind × state) renders ───────────────────────────────────────────

test("§6.9 every finding state has copy and a distinct treatment", async () => {
  const states = await unionMembers("ConversationFindingState");
  assert.equal(states.length, 4, `there are ${states.length} finding states; the console renders four`);

  const css = await readFile(join(CONSOLE_ROOT, "src", "app", "globals.css"), "utf8");
  const renderer = await readFile(join(CONSOLE_ROOT, "src", "components", "conversation", "messages.tsx"), "utf8");
  for (const state of states) {
    assert.ok(FINDING_STATE_COPY[state], `no copy for the "${state}" finding state`);
    assert.ok(FINDING_STATE_COPY[state].label, `the "${state}" finding state has an empty label`);
    // 🔴 A DISTINCT rule, not a shared one with a variable colour. Four states that differ only by hue
    // are one state to a colour-blind reader, in greyscale, and to anybody scanning quickly.
    assert.ok(
      css.includes(`.finding__state--${state}`),
      `the "${state}" finding state has no treatment of its own in globals.css`,
    );
    assert.ok(renderer.includes(state), `messages.tsx never mentions the "${state}" state`);
  }
  // `measured` is the only state whose lede is empty — the other three each say what is missing, what
  // the lower layer said, or which revision the claim describes.
  for (const state of states.filter((s) => s !== "measured")) {
    assert.ok(
      FINDING_STATE_COPY[state].lede,
      `the "${state}" finding state has no lede. A state label with nothing after it is the omission ` +
        "problem with a label on it.",
    );
  }
});

test("§6.9 every step state has copy, and only `done` needs no reason", async () => {
  const states = await unionMembers("ConversationStepState");
  for (const state of states) {
    assert.ok(STEP_STATE_COPY[state], `no copy for the "${state}" step state`);
    assert.equal(
      STEP_STATE_COPY[state].needsReason,
      state !== "done",
      `"${state}" disagrees with the server about whether it must name a reason`,
    );
  }
});

test("§6.9 every phase has copy", async () => {
  for (const phase of await unionMembers("ConversationPhase")) {
    assert.ok(PHASE_COPY[phase]?.label, `no copy for the "${phase}" phase`);
    assert.ok(PHASE_COPY[phase]?.detail, `the "${phase}" phase says nothing about what is happening`);
  }
});

/**
 * §7.5 — a budget-stopped run reads as a STATE WITH A NEXT ACTION.
 *
 * 🔴 The assertion is on `next`, not on the label. A stop reason with a name and no next action is the
 * sentence that makes a person wonder whether the product is broken — and it is what this table would
 * degrade to, one limit at a time, if nothing checked.
 */
test("§7.5 every stop reason has copy, and every LIMIT names a next action", async () => {
  const reasons = await unionMembers("StopReason");
  const limits = new Set(["ceiling", "token-budget", "tool-call-ceiling", "wall-clock"]);
  for (const reason of reasons) {
    const copy = STOP_COPY[reason];
    assert.ok(copy, `no copy for the "${reason}" stop reason`);
    assert.ok(copy.label && copy.body, `the "${reason}" stop reason has an incomplete entry`);
    if (limits.has(reason)) {
      assert.ok(
        copy.next,
        `the "${reason}" limit names no next action. It is neither a failure nor a completion, and ` +
          "copy that omits the next action leaves a reader with nowhere to go.",
      );
    }
  }
  // Task 4.13: a run that finished normally SAYS so rather than being the absence of a limit.
  assert.ok(STOP_COPY.satisfied.body, "a satisfied run says nothing about having finished");
});

// ── §6.10 · 503 / 404 / transport render as three distinct messages ──────────────────────────────

test("§6.10 the three failure classes have three labels and three different next actions", async () => {
  const classes = await unionMembers("ConversationFailureClass");
  assert.equal(classes.length, 3, "the three failure classes became " + classes.length);

  const labels = new Set();
  const nexts = new Set();
  for (const kind of classes) {
    const copy = FAILURE_COPY[kind];
    assert.ok(copy, `no copy for the "${kind}" failure class`);
    labels.add(copy.label);
    nexts.add(copy.next);
  }
  assert.equal(labels.size, 3, "two failure classes share a label");
  // The real test of this table: the three NEXT ACTIONS are three different things to do — mount
  // something, check an identifier, retry. A person handed the wrong one spends an afternoon on the
  // wrong question.
  assert.equal(nexts.size, 3, "two failure classes tell the reader to do the same thing");

  const renderer = await readFile(join(CONSOLE_ROOT, "src", "components", "conversation", "messages.tsx"), "utf8");
  for (const kind of classes) {
    assert.ok(renderer.includes(kind), `the refusal renderer never mentions the "${kind}" class`);
  }
});

// ── §6.15 · the intent set IS the working-surface set ────────────────────────────────────────────

/** goIntentSurfaces reads the route-backed surfaces out of the Go intent table. */
async function goIntentSurfaces() {
  const src = await readFile(join(REPO_ROOT, "internal", "conversation", "intent.go"), "utf8");
  const start = src.indexOf("var intents = []IntentSpec{");
  assert.ok(start >= 0, "the intent table was not found — this fence's scan is broken, not the code");
  const block = src.slice(start, src.indexOf("\n}", start));
  return [...block.matchAll(/BackedByRoute,\s*"(\/app\/[^"]*)"/g)].map(([, s]) => s);
}

test("§6.15 every intent names a working surface, and every working surface has an intent", async () => {
  const fromGo = (await goIntentSurfaces()).slice().sort();
  const fromConsole = WORKING_SURFACES.slice().sort();
  assert.deepEqual(
    fromGo,
    fromConsole,
    "the intent table and the console's working-surface list are not the same set.\n" +
      "🔴 This drifts in ONE DIRECTION: a surface ships, nobody adds its intent, and the conversation " +
      "quietly cannot reach it. Nothing fails — the user asks and gets a REFUSAL, which is well-formed, " +
      "polite, and indistinguishable from the surface not existing.\n" +
      `  intents:  ${fromGo.join(", ")}\n  surfaces: ${fromConsole.join(", ")}`,
  );
});

/**
 * 🔴 This is the fence that makes the one above impossible to satisfy by omission.
 *
 * Without it, adding a route and listing it NOWHERE passes: the two sets above stay equal, and the new
 * surface is simply unreachable by sentence. So every canonical route must be classified exactly once —
 * working surface, out-of-scope redirection, or shell — and adding one forces the decision.
 */
test("§6.15 every canonical route is classified exactly once", async () => {
  const all = new Set();
  for (const build of Object.values(routes)) {
    // Every route builder takes zero, one or two subject arguments. Called with placeholders, then
    // reduced to its base path — the classification is about SURFACES, not about subjects.
    const path = build("x", "y").split("?")[0];
    const base = path.startsWith("/app/workflows/") ? "/app/workflows" : path.replace(/\/x(\/.*)?$/, "").replace(/\/x\/y$/, "");
    all.add(base.replace(/\/(x|y)(\/|$)/g, ""));
  }
  const classified = new Set([...WORKING_SURFACES, ...OUT_OF_SCOPE_SURFACES, ...SHELL_SURFACES]);
  const unclassified = [...all].filter((r) => !classified.has(r)).sort();
  assert.deepEqual(
    unclassified,
    [],
    "these canonical routes are in no bucket. Add each to WORKING_SURFACES (a person can ask for it), " +
      "OUT_OF_SCOPE_SURFACES (they can ask ABOUT it and the agent will not do it), or SHELL_SURFACES " +
      "(neither):\n  " + unclassified.join("\n  "),
  );

  const buckets = [WORKING_SURFACES, OUT_OF_SCOPE_SURFACES, SHELL_SURFACES];
  for (let i = 0; i < buckets.length; i += 1) {
    for (let j = i + 1; j < buckets.length; j += 1) {
      const both = buckets[i].filter((r) => buckets[j].includes(r));
      assert.deepEqual(both, [], `these routes are in two buckets at once: ${both.join(", ")}`);
    }
  }
});

test("§6.15 the out-of-scope surfaces the router names exist in the console", async () => {
  const src = await readFile(join(REPO_ROOT, "internal", "conversation", "intent.go"), "utf8");
  const start = src.indexOf("var outOfScope = []OutOfScopeTopic{");
  const block = src.slice(start, src.indexOf("\n}", start));
  const named = new Set([...block.matchAll(/"(\/app\/[^"]*)"/g)].map(([, s]) => s));
  for (const surface of named) {
    assert.ok(
      OUT_OF_SCOPE_SURFACES.includes(surface),
      `the router redirects to ${surface}, which the console does not list as an out-of-scope surface. ` +
        "A refusal that names a route nobody serves sends a person to a 404.",
    );
  }
});

// ── §7.4 · the noun dictionary ───────────────────────────────────────────────────────────────────

/**
 * The surface uses `workflow`, `node`, `axis`, `proposal`, `run` exactly as the rest of the product
 * does. This checks the near-synonyms a chat surface invites, because prose makes a synonym feel like
 * variety rather than like a second name for one thing.
 *
 * 🔴 The check is on the CONSOLE'S OWN COPY only — the platform's verbatim causes pass through this
 * surface untouched, and re-wording them would be the FR15 defect. So the scan reads the copy module
 * and the components, and deliberately not any string that arrives at run time.
 */
test("§7.4 the surface's own copy uses the product's nouns", async () => {
  const files = [
    join(CONSOLE_ROOT, "src", "lib", "conversationCopy.ts"),
    join(CONSOLE_ROOT, "src", "components", "conversation", "messages.tsx"),
    join(CONSOLE_ROOT, "src", "components", "conversation", "conversation.tsx"),
    join(CONSOLE_ROOT, "src", "app", "app", "ask", "page.tsx"),
  ];
  const banned = [
    [/\bpipelines?\b/i, "pipeline", "workflow"],
    [/\bdimensions?\b/i, "dimension", "axis"],
    [/\bsuggestions?\b/i, "suggestion", "proposal"],
    [/\bsessions?\b/i, "session", "run (a console session is a different thing entirely)"],
    [/\bjobs?\b/i, "job", "run"],
    [/\bchatbot\b/i, "chatbot", "nothing — this surface is not one"],
  ];
  for (const file of files) {
    // Comments discuss the vocabulary — including the banned words, in the sentences explaining why
    // they are banned. A scan that read prose as copy would report a defect that is a note about the
    // defect, which is how a fence gets switched off.
    const source = await readFile(file, "utf8");
    const code = source
      .split("\n")
      .filter((line) => !/^\s*(\/\/|\*|\/\*)/.test(line))
      .join("\n");
    for (const [pattern, wrong, right] of banned) {
      assert.ok(
        !pattern.test(code),
        `${file.replace(CONSOLE_ROOT, "")} uses "${wrong}". The product's noun is "${right}" — a word ` +
          "that means two things is a defect rather than a synonym, and a conversational surface is " +
          "where that drift is cheapest to start and hardest to reverse.",
      );
    }
  }
});

test("§7.2 an un-approvable request with no reason says so rather than inventing one", () => {
  const stated = unapprovableCopy("Your plan does not include automatic delivery.");
  assert.match(stated, /plan/, "the server's own reason was discarded");
  const missing = unapprovableCopy("   ");
  assert.match(
    missing,
    /defect/,
    "an approval request that arrived with no reason produced a plausible-sounding cause. The platform " +
      "refuses to emit one, so if this ever renders it is a defect and the copy must say so — inventing " +
      "a reason here would hide it.",
  );
  assert.doesNotMatch(missing, /not permitted/i, "the fallback tells a person they did something wrong");
});

// ── §7.7 · the surface states what it can be asked ───────────────────────────────────────────────

test("§7.7 every intent's question comes from the intent table, not from console copy", async () => {
  const src = await readFile(join(REPO_ROOT, "internal", "conversation", "intent.go"), "utf8");
  const start = src.indexOf("var intents = []IntentSpec{");
  const block = src.slice(start, src.indexOf("\n}", start));
  // The LAST string of each row, not "every string containing a question mark": several are imperatives
  // ("fix it, and open a pull request"), and a scan keyed on punctuation would have counted ten and
  // called that fine — a fence that measures the easy subset of what it claims to.
  //
  // 🔴 FIFTEEN since P34 split `harness` into loop (how many turns it takes) and harness (what it is
  // allowed to do). The count is bumped rather than replaced with "every row has one", because the
  // failure this catches is a row added WITHOUT a question — and `>= 1 per row` would pass on that.
  const questions = [...block.matchAll(/"([^"]*)"\},/g)].map(([, q]) => q);
  assert.equal(
    questions.length,
    15,
    `the intent table carries ${questions.length} questions; there are fifteen intents. The refusal ` +
      "renders this list, so a missing question is a boundary the user is not shown.",
  );

  const renderer = await readFile(join(CONSOLE_ROOT, "src", "components", "conversation", "messages.tsx"), "utf8");
  assert.ok(
    renderer.includes("can_do"),
    "the refusal renderer does not render `can_do`. An open text box implies infinity, so the finite " +
      "list is the only thing that states the boundary.",
  );
  // 🔴 And the console must NOT carry its own copy of the fourteen. Two lists of fourteen sentences
  // disagree within a quarter, and the copy is the one that is wrong and the one the user reads.
  const copy = await readFile(join(CONSOLE_ROOT, "src", "lib", "conversationCopy.ts"), "utf8");
  const duplicated = questions.filter((q) => copy.includes(q));
  assert.deepEqual(
    duplicated,
    [],
    "the console has its own copy of these intent questions: " + duplicated.join(" / "),
  );
});
