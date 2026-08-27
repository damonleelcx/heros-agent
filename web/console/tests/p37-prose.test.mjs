// p37-prose.test.mjs proves the static-prose fence goes RED (task 6.1).
//
// # The standing rule this test is
//
// *A fence without a failing fixture is not delivered.* A fence nobody has watched fail is a fence
// nobody knows is connected, and the failure mode is silent: a disconnected fence reports success on
// everything, forever, and the first person to find out is a reviewer who let a document back onto a
// working route.
//
// # 🔴 Why the drill runs against a FIXTURE tree and not against `src/`
//
// `HEROS_APP_ROOT` points `scan-prose.mjs` at a temporary copy. A drill that had to edit a real surface
// to prove the fence works is a drill that can leave the tree broken — and worse, one that people stop
// running.
//
// # 🔴 Why +70 words on ONE route is not the whole claim
//
// It would pass with a 2,000-word ceiling on a 200-word route. The property that makes this drill mean
// something is asserted in `p37-inventory.test.mjs`: EVERY route sits within 70 words of its own
// ceiling, so +70 fails everywhere rather than on the one route the drill happened to pick.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, mkdir, writeFile, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

import { MUTATION_WORDS, PROSE_BUDGETS } from "../scripts/lib/prose-budgets.mjs";

const exec = promisify(execFile);
const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

/** runScan runs the fence against an app tree and returns its exit code and output. */
async function runScan(appRoot) {
  try {
    const { stdout, stderr } = await exec("node", [join(ROOT, "scripts", "scan-prose.mjs")], {
      cwd: ROOT,
      env: { ...process.env, HEROS_APP_ROOT: appRoot },
    });
    return { code: 0, output: stdout + stderr };
  } catch (error) {
    return { code: error.code ?? 1, output: `${error.stdout ?? ""}${error.stderr ?? ""}` };
  }
}

/**
 * fixtureTree builds a temporary app tree carrying EVERY budgeted route, with the one under test copied
 * from the real source and mutated.
 *
 * # Why every route and not just the one being mutated
 *
 * The fence checks BOTH directions — a route with no budget fails, and a budget with no route fails. A
 * fixture holding one route would trip the second check thirty-five times and drown the finding the
 * drill is actually watching for. Suppressing that check under the override would be worse: it would
 * make the drill run against a fence that is not the one that ships.
 *
 * The other routes are one-line stubs. They carry no prose, so they cannot mask an over-budget finding,
 * and the route under test is the REAL file — a fixture the fence has never seen in the wild would
 * prove the fence works on fixtures.
 */
async function fixtureTree(route, mutate = (s) => s) {
  const dir = await mkdtemp(join(tmpdir(), "heros-prose-"));
  for (const budgeted of Object.keys(PROSE_BUDGETS)) {
    const segments = budgeted.split("/").slice(2);
    const target = join(dir, ...segments);
    await mkdir(target, { recursive: true });
    if (budgeted !== route) {
      await writeFile(join(target, "page.tsx"), "export default function Page() {\n  return null;\n}\n", "utf8");
      continue;
    }
    // 🔴 ALL of the route's own files, not just `page.tsx`. A route's budget is over everything it
    // renders — `/app/context` is a page plus an editor — and a fixture holding half of it would be
    // testing a route that does not exist, under a ceiling set for one that does.
    const from = join(ROOT, "src", "app", "app", ...segments);
    for (const entry of await readdir(from, { withFileTypes: true })) {
      if (!entry.isFile() || !/\.(tsx|ts)$/.test(entry.name)) continue;
      const source = await readFile(join(from, entry.name), "utf8");
      await writeFile(join(target, entry.name), entry.name === "page.tsx" ? mutate(source) : source, "utf8");
    }
  }
  return { dir, target: join(dir, ...route.split("/").slice(2)) };
}

/** SEVENTY_WORDS is a real paragraph, not `word `.repeat(70): the counter skips tokens with no letter. */
const SEVENTY_WORDS = Array.from(
  { length: MUTATION_WORDS },
  (_, i) => `paragraph${i}`,
).join(" ");

test("🔴 6.1 the fence is GREEN on the real tree", async () => {
  const { code, output } = await runScan(join(ROOT, "src", "app", "app"));
  assert.equal(code, 0, `the fence fails on the tree it ships against:\n${output}`);
  assert.match(output, /prose scan passed/);
});

