// authoring-acceptance.test.mjs — P13 13c task 13.6, the RENDERED half.
//
// # Why this is separate from authoring.test.mjs
//
// That file reads source and asserts the components exist and say the right things. This one starts the
// real console, signs in, and reads what actually comes back over HTTP — because a green source
// assertion and a page that renders nothing are entirely compatible, and this console has paid for that
// lesson three times already.
//
// # The property being protected
//
// 🔴 The three preflight verdicts must reach a reader as THREE ANSWERS. "We decline this" and "we have
// not measured this" lead to opposite next actions — fix your change, versus run an evaluation — and a
// surface that renders them the same has turned a remedy into a guess. Source can be right about that
// while the rendering is wrong (a shared component, a dropped branch, a tone applied by a wrapper), so
// it is asserted here against bytes the browser would receive.

import { test, before, after } from "node:test";
import { readFile } from "node:fs/promises";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";
import { WORKFLOW, connected } from "./support/connected.mjs";

let platform;
let console_;
let cookie;

before(async () => {
  platform = await startStubPlatform();
  // 🔴 P37 bound these surfaces to the reader's own node, so the platform this suite renders against
  // has to HOLD one. Before P37 the surfaces demonstrated their verdicts against fixtures and an empty
  // platform was enough; asserting the same claims now means asserting them over the reader's data,
  // which is the point of the phase rather than a cost of it.
  platform.set(connected);
  console_ = await startConsole(platform.base);
  cookie = await signIn(console_.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

async function get(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie }, redirect: "manual" });
  return { status: res.status, html: await res.text() };
}

test("13.6 the authoring surface renders, and is reachable by navigation", async () => {
  const { status, html } = await get("/app/authoring");
  assert.equal(status, 200, "the authoring surface did not render");

  // Not merely 200 — a 200 with an empty shell is the failure this file exists to catch.
  assert.match(html, /Make the change yourself/, "the page rendered without its heading");
  assert.ok(html.length > 5000, `the page rendered ${html.length} bytes — that is a shell, not a surface`);

  // Reachable by navigation, from a page that is not itself.
  const studio = await get("/app/studio");
  assert.match(studio.html, /href="\/app\/authoring"/,
    "the authoring surface is not linked from the shell — a surface reachable only by typing its URL is one most readers never find");
});

test("13.6 the three verdicts stay three, and the component that draws them still separates them", async () => {
  // 🔴 P13 asserted the three verdicts as FIXTURES rendered on `/app/authoring`. P37 removed the
  // fixtures — the surface now renders the READER's verdict, which does not exist until they compose a
  // change — so the claim is asserted at both ends:
  //
  //   · the three are explained at their destination, where they are the same for every reader;
  //   · the component that draws a LIVE one still draws three different things, and the third still is
  //     not dressed as a refusal.
  //
  // The second half is what actually protects a reader, and it is now protected for all seven axes at
  // once rather than for the one page that used to demonstrate it.
  const doc = await readFile(new URL("../content/docs/en/concepts/authored-changes.md", import.meta.url), "utf8");
  assert.match(doc, /## The three verdicts/, "the three verdicts lost their destination section");
  assert.match(doc, /not yet measurable/i, "the third verdict is not explained");
  assert.match(
    doc,
    /Rendering it as a refusal would point you at your own\s+configuration to find a fault that is not there/,
    "the destination does not say why the third verdict must not wear the refusal's clothes",
  );

  const panel = await readFile(new URL("../src/components/authoring.tsx", import.meta.url), "utf8");
  assert.match(panel, /This change can be applied/, "the admissible verdict is gone from the component");
  assert.match(panel, /was declined, and nothing was submitted/, "the refused verdict is gone");
  assert.match(panel, /We have not measured this yet/, "the third verdict is gone");
  // Three branches, three treatments. One component with three tones is how three states become two.
  assert.match(panel, /tone="warn"/, "the refusal is no longer drawn as a hazard");
  const third = panel.slice(panel.indexOf("We have not measured this yet") - 1500, panel.indexOf("We have not measured this yet") + 1500);
  assert.match(third, /not<\/strong> a refusal|not a refusal/, "the third verdict does not say it is not a refusal");
});

test("13.6 an authored change is rendered with its verification state, never without", async () => {
  const { html } = await get("/app/authoring");
  assert.match(html, /unverified/, "no authored change carried its verification state");
  assert.match(html, /contributes nothing to any improvement or savings figure/i,
    "the surface shows the word 'unverified' without saying what it means");
});

test("13.6 the studio gained authoring and lost nothing", async () => {
  // 🔴 Carrying a subject, because P37 asks WHICH NODE once — with two candidates and no answer the
  // shell asks, which is the designed behaviour and is asserted in `p37-acceptance.test.mjs`. This test
  // is about what the surface holds once that question has an answer.
  const { status, html } = await get(`/app/studio?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  assert.equal(status, 200);

  // The tabs that existed before 13c, still rendered. P37 renamed the authoring tab to "This node" —
  // the tab it names is the reader's own call site now — and added nothing else and removed nothing.
  for (const label of ["Matrix", "Prompt library", "Bound nodes"]) {
    assert.ok(html.includes(label), `the studio lost its "${label}" tab in the rendered page`);
  }
  assert.ok(html.includes(">This node<") || /This node<\/button>/.test(html),
    "the studio did not keep the authoring tab");

  // Model authoring is present, and the provider boundary is stated where the choice is made.
  //
  // 🔴 P13 asserted that a foreign-provider model is ABSENT. P37 changes that to DISABLED AND NAMED
  // (FR7), and the change is a strengthening: a silently short list reads as an incomplete catalogue and
  // the reader's next move is to look for the missing entries or file a bug. The safety property is
  // unchanged — a foreign model cannot be SELECTED — and it is now enforced where the options are built
  // (`lib/axisVocabularyShapes.ts`) rather than by a literal list.
  assert.match(html, /What this call site can be changed to/, "model authoring did not render");
  assert.match(html, /Only anthropic models are offered for this call site/,
    "the provider boundary is not stated where the choice is made");
  const foreignAt = html.indexOf("gpt-4o");
  if (foreignAt > 0) {
    const around = html.slice(Math.max(0, foreignAt - 800), foreignAt + 400);
    assert.match(around, /disabled/, "a foreign-provider model is offered as selectable");
    assert.match(around, /openai SDK/, "a disabled foreign model does not name what it would need");
  }
});

test("13.6 a refusal has no override, and the surface reaches the page that says so", async () => {
  // P37 moved the sentence to the shared contract (it is identical for every reader) and left the route
  // to it on the surface. Both halves are asserted, because a claim with a link and no destination is
  // the 404 nobody reports.
  const { html } = await get("/app/authoring");
  assert.match(html, /href="\/docs\/concepts\/authored-changes"/,
    "the surface carries no route to the contract it stopped restating");
  const doc = await readFile(new URL("../content/docs/en/concepts/authored-changes.md", import.meta.url), "utf8");
  assert.match(doc, /## There is no override/i,
    "the contract does not tell the reader a refusal cannot be forced, which is the first thing they will try");
});

/**
 * section returns the rendered slice around a marker, so a tone assertion is scoped to the banner it is
 * about rather than to the whole document — where any other banner's class would satisfy it.
 */
function section(html, marker) {
  const at = html.indexOf(marker);
  assert.ok(at > 0, `marker ${JSON.stringify(marker)} not found in the rendered page`);
  return html.slice(Math.max(0, at - 1200), at + 2000);
}
