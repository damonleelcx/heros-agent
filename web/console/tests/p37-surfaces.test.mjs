// p37-surfaces.test.mjs is §6's fences over the seven rewritten surfaces (tasks 6.2–6.5, 6.7–6.10).
//
// # What these are for, in one sentence
//
// Six surfaces are rewritten at once, each carrying states that LOOK LIKE DECORATION — `refused` with its
// verbatim cause, `not_measured` with its named input, `unverified`, `not_connected`, and the
// disabled-with-a-reason option. Every one of them is a state the reader acts on, and every one is a
// candidate for accidental deletion during a layout rewrite (PRD R1, rated High).
//
// A redesign optimising for a word count deletes all five along with the filler, because they are all
// "text" and only the filler is obviously disposable. These are the tests that catch that.
//
// # 🔴 Why these read SOURCE rather than rendering
//
// The same reason `memory.test.mjs`, `context.test.mjs` and `craft.test.mjs` do: the properties are
// structural — a boundary rendered ABOVE a picker, a fixture ABSENT from a position, a cause passed
// through UNMODIFIED — and they hold for every value the platform could return. A render test proves
// them for the one fixture it was given.
//
// The two claims a source read cannot make are made elsewhere and on purpose: `p37-save.test.mjs` drives
// a real write through to the store (6.6), and `scripts/p37-acceptance.mjs` drives a real browser (6.11).

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import { REWRITTEN } from "../scripts/lib/prose-budgets.mjs";
import { AXIS_DOC } from "../src/lib/axisSubject.ts";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(ROOT, rel), "utf8");

/** filesOf returns every source file a route renders from. */
async function filesOf(route) {
  const dir = join(ROOT, "src", "app", "app", ...route.split("/").slice(2));
  const entries = await readdir(dir, { withFileTypes: true });
  return entries
    .filter((e) => e.isFile() && /\.(tsx|ts)$/.test(e.name))
    .map((e) => join("src", "app", "app", ...route.split("/").slice(2), e.name));
}

/** sourceOf concatenates a route's own files. */
async function sourceOf(route) {
  const files = await filesOf(route);
  return (await Promise.all(files.map(read))).join("\n");
}

/** strip removes comments, so a fence never fires on the prose defending the rule it enforces. */
const strip = (src) => src.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/^\s*\/\/.*$/gm, " ");

// ── 6.2 · no fixture in the reader's data position ──────────────────────────────────────────────

/**
 * FIXTURES are the demonstration values that used to occupy the reader's position, by name.
 *
 * 🔴 Named individually rather than matched by a pattern. "Looks like a fixture" is a heuristic that
 * would fire on a legitimate value and, worse, would pass on the next fixture somebody adds under a
 * different-looking name. These are the ones that were actually there.
 */
const FIXTURES = [
  ["recall", "/app/memory's demonstration node"],
  ["n_24ee3c42", "the **kwargs decline's fixture node"],
  ["n_9f1c04ab", "the TypeScript decline's fixture node"],
  ["turnThree", "the applied window's fixture turns"],
  ["pipeline.go", "the context fixture's filename"],
  ["wiring.go", "the wiring fixture's filename"],
  ["ac_4f19c2ab7d3e5610bb42", "an authored-change fixture id"],
  ["9c4e21f0ab73", "the memory panel's fixture parent hash"],
  ["b41c7e09aa25", "the context preflight fixture's hash"],
  ["claude-haiku-4-5", "the studio's fixture model list"],
];

test("🔴 6.2 no fixture occupies the reader's data position on ANY rewritten surface", async () => {
  const found = [];
  for (const route of REWRITTEN) {
    const source = strip(await sourceOf(route));
    for (const [fixture, what] of FIXTURES) {
      if (source.includes(fixture)) found.push(`${route} still renders ${what} (${fixture})`);
    }
  }
  assert.deepEqual(
    found,
    [],
    "a demonstration value is in the position the reader's own data occupies. A sample node dressed as " +
      "theirs is worse than an empty screen, because they cannot tell which one they are looking at:\n  " +
      found.join("\n  "),
  );
});

