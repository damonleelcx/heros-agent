// p37-inventory.test.mjs gates P37 §1 — the Product Designer's classification, before any code moves.
//
// # Why a document gets a test
//
// `block-inventory.md` is not commentary. It is the list §4's pull request is checked against, and the
// list QA fence 6.3 draws its protected text from. A document that quietly falls out of step with the
// tree it describes is worse than no document: it reads as a decision that was made and kept.
//
// So three things are asserted mechanically rather than by review:
//
//   1. every working route has a stated budget — a new route cannot inherit silence;
//   2. every budgeted route exists — a ratchet for a deleted route is a number nobody agreed to;
//   3. the seven rewritten surfaces named in the inventory are exactly the seven in the budget module.
//
// The rest of the inventory — whether a block was classified CORRECTLY — is a review judgement and no
// test can make it. Saying so here is the same discipline design D3 applies to the word count: a fence
// whose limits are unstated is a fence that will be trusted past them.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import { PROSE_BUDGETS, REWRITTEN, MUTATION_WORDS, LEDE_WORDS } from "../scripts/lib/prose-budgets.mjs";
import { totalWords } from "../scripts/lib/prose.mjs";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const APP = join(ROOT, "src", "app", "app");
const INVENTORY = join(ROOT, "..", "..", "openspec", "changes", "p37-source-bound-editors", "block-inventory.md");

/** workingRoutes walks `src/app/app/**` and returns every route that has a `page.tsx`. */
async function workingRoutes(dir = APP, base = "/app") {
  const entries = await readdir(dir, { withFileTypes: true });
  const out = entries.some((e) => e.isFile() && e.name === "page.tsx") ? [base] : [];
  for (const entry of entries) {
    if (entry.isDirectory()) out.push(...(await workingRoutes(join(dir, entry.name), `${base}/${entry.name}`)));
  }
  return out;
}

test("1.5 every working route carries a stated prose budget", async () => {
  const routes = await workingRoutes();
  const missing = routes.filter((route) => PROSE_BUDGETS[route] === undefined);
  assert.deepEqual(
    missing,
    [],
    `these routes have no budget in scripts/lib/prose-budgets.mjs — classify each one rather than letting it inherit silence:\n  ${missing.join("\n  ")}`,
  );
});

test("1.5 no budget names a route that does not exist", async () => {
  const routes = new Set(await workingRoutes());
  const orphans = Object.keys(PROSE_BUDGETS).filter((route) => !routes.has(route));
  assert.deepEqual(orphans, [], `budget entries for routes that are gone: ${orphans.join(", ")}`);
});

test("1.6 the seven rewritten surfaces are budgeted", () => {
  assert.equal(REWRITTEN.length, 7, "P37 rewrites seven surfaces once /app/studio is folded in (§1.6)");
  for (const route of REWRITTEN) {
    assert.ok(PROSE_BUDGETS[route] !== undefined, `${route} is a P37 rewrite and carries no budget`);
  }
});

// 🔴 1.5 / 6.1 — the mutation margin, asserted over the WHOLE table rather than demonstrated on one
// convenient route.
//
// Task 6.1 requires that adding 70 words to a working route fails the build. A drill that picks its own
// target proves the target, not the fence: it would still pass with a 2,000-word ceiling on a 200-word
// route, which is a fence that admits eight paragraphs before it bites.
//
// So this asserts the PROPERTY the budget table is built on — every route sits within 70 words of its
// own ceiling — which is what makes the drill in `p37-prose.test.mjs` a statement about every route.
test("1.5 every route's headroom is smaller than the mutation the fence must catch", async () => {
  const routes = await workingRoutes();
  const tight = [];
  for (const route of routes) {
    const budget = PROSE_BUDGETS[route];
    if (budget === undefined) continue;
    const dir = join(APP, ...route.split("/").slice(2));
    const files = (await readdir(dir, { withFileTypes: true }))
      .filter((e) => e.isFile() && /\.(tsx|ts)$/.test(e.name))
      .map((e) => join(dir, e.name));
    let words = 0;
    for (const file of files) words += totalWords(await readFile(file, "utf8"));
    if (budget - words >= MUTATION_WORDS) {
      tight.push(`${route}: ${words} words under a ${budget} ceiling — ${budget - words} words of headroom`);
    }
  }
  assert.deepEqual(
    tight,
    [],
    `these routes have ${MUTATION_WORDS}+ words of headroom, so adding ${MUTATION_WORDS} words would NOT ` +
      `fail the build there. Lower the ceiling to the measured count rounded up:\n  ${tight.join("\n  ")}`,
  );
});

test("1.1–1.6 the inventory document carries every section the tasks require", async () => {
  const doc = await readFile(INVENTORY, "utf8");
  for (const heading of [
    "## 1.1 / 1.2 — Every static block, classified, with its destination",
    "## 1.3 — The protected text, per surface",
    "## 1.4 — The `not_connected` copy",
    "## 1.5 — The prose budget",
    "## 1.6 — `/app/studio` is in full scope",
  ]) {
    assert.ok(doc.includes(heading), `block-inventory.md is missing "${heading}"`);
  }
});

test("1.1 every surface P37 rewrites has a classification table in the inventory", async () => {
  const doc = await readFile(INVENTORY, "utf8");
  for (const route of REWRITTEN) {
    assert.ok(
      doc.includes(`### \`${route}\``),
      `block-inventory.md has no block table for ${route} — a surface cannot be rewritten before its blocks are classified`,
    );
  }
});

test("1.4 the not_connected copy names the input, the connection flow and the reading surface", async () => {
  const doc = await readFile(INVENTORY, "utf8");
  const section = doc.split("## 1.4 — The `not_connected` copy")[1].split("\n## ")[0];
  assert.match(section, /a connected repository/, "the copy must NAME the missing input, not say 'no data'");
  assert.match(section, /\/app\/connections/, "the copy must link to the connection flow");
  assert.match(section, /\/docs\//, "the copy must link to the reading surface (PRD §4, the first-time reader)");
  assert.match(section, /200/, "not_connected is delivered as a 200 carrying the word, never a 404 (design D6)");
});

test("1.5 the lede cap is the number the PRD proposed", () => {
  assert.equal(LEDE_WORDS, 60, "FR10 — one lede, at most 60 words");
});
