// linked-run.test.mjs proves the console can show a run that was measured elsewhere and LINKED here.
//
// # The hole this closes
//
// `heros link` transmitted a run, the platform accepted it (`201`), stored it durably in `run_link`,
// and answered `409 already_linked` on a re-link. Then the console was asked for that run — and said
// **"No such run — the identifier does not resolve."** Not because anything failed, but because
// `/api/v1/runs/{id}` reads the EXECUTOR's table, and a linked run was never in it. Two tables, one
// identifier, and nothing joining them. The user was told their run did not exist.
//
// # Why the red fixture at the bottom is the important test
//
// The fix is a fallback, and a fallback is exactly the shape of change that quietly swallows the case
// it was meant to leave alone. If the linked lookup ever answered for a run nobody linked, this page
// would stop being able to say "no such run" at all — and a typo'd identifier would render as an
// almost-empty success. So the last test asserts the page STILL reports not-found when neither subject
// resolves, and that it reports the EXECUTOR's failure rather than the second one.

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
});

after(async () => {
  await console_?.close();
  await platform?.close();
});

async function get(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie }, redirect: "manual" });
  return { status: res.status, html: await res.text() };
}

/**
 * routeAnswers points the stub at a per-path reply table.
 *
 * Per-path rather than one canned answer, because this page now makes TWO upstream reads and the whole
 * behaviour under test is which one it falls back to.
 */
function routeAnswers(table) {
  platform.set((req, res) => {
    const path = req.url.split("?")[0];
    const hit = table[path] ?? { status: 404, body: { error: "no such run" } };
    res.writeHead(hit.status, { "content-type": "application/json" });
    res.end(JSON.stringify(hit.body));
  });
}

/** linkedRun is a LinkedRunView as the generated type defines it — every field, no partial fixture. */
function linkedRun(runId) {
  return {
    run_id: runId,
    workflow_id: "openclaw/openclaw",
    config_hash: "1f9b7ff9044ff7f5115020ffe662f48af773edea38465845a7fd186a0b91031e",
    config_hash_display: "1f9b7ff9044f",
    source_revision: "1a51b0e58d674fdccd6704389f1116adfc901918",
    tool_version: "0.11.0-dev",
    linked_at: "2026-08-03T04:00:00Z",
    scores: [
      { metric: "quality", value: 0.8039, ci_low: 0.75, ci_high: 0.8618 },
      { metric: "cost_usd", value: 0.2097, ci_low: 0.1941, ci_high: 0.226 },
    ],
  };
}

test("a linked run renders instead of 'no such run' when the executor has no record of it", async () => {
  const id = "run-86945c8cd5b2";
  routeAnswers({
    [`/api/v1/runs/${id}`]: { status: 404, body: { error: "no such run" } },
    [`/api/v1/runs/${id}/link`]: { status: 200, body: linkedRun(id) },
  });

  const { status, html } = await get(`/app/runs/${id}`);
  assert.equal(status, 200);
  assert.ok(html.includes(id), "the subject must be named in the frame");
  assert.ok(
    !/No such run/i.test(html),
    "the console reported 'no such run' for a run the platform had linked — the exact defect this closes",
  );
  assert.ok(
    /Measured on your machine/i.test(html),
    "a linked run must say where it was measured; the platform did not execute it",
  );
});

test("the linked run's scores come through WITH their intervals", async () => {
  const id = "run-86945c8cd5b2";
  routeAnswers({
    [`/api/v1/runs/${id}`]: { status: 404, body: { error: "no such run" } },
    [`/api/v1/runs/${id}/link`]: { status: 200, body: linkedRun(id) },
  });

  const { html } = await get(`/app/runs/${id}`);
  assert.ok(html.includes("0.8039"), "the quality value the CLI computed is missing");
  // 🔴 The interval is not decoration. A value rendered without one is a claim this platform does not
  // make, and it is the single easiest thing to drop when a table is reshaped.
  assert.ok(html.includes("0.7500") && html.includes("0.8618"), "the 95% interval is missing beside the value");
  assert.ok(html.includes("0.2097"), "the cost metric is missing");
  assert.ok(html.includes("1f9b7ff9044f"), "the configuration this run measured is not identified");
  assert.ok(html.includes("0.11.0-dev"), "the CLI build is not shown — scores are only comparable within one harness");
});

test("an executed run still wins: the executor's record is the primary read", async () => {
  const id = "run-executed";
  routeAnswers({
    [`/api/v1/runs/${id}`]: {
      status: 200,
      body: {
        run_id: id,
        config_hash: "a".repeat(64),
        config_hash_display: "aaaaaaaaaaaa",
        source_revision: "rev1",
        seed: 1000,
        status: "succeeded",
        nodes: [],
      },
    },
    // Answering here too, to prove the page does not prefer it or render both.
    [`/api/v1/runs/${id}/link`]: { status: 200, body: linkedRun(id) },
  });

  const { html } = await get(`/app/runs/${id}`);
  assert.ok(
    /node by node/i.test(html),
    "an executed run must keep the executor lede — the richer subject stays the primary answer",
  );
  assert.ok(
    !/Measured on your machine/i.test(html),
    "the linked panel rendered over an executed run; the fallback must be a fallback",
  );
});

test("🔴 a run that is neither executed NOR linked still reports not-found", async () => {
  const id = "run-does-not-exist";
  routeAnswers({
    [`/api/v1/runs/${id}`]: { status: 404, body: { error: "no such run" } },
    [`/api/v1/runs/${id}/link`]: { status: 404, body: { error: "no such linked run" } },
  });

  const { html } = await get(`/app/runs/${id}`);
  assert.ok(
    /No such run/i.test(html),
    "the fallback swallowed a genuinely missing run — a typo'd identifier would now render as an " +
      "almost-empty success, which is the failure the fallback was supposed to avoid, inverted",
  );
  assert.ok(
    !/Measured on your machine/i.test(html),
    "an unlinked run must not render the linked panel",
  );
});

test("a not-mounted linking capability does not become 'no such run'", async () => {
  const id = "run-unmounted";
  routeAnswers({
    [`/api/v1/runs/${id}`]: { status: 503, body: { error: "the P2 store is not mounted" } },
    [`/api/v1/runs/${id}/link`]: { status: 503, body: { error: "run-linking ingest is not mounted" } },
  });

  const { html } = await get(`/app/runs/${id}`);
  assert.ok(
    /not mounted on this deployment/i.test(html),
    "an absent capability must read as absent, never as a wrong identifier",
  );
});
