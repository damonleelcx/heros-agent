// axes.test.mjs is the console half of P26 wave 26d.
//
// The platform half (internal/adminops/axis_test.go) asserts parity with the real engine in both
// directions. This file asserts the properties that only exist on the screen: that no cell renders as
// *not applicable*, that an unknown adoption count is not painted as a zero, that a refusal count does
// not wear the visual grammar of a ranked evaluation result, and that no plan is named anywhere near a
// coverage gap.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

test("🔴 5.7 — no coverage cell renders as *not applicable*, in the type or on the page", async () => {
  const types = await read("src/lib/types.ts");
  assert.match(
    types,
    /export type CellState = "applies" \| "refused" \| "unknown";/,
    "CellState is not the three states — *not applicable* must not be expressible",
  );
  const page = await read("src/app/axes/page.tsx");
  const prose = page.replace(/\/\*[\s\S]*?\*\/|^\s*\/\/.*$/gm, ""); // strip the comments that EXPLAIN the rule
  // Word-bounded, so the fence does not fire on `/admin/api/axes` — a fence that cried wolf on a URL
  // would be switched off within a week, and this one has to survive.
  for (const pattern of [/not[- ]applicable/i, /\bN\/A\b/i]) {
    assert.equal(
      pattern.test(prose),
      false,
      `the axes page renders ${pattern}. It says "your call site cannot carry this", while the truth ` +
        `may be "we have not built the materializer" — a substitution the customer cannot discover.`,
    );
  }
  // And an unknown cell names what is missing rather than showing a blank or a dash.
  assert.match(page, /missing_input/, "an unknown or refused cell does not name its missing input");
});

test("🔴 5.2 — an unknown adoption count is stated, never rendered as 0", async () => {
  const page = await read("src/app/axes/page.tsx");
  assert.match(page, /a\.tenants === null \?/, "a null tenant count is not branched on");
  assert.match(page, /a\.nodes === null \?/, "a null node count is not branched on");
  assert.match(page, /unknown — no adoption source/, "the unknown case does not say why it is unknown");
});

test("🔴 5.9 — a refusal count is not rendered with the grammar of a ranked result", async () => {
  const page = await read("src/app/axes/page.tsx");
  // No bar chart, no score, no position. The ranking is a plain table of counts.
  assert.equal(/AggregateChart/.test(page), false, "the axes page renders counts as a chart");
  assert.equal(/bar-chart/.test(page), false, "the axes page renders a bar for a count");
  // No score is READ from the data and none is rendered as a column. The page may (and does) say in
  // words that nothing here has been scored — denying the grammar is not adopting it.
  assert.equal(/\.score\b/.test(page), false, "the axes page reads a score off the read model");
  assert.equal(/label: "[^"]*[Ss]core/.test(page), false, "a column on the axes page is labelled as a score");
  assert.equal(/aria-valuenow|role="meter"|role="progressbar"/.test(page), false,
    "the axes page renders a count with a meter's or progress bar's semantics");
  // Whitespace-normalised: the sentence wraps across JSX lines, and a fence that broke on a reflow is
  // a fence people learn to edit rather than obey.
  assert.match(
    page.replace(/\s+/g, " "),
    /counts, not evaluation results/,
    "the page does not tell the reader these are counts rather than evaluation results",
  );
  const types = await read("src/lib/types.ts");
  assert.match(types, /is_ranking: boolean;/, "the wire type does not carry the is-ranking declaration");
});

test("🔴 5.8 — no plan, tier or entitlement is named as a way to change a coverage answer", async () => {
  const page = await read("src/app/axes/page.tsx");
  // The rule is that no plan is OFFERED as a way to change a coverage answer. The page states the
  // denial in words, so the assertion targets the offer: a plan/tier value read from data, a call to
  // action, or an entitlement field rendered beside a cell.
  for (const pattern of [/\.(plan|tier|entitlement)\b/, /upgrade to/i, /\bcontact sales\b/i, /available on /i]) {
    assert.equal(
      pattern.test(page),
      false,
      `the axes page offers ${pattern} — a coverage gap is identical on every plan, and implying ` +
        `otherwise converts a platform gap into a sales objection`,
    );
  }
  assert.match(page, /no tier unlocks a cell the engine refuses/,
    "the page does not deny that a tier would change a refused cell");
  assert.match(page, /not a plan boundary/, "the page does not state that a gap is not a plan boundary");
});

test("🔴 5.5 — the page renders the coverage source as received and names it", async () => {
  const page = await read("src/app/axes/page.tsx");
  assert.match(page, /view\.coverage_source/, "the page does not name the coverage source");
  assert.match(page, /view\.coverage_version/, "the page does not carry the engine's table version");
  // No client-side derivation of a coverage answer: the only transform is a filter by the URL's axis,
  // which selects rows and changes none of them.
  const derivations = page.match(/\.(map|reduce|sort)\(/g) ?? [];
  const rendering = page.match(/\.map\(/g) ?? [];
  assert.equal(
    derivations.length,
    rendering.length,
    "the axes page reduces or sorts a coverage answer — the read model is computed server-side and " +
      "rendered as received (P26 §5.5)",
  );
});

test("🔴 5.3 — the three typed causes are rendered separately, with whose move each is", async () => {
  const page = await read("src/app/axes/page.tsx");
  assert.match(page, /view\.legend\.map/, "the cause legend is not rendered");
  assert.match(page, /Whose move/, "the legend does not say whose move each cause is");
  assert.match(page, /a\.refusals\)/, "refusals are not rendered per cause");
  assert.match(page, /Object\.entries\(a\.refusals\)/, "refusals are rendered as a single combined total");
});

test("🔴 5.10 — the /axes destination is registered, gated, and drills down", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  assert.match(surfaces, /capability: "axis\.read",\s*\n\s*href: "\/axes"/,
    "the /axes destination is not registered against axis.read");
  const page = await read("src/app/axes/page.tsx");
  assert.match(page, /hasCapability\(identity, "axis\.read"\)/, "the page does not check the capability");
  assert.match(page, /\/axes\?axis=/, "an axis row does not drill down");
});
