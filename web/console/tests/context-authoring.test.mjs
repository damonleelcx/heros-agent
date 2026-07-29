// context-authoring.test.mjs — P16 16c frontend guards (tasks 8.11–8.15).
//
// Two properties, and the second is the one the product's honesty rests on:
//
//   the drop gate's THREE verdicts must render as three states — and "we have not measured this" must
//   not wear the refusal's tone, because it points the reader somewhere else entirely;
//
//   the drop ratio must be described as INFORMATION DISCARDED, never as a saving. A lossier policy
//   shows fewer tokens, and a falling token count next to a green arrow is the easiest chart in the
//   product to draw and the most misleading one it could carry.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const flat = async (rel) => (await read(rel)).replace(/\s+/g, " ");

/**
 * copy returns only what the surface RENDERS: block and line comments are stripped first.
 *
 * The forbidden-word checks below are about the words a reader sees. An earlier version scanned the raw
 * source and flagged this component's own doc comment, which explains the "tokens saved" failure mode in
 * order to prevent it — a check that punishes the explanation of a rule is a check that gets the rule
 * deleted.
 */
const copy = async (rel) =>
  (await read(rel))
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/^\s*\/\/.*$/gm, " ")
    .replace(/\s+/g, " ");

const AUTHORING = "src/app/app/context/authoring.tsx";
const CONTEXT_PAGE = "src/app/app/context/page.tsx";

// ── 8.11 three verdicts, three states ───────────────────────────────────────────────────────────

test("8.11 the drop gate's three verdicts render as three distinct states", async () => {
  const src = await read(AUTHORING);

  for (const verdict of ["admissible", "refused", "not_yet_measurable"]) {
    assert.ok(src.includes(`verdict: "${verdict}"`), `the ${verdict} verdict is not rendered`);
  }
  // Rendered through the shared component, so this surface's three states are identical to every
  // other's. A local re-implementation is how one surface ends up with two.
  assert.match(src, /import \{[^}]*PreflightPanel/s, "the surface does not use the shared verdict component");

  const flatSrc = await flat(AUTHORING);
  // 🔴 The third state must not be dressed as a refusal — and the surface must say what is missing.
  assert.match(flatSrc, /Not yet measurable/i, "there is no not-yet-measurable section");
  assert.match(flatSrc, /context_drop_ratio/, "the missing measurement is not named");
  assert.match(flatSrc, /where we do not, it says so rather than guessing in either direction/i,
    "the surface does not say the gate refuses to guess");
});

test("8.11 the over-tolerance refusal shows both numbers", async () => {
  const src = await flat(AUTHORING);
  // Either number could be the thing to change. "Exceeds tolerance" alone gives the reader neither.
  assert.match(src, /0\.20/, "the declared tolerance is not shown");
  assert.match(src, /0\.62/, "the measured ratio is not shown");
  assert.match(src, /relax the tolerance .* or pick a policy that discards less/i,
    "the surface does not say what to do about it");
  assert.match(src, /before any evaluation spend/i,
    "the surface does not say the refusal happened before spend");
});

// ── 8.12 🔴 loss is never a saving ──────────────────────────────────────────────────────────────

test("8.12 the drop ratio is described as information discarded, never as a saving", async () => {
  const src = await copy(AUTHORING);

  assert.match(src, /information discarded/i, "the drop ratio is not described as loss");
  assert.match(src, /never as a saving/i, "the surface does not deny the saving reading");
  assert.match(src, /discarded the answer looks exactly like one that discarded filler/i,
    "the surface does not say why a token count cannot judge a policy");

  // 🚫 The words that would invert the meaning, allowed ONLY inside a clause that denies them.
  for (const forbidden of ["tokens saved", "cost saved", "savings", "cheaper", "more efficient"]) {
    const clauses = src.match(new RegExp(`[^.]*\\b${forbidden}\\b[^.]*\\.`, "gi")) ?? [];
    for (const clause of clauses) {
      assert.match(clause, /\b(not|never|rather than|instead of)\b/i,
        `"${forbidden}" is used as a claim rather than denied: ${clause.trim()}`);
    }
  }
});

