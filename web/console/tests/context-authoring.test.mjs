// context-authoring.test.mjs — P16 16c's guards, RE-POINTED at their destinations by P37.
//
// # 🔴 Read this before assuming a test was weakened
//
// Every assertion below was written by P16 against `src/app/app/context/authoring.tsx`, a surface that
// demonstrated the drop gate against the platform's own fixtures. P37 removed that demonstration and
// bound the surface to the reader's own node — and moved the explanation to
// `/docs/concepts/context-policies`.
//
// So each of P16's claims now has a NAMED DESTINATION, and this file asserts it there:
//
//   the three verdicts, the over-tolerance numbers, the retrieval gate, the classifier label, the
//   language boundary, the "information discarded" rule
//       → `content/docs/en/concepts/context-policies.md`  (they are the same for every reader)
//
//   the boundary above the control, the live control, the shared verdict component, no free text,
//   no client derivation
//       → `src/app/app/context/{page,editor}.tsx`         (they change with the reader's data)
//
// That split is the moving rule (P37 FR9) applied to the FENCES rather than only to the prose, and it is
// what `axis-node-projection`'s modified requirement means by "a named destination". A claim with no
// destination would have been a claim P37 deleted, and this file would say so.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const flat = async (rel) => (await read(rel)).replace(/\s+/g, " ");

/**
 * copy returns only what a surface RENDERS: block and line comments are stripped first.
 *
 * The forbidden-word checks below are about the words a reader sees. An earlier version scanned the raw
 * source and flagged a doc comment that explains the "tokens saved" failure mode in order to prevent it
 * — a check that punishes the explanation of a rule is a check that gets the rule deleted.
 */
const copy = async (rel) =>
  (await read(rel))
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/^\s*\/\/.*$/gm, " ")
    .replace(/\s+/g, " ");

const PAGE = "src/app/app/context/page.tsx";
const EDITOR = "src/app/app/context/editor.tsx";
const KIT = "src/components/editorKit.tsx";
const DOC = "content/docs/en/concepts/context-policies.md";

// ── 8.11 three verdicts, three states ───────────────────────────────────────────────────────────

test("8.11 the drop gate's three verdicts are explained at their destination", async () => {
  const doc = await flat(DOC);
  for (const verdict of ["admissible", "refused", "not_yet_measurable"]) {
    assert.match(doc, new RegExp(verdict.replace(/_/g, "[ _]")), `the ${verdict} verdict is not explained`);
  }
  // 🔴 The third state must not be dressed as a refusal, and the document must say what is missing.
  assert.match(doc, /not yet measurable/i, "there is no not-yet-measurable explanation");
  assert.match(doc, /context_drop_ratio/, "the missing measurement is not named");
  assert.match(
    doc,
    /Drawn as a refusal it points the reader at their own configuration/i,
    "the document does not say why the third verdict must not wear the refusal's clothes",
  );
});

test("8.11 the READER's own verdict renders through the shared component, in its own state", async () => {
  // The surface's job after P37 is to render the reader's REAL verdict, not to demonstrate three. That
  // is `PreflightPanel`, reached through the kit — a local re-implementation is how one surface ends up
  // with two vocabularies for one refusal.
  const kit = await read(KIT);
  assert.match(kit, /import \{[^}]*PreflightPanel/s, "the kit does not use the shared verdict component");
  assert.match(kit, /\{result \? <PreflightPanel result=\{result\} \/> : null\}/,
    "the kit does not render the platform's verdict");
  // 🔴 And a transport failure is NEVER rendered as a refusal. Nothing went wrong when the engine
  // refuses; something did when the platform did not answer, and retrying helps in exactly one of them.
  assert.match(kit, /A transport failure is NOT a refusal/, "the kit does not separate the two");
});

test("8.11 the over-tolerance refusal's two numbers are explained at their destination", async () => {
  const doc = await flat(DOC);
  // Either number could be the thing to change. "Exceeds tolerance" alone gives the reader neither.
  assert.match(doc, /0\.20/, "the declared tolerance is not shown");
  assert.match(doc, /0\.62/, "the measured ratio is not shown");
  assert.match(doc, /relax the tolerance .{0,80}or pick a policy that discards less/i,
    "the document does not say what to do about it");
  assert.match(doc, /before any evaluation spend/i,
    "the document does not say the refusal happens before spend");
});

// ── 8.12 🔴 loss is never a saving ──────────────────────────────────────────────────────────────

test("8.12 the drop ratio is described as information discarded, never as a saving", async () => {
  // Asserted on the DOCUMENT (where the explanation lives) and enforced on the SURFACE (where a
  // regression would actually mislead somebody).
  const doc = await copy(DOC);
  assert.match(doc, /information discarded/i, "the drop ratio is not described as loss");
  assert.match(doc, /never as a saving/i, "the document does not deny the saving reading");
  assert.match(doc, /discarded the answer looks exactly like one that discarded filler/i,
    "the document does not say why a token count cannot judge a policy");

  // 🚫 The words that would invert the meaning, allowed ONLY inside a clause that denies them — on the
  // document AND on the working surface, because the surface is where a reader is about to act.
  for (const source of [doc, await copy(PAGE), await copy(EDITOR)]) {
    for (const forbidden of ["tokens saved", "cost saved", "savings", "cheaper", "more efficient"]) {
      const clauses = source.match(new RegExp(`[^.]*\\b${forbidden}\\b[^.]*\\.`, "gi")) ?? [];
      for (const clause of clauses) {
        assert.match(clause, /\b(not|never|rather than|instead of)\b/i,
          `"${forbidden}" is used as a claim rather than denied: ${clause.trim()}`);
      }
    }
  }
});

