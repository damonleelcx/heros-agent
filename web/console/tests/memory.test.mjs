// memory.test.mjs — P17 QA guards on the memory surface, as FAILING TESTS rather than review notes.
//
// This surface has to do something no other console surface does: sell the reader LESS than they will
// expect, up front, and still be worth using. At this milestone a memory change cannot be written into
// their source at all, and every way of softening that is a lie the console is uniquely able to tell.
//
// So the tests below pin the three rules P17 decisions.md D7 fixes:
//
//   1. the boundary is stated BEFORE the choice, and blames the platform's missing artifact — never the
//      reader's call site, language, or choice of strategy;
//   2. the control is LIVE, not disabled — a greyed-out control says nothing about why;
//   3. a refusal is never rendered as success — no Apply, no attributed gain, `refused` as its own state.
//
// # 🔴 P37 removed the MIRROR, and this file changed shape with it
//
// `src/app/app/memory/strategies.ts` mirrored `registry.BuiltinMemoryStrategies()` so the surface could
// render its vocabulary without a live platform. It was a second source of truth kept honest by a gate,
// and the gate was correct.
//
// P37 binds the picker to `GET /api/v1/memory?language=`, which the platform derives from the registry
// AND from `transform.CoverageFor("memory")`. There is no copy left to drift, so the mirror gate has no
// subject — the same act, for the same reason, as the context coverage transcription (design D5).
//
// What replaces it: the assertions below now run against the ENGINE and against the platform handler
// that serves the vocabulary, plus the registry's own cardinality assertion, which is unchanged.
//
// The three RULES this file exists for are untouched, and now hold for all seven axes because the kit
// they moved into is shared: boundary above the choice, control live rather than disabled, refusal never
// rendered as success.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");
/** readContent reads a reading-surface document — where P37 moved this axis's explanation. */
const readContent = (rel) => readFile(join(root, rel), "utf8");

const PAGE = "src/app/app/memory/page.tsx";
const AUTHORING = "src/app/app/memory/authoring.tsx";
const KIT = "src/components/editorKit.tsx";
const HANDLER = "internal/api/memory.go";
const BUILTINS = "internal/registry/memory_builtins.go";
const COVERAGE = "internal/transform/coverage.go";

/** engineStrategies parses the registry's closed builtin set: the wire name of each strategy. */
function engineStrategies(goSrc) {
  const out = new Set();
  for (const m of goSrc.matchAll(/func \([A-Za-z]+\) Name\(\) string\s*\{\s*return ([^}]+)\}/g)) {
    const v = m[1].trim();
    if (v === "StrategyNone") {
      out.add("none");
      continue;
    }
    const q = v.match(/^"([a-z-]+)"$/);
    if (q) out.add(q[1]);
  }
  return out;
}

test("the strategy vocabulary is SERVED by the platform, not mirrored in the console (P17 FR17)", async () => {
  // The closed set still has to be exactly five, and the registry still has to say so. That assertion is
  // about the ENGINE and is unchanged by P37.
  const engine = engineStrategies(await readRepo(BUILTINS));
  assert.equal(
    engine.size,
    5,
    `parsed ${engine.size} strategies from the registry, expected the closed set of 5 — parser drift, ` +
      `which would make every assertion below pass for the wrong reason`,
  );
  const builtins = await readRepo(BUILTINS);
  assert.match(
    builtins,
    /MemoryStrategySetSize\s*=\s*5/,
    "the registry's cardinality assertion moved; a sixth strategy could be added without a version bump",
  );

  // 🔴 And the console no longer holds a copy of it. A mirror that nothing renders is a mirror nothing
  // gates, which is worse than the mirror that had one.
  await assert.rejects(
    () => read("src/app/app/memory/strategies.ts"),
    "the console's strategy mirror came back. The picker binds to GET /api/v1/memory, which the platform " +
      "derives from the registry — a second copy here is a second answer to what the closed set is.",
  );

  // What the surface binds to instead: the platform's own read model, which carries the vocabulary AND
  // each entry's params schema, both derived server-side.
  const handler = await readRepo(HANDLER);
  assert.match(handler, /MemoryStrategyOptions\(\)/, "the handler does not serve the registry's own option set");
  assert.match(handler, /ParamsSchema/, "the handler does not serve each entry's params schema");
  // 🔴 Read from `lib/axisKit.ts`, not from the kit component. The function lives in a module with NO
  // `"use client"` directive because `lib/axisVocabulary.ts` calls it on the SERVER — a re-export through
  // the client component still marks the value as client and fails at request time with a 500 the build
  // is green through. That is a real defect this phase shipped and then found, and pinning the location
  // here is what stops it being moved back.
  const pure = await read("src/lib/axisKit.ts");
  assert.match(
    pure,
    /export function paramsFromSchema/,
    "the form's fields are not derived from the schema — a hand-written field list is a staler copy of it",
  );
  const kit = await read(KIT);
  assert.doesNotMatch(
    kit,
    /^export (function paramsFromSchema|\{[^}]*\bparamsFromSchema\b)/m,
    "paramsFromSchema is exported from a `use client` module again; the server calls it, and Next.js " +
      "fails that at REQUEST time rather than at build time",
  );
});

