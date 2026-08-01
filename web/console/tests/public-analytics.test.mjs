// public-analytics.test.mjs holds P24 wave 24e — GA4 and Clarity, on the public surface and nowhere else.
//
// # The three refusals this file is really about
//
// Session replay on `/app/**`. Session replay on every operator route. Any browser analytics tag on
// either. Each is asserted THREE ways, because the design says three independent mechanisms and one of
// any pair is always a checklist:
//
//   1. unreachable from those layouts — the component is mounted from the public layout only;
//   2. the origin is absent from those prefixes' policy — the class does not permit the category;
//   3. the runtime is absent from any chunk those routes load — `scan-bundle.mjs`, demonstrated red.
//
// # Why the configuration is asserted rather than read
//
// Every line of `GA4_CONFIG` turns something OFF that is on by default. "We do not do remarketing" in a
// document is worth nothing; the test reads the object that is actually passed to `gtag('config', …)`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import {
  CLARITY_CONFIG,
  CLARITY_UNMASKED,
  GA4_CONFIG,
  plannedIntegrations,
} from "../../design-system/public-analytics.ts";
import {
  ANALYTICS_EVENTS,
  ANALYTICS_ALLOWLIST,
  PENDING_CALL_SITES,
  PUBLIC_FUNNEL_EVENTS,
  SECTIONS,
} from "../../design-system/analytics-events.ts";
import { ALLOWED_ORIGINS, SURFACE_CLASS_RULES } from "../../design-system/third-party-policy.ts";
import { buildContentSecurityPolicy } from "../../design-system/csp.ts";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const WEB = new URL("../..", import.meta.url).pathname;
const readWeb = (rel) => readFile(join(WEB, rel), "utf8");

// ── 5.1 · The origins ────────────────────────────────────────────────────────

test("5.1 each analytics and replay origin carries its category, directive, surfaces and budget", () => {
  const analytics = ALLOWED_ORIGINS.filter((o) => o.category === "product_analytics");
  const replay = ALLOWED_ORIGINS.filter((o) => o.category === "session_replay");
  assert.ok(analytics.length >= 1, "no analytics origin is on the allowlist");
  assert.ok(replay.length >= 1, "no session-replay origin is on the allowlist");
  for (const origin of [...analytics, ...replay]) {
    // 🔴 PUBLIC ONLY. Not "public first", not "public by default" — the surfaces list is one entry long.
    assert.deepEqual(
      [...origin.surfaces],
      ["public"],
      `${origin.origin} claims a surface other than public — a URL under /app carries variant, run, ` +
        `node and tenant identifiers, and a replay of /app/studio is a copy of most of the ` +
        `never-permitted list in internal/runlink/allowlist.go`,
    );
    assert.ok(origin.budgetBytes > 0, `${origin.origin} has no transfer budget`);
    assert.notEqual(origin.directive, "script-src", `${origin.origin} would put a host on script-src`);
  }
});

// ── 5.2 · Loaded nonced, after first paint, only under a grant ───────────────

test("5.2 nothing loads without the grant, and nothing loads on a non-public surface", () => {
  // Exercised through `plannedIntegrations`, which is the DECISION half. `installPublicAnalytics`
  // returns [] outside a browser for a different reason (no `window`), so testing it here would have
  // passed for every case including the ones that must fail — a green result that proves nothing.
  const base = {
    measurementId: "G-TEST", clarityProjectId: "test", release: "v", edition: "dev",
    granted: { productAnalytics: true, sessionReplay: true },
  };
  // A tenant surface, with EVERY category granted: still nothing.
  assert.deepEqual(
    plannedIntegrations({ ...base, surface: "tenant.studio" }),
    [],
    "a tenant surface loaded an analytics or replay tag with categories granted",
  );
  assert.deepEqual(plannedIntegrations({ ...base, surface: "operator.tenants" }), []);
  // A public surface with nothing granted: still nothing.
  assert.deepEqual(
    plannedIntegrations({ ...base, surface: "public.home", granted: { productAnalytics: false, sessionReplay: false } }),
    [],
  );
  // And per category, not all-or-nothing.
  assert.deepEqual(
    plannedIntegrations({ ...base, surface: "public.home", granted: { productAnalytics: true, sessionReplay: false } }),
    ["product_analytics"],
  );
  assert.deepEqual(
    plannedIntegrations({ ...base, surface: "public.home", granted: { productAnalytics: false, sessionReplay: true } }),
    ["session_replay"],
  );
  // An absent id keeps the tag absent even with the grant — the deployment posture, not the visitor's.
  assert.deepEqual(
    plannedIntegrations({ ...base, surface: "public.home", measurementId: "", clarityProjectId: "" }),
    [],
  );
});

