// security.test.mjs holds the §3.11 assertions — SECURITY ASSERTIONS, NOT REVIEW ITEMS.
//
// That distinction is the whole reason this file exists. "The credential is server-side", "the route
// fails closed", "a revoked session is denied at the next request" are all statements somebody can
// read a diff and believe. Each of them has also been true right up until the commit that made it
// false, in every codebase that only checked them by reading.
//
// Two halves. The STATIC half parses the source for the invariants a boundary is made of. The LIVE
// half starts a real console against a controllable stub platform and exercises the boundary over real
// sockets — because a 404 that must survive two hops, and a hung upstream that must become a transport
// failure inside a timeout, are not observable any other way.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { startStubPlatform, startConsole, signIn, TENANT } from "./support/harness.mjs";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

/**
 * code strips comments before scanning.
 *
 * Written after two of the assertions below failed against the very PROSE that describes the rule they
 * enforce: the file explaining why `catch { return [] }` must never appear was flagged for containing
 * `catch { return [] }`. A source guard that cannot tell code from commentary either cries wolf or, if
 * somebody "fixes" it by loosening the pattern, stops catching the real thing.
 */
const code = async (rel) =>
  (await read(rel)).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else if (rel.endsWith(".ts") || rel.endsWith(".tsx")) yield rel;
  }
}

// ── Static half ──────────────────────────────────────────────────────────────

test("the credential is read in exactly one module, and that module is server-only", async () => {
  const offenders = [];
  for await (const file of walk("src")) {
    const src = await read(file);
    if (!/CONSOLE_PLATFORM_CREDENTIAL/.test(src)) continue;
    // The health route reports whether a credential is CONFIGURED — a boolean, never the value.
    if (file.endsWith(join("api", "health", "route.ts"))) {
      assert.match(src, /Boolean\(process\.env\.CONSOLE_PLATFORM_CREDENTIAL\)/, `${file} must report only a boolean`);
      continue;
    }
    if (!file.endsWith(join("lib", "platformApi.ts"))) offenders.push(file);
  }
  assert.deepEqual(offenders, [], "the platform credential is read outside src/lib/platformApi.ts");
  assert.match(await read("src/lib/platformApi.ts"), /^import "server-only";/m);
});

test("no client component can reach the credential-holding modules", async () => {
  // A `"use client"` file that imports a server-only module is a build error — but only if the import
  // exists to be caught. This asserts the shape directly, so the rule is visible rather than implied.
  for await (const file of walk("src")) {
    const src = await read(file);
    if (!/^"use client";/m.test(src)) continue;
    assert.doesNotMatch(src, /from "@?\/?.*lib\/(platformApi|session|scope|identity|bff)"/, `${file} is a client component importing a server-side module`);
  }
});

test("the bundle scan checks for credential material and priced literals", async () => {
  const scan = await read("scripts/scan-bundle.mjs");
  assert.match(scan, /CONSOLE_PLATFORM_CREDENTIAL/);
  assert.match(scan, /CONSOLE_TENANT_ASSERTIONS/);
  assert.match(scan, /PRICE_PATTERNS/);
});

