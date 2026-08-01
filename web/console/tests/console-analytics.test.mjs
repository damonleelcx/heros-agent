// console-analytics.test.mjs holds P24 wave 24f — console usage, emitted server-side, and the boundary
// that keeps an analytics figure from becoming a business number.
//
// # The two properties, and why the second one is the harder sell
//
// The first is mechanical: a browser tag under `/app/**` would report a URL, and a URL under `/app`
// carries variant, run, node and tenant identifiers. So the event is emitted from the server with a
// surface id from a closed enum, and a call site cannot supply anything else.
//
// The second is D8, and it is the one that gets argued with: **no analytics figure may become a business
// number.** Not on a customer-facing surface, not in an invoiced quantity, not in a claim. GA4 is a
// third source of truth held by a party we do not control, sampled, ad-blocked and consent-gated —
// systematically wrong in a direction nobody can quantify. The console's read-model rule already forbids
// the browser recomputing a statistical claim for the weaker version of this reason.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import {
  buildConsoleEvent,
  relayConsoleEvent,
  relayState,
} from "../../design-system/console-analytics.ts";
import { ANALYTICS_ALLOWLIST_KEYS, CONSOLE_EVENTS } from "../../design-system/analytics-events.ts";
import { SURFACES } from "../../design-system/third-party-policy.ts";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const WEB = new URL("../..", import.meta.url).pathname;
const readWeb = (rel) => readFile(join(WEB, rel), "utf8");

const BASE = {
  event: "surface_viewed",
  surfaceId: "tenant.studio",
  edition: "hosted",
  release: "v0.24.0",
  occurredAt: 1_754_006_400.789,
};

// ── 6.1 · Constructed from an allowlist, both directions ─────────────────────

test("6.1 the constructed event carries exactly the allowlist, and every entry is populated", () => {
  const event = buildConsoleEvent({ ...BASE, planName: "Team" });
  assert.ok(event, "a valid input produced no event");
  assert.deepEqual(Object.keys(event).sort(), [...ANALYTICS_ALLOWLIST_KEYS].sort());
  for (const key of ANALYTICS_ALLOWLIST_KEYS) {
    assert.ok(key in event, `allowlist entry ${key} is populated by nothing`);
    assert.notEqual(event[key], undefined, `${key} is present but undefined`);
  }
});

test("6.1 🔴 a field added to the input does not reach the event", () => {
  // The asymmetry, demonstrated: an extra field on an internal representation is ABSENT by default,
  // because the event is BUILT rather than serialised-and-stripped.
  const event = buildConsoleEvent({
    ...BASE,
    tenantId: "cus_nousresearch",
    principalId: "prn_7f31",
    runId: "run-9",
    path: "/app/variants/var-7f31c9/scorecard",
    referrer: "https://example.test/?q=secret",
  });
  const serialised = JSON.stringify(event);
  for (const leak of ["cus_nousresearch", "prn_7f31", "run-9", "/app/variants", "example.test", "secret"]) {
    assert.ok(!serialised.includes(leak), `the constructed event carries ${leak}`);
  }
});

test("6.1 the surface, the event name and the plan are each a closed value", () => {
  // A path never survives.
  assert.equal(buildConsoleEvent({ ...BASE, surfaceId: "/app/runs/run-9/live" })["surface_id"], "unknown");
  assert.equal(buildConsoleEvent({ ...BASE, surfaceId: "tenant.studio" })["surface_id"], "tenant.studio");
  // An unrecognised plan becomes empty rather than being passed through — and rather than vanishing,
  // which would make "every entry is populated" true only for the values that happened to be valid.
  assert.equal(buildConsoleEvent({ ...BASE, planName: "Enterprise Plus, $4,000/mo" })["plan_name"], "");
  assert.equal(buildConsoleEvent({ ...BASE, planName: "Business" })["plan_name"], "Business");
  // An event name that is not a member produces NO event at all.
  assert.equal(buildConsoleEvent({ ...BASE, event: "surface_viewed_v2" }), null);
  assert.deepEqual([...CONSOLE_EVENTS], ["surface_viewed"]);
  // And the timestamp is truncated: a millisecond value joined across two events is a weak identifier.
  assert.equal(buildConsoleEvent(BASE)["occurred_at"], 1_754_006_400);
});

