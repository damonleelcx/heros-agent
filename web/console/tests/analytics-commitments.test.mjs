// analytics-commitments.test.mjs is P24 task 8.2 — the FOUR AMENDED COMMITMENTS, each with a named
// regression test that fails if a future phase re-widens it.
//
// # Why this file exists separately from the fences
//
// Every other test in this phase asserts what the code does. This one asserts what the phase PROMISED,
// against the four commitments PRD §2.3 amends. Those four were enforced by tests before P24 and are
// still enforced after it — narrower, by route prefix, out loud — and the failure mode this file is
// written against is not a bug. It is a future phase reading "the public surface may name allowlisted
// origins" and generalising it one prefix at a time, each step reasonable, until the tenant guarantee is
// gone and nobody can say which change removed it.
//
// So each commitment gets ONE named test, stating what was amended and what was not.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { buildContentSecurityPolicy } from "../../design-system/csp.ts";
import { ALLOWED_ORIGINS, SURFACE_CLASS_RULES } from "../../design-system/third-party-policy.ts";
import { DEFAULT_STATE, grantedCategories } from "../../design-system/consent.ts";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const WEB = new URL("../..", import.meta.url).pathname;
const readWeb = (rel) => readFile(join(WEB, rel), "utf8");

const ALL_CATEGORIES = ["essential", "product_analytics", "session_replay", "error_diagnostics"];

// ── Commitment 1 ─────────────────────────────────────────────────────────────

