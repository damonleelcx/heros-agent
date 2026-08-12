import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (p) => readFile(join(ROOT, p), "utf8");

const GRAPH = "src/app/app/workflows/[workflowId]/graph/page.tsx";
const EVALSET = "src/app/app/workflows/[workflowId]/evalset/page.tsx";
const CSS = "src/app/globals.css";

/**
 * p30-composition.test.mjs is P30 workstream 8 — the customer surfaces.
 *
 * # What these fences are for
 *
 * Every one of them protects the same property from a different angle: a HYPOTHESIS MUST NEVER RENDER
 * AS A MEASUREMENT. The platform computes the distinction and hands it to the page; what these check is
 * that the page keeps it — that a count is split, that an inferred edge has its own channel, that the
 * four states stay four, and that prose a model wrote is marked as such.
 *
 * They are source assertions rather than rendered-DOM ones on purpose. The failure they guard against
 * is a page-local shortcut ("just show the total"), and a shortcut is visible in the source of the
 * component that takes it while a rendered page merely looks tidy.
 */

test("🔴 8.4 · a mixed count renders the total AND the inferred portion, never one number", async () => {
  const src = await read(GRAPH);

  // The edges stat branches on the inferred count rather than printing `edges.length` alone.
  assert.match(
    src,
    /composition\.edges_inferred\s*>\s*0/,
    "the edge count must branch on whether anything was inferred",
  );
  assert.match(
    src,
    /integer\(composition\.edges_inferred\)/,
    "the inferred portion must be rendered, not only counted",
  );

  // And the node coverage figure — the one a reader is most likely to quote — does the same.
  assert.match(
    src,
    /composition\.nodes_covered_inferred\s*>\s*0/,
    "node coverage must state its inferred portion where there is one",
  );
});

test("🔴 8.3 · an inferred edge has its own stroke AND its own arrowhead, and authorship beats kind", async () => {
  const src = await read(GRAPH);
  const css = await read(CSS);

  assert.match(src, /edge--inferred/, "the drawing must have a class for an inferred edge");
  assert.match(src, /arrow-inferred/, "an inferred edge must carry its own marker");

  // 🔴 TWO CHANNELS. A reader in greyscale, on a projector, or with a colour vision deficiency must
  // still separate a hypothesis from a parsed fact — which is the same rule that makes a control edge
  // differ from a data edge by arrowhead as well as by hue.
  const rule = css.match(/\.graph \.edge--inferred\s*\{[^}]*\}/s);
  assert.ok(rule, "edge--inferred has no CSS anchor");
  assert.match(rule[0], /stroke-dasharray/, "the inferred stroke must differ by more than colour");

  // It reuses the console's ONE "a model was consulted" channel rather than inventing a sixth.
  assert.match(
    rule[0],
    /var\(--llm\)/,
    "an inferred edge must reuse the --llm channel a model-labelled region already uses — a new hue " +
      "would be a new thing for a reader to learn for a distinction they already know",
  );

  // Authorship wins over kind: an inferred CONTROL edge must not be drawn in the control treatment.
  assert.match(
    src,
    /const inferred = edge\.author === "heros"/,
    "the drawing must decide on authorship first",
  );
  const branch = src.match(/const className = inferred[\s\S]{0,220}/);
  assert.ok(branch, "the edge class is not chosen from the inferred flag");
  assert.ok(
    branch[0].indexOf("edge--inferred") < branch[0].indexOf("edge--control"),
    "kind must not take precedence over authorship: an inferred control edge drawn in the control " +
      "treatment puts a model's guess in the same channel as a parsed fact",
  );
});

test("8.3 · the legend names the inferred channel, and the edge TABLE carries it too", async () => {
  const src = await read(GRAPH);

  assert.match(src, /legend__swatch--inferred/, "the legend must name the inferred channel");
  assert.match(
    src,
    /inferred edge — a model proposed this dependency/,
    "the legend entry must say what it means, not only that it exists",
  );

  // 🔴 The table is the drawing's TEXT ALTERNATIVE. A reader using it instead of the SVG must not lose
  // the one channel that says which edges are hypotheses.
  const table = src.match(/caption="Every edge[^"]*"[\s\S]{0,1400}/);
  assert.ok(table, "the edge table is missing");
  assert.match(table[0], /How we know/, "the edge table must carry the measured/inferred column");
  assert.match(table[0], /edge\.author === "heros"/, "the table must branch on authorship");
});

