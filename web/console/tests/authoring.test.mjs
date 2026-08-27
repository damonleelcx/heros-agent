// authoring.test.mjs — P13 13c section 11 guards on the authoring surface, as FAILING TESTS rather
// than review notes.
//
// Each test here corresponds to one task's evidence pointer:
//
//   11.1  studio capability parity        — adding authoring removed nothing
//   11.2  studio authoring                — model + parameter authoring, cross-provider stated not silent
//   11.3  preflight three-state           — three verdicts render as three states
//   11.4  no client derivation            — the surface computes nothing
//   11.5  unverified label                — the word travels with every authored change
//   11.6  nav slots                       — route + navigation entry + permission gate, all three

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");
/** readContent reads a reading-surface document — where P37 moved the fixtures this file used to assert. */
const readContent = (rel) => readFile(join(root, rel), "utf8");

const AUTHORING = "src/components/authoring.tsx";
const PAGE = "src/app/app/authoring/page.tsx";
const STUDIO_AUTHORING = "src/app/app/studio/authoring.tsx";
const STUDIO_PAGE = "src/app/app/studio/page.tsx";
const LAYOUT = "src/app/app/layout.tsx";

// ── 11.1 capability parity ──────────────────────────────────────────────────────────────────────

test("11.1 adding authoring to the studio removed no capability it already had", async () => {
  const page = await read(STUDIO_PAGE);

  // Every tab that existed before 13c must still be mounted, by id AND by component. A UI revision
  // that adds a capability while quietly moving another is indistinguishable in review from one that
  // adds a capability — and the one that gets lost is always the old one, because nobody looks for it.
  for (const [id, component] of [
    ["matrix", "StudioMatrix"],
    ["library", "Studio"],
    ["bound", "BoundModeShowcase"],
  ]) {
    assert.ok(page.includes(`id: "${id}"`), `studio lost its "${id}" tab`);
    assert.ok(page.includes(`<${component} `) || page.includes(`<${component}/`) || page.includes(`<${component} />`),
      `studio no longer renders ${component}`);
  }
  // And the new one is present rather than replacing one of them.
  assert.ok(page.includes(`id: "author"`), "the studio did not gain the authoring tab");

  // The files those tabs live in must still exist.
  for (const f of ["src/app/app/studio/matrix.tsx", "src/app/app/studio/studio.tsx", "src/app/app/studio/boundmode.tsx"]) {
    await read(f); // throws if the file was deleted
  }
});

// ── 11.2 model + parameter authoring, and the stated boundary ───────────────────────────────────

