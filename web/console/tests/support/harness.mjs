// harness.mjs starts a real console against a CONTROLLABLE stub platform.
//
// # Why a stub platform rather than a mock inside the process
//
// The properties under test are properties of the BOUNDARY: that a 404 crosses two hops and arrives as
// a 404, that a hung socket becomes a transport failure within the timeout, that an SSE stream is not
// batched. None of those is observable if the upstream is a function call — they are observable only
// when there is a real socket that can really hang, really 404, and really flush one event and wait.
//
// So this starts two processes' worth of reality: a stub HTTP server whose behavior each test dictates,
// and the console itself, wired to it by `PLATFORM_API_BASE`. A test that passes here has exercised the
// same code path a production request takes.
//
// # Why `next start` and not `next dev`
//
// The shipped CSP, the production cookie flags and the production identity guard all differ in dev, and
// the security assertions are about what SHIPS. Rendered-browser acceptance (R11) runs against `next dev`
// separately, for the opposite reason: it needs hot reload and a browser-usable cookie over plain HTTP.

import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { existsSync } from "node:fs";
import { join } from "node:path";

/** TENANT is the tenant the test assertion resolves to. */
export const TENANT = "tenant-hermes";
/** ASSERTION is the sign-in credential the configured identity provider accepts. */
export const ASSERTION = "test-assertion-value-not-a-secret";

/**
 * startStubPlatform starts an HTTP server whose response is decided per request by `handler`.
 *
 * `handler(req, res)` may do anything, including nothing at all — which is how the hung-upstream case
 * is expressed: a socket that accepts and never answers.
 */