test("8.2 COMMITMENT 1 — `default-src 'self'` and the per-request nonce survive on EVERY prefix", () => {
  // AMENDED: the public prefix may name allowlisted origins under connect-src and img-src.
  // NOT AMENDED: `default-src 'self'` everywhere; the nonce everywhere; `'strict-dynamic'` everywhere;
  //              no `'unsafe-inline'` for scripts; no host on script-src, on any prefix, ever.
  for (const consoleId of ["customer", "operator"]) {
    for (const pathname of ["/", "/install", "/app", "/app/studio", "/api/console/x", "/tenants", "/audit"]) {
      const csp = buildContentSecurityPolicy({
        consoleId, pathname, nonce: "NONCE", dev: false, granted: ALL_CATEGORIES,
      });
      assert.match(csp, /default-src 'self'/, `${consoleId}${pathname} lost default-src 'self'`);
      assert.match(csp, /'nonce-NONCE'/, `${consoleId}${pathname} lost its per-request nonce`);
      assert.match(csp, /'strict-dynamic'/, `${consoleId}${pathname} lost strict-dynamic`);
      const scriptSrc = /script-src ([^;]+)/.exec(csp)?.[1] ?? "";
      assert.doesNotMatch(scriptSrc, /'unsafe-inline'/, `${consoleId}${pathname} allows inline script`);
      assert.doesNotMatch(scriptSrc, /'unsafe-eval'/, `${consoleId}${pathname} allows eval in production`);
      assert.doesNotMatch(scriptSrc, /https?:\/\//, `${consoleId}${pathname} put a HOST on script-src`);
      assert.match(csp, /object-src 'none'/);
      assert.match(csp, /frame-ancestors 'none'/);
      assert.match(csp, /base-uri 'self'/);
      assert.match(csp, /form-action 'self'/);
    }
  }
});

// ── Commitment 2 ─────────────────────────────────────────────────────────────

test("8.2 COMMITMENT 2 — the no-`https://`-origin rule is amended by PREFIX, and the tenant half is absolute", () => {
  // AMENDED: the public prefix's rule became "only origins from the checked-in allowlist".
  // NOT AMENDED: a tenant or operator prefix names NO third-party origin except the error-reporting
  //              one, under connect-src, with everything granted.
  const reporting = new Set(
    ALLOWED_ORIGINS.filter((o) => o.category === "error_diagnostics").map((o) => o.origin),
  );
  for (const [consoleId, pathname] of [
    ["customer", "/app"], ["customer", "/app/studio"], ["customer", "/api/console/x"],
    ["operator", "/"], ["operator", "/tenants"], ["operator", "/api/overview"],
  ]) {
    const csp = buildContentSecurityPolicy({ consoleId, pathname, nonce: "N", dev: false, granted: ALL_CATEGORIES });
    const named = [...csp.matchAll(/https?:\/\/[^\s;]+/g)].map((m) => m[0]);
    for (const origin of named) {
      assert.ok(reporting.has(origin), `${consoleId}${pathname} names ${origin}, which is not the reporting origin`);
    }
    // And the reporting origin only under connect-src.
    const img = /img-src ([^;]+)/.exec(csp)?.[1] ?? "";
    assert.doesNotMatch(img, /https?:\/\//, `${consoleId}${pathname} put an origin on img-src`);
  }
  // The tenant and operator CLASSES cannot express the other two categories at all.
  for (const klass of ["tenant", "operator"]) {
    assert.equal(SURFACE_CLASS_RULES[klass].thirdPartyOrigins, "none");
    assert.deepEqual([...SURFACE_CLASS_RULES[klass].categories], ["error_diagnostics"]);
  }
});

// ── Commitment 3 ─────────────────────────────────────────────────────────────

test("8.2 COMMITMENT 3 — the public surface's third-party origins are allowlist-bounded, not unbounded", async () => {
  // AMENDED: the public surface may reference allowlisted origins, each gated on its consent category.
  // NOT AMENDED: it may reference nothing else, and nothing at all before a grant.
  const declined = buildContentSecurityPolicy({ consoleId: "customer", pathname: "/", nonce: "N", dev: false });
  assert.doesNotMatch(declined, /https?:\/\//, "the public prefix names an origin before anybody answered");

  const granted = buildContentSecurityPolicy({
    consoleId: "customer", pathname: "/", nonce: "N", dev: false, granted: ALL_CATEGORIES,
  });
  const permitted = new Set(ALLOWED_ORIGINS.map((o) => o.origin));
  for (const named of [...granted.matchAll(/https?:\/\/[^\s;]+/g)].map((m) => m[0])) {
    assert.ok(permitted.has(named), `the public prefix names ${named}, which the allowlist does not carry`);
  }
  // And the ONLY way an origin enters is the artefact: a literal in either middleware fails the build.
  const { stdout } = await exec("node", [join(WEB, "design-system", "scan-origins.mjs")], { cwd: ROOT });
  assert.match(stdout, /origin scan passed/);
});

// ── Commitment 4 ─────────────────────────────────────────────────────────────

test("8.2 COMMITMENT 4 — a visitor is not tracked before they consent to anything", () => {
  // NOT AMENDED AT ALL. The P9 scenario is preserved verbatim; default-denied consent is what preserves
  // it, and categories are what make it survivable as integrations grow — a visitor who accepted usage
  // counting has not accepted being filmed.
  assert.deepEqual(grantedCategories(DEFAULT_STATE), ["essential"]);
  for (const category of ["product_analytics", "session_replay", "error_diagnostics"]) {
    assert.equal(DEFAULT_STATE.decisions[category], "not-asked");
  }
  // Nothing is named in the header, on any surface, for a visitor who has answered nothing.
  for (const consoleId of ["customer"]) {
    for (const pathname of ["/", "/install", "/docs", "/signin", "/app"]) {
      const csp = buildContentSecurityPolicy({ consoleId, pathname, nonce: "N", dev: false });
      assert.doesNotMatch(csp, /https?:\/\//, `${consoleId}${pathname} names an origin for an unanswered visitor`);
    }
  }
});

// ── Commitment 5 — strengthened, not amended ─────────────────────────────────

test("8.2 COMMITMENT 5 — the payload ceiling MEANS MORE after this phase, not less", async () => {
  /*
   * The one the design says is strengthened. As written, `scan-bundle.mjs` measures `.next/static` —
   * the JavaScript OUR BUILD produces — so a script from a third-party host is not there. It would have
   * stopped a small 3D library and not noticed three trackers.
   *
   * Two additions, and both are asserted here rather than described: a per-origin transfer budget
   * measured in a real browser, and the inverse of the decorative-runtime scan.
   */
  const scan = await readFile(join(ROOT, "scripts", "scan-bundle.mjs"), "utf8");
  assert.match(scan, /PAYLOAD_CEILING_BYTES = 1_400_000/, "the first-party ceiling moved");
  assert.match(scan, /OBSERVABILITY RUNTIME/, "the inverse runtime scan is gone");
  assert.match(scan, /guardedChunks/, "the reachability partition is gone");

  const accept = await readFile(join(ROOT, "scripts", "accept.mjs"), "utf8");
  assert.match(accept, /encodedDataLength/, "the transfer budget is not measured on the wire");
  assert.match(accept, /BUDGET:/, "the transfer budget produces no finding");
  assert.match(accept, /another origin shrinking does not buy this one headroom/, "the budget became a total");

  for (const origin of ALLOWED_ORIGINS) {
    assert.ok(origin.budgetBytes > 0, `${origin.origin} carries no per-origin budget`);
  }
});

// ── Commitments 6–8 — untouched, and asserted to be untouched ────────────────

test("8.2 COMMITMENTS 6-8 — the CLI, the P11 boundary and the scrubbing chokepoint are untouched", async () => {
  // 6: no integration reaches the CLI. Asserted in Go two ways — a transitive ban on `net/http`, and a
  //    P24-specific ban on `internal/erroreport`.
  const surfaceTest = await readFile(join(WEB, "..", "internal", "erroreport", "surface_test.go"), "utf8");
  assert.match(surfaceTest, /TestNoIntegrationReachesTheCLI/);

  // 7: the P11 allowlist is unchanged. P24 REUSED the pattern; it did not touch the boundary.
  const runlink = await readFile(join(WEB, "..", "internal", "runlink", "allowlist.go"), "utf8");
  assert.match(runlink, /PlatformBaseURL = "https:\/\/heros-agent\.space"/, "the P11 link target moved");
  assert.doesNotMatch(runlink, /erroreport|sentry|analytics/i, "P24 reached into the P11 boundary");

  // 8: the scrubbing chokepoint is REUSED as the second guard rather than replaced.
  const scrub = await readFile(join(WEB, "..", "internal", "erroreport", "scrub.go"), "utf8");
  assert.match(scrub, /telemetry\.Scrubber/, "the error boundary does not chain the existing chokepoint");
  assert.match(scrub, /a second implementation of "what does a secret look like" is a second truth source/);
  const telemetryScrub = await readFile(join(WEB, "..", "internal", "telemetry", "scrub.go"), "utf8");
  assert.match(telemetryScrub, /func secretPatterns\(\)/, "the pattern list moved out of the chokepoint");
});

// ── 8.2 · Every fence has been demonstrated red ─────────────────────────────

test("8.2 every fence in this phase has a RED demonstration that runs", async () => {
  /*
   * The register. Each row is a fence and the assertion that makes it go red — and the assertion has to
   * EXIST, in a file, with that name. A fence nobody has seen fail is a fence nobody knows is connected,
   * and "we demonstrated it once, by hand" is a claim with a half-life.
   *
   * The three that are demonstrated inside a SCRIPT rather than a test are marked as such: the
   * acceptance run refuses to report green until it has detected a fixture third-party request, and the
   * two shell gates carry `--self-test`. Those run on every execution rather than once.
   */
  const register = [
    ["hard-coded origin", "console/tests/third-party-fence.test.mjs", /🔴 the origin scan goes RED on an origin/],
    ["per-prefix CSP", "console/tests/third-party-fence.test.mjs", /🔴 the per-prefix assertion catches a third-party origin/],
    ["structural category refusal", "console/tests/third-party-fence.test.mjs", /🔴 an analytics or replay entry claiming the tenant surface/],
    ["replay runtime in a tenant chunk", "console/tests/third-party-fence.test.mjs", /🔴 a session-replay runtime in a TENANT chunk fails/],
    ["replay runtime in an operator chunk", "admin-console/tests/third-party-fence.test.mjs", /🔴 a session-replay runtime in ANY chunk/],
    ["unlisted origin", "console/tests/third-party-fence.test.mjs", /🔴 an unlisted origin fails acceptance/],
    ["budget overage", "console/tests/third-party-fence.test.mjs", /🔴 an over-budget origin fails/],
    ["headroom stealing", "console/tests/third-party-fence.test.mjs", /🔴 one integration cannot absorb another/],
    ["stale allowlist entry", "console/tests/third-party-fence.test.mjs", /🔴 a stale allowlist entry fails/],
    ["total ceiling", "console/tests/third-party-fence.test.mjs", /🔴 the total ceiling fails/],
    ["two-console drift", "console/tests/third-party-fence.test.mjs", /🔴 the drift check goes RED on a divergent map/],
    ["drift vacuity", "console/tests/third-party-fence.test.mjs", /🔴 the drift check refuses to pass vacuously/],
    ["air-gapped external origin", "../internal/deploy/external_origins_test.go", /TestOriginGateIsConnected/],
    ["allowlist doc drift", "../internal/erroreport/doc_test.go", /TestTheDocumentGateGoesRed/],
    ["forbidden shape on the wire", "../internal/erroreport/boundary_test.go", /TestTransmittedBytesCarryNoForbiddenShape/],
    ["unexplained transmitted value", "../internal/erroreport/boundary_test.go", /TestEveryTransmittedValueIsExplained/],
    ["message-shaped code", "../internal/erroreport/boundary_test.go", /TestAMessageShapedValueThatIsNotAnEnumValue/],
    ["scrubber second guard", "../internal/erroreport/boundary_test.go", /TestTheScrubberCatchesWhatConstructionMissed/],
    ["source map in the tree", "console/tests/browser-reporting.test.mjs", /the shipped tree carries no source map/],
    ["ad-hoc event name", "design-system/scan-events.mjs", /SELF-TEST FAILED/],
    ["external origin in a package", "../deploy/scripts/check-external-origins.sh", /SELF-TEST FAILED/],
    ["measurement not connected", "console/scripts/accept.mjs", /SELF-CHECK FAILED/],
    ["nonce regime", "console/scripts/accept.mjs", /CSP SELF-CHECK/],
    ["decline is real", "console/scripts/accept.mjs", /a deliberate error with NO consent cookie/],
    ["tenant egress", "console/scripts/accept.mjs", /TENANT EGRESS/],
    ["equal-weight decline", "console/tests/consent.test.mjs", /🔴 decline carries the SAME visual weight/],
    ["refusal remembered", "console/tests/consent.test.mjs", /🔴 a refusal is stored AS a refusal/],
    ["material version re-asks", "console/tests/consent.test.mjs", /🔴 the consent policy version is the sub-processor/],
    ["replay on a tenant prefix", "console/tests/public-analytics.test.mjs", /🔴 the replay origin cannot appear on a tenant/],
    ["GA4 settings", "console/tests/public-analytics.test.mjs", /🔴 every GA4 setting that must be off/],
    ["Clarity masking", "console/tests/public-analytics.test.mjs", /🔴 Clarity starts fully masked/],
    ["analytics as a business number", "console/tests/console-analytics.test.mjs", /🔴 no analytics figure is rendered/],
    ["field added to an event", "console/tests/console-analytics.test.mjs", /🔴 a field added to the input does not reach/],
    ["seven propagation layers", "console/tests/console-analytics.test.mjs", /🔴 all seven propagation layers/],
  ];

  const missing = [];
  for (const [fence, rel, pattern] of register) {
    const src = await readFile(join(WEB, rel), "utf8").catch(() => "");
    if (!pattern.test(src)) missing.push(`${fence} — expected ${pattern} in ${rel}`);
  }
  assert.deepEqual(missing, [], `a fence has no red demonstration:\n  ${missing.join("\n  ")}`);
  assert.ok(register.length >= 30, `only ${register.length} fences registered — the list looks truncated`);
});
