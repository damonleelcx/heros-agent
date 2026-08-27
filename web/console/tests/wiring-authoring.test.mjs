// wiring-authoring.test.mjs — P15 15d frontend guards (tasks 19.10–19.14).
//
// This axis is where the console's honesty is easiest to lose, because a graph editor implies a freedom
// the engine does not have. Three properties are protected here, in descending order of how much damage
// their loss does:
//
//   a refused draft must NEVER be presented as a variant or as pending — it is unscoreable, and showing
//   it as awaiting a number promises a measurement that would be false if produced;
//
//   an inserted adapter must be visible BEFORE submission — otherwise a component appears in the diff
//   that the author never agreed to;
//
//   an incoherence refusal must name the consumer, the producer AND the field — a graph error with no
//   names leaves the reader staring at a diagram.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const flat = async (rel) => (await read(rel)).replace(/\s+/g, " ");

const EDITOR = "src/app/app/graph/editor.tsx";
const WIRING_PAGE = "src/app/app/graph/page.tsx";

// ── 19.10 a verdict per gesture, not on submit ──────────────────────────────────────────────────

test("19.10 every gesture carries its own verdict, decided before submission", async () => {
  const src = await read(EDITOR);

  // Each gesture has a verdict attached to it, rather than one verdict for the page.
  assert.match(src, /GESTURES\s*:\s*Gesture\[\]/, "gestures are not modelled with their verdicts");
  assert.match(src, /PreflightPanel/, "verdicts are not rendered through the shared three-state component");

  // All the outcome classes appear, so a reader learns the axis rather than only its refusals.
  const flatSrc = await flat(EDITOR);
  for (const outcome of ["admissible", "refused"]) {
    assert.ok(flatSrc.includes(`verdict: "${outcome}"`), `no ${outcome} gesture is shown`);
  }
  // 🔴 The CLAIM moved to the reading surface (P37, block-inventory G9); the PROPERTY it describes is
  // still enforced here, by the section heading the reader sees and by the gate the editor calls.
  assert.match(flatSrc, /Every gesture gets its verdict as you make it/i,
    "the editor does not say the verdict arrives as the gesture is made");
  const doc = await read("content/docs/en/concepts/graph-and-wiring.md");
  assert.match(doc, /each one gets its verdict \*as you make it\*/i,
    "the destination does not carry the before-submission claim");
});

test("19.10 the editor uses the same gate as the compiler, and says so", async () => {
  const src = await flat(EDITOR);
  assert.match(src, /same coherence gate the compiler runs/i,
    "the surface does not state that the editor and the compiler share one gate");
});

// ── 19.11 the adapter is visible before submission ──────────────────────────────────────────────

test("19.11 an inserted adapter is rendered as a visible node with its edge, before submit", async () => {
  const src = await read(EDITOR);
  assert.match(src, /function AdapterNodes/, "there is no component that renders an inserted adapter");
  assert.match(src, /adp_retrieve_summarize_rename/, "no adapted gesture is shown");

  const flatSrc = await flat(EDITOR);
  // It must be shown as a NODE on an EDGE, not merely mentioned.
  assert.match(flatSrc, /the adapter node that would be inserted/, "the adapter is not labelled as a node");
  assert.match(flatSrc, /the edge it sits on/, "the adapter's edge is not shown");
  // And the reason it is shown at all must be stated, or a later edit will drop it as noise.
  assert.match(flatSrc, /the change you submit is the change you saw/i,
    "the surface does not say why the adapter is shown before submission");
  assert.match(flatSrc, /not a hidden runtime coercion/i,
    "the surface does not distinguish an inserted node from a hidden coercion");
});

// ── 19.12 🔴 a refused draft is not a variant, and never 'pending' ──────────────────────────────