test("🔴 6.2 an unconnected reader reaches NO surface body — the type system carries FR4", async () => {
  // The strongest form of the rule, and the reason it is not only a runtime check: `AxisFrame`'s
  // `children` is a FUNCTION OF THE RESOLVED SUBJECT, so there is no code path that renders a surface's
  // editor without one. A fixture cannot occupy the reader's position because the position does not
  // exist until a subject does.
  const frame = await read("src/components/axisFrame.tsx");
  assert.match(
    frame,
    /children: \(subject: AxisSubject, candidates: AxisSubject\[\]\) => ReactNode/,
    "AxisFrame's body is a node rather than a function of the subject, so a surface can render without one",
  );
  assert.match(frame, /return <NotConnected axis=\{axis\} \/>;/, "not_connected renders no body at all");

  for (const route of REWRITTEN) {
    const source = await sourceOf(route);
    assert.match(
      source,
      /<AxisFrame axis="/,
      `${route} does not go through AxisFrame, so nothing stops it rendering without a subject`,
    );
  }
});

test("🔴 6.2 the not_connected state names its input and carries BOTH links", async () => {
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /NOT_CONNECTED\.missingInput/, "the state does not name the missing input");
  assert.match(kit, /NOT_CONNECTED\.connectHref/, "the state does not link to the connection flow");
  assert.match(
    kit,
    /AXIS_DOC\[axis\]/,
    "the state does not link to the reading surface — the disconnected reader IS the first-time reader, " +
      "and sending them to the document is what makes moving the explanation an improvement for them",
  );
  const subject = await read("src/lib/axisSubject.ts");
  assert.doesNotMatch(
    strip(subject),
    /no data available|nothing to show here\./i,
    "the copy says something is missing without naming it",
  );
});

// ── 6.3 · the refusal survives, verbatim ────────────────────────────────────────────────────────