test("the boundary is stated BEFORE the picker (P17 FR20)", async () => {
  // 🔴 The rule did not change; where it is enforced did. The boundary is now a PROP the kit renders
  // above the picker, so the ordering is structural rather than a fact about one file's line order —
  // and it holds for all seven axes instead of this one.
  const kit = await read(KIT);
  const boundaryAt = kit.indexOf("{boundary}");
  const pickerAt = kit.indexOf('type="radio"');
  assert.ok(boundaryAt > 0, "the kit renders no boundary slot at all");
  assert.ok(pickerAt > 0, "the kit has no picker");
  assert.ok(
    boundaryAt < pickerAt,
    "the boundary is rendered AFTER the picker. A reader who composes a change and only then meets the " +
      "wall has been given a technically honest bait-and-switch — the platform knew before they started.",
  );

  // And this axis supplies one that NAMES the missing artifact, so "unavailable" never renders bare.
  const src = await read(AUTHORING);
  assert.match(src, /boundary=\{/, "the memory surface passes no boundary to the kit");
  assert.match(
    src,
    /boundary\.missing_artifact/,
    "the boundary does not name what the platform owes; a limit with no named artifact reads as a defect",
  );
});

test("the boundary is PER-CELL and names what is covered (P18 §7.2)", async () => {
  const authoring = await read(AUTHORING);

  // 🔴 P17 asserted this surface says the limit is language-INDEPENDENT, and that was correct while
  // nothing had a materializer. P18 shipped the runtime and a Python rewriter, so that sentence became
  // false — and a surface still carrying it would be lying in the OPPOSITE direction: over-refusing
  // rather than over-claiming. Over-refusing is the one nobody reports, because nobody files a bug about
  // being told "no".
  assert.doesNotMatch(
    authoring,
    /not about your language/i,
    "the surface still claims the limit is independent of the language. That was true before the memory " +
      "runtime landed and is false now — Python call sites materialize.",
  );

  // 🔴 P37 makes this STRONGER than the mirrored list P18 checked. The boundary is computed for the
  // READER'S OWN node's language, server-side, from `transform.CoverageFor("memory")` — so it cannot
  // fall behind the engine at all, and it answers about the language they actually have.
  assert.match(
    authoring,
    /subject\.language/,
    "the boundary does not name the reader's own language, so it is a claim about somebody else's code",
  );
  assert.match(authoring, /boundary\.applicable/, "the surface does not branch on the engine's own answer");

  const page = await read(PAGE);
  assert.match(
    page,
    /loadMemoryVocabulary\(language\)/,
    "the boundary is not requested for the node's own language",
  );
  const handler = await readRepo("internal/api/memory.go");
  assert.match(
    handler,
    /No silent default to "go"/,
    "the platform handler no longer refuses to guess a language; a boundary computed for the wrong one " +
      "is a claim about code the reader does not have",
  );

  // 🔴 The PRECONDITIONS a materializing cell carries must be stated up front, on the surface, not only
  // in the document — a reader meets them before composing, or they meet them at apply time.
  assert.match(
    authoring,
    /memory is a read <em>and<\/em> a write/,
    "the surface does not state the read-and-write precondition before the choice",
  );
});

test("the control is LIVE, not disabled (P17 FR20 — a disabled control says nothing about why)", async () => {
  const src = await read(AUTHORING);
  const kit = await read(KIT);

  // No disabled inputs on this axis's own file.
  //
  // 🔴 The pattern matches an ATTRIBUTE, not the word. An earlier version matched `disabled` followed by
  // whitespace and went red on a doc comment explaining why nothing is disabled — a fence that fires on
  // the prose defending the rule is a fence someone deletes.
  for (const attr of [/\sdisabled(=|\s*\/?>)/, /\sreadOnly(=|\s*\/?>)/, /aria-disabled=/]) {
    assert.doesNotMatch(src, attr, `a control on the memory authoring surface carries ${attr}`);
  }

  // 🔴 The kit has EXACTLY ONE disabled path, and it is FR7's: an option this deployment cannot supply,
  // rendered disabled WITH the service it needs. Rule 2 forbids disabling a control whose reason is a
  // boundary; it does not forbid disabling one that is genuinely not there. The reason must travel with
  // it, or the exception becomes the rule.
  assert.match(kit, /disabled=\{unavailable\}/, "the kit's one disabled path is gone");
  assert.match(kit, /needs \{o\.unavailableReason\}/, "a disabled option does not name what it needs");
  assert.doesNotMatch(
    kit,
    /disabled=\{true\}|disabled\s*\/>/,
    "the kit disables a control unconditionally; the boundary is stated instead",
  );

  // Every option is rendered — including the refused ones, because authoring is not refused.
  assert.match(kit, /vocabulary\.options\.map/, "the picker does not render the full option set");
  assert.match(kit, /onChange=\{\(\) => \{/, "selecting an option does not change state; the picker is decorative");
  assert.match(kit, /setParams\(\{ \.\.\.params/, "the parameter fields do not accept input");
});

test("a refusal is never rendered as success (P17 FR21, NFR11)", async () => {
  const src = await read(AUTHORING);
  const page = await read(PAGE);
  const kit = await read(KIT);

  // 🚫 No apply/deliver/merge control anywhere on the authoring path.
  for (const banned of [/>\s*Apply\b/, /Apply this change/i, /Deliver\b/, /Open a pull request/i, /Merge\b/]) {
    assert.doesNotMatch(src + kit, banned, `the memory surface offers an apply/deliver control (${banned})`);
  }

  // No attributed gain. An unverified change claims nothing.
  for (const banned of [/tokens saved/i, /cost saving/i, /improves? (recall|memory|quality)/i, /faster/i]) {
    assert.doesNotMatch(src + page + kit, banned, `an outcome (${banned}) is attributed to an unverified change`);
  }

  // `refused` renders through the three-verdict panel, which draws it as its own state — and the verdict
  // now comes from the PLATFORM rather than from a fixture in this file, which is the stronger form:
  // there is no local branch that could collapse refused into failed or pending.
  assert.match(kit, /PreflightPanel/, "the surface does not render the platform's verdict");
  assert.match(
    kit,
    /\/api\/console\/authoring\/preflight/,
    "the verdict is not obtained from the platform; a locally-produced verdict is a locally-produced lie",
  );
  assert.match(kit, /UnverifiedLabel/, "the authored change is shown without its unverified state");
});

test("the reader is told what their change DID produce (P17 FR22)", async () => {
  const kit = await read(KIT);
  const doc = await readContent("content/docs/en/concepts/memory-strategies.md");

  // The refusal is narrow, and a surface that only says no teaches the reader the axis does not work.
  assert.match(kit, /config_hash/, "the surface never shows the configuration the change produced");
  assert.match(kit, /saved\.verification_state/, "the produced configuration is shown without its state");
  // 🔴 And it is the SERVER's hash. The panel this was extracted from computed a pseudo-hash in the
  // browser; NFR7.3 says the browser derives nothing, and block-inventory M10 records that deletion.
  assert.doesNotMatch(kit, /function hashFor/, "the browser computes a configuration hash again");
  assert.match(
    kit,
    /rendered as received/,
    "the surface does not state that the hash is the platform's rather than its own",
  );

  // Why authoring is worth doing while it is refused — at its destination.
  assert.match(
    doc,
    /materializes unchanged the day the rewriter lands/i,
    "nothing says the change survives to the day it can be applied, which is the reason authoring is " +
      "worth doing while it is refused",
  );
});

test("the surface keeps memory and context disjoint (P17 decisions.md D2)", async () => {
  const page = await read(PAGE);

  assert.match(
    page,
    /ACROSS invocations/,
    "the page never states that memory persists across invocations — the one distinction a reader must " +
      "leave with",
  );
  assert.match(
    page,
    /Within ONE call/,
    "the page never contrasts memory with context's within-a-call scope, so the two remain conflatable",
  );
  // It links to the sibling axis rather than re-explaining it, so the split is stated once.
  const authoring = await read(AUTHORING);
  assert.match(
    authoring,
    /href="\/app\/context"/,
    "the memory surface does not link to the context surface; a reader who is on the wrong axis has no " +
      "way to reach the right one",
  );
});

test("the engine's memory coverage is derived from the materializer table (P18 §5.1)", async () => {
  const cov = await readRepo(COVERAGE);
  const fn = cov.slice(cov.indexOf("func memoryCoverage("), cov.indexOf("func sortedPolicyNames("));
  assert.ok(fn.length > 0, "parser drift: memoryCoverage is no longer in coverage.go");

  // 🔴 P17 asserted this function must NOT branch on language, because the axis was uniform. P18 made it
  // per-cell, so the assertion inverts: it must derive from the materializer table, and it must still
  // classify an uncovered cell as a platform gap rather than the customer's problem.
  assert.match(
    fn,
    /HasMemoryMaterializer/,
    "memoryCoverage no longer derives from the materializer table; the coverage claim and the rewriter's " +
      "behaviour could then disagree",
  );
  assert.match(
    fn,
    /CauseNoMaterializer/,
    "an uncovered memory cell is not classed as a platform gap; only that class names work we owe",
  );
  assert.match(
    fn,
    /MemoryMaterializerLanguages/,
    "an uncovered cell's note does not name the covered languages, so a reader cannot tell whether the " +
      "axis works anywhere",
  );
});

test("the surface is reachable (P17 §10)", async () => {
  const layout = await read("src/app/app/layout.tsx");
  assert.match(layout, /href: "\/app\/memory"/, "the memory surface is not in the primary navigation");
  assert.match(
    layout,
    /label: "Memory strategy", href: "\/app\/memory"/,
    "the memory surface is not in the command path; a surface reachable only by typing a URL is not " +
      "reachable",
  );
});
