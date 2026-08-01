// third-party-fence.test.mjs is the operator console's half of the P24 wave-24a fence.
//
// The operator console's position in this phase is the narrowest of the three surfaces, and it is
// worth stating rather than leaving to be inferred from what is absent: it takes the error reporter
// and refuses BOTH the analytics tag and the session recorder, on every route, structurally.
//
// The argument is not squeamishness. This console's screen renders cross-tenant aggregates, tenant
// names, active impersonation state and audit rows. A session recording of it is a copy of the exact
// material the platform's egress allowlist exists to keep inside a boundary, held by a party we do not
// control, and no configuration option changes what a recording contains. An analytics tag is the same
// argument one step down: it would put an operator's navigation — which tenant, which incident, when —
// into a third party's logs.
//
// Every assertion below therefore names the OPERATOR class specifically. Before P24 this console's
// only protection was a global "the CSP contains no https origin" check, which was true and would have
// stayed true right up until the day the customer console's public surface legitimately gained one.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { promisify } from "node:util";
import { startAdminConsole } from "./support/server.mjs";
import { buildContentSecurityPolicy, resolveSurfaceClass } from "../../design-system/csp.ts";
import {
  ALLOWED_ORIGINS,
  CONSOLE_PREFIXES,
  SURFACE_CLASS_RULES,
} from "../../design-system/third-party-policy.ts";

/**
 * originOf reduces a CSP source expression to its origin.
 *
 * Written as a parse rather than as `replace(/\/.*$/, "")`, which was the first version and was
 * WRONG in the direction that matters: it cut at the first slash, turning
 * `https://ingest.example` into `https:` — so a legitimately allowlisted origin was reported as
 * unlisted. An assertion that fails on correct input gets loosened, and the loosening is what
 * actually costs the guarantee.
 */
function originOf(source) {
  try {
    return new URL(source).origin;
  } catch {
    return source;
  }
}

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const WEB = new URL("../..", import.meta.url).pathname;