test("🔴 8.5 · the four states stay four, and the default is not styled as a fault", async () => {
  const src = await read(GRAPH);

  const tone = src.match(/const STATE_TONE[\s\S]{0,320}?\};/);
  assert.ok(tone, "the state→tone map is missing");
  for (const state of ["measured", "inferred", "not_analysed", "unavailable"]) {
    assert.match(tone[0], new RegExp(`${state}:`), `${state} has no tone`);
  }

  // 🔴 The two that look alike must not LOOK alike. `not_analysed` is a setting — the default one,
  // which every organization sees before an operator turns analysis on — and `unavailable` is a fault.
  assert.match(tone[0], /not_analysed:\s*"neutral"/, "the default state must be neutral");
  assert.match(tone[0], /unavailable:\s*"warn"/, "a fault must not read as the default");

  // And the sentence comes from the platform, so a fifth state cannot be invented in TypeScript.
  assert.match(src, /agent\.state_sentence/, "the page must render the sentence it was handed");
  assert.doesNotMatch(
    src,
    /state === "not_analysed" \? "[^"]{40,}/,
    "a state's prose must not be written in the page",
  );
});

test("🔴 8.2 · the narrative is marked `assessed`, and has no else branch", async () => {
  const src = await read(GRAPH);
  const css = await read(CSS);

  assert.match(src, /agent\.narrative \?/, "the narrative must be conditional on there being one");
  assert.match(src, /className="assessed"/, "the narrative must carry the assessed treatment");
  assert.match(src, /assessed__mark/, "the narrative must carry a visible mark, not only a class");

  // 🚫 NO ELSE BRANCH. When the agent wrote nothing, NOTHING is rendered — prose assembled from the
  // counts would appear in this same treatment, which tells a reader a model wrote it.
  //
  // 🔴 The block is extracted by BALANCING BRACES, not by a regex. The regex version of this
  // (`/\{agent\.narrative \? \([\s\S]*?\) : null\}/`) passed against a page whose narrative
  // conditional had a generated-prose else branch, because the non-greedy match simply ran on and
  // found a `) : null}` belonging to a LATER conditional. It was checked and it was checking nothing —
  // which is worse than absent, because it reported the property as held.
  const block = jsxExpressionAt(src, "{agent.narrative ?");
  assert.ok(block, "the narrative conditional is missing");
  assert.ok(
    /\)\s*:\s*null\s*\}$/.test(block),
    "the narrative conditional must end `: null`. Its else branch is the one place generated prose " +
      `would live, and it would render in the assessed treatment. Found:\n${block.slice(-160)}`,
  );

  // Belt and braces: exactly ONE assessed block in the panel. A second is prose from somewhere else.
  const panel = src.match(/function AgentPanel[\s\S]*?\n\}/)[0];
  assert.equal(
    (panel.match(/className="assessed"/g) ?? []).length,
    1,
    "the panel renders more than one assessed block — only the agent's own prose may carry that mark",
  );

  // The treatment reuses the model channel rather than inventing one.
  const rule = css.match(/\.assessed\s*\{[^}]*\}/s);
  assert.ok(rule, ".assessed has no CSS anchor — 8.10 requires every token have one");
  assert.match(rule[0], /var\(--llm\)/, "the assessed treatment must reuse the --llm channel");
});

/**
 * jsxExpressionAt returns the full `{…}` expression beginning at `open`, by balancing braces.
 *
 * Written because the regex it replaces did not bind (see its call site). A JSX conditional can contain
 * arbitrary nesting, so the end of one is a counting problem and not a pattern-matching one.
 */
