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
// Plus the one every mirrored table needs: the strategy vocabulary agrees with the engine's, strategy
// for strategy. A mirror with no gate is a second source of truth whose failure is silent.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");

const PAGE = "src/app/app/memory/page.tsx";
const AUTHORING = "src/app/app/memory/authoring.tsx";
const MIRROR = "src/app/app/memory/strategies.ts";
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

/** mirrorStrategies parses the console's mirror. */
function mirrorStrategies(tsSrc) {
  const out = new Set();
  for (const m of tsSrc.matchAll(/^\s*strategy:\s*"([a-z-]+)",/gm)) out.add(m[1]);
  return out;
}

test("the strategy vocabulary matches the engine's, strategy for strategy (P17 FR17)", async () => {
  const engine = engineStrategies(await readRepo(BUILTINS));
  const mirror = mirrorStrategies(await read(MIRROR));

  assert.equal(
    engine.size,
    5,
    `parsed ${engine.size} strategies from the registry, expected the closed set of 5 — parser drift, ` +
      `which would make every assertion below pass for the wrong reason`,
  );

  for (const s of engine) {
    assert.ok(
      mirror.has(s),
      `the registry ships "${s}" and the console does not offer it. A strategy the platform knows but ` +
        `the surface hides is unreachable — the user can never author it.`,
    );
  }
  for (const s of mirror) {
    assert.ok(
      engine.has(s),
      `the console offers "${s}", which is not in the registry's closed set. Selecting it would fail at ` +
        `seal, so the surface would be offering a choice that cannot be made.`,
    );
  }

  // And the cardinality assertion the registry itself carries is still live.
  const builtins = await readRepo(BUILTINS);
  assert.match(
    builtins,
    /MemoryStrategySetSize\s*=\s*5/,
    "the registry's cardinality assertion moved; a sixth strategy could be added without a version bump",
  );
});

test("the boundary is stated BEFORE the picker (P17 FR20)", async () => {
  const src = await read(AUTHORING);

  const bannerAt = src.indexOf("<Banner");
  const pickerAt = src.indexOf('name="memory-strategy"');
  assert.ok(bannerAt > 0, "the surface states no boundary at all");
  assert.ok(pickerAt > 0, "the surface has no strategy picker");
  assert.ok(
    bannerAt < pickerAt,
    "the boundary banner is rendered AFTER the picker. A reader who composes a change and only then " +
      "meets the wall has been given a technically honest bait-and-switch — the platform knew before " +
      "they started.",
  );

  // It names the missing artifact, so "unavailable" is never rendered without a reason.
  assert.match(
    src,
    /missingArtifact/,
    "the boundary does not name what the platform owes; a limit with no named artifact reads as a defect",
  );
});

test("the boundary is PER-CELL and names what is covered (P18 §7.2)", async () => {
  const mirror = await read(MIRROR);
  const authoring = await read(AUTHORING);

  // 🔴 P17 asserted this surface says the limit is language-INDEPENDENT, and that was correct while
  // nothing had a materializer. P18 shipped the runtime and a Python rewriter, so that sentence became
  // false — and a surface still carrying it would be lying in the OPPOSITE direction: over-refusing
  // rather than over-claiming. Both are the same defect, and over-refusing is the one nobody reports,
  // because nobody files a bug about being told "no".
  assert.doesNotMatch(
    authoring,
    /not about your language/i,
    "the surface still claims the limit is independent of the language. That was true before the memory " +
      "runtime landed and is false now — Python call sites materialize.",
  );
  assert.doesNotMatch(
    mirror,
    /missing in every language/i,
    "the mirrored boundary still says the gap is in every language",
  );

  // What it must say instead: which languages ARE covered, sourced from one list.
  assert.match(mirror, /MATERIALIZED_LANGUAGES/, "the mirror declares no covered-language list");
  assert.match(mirror, /"python"/, "the covered-language list does not include python");
  assert.match(
    authoring,
    /applicableIn/,
    "the surface does not render the covered-language list, so a reader cannot tell where it applies",
  );

  // 🔴 And the PRECONDITIONS a materializing cell still carries must be stated up front. A reader who
  // meets "your call site must assign its result" at apply time has been told half the truth.
  assert.match(mirror, /preconditions/, "the mirror states no preconditions for a materializing cell");
  assert.match(
    mirror,
    /read AND a write|both/i,
    "the preconditions do not mention that both halves must land, which is the one that decides whether " +
      "a given call site works",
  );
  assert.match(
    mirror,
    /session/i,
    "the preconditions do not mention the session id, which the generated module raises without",
  );
  assert.match(
    authoring,
    /BOUNDARY\.preconditions/,
    "the surface does not render the preconditions before the choice",
  );
});

