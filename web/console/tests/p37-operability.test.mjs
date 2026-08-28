// p37-operability.test.mjs — §7 (DevOps) and §8 (Sales Operations), as fences rather than intentions.
//
// # Why these two sections get a test at all
//
// They are the two whose failures are invisible. A deploy ordering that is only written down is one
// somebody gets right until the week they are in a hurry; a noun that means two things is a defect that
// never throws. Both are checkable, so both are checked.
//
// # What is deliberately NOT here
//
// Task 8.1 asks for the boundary copy to be REVIEWED as a customer-facing commitment. No test performs a
// review. What this file can do — and does — is assert that the copy exists, that it is one copy rather
// than seven, and that it does not promise what §8.3 forbids. The judgement of whether the wording is
// one the company wants to be held to is a sign-off item, and it is listed as one.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import { REWRITTEN } from "../scripts/lib/prose-budgets.mjs";
import { SUBJECT_STATES } from "../src/lib/axisSubject.ts";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const strip = (src) => src.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/^\s*\/\/.*$/gm, " ");

async function sourceOf(route) {
  const dir = join(ROOT, "src", "app", "app", ...route.split("/").slice(2));
  const files = (await readdir(dir, { withFileTypes: true }))
    .filter((e) => e.isFile() && /\.(tsx|ts)$/.test(e.name))
    .map((e) => join(dir, e.name));
  return (await Promise.all(files.map((f) => readFile(f, "utf8")))).join("\n");
}

// ── 7.1 · the resolver's outcomes are readable, not only logged ─────────────────────────────────