export async function startStubPlatform() {
  let handler = (_req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  };
  const requests = [];
  const server = createServer((req, res) => {
    requests.push({ method: req.method, url: req.url, headers: req.headers });
    handler(req, res);
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const { port } = server.address();
  return {
    base: `http://127.0.0.1:${port}`,
    requests,
    set(next) {
      handler = next;
    },
    async close() {
      server.closeAllConnections?.();
      server.close();
    },
  };
}

/**
 * freePort asks the OS for a port nobody is using, then releases it.
 *
 * # Why not a derived number
 *
 * This used to be `4400 + hrtime % 90`. Node's test runner runs test FILES concurrently, and this
 * repository now has three that each start a console — plus one test that starts a fourth mid-run. In a
 * 90-slot space that collides regularly, and the failure is worse than a crash: the loser's health
 * check can succeed against the WINNER's console, so a test quietly asserts against another file's
 * server. That is exactly the intermittent failure this function was written after chasing.
 *
 * Asking the OS removes the arithmetic. The window between closing the probe and Next binding is small,
 * and an ephemeral port is not handed to a second listener inside it — but `startConsole` retries once
 * regardless, because "small" is not "none".
 */
async function freePort() {
  const probe = createServer();
  probe.listen(0, "127.0.0.1");
  await once(probe, "listening");
  const { port } = probe.address();
  await new Promise((resolve) => probe.close(resolve));
  return port;
}

/**
 * requireProductionBuild fails LOUD when `.next` is not a usable production build.
 *
 * # Why this check earns its place
 *
 * Without it, a missing or clobbered build produces the single most misleading failure this suite can
 * emit. `next start` boots, answers `/api/health`, and then serves pages assembled from a half-written
 * manifest — so every test that signs in fails with "the sign-in page rendered no form action". That
 * sentence sends the reader to `signin/page.tsx`, where the form and its `action` are plainly present
 * and correct, and there is nothing to find. A hundred tests then report a defect that does not exist.
 *
 * The clobbering cause is not obvious either: `next dev` and `next start` share ONE `.next` directory,
 * so a dev server left running anywhere against this console rewrites the build these tests depend on,
 * continuously, from another terminal. That is why the message below names it — the fix is "stop the dev
 * server", and no amount of reading the sign-in page reveals that.
 *
 * A precondition reported as a precondition costs one line; reported as a hundred assertion failures it
 * costs an afternoon.
 */
function requireProductionBuild() {
  const buildID = join(process.cwd(), ".next", "BUILD_ID");
  if (existsSync(buildID)) return;
  throw new Error(
    [
      `no production build at ${buildID}`,
      "",
      "These tests run `next start`, which needs a build that `next dev` does not produce.",
      "  1. run `npm run build` in web/console",
      "  2. make sure NO `next dev` is running against this directory - dev and start share one",
      "     .next, so a dev server rewrites the build out from under this suite while it runs",
      "     (check with: pgrep -fl 'next dev')",
      "",
      "Failing here on purpose: without this check the suite reports ~100 sign-in assertion failures",
      "for a sign-in page that is entirely correct.",
    ].join("\n"),
  );
}

/** startConsole starts `next start` on a free port, wired to `platformBase`. */
export async function startConsole(platformBase, extraEnv = {}, attempt = 0) {
  requireProductionBuild();
  const port = await freePort();
  const child = spawn("npx", ["next", "start", "--port", String(port)], {
    cwd: process.cwd(),
    env: {
      ...process.env,
      NODE_ENV: "production",
      PLATFORM_API_BASE: platformBase,
      CONSOLE_PLATFORM_CREDENTIAL: "harness-platform-credential-do-not-ship",
      CONSOLE_TENANT_IDENTITY: "configured",
      CONSOLE_TENANT_ASSERTIONS: JSON.stringify({ [ASSERTION]: TENANT }),
      // Short enough that the hung-upstream test does not take ten seconds, long enough that a
      // healthy stub always answers inside it.
      CONSOLE_UPSTREAM_TIMEOUT_MS: "1500",
      ...extraEnv,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  const logs = [];
  child.stdout.on("data", (chunk) => logs.push(String(chunk)));
  child.stderr.on("data", (chunk) => logs.push(String(chunk)));

  const base = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 30_000;
  for (;;) {
    if (Date.now() > deadline) {
      child.kill("SIGKILL");
      throw new Error(`console did not start within 30s:\n${logs.join("")}`);
    }
    // A bind failure is retried ONCE on a fresh port rather than waited out: the loop would otherwise
    // spend thirty seconds polling a port this process never got, and then report a timeout that says
    // nothing about the actual cause.
    if (logs.join("").includes("EADDRINUSE")) {
      child.kill("SIGKILL");
      if (attempt > 0) throw new Error(`console could not bind a free port:\n${logs.join("")}`);
      return startConsole(platformBase, extraEnv, attempt + 1);
    }
    try {
      const res = await fetch(`${base}/api/health`);
      if (res.ok) break;
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }

  return {
    base,
    logs,
    async close() {
      child.kill("SIGTERM");
      await new Promise((resolve) => setTimeout(resolve, 150));
      child.kill("SIGKILL");
    },
  };
}

/**
 * signIn performs the session exchange and returns the cookie header to send on later requests.
 *
 * It posts the server action the same way the browser does. `redirect: "manual"` is required: the
 * action answers with a redirect, and following it would discard the `Set-Cookie` we are here for.
 */
export async function signIn(base) {
  // The page is read first, and its form target is used — so this exercises the path a browser takes
  // rather than a URL the test happens to know. If the form ever stops posting to the session route,
  // this breaks, which is the point.
  const page = await fetch(`${base}/signin`);
  const html = await page.text();
  const target = html.match(/<form[^>]*action="([^"]+)"/)?.[1];
  if (!target) throw new Error("the sign-in page rendered no form action");

  const form = new URLSearchParams({ assertion: ASSERTION, next: "/app" });
  const post = await fetch(`${base}${target}`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: form,
    redirect: "manual",
  });
  const setCookie = post.headers.getSetCookie?.() ?? [];
  const session = setCookie.map((c) => c.split(";")[0]).find((c) => c.startsWith("heros_console_session="));
  if (!session) throw new Error(`sign-in did not set a session cookie (status ${post.status})`);
  return session;
}