test("8.12 the gate is described as a measurement, not a guarantee", async () => {
  const doc = await flat(DOC);
  assert.match(doc, /It never rejects a policy simply because nothing has measured it yet/i,
    "the document presents the drop gate as a guarantee");
  assert.match(doc, /we have no data.{0,40}must not come to mean/i,
    "the document does not deny the guarantee reading explicitly");
});

// ── 8.13 the language boundary is stated, and now it is the READER's language ───────────────────

test("8.13 a node whose language has no rewriter states the boundary WITH the language named", async () => {
  const editor = await flat(EDITOR);
  // 🔴 P16 asserted a FIXTURE's language (`kotlin`). After P37 the surface names the READER's own node's
  // language, which is strictly stronger: it is the one they can act on.
  assert.match(editor, /subject\.language \|\| "this language"/,
    "the boundary does not name the reader's own language");
  assert.match(editor, /has not landed/i, "the boundary does not say the rewriter is missing");
  assert.match(editor, /This one is ours/i, "the boundary does not say whose move it is");
  // And the document says WHY context needs a rewriter at all, or the boundary reads as arbitrary.
  const doc = await flat(DOC);
  assert.match(doc, /how the surrounding code builds the message list/i,
    "the document does not explain why a context policy is a code rewrite");
  assert.match(doc, /the selection rewriter has landed for `go` and `python`/i,
    "the document does not say which languages are covered");
});

// ── 8.14 retrieval params are classifier-gated, and the label is not settable ───────────────────

test("8.14 retrieval parameters are gated by the classifier, with the reason stated", async () => {
  const doc = await flat(DOC);
  assert.match(doc, /proposed only on a node the classifier labels as retrieval/i,
    "the classifier gate is not stated");
  assert.match(doc, /the reason is stated/i,
    "the document does not say the reason is given when parameters are absent");
  assert.match(doc, /would read as a missing feature/i,
    "the document does not say why an absent control is the wrong design");

  // 🔴 The label must be stated as NOT settable, with the reason.
  assert.match(doc, /label is \*\*not\*\* settable/i,
    "the document does not say the classifier label cannot be set from the console");
  assert.match(doc, /attributed to parameters that did nothing/i,
    "the document does not say why relabelling would be harmful");
});

// ── 8.15 capability parity + no client derivation ──────────────────────────────────────────────

test("8.15 P37 removed no capability from the context surface — each one has a destination", async () => {
  const page = await read(PAGE);
  const doc = await read(DOC);

  // The three tabs P16 required are gone from the working surface BY DESIGN, and each has a named
  // section on the reading surface. `axis-node-projection` (P37 delta): "every panel present before is
  // present at a named destination", and "a panel with no destination is not removed".
  for (const [gone, destination] of [
    ["AppliedTab", "## Worked examples"],
    ["DropTab", "## What it costs to lose context"],
    ["RetrievalTab", "## Retrieval tuning"],
    ["DECLINES", "### Four declines, in the order the engine considers them"],
  ]) {
    assert.ok(!page.includes(`<${gone} />`), `${gone} is still on the working surface`);
    assert.ok(doc.includes(destination), `${gone} has no destination section — a panel with no destination is not cut`);
  }

  // What REPLACED them is the reader's own node, and the surface links to every destination.
  assert.match(page, /<AxisFrame axis="context"/, "the surface is not bound to a subject");
  assert.match(page, /<ContextEditor/, "the surface has no editor");
  assert.match(page, /<ReadOn href=\{AXIS_DOC\.context\}/, "the surface does not link to its destination");
  assert.match(page, /<AxisProjectionPanel axis="context"/, "the projection panel was dropped");
});

test("8.15 the context surface derives nothing", async () => {
  for (const rel of [PAGE, EDITOR]) {
    const src = await read(rel);
    assert.ok(!/\.sort\(\s*\(/.test(src), `${rel} sorts client-side`);
    assert.ok(!/\.reduce\(/.test(src), `${rel} aggregates client-side`);
    assert.ok(!/Math\.(max|min|round|abs)\(/.test(src), `${rel} computes a figure client-side`);

    const ranked = /\b(score|winner|rank|ranked|best|promotion)\b/i;
    const negated = /\b(no|not|never|without|nothing|unmeasured)\b/i;
    for (const [i, line] of src.split("\n").entries()) {
      if (ranked.test(line) && !negated.test(line)) {
        assert.fail(`${rel}:${i + 1} carries a ranking artefact without negating it: ${line.trim()}`);
      }
    }
  }
});

test("8.15 only registered policies are offered, with no free-text path", async () => {
  const editor = await read(EDITOR);
  // 🔴 STRONGER than P16's check. P16 asserted a local `const POLICIES = [...]` — a literal in a TSX
  // file, which is a second source of truth for a closed set. After P37 the options are derived from the
  // ENGINE's own live coverage, so the surface cannot offer a policy the engine does not carry.
  assert.match(editor, /contextVocabularyFrom\(coverage, setVersion\)/,
    "the policies are not derived from the engine's coverage");
  assert.ok(!/const POLICIES = \[/.test(editor), "a hand-written policy list came back");
  assert.ok(!/<input\b/i.test(editor), "the surface renders a text input on the policy path");
  assert.ok(!/<textarea\b/i.test(editor), "the surface renders a textarea on the policy path");

  const doc = await flat(DOC);
  assert.match(doc, /a policy outside the registered set is not a lesser choice/i,
    "the document does not say why only registered policies are offered");
});
