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

test("11.2 the studio offers model and provider-parameter authoring", async () => {
  const src = await read(STUDIO_AUTHORING);
  assert.match(src, /Set a model yourself/i, "no model authoring on the studio's authoring tab");
  assert.match(src, /Provider parameters/i, "no provider-parameter authoring");
  assert.match(src, /ApplyModeNote/, "the apply-mode boundary is not rendered at the point of choice");
});

test("11.2 cross-provider models are absent AND the boundary is stated, not silently short", async () => {
  const src = await read(STUDIO_AUTHORING);

  // Every offered model must belong to the call site's provider. A single foreign model here would be
  // a diff that compiles and then calls the wrong provider in production.
  const offered = /const OFFERED = \[([^\]]*)\]/s.exec(src);
  assert.ok(offered, "the offered model list is not declared where it can be checked");
  for (const foreign of ["gpt-", "gemini", "llama", "mistral", "command-r"]) {
    assert.ok(
      !offered[1].toLowerCase().includes(foreign),
      `a ${foreign} model is offered for an anthropic call site — a cross-provider swap would compile and call the wrong provider`,
    );
  }

  // A silently short list reads as an incomplete catalogue. The boundary must be stated.
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

test("11.6 the authoring surface states there is no override, at any tier", async () => {
  const page = await read(PAGE);
  assert.match(page, /no override/i, "the surface does not state that a refusal has no override");
  // A capability row must not promise past a refusal either.
  const ents = await read("src/lib/entitlements.ts");
  const row = ents.slice(ents.indexOf('id: "authoring"'));
  assert.ok(
    !/override|force|bypass/i.test(row.slice(0, row.indexOf("},"))),
    "the capability row offers an override for a refusal",
  );
});
