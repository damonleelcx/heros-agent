// install.test.mjs — the guards on the install surface (P20 section 6).
//
// This surface answers the first question a customer's engineer asks and the last one their security reviewer
// asks, and it can get both wrong in ways nobody notices internally:
//
//   1. It shows an install command for a channel nobody can install from. The manifest exists, the generator
//      works, the row looks finished — and the user's `brew install` fails. That is the failure the
//      delivered/publication split exists to prevent, and it would be reintroduced the moment the page rendered
//      commands for every channel it received.
//   2. It states a trust property the release did not deliver. "Notarized" the day the budget was approved.
//   3. It collapses the three unavailable reasons into one greyed-out row — the coverage lesson, forgotten one
//      surface later. An unbuilt platform, an unpublished channel and an available one are three different
//      reader actions.
//
// Each is pinned below as a failing test rather than as a review note.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");

const COMPONENT = "src/components/install.tsx";
const PAGE = "src/app/install/page.tsx";
const PREVIEW = "src/app/preview/install/page.tsx";
const DATA = "src/app/install/data.ts";
const CHANNELS = "internal/distribution/channels.go";

// 🔴 The publication states are DATA shared with the engine. The console branches on them, so a renamed state
// must break a test rather than silently fall through to a default treatment — which is how three different
// answers become one.
test("the console handles exactly the engine's publication states", async () => {
  const [component, engine] = await Promise.all([read(COMPONENT), readRepo(CHANNELS)]);
  const states = [...engine.matchAll(/Publication = "([a-z-]+)"/g)].map((m) => m[1]);
  assert.equal(states.length, 3, `the engine must declare three publication states, got ${states}`);
  assert.ok(states.includes("published"), "the engine no longer has a 'published' state");
  // The two pending states must be distinguishable on screen: one is waiting on a repository WE create, the
  // other on a review somebody else schedules, and a reader planning around them needs to know which.
  assert.ok(
    component.includes("pending-upstream-pr"),
    "the component does not distinguish an upstream-review blocker from a missing-repository one",
  );
});

test("an undelivered channel is never rendered with an install command", async () => {
  const [component, page] = await Promise.all([read(COMPONENT), read(PAGE)]);

  // The page must split on `delivered` before choosing a renderer.
  assert.match(
    page,
    /channels\.filter\(\(c\) => c\.delivered\)/,
    "the page does not separate delivered channels from the rest; every channel would get an install card",
  );
  assert.match(
    page,
    /channels\.filter\(\(c\) => !c\.delivered\)/,
    "the page does not collect the undelivered channels, so they would be silently dropped — and an absent " +
      "channel reads as 'not supported', which is not what is true",
  );

  // The pending renderer must not print a command. A command a reader cannot run is worse than no command,
  // because they will run it.
  const pendingBlock = component.slice(
    component.indexOf("export function PendingChannelRow"),
    component.indexOf("export function TargetTable"),
  );
  assert.ok(pendingBlock.length > 0, "PendingChannelRow is gone");
  assert.ok(
    !pendingBlock.includes("channel.install"),
    "PendingChannelRow renders channel.install — that command does not work yet",
  );
  assert.ok(
    pendingBlock.includes("channel.blocker"),
    "PendingChannelRow does not state the blocker, so the row reads as 'disabled for your plan'",
  );
});

test("an unavailable channel is not merely a dimmed copy of an available one", async () => {
  const component = await read(COMPONENT);
  const pendingBlock = component.slice(
    component.indexOf("export function PendingChannelRow"),
    component.indexOf("export function TargetTable"),
  );
  // A dashed border and a different icon: the row must differ in SHAPE, not only in opacity. A greyed-out
  // control says "not for you" and stops the reader; this state means the artifact is fine and something else
  // is missing.
  assert.ok(
    pendingBlock.includes("border-dashed"),
    "the pending row is not visually distinct in shape from an available card",
  );
  assert.ok(
    !/opacity-\d/.test(pendingBlock),
    "the pending row is rendered by dimming — that is the greyed-out control this surface exists to replace",
  );
});

test("the platform matrix is total: unbuilt rows carry both a reason and an answer", async () => {
  const component = await read(COMPONENT);
  const table = component.slice(
    component.indexOf("export function TargetTable"),
    component.indexOf("export function TrustPosture"),
  );
  assert.ok(table.includes("t.limit"), "an unbuilt platform row does not say why it is unbuilt");
  assert.ok(
    table.includes("t.answer"),
    "an unbuilt platform row does not say what to use instead — a limit with no next step is an apology",
  );
  assert.ok(
    table.includes("not built"),
    "the unbuilt state is not labelled in words; a reader who cannot see colour learns nothing from the row",
  );
});

