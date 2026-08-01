// browser-reporting.test.mjs holds P24 wave 24c — the browser half of the error-reporting boundary.
//
// # What this file can assert, and what only a browser can
//
// The CONSTRUCTION is asserted here: which fields exist, which are dropped, that there is no breadcrumb
// collection to filter, that the surface is an id rather than a URL, and that the reporter refuses a
// target the allowlist does not carry. All of that is a property of the code and is checkable without
// a browser.
//
// The REGIME is not. Whether an unnonced script executes, whether a declined grant really produces zero
// requests, and whether the transmitted body is what this file says it is are properties of a running
// browser under a real header — and they are asserted in `scripts/accept.mjs`, which drives a real
// Chrome, intercepts the reporting origin so nothing leaves the machine, and reads the request body off
// the wire. Splitting them this way is deliberate: `npm test` must not need a Chrome install, and a
// claim about a browser must not be made without one.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import {
  ALLOWLIST,
  BROWSER_ERROR_CODES,
  buildEvent,
  classify,
  encodeEnvelope,
  parseDsn,
  parseFrames,
} from "../../design-system/error-report.ts";
import { ALLOWED_ORIGINS, SURFACES } from "../../design-system/third-party-policy.ts";
import { resolveSurface, surfaceRoutes } from "../../design-system/surface-map.ts";
import { buildContentSecurityPolicy } from "../../design-system/csp.ts";

