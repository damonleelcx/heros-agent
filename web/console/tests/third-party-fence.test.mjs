// third-party-fence.test.mjs holds the P24 wave-24a fence — the assertions that had to exist BEFORE
// any third-party tool was installed.
//
// # Why the order matters enough to have its own file
//
// This phase installs three third-party products into a system that committed, in code and in shipped
// specs, to running none. If the tools go in first, every fence ends up shaped around the tool that is
// already there: the assertion is written to pass, discovers nothing, and the phase ships a guarantee
// that was retro-fitted to the thing it was supposed to bound. So every fence below was written and
// demonstrated red against an EMPTY allowlist, and the tools arrive in later waves through the door
// these assertions guard.
//
// # The four properties
//
//   1. The header is BYTE-IDENTICAL to the one shipped before P24 (task 1.2). A refactor of a security
//      header is only safe if somebody diffed the output, and "somebody diffed it" is not a property.
//   2. The rule is PER PREFIX (task 1.4). The two assertions this replaces were global
//      `doesNotMatch(/https?:\/\//)` checks — true today, and silently false the moment the public
//      surface gains one legitimate origin, taking the tenant guarantee with it.
//   3. An origin cannot enter through middleware (task 1.3).
//   4. An analytics or replay runtime cannot reach a tenant chunk (task 1.5).
//
// Each is demonstrated RED here, by a deliberate violation, because a fence nobody has seen fail is a
// fence nobody knows is connected.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { promisify } from "node:util";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";
import { buildContentSecurityPolicy, resolveSurfaceClass } from "../../design-system/csp.ts";
import {
  ALLOWED_ORIGINS,
  CONSOLE_PREFIXES,
  SHARED_PREFIXES,
  SURFACE_CLASS_RULES,
  SURFACES,
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
const read = (rel) => readFile(join(ROOT, rel), "utf8");

/**
 * SHIPPED_BEFORE_P24 is the header both consoles served before this phase, with the nonce as a hole.
 *
 * It is pinned as a LITERAL rather than derived, and that is the entire point: deriving the expected
 * value from the code under test proves the code equals itself. This string was copied from the two
 * `middleware.ts` files at the commit before P24, and it is what task 1.2 means by byte-identical.
 */
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

// ── 1.2 · The construction reproduces the literal it replaced ────────────────

test("1.2 the constructed header is byte-identical to the shipped one, on every prefix", () => {
  const probes = ["/", "/install", "/docs", "/signin", "/app", "/app/studio", "/api/health", "/api/console/runs/r"];
  for (const pathname of probes) {
    for (const dev of [false, true]) {
      const built = buildContentSecurityPolicy({ consoleId: "customer", pathname, nonce: "NONCE", dev });
      assert.equal(
        built,
        SHIPPED_BEFORE_P24("NONCE", dev),
        `the constructed header for ${pathname} (dev=${dev}) is not the header this console shipped`,
      );
    }
  }
});

test("1.2 every prefix resolves to a class, and the catch-all is last", () => {
  // A route with no policy must not be a route with no restrictions. The resolver throws rather than
  // returning a permissive default, and this is the assertion that keeps the catch-all in place.
  for (const consoleId of ["customer", "operator"]) {
    const prefixes = CONSOLE_PREFIXES[consoleId];
    assert.equal(prefixes.at(-1).prefix, "/", `${consoleId}'s prefix list does not end in a catch-all`);
    const specific = prefixes.slice(0, -1);
    for (const policy of specific) {
      assert.notEqual(policy.prefix, "/", `${consoleId} lists the catch-all before a specific prefix`);
    }
  }
  assert.equal(resolveSurfaceClass("customer", "/app/anything/at/all"), "tenant");
  assert.equal(resolveSurfaceClass("customer", "/api/console/x"), "tenant");
  assert.equal(resolveSurfaceClass("customer", "/whatever-nobody-classified"), "public");
  assert.equal(resolveSurfaceClass("operator", "/api/overview"), "operator");
  assert.equal(resolveSurfaceClass("operator", "/tenants/t-1"), "operator");
});

// ── 1.3 · An origin cannot enter through middleware ──────────────────────────

test("1.3 the origin scan passes on the shipped tree", async () => {
  const { stdout } = await exec("node", [join(WEB, "design-system", "scan-origins.mjs")], { cwd: ROOT });
  assert.match(stdout, /origin scan passed/);
  assert.match(stdout, /4 governed file/);
});

test("1.3 🔴 the origin scan goes RED on an origin written into a middleware, naming file and line", async () => {
  // Against a COPY. Injecting into the real middleware means a crashed run can leave a security header
  // modified on disk, which is a worse outcome than an unproven fence.
  const dir = join(tmpdir(), `p24-origin-probe-${process.pid}`);
  await mkdir(dir, { recursive: true });
  const probe = join(dir, "middleware.ts");
  const real = await read("src/middleware.ts");
  await writeFile(probe, real.replace("const DEV =", 'const ingest = "https://www.googletagmanager.com";\nconst DEV ='));
  try {
    await assert.rejects(
      () => exec("node", [join(WEB, "design-system", "scan-origins.mjs"), probe], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1, "the origin scan did not fail on a hard-coded origin");
        assert.match(error.stderr, /origin scan FAILED/);
        assert.match(error.stderr, /googletagmanager\.com/, "the failure does not name the literal");
        assert.match(error.stderr, /middleware\.ts:\d+/, "the failure does not name the file and line");
        assert.match(error.stderr, /third-party-policy\.ts/, "the failure does not name the way in");
        return true;
      },
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }

  // And prose is not code: a URL in a comment must not trip it, or the gate gets loosened until it
  // stops catching the real thing.
  const { stdout } = await exec("node", [join(WEB, "design-system", "scan-origins.mjs"), "--self-test"], { cwd: ROOT });
  assert.match(stdout, /self-test passed/);
});

// ── 1.5 · A recorder cannot reach a tenant chunk ─────────────────────────────

/**
 * fixtureBuild writes a minimal `.next` whose route manifest puts `needle` in one partition.
 *
 * The fence is about REACHABILITY, which is a property of the route manifest rather than of a file
 * name, so the fixture is a manifest — the same input the real scan reads from a real build.
 */
async function fixtureBuild(partition, needle) {
  const dir = join(tmpdir(), `p24-bundle-probe-${process.pid}-${partition}`);
  await mkdir(join(dir, ".next", "static", "chunks"), { recursive: true });
  await writeFile(
    join(dir, ".next", "app-build-manifest.json"),
    JSON.stringify({
      pages: {
        "/app/studio/page": ["static/chunks/tenant.js"],
        "/(public)/page": ["static/chunks/public.js"],
      },
    }),
  );
  await writeFile(join(dir, ".next", "build-manifest.json"), JSON.stringify({ rootMainFiles: [] }));
  const carrier = partition === "tenant" ? "tenant.js" : "public.js";
  const other = partition === "tenant" ? "public.js" : "tenant.js";
  await writeFile(join(dir, ".next", "static", "chunks", carrier), `var x = "${needle}";\n`);
  await writeFile(join(dir, ".next", "static", "chunks", other), "var y = 1;\n");
  return dir;
}

test("1.5 🔴 a session-replay runtime in a TENANT chunk fails the build, naming runtime and chunk", async () => {
  const dir = await fixtureBuild("tenant", "https://www.clarity.ms/tag/");
  try {
    await assert.rejects(
      () => exec("node", [join(ROOT, "scripts", "scan-bundle.mjs"), "--root", dir], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1, "the bundle scan did not fail on a replay runtime in a tenant chunk");
        assert.match(error.stderr, /OBSERVABILITY RUNTIME/);
        assert.match(error.stderr, /Microsoft Clarity/, "the failure does not name the runtime");
        assert.match(error.stderr, /static\/chunks\/tenant\.js/, "the failure does not name the chunk");
        return true;
      },
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("1.5 the same runtime in a PUBLIC-ONLY chunk passes — the rule is reachability, not presence", async () => {
  const dir = await fixtureBuild("public", "https://www.clarity.ms/tag/");
  try {
    const { stdout } = await exec("node", [join(ROOT, "scripts", "scan-bundle.mjs"), "--root", dir], { cwd: ROOT });
    assert.match(stdout, /bundle scan passed/);
    // Both partitions must be non-empty in the message, or "no findings" is indistinguishable from
    // "looked at nothing" — the failure mode a passing fence hides best.
    assert.match(stdout, /1 chunk\(s\) reachable from a tenant route/);
    assert.match(stdout, /1 reachable only from the public surface/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("1.5 the analytics runtime is refused in a tenant chunk too, not only the recorder", async () => {
  const dir = await fixtureBuild("tenant", "https://www.googletagmanager.com/gtag/js");
  try {
    await assert.rejects(
      () => exec("node", [join(ROOT, "scripts", "scan-bundle.mjs"), "--root", dir], { cwd: ROOT }),
      (error) => {
        assert.match(error.stderr, /Google Analytics/);
        assert.match(error.stderr, /analytics/);
        return true;
      },
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("1.5 the real build's tenant partition is non-empty, so the rule is not vacuously satisfied", async () => {
  const { stdout } = await exec("node", [join(ROOT, "scripts", "scan-bundle.mjs")], { cwd: ROOT });
  const match = /(\d+) chunk\(s\) reachable from a tenant route, (\d+) reachable only from the public/.exec(stdout);
  assert.ok(match, "the bundle scan did not report a reachability partition");
  assert.ok(Number(match[1]) > 0, "no chunk is reachable from a tenant route — the fence checks nothing");
  assert.ok(Number(match[2]) > 0, "no chunk is public-only — the partition is not a partition");
});

// ── 1.4 · The per-prefix assertion, demonstrated red ─────────────────────────

test("1.4 🔴 the per-prefix assertion catches a third-party origin on a tenant header", () => {
  // The live tests below read a real header and find nothing, which is the correct result and also
  // exactly what a broken assertion produces. This feeds the same extraction a header that DOES name an
  // origin, so "no third-party origin on /app" is a measurement rather than a hope.
  const contaminated =
    "default-src 'self'; script-src 'self' 'nonce-N' 'strict-dynamic'; " +
    "connect-src 'self' https://www.clarity.ms; img-src 'self' data: https://www.google-analytics.com";
  const found = thirdPartyOrigins(contaminated);
  assert.deepEqual(found, ["https://www.clarity.ms", "https://www.google-analytics.com"]);
  assert.throws(() => assert.deepEqual(found, []), "the assertion the live tests use does not fail on a contaminated header");
});

test("1.4 🔴 an analytics or replay entry claiming the tenant surface is still refused by the class", async () => {
  // The refusal is structural, not "the table happens to be empty" — which matters more now that the
  // table is NOT empty. An analytics origin that names the tenant surface in its own `surfaces` list is
  // STILL absent from a tenant policy, because the tenant class does not permit that CATEGORY at all,
  // and a visitor granting the category changes nothing.
  const { originsFor } = await import("../../design-system/csp.ts");
  for (const category of ["product_analytics", "session_replay"]) {
    assert.ok(
      !SURFACE_CLASS_RULES.tenant.categories.includes(category),
      `the tenant class permits ${category} — the refusal is a preference, not a structure`,
    );
    assert.ok(
      !SURFACE_CLASS_RULES.operator.categories.includes(category),
      `the operator class permits ${category}`,
    );
  }
  // And the function that builds the header agrees, with every category granted: the ONLY origin a
  // tenant policy admits is the error-reporting one.
  const admitted = [...originsFor("tenant", ["product_analytics", "session_replay", "error_diagnostics"])];
  for (const origin of admitted) {
    assert.equal(
      origin.category,
      "error_diagnostics",
      `a tenant policy admitted ${origin.origin} (${origin.category}) with all categories granted`,
    );
    assert.equal(origin.directive, "connect-src", `${origin.origin} reaches a tenant prefix outside connect-src`);
  }
  // And nothing is admitted at all with no grant, which is the default state.
  assert.deepEqual([...originsFor("tenant", [])], [], "a tenant policy admitted an origin with nothing granted");
});

// ── 1.6 · The per-origin transfer budget ─────────────────────────────────────

/*
 * The browser half of this fence is demonstrated red INSIDE `scripts/accept.mjs`, on every run: it
 * drives the same code path over a fixture page that really loads a second origin and refuses to
 * report green unless that origin is detected and named. That is the only honest way to tell
 * "0 third-party bytes from 0 origins" — the expected result — apart from a measurement that saw
 * nothing, which prints the same line.
 *
 * What is demonstrated here is the DECISION. Three of its four rules cannot be reached by a fixture
 * page today, because an over-budget origin, a stale entry and an over-budget total each need a real
 * integration to exist first — which is the wrong order. The fence has to be red before the tool
 * arrives, or it ends up shaped around the tool.
 */
test("1.6 🔴 an unlisted origin fails acceptance, naming the origin and the bytes", async () => {
  const { evaluate } = await import("../scripts/accept.mjs");
  const { failures } = evaluate({
    bytesByOrigin: new Map([["https://console.test", 1000], ["https://tracker.example", 4096]]),
    urlsByOrigin: new Map([["https://tracker.example", new Set(["https://tracker.example/t.js"])]]),
    own: "https://console.test",
    allowlist: [],
    totalBudget: 300 * 1024,
  });
  assert.equal(failures.length, 1);
  assert.match(failures[0], /UNLISTED ORIGIN/);
  assert.match(failures[0], /tracker\.example/);
  assert.match(failures[0], /4096 bytes/);
});

test("1.6 🔴 an over-budget origin fails with the origin, the budget and the overage", async () => {
  const { evaluate } = await import("../scripts/accept.mjs");
  const entry = {
    origin: "https://tag.example",
    integration: "a-measured-integration",
    category: "product_analytics",
    directive: "connect-src",
    budgetBytes: 1000,
    surfaces: ["public"],
    contactedOn: "page-load",
    why: "a budgeted entry used to prove the ceiling is enforced",
  };
  const { failures } = evaluate({
    bytesByOrigin: new Map([["https://console.test", 10], ["https://tag.example", 1750]]),
    urlsByOrigin: new Map(),
    own: "https://console.test",
    allowlist: [entry],
    totalBudget: 300 * 1024,
  });
  assert.equal(failures.length, 1);
  assert.match(failures[0], /BUDGET/);
  assert.match(failures[0], /tag\.example/);
  assert.match(failures[0], /1000-byte budget/);
  assert.match(failures[0], /by 750/, "the failure does not name the overage");
});

test("1.6 🔴 one integration cannot absorb another's headroom", async () => {
  // The scenario the per-origin rule exists for: the TOTAL is unchanged, and the grown origin still
  // fails. A total-only budget would pass this and the decision would have been made by nobody.
  const { evaluate } = await import("../scripts/accept.mjs");
  const allowlist = [
    { origin: "https://a.example", integration: "A", category: "product_analytics", directive: "connect-src", budgetBytes: 1000, surfaces: ["public"], contactedOn: "page-load", why: "x".repeat(30) },
    { origin: "https://b.example", integration: "B", category: "session_replay", directive: "connect-src", budgetBytes: 1000, surfaces: ["public"], contactedOn: "page-load", why: "x".repeat(30) },
  ];
  const { failures, total } = evaluate({
    bytesByOrigin: new Map([["https://own.test", 1], ["https://a.example", 1800], ["https://b.example", 200]]),
    urlsByOrigin: new Map(),
    own: "https://own.test",
    allowlist,
    totalBudget: 300 * 1024,
  });
  assert.equal(total, 2000, "the total is unchanged from two origins at budget");
  assert.equal(failures.length, 1);
  assert.match(failures[0], /a\.example/);
  assert.match(failures[0], /another origin shrinking does not buy this one headroom/);
});

test("1.6 🔴 a stale allowlist entry fails — a permission nobody asked for", async () => {
  const { evaluate } = await import("../scripts/accept.mjs");
  const { failures } = evaluate({
    bytesByOrigin: new Map([["https://own.test", 1]]),
    urlsByOrigin: new Map(),
    own: "https://own.test",
    allowlist: [
      { origin: "https://gone.example", integration: "a retired integration", category: "product_analytics", directive: "connect-src", budgetBytes: 1000, surfaces: ["public"], contactedOn: "page-load", why: "x".repeat(30) },
    ],
    totalBudget: 300 * 1024,
  });
  assert.equal(failures.length, 1);
  assert.match(failures[0], /STALE ALLOWLIST ENTRY/);
  assert.match(failures[0], /gone\.example/);
});

test("1.6 🔴 the total ceiling fails even when every origin is inside its own budget", async () => {
  const { evaluate } = await import("../scripts/accept.mjs");
  const allowlist = ["a", "b", "c", "d"].map((n) => ({
    origin: `https://${n}.example`,
    integration: n,
    category: "product_analytics",
    directive: "connect-src",
    budgetBytes: 100 * 1024,
    surfaces: ["public"],
    contactedOn: "page-load",
    why: "x".repeat(30),
  }));
  const bytesByOrigin = new Map([["https://own.test", 1]]);
  for (const entry of allowlist) bytesByOrigin.set(entry.origin, 90 * 1024);
  const { failures } = evaluate({
    bytesByOrigin,
    urlsByOrigin: new Map(),
    own: "https://own.test",
    allowlist,
    totalBudget: 300 * 1024,
  });
  assert.equal(failures.length, 1, `expected only the total to fail, got: ${failures.join(" | ")}`);
  assert.match(failures[0], /third-party total is 368640 bytes/);
});

test("1.6 the acceptance run refuses to report green without proving its own measurement", async () => {
  // The property that keeps "0 bytes from 0 origins" meaningful. Asserted on the script's source
  // because the behaviour it describes — a self-check that must go red — is what the run does before
  // it prints anything, and running the whole browser gate inside `npm test` would make the unit
  // suite depend on a Chrome install.
  const src = await read("scripts/accept.mjs");
  assert.match(src, /proveTheMeasurementIsConnected/);
  assert.match(src, /if \(!\(await proveTheMeasurementIsConnected\(browser\)\)\)/);
  assert.match(src, /acceptance: no browser found/, "the gate skips instead of refusing when no browser exists");
  assert.doesNotMatch(src, /process\.exit\(0\)/, "the gate has a path that reports success without measuring");
});

// ── 1.7 · The two consoles cannot drift ──────────────────────────────────────

test("1.7 the two consoles derive the same policy for every shared prefix", () => {
  // The check is not a tautology even though the builder is shared. Each console passes its own id,
  // and its own prefix→class map lives in the artefact — a map that called the operator console's
  // `/api` public would produce a perfectly well-formed header naming an analytics origin on an
  // operator surface. That mapping error is what this compares.
  assert.ok(SHARED_PREFIXES.length > 0, "no shared prefix — the drift check compares nothing");
  for (const prefix of SHARED_PREFIXES) {
    const pathname = `${prefix}/drift-probe`;
    const customer = buildContentSecurityPolicy({ consoleId: "customer", pathname, nonce: "N", dev: false });
    const operator = buildContentSecurityPolicy({ consoleId: "operator", pathname, nonce: "N", dev: false });
    assert.equal(
      customer,
      operator,
      `the two consoles disagree about ${prefix}:\n  customer: ${customer}\n  operator: ${operator}`,
    );
    // And both must resolve it to a class that permits no analytics and no replay origin.
    for (const consoleId of ["customer", "operator"]) {
      const surface = resolveSurfaceClass(consoleId, pathname);
      const rule = SURFACE_CLASS_RULES[surface];
      assert.equal(rule.thirdPartyOrigins, "none", `${consoleId} treats ${prefix} as a third-party surface`);
      assert.ok(!rule.categories.includes("product_analytics"), `${consoleId} permits analytics on ${prefix}`);
      assert.ok(!rule.categories.includes("session_replay"), `${consoleId} permits session replay on ${prefix}`);
    }
  }
});

test("1.7 the drift check runs clean on the shipped artefact", async () => {
  const { driftFindings } = await import("../../design-system/drift.mjs");
  const findings = driftFindings({
    prefixes: CONSOLE_PREFIXES,
    rules: SURFACE_CLASS_RULES,
    sharedPrefixes: SHARED_PREFIXES,
  });
  assert.deepEqual(findings, [], `the two consoles' policies drifted:\n  ${findings.join("\n  ")}`);
});

test("1.7 🔴 the drift check goes RED on a divergent map, naming the rule and both values", async () => {
  const { driftFindings } = await import("../../design-system/drift.mjs");
  // The exact mistake the check exists for: a mapping that calls the operator console's `/api` public.
  // The header it produces is perfectly well-formed — correct directives, correct order, correct nonce
  // — and names an analytics origin on an operator surface. Nothing about it looks wrong.
  const divergent = {
    ...CONSOLE_PREFIXES,
    operator: [{ prefix: "/api", surface: "public" }, { prefix: "/", surface: "operator" }],
  };
  const findings = driftFindings({
    prefixes: divergent,
    rules: SURFACE_CLASS_RULES,
    sharedPrefixes: SHARED_PREFIXES,
  });
  assert.ok(findings.length > 0, "a divergent prefix map produced no finding — the drift check is not connected");
  const joined = findings.join("\n");
  assert.match(joined, /\/api/, "the finding does not name the rule");
  assert.match(joined, /customer/, "the finding does not name the first console");
  assert.match(joined, /operator/, "the finding does not name the second console");
  assert.match(joined, /"none"/, "the finding does not carry the first value");
  assert.match(joined, /"allowlisted"/, "the finding does not carry the second value");
});

test("1.7 🔴 the drift check refuses to pass vacuously", async () => {
  // Two ways this check could report success while comparing nothing, both of which have happened to
  // assertions in this repository: an empty collection, and a single element compared to itself.
  const { driftFindings } = await import("../../design-system/drift.mjs");
  assert.match(
    driftFindings({ prefixes: CONSOLE_PREFIXES, rules: SURFACE_CLASS_RULES, sharedPrefixes: [] })[0],
    /compares nothing/,
  );
  assert.match(
    driftFindings({
      prefixes: { customer: CONSOLE_PREFIXES.customer },
      rules: SURFACE_CLASS_RULES,
      sharedPrefixes: SHARED_PREFIXES,
    })[0],
    /fewer than two consoles/,
  );
});

test("1.7 neither middleware names an origin of its own", async () => {
  for (const rel of ["console/src/middleware.ts", "admin-console/src/middleware.ts"]) {
    const src = (await readFile(join(WEB, rel), "utf8"))
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/^\s*\/\/.*$/gm, "");
    assert.doesNotMatch(src, /https?:\/\//, `${rel} names an origin instead of reading the artefact`);
    assert.match(src, /buildContentSecurityPolicy/, `${rel} does not construct its header from the artefact`);
  }
});

// ── The artefact's own shape ─────────────────────────────────────────────────

test("the allowlist carries exactly what has been installed, and the classes that refuse say so", () => {
  // 🔴 This assertion used to be `deepEqual(ALLOWED_ORIGINS, [])`, and it was correct for wave 24a: the
  // fences were built and demonstrated red against an EMPTY table, because installing the tool first is
  // how a fence ends up shaped around the tool. Wave 24c installed one origin, so the assertion moved
  // from "nothing" to "exactly this, for exactly this reason" rather than being deleted — an allowlist
  // test that stops naming its contents is an allowlist test that would not notice a second entry.
  const byCategory = {};
  for (const origin of ALLOWED_ORIGINS) {
    byCategory[origin.category] = (byCategory[origin.category] ?? 0) + 1;
  }
  assert.deepEqual(
    byCategory,
    { error_diagnostics: 1, product_analytics: 2, session_replay: 3 },
    `the allowlist carries ${JSON.stringify(byCategory)}. Waves 24c and 24e installed one error-reporting ` +
      `origin, one analytics TAG host, and Clarity's tag host plus its wildcard ingest; the live-wiring ` +
      `fix of 2026-08-04 added the two EGRESS rows those waves were missing — GA4's measurement ` +
      `endpoint and Clarity's img-src beacon, each of which was being refused while the integration ` +
      `above it looked installed. A seventh entry means an origin arrived without a measurement ` +
      `behind it.`,
  );
  assert.equal(SURFACE_CLASS_RULES.tenant.thirdPartyOrigins, "none");
  assert.equal(SURFACE_CLASS_RULES.operator.thirdPartyOrigins, "none");
  assert.equal(SURFACE_CLASS_RULES.public.thirdPartyOrigins, "allowlisted");
  // `session_replay` is not merely absent from the tenant allowlist — it is not expressible there.
  for (const klass of ["tenant", "operator"]) {
    assert.ok(!SURFACE_CLASS_RULES[klass].categories.includes("session_replay"));
    assert.ok(!SURFACE_CLASS_RULES[klass].categories.includes("product_analytics"));
  }
});

test("no allowlisted origin claims a category its own surfaces refuse", () => {
  // The both-directions check applied to the artefact itself: an entry may not name a surface class
  // whose rule does not permit its category. Without this, a row could look permitted and silently do
  // nothing — which reads, to the next person, as a working integration that is broken.
  for (const origin of ALLOWED_ORIGINS) {
    for (const surface of origin.surfaces) {
      assert.ok(
        SURFACE_CLASS_RULES[surface].categories.includes(origin.category),
        `${origin.origin} claims the ${surface} surface but ${surface} does not permit ` +
          `${origin.category} — the row is a permission that can never take effect`,
      );
    }
  }
});

test("every allowlist entry carries a justification, a category, a directive and a budget", () => {
  // Vacuous today by design; it is the assertion that stops the FIRST entry arriving without them.
  for (const origin of ALLOWED_ORIGINS) {
    assert.match(origin.origin, /^https:\/\/[a-z0-9.*-]+$/, `${origin.origin} is not a bare https origin`);
    if (origin.origin.includes("*")) {
      /*
       * 🔴 A wildcard is permitted ONLY under the four conditions below, and each of them is what keeps
       * "an allowlist with a wildcard is not an allowlist" from becoming true.
       *
       * The design's rule is exact origins. One vendor cannot meet it — Clarity ingests to a regional
       * host chosen at runtime — and the three ways out were: enumerate the regions (fails closed and
       * SILENTLY when a new one appears), refuse Clarity (what the tenant and operator surfaces do), or
       * permit a wildcard that is bounded and stated. This is the third, and these are the bounds.
       */
      assert.match(origin.origin, /^https:\/\/\*\.[a-z0-9.-]+$/, `${origin.origin} wildcards more than the leftmost label`);
      assert.ok(origin.wildcardReason && origin.wildcardReason.length > 60, `${origin.origin} has no recorded reason for its wildcard`);
      assert.equal(origin.directive, "connect-src", `${origin.origin} wildcards a directive other than connect-src`);
      assert.deepEqual([...origin.surfaces], ["public"], `${origin.origin} wildcards a non-public surface class`);
    } else {
      assert.ok(!origin.wildcardReason, `${origin.origin} records a wildcard reason but has no wildcard`);
    }
    assert.notEqual(origin.category, "essential", `${origin.origin} claims to be essential — no third party is`);
    assert.ok(origin.budgetBytes > 0, `${origin.origin} has no transfer budget`);
    assert.ok(origin.why.length > 20, `${origin.origin} has no justification a reviewer can read`);
    assert.ok(
      ["page-load", "on-event"].includes(origin.contactedOn),
      `${origin.origin} does not say WHEN it is contacted — the stale-entry check needs it, and an ` +
        `entry with no answer would be either permanently reported stale or silently exempt`,
    );
    assert.ok(
      ["connect-src", "img-src", "strict-dynamic"].includes(origin.directive),
      `${origin.origin} names script-src`,
    );
  }
});

test("the surface enum is closed, well-formed, and carries no URL", () => {
  assert.ok(SURFACES.length >= 30, `only ${SURFACES.length} surfaces — the enum looks truncated`);
  const seen = new Set();
  for (const id of SURFACES) {
    assert.match(id, /^(public|tenant|operator)\.[a-z0-9_]+$/, `${id} is not <class>.<name>`);
    assert.ok(!seen.has(id), `duplicate surface id ${id}`);
    seen.add(id);
    assert.doesNotMatch(id, /\//, `${id} contains a path segment — a surface id is never a URL`);
  }
});

// ── 1.4 · Per-prefix assertions, against a real running console ──────────────

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

/** thirdPartyOrigins extracts every absolute origin a policy names. */
function thirdPartyOrigins(csp) {
  return [...csp.matchAll(/https?:\/\/[^\s;]+/g)].map((m) => m[0]);
}

test("1.4 the /api prefix carries default-src 'self' and no third-party origin", async () => {
  const res = await fetch(`${console_.base}/api/health`);
  const csp = res.headers.get("content-security-policy") ?? "";
  assert.match(csp, /default-src 'self'/, "the /api prefix lost default-src 'self'");
  assert.deepEqual(
    thirdPartyOrigins(csp),
    [],
    "the BFF data prefix names a third-party origin — a tenant's URLs must not reach a third party's logs",
  );
  assert.doesNotMatch(csp, /clarity|googletagmanager|google-analytics/i, "an analytics or replay origin reached /api");
  assert.doesNotMatch(csp, /'unsafe-eval'/);
  assert.match(csp, /'strict-dynamic'/);
});

test("1.4 the /app prefix carries default-src 'self' and no third-party origin", async () => {
  // A session is required: an unauthenticated `/app` redirects before the header is set, so asserting
  // against the redirect would be asserting against a response the tenant never sees.
  const cookie = await signIn(console_.base);
  const res = await fetch(`${console_.base}/app`, { headers: { cookie } });
  assert.equal(res.status, 200, "the tenant route did not render — the assertion would prove nothing");
  const csp = res.headers.get("content-security-policy") ?? "";
  assert.match(csp, /default-src 'self'/, "the tenant prefix lost default-src 'self'");
  // Bounded by the error-diagnostics half of the allowlist rather than by zero. The ONE third-party
  // origin a tenant prefix may name is the error-reporting ingest host, and it may name it only when
  // `error_diagnostics` is granted — so on this request, which carries no consent cookie, the correct
  // answer is still none, and the assertion says WHY rather than asserting a number.
  const reporting = new Set(
    ALLOWED_ORIGINS.filter((o) => o.category === "error_diagnostics").map((o) => o.origin),
  );
  for (const named of thirdPartyOrigins(csp)) {
    assert.ok(
      reporting.has(originOf(named)),
      `the tenant prefix names ${named} — /app renders prompt text, diffs and run output, and the only ` +
        `third-party origin permitted there is the error-reporting one`,
    );
  }
  assert.deepEqual(
    thirdPartyOrigins(csp),
    [],
    "a request with no consent cookie was served a tenant policy naming a third-party origin — " +
      "default-denied means the header does not name it either",
  );
  // The refusal that is specific to this prefix, and that the global assertion could never state.
  assert.doesNotMatch(csp, /clarity/i, "a session-replay origin appears on a tenant route");
  assert.doesNotMatch(csp, /googletagmanager|google-analytics/i, "an analytics origin appears on a tenant route");
});

test("1.4 the public prefix names only origins the allowlist carries", async () => {
  for (const path of ["/", "/install", "/signin"]) {
    const res = await fetch(`${console_.base}${path}`);
    const csp = res.headers.get("content-security-policy") ?? "";
    assert.match(csp, /default-src 'self'/);
    const permitted = new Set(ALLOWED_ORIGINS.map((o) => o.origin));
    for (const origin of thirdPartyOrigins(csp)) {
      const bare = origin.replace(/\/.*$/, "");
      assert.ok(permitted.has(bare), `${path} names ${bare}, which the allowlist does not carry`);
    }
  }
});

test("1.4 script-src gains no host on any prefix — third-party scripts arrive through strict-dynamic", async () => {
  const cookie = await signIn(console_.base);
  for (const [path, headers] of [["/", {}], ["/api/health", {}], ["/app", { cookie }]]) {
    const res = await fetch(`${console_.base}${path}`, { headers });
    const csp = res.headers.get("content-security-policy") ?? "";
    const scriptSrc = /script-src ([^;]+)/.exec(csp)?.[1] ?? "";
    assert.doesNotMatch(scriptSrc, /https?:\/\//, `${path}'s script-src names a host`);
    assert.doesNotMatch(scriptSrc, /'unsafe-inline'/, `${path}'s script-src allows inline script`);
    assert.match(scriptSrc, /'strict-dynamic'/, `${path}'s script-src lost strict-dynamic`);
  }
});