// ── 6.2 · Emitted server-side, from both BFF halves ──────────────────────────

test("6.2 both consoles emit surface_viewed from the SERVER, and neither call site can supply a path", async () => {
  for (const rel of ["console/src/lib/consoleAnalytics.ts", "admin-console/src/lib/consoleAnalytics.ts"]) {
    const src = await readWeb(rel);
    assert.match(src, /^import "server-only";/m, `${rel} is not server-only`);
    assert.match(src, /recordSurfaceViewed\(\): void/, `${rel}'s emitter takes an argument`);
    assert.match(src, /resolveSurface\(/, `${rel} does not resolve the surface through the closed enum`);
  }
  for (const rel of ["console/src/app/layout.tsx", "admin-console/src/app/layout.tsx"]) {
    const src = await readWeb(rel);
    assert.match(src, /recordSurfaceViewed\(\);/, `${rel} does not emit the event`);
    assert.doesNotMatch(src, /await recordSurfaceViewed/, `${rel} awaits analytics on a render path`);
  }
});

test("6.2 the relayed BYTES carry no path, no query string and no free text", async () => {
  let body = "";
  await relayConsoleEvent(
    {
      measurementId: "G-TEST",
      apiSecret: "secret-not-real",
      fetchImpl: async (_url, init) => {
        body = String(init.body);
        return new Response("{}", { status: 200 });
      },
    },
    buildConsoleEvent({
      ...BASE,
      surfaceId: "/app/variants/var-7f31c9/scorecard?tenant=acme",
      planName: "Free",
    }),
  );
  assert.ok(body.length > 0, "nothing was transmitted — the assertion would be vacuous");
  for (const leak of ["/app", "var-7f31c9", "tenant=acme", "?", "acme"]) {
    assert.ok(!body.includes(leak), `the relayed body carries ${JSON.stringify(leak)}`);
  }
  const parsed = JSON.parse(body);
  assert.equal(parsed.events[0].name, "surface_viewed");
  assert.equal(parsed.events[0].params.surface_id, "unknown");
  // 🔴 A CONSTANT client id. The protocol requires the field; a per-request value would be a user
  // identifier invented to satisfy a schema.
  assert.equal(parsed.client_id, "heros-console-server");
  assert.equal(parsed.non_personalized_ads, true);
  assert.deepEqual(
    Object.keys(parsed.events[0].params).sort(),
    ["edition", "occurred_at", "plan_name", "release", "surface_id"],
  );
});

// ── 6.3 · Server-only relay ──────────────────────────────────────────────────

test("6.3 the relay is absent without a secret, and no browser module can reach it", async () => {
  assert.equal(relayState({ measurementId: "", apiSecret: "" }), "absent");
  assert.equal(relayState({ measurementId: "G-TEST", apiSecret: "" }), "absent");
  assert.equal(relayState({ measurementId: "G-TEST", apiSecret: "s" }), "configured");

  // Absent means nothing is transmitted at all — asserted rather than assumed.
  let called = false;
  await relayConsoleEvent(
    { measurementId: "", apiSecret: "", fetchImpl: async () => { called = true; return new Response("{}"); } },
    buildConsoleEvent(BASE),
  );
  assert.equal(called, false, "an absent relay transmitted");

  // No client component imports it. `server-only` makes that a build error; this makes the rule visible.
  const offenders = [];
  const walk = async (dir) => {
    for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
      const child = join(dir, entry.name);
      if (entry.isDirectory()) { await walk(child); continue; }
      if (!/\.tsx?$/.test(entry.name)) continue;
      const src = await readFile(join(ROOT, child), "utf8");
      if (/^"use client";/m.test(src) && /consoleAnalytics|console-analytics/.test(src)) offenders.push(child);
    }
  };
  await walk("src");
  assert.deepEqual(offenders, [], "a client component reaches the server-side analytics relay");
});