// 🔴 The whole point of splitting Posture from Attestation is that the console cannot announce an intention as a
// property. This test is that split, checked on the surface that would be tempted to blur it.
test("the trust tab renders the delivered attestation, never the ratified posture, as a property", async () => {
  const component = await read(COMPONENT);
  const trust = component.slice(
    component.indexOf("export function TrustPosture"),
    component.indexOf("export function VerifyItYourself"),
  );
  assert.ok(
    trust.includes("view.delivered"),
    "the trust section does not read the delivered attestation",
  );
  assert.ok(
    trust.includes("No published release is known"),
    "with no attestation the section must say so; rendering the ratified posture instead would describe a " +
      "budget decision as a property of a file the reader downloaded",
  );
  // Every claim is rendered, earned or not. A section showing only the earned ones could not disclose a gap.
  assert.ok(
    trust.includes("claim.earned"),
    "the trust section does not distinguish earned claims from unearned ones",
  );
  assert.ok(
    !/claims\.filter\(/.test(trust),
    "the trust section filters the claims — dropping the unearned ones turns evidence into a sales sheet",
  );
});

test("the page carries no local copy of the distribution contract", async () => {
  const [page, data] = await Promise.all([read(PAGE), read(DATA)]);
  // No hard-coded install command anywhere on the page: every one must come from the payload.
  for (const smell of ["curl -fsSL", "brew install", "winget install", "docker run"]) {
    assert.ok(
      !page.includes(smell),
      `the page hard-codes the install command ${smell}; a second copy of the contract drifts optimistic`,
    );
  }
  assert.match(
    data,
    /outcome\.ok \? outcome\.data : null/,
    "the data loader does not fail closed to null; a fallback table would be the second source of truth",
  );
  assert.ok(
    !data.includes("const FALLBACK") && !data.includes("?? {"),
    "the data loader carries a local fallback — when the platform cannot be read the page must say so",
  );
});

// 🔴 The install page must NOT be behind a session. Its readers are people who do not have the CLI yet and
// therefore have no account, and the page itself tells them the CLI is free and needs none — a sign-in wall in
// front of that is a contradiction the page refutes two paragraphs later. It lived at /app/install until that
// was caught.
test("the install surface is public — no session, no tenant", async () => {
  const [page, data] = await Promise.all([read(PAGE), read(DATA)]);
  // Checked against the CODE, with comments stripped. The page's doc comment explains why it has no session and
  // names `requireSession()` while doing so; a check that fired on that sentence would be "fixed" by deleting
  // the explanation, which is the wrong direction — the same mistake a `telemetry` source scan made earlier in
  // this phase, caught the same way.
  const pageCode = stripComments(page);
  assert.ok(
    !/from "@\/lib\/session"/.test(pageCode),
    "the install page imports the session module — the audience for 'how do I install the free CLI' is " +
      "precisely the people who cannot sign in",
  );
  assert.ok(
    !/\brequireSession\s*\(/.test(pageCode),
    "the install page calls requireSession()",
  );
  assert.ok(
    !stripComments(data).includes("tenantId"),
    "the install data loader takes a tenantId — it reads an endpoint that has no tenant, and requiring one " +
      "would put the page back behind a session",
  );
  assert.ok(
    data.includes("platformFetchPublic"),
    "the loader does not use the session-less door; platformFetch's required tenantId exists to stop a " +
      "tenant-scoped call from compiling without a session, and it must not be satisfied with a fake value",
  );
});

test("the surface is reachable by navigation and by the command path", async () => {
  const [layout, routes] = await Promise.all([read("src/app/app/layout.tsx"), read("src/lib/routes.ts")]);
  assert.ok(layout.includes('"/install"'), "the install surface is not in the navigation rail");
  assert.ok(
    layout.includes('label: "Install the CLI'),
    "the install surface is not in the command path — a surface reachable only by typing a URL is one most " +
      "readers never learn exists",
  );
  assert.ok(
    routes.includes('install: () => "/install"'),
    "routes.ts does not point at the PUBLIC install route — a console-only route would put it back behind the " +
      "session gate for the readers who need it most",
  );
});

test("the preview shows both trust states, so the difference can be seen rather than asserted", async () => {
  const preview = await read(PREVIEW);
  assert.ok(
    preview.includes("PREVIEW_INSTALL_PUBLISHED"),
    "the preview does not render a published release's posture beside the no-release-yet one. Whether one " +
      "EARNED claim and three UNEARNED ones are distinguishable at a glance is what this section exists for, " +
      "and no test can establish it — it has to be looked at",
  );
  assert.ok(
    preview.includes("PendingChannelRow"),
    "the preview does not render the pending-channel state, so its treatment is never looked at",
  );
});

// The free/paid boundary belongs where an evaluating engineer reads it, not on a pricing page they do not.
test("the install surface states the free-vs-paid boundary", async () => {
  const component = await read(COMPONENT);
  assert.ok(component.includes("free, no account, forever"), "the CLI's free-forever promise is not stated");
  assert.ok(
    component.includes("Nothing in the free path is degraded to sell it"),
    "the boundary is stated without the guarantee that the free path is not degraded — which is the part a " +
      "customer's engineer is actually checking for",
  );
});

/** stripComments removes block and line comments so a source check tests the CODE rather than the prose about
 * it. Crude on purpose: these files contain no `//` inside a string literal that would matter. */
function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}