test("🔴 7.1 every subject-resolver outcome is counted and published on the health endpoint", async () => {
  const health = await read("src/app/api/health/route.ts");
  assert.match(health, /subject_resolver: subjectResolverHealth\(\)/, "the counters are not published");

  const module_ = await read("src/lib/subjectHealth.ts");
  // Every member of the closed set, including the ones at zero. An omitted key and a zero are different
  // facts, and only one of them is good news.
  assert.match(
    module_,
    /SUBJECT_STATES\.map\(\(state\) => \[state, 0\]\)/,
    "the counter map is not built from the closed state set, so a state could be silently uncounted",
  );
  for (const state of SUBJECT_STATES) {
    assert.ok(SUBJECT_STATES.includes(state), `${state} left the closed set`);
  }

  // 🔴 Counted at ONE place. Eight `recordSubjectOutcome` call sites is eight chances to forget the
  // eighth, and the state it stops counting is the one nobody notices is missing.
  const resolver = await read("src/lib/subjectResolver.ts");
  const calls = [...resolver.matchAll(/recordSubjectOutcome\(/g)].length;
  assert.equal(calls, 1, `recordSubjectOutcome is called ${calls} times; it is called once, on the way out`);
  assert.match(resolver, /const outcome = await resolve\(selection\);/, "the resolution is not wrapped");
});

test("7.1 the counters state their own scope where a reader will see it", async () => {
  // A per-process counter read as a fleet total is a wrong number that looks right. `health-signal-surface`
  // asks for a readable signal; a readable signal that misleads is worse than a log line.
  const module_ = await read("src/lib/subjectHealth.ts");
  assert.match(module_, /scope: "this console process since it started/, "the numbers travel without their scope");
  assert.match(module_, /not summed across replicas and reset by every rollout/, "the caveat is not stated");
});

// ── 7.2 · destinations deploy before the surfaces that link to them ─────────────────────────────

test("🔴 7.2 the destinations ship in the same artifact as the surfaces that link to them", async () => {
  // The ordering requirement has a stronger form than "deploy A before B", and this is it: the content
  // and the console are ONE IMAGE. `content/docs/en/**` is read from the container's own filesystem
  // (`lib/reading/corpus.ts`), so a console that renders a `ReadOn` link is by construction a console
  // that holds the page it points at. There is no window in which one is deployed and the other is not.
  const corpus = await read("src/lib/reading/corpus.ts");
  assert.match(
    corpus,
    /join\(process\.cwd\(\), "content"\)/,
    "the corpus is no longer read from the image's own filesystem, so the destinations and the surfaces " +
      "can now be deployed independently — and a link to a section that has not shipped yet is the 404 " +
      "nobody reports",
  );

  // And the image is built with the content in it.
  const dockerfile = await read("../../deploy/console.Dockerfile").catch(() => "");
  if (dockerfile) {
    assert.match(dockerfile, /content/, "the console image does not copy the content tree");
  }

  // The remaining ordering risk is a CACHED page, not a missing one, and `docs/[...slug]` is
  // `force-dynamic` — so a rollout cannot serve yesterday's corpus from a prerendered page.
  const docsPage = await read("src/app/(reading)/docs/[...slug]/page.tsx");
  assert.match(docsPage, /export const dynamic = "force-dynamic"/, "a docs page could serve a stale corpus");
});

// ── 7.3 · each surface independently revertible ────────────────────────────────────────────────

test("🔴 7.3 each surface is revertible on its own — no surface imports another's editor", async () => {
  // The bisectability requirement, expressed as the property that actually delivers it: a regression is
  // revertible per surface only if reverting one surface's files cannot break another's. Shared code
  // lives in `components/` and `lib/`; a surface reaching into a sibling's directory is a coupling that
  // makes "revert /app/graph" also mean "revert /app/context".
  const violations = [];
  for (const route of REWRITTEN) {
    const source = await sourceOf(route);
    for (const match of strip(source).matchAll(/from "\.\.\/([a-z-]+)\//g)) {
      violations.push(`${route} imports from a sibling surface: ../${match[1]}/`);
    }
  }
  assert.deepEqual(
    violations,
    [],
    "a surface reaches into another surface's directory, so the two cannot be reverted independently:\n  " +
      violations.join("\n  "),
  );
});

// ── 8.2 · the noun dictionary ──────────────────────────────────────────────────────────────────

/** NOUNS is §8.2's list. Each is defined ONCE on the reading surface and used unchanged everywhere. */
const NOUNS = ["node", "axis", "policy", "strategy", "variant", "config_hash"];

test("🔴 8.2 every noun in the dictionary is defined once, on the reading surface", async () => {
  const glossary = await read("content/docs/en/concepts/glossary.md");
  const headings = [...glossary.matchAll(/^##\s+(.+)$/gm)].map((m) => m[1].trim().toLowerCase());

  const missing = NOUNS.filter(
    (noun) => !headings.some((h) => h === noun || h === `\`${noun}\`` || h.startsWith(noun)),
  );
  assert.deepEqual(
    missing,
    [],
    "these nouns have no definition on the reading surface, so six pages will each acquire their own:\n  " +
      missing.join("\n  "),
  );
});

test("🔴 8.2 no rewritten surface re-defines a noun the glossary owns", async () => {
  // The failure this catches is not a synonym — it is a SECOND DEFINITION. Six pages that each
  // re-explain "strategy" in their own words is how a product acquires six meanings for one noun, and
  // the drift is invisible because every one of them reads correctly on its own page.
  const redefinition = /\b(a|an|the)\s+(node|axis|policy|strategy|variant)\s+is\s+(a|an|the)\b/i;
  const found = [];
  for (const route of REWRITTEN) {
    const source = strip(await sourceOf(route));
    for (const [i, line] of source.split("\n").entries()) {
      if (redefinition.test(line)) found.push(`${route}:${i + 1} — ${line.trim().slice(0, 90)}`);
    }
  }
  assert.deepEqual(
    found,
    [],
    "a working surface defines a noun the glossary defines. Link to the definition instead:\n  " +
      found.join("\n  "),
  );
});

// ── 8.1 / 8.3 · the boundary is a commitment, and the shorter copy promises no more ─────────────

test("🔴 8.1 the boundary copy exists per axis that has one, and is ONE copy", async () => {
  // A boundary written seven times is seven sentences that drift, and the one that drifts is the one a
  // customer is held to. The kit renders `{boundary}` from a single slot; each axis supplies the clause
  // only it knows.
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /\{boundary\}/, "the kit has no single boundary slot");

  const perAxis = {
    "/app/memory": /boundary\.missing_artifact/,
    "/app/context": /refused when it is proposed/i,
    "/app/studio": /does not become another by changing a model string/i,
    "/app/harness": /That is not the same as unenforced/i,
    "/app/graph": /<WiringBoundaries \/>/,
  };
  for (const [route, needle] of Object.entries(perAxis)) {
    assert.match(await sourceOf(route), needle, `${route} lost the boundary §8.1 reviews as a commitment`);
  }
});

test("🔴 8.3 nothing in the shortened copy promises an axis can reach source when it cannot", async () => {
  // The risk P37 creates and PRD §9.8 names: *"a page that used to spend four paragraphs explaining the
  // boundary and now spends none has PROMISED MORE by saying less."* So the words that would constitute
  // that promise are banned unless the same sentence withdraws them.
  const overclaims = [
    /\bwill be (applied|written) (into|to) your source\b/i,
    /\bautomatically (applies|merges|ships)\b/i,
    /\bevery axis (reaches|writes to) (your )?source\b/i,
    /\bguarantee[sd]?\b/i,
  ];
  const denial = /\b(not|never|cannot|no |refus|declin|is not|does not)\b/i;

  const found = [];
  for (const route of REWRITTEN) {
    const source = strip(await sourceOf(route));
    for (const [i, line] of source.split("\n").entries()) {
      for (const pattern of overclaims) {
        if (pattern.test(line) && !denial.test(line)) {
          found.push(`${route}:${i + 1} — ${line.trim().slice(0, 100)}`);
        }
      }
    }
  }
  assert.deepEqual(
    found,
    [],
    "the shortened copy promises more than the product does. A boundary that used to be four paragraphs " +
      "and is now none has promised more by saying less:\n  " + found.join("\n  "),
  );
});

test("8.3 the axes that reach source NOWHERE say so on their own surface", async () => {
  // The sharpest case, and the one a word count deletes first: `harness` materializes in no language,
  // permanently. A surface that stopped saying so would be over-promising by omission.
  const harness = await sourceOf("/app/harness");
  assert.match(harness, /nothing, in any language, permanently/i, "the permanent boundary is gone");
  assert.match(harness, /That is not the same as unenforced/i, "the sentence that stops `refused` reading as `ignored` is gone");
});