test("5.2 the tags are deferred past first paint and never block render", async () => {
  const src = await readWeb("design-system/public-analytics.ts");
  assert.match(src, /requestAnimationFrame/, "the tag is not deferred past the first frame");
  assert.match(src, /requestIdleCallback/, "the tag is not deferred to an idle moment");
  assert.match(src, /el\.async = true/, "the injected script is not async");
  assert.doesNotMatch(src, /document\.write/, "the tag is written into the parser");
  // And the public surface's own requirement is untouched: nothing here is awaited by a render path.
  const layout = await readWeb("console/src/app/(public)/layout.tsx");
  assert.match(layout, /<PublicAnalytics \{\.\.\.analytics\} \/>/);
  assert.doesNotMatch(layout, /await installPublicAnalytics/, "a tag is awaited on the render path");
});

test("5.2 the public page still renders with the platform API stopped", async () => {
  // Asserted by the EXISTING decisive test rather than duplicated here: `public-surface.test.mjs` stops
  // the platform entirely and asks for the page. What this checks is that P24 did not put anything on
  // that path — the analytics config reads env, a cookie and a header, and calls no upstream.
  const config = await readFile(join(ROOT, "src", "lib", "publicAnalyticsConfig.ts"), "utf8");
  assert.doesNotMatch(config, /platformApi|bff|fetch\(/, "the analytics configuration makes an upstream call");
});

// ── 5.3 · GA4, configured rather than documented ─────────────────────────────

test("5.3 🔴 every GA4 setting that must be off is off, as data", () => {
  assert.equal(GA4_CONFIG.anonymize_ip, true, "IP anonymisation is not on");
  assert.equal(GA4_CONFIG.allow_google_signals, false, "cross-site / advertising signals are on");
  assert.equal(GA4_CONFIG.allow_ad_personalization_signals, false, "ad personalisation is on");
  assert.equal(GA4_CONFIG.restricted_data_processing, true);
  // The one that does the most work: no client id is written at all, so a counted visitor is not a
  // persistently identified one.
  assert.equal(GA4_CONFIG.client_storage, "none", "GA4 may write a client identifier");
  assert.equal(GA4_CONFIG.conversion_linker, false, "a conversion pixel is permitted");
  // Automatic page views off: every event this product reports comes from the central enum.
  assert.equal(GA4_CONFIG.send_page_view, false);
});

test("5.3 no remarketing, no advertising identifier and no conversion pixel anywhere in the source", async () => {
  const src = (await readWeb("design-system/public-analytics.ts"))
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");
  for (const f of ["remarketing", "user_id", "doubleclick", "adsbygoogle", "gtm.js"]) {
    assert.doesNotMatch(src, new RegExp(f, "i"), `the analytics module references ${f}`);
  }
});

// ── 5.4 · Clarity, masked by default ─────────────────────────────────────────

test("5.4 🔴 Clarity starts fully masked, and every unmasking needs a recorded reason", () => {
  assert.equal(CLARITY_CONFIG.mask, "all", "Clarity does not mask everything by default");
  assert.equal(CLARITY_CONFIG.maskTextInputs, true, "text inputs are not masked");
  assert.equal(CLARITY_CONFIG.cookies, false);
  // The opt-out list is empty, and an entry needs a reason. Asserted rather than assumed, because the
  // way masking stops meaning anything is one unmasked selector at a time.
  assert.deepEqual([...CLARITY_UNMASKED], [], "an element is unmasked with no review of why");
  for (const entry of CLARITY_UNMASKED) {
    assert.ok(entry.reason && entry.reason.length > 40, `${entry.selector} is unmasked with no recorded reason`);
  }
});

// ── 5.5 · The replay origin is absent from tenant and operator prefixes ──────

test("5.5 🔴 the replay origin cannot appear on a tenant or operator prefix, with everything granted", () => {
  const replay = ALLOWED_ORIGINS.filter((o) => o.category === "session_replay").map((o) => o.origin);
  assert.ok(replay.length > 0, "there is no replay origin — the assertion would be vacuous");
  const all = ["essential", "product_analytics", "session_replay", "error_diagnostics"];
  for (const [consoleId, pathname] of [
    ["customer", "/app"], ["customer", "/app/studio"], ["customer", "/api/console/x"],
    ["operator", "/"], ["operator", "/tenants"], ["operator", "/audit"],
  ]) {
    const csp = buildContentSecurityPolicy({ consoleId, pathname, nonce: "N", dev: false, granted: all });
    for (const origin of replay) {
      const host = origin.replace("https://*.", "").replace("https://", "");
      assert.ok(!csp.includes(host), `${consoleId}${pathname} names the replay host ${host}`);
    }
    assert.doesNotMatch(csp, /clarity/i, `${consoleId}${pathname} names a Clarity origin`);
    assert.doesNotMatch(csp, /googletagmanager|google-analytics/i, `${consoleId}${pathname} names an analytics origin`);
  }
  // And structurally: the category is not in those classes' lists at all.
  for (const klass of ["tenant", "operator"]) {
    assert.ok(!SURFACE_CLASS_RULES[klass].categories.includes("session_replay"));
    assert.ok(!SURFACE_CLASS_RULES[klass].categories.includes("product_analytics"));
  }
});

test("5.5 the replay script is unreachable from the tenant and operator layouts", async () => {
  // Mechanism 1 of 3: the component is mounted from the PUBLIC layout only.
  const publicLayout = await readWeb("console/src/app/(public)/layout.tsx");
  assert.match(publicLayout, /PublicAnalytics/, "the public layout does not mount the tags");
  for (const rel of ["console/src/app/layout.tsx", "console/src/app/app/layout.tsx", "admin-console/src/app/layout.tsx"]) {
    const src = await readWeb(rel);
    assert.doesNotMatch(src, /PublicAnalytics/, `${rel} mounts the public analytics tags`);
    assert.doesNotMatch(src, /public-analytics/, `${rel} imports the public analytics module`);
  }
  // Mechanism 3 of 3: the build refuses the runtime in a guarded chunk. Its red demonstration lives in
  // third-party-fence.test.mjs; this asserts the real build's partition is clean and non-empty.
  const { stdout } = await exec("node", [join(ROOT, "scripts", "scan-bundle.mjs")], { cwd: ROOT });
  assert.match(stdout, /no analytics or session-replay runtime in the tenant partition/);
  const match = /(\d+) chunk\(s\) reachable from a tenant route, (\d+) reachable only from the public/.exec(stdout);
  assert.ok(Number(match[1]) > 0 && Number(match[2]) > 0, "the reachability partition is not a partition");
});

// ── 5.6 · Funnel events from the central enum ────────────────────────────────

test("5.6 the event scan passes, and goes RED on a template literal, a variable and an unknown name", async () => {
  const { stdout } = await exec("node", [join(WEB, "design-system", "scan-events.mjs")], { cwd: ROOT });
  assert.match(stdout, /event scan passed/);
  const self = await exec("node", [join(WEB, "design-system", "scan-events.mjs"), "--self-test"], { cwd: ROOT });
  assert.match(self.stdout, /self-test passed/);
});

test("5.6 every enum member is either called or listed as pending WITH a reason", () => {
  // The both-directions rule applied to events: a member nothing emits is a permission nobody asked for,
  // exactly like a stale allowlist entry. "Declared and never wired" has to be a visible fact.
  const called = ["page_viewed", "section_reached", "install_page_viewed", "signup_started", "surface_viewed"];
  for (const event of ANALYTICS_EVENTS) {
    if (called.includes(event)) continue;
    const reason = PENDING_CALL_SITES[event];
    assert.ok(reason, `${event} is declared, is never emitted, and has no recorded reason`);
    assert.ok(reason.length > 60, `${event}'s pending reason does not say why`);
  }
  assert.ok(PUBLIC_FUNNEL_EVENTS.length >= 9, "the funnel enum looks truncated");
  assert.equal(new Set(ANALYTICS_EVENTS).size, ANALYTICS_EVENTS.length, "an event name is declared twice");
});

test("5.6 an analytics event carries only allowlisted parameters", () => {
  const keys = ANALYTICS_ALLOWLIST.map((f) => f.name);
  assert.deepEqual(keys, ["event.name", "surface_id", "plan_name", "edition", "release", "occurred_at"]);
  for (const field of ANALYTICS_ALLOWLIST) {
    assert.ok(field.why.length > 40, `${field.name} has no justification a reviewer can read`);
  }
  for (const forbidden of ["tenant", "principal", "run_id", "variant", "node", "path", "query", "referrer", "url"]) {
    assert.ok(!keys.some((k) => k.includes(forbidden)), `${forbidden} is expressible on an analytics event`);
  }
  assert.ok(SECTIONS.length >= 3 && SECTIONS.every((s) => /^[a-z]+$/.test(s)), "a section id is not a closed label");
});