test("19.12 a refused rearrangement is never shown as a variant or as pending", async () => {
  const src = await flat(EDITOR);

  assert.match(src, /is <strong>not<\/strong> a variant|not a variant/i,
    "the surface does not say a refused rearrangement is not a variant");
  assert.match(src, /no\s*configuration hash/i,
    "the surface does not say a recorded intent has no configuration hash");
  assert.match(src, /never evaluated/i, "the surface does not say it is never evaluated");

  // 🚫 The words that imply a measurement is coming. Each may appear ONLY inside a clause that denies
  // it — "not queued for anything" is correct copy, and an earlier version of this test failed it.
  // A ban that cannot tell an assertion from its denial is a ban that punishes the right sentence.
  for (const forbidden of ["awaiting evaluation", "queued for", "pending evaluation", "in the queue", "pending"]) {
    const clauses = src.match(new RegExp(`[^.]*\\b${forbidden}\\b[^.]*\\.`, "gi")) ?? [];
    for (const clause of clauses) {
      assert.match(clause, /\b(not|never|nor|would imply|calling it|rather than)\b/i,
        `"${forbidden}" is used as a state rather than denied: ${clause.trim()}`);
    }
  }

  // 🔴 And the REASON must be the real one — a false measurement, not tidiness. P37 moved the paragraph
  // to the shared refusal page (it is identical for every reader) and the editor links to it, so the
  // claim is asserted at both ends: the destination carries it, and the surface reaches it.
  const refusals = await read("content/docs/en/concepts/refusals.md");
  assert.match(refusals, /## A refusal is not a queue/i, "the destination lost the recorded-intent rule");
  assert.match(refusals, /never \*\*scored\*\*|never be scored|never .{0,20}scored/i,
    "the destination does not say why a refused draft is not evaluated");
  assert.match(src, /refusals#a-refusal-is-not-a-queue/,
    "the editor carries no link to the section its paragraph moved to");
});

test("19.12 each gesture states whether it can be evaluated at all", async () => {
  const src = await read(EDITOR);
  assert.match(src, /function ScoreabilityNote/, "no per-gesture scoreability is rendered");
  assert.match(src, /scoreable: false/, "no gesture is marked unscoreable");
  assert.match(src, /scoreable: true/, "no gesture is marked scoreable — the distinction would be vacuous");
});

// ── 19.13 the incoherence refusal names all three ───────────────────────────────────────────────

test("19.13 an incoherence refusal names the consumer, the producer and the field", async () => {
  const src = await read(EDITOR);
  assert.match(src, /function BreakHighlight/, "there is no component that names the break");

  const flatSrc = await flat(EDITOR);
  for (const name of ["consumer:", "producer:", "field:"]) {
    assert.ok(flatSrc.includes(name), `the break does not label the ${name.replace(":", "")}`);
  }
  // The three concrete names must be present in the worked example.
  for (const name of ["summarize", "retrieve", "passages"]) {
    assert.ok(flatSrc.includes(name), `the worked break does not name ${name}`);
  }
  // And it must offer a next step, or naming them changes nothing.
  assert.match(flatSrc, /Move .* back after|give it another source/i,
    "the break names the problem without offering a next step");
  // Highlighted in the graph, not only in a toast.
  assert.match(flatSrc, /highlighted in the graph/i,
    "the break is not highlighted where the reader is looking");
});

// ── 19.14 capability parity + tokens ────────────────────────────────────────────────────────────

test("19.14 P37 removed no capability from the wiring surface — each one has a destination", async () => {
  const page = await read(WIRING_PAGE);
  const doc = await read("content/docs/en/concepts/graph-and-wiring.md");

  // 🔴 P15 required the axis and applied tabs to be PRESENT ON THE PAGE. P37's delta to
  // `axis-node-projection` changes the unit from "the same page" to "a named destination": each one is
  // on the reading surface, labelled as the platform's fixture, and the page links there.
  for (const [gone, destination] of [
    ["<AxisTab />", "## The four wiring operations"],
    ["<AppliedTab />", "### An applied transposition"],
    ["...EXAMPLES.map", "### A declined reorder"],
    ["<TopologyTab />", "## The three topology forms"],
  ]) {
    assert.ok(!page.includes(gone), `${gone} is still on the working surface`);
    assert.ok(doc.includes(destination), `${gone} has no destination — a panel with no destination is not cut`);
  }
  // The engine's own sentences travelled verbatim; a paraphrase would be documenting a page that does
  // not exist.
  assert.match(doc, /control-flow surgery, not the value replacement this engine performs/,
    "the engine's refusal wording was paraphrased on the way to its destination");

  // 🔴 The EDITOR is not moved — it is interactive, and a markdown document cannot hold it. It stays,
  // relabelled as the platform's fixture so it does not occupy the reader's data position (FR4).
  assert.match(page, /id: "editor"/, "the wiring surface lost its editor");
  assert.match(page, /the platform's fixture/i, "the editor is not labelled as a fixture");
  assert.match(page, /<AxisFrame axis="graph"/, "the surface is not bound to the reader's own node");
  assert.match(page, /id: "this-node"/, "the reader's own node has no tab");
});

test("19.14 the editor derives nothing and ranks nothing", async () => {
  const src = await read(EDITOR);
  assert.ok(!/\.sort\(\s*\(/.test(src), "the editor sorts client-side");
  assert.ok(!/\.reduce\(/.test(src), "the editor aggregates client-side");
  assert.ok(!/Math\.(max|min|round|abs)\(/.test(src), "the editor computes a figure client-side");

  const ranked = /\b(score|winner|rank|ranked|best|promotion)\b/i;
  const negated = /\b(no|not|never|without|nothing|unscoreable|unmeasured)\b/i;
  for (const [i, line] of src.split("\n").entries()) {
    if (ranked.test(line) && !negated.test(line)) {
      assert.fail(`${EDITOR}:${i + 1} carries a ranking artefact without negating it: ${line.trim()}`);
    }
  }
});

// ── 9.4 the outcome cards have a linkable, renderable page ──────────────────────────────────────
//
// P15 task 9.4. `inventory.test.mjs` (P15-2…P15-4) proves the refusal COMPONENT says the right words.
// That is not the same claim as "the card renders": a green build is entirely compatible with a page
// that renders nothing, and the declined-change card is the wiring axis's most common user-facing
// state. This guards the preview surface that makes it checkable in a browser.

const WIRING_PREVIEW = "src/app/preview/wiring/page.tsx";

test("9.4 the preview renders the SAME outcome cards the submit path renders", async () => {
  const src = await read(WIRING_PREVIEW);

  // The load-bearing assertion: the components are IMPORTED from the shared module, not reimplemented.
  // A copy would drift the first time the card changed, and this page would become a picture of a
  // surface rather than the surface — which is exactly the failure a preview exists to prevent.
  assert.match(
    src,
    /import \{[^}]*AxisRefusal[^}]*\} from "@\/components\/axisRefusal"/,
    "the preview must render the shared refusal card, the one configurator.tsx renders on a 400",
  );
  assert.match(src, /<AxisRefusal\b/, "the preview imports the refusal card but never renders it");

  // The applied case is here for the same reason /app/wiring leads with it: four refusals in a row read
  // as a broken feature however carefully each one is worded.
  assert.match(src, /<AxisApplied\b/, "the preview shows only refusals, teaching that the axis never applies");
});

test("9.4 one linkable fixture per shape, and the un-annotated axis is one of them", async () => {
  const src = await read(WIRING_PREVIEW);

  // Each shape is addressable by URL. A state reachable only by clicking never reaches a PR
  // description, a bug report, or a screenshot pipeline — so it is the state nobody checks.
  for (const shape of ["reorder", "merge", "edge", "unknown"]) {
    assert.match(src, new RegExp(`id:\\s*"${shape}"`), `no fixture for the ${shape} shape`);
  }
  assert.match(src, /\/preview\/wiring\?tab=\$\{/, "the fixtures are not linkable by URL");

  // 🔴 The un-annotated axis is the load-bearing fixture: it must be an axis AXIS_NOTE does not carry,
  // because what it proves is that an unrecognised refusal still renders rather than being swallowed.
  const notes = await read("src/components/axisRefusal.tsx");
  const unknownAxis = src.match(/id:\s*"unknown"[\s\S]*?axis:\s*"([a-z]+)"/)?.[1];
  assert.ok(unknownAxis, "the un-annotated fixture does not declare an axis");
  assert.ok(
    !new RegExp(`^\\s{2}${unknownAxis}:`, "m").test(notes),
    `the "un-annotated" fixture uses ${unknownAxis}, which AXIS_NOTE DOES annotate — so it proves nothing`,
  );

  // The picker is navigation, not a second tablist: a tablist inside a tablist is two roving tab-stops
  // fighting over the arrow keys.
  assert.match(src, /aria-label="Preview fixture"/, "the fixture picker is not links");
  const code = src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  assert.equal((code.match(/<Tabs\b/g) ?? []).length, 0, "the preview must not nest a tablist in the picker");
});