test("scope is derived from the session and has no tenant parameter to override", async () => {
  const scope = await read("src/lib/scope.ts");
  // `scoped(session)` captures the tenant. If any exported path builder took a tenant argument, a call
  // site could supply one — which is the whole class of defect NFR12 forbids.
  assert.match(scope, /export function scoped\(session: Session\)/);
  assert.doesNotMatch(scope, /\(\s*tenantId\s*:/, "a path builder accepts a tenant argument — scope could be widened by a caller");
  // The one tenant-keyed upstream route must use the captured tenant, never an argument.
  assert.match(scope, /billing:\s*\(period\?: string\)/);
});

test("the session cookie is HttpOnly and SameSite, in exactly one place", async () => {
  const cookies = await read("src/lib/cookies.ts");
  assert.match(cookies, /httpOnly:\s*true/);
  assert.match(cookies, /sameSite:\s*"strict"/);

  // The invariant is about the CREDENTIAL cookie: its flags are set once, so there is one place to
  // audit and one place to get wrong. The console does set a second cookie — the theme preference —
  // and that one is deliberately NOT httpOnly, because it carries no identity and authorises nothing;
  // marking it httpOnly would imply it is sensitive, and a flag that means nothing stops meaning
  // anything. So the count below is of cookies claiming credential protection, not of cookies.
  const protecting = [];
  for await (const file of walk("src")) {
    if (/httpOnly:\s*true/.test(await read(file))) protecting.push(file);
  }
  assert.deepEqual(protecting, ["src/lib/cookies.ts"], "credential cookie flags are set in more than one place");

  // And the non-credential cookie must say so explicitly rather than by omission.
  const theme = await read("src/lib/theme.ts");
  assert.match(theme, /httpOnly:\s*false/, "a non-credential cookie states its flags rather than defaulting");
  assert.doesNotMatch(theme, /sameSite:\s*"strict"/, "the theme must survive arriving from a shared link");
});

test("telemetry logs no request or response body", async () => {
  const telemetry = await code("src/lib/telemetry.ts");
  // The log emitter takes a fixed field set. A body would have to arrive through one of them.
  assert.doesNotMatch(telemetry, /\bbody\b\s*[:,]/, "telemetry references a body field");
  assert.match(telemetry, /export function template\(/, "paths are not reduced to templates");
});

test("the forwarder returns an outcome rather than throwing, so a caller cannot lose the failure class", async () => {
  const api = await code("src/lib/platformApi.ts");
  // The union is asserted exactly, so a class cannot be added or removed without a deliberate edit
  // here. `gated` joined it when FR15's entitlement boundary stopped being rendered as an error.
  assert.match(
    api,
    /export type PlatformFailureKind = "not-mounted" \| "not-found" \| "gated" \| "upstream" \| "transport"/,
  );

  // The transport catch must produce a transport OUTCOME, not a value. This is the specific assertion;
  // the sweep below is the general one.
  const transportCatch = api.match(/catch \(cause\) {[\s\S]*?return outcome;/);
  assert.ok(transportCatch, "platformFetch's transport catch does not return a transport outcome");
  assert.match(transportCatch[0], /kind: "transport" as const/);

  // The sweep forbids the shape that LIES: `catch { return [] }` turns "we could not reach the
  // platform" into "there is genuinely nothing", which tells the user the wrong thing and sends them
  // to look in the wrong place.
  //
  // It deliberately does NOT forbid `catch { return null }`. That is what a JSON parse helper does
  // when a body is not JSON, and the caller then treats it as "no parsed body" rather than as an empty
  // result — a different fact, correctly represented. Writing the guard broadly enough to catch it
  // meant either contorting the parser to satisfy a regex, or loosening the pattern until it stopped
  // catching the real thing. Precision here is what keeps the guard alive.
  for await (const file of walk("src")) {
    const src = await code(file);
    assert.doesNotMatch(
      src,
      /catch\s*(\([^)]*\))?\s*{\s*return\s*(\[\]|\{\})\s*;?\s*}/,
      `${file} swallows a failure into an empty result`,
    );
  }
});

// ── Live half ────────────────────────────────────────────────────────────────

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

test("an unauthenticated data route redirects to sign-in rather than rendering a shell", async () => {
  const res = await fetch(`${console_.base}/app`, { redirect: "manual" });
  assert.equal(res.status, 307, "an unauthenticated console route did not redirect");
  const location = res.headers.get("location") ?? "";
  assert.match(location, /\/signin\?/);
  assert.match(location, /reason=no_session/);
  // And it must not have rendered anything of the console.
  const body = await res.text();
  assert.doesNotMatch(body, /Overview/, "the redirect response still carried console content");
});

test("an unauthenticated BFF route refuses rather than redirecting", async () => {
  const res = await fetch(`${console_.base}/api/console/runs/run-1`, { redirect: "manual" });
  assert.equal(res.status, 401);
  assert.equal(platform.requests.length, 0, "an unauthenticated request reached the platform");
});

test("a signed-in request reaches the platform scoped to the session's tenant", async () => {
  const cookie = await signIn(console_.base);
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ run_id: "run-1", status: "succeeded", nodes: [] }));
  });
  const before = platform.requests.length;
  const res = await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  assert.equal(res.status, 200);
  const upstream = platform.requests[before];
  assert.equal(upstream.url, "/api/p2/runs/run-1");
  assert.equal(upstream.headers["x-console-tenant"], TENANT);
});

test("a client-supplied tenant identifier cannot widen scope", async () => {
  const cookie = await signIn(console_.base);
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  const before = platform.requests.length;
  // Every channel a client controls: query, header, and a tenant-shaped path segment.
  await fetch(`${console_.base}/api/console/runs/run-1?tenant=tenant-other&customer_id=tenant-other`, {
    headers: { cookie, "X-Console-Tenant": "tenant-other", "X-Tenant-Id": "tenant-other" },
  });
  const upstream = platform.requests[before];
  assert.equal(upstream.headers["x-console-tenant"], TENANT, "a client-supplied tenant reached the platform");
  assert.doesNotMatch(upstream.url, /tenant-other/, "a client-supplied tenant reached the upstream path");
});

test("a 404 is not returned as an empty successful result", async () => {
  const cookie = await signIn(console_.base);
  platform.set((req, res) => {
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "no such run" }));
  });
  const res = await fetch(`${console_.base}/api/console/runs/missing`, { headers: { cookie } });
  assert.equal(res.status, 404, "a 404 did not survive the BFF hop");
  const body = await res.json();
  assert.equal(body.kind, "not-found");
  // The upstream's own words survive: "no such run" is the product, not a message invented here.
  assert.equal(body.error, "no such run");
});

test("a 503 not-mounted stays distinguishable from a 404", async () => {
  const cookie = await signIn(console_.base);
  platform.set((req, res) => {
    res.writeHead(503, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "the P2 store is not mounted" }));
  });
  const res = await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  assert.equal(res.status, 503);
  const body = await res.json();
  assert.equal(body.kind, "not-mounted");
  assert.equal(body.error, "the P2 store is not mounted");
});