test("8.12 the gate is described as a measurement, not a guarantee", async () => {
  const src = await flat(AUTHORING);
  assert.match(src, /A measured check, not a guarantee/i,
    "the surface presents the drop gate as a guarantee");
  assert.match(src, /not a promise that no context will ever be lost/i,
    "the surface does not deny the guarantee reading explicitly");
});

// ── 8.13 the language boundary is stated ────────────────────────────────────────────────────────

test("8.13 a node whose language has no rewriter states the boundary with the language named", async () => {
  const src = await flat(AUTHORING);
  assert.match(src, /no rewriter for kotlin has landed yet/,
    "the refusal does not name the language");
  assert.match(src, /covered today: go, python/, "the refusal does not say what is covered");
  // And it must say WHY context needs a rewriter at all, or the boundary reads as arbitrary.
  assert.match(src, /how the surrounding code builds the message list/i,
    "the refusal does not explain why a context policy is a code rewrite");
});

// ── 8.14 retrieval params are classifier-gated, and the label is not settable ───────────────────

test("8.14 retrieval parameters are gated by the classifier, with the reason stated", async () => {
  const src = await flat(AUTHORING);
  assert.match(src, /Offered only on a node the classifier labels as retrieval/i,
    "the classifier gate is not stated");
  assert.match(src, /the reason is stated/i,
    "the surface does not say the reason is given when parameters are absent");
  assert.match(src, /would read as a missing feature/i,
    "the surface does not say why an absent control is the wrong design");

  // 🔴 The label must be stated as NOT settable, with the reason.
  assert.match(src, /label is <strong>not<\/strong> settable|label is not settable/i,
    "the surface does not say the classifier label cannot be set here");
  assert.match(src, /attributed to parameters that did nothing/i,
    "the surface does not say why relabelling would be harmful");
});

// ── 8.15 capability parity + no client derivation ───────────────────────────────────────────────

test("8.15 adding authoring removed no existing capability from the context surface", async () => {
  const page = await read(CONTEXT_PAGE);

  for (const [id, marker] of [
    ["applied", "<AppliedTab />"],
    ["drop", "<DropTab />"],
    ["retrieval", "<RetrievalTab />"],
  ]) {
    assert.ok(page.includes(`id: "${id}"`), `the context surface lost its "${id}" tab`);
    assert.ok(page.includes(marker), `the context surface no longer renders ${marker}`);
  }
  assert.match(page, /\.\.\.DECLINES\.map/, "the worked decline examples were removed");
  assert.match(page, /id: "authoring"/, "the context surface did not gain the authoring tab");
});

test("8.15 the context authoring surface derives nothing", async () => {
  const src = await read(AUTHORING);
  assert.ok(!/\.sort\(\s*\(/.test(src), "the surface sorts client-side");
  assert.ok(!/\.reduce\(/.test(src), "the surface aggregates client-side");
  assert.ok(!/Math\.(max|min|round|abs)\(/.test(src), "the surface computes a figure client-side");

  const ranked = /\b(score|winner|rank|ranked|best|promotion)\b/i;
  const negated = /\b(no|not|never|without|nothing|unmeasured)\b/i;
  for (const [i, line] of src.split("\n").entries()) {
    if (ranked.test(line) && !negated.test(line)) {
      assert.fail(`${AUTHORING}:${i + 1} carries a ranking artefact without negating it: ${line.trim()}`);
    }
  }
});

test("8.15 only registered policies are offered, with no free-text path", async () => {
  const src = await read(AUTHORING);
  assert.match(src, /const POLICIES = \[/, "the offered policies are not declared where they can be checked");
  assert.ok(!/<input\b/i.test(src), "the surface renders a text input on the policy path");
  assert.ok(!/<textarea\b/i.test(src), "the surface renders a textarea on the policy path");
  const flatSrc = await flat(AUTHORING);
  assert.match(flatSrc, /a policy nothing resolves is not a choice/i,
    "the surface does not say why only registered policies are offered");
});