function jsxExpressionAt(src, open) {
  const start = src.indexOf(open);
  if (start < 0) return null;
  let depth = 0;
  for (let i = start; i < src.length; i += 1) {
    if (src[i] === "{") depth += 1;
    else if (src[i] === "}") {
      depth -= 1;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  return null;
}

test("🔴 8.7 · an action is offered where one exists, and a reason where none is", async () => {
  const src = await read(GRAPH);

  assert.match(src, /agent\.action === "analyse"/, "the page must branch on the offered action");
  assert.match(src, /agent\.action_reason/, "an unavailable action must render its reason");

  // 🚫 No control that cannot work. The page renders sentences and a command to copy; it never puts a
  // button behind an action the platform has said it cannot run.
  const panel = src.match(/function AgentPanel[\s\S]*?\n\}/);
  assert.ok(panel, "AgentPanel is missing");
  assert.doesNotMatch(
    panel[0],
    /<button|<form|onClick/,
    "the agent panel must not render a control: an action that fails on press is worse than an absent " +
      "one with a sentence, and the platform has already said whether it can run",
  );
});

test("🔴 8.8 · a HEROS failure is a banner INSIDE the panel, never the page", async () => {
  const src = await read(GRAPH);

  const panel = src.match(/function AgentPanel[\s\S]*?\n\}/);
  assert.ok(panel, "AgentPanel is missing");
  assert.match(panel[0], /agent\.failure \?/, "the panel must render a failure it was handed");
  assert.match(panel[0], /<Banner/, "the failure must render as a banner within the panel");

  // The page-level failure component is reserved for the GRAPH read failing. An agent failure reaching
  // it would replace every rule-derived surface on the page with an error — making an optional
  // subsystem's outage look like a total loss of the customer's data.
  const body = src.match(/function GraphBody[\s\S]*?\n\}/);
  assert.ok(body, "GraphBody is missing");
  assert.doesNotMatch(
    body[0],
    /agent[\s\S]{0,40}<Failure/,
    "an agent failure must never reach the page-level Failure component",
  );
});

test("🔴 8.9 · the eval set says it CANNOT TELL, rather than showing an empty list", async () => {
  const src = await read(EVALSET);

  assert.match(
    src,
    /coverageUnknown/,
    "the page must distinguish `no uncovered nodes` from `we cannot compute uncovered nodes`",
  );
  assert.match(
    src,
    /includes\("uncovered_nodes"\)/,
    "the unknown case must be read from the platform's `unattributed` list, not guessed",
  );

  // 🔴 THREE renderings, and the third is the point. The section used to be hidden whenever the list
  // was empty, which rendered the reassuring reading of both states.
  assert.match(src, /cannot say which nodes your cases exercise/i, "the unknown state has no sentence");
  assert.match(src, /Every node in this workflow/, "the genuinely-covered state has no sentence");
  assert.doesNotMatch(
    src,
    /\{uncovered\.length > 0 \? \(\s*<Section title="Graph nodes/,
    "the section must not be hidden on an empty list — an empty list of unexercised nodes and a " +
      "deployment that cannot compute one produce the same absence, and only one means `all covered`",
  );
});

test("🔴 a body with NO composition degrades — it neither crashes nor invents a zero", async () => {
  const src = await read(GRAPH);

  // 🔴 The crash this prevents was real: the page dereferenced `view.composition` unguarded, so a 200
  // from a platform older than this field rendered `__next_error__` for the WHOLE route — the graph,
  // the node table, the topology statement, all of it. Found by the acceptance suite, whose graph
  // fixture is deliberately a pre-P30 body.
  assert.match(
    src,
    /view\.composition \?\? null/,
    "the composition must be read as nullable: a 200 whose body does not match the read model is a " +
      "fourth state on this console, not a crash",
  );
  assert.match(src, /!composition \?/, "the missing case must have its own rendering");

  // 🚫 And it must not be defaulted to zeros. `0 of 0 nodes covered` is a measurement nobody made.
  assert.doesNotMatch(
    src,
    /composition\s*\?\?\s*\{/,
    "a missing composition must not be defaulted to an empty one — that renders invented counts as " +
      "measured, which is the fabrication the whole workstream is about",
  );
  assert.match(
    src,
    /did not report a composition/,
    "the missing case must SAY the platform did not report one",
  );
});

test("8.10 · every class this workstream introduced has a CSS anchor, and the copy is English", async () => {
  const css = await read(CSS);
  for (const cls of ["assessed", "assessed__mark", "edge--inferred", "legend__swatch--inferred"]) {
    assert.match(
      css,
      new RegExp(`\\.${cls.replace(/--/g, "--")}\\b`),
      `.${cls} is used and defined nowhere — an improvised class is a second design language`,
    );
  }
  // The console is English-only. Asserted over the two files this workstream touched rather than
  // repo-wide, which design-system.test.mjs already covers.
  for (const file of [GRAPH, EVALSET]) {
    const src = await read(file);
    assert.doesNotMatch(
      src,
      /[぀-ヿ一-鿿؀-ۿЀ-ӿ]/,
      `${file} contains non-Latin script`,
    );
  }
});