test("11.2 the studio authors a MODEL for the reader's own node, not for a fixture", async () => {
  const src = await read(STUDIO_AUTHORING);

  // 🔴 P13 11.2 asserted a fixture table — three nodes, a hard-coded model list, a fixture refusal.
  // P37 bound the surface to the reader's own node, so what this test asserts moved from "the table is
  // present" to "the table is the reader's". The capability did not change; its subject did.
  assert.match(src, /subject: AxisSubject/, "the studio's authoring tab is not bound to a subject");
  assert.match(src, /<AxisEditor/, "model authoring does not go through the shared editor kit");
  assert.match(src, /ApplyModeNote/, "the apply-mode boundary is not rendered at the point of choice");
  assert.match(src, /What this node can carry/i, "provider-parameter carriage is not stated per node");

  // And the fixtures are gone from the reader's data position (FR4).
  for (const fixture of ["const NODES", "const OFFERED", "const PARAM_REFUSAL"]) {
    assert.ok(!src.includes(fixture), `${fixture} still occupies the reader's data position`);
  }

  // Their destination, because nothing is deleted.
  const doc = await readContent("content/docs/en/concepts/prompt-and-model-studio.md");
  assert.match(doc, /## Worked example/, "the fixture nodes have no destination section");
  assert.match(doc, /claude-haiku-4-5/, "the offered-model fixture was lost rather than moved");
  assert.match(doc, /## Provider parameters/, "the parameter refusal has no destination section");
});

test("11.2 cross-provider models are shown DISABLED, never offered and never hidden", async () => {
  const src = await read(STUDIO_AUTHORING);

  // 🔴 P13 checked a literal list for foreign model ids. After P37 the list is the platform's registered
  // catalogue, so a literal check would be checking nothing — the rule has to be enforced where the
  // options are BUILT. `modelVocabularyFrom` marks a foreign-provider model `unavailableReason`, and the
  // kit renders `unavailableReason` as `disabled` with the reason beside it.
  const shapes = await read("src/lib/axisVocabularyShapes.ts");
  assert.match(
    shapes,
    /provider && m\.provider && m\.provider !== provider/,
    "the model list is not filtered by the call site's own provider — a cross-provider swap would compile " +
      "and then call the wrong provider in production",
  );
  assert.match(
    shapes,
    /a call site written against the \$\{m\.provider\} SDK/,
    "an unavailable model does not name what it would need",
  );
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /disabled=\{unavailable\}/, "an unavailable option is not rendered disabled");
  assert.match(kit, /needs \{o\.unavailableReason\}/, "a disabled option does not name its reason");

  // A silently short list reads as an incomplete catalogue. The boundary must still be stated.
  assert.match(src, /ProviderBoundary/, "the provider boundary is not stated where the choice is made");
  const boundary = await read(AUTHORING);
  assert.match(
    boundary,
    /Only .* models are offered for this call site/,
    "the boundary component does not say which provider is offered",
  );
});

// ── 11.3 three verdicts, three states ───────────────────────────────────────────────────────────

test("11.3 preflight renders three distinct states, and not-yet-measurable is not a refusal", async () => {
  const src = await read(AUTHORING);

  // Three branches, each its own component. One component with three tones is how three states become
  // two: a later edit keeps the colour and drops the words that differ.
  for (const branch of ["PreflightAdmissible", "PreflightRefused", "PreflightNotYetMeasurable"]) {
    assert.ok(src.includes(`function ${branch}`), `missing the ${branch} state`);
  }
  for (const verdict of ["admissible", "refused", "not_yet_measurable"]) {
    assert.ok(src.includes(`case "${verdict}"`), `the ${verdict} verdict is not handled`);
  }

  // 🔴 The third state must NOT be drawn in the hazard tone and must not use refusal words. "We have
  // not measured this" and "you may not do this" lead a reader to opposite actions.
  const third = src.slice(src.indexOf("function PreflightNotYetMeasurable"));
  const body = third.slice(0, third.indexOf("\n}\n"));
  assert.ok(
    !/tone="warn"|tone="bad"/.test(body),
    "not-yet-measurable is drawn in a hazard tone — it reads as a refusal",
  );
  assert.match(body, /not<\/strong> a refusal|not a refusal/i,
    "the third state does not say it is not a refusal");
  assert.match(body, /gap in\s*<strong>our<\/strong>|our<\/strong> measurements/,
    "the third state does not say the gap is ours rather than the reader's");
  // It must name what would resolve it, or it is a dead end.
  assert.match(body, /missing_kind|missing measurement/,
    "the third state does not name the missing measurement");

  // An unrecognised verdict must not be mapped onto one of the three.
  assert.match(src, /not one this console recognises/i,
    "an unknown verdict is silently mapped onto a known one");
});

// ── 11.4 the surface derives nothing ────────────────────────────────────────────────────────────

test("11.4 the authoring surface computes no score, rank, interval or comparison", async () => {
  for (const rel of [AUTHORING, PAGE, STUDIO_AUTHORING]) {
    const src = await read(rel);

    // No arithmetic on a metric, and no client-side ordering by one. A recomputation here is a second
    // source of truth for a statistical claim, and when the two disagree nobody can tell which lies.
    assert.ok(!/\.sort\(\s*\(/.test(src), `${rel} sorts client-side`);
    assert.ok(!/\.reduce\(/.test(src), `${rel} aggregates client-side`);
    assert.ok(!/Math\.(max|min|abs|round)\(/.test(src), `${rel} computes a figure client-side`);

    // No ranking artefact unless the line negates it.
    const ranked = /\b(score|winner|rank|ranked|promotion|promote)\b/i;
    const negated = /\b(no|not|never|without|unranked|nothing|neither)\b/i;
    for (const [i, line] of src.split("\n").entries()) {
      if (ranked.test(line) && !negated.test(line)) {
        assert.fail(`${rel}:${i + 1} carries a ranking artefact without negating it: ${line.trim()}`);
      }
    }
  }
});

// ── 11.5 the unverified label travels ───────────────────────────────────────────────────────────

test("11.5 every authored-change render carries its verification state", async () => {
  const src = await read(AUTHORING);

  // The label is a component, not a string, so a call site cannot render a change without it.
  assert.ok(src.includes("export function UnverifiedLabel"), "the unverified label is not a component");

  // The change summary must include it — asserted structurally so a future edit that renders a change
  // some other way still fails here.
  const summary = src.slice(src.indexOf("export function AuthoredChangeSummary"));
  const body = summary.slice(0, summary.indexOf("\n}\n"));
  assert.match(body, /<UnverifiedLabel/, "a change can be rendered without its verification state");

  // 🚫 Not in the hazard palette. Danger is only legible while it is rare, and "nobody measured this
  // yet" is a normal state of a perfectly good change.
  const label = src.slice(src.indexOf("export function UnverifiedLabel"));
  const labelBody = label.slice(0, label.indexOf("\n}\n"));
  assert.ok(
    !/tone=\{?["']?(warn|bad|halt)/.test(labelBody),
    "the unverified label uses the hazard palette, which is reserved for hazard",
  );

  // And the surface must say what unverified MEANS, not merely display the word.
  assert.match(src, /contributes nothing to any improvement or savings figure/i,
    "the surface shows the word 'unverified' without saying what it means");
});

// ── 11.6 route + navigation entry + permission gate, all three ──────────────────────────────────

test("11.6 the new surface is wired in all three places, so no slot goes silently missing", async () => {
  // 1. the route exists as a page
  const pages = await readdir(join(root, "src/app/app/authoring"));
  assert.ok(pages.includes("page.tsx"), "no page at /app/authoring");

  // 2. it is reachable by navigation — FR9: a surface reachable only by typing its URL is one most
  //    readers never learn exists.
  const layout = await read(LAYOUT);
  assert.match(layout, /href: "\/app\/authoring"/, "the surface has no navigation entry");
  assert.match(layout, /id: "s:authoring"/, "the surface is missing from the command path");

  // 3. it is in the capability table, so a reader who lacks it is told which plan carries it rather
  //    than finding the surface absent.
  const ents = await read("src/lib/entitlements.ts");
  assert.match(ents, /id: "authoring"/, "authoring has no capability row");

  // and it is a canonical route rather than a hand-written path
  const routes = await read("src/lib/routes.ts");
  assert.match(routes, /authoring: \(\) => "\/app\/authoring"/, "the route is not canonical");
});

test("11.6 there is no override, at any tier — and it is stated where a reader meets a refusal", async () => {
  // 🔴 P13 asserted the sentence on `/app/authoring`, where it was one of five restatements of a rule
  // that is identical on every axis. P37 moved the restatement to the shared document (it is the same
  // for every reader) and the surface links to it — so the claim is asserted at BOTH ends: the document
  // must carry it, and the surface must reach it. A claim with a link and no destination is the 404
  // nobody reports.
  const doc = await readContent("content/docs/en/concepts/authored-changes.md");
  assert.match(doc, /## There is no override/i, "the shared contract no longer states that a refusal has no override");
  assert.match(
    doc,
    /No plan, role or setting materialises a change the engine refuses/i,
    "the sentence itself is gone; a heading with no claim under it is not a statement",
  );
  const refusals = await readContent("content/docs/en/concepts/refusals.md");
  assert.match(refusals, /## A refusal is not a permission problem/i, "the shared refusal page lost the rule");

  const page = await read(PAGE);
  assert.match(
    page,
    /AXIS_DOC\.prompt/,
    "the authoring surface carries no link to the contract it stopped restating",
  );
  // A capability row must not promise past a refusal either.
  const ents = await read("src/lib/entitlements.ts");
  const row = ents.slice(ents.indexOf('id: "authoring"'));
  assert.ok(
    !/override|force|bypass/i.test(row.slice(0, row.indexOf("},"))),
    "the capability row offers an override for a refusal",
  );
});
