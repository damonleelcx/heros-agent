// coverage.test.mjs — the guards on the coverage surface (P13 20.1–20.4, P14 11.18–11.19,
// P15 20.14, P16 10.15).
//
// This surface makes the one claim a console is uniquely able to get wrong: it tells a reader what the
// platform will write into their source, and — where it will not — WHOSE MOVE the rest is. Two failures
// are silent and expensive, so both are pinned here as failing tests rather than as review notes:
//
//   1. The console derives a verdict of its own. It must not: status, cause class and the missing
//      artifact all arrive from the engine and are rendered as received. A console that decided for
//      itself what "counts as supported" would be the second coverage source the contract exists to
//      prevent, and it would drift optimistic.
//   2. The three refusals collapse into one greyed-out control. That is the exact failure the whole
//      capability was built to end — it sends the reader whose call site is the problem to wait for us,
//      and the reader whose language we have not built to go and edit working code.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");

const COMPONENT = "src/components/coverage.tsx";
const PAGE = "src/app/app/coverage/page.tsx";
const ENGINE = "internal/transform/coverage.go";

// 🔴 The three cause identifiers are DATA shared with the engine. The console branches on them, so a
// renamed class must break the build rather than silently fall through to a default treatment.
test("the console's cause classes are exactly the engine's three", async () => {
  const [component, engine] = await Promise.all([read(COMPONENT), readRepo(ENGINE)]);
  const engineIds = [...engine.matchAll(/CauseClass = "([a-z-]+)"/g)].map((m) => m[1]);
  assert.equal(engineIds.length, 3, `the engine must declare exactly three cause classes, got ${engineIds}`);
  for (const id of engineIds) {
    assert.ok(
      component.includes(`"${id}"`),
      `the console does not handle the engine's cause class ${id}; an unhandled class falls through to a default treatment, which is how three different answers become one`,
    );
  }
});

// 🔴 Three refusals, three DISTINCT treatments. Distinct by border style and weight rather than by
// colour alone — the accessibility floor, and also what keeps the hazard palette free for hazard.
test("each refusal class gets its own visual treatment", async () => {
  const component = await read(COMPONENT);
  const block = component.slice(component.indexOf("const STATE_STYLE"), component.indexOf("/** A single cause"));
  const boxes = [...block.matchAll(/box:\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.equal(boxes.length, 4, "four states must be styled: applies, plus the three refusal classes");
  assert.equal(new Set(boxes).size, 4, `two states share a treatment, so a reader cannot tell them apart: ${boxes}`);

  const shorts = [...block.matchAll(/short:\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.equal(new Set(shorts).size, 4, `two states share a label: ${shorts}`);
});

// 🚫 The hazard palette is reserved for hazard. A refusal is an ANSWER, not a danger, and spending the
// hazard colour here would make it stop meaning anything where it matters.
test("the coverage states do not spend the hazard palette", async () => {
  const component = await read(COMPONENT);
  for (const hazard of ["danger", "warn"]) {
    assert.ok(
      !new RegExp(`(border|bg|text)-${hazard}`).test(component),
      `the coverage states use the ${hazard} palette, which is reserved for hazard`,
    );
  }
});

// 🔴 Partial coverage reads as the REFUSAL. Rounding a square up to "applies" because something in it
// works is how a coverage table becomes a promise the engine then refuses.
test("a partially covered square is not rounded up to applies", async () => {
  const component = await read(COMPONENT);
  const fn = component.slice(component.indexOf("function dominantState"));
  assert.ok(
    /refused\.length === 0/.test(fn),
    "dominantState must report `applies` only when NOTHING in the square refuses",
  );
});

// 🚫 No client-side derivation. The page groups cells into squares; it must not compute a verdict.
test("the page renders the platform's verdict rather than computing one", async () => {
  const page = await read(PAGE);
  assert.ok(page.includes("fetchCoverage"), "the page must read the coverage table from the platform");
  for (const banned of ["supported", "unsupported"]) {
    assert.ok(
      !new RegExp(`["'\`]${banned}["'\`]`).test(page),
      `the page invents the verdict "${banned}" instead of rendering the engine's status and cause`,
    );
  }
  const data = await read("src/app/app/coverage/data.ts");
  assert.ok(
    !/const\s+(COVERAGE|FALLBACK|CELLS)\b/.test(data),
    "the console carries a local coverage table; a copy is the second source of truth this contract exists to prevent",
  );
});

// 🔴 The boundary is STATED before a picker, and the two kinds of boundary read as two different
// sentences: a platform gap has a "when" and a source fact does not.
test("the boundary component states which boundary it is, and never renders an empty picker", async () => {
  const component = await read(COMPONENT);
  const fn = component.slice(component.indexOf("export function CoverageBoundary"));
  assert.ok(fn.includes("yet"), "a platform gap must say `yet` — it is the one refusal with a when");
  assert.ok(
    fn.includes("This is not a wait"),
    "a not-in-source refusal must say plainly that there is nothing to wait for",
  );
  assert.ok(
    fn.includes("rather than something to wait for"),
    "a call-site refusal must send the reader to their own code, not to our backlog",
  );
  assert.ok(
    fn.includes("identical on every plan"),
    "the boundary must state that no tier changes the answer, or a refusal reads as an upsell",
  );
  // The picker (children) renders ONLY when something applies.
  const appliesBranch = fn.slice(fn.indexOf("if (applies.length > 0)"), fn.indexOf("const state ="));
  assert.ok(appliesBranch.includes("{children}"), "the picker must render when the node can carry the change");
  const refuseBranch = fn.slice(fn.indexOf("const state ="));
  assert.ok(
    !refuseBranch.includes("{children}"),
    "the picker is rendered on a node that cannot carry the change; an empty list reads as `no options` when the truth is `not here`",
  );
});

// The surface is reachable: a page nobody can navigate to is a page that does not exist.
test("coverage has a route and a command-path entry", async () => {
  const [routes, layout] = await Promise.all([read("src/lib/routes.ts"), read("src/app/app/layout.tsx")]);
  assert.ok(/coverage:\s*\(\)\s*=>\s*"\/app\/coverage"/.test(routes), "routes.ts must carry the coverage route");
  assert.ok(
    layout.includes('"/app/coverage"'),
    "the command path must reach the coverage surface",
  );
  // R19: the palette is an accelerator, never the only route — the rail must carry it too.
  assert.ok(
    /\{\s*href:\s*"\/app\/coverage"/.test(layout),
    "coverage is in the command path but not in the navigation rail",
  );
});