/** The header this console served before P24, with the nonce as a hole. Pinned, not derived. */
const SHIPPED_BEFORE_P24 = (nonce, dev) =>
  [
    `default-src 'self'`,
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${dev ? ` 'unsafe-eval'` : ""}`,
    `style-src 'self' 'unsafe-inline'`,
    `img-src 'self' data:`,
    `connect-src 'self'`,
    `font-src 'self'`,
    `object-src 'none'`,
    `base-uri 'self'`,
    `form-action 'self'`,
    `frame-ancestors 'none'`,
  ].join("; ");

test("1.2 the constructed header is byte-identical to the one this console shipped", () => {
  for (const pathname of ["/", "/tenants", "/tenants/t-1", "/killswitch", "/audit", "/api/overview", "/signin"]) {
    for (const dev of [false, true]) {
      assert.equal(
        buildContentSecurityPolicy({ consoleId: "operator", pathname, nonce: "NONCE", dev }),
        SHIPPED_BEFORE_P24("NONCE", dev),
        `the constructed header for ${pathname} (dev=${dev}) is not the header this console shipped`,
      );
    }
  }
});

test("1.4 every route of this console resolves to the operator class", () => {
  // Not "most routes". The catch-all is `operator`, so a page added tomorrow is governed before
  // anybody remembers to classify it — which is the failure mode a per-page judgement always has.
  assert.equal(CONSOLE_PREFIXES.operator.at(-1).surface, "operator");
  for (const pathname of ["/", "/tenants", "/audit", "/api/overview", "/a-route-nobody-has-written-yet"]) {
    assert.equal(resolveSurfaceClass("operator", pathname), "operator", `${pathname} is not an operator route`);
  }
});

test("1.4 the operator class cannot express an analytics or session-replay origin", () => {
  const rule = SURFACE_CLASS_RULES.operator;
  assert.equal(rule.thirdPartyOrigins, "none");
  assert.ok(!rule.categories.includes("product_analytics"), "the operator class permits analytics");
  assert.ok(!rule.categories.includes("session_replay"), "the operator class permits session replay");
  // The refusal is a property of the CLASS, not of the current allowlist being empty. An analytics
  // origin added to the allowlist tomorrow still cannot appear here, and that is the difference
  // between a structural refusal and an empty table.
  const hypothetical = {
    origin: "https://analytics.example",
    integration: "hypothetical",
    category: "product_analytics",
    directive: "connect-src",
    budgetBytes: 1,
    surfaces: ["public", "tenant", "operator"],
    why: "a hypothetical entry used only to prove the class refuses it",
  };
  assert.ok(
    !rule.categories.includes(hypothetical.category),
    "an analytics entry claiming the operator surface would be admitted — the refusal is not structural",
  );
});

test("1.3 the origin scan covers this console's middleware and configuration", async () => {
  const { stdout } = await exec("node", [join(WEB, "design-system", "scan-origins.mjs")], { cwd: ROOT });
  assert.match(stdout, /origin scan passed/);
  const src = (await readFile(join(ROOT, "src", "middleware.ts"), "utf8"))
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");
  assert.doesNotMatch(src, /https?:\/\//, "this console's middleware names an origin of its own");
  assert.match(src, /buildContentSecurityPolicy/, "this console does not construct its header from the artefact");
});

test("1.3 🔴 the origin scan goes RED on an origin written into this console's Next configuration", async () => {
  const dir = join(tmpdir(), `p24-admin-origin-probe-${process.pid}`);
  await mkdir(dir, { recursive: true });
  const probe = join(dir, "next.config.mjs");
  const real = await readFile(join(ROOT, "next.config.mjs"), "utf8");
  await writeFile(probe, real.replace("const nextConfig = {", 'const ingest = "https://www.clarity.ms";\nconst nextConfig = {'));
  try {
    await assert.rejects(
      () => exec("node", [join(WEB, "design-system", "scan-origins.mjs"), probe], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1);
        assert.match(error.stderr, /clarity\.ms/);
        assert.match(error.stderr, /next\.config\.mjs:\d+/);
        return true;
      },
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("1.7 the drift check runs from THIS side too, and goes red on a divergent map", async () => {
  // Run from both consoles on purpose. A drift check that only one side runs is a check that stops
  // running the day that side's suite is skipped — and the whole point is that neither console is the
  // authority on a rule they share.
  const { driftFindings } = await import("../../design-system/drift.mjs");
  const { SHARED_PREFIXES } = await import("../../design-system/third-party-policy.ts");
  assert.deepEqual(
    driftFindings({ prefixes: CONSOLE_PREFIXES, rules: SURFACE_CLASS_RULES, sharedPrefixes: SHARED_PREFIXES }),
    [],
  );
  const divergent = {
    ...CONSOLE_PREFIXES,
    operator: [{ prefix: "/api", surface: "public" }, { prefix: "/", surface: "operator" }],
  };
  const findings = driftFindings({
    prefixes: divergent,
    rules: SURFACE_CLASS_RULES,
    sharedPrefixes: SHARED_PREFIXES,
  });
  assert.ok(findings.length > 0, "a divergent map produced no finding on the operator side");
  assert.match(findings.join("\n"), /\/api/);
});

/** fixtureBuild writes a minimal `.next` whose route manifest is an operator console's: all guarded. */
async function fixtureBuild(needle) {
  const dir = join(tmpdir(), `p24-admin-bundle-probe-${process.pid}`);
  await mkdir(join(dir, ".next", "static", "chunks"), { recursive: true });
  await writeFile(
    join(dir, ".next", "app-build-manifest.json"),
    JSON.stringify({ pages: { "/tenants/page": ["static/chunks/operator.js"] } }),
  );
  await writeFile(join(dir, ".next", "build-manifest.json"), JSON.stringify({ rootMainFiles: [] }));
  await writeFile(join(dir, ".next", "static", "chunks", "operator.js"), `var x = "${needle}";\n`);
  return dir;
}

test("1.5 🔴 a session-replay runtime in ANY chunk of this console fails the build", async () => {
  const dir = await fixtureBuild("https://www.clarity.ms/tag/");
  try {
    await assert.rejects(
      () => exec("node", [join(ROOT, "scripts", "scan-bundle.mjs"), "--root", dir], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1);
        assert.match(error.stderr, /OBSERVABILITY RUNTIME/);
        assert.match(error.stderr, /Microsoft Clarity/);
        assert.match(error.stderr, /static\/chunks\/operator\.js/);
        assert.match(error.stderr, /every route of this console is an operator route/i);
        return true;
      },
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("1.5 the real build reports a non-empty guarded partition", async () => {
  const { stdout } = await exec("node", [join(ROOT, "scripts", "scan-bundle.mjs")], { cwd: ROOT });
  const match = /(\d+) chunk\(s\) reachable from an operator route/.exec(stdout);
  assert.ok(match, "the bundle scan did not report a guarded partition");
  assert.ok(Number(match[1]) > 0, "no chunk is guarded — the rule checks nothing");
});

// ── The live half: a real response header from a real operator route ─────────

let console_;

before(async () => {
  console_ = await startAdminConsole();
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
});

test("1.4 🔴 a real operator response names no analytics and no session-replay origin", async () => {
  // Unauthenticated routes are used deliberately: middleware sets the policy before any page code
  // runs, so this reads the same header an operator gets, without minting an operator session inside a
  // test — a test that can obtain an operator session is a second credential path onto the
  // highest-blast-radius surface, which `visual-baseline.mjs` already refuses to be.
  for (const path of ["/signin", "/api/health", "/"]) {
    const res = await fetch(`${console_.base}${path}`, { redirect: "manual" });
    const csp = res.headers.get("content-security-policy") ?? "";
    assert.match(csp, /default-src 'self'/, `${path} lost default-src 'self'`);
    assert.doesNotMatch(csp, /clarity/i, `${path} names a session-replay origin`);
    assert.doesNotMatch(csp, /googletagmanager|google-analytics/i, `${path} names an analytics origin`);
    const permitted = new Set(ALLOWED_ORIGINS.map((o) => o.origin));
    for (const origin of [...csp.matchAll(/https?:\/\/[^\s;]+/g)].map((m) => originOf(m[0]))) {
      assert.ok(permitted.has(origin), `${path} names ${origin}, which the allowlist does not carry`);
    }
    const scriptSrc = /script-src ([^;]+)/.exec(csp)?.[1] ?? "";
    assert.doesNotMatch(scriptSrc, /https?:\/\//, `${path}'s script-src names a host`);
    assert.doesNotMatch(scriptSrc, /'unsafe-eval'/, `${path}'s shipped script-src allows eval`);
    assert.match(scriptSrc, /'strict-dynamic'/);
  }
});