test("the control is LIVE, not disabled (P17 FR20 — a disabled control says nothing about why)", async () => {
  const src = await read(AUTHORING);

  // No disabled inputs anywhere on the authoring path.
  //
  // 🔴 The pattern matches an ATTRIBUTE, not the word. An earlier version matched `disabled` followed by
  // whitespace and went red on this component's own doc comment explaining why nothing is disabled —
  // a fence that fires on the prose defending the rule is a fence someone deletes. What is banned is
  // `disabled`, `readOnly` or `aria-disabled` written as a JSX prop.
  for (const attr of [/\sdisabled(=|\s*\/?>)/, /\sreadOnly(=|\s*\/?>)/, /aria-disabled=/]) {
    assert.doesNotMatch(
      src,
      attr,
      `a control on the memory authoring surface carries ${attr}. A greyed-out control tells the reader ` +
        `nothing about WHY, and invites the belief that some other strategy, language, or plan would ` +
        `unlock it. The reason is stated instead.`,
    );
  }

  // Every strategy is selectable — including the refused ones, because authoring is not refused.
  assert.match(src, /STRATEGIES\.map/, "the picker does not render the full strategy set");
  assert.match(
    src,
    /onChange=\{\(\) => \{\s*setSelected/,
    "selecting a strategy does not change state; the picker is decorative",
  );
  // Parameters are editable.
  assert.match(src, /setParams\(\{ \.\.\.params/, "the parameter fields do not accept input");
  // And the change can be backed out.
  assert.match(
    src,
    /Clear the memory strategy/,
    "there is no way to clear an authored memory strategy; a change a user cannot take back is not a " +
      "change they can safely try",
  );
});

test("a refusal is never rendered as success (P17 FR21, NFR11)", async () => {
  const src = await read(AUTHORING);
  const page = await read(PAGE);

  // 🚫 No apply/deliver/merge control. There is nothing to apply, so offering the button would be the lie.
  for (const banned of [/>\s*Apply\b/, /Apply this change/i, /Deliver\b/, /Open a pull request/i, /Merge\b/]) {
    assert.doesNotMatch(
      src,
      banned,
      `the memory surface offers an apply/deliver control (${banned}); while the transform refuses, a ` +
        `memory change cannot be applied, delivered, or merged, and a control implying otherwise is the ` +
        `whole failure this surface exists to avoid`,
    );
  }

  // No attributed gain. An unverified, unapplied change claims nothing.
  for (const banned of [/tokens saved/i, /cost saving/i, /improves? (recall|memory|quality)/i, /faster/i]) {
    assert.doesNotMatch(
      src + page,
      banned,
      `the memory surface attributes an outcome (${banned}) to a change that cannot be verified at this ` +
        `milestone`,
    );
  }

  // `refused` renders through the three-verdict panel, which draws it as its own state.
  assert.match(
    src,
    /PreflightPanel/,
    "the surface does not render the platform's verdict; a hand-drawn state could collapse refused into " +
      "failed or pending, which lead a reader to opposite actions",
  );
  assert.match(
    src,
    /verdict:\s*"refused"/,
    "the surface never produces a refused verdict — the state a memory change actually ends in",
  );
  // And the unverified label travels with the produced configuration.
  assert.match(src, /UnverifiedLabel/, "the authored change is shown without its unverified state");
});

test("the reader is told what their change DID produce (P17 FR22)", async () => {
  const src = await read(AUTHORING);

  // The refusal is narrow, and a surface that only says no teaches the reader the axis does not work.
  assert.match(src, /config_hash/, "the surface never shows the configuration the change produced");
  assert.match(src, /parent/, "the surface never shows the parent the change was derived from");
  assert.match(
    src,
    /origin/,
    "the surface never shows that the change is attributed to the reader; origin is what makes an " +
      "authored change auditable",
  );
  assert.match(
    src,
    /materializes unchanged|survives|re-materializable|once the rewriter lands/i,
    "the surface never says the change survives to the day it can be applied — which is the reason " +
      "authoring is worth doing while it is refused",
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