test("6.3 a failing relay never surfaces to a caller", async () => {
  // Fail-static. `relayConsoleEvent` returns normally when the backend refuses, times out or throws —
  // there is nothing a served request could do with the failure, so it never learns of one.
  await relayConsoleEvent(
    { measurementId: "G", apiSecret: "s", fetchImpl: async () => { throw new Error("network down"); } },
    buildConsoleEvent(BASE),
  );
  await relayConsoleEvent(
    { measurementId: "G", apiSecret: "s", fetchImpl: async () => new Response("no", { status: 500 }) },
    buildConsoleEvent(BASE),
  );
});

// ── 6.5 · An analytics figure may never become a business number ─────────────

test("6.5 🔴 no analytics figure is rendered, invoiced or claimed", async () => {
  /*
   * Asserted, not documented (D8). Three separate things are forbidden and each fails differently:
   *
   *   - RENDERED on a customer-facing surface — it would be a second source of truth for a number the
   *     substrate already owns, and a wrong one;
   *   - used to derive an INVOICED quantity — metering derives from LINKED runs, and the platform never
   *     infers or extrapolates;
   *   - presented as a PLATFORM METRIC — the metric catalogue is the platform's own, computed where it
   *     says it is computed.
   */
  const offenders = [];
  const walk = async (root, dir) => {
    for (const entry of await readdir(join(root, dir), { withFileTypes: true })) {
      const child = join(dir, entry.name);
      if (entry.isDirectory()) { await walk(root, child); continue; }
      if (!/\.tsx?$/.test(entry.name)) continue;
      const src = (await readFile(join(root, child), "utf8"))
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");
      // A component that READS an analytics figure back. The relay is write-only by construction —
      // there is no reader in this codebase — so any import of an analytics module by a rendering path
      // would be the first one.
      if (!/gtag|dataLayer|clarity|google-analytics|analytics-events|console-analytics|public-analytics/.test(src)) continue;
      const permitted = [
        join("src", "lib", "consoleAnalytics.ts"),
        join("src", "lib", "publicAnalyticsConfig.ts"),
        join("src", "components", "publicAnalytics.tsx"),
        join("src", "app", "layout.tsx"),
        join("src", "app", "(public)", "layout.tsx"),
      ];
      if (permitted.includes(child)) continue;
      offenders.push(child);
    }
  };
  await walk(ROOT, "src");
  await walk(join(WEB, "admin-console"), "src");
  assert.deepEqual(offenders, [], "an analytics module is reachable from a rendering path");

  // And there is no read path at all: the relay exports no getter, no query, no aggregate.
  const relay = await readWeb("design-system/console-analytics.ts");
  assert.doesNotMatch(relay, /export (async )?function (get|read|query|count|aggregate)/, "the relay has a read path");
  assert.doesNotMatch(relay, /method: "GET"/, "the relay reads from the analytics backend");
});

test("6.5 the billing and metering paths carry no analytics import", async () => {
  const { stdout } = await exec(
    "grep",
    ["-rl", "-e", "gtag", "-e", "dataLayer", "-e", "clarity", "-e", "google-analytics", join(WEB, "..", "internal")],
    { cwd: WEB },
  ).catch((error) => ({ stdout: error.stdout ?? "" }));
  const hits = stdout.split("\n").filter(Boolean).filter((f) => /billing|metering|scorecard|evalstats/.test(f));
  assert.deepEqual(hits, [], "a billing, metering or scoring package references an analytics backend");
});

// ── 6.6 · Nothing entered a hashed structure ─────────────────────────────────

test("6.6 the P0 golden config_hash vectors reproduce byte-identically", async () => {
  // The whole phase is explicitly outside the resolver, the gates and the transforms. This is the
  // assertion that says so with a number rather than with a scope statement — and it runs the REAL
  // vectors rather than checking that no file was touched.
  const { stdout } = await exec("go", ["test", "./internal/confighash/", "-run", "Golden|Vector|Hash", "-count=1"], {
    cwd: join(WEB, ".."),
    env: { ...process.env, GOWORK: "off" },
  });
  assert.match(stdout, /^ok/m, `the config_hash golden vectors did not reproduce:\n${stdout}`);
});

