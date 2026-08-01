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

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { existsSync } from "node:fs";

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
 * Two guards, because a `.next` fails these tests in two different ways and each names a different
 * fix. requireProductionBuild catches NO build (run `npm run build`); assertProductionBuild catches a
 * build a dev server has since clobbered (stop `next dev`, then rebuild). Both were written
 * independently against the same afternoon-costing symptom — ~100 sign-in assertion failures for a
 * sign-in page that is entirely correct — and neither subsumes the other, so both stay.
 *
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

/**
 * assertProductionBuild refuses to run against a `.next` a dev server has written into.
 *
 * # Why this is a hard refusal and not a warning
 *
 * `next dev` overwrites the production chunks and manifests with its own. `next start` then serves a
 * DEVELOPMENT build under production environment variables, and the identity guard behaves differently
 * enough that ~95 of these tests fail at sign-in — with assertion messages about billing copy and
 * missing plan names, none of which mention the actual cause.
 *
 * That failure has now cost two debugging passes in this repository, and both times the tests were
 * right and the tree was wrong. A build the tests cannot trust must say so in one line, the way
 * `scan-bundle.mjs` already refuses to weigh a contaminated tree, rather than producing ninety-five
 * confident and unrelated failures.
 */
async function assertProductionBuild() {
  try {
    await readFile(join(process.cwd(), ".next", "static", "development", "_buildManifest.js"), "utf8");
  } catch {
    return; // no dev artefacts — this is a production build
  }
  throw new Error(
    "`.next` has been written by a dev server, so these tests would run against a DEVELOPMENT build " +
      "under production settings and fail at sign-in for reasons unrelated to what they assert.\n" +
      "  Stop `next dev`, then:  rm -rf .next && npm run build && npm test",  );
}

/**
 * startConsole starts `next start` on a free port, wired to `platformBase`.
 *
 * `extraEnv` may be a FUNCTION of the console's own base URL. P22 needs that: a federated deployment's
 * redirect allowlist and SAML ACS allowlist contain this console's own callback URLs, and the port is
 * not known until this function has asked the OS for one. Passing a literal object would mean either
 * guessing a port — the collision failure `freePort` was written to remove — or a wildcard allowlist,
 * which is the one thing an allowlist may never be.
 */
export async function startConsole(platformBase, extraEnv = {}, attempt = 0) {
  requireProductionBuild();
  await assertProductionBuild();
  const port = await freePort();
  const resolvedEnv = typeof extraEnv === "function" ? extraEnv(`http://127.0.0.1:${port}`) : extraEnv;
  /*
   * 🔴 `detached: true`, and `stop()` below kills the process GROUP.
   *
   * `npx next start` is a WRAPPER: it spawns the real `next` server as a grandchild, so `child.kill()`
   * reaps the wrapper and leaves the server running with its stdio pipes attached to this process —
   * and Node will not exit while a pipe it is reading from is open.
   *
   * Found on a CI runner, on the OPERATOR console's copy of this harness: 116 tests passed and the job
   * sat there for eleven minutes with nothing left to do. This file had the same defect and had been
   * getting away with it — `pgrep -f "next start"` after a local run showed three orphaned servers,
   * one per suite that starts a console. It leaked; it just did not also hang.
   */
  const child = spawn("npx", ["next", "start", "--port", String(port)], {
    cwd: process.cwd(),
    detached: true,
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
      ...resolvedEnv,
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
      stop(child);
      throw new Error(`console did not start within 30s:\n${logs.join("")}`);
    }
    // A bind failure is retried ONCE on a fresh port rather than waited out: the loop would otherwise
    // spend thirty seconds polling a port this process never got, and then report a timeout that says
    // nothing about the actual cause.
    if (logs.join("").includes("EADDRINUSE")) {
      stop(child);
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
      stop(child);
      // Wait for the process to be REAPED rather than for a fixed delay. A delay that is long enough on
      // a developer machine is exactly the thing that was not long enough on a CI runner.
      await new Promise((resolve) => {
        if (child.exitCode !== null || child.signalCode !== null) return resolve();
        child.once("exit", resolve);
        setTimeout(resolve, 5000);
      });
    },
  };
}

/**
 * stop reaps the server and everything it spawned, then closes the pipes.
 *
 * The process GROUP, not the process — see the comment on `detached` above. The pipes are destroyed as
 * well, because a killed process's pipe can still hold the event loop open until its descriptor closes.
 */
function stop(child) {
  try {
    process.kill(-child.pid, "SIGKILL");
  } catch {
    try {
      child.kill("SIGKILL");
    } catch {
      // nothing left to kill
    }
  }
  child.stdout?.destroy();
  child.stderr?.destroy();
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