// ── The regression guard for the failure that got here ──────────────────────

test("🔴 the CI job that runs this suite is on a Node that can load the shared artefact", async () => {
  /*
   * This test exists because CI went red and the local run did not.
   *
   * Both consoles' middleware constructs its Content-Security-Policy from ONE checked-in TypeScript
   * artefact, and these tests import that artefact DIRECTLY so they assert against the shipped policy
   * rather than against a copy of it. Node cannot load a `.ts` file without type stripping — 22.6
   * behind a flag, 22.18 by default — so on Node 20 the whole suite dies with
   * ERR_UNKNOWN_FILE_EXTENSION before a single assertion runs.
   *
   * The failure mode is the dangerous one: not a wrong answer, but NO answer, reported as a test-file
   * error that reads like a broken import. Every fence in this file is silent at once.
   *
   * So the workflow's pinned version is checked against the floor `package.json` declares. Setting
   * `node-version` back is now a red build rather than a mystery in the CI log.
   */
  const { readFile } = await import("node:fs/promises");
  const workflow = await readFile(join(ROOT, "..", "..", ".github", "workflows", "ci.yml"), "utf8");
  const pkg = JSON.parse(await readFile(join(ROOT, "package.json"), "utf8"));

  const floor = Number(/>=\s*(\d+)/.exec(pkg.engines?.node ?? "")?.[1]);
  assert.ok(floor >= 22, `package.json declares no Node floor that can strip types (${pkg.engines?.node})`);

  // The job that runs THIS suite, found by its working-directory rather than by its position.
  const job = workflow.slice(workflow.indexOf("  operator-console:"));
  const pinned = Number(/node-version:\s*"?(\d+)/.exec(job)?.[1]);
  assert.ok(Number.isFinite(pinned), "the operator-console job pins no Node version");
  assert.ok(
    pinned >= floor,
    `CI runs this suite on Node ${pinned}, below the ${floor} floor package.json declares. Node cannot ` +
      `load web/design-system/*.ts below 22.18, so every assertion in this file would be skipped — ` +
      `silently, as a file-level error rather than as a failure.`,
  );

  // And the runtime actually executing right now, so a developer sees the same sentence CI would.
  const running = Number(process.versions.node.split(".")[0]);
  assert.ok(
    running >= floor,
    `this run is on Node ${process.versions.node}, below the ${floor} floor. Upgrade rather than ` +
      `reading the ERR_UNKNOWN_FILE_EXTENSION that follows.`,
  );
});