test("🔴 6.1 adding 70 words to a working route makes the build FAIL", async () => {
  const route = "/app/context";
  const { dir, target } = await fixtureTree(route, (src) =>
    src.replace("<AxisFrame", `<p>${SEVENTY_WORDS}</p>\n      <AxisFrame`),
  );
  try {
    const { code, output } = await runScan(dir);
    assert.equal(code, 1, `the fence admitted ${MUTATION_WORDS} extra words:\n${output}`);
    // 🔴 The MESSAGE, not just the exit code. A scan that CRASHED would also exit non-zero, and the
    // difference is what matters when this is later refactored.
    assert.match(output, new RegExp(`${route}: \\d+ words of static prose, over its ${PROSE_BUDGETS[route]}-word budget`));
    assert.match(output, /belongs on the\s+reading surface/);
    assert.ok(target, "the fixture route was not written");
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("🔴 6.1 a lede over 60 words makes the build FAIL, naming the file", async () => {
  const { dir } = await fixtureTree("/app/harness", (src) =>
    src.replace(/lede="[^"]*"/, `lede="${SEVENTY_WORDS}"`),
  );
  try {
    const { code, output } = await runScan(dir);
    assert.equal(code, 1, `the fence admitted a ${MUTATION_WORDS}-word lede:\n${output}`);
    assert.match(output, /the lede is \d+ words and the cap is 60/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("🔴 6.1 a route with NO budget makes the build FAIL rather than passing silently", async () => {
  const { dir } = await fixtureTree("/app/harness");
  try {
    await mkdir(join(dir, "brand-new-surface"), { recursive: true });
    await writeFile(
      join(dir, "brand-new-surface", "page.tsx"),
      "export default function Page() {\n  return <p>A new surface nobody budgeted.</p>;\n}\n",
      "utf8",
    );
    const { code, output } = await runScan(dir);
    assert.equal(code, 1, `an unbudgeted route passed:\n${output}`);
    assert.match(output, /\/app\/brand-new-surface: no prose budget/);
    assert.match(output, /a route with no stated\s+ceiling is where the prose goes/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("🔴 6.1 moving an explanation into a disclosure widget makes the build FAIL", async () => {
  // The fence's own blind spot, covered by the second rule (FR11, design D3). Without this, a route over
  // budget could be brought back under it by hiding the paragraph in an accordion — same words, same
  // reader, and a green scan.
  const { dir } = await fixtureTree("/app/harness", (src) =>
    src.replace(
      "<AxisFrame",
      `<details><summary>More</summary><p>${SEVENTY_WORDS}</p></details>\n      <AxisFrame`,
    ),
  );
  try {
    const { code, output } = await runScan(dir);
    assert.equal(code, 1, `an explanation hidden in a <details> passed:\n${output}`);
    assert.match(output, /a <details> disclosure widget carries \d+ words of static prose/);
    assert.match(output, /never in a disclosure widget/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("🔴 6.1 a data readout in a Tooltip does NOT fail — the ban is on prose, not on the element", async () => {
  // The false positive that forced the distinction, kept as a fixture so the fence cannot silently
  // regress to a flat ban. `/app/workflows/…/board` renders its Pareto readout through a component
  // called `Tooltip`; it shows the hovered point's values, in flow, and hides nothing. A flat ban would
  // be satisfied by deleting a working accessible readout to please a scan.
  const { dir } = await fixtureTree("/app/harness", (src) =>
    src.replace("<AxisFrame", "<Tooltip visible>{`${a.label} — quality ${a.q}`}</Tooltip>\n      <AxisFrame"),
  );
  try {
    const { code, output } = await runScan(dir);
    assert.equal(code, 0, `a live data readout was banned as a disclosure widget:\n${output}`);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("6.1 the fence states its own blind spot where somebody reading the output will see it", async () => {
  const source = await readFile(join(ROOT, "scripts", "scan-prose.mjs"), "utf8");
  assert.match(source, /It measures VOLUME/, "the scan does not state what it measures");
  assert.match(
    source,
    /never be cited as though it were|not evidence that anything was\s+moved rather than rearranged/,
    "the scan does not say what a green run is NOT evidence of — design D3 requires the limit stated " +
      "in the same breath as the fence",
  );
  const { output } = await runScan(join(ROOT, "src", "app", "app"));
  assert.match(output, /Volume only/, "the passing message does not carry the caveat to the reader");
});
