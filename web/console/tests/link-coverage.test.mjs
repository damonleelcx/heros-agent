// link-coverage.test.mjs is the P11 §5 acceptance surface: link coverage renders beside the SUM figure
// on the account view, and the THREE states — complete, partial, unknown — are visibly distinct
// (tasks 5.2/5.3). It runs the real console against a stub platform and asserts on rendered HTML, the
// same way the rest of the acceptance gate does.

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

function answering(status, body) {
  platform.set((_req, res) => {
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  });
}

async function get(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie }, redirect: "manual" });
  return { status: res.status, html: await res.text() };
}

// billing builds a minimal-but-complete BillingView, with an optional link_coverage.
function billing(linkCoverage) {
  return {
    customer_id: "tenant-hermes",
    period: "2026-07",
    sum: 12.5,
    sum_unit: "USD",
    plan_id: "team",
    plan_name: "Team",
    plan_config_version: "v1",
    entitlements: [],
    meters: [],
    invoice: { lines: [] },
    state: { payment_failed: false, past_due: false },
    savings: {},
    empty: false,
    link_coverage: linkCoverage,
  };
}

test("5.2/5.3 — partial coverage states the fraction beside SUM and refuses to estimate", async () => {
  answering(200, billing({ runs_linked: 3, runs_reported: 10, known: true, complete: false }));
  const { html } = await get("/app/account");
  assert.match(html, /Coverage of this figure/, "the coverage panel is not shown beside SUM");
  assert.match(html, /3 of 10/, "the linked/reported fraction is not stated");
  assert.match(html, /never estimated/, "the no-extrapolation guarantee is not stated");
  // SUM is still there — coverage sits beside it, not instead of it.
  assert.match(html, /Spend this period/);
});

test("5.3 — complete coverage is stated as complete, not merely 100%", async () => {
  answering(200, billing({ runs_linked: 5, runs_reported: 5, known: true, complete: true }));
  const { html } = await get("/app/account");
  assert.match(html, /Coverage of this figure/);
  assert.match(html, /all known activity/, "complete coverage does not say it reflects all known activity");
});

test("5.3 — unknown coverage is distinguished from complete, never rendered as full", async () => {
  // No link_coverage at all — the server did not report it. This must read as UNKNOWN, not as 100%.
  answering(200, billing(null));
  const { html } = await get("/app/account");
  assert.match(html, /Coverage of this figure/);
  assert.match(html, /Coverage is unknown/, "absent coverage is not rendered as the distinct unknown state");
  assert.doesNotMatch(html, /all known activity/, "unknown coverage was collapsed into complete");
});