test("a hung upstream becomes a transport failure inside the timeout", async () => {
  const cookie = await signIn(console_.base);
  // Accept the connection and answer nothing, ever. This is the case an unbounded fetch turns into an
  // infinite spinner, which is the worst of the failures to render: it says nothing and offers nothing.
  platform.set(() => {});
  const started = Date.now();
  const res = await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  const elapsed = Date.now() - started;
  assert.equal(res.status, 502, "a hung upstream did not surface as a transport failure");
  const body = await res.json();
  assert.equal(body.kind, "transport");
  assert.ok(elapsed < 6000, `the timeout did not bound the request (took ${elapsed} ms)`);
});

test("a revoked session is denied at the NEXT request, with no grace period", async () => {
  const cookie = await signIn(console_.base);
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  const ok = await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  assert.equal(ok.status, 200, "the session was not usable before revocation");

  const out = await fetch(`${console_.base}/api/session`, { method: "DELETE", headers: { cookie } });
  assert.equal(out.status, 200);

  // The browser still holds the cookie. The next request must be denied on the SERVER's judgement,
  // not on the client having discarded it.
  const before = platform.requests.length;
  const denied = await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  assert.equal(denied.status, 401, "a revoked session was still served");
  assert.equal(platform.requests.length, before, "a revoked session's request still reached the platform");
});

test("a revoked session is denied on a page route too, and no shell is rendered", async () => {
  const cookie = await signIn(console_.base);
  await fetch(`${console_.base}/api/session`, { method: "DELETE", headers: { cookie } });
  const res = await fetch(`${console_.base}/app`, { headers: { cookie }, redirect: "manual" });
  assert.equal(res.status, 307);
  assert.match(res.headers.get("location") ?? "", /reason=session_ended/);
});

test("the shipped CSP has no unsafe-eval and no third-party origin", async () => {
  const res = await fetch(`${console_.base}/signin`);
  const csp = res.headers.get("content-security-policy") ?? "";
  assert.match(csp, /default-src 'self'/);
  assert.match(csp, /'strict-dynamic'/);
  assert.doesNotMatch(csp, /'unsafe-eval'/, "the shipped CSP allows eval");
  assert.doesNotMatch(csp, /https?:\/\//, "the shipped CSP names a third-party origin");
});

test("the health endpoint reports the console without probing the platform", async () => {
  const before = platform.requests.length;
  const res = await fetch(`${console_.base}/api/health`);
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.equal(body.component, "console");
  assert.equal(body.credential_configured, true);
  // A boolean, never the value.
  assert.equal(JSON.stringify(body).includes("harness-platform-credential"), false);
  assert.equal(platform.requests.length, before, "the health check probed the platform");
});

test("the SSE proxy delivers each event as it arrives, without batching", async () => {
  const cookie = await signIn(console_.base);
  // The stub flushes ONE event, then waits. A proxy that batches, buffers, or waits for the stream to
  // finish delivers nothing here — which is exactly the failure that looks like "streaming works" in
  // review and like a frozen screen in production.
  let close;
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
    res.write("data: {\"run_id\":\"run-1\",\"status\":\"running\",\"terminal\":false,\"nodes\":[]}\n\n");
    close = () => {
      res.write("data: {\"run_id\":\"run-1\",\"status\":\"succeeded\",\"terminal\":true,\"nodes\":[]}\n\n");
      res.end();
    };
  });

  const res = await fetch(`${console_.base}/api/stream/runs/run-1`, { headers: { cookie } });
  assert.equal(res.status, 200);
  assert.match(res.headers.get("content-type") ?? "", /text\/event-stream/);
  assert.equal(res.headers.get("x-accel-buffering"), "no");

  const reader = res.body.getReader();
  const first = await Promise.race([
    reader.read().then(({ value }) => new TextDecoder().decode(value)),
    new Promise((_, reject) => setTimeout(() => reject(new Error("no event arrived within 3s — the proxy is batching")), 3000)),
  ]);
  assert.match(first, /"status":"running"/, "the first event did not arrive on its own");

  // And the upstream close must close the client stream, so the client can tell a completed stream
  // from a failed one rather than hanging on a socket that will never speak again.
  close();
  let sawTerminal = false;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (new TextDecoder().decode(value).includes('"terminal":true')) sawTerminal = true;
  }
  assert.equal(sawTerminal, true, "the terminal event was not delivered before the stream closed");
});

test("the platform's trace_id is carried back to the browser", async () => {
  const cookie = await signIn(console_.base);
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json", "X-Trace-Id": "trace-abc123" });
    res.end("{}");
  });
  const res = await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  // A single request must be followable browser -> BFF -> platform. Without this the console's logs
  // and the platform's logs are two unrelated piles during the incident where you need one.
  assert.equal(res.headers.get("x-trace-id"), "trace-abc123");
});
