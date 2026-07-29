// skill-tool-authoring.test.mjs — P14 14c frontend guards (tasks 9.9–9.12).
//
// The properties here are the two that a UI gets wrong in opposite directions:
//
//   an EMPTY picker where a boundary belongs — reads as "your catalogue is empty" when the truth is
//   "this language cannot carry a binding yet", and sends the reader to install something;
//
//   a TEXT INPUT where a choice belongs — lets a reader name a tool the codemod cannot delete, which
//   emits a diff that removes nothing or removes the wrong span, silently.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");

const SELECTION = "src/app/app/configure/selection.tsx";
const CONFIGURE_PAGE = "src/app/app/configure/page.tsx";

// ── 9.9 the language boundary is stated, not an empty picker ────────────────────────────────────

test("9.9 a node whose language has no materializer states the boundary rather than showing an empty list", async () => {
  const src = (await read(SELECTION)).replace(/\s+/g, " ");

  // The refusal must be RENDERED, not implied by an absence.
  assert.match(src, /skills_refused/, "the language boundary is not carried as a refusal");
  assert.match(src, /PreflightPanel/, "the boundary is not rendered through the refusal component");

  // It must name the language and say what would have worked.
  assert.match(src, /no materializer for python has landed yet/,
    "the refusal does not name the language");
  assert.match(src, /covered today: go/, "the refusal does not say which languages are covered");

  // And it must say the gap is OURS. Without this the reader concludes their catalogue is wrong.
  assert.match(src, /gap in the platform, not in your catalogue/i,
    "the surface does not say the gap is the platform's");
});

// ── 9.10 no free text on the binding or selection path ──────────────────────────────────────────

test("9.10 no free-text entry exists as a binding or selection path", async () => {
  for (const rel of [SELECTION, "src/components/authoring.tsx"]) {
    const src = await read(rel);
    // A skill is bound from a sealed contract; a tool is selected from the discovered set. Neither is
    // typed. An <input> or a contentEditable here is where an untypeable name gets typed.
    assert.ok(!/<input\b/i.test(src), `${rel} renders a text input on the selection path`);
    assert.ok(!/<textarea\b/i.test(src), `${rel} renders a textarea on the selection path`);
    assert.ok(!/contentEditable/i.test(src), `${rel} renders a contentEditable region`);
  }

  const src = await read(SELECTION);
  // What IS rendered is a set of choices drawn from platform-supplied data.
  assert.match(src, /offered_skills\.map/, "skills are not rendered from the offered set");
  assert.match(src, /discovered_tools\.map/, "tools are not rendered from the discovered set");
});

test("9.10 only sealed, pinned skills are presented", async () => {
  const src = await read(SELECTION);
  // Every offered skill carries a version. An entry without one is not a lesser offer — it is not an
  // offer, because the value its binding would construct is undetermined.
  assert.match(src, /version_id/, "offered skills do not carry a pinned version");
  assert.match(src, /pinned/i, "the surface does not say bindings are pinned");
  const offered = /offered_skills: \[([^\]]*)\]/s.exec(src);
  assert.ok(offered, "the offered set is not declared where it can be checked");
  const entries = offered[1].split("{").filter((s) => s.includes("ref:"));
  for (const e of entries) {
    assert.match(e, /version_id: "/, `an offered skill has no pinned version: ${e.trim()}`);
  }
});

// ── 9.11 a reorder is a real change, and three verdicts stay three ──────────────────────────────

test("9.11 a skill reorder is presented as a real change, not as tidying", async () => {
  const src = (await read(SELECTION)).replace(/\s+/g, " ");
  assert.match(src, /reorder/i, "reordering is not mentioned at all");
  assert.match(src, /two orders are two configurations/i,
    "the surface does not say that reordering changes the configuration");
  assert.match(src, /identity-bearing|two hashes/i,
    "the surface does not say a reorder re-hashes");
  // 🚫 And it must not be described as cosmetic.
  assert.ok(!/cosmetic change|just reorder|simply reorder/i.test(src),
    "the surface describes a reorder as cosmetic");
});

test("9.11 the three preflight verdicts render through the shared three-state component", async () => {
  const src = await read(SELECTION);
  // Reusing PreflightPanel is what keeps this surface's three states identical to every other's. A
  // local re-implementation is how one surface ends up rendering two.
  assert.match(src, /import \{[^}]*PreflightPanel/s, "this surface does not use the shared verdict component");
  assert.ok(!/banner--warn|banner--bad/.test(src),
    "the surface hand-rolls a tone rather than letting the verdict component choose it");
});

// ── 9.12 capability parity on the configure surface ─────────────────────────────────────────────

test("9.12 adding skills-and-tools authoring removed nothing from the configure surface", async () => {
  const page = await read(CONFIGURE_PAGE);

  // The Configurator — everything the configure surface could already do — must still be mounted.
  assert.match(page, /import \{ Configurator \}/, "the configure page no longer imports the Configurator");
  assert.match(page, /<Configurator \/>/, "the configure page no longer renders the Configurator");
  assert.match(page, /id: "override"/, "the original surface lost its tab");

  // And the file it lives in still exists, with its behaviour intact.
  const configurator = await read("src/app/app/configure/configurator.tsx");
  assert.ok(configurator.length > 1000, "configurator.tsx was gutted");

  // The new tab is additive.
  assert.match(page, /id: "selection"/, "the configure page did not gain the selection tab");
});

test("9.12 the selection surface derives nothing", async () => {
  const src = await read(SELECTION);
  assert.ok(!/\.sort\(\s*\(/.test(src), "the surface sorts client-side");
  assert.ok(!/\.reduce\(/.test(src), "the surface aggregates client-side");
  assert.ok(!/Math\.(max|min|round|abs)\(/.test(src), "the surface computes a figure client-side");

  // No ranking artefact unless the line negates it.
  const ranked = /\b(score|winner|rank|ranked|best|promotion)\b/i;
  const negated = /\b(no|not|never|without|nothing|unmeasured)\b/i;
  for (const [i, line] of src.split("\n").entries()) {
    if (ranked.test(line) && !negated.test(line)) {
      assert.fail(`${SELECTION}:${i + 1} carries a ranking artefact without negating it: ${line.trim()}`);
    }
  }
});

test("9.12 an unverified prune claims no saving on the surface", async () => {
  // Prose is line-wrapped in JSX, so the source is normalised before matching. An assertion that
  // depended on where a line happened to break would fail on a reformat and say nothing about the copy.
  const src = (await read(SELECTION)).replace(/\s+/g, " ");
  assert.match(src, /not reported as a saving/i,
    "the surface does not say a prune's visible token drop is not a saving");
  assert.match(src, /task success is unmeasured/i,
    "the surface does not say what the token count cannot see");
});

// ── the page is reachable, and the file exists where the task says ──────────────────────────────

test("9.9–9.12 the selection surface exists where the tasks point", async () => {
  const files = await readdir(join(root, "src/app/app/configure"));
  assert.ok(files.includes("selection.tsx"), "no selection surface under configure/");
});
