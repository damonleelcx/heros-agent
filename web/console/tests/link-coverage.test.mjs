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

// ── P23 §12.8 · Documentation reachability (FR21/FR22) ───────────────────────
//
// Two properties, and the second is the one that ships inside a binary.
//
//   1. Every documentation page is reachable BY NAVIGATION, not only by URL. A page nobody can walk to
//      is a page a search engine has and a customer does not.
//   2. Every anchor referenced from CLI output or console copy RESOLVES. A renamed heading breaks a link
//      that ships inside a binary the customer already installed, and no deploy fixes it for them.

test("12.8 — every documentation page is reachable by navigation from /docs", async () => {
  const manifest = JSON.parse(
    await (await import("node:fs/promises")).readFile(
      new URL("../docs/slug-manifest.json", import.meta.url),
      "utf8",
    ),
  );
  const pages = manifest.pages.map((page) => page.route).filter((route) => route.startsWith("/docs/"));
  assert.ok(pages.length > 0, "the slug manifest lists no documentation pages");

  const index = await fetch(`${console_.base}/docs`);
  assert.equal(index.status, 200);
  const html = await index.text();

  const unreachable = pages.filter((route) => !html.includes(`href="${route}"`));
  assert.deepEqual(
    unreachable,
    [],
    `these pages exist and cannot be walked to from /docs: ${unreachable.join(", ")}. A page reachable ` +
      `only by URL is a page a search engine has and a customer does not.`,
  );
});

test("12.8 — every anchor the console or the CLI points at resolves", async () => {
  const { readFile } = await import("node:fs/promises");
  const manifest = JSON.parse(await readFile(new URL("../docs/slug-manifest.json", import.meta.url), "utf8"));
  const anchors = new Map(manifest.pages.map((page) => [page.route, new Set(page.anchors)]));

  /*
   * The deep links this product publishes into its own documentation. Each is a PUBLISHED CONTRACT: it
   * appears in shipped copy, and a renamed heading breaks it for readers who cannot be reached.
   *
   * Listed explicitly rather than scraped, because the point is that adding one is a deliberate act that
   * accepts the maintenance — not something that accumulates by accident.
   */
  const PUBLISHED_DEEP_LINKS = [
    "/docs/reference/cli#exit-codes",
    "/docs/reference/cli#configuration-and-which-source-wins",
    "/docs/concepts/refusals",
    "/docs/concepts/glossary",
    "/docs/start/install",
    "/docs/start/quickstart",
  ];

  for (const link of PUBLISHED_DEEP_LINKS) {
    const [route, anchor] = link.split("#");
    assert.ok(anchors.has(route), `${route} is referenced but does not exist`);
    if (anchor) {
      assert.ok(
        anchors.get(route).has(anchor),
        `${link} is referenced in shipped copy and the anchor does not resolve — this link ships inside a ` +
          `binary a customer already installed, and no deploy fixes it for them`,
      );
    }
    // And it must actually serve.
    const res = await fetch(`${console_.base}${route}`);
    assert.equal(res.status, 200, `${route} does not serve`);
  }
});