test("🔴 6.3 every rewritten surface renders the engine's cause through the shared panel", async () => {
  // 🔴 The cause text is the ENGINE's sentence. A surface that re-worded it would be making a second,
  // softer statement of a safety boundary — and the reader would have no way to tell which one is the
  // product's.
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /<PreflightPanel result=\{result\} \/>/, "the kit does not render the platform's verdict");

  const panel = await read("src/components/authoring.tsx");
  assert.match(
    panel,
    /\{result\.cause\}/,
    "PreflightPanel no longer renders the cause as received; a template around it is a paraphrase",
  );

  // 🚫 And nothing on a rewritten surface may reshape a cause on the way to the panel.
  for (const route of REWRITTEN) {
    const source = strip(await sourceOf(route));
    for (const reshape of [/cause\s*[:=]\s*`/, /cause\.replace\(/, /cause\.slice\(/, /cause\.split\(/]) {
      assert.doesNotMatch(
        source,
        reshape,
        `${route} reshapes a refusal cause (${reshape}). It is rendered unchanged or it is a paraphrase.`,
      );
    }
  }
});

test("🔴 6.3 a transport failure is never rendered as a refusal", async () => {
  // The two look alike on a screen and are opposite facts: a refusal is a computed answer that a retry
  // will reproduce, and a transport failure is the platform not having answered. Collapsing them sends
  // the reader to re-compose a change that was never rejected.
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /setTransportError\(/, "the kit has no separate transport state");
  assert.match(kit, /The check did not happen/, "a transport failure is not named as one");
  assert.match(kit, /Nothing was written/, "a transport failure does not say what did not happen");
});

// ── 6.4 · not_measured, with its named missing input, on every axis ─────────────────────────────

test("🔴 6.4 every rewritten surface can render not_measured WITH a named missing input", async () => {
  const missing = [];
  for (const route of REWRITTEN) {
    const source = await sourceOf(route);
    // Either the surface renders `CurrentValue` (which draws `not_measured` and its input), or it hands
    // a `current` to the editor kit, which does. Both paths carry `missingInput` and `because`.
    const draws = /<CurrentValue/.test(source) || /current=\{/.test(source);
    if (!draws) missing.push(`${route} renders no per-node state at all`);
    if (!/missingInput/.test(source)) missing.push(`${route} never names a missing input`);
    if (!/because:/.test(source)) missing.push(`${route} never says WHY the input is missing`);
  }
  assert.deepEqual(
    missing,
    [],
    "absence is DRAWN, never omitted and never rendered as zero — and a `not_measured` that names a " +
      "machine identifier and nothing else is an apology rather than an action:\n  " + missing.join("\n  "),
  );
});

test("🔴 6.4 the component refuses to draw not_measured without naming what is missing", async () => {
  const kit = await read("src/components/editorKit.tsx");
  const block = kit.slice(kit.indexOf('state === "not_measured"'), kit.indexOf("// ── The kit"));
  assert.match(block, /missingInput \?\? "unnamed"/, "an unnamed missing input renders as blank rather than as a defect");
  assert.match(block, /\{because \? <> — \{because\}<\/> : null\}/, "the sentence is not rendered beside the name");
});

// ── 6.5 · the boundary is rendered ABOVE its control ────────────────────────────────────────────

test("🔴 6.5 the boundary slot is above the picker, structurally, for every axis at once", async () => {
  const kit = await read("src/components/editorKit.tsx");
  const boundaryAt = kit.indexOf("{boundary}");
  const pickerAt = kit.indexOf('type="radio"');
  assert.ok(boundaryAt > 0 && pickerAt > 0, "the kit has no boundary slot or no picker");
  assert.ok(
    boundaryAt < pickerAt,
    "the boundary renders after the picker. A reader who composes a change and only then meets a wall " +
      "has been given a technically honest bait-and-switch — and because this is the SHARED kit, getting " +
      "it wrong here gets it wrong on all seven axes at once.",
  );
});

test("🔴 6.5 every axis that HAS a boundary states it, and names what is missing", async () => {
  // Four of the seven have one. The other three are read-only surfaces whose whole body is the reason,
  // which is P34 §7.3's treatment rather than an omission — and they are asserted separately below.
  const withBoundary = {
    "/app/context": /refused when it is proposed/i,
    "/app/memory": /boundary\.missing_artifact/,
    "/app/studio": /does not become another by changing a model string/i,
    "/app/harness": /That is not the same as unenforced/i,
  };
  for (const [route, needle] of Object.entries(withBoundary)) {
    assert.match(await sourceOf(route), needle, `${route} lost its stated boundary`);
  }

  // 🔴 And the read-only surfaces LEAD with their reason rather than offering a control that cannot
  // produce a diff. A hidden axis is indistinguishable from one that does not exist; a picker that
  // cannot produce a diff reads as a bug.
  const graph = await sourceOf("/app/graph");
  assert.match(graph, /<WiringBoundaries \/>/, "the graph surface lost the two boundaries P15 stated");
});

// ── 6.7 · the subject persists, and is visible on every surface ─────────────────────────────────

test("🔴 6.7 all seven surfaces resolve the SAME subject through the SAME resolver", async () => {
  for (const route of REWRITTEN) {
    const source = await sourceOf(route);
    assert.match(source, /resolveSubject\(subjectFromSearchParams\(/, `${route} does not resolve a subject`);
    assert.match(source, /returnTo="\/app\//, `${route} does not tell the switcher where to come back to`);
  }
  // One resolver, one cookie, one order. Seven resolvers is seven answers to one question.
  const resolver = await read("src/lib/subjectResolver.ts");
  assert.match(resolver, /export async function resolveSubject/, "the resolver is gone");
  assert.match(resolver, /SUBJECT_COOKIE/, "the resolution does not consult the remembered choice");
});

test("🔴 6.7 the subject is NAMED on screen even when there was only one candidate", async () => {
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /export function SubjectName/, "there is no component that names the subject");
  assert.match(
    kit,
    /the only node reported for this workflow — chosen without asking, and named so you can check/,
    "a sole candidate is chosen silently. Being TOLD which node was chosen is not the same as being " +
      "defaulted into one, and the difference is a reader editing something they did not mean to.",
  );
  const frame = await read("src/components/axisFrame.tsx");
  assert.match(frame, /<SubjectName subject=/, "the frame does not render the subject's name");
  assert.match(
    frame,
    /outcome\.candidates\.length > 1 \? \(\s*<SubjectSwitcher/,
    "the subject is not changeable, or a picker with one option asks a question that has no second answer",
  );
});

test("🔴 6.7 a remembered subject is VALIDATED against the live node list, never trusted", async () => {
  const resolver = await read("src/lib/subjectResolver.ts");
  assert.match(
    resolver,
    /const still = candidates\.find\(\(c\) => c\.node_id === remembered\.node_id\);/,
    "the remembered subject is used without checking it still exists. A cookie is not evidence that a " +
      "node exists, and a picker must never offer a door that does not open.",
  );
});

// ── 6.8 · axis state is per node, never averaged ────────────────────────────────────────────────

test("🔴 6.8 no rewritten surface renders an aggregate in place of the per-node states", async () => {
  const found = [];
  for (const route of REWRITTEN) {
    const source = strip(await sourceOf(route));
    for (const aggregate of [/\d+% covered/i, /\bpercent(age)? covered\b/i, /\.reduce\(\(/, /\/ *nodes\.length/]) {
      if (aggregate.test(source)) found.push(`${route} computes or renders an aggregate (${aggregate})`);
    }
  }
  assert.deepEqual(
    found,
    [],
    '"Context: 80% covered" hides the one node with no policy, and the one node with no policy is the ' +
      "reason the reader came:\n  " + found.join("\n  "),
  );
});

test("6.8 every count the projection renders carries its denominator", async () => {
  // Kept from P29 §5.7 and re-asserted here because P37 is the change most likely to lose it: "68%
  // covered" over three nodes and over four hundred are the same string and different facts.
  const panel = await read("src/components/axisProjection.tsx");
  assert.match(panel, /\{value\}[\s\S]{0,200}\/ \{of\}/, "a count is rendered without its denominator");
});

// ── 6.9 · an unavailable option is disabled with the service it needs ───────────────────────────

test("🔴 6.9 an unavailable option is rendered, disabled, naming what it needs — never hidden", async () => {
  const kit = await read("src/components/editorKit.tsx");
  assert.match(kit, /disabled=\{unavailable\}/, "an unavailable option is not disabled");
  assert.match(kit, /needs \{o\.unavailableReason\}/, "a disabled option does not name the service it needs");
  // 🚫 And it is not filtered out of the list. A hidden option is indistinguishable from one that does
  // not exist, and a reader who cannot see it cannot ask for it.
  assert.doesNotMatch(
    kit,
    /options\.filter\(/,
    "the picker filters its options. FR7: an option this deployment cannot supply is SHOWN, disabled.",
  );

  // Two axes populate the reason, from two different facts, and neither invents one.
  const shapes = await read("src/lib/axisVocabularyShapes.ts");
  assert.match(shapes, /a call site written against the \$\{m\.provider\} SDK/, "the model axis names no SDK");
  assert.match(shapes, /a rewriter this policy will never have at a call site/, "the context axis names no reason");
});

// ── 6.10 · every reading-surface destination resolves ───────────────────────────────────────────

test("🔴 6.10 every destination a surface links to EXISTS on the reading surface", async () => {
  const broken = [];
  for (const [axis, href] of Object.entries(AXIS_DOC)) {
    const slug = href.replace(/^\/docs\//, "");
    try {
      await read(join("content", "docs", "en", `${slug}.md`));
    } catch {
      broken.push(`${axis} → ${href} (content/docs/en/${slug}.md does not exist)`);
    }
  }
  assert.deepEqual(
    broken,
    [],
    "a working surface links to a reading-surface page that does not exist. That is the specific 404 " +
      "nobody reports, because it looks like a docs problem:\n  " + broken.join("\n  "),
  );
});

test("🔴 6.10 every rewritten surface carries at least one route back to its destination", async () => {
  const orphans = [];
  for (const route of REWRITTEN) {
    const source = await sourceOf(route);
    if (!/AXIS_DOC\.|<ReadOn|<AxisFrame axis=/.test(source)) orphans.push(route);
  }
  assert.deepEqual(
    orphans,
    [],
    "a surface moved its explanation and kept no link to where it went. A capability nobody can find is " +
      "indistinguishable from one that does not exist:\n  " + orphans.join("\n  "),
  );
});

test("🔴 6.10 the destination map covers every rewritten surface's axis", async () => {
  // The map is what `not_connected` and every `ReadOn` resolve through, so a missing entry is a link
  // that renders as nothing rather than as a 404 — quieter, and therefore worse.
  for (const axis of ["context", "memory", "harness", "graph", "wiring", "model", "prompt", "delivery"]) {
    assert.ok(AXIS_DOC[axis], `no reading-surface destination is declared for the ${axis} axis`);
  }
});