// ── 7.5 · The seven propagation layers ───────────────────────────────────────

test("7.5 🔴 all seven propagation layers carry the change", async () => {
  /*
   * `systemic-fix-propagation` in one assertion. A change like this one is not done when the code
   * works — it is done when every layer that could contradict it agrees, and the failure mode is
   * always the same: six layers land, the seventh is a file nobody opened, and the contradiction is
   * discovered by a customer reading a document.
   *
   * Each row below is a LAYER and a thing that must be true of it. A missing row is a red build, which
   * is the only form of "we checked all seven" that survives.
   */
  const layers = [
    ["1 shared artefact", "design-system/third-party-policy.ts", /ALLOWED_ORIGINS/],
    ["2a customer middleware", "console/src/middleware.ts", /buildContentSecurityPolicy/],
    ["2b operator middleware", "admin-console/src/middleware.ts", /buildContentSecurityPolicy/],
    ["3a customer next.config", "console/next.config.mjs", /productionBrowserSourceMaps: false/],
    ["3b operator next.config", "admin-console/next.config.mjs", /productionBrowserSourceMaps: false/],
    ["4a customer scan-bundle", "console/scripts/scan-bundle.mjs", /OBSERVABILITY RUNTIME/],
    ["4b operator scan-bundle", "admin-console/scripts/scan-bundle.mjs", /OBSERVABILITY RUNTIME/],
    ["5 Go initialisation", "../internal/launch/launch.go", /erroreport\.FromEnv/],
    ["6a deployment manifests", "../internal/deploy/p24_origins_test.go", /TestDeploymentManifestsCarryNoReportingIdentity/],
    ["6b air-gapped packager", "../deploy/scripts/package-airgapped.sh", /check-external-origins\.sh/],
    ["7a release pipeline", "../scripts/release-cli.sh", /check-external-origins\.sh/],
    ["7b source-map upload", "console/scripts/upload-sourcemaps.mjs", /HEROS_SOURCEMAP_UPLOAD_TOKEN/],
    ["8a legal — sub-processors", "console/content/legal/en/sub-processors/1.0.0.md", /material: true/],
    ["8b legal — privacy", "console/content/legal/en/privacy/1.1.0.md", /sub-processors/],
    ["8c operator notice", "../docs/decisions/p24-operator-acceptable-use.md", /no consent banner/i],
    ["8d sales FAQ", "../docs/sales/P24-analytics-and-error-monitoring-faq.md", /self-hosted collector/],
  ];
  for (const [layer, rel, expected] of layers) {
    const src = await readFile(join(WEB, rel), "utf8").catch(() => "");
    assert.ok(src.length > 0, `layer ${layer}: ${rel} does not exist`);
    assert.match(src, expected, `layer ${layer} (${rel}) does not carry the change`);
  }
});

// ── 7.6 · The sales answers say what shipped ─────────────────────────────────

test("7.6 the FAQ answers all four questions, and the fourth answers 'no'", async () => {
  const faq = await readFile(join(WEB, "..", "docs", "sales", "P24-analytics-and-error-monitoring-faq.md"), "utf8");
  for (const question of [
    /Do you record my screen/i,
    /Can I turn it off/i,
    /Does your on-prem install phone home/i,
    /Can I get error reports from my own install/i,
  ]) {
    assert.match(faq, question, `the FAQ does not answer ${question}`);
  }
  // 🔴 The fourth answer is the one that must not be softened. A "roadmap" answer to a capability that
  // does not exist is the claim a customer holds us to.
  const fourth = faq.slice(faq.indexOf("Can I get error reports from my own install"));
  assert.match(fourth, /\*\*No\./, "the fourth question is not answered no");
  assert.match(fourth, /self-hosted collector.*not built|not built.*self-hosted collector/s);
  assert.doesNotMatch(fourth, /coming soon|on the roadmap|planned for/i, "the FAQ promises an unbuilt capability");
});
