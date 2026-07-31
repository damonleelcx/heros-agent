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
import assert from "node:assert/strict";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

let platform;
let console_;
let cookie;

before(async () => {
  platform = await startStubPlatform();
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

test("13.6 the three verdicts render as three distinguishable answers", async () => {
  const { html } = await get("/app/authoring");

  // Each verdict's own words must be present. These are the sentences a reader acts on.
  assert.match(html, /This change can be applied/, "the admissible verdict did not render");
  assert.match(html, /was declined, and nothing was submitted/, "the refused verdict did not render");
  assert.match(html, /We have not measured this yet/, "the third verdict did not render");

  // 🔴 And the third must NOT be dressed as a refusal. The refusal carries the hazard tone; the
  // not-yet-measurable answer must not, or a reader scanning the page takes "you may not" from a
  // sentence that says "we do not know".
  const third = section(html, "We have not measured this yet");
  assert.ok(
    !/banner--warn|banner--bad/.test(third),
    "not-yet-measurable is drawn in a hazard tone — it reads as a refusal",
  );
  assert.match(third, /not<\/strong> a refusal|not a refusal/,
    "the third verdict does not say it is not a refusal");

  // The refusal, by contrast, SHOULD carry the hazard tone and must name what it refused.
  const refused = section(html, "was declined, and nothing was submitted");
  assert.match(refused, /banner--warn/, "the refusal is not drawn as one");
  assert.match(refused, /summarize/, "the refusal does not name the node");
  assert.match(refused, /provider_params/, "the refusal does not name the field");
});

test("13.6 an authored change is rendered with its verification state, never without", async () => {
  const { html } = await get("/app/authoring");
  assert.match(html, /unverified/, "no authored change carried its verification state");
  assert.match(html, /contributes nothing to any improvement or savings figure/i,
    "the surface shows the word 'unverified' without saying what it means");
});

test("13.6 the studio gained authoring and lost nothing", async () => {
  const { status, html } = await get("/app/studio");
  assert.equal(status, 200);

  // The tabs that existed before 13c, still rendered.
  for (const label of ["Matrix", "Prompt library", "Bound nodes"]) {
    assert.ok(html.includes(label), `the studio lost its "${label}" tab in the rendered page`);
  }
  assert.ok(html.includes(">Author<") || /Author<\/button>/.test(html),
    "the studio did not gain the authoring tab");

  // Model authoring is present, and only same-provider models are offered.
  assert.match(html, /Set a model yourself/, "model authoring did not render");
  assert.match(html, /Only anthropic models are offered for this call site/,
    "the provider boundary is not stated where the choice is made");
  for (const foreign of ["gpt-4o", "gemini-", "llama-3"]) {
    assert.ok(!html.includes(foreign),
      `a ${foreign} model is offered for an anthropic call site — that diff would compile and call the wrong provider`);
  }
});

test("13.6 the surface states that a refusal has no override", async () => {
  const { html } = await get("/app/authoring");
  assert.match(html, /no override/i,
    "the surface does not tell the reader a refusal cannot be forced, which is the first thing they will try");
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