const ROOT = new URL("..", import.meta.url).pathname;
const WEB = new URL("../..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const stripComments = (source) => source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
const readWeb = (rel) => readFile(join(WEB, rel), "utf8");

const CONFIG = {
  dsn: `${ALLOWED_ORIGINS[0].origin.replace("://", "://fixturekey@")}/4242`,
  release: "v0.24.0",
  edition: "hosted",
  surface: "tenant.studio",
  traceId: "9f2c1ab47e6d40518c33a7b1e0d4f6a2",
  granted: true,
};

// ── 3.1 · Installed on all three surfaces, under the nonce regime ────────────

test("3.1 both consoles install the reporter in their ROOT layout", async () => {
  // The root layout, not a page: the failures this is for — a chunk that did not load, a hydration
  // mismatch, a script the policy refused — happen before any page-level component mounts.
  for (const [rel, label] of [
    ["console/src/app/layout.tsx", "the customer console (public surface AND tenant prefix)"],
    ["admin-console/src/app/layout.tsx", "the operator console"],
  ]) {
    const src = await readWeb(rel);
    assert.match(src, /<ErrorReporting \{\.\.\.reporting\} \/>/, `${label} does not install the reporter`);
    assert.match(src, /reportingConfig\(\)/, `${label} does not build a reporting configuration`);
  }
});

test("3.1 no inline script and no third-party script host is rendered by either console", async () => {
  // The reporter is FIRST-PARTY bundled code reached through `'strict-dynamic'` from the nonced
  // bootstrap. Two things follow, and both are asserted rather than argued: nothing is injected inline
  // (which would need `dangerouslySetInnerHTML`, itself already fenced), and `script-src` gains no host.
  for (const rel of ["console/src/app/layout.tsx", "admin-console/src/app/layout.tsx",
    "console/src/components/errorReporting.tsx", "admin-console/src/components/errorReporting.tsx"]) {
    // Comments stripped first: these files DOCUMENT why an inline script was refused, and naming the
    // refused construct is the explanation. A fence that punished the explanation would make the
    // explanation the thing people delete.
    const src = stripComments(await readWeb(rel));
    assert.doesNotMatch(src, /dangerouslySetInnerHTML/, `${rel} injects raw markup`);
    assert.doesNotMatch(src, /<script/, `${rel} renders a script tag`);
  }
  for (const consoleId of ["customer", "operator"]) {
    for (const pathname of ["/", "/app/studio", "/api/health", "/tenants"]) {
      const csp = buildContentSecurityPolicy({
        consoleId,
        pathname,
        nonce: "N",
        dev: false,
        granted: ["essential", "product_analytics", "session_replay", "error_diagnostics"],
      });
      const scriptSrc = /script-src ([^;]+)/.exec(csp)?.[1] ?? "";
      assert.doesNotMatch(scriptSrc, /https?:\/\//, `${consoleId}${pathname} script-src names a host`);
      assert.doesNotMatch(scriptSrc, /'unsafe-inline'/, `${consoleId}${pathname} allows inline script`);
    }
  }
});

test("3.1 both consoles' reporter wrappers are the same eight lines around the shared installer", async () => {
  // The wrapper is duplicated because `web/design-system/` cannot resolve `react`. Everything that
  // decides WHAT is transmitted is shared; this asserts the duplication stayed trivial.
  const bodies = [];
  for (const rel of ["console/src/components/errorReporting.tsx", "admin-console/src/components/errorReporting.tsx"]) {
    const src = await readWeb(rel);
    assert.match(src, /installErrorReporting/, `${rel} does not call the shared installer`);
    assert.match(src, /design-system\/error-report\.ts/, `${rel} does not import the shared module`);
    assert.match(src, /return null;/, `${rel} renders something — the reporter must render nothing`);
    bodies.push(src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "").trim());
  }
  assert.equal(bodies[0], bodies[1], "the two reporter wrappers have drifted apart");
});

// ── 3.2 · Breadcrumbs are ABSENT, not filtered ───────────────────────────────

test("3.2 there is no breadcrumb collection anywhere in the reporter", async () => {
  const src = await readWeb("design-system/error-report.ts");
  const code = src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  for (const shape of ["breadcrumb", "addBreadcrumb", "console.log", "history.pushState",
    "XMLHttpRequest", "PerformanceObserver", "click"]) {
    assert.doesNotMatch(
      code,
      new RegExp(shape.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "i"),
      `the reporter references ${shape} — a breadcrumb collection is ABSENT here, never filtered, and ` +
        `the only way to be sure a collection is absent is for no code to collect it`,
    );
  }
});

test("3.2 no breadcrumb array reaches the wire", () => {
  const event = buildEvent(CONFIG, "error", new Error("boom"));
  const envelope = encodeEnvelope(event, "e".repeat(32), "2026-08-01T00:00:00.000Z");
  assert.doesNotMatch(envelope, /breadcrumb/i, "a breadcrumb collection reached the wire");
  for (const absent of ["request", "user", "contexts", "modules", "extra", "server_name", "sdk"]) {
    assert.doesNotMatch(
      envelope,
      new RegExp(`"${absent}"`),
      `the envelope carries a ${absent} block — it is not written, so it must not appear`,
    );
  }
});

// ── 3.3 · Constructed from the allowlist ─────────────────────────────────────

test("3.3 the browser allowlist is the SAME thirteen keys as the Go side", async () => {
  const go = await readFile(join(WEB, "..", "internal", "erroreport", "allowlist.go"), "utf8");
  const goKeys = [...go.matchAll(/\{"([a-z._]+)", "(classification|location|correlation|provenance)"/g)].map((m) => m[1]);
  assert.ok(goKeys.length >= 13, `parsed only ${goKeys.length} Go allowlist keys — the parse is wrong`);
  assert.deepEqual(
    [...ALLOWLIST].sort(),
    goKeys.sort(),
    "the browser and server allowlists have drifted — one review must answer for both",
  );
});

test("3.3 the message body is dropped and the code carries the classification", () => {
  const event = buildEvent(CONFIG, "error", new Error("failed to resolve prompt \"customer contract Q3\""));
  const envelope = encodeEnvelope(event, "e".repeat(32), "2026-08-01T00:00:00.000Z");
  assert.doesNotMatch(envelope, /customer contract/, "the message body reached the wire");
  assert.doesNotMatch(envelope, /failed to resolve/, "the message body reached the wire");
  assert.match(envelope, /BROWSER_UNHANDLED_ERROR/, "the classification is absent — nothing is reportable");
  // The exception's `value` is the ONLY message-shaped field, and it is a member of the closed enum.
  const payload = JSON.parse(envelope.split("\n")[2]);
  assert.ok(
    BROWSER_ERROR_CODES.includes(payload.exception.values[0].value),
    `the exception value is ${payload.exception.values[0].value}, which is not a member of the enum`,
  );
});

test("3.3 the surface is an id from the closed enum and NEVER a URL", () => {
  const event = buildEvent({ ...CONFIG, surface: "/app/variants/var-7f31c9/scorecard" }, "error", new Error("x"));
  assert.equal(event.surface, "unknown", "a path-shaped surface was passed through");
  assert.equal(buildEvent(CONFIG, "error", new Error("x")).surface, "tenant.studio");

  // And the resolver never produces one either, for any path.
  for (const consoleId of ["customer", "operator"]) {
    for (const path of ["/app/variants/var-7f31c9/scorecard?tab=2", "/app/runs/run-9/live",
      "/tenants/tenant-hermes", "/nothing/anybody/classified"]) {
      const surface = resolveSurface(consoleId, path);
      assert.ok(SURFACES.includes(surface), `${consoleId}${path} resolved to ${surface}, not a member of the enum`);
      assert.doesNotMatch(surface, /\//, `${consoleId}${path} resolved to something path-shaped`);
    }
  }
});

test("3.3 every surface the map can produce is a member of the closed enum", () => {
  for (const consoleId of ["customer", "operator"]) {
    const routes = surfaceRoutes(consoleId);
    assert.ok(routes.length > 5, `${consoleId} has only ${routes.length} routes — the table looks truncated`);
    assert.equal(routes.at(-1).prefix, "/", `${consoleId}'s route table has no catch-all`);
    for (const route of routes) {
      assert.ok(SURFACES.includes(route.surface), `${route.prefix} maps to ${route.surface}, not in the enum`);
    }
  }
});

test("3.3 a frame carries a pathname, never a full URL or a query string", () => {
  const frames = parseFrames(
    "Error: x\n" +
      "    at doThing (https://console.example/_next/static/chunks/app/app/studio/page-abc.js?tenant=acme:12:34)\n" +
      "    at https://console.example/_next/static/chunks/main-app.js:5:6",
  );
  assert.ok(frames.length >= 1, "no frame was parsed — the assertion would be vacuous");
  for (const frame of frames) {
    assert.doesNotMatch(frame.file, /https?:/, "a frame carries a full URL");
    assert.doesNotMatch(frame.file + frame.package, /\?/, "a frame carries a query string");
  }
});

// ── 3.4 · The reporting origin, and only that ────────────────────────────────

test("3.4 the reporting origin is on connect-src for EVERY prefix when granted", () => {
  const reporting = ALLOWED_ORIGINS.find((o) => o.category === "error_diagnostics");
  assert.ok(reporting, "no error-reporting origin is on the allowlist");
  assert.equal(reporting.directive, "connect-src");
  assert.deepEqual([...reporting.surfaces].sort(), ["operator", "public", "tenant"]);

  for (const [consoleId, pathname] of [["customer", "/"], ["customer", "/app/studio"],
    ["customer", "/api/console/x"], ["operator", "/tenants"], ["operator", "/api/overview"]]) {
    const csp = buildContentSecurityPolicy({
      consoleId, pathname, nonce: "N", dev: false,
      granted: ["essential", "error_diagnostics"],
    });
    const connect = /connect-src ([^;]+)/.exec(csp)?.[1] ?? "";
    assert.ok(
      connect.includes(reporting.origin),
      `${consoleId}${pathname} does not name the reporting origin under connect-src: ${connect}`,
    );
    // And it is the ONLY third-party origin there.
    const named = [...csp.matchAll(/https?:\/\/[^\s;]+/g)].map((m) => m[0]);
    assert.deepEqual(named, [reporting.origin], `${consoleId}${pathname} names more than the reporting origin`);
  }
});

test("3.4 without the grant, no prefix names it at all", () => {
  for (const [consoleId, pathname] of [["customer", "/"], ["customer", "/app"], ["operator", "/"]]) {
    const csp = buildContentSecurityPolicy({ consoleId, pathname, nonce: "N", dev: false });
    assert.doesNotMatch(csp, /https?:\/\//, `${consoleId}${pathname} names an origin with nothing granted`);
  }
});

test("3.4 the reporter REFUSES a DSN whose origin the allowlist does not carry", () => {
  // The load-bearing half of "the policy tells the truth about where data goes". A deployment that
  // could point the DSN elsewhere would make the header and the destination disagree, and the header
  // would be the one that is wrong.
  assert.equal(parseDsn("https://key@evil.example/1"), null, "a non-allowlisted origin was accepted");
  assert.equal(parseDsn("not a url"), null);
  assert.equal(parseDsn(`${ALLOWED_ORIGINS[0].origin.replace("://", "://key@")}/`), null, "a DSN with no project id was accepted");
  const ok = parseDsn(CONFIG.dsn);
  assert.ok(ok, "the allowlisted origin was refused");
  assert.equal(ok.endpoint, `${ALLOWED_ORIGINS[0].origin}/api/4242/envelope/`);
});

// ── 3.5 · Four failure classes ───────────────────────────────────────────────

test("3.5 chunk-load, hydration, unhandled error and unhandled rejection each classify distinctly", () => {
  assert.equal(classify("error", "Loading chunk 42 failed"), "BROWSER_CHUNK_LOAD_FAILED");
  assert.equal(classify("error", "Failed to fetch dynamically imported module: /x.js"), "BROWSER_CHUNK_LOAD_FAILED");
  assert.equal(classify("error", "Hydration failed because the server rendered HTML didn't match"), "BROWSER_HYDRATION_FAILED");
  assert.equal(classify("error", "x is not a function"), "BROWSER_UNHANDLED_ERROR");
  assert.equal(classify("rejection", "x is not a function"), "BROWSER_UNHANDLED_REJECTION");
  // Four distinct values, or the classification is decoration.
  assert.equal(new Set(BROWSER_ERROR_CODES).size, BROWSER_ERROR_CODES.length);
});

test("3.5 both consoles report a failure their error boundary caught", async () => {
  for (const rel of ["console/src/app/error.tsx", "admin-console/src/app/error.tsx"]) {
    const src = await readWeb(rel);
    assert.match(src, /reportHandledFailure\(error\)/, `${rel} renders a fallback without reporting`);
    assert.match(src, /useEffect\(/, `${rel} reports during render — a boundary can re-render`);
  }

  /*
   * 🔴 The CUSTOMER-facing boundary does not render the error's own message; the OPERATOR one does, and
   * that difference is deliberate rather than an oversight in one of them.
   *
   * P24's message drop is about what crosses a boundary to a THIRD PARTY. It is not a rule about what a
   * person may see on their own screen, and conflating the two would have cost the operator console a
   * diagnostic it has shipped since P8 — "UI改版不得丢失既有功能" — for no security gain: an operator is
   * already inside the trust boundary and is looking at the platform's own failure.
   *
   * The customer boundary is a different reader. A tenant seeing a raw internal error string is being
   * shown platform internals they did not ask for and cannot act on, one screen from the same
   * disclosure this phase drops from the wire.
   */
  const customer = await readWeb("console/src/app/error.tsx");
  assert.doesNotMatch(customer, /\{error\.message\}/, "the customer boundary renders a raw internal error string");
  const operator = await readWeb("admin-console/src/app/error.tsx");
  assert.match(operator, /\{error\.message\}/,
    "the operator boundary stopped showing the platform's error — that diagnostic predates P24 and is " +
      "not a boundary concern; if it was removed deliberately, this assertion is what says so");
});

// ── 3.6 · Source maps ────────────────────────────────────────────────────────

test("3.6 browser source maps are off EXPLICITLY in both consoles", async () => {
  for (const rel of ["console/next.config.mjs", "admin-console/next.config.mjs"]) {
    const src = await readWeb(rel);
    assert.match(
      src,
      /productionBrowserSourceMaps:\s*false/,
      `${rel} leaves source maps off by omission — "we did not turn them on" and "they are off" read ` +
        `the same in a green build and differ completely in a review`,
    );
  }
});

test("3.6 the shipped tree carries no source map and no pointer to one", async () => {
  const scan = await read("scripts/scan-bundle.mjs");
  assert.match(scan, /SOURCE MAP/, "the bundle scan does not check for source maps");
  assert.match(scan, /sourceMappingURL/, "the bundle scan does not check for a map POINTER");
  // The walk must SEE `.map` files, or the rule is unreachable.
  assert.match(scan, /endsWith\("\.map"\)/, "the bundle scan's walk cannot see a .map file");
});

test("3.6 the upload step refuses without a release-scoped token and never leaves a map behind", async () => {
  const src = await read("scripts/upload-sourcemaps.mjs");
  assert.match(src, /HEROS_SOURCEMAP_UPLOAD_TOKEN/, "the upload step names no token");
  assert.match(src, /removeAll\(maps\)/, "the upload step does not remove the maps");
  // Removal on EVERY path, including the no-token one: a build that produced maps and has no token is
  // a build that would otherwise ship them.
  assert.ok(
    (src.match(/await removeAll\(maps\)/g) ?? []).length >= 3,
    "the maps are not removed on every path",
  );
  assert.match(src, /NEVER EXECUTED/, "the script does not state that the upload half is unproven");
});
