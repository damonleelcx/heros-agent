// server.mjs starts this console the way it is deployed, so a HEADER can be asserted rather than a
// source line that is believed to produce one.
//
// # Why the operator console grew a live harness for P24 and not before
//
// Every other assertion in this suite is about source shape, and source shape was enough: a token
// literal, a missing ledger row and a derived figure without its coverage are all visible in the file
// that carries them. A Content-Security-Policy is not. It is assembled in middleware, on the edge
// runtime, per request — and the failure this phase is guarding against is precisely a header that
// says something different from what the file appears to say.
//
// The customer console already carries `tests/support/harness.mjs` for the same reason and with the
// same argument. This is the narrow version of it: no stub upstream, because none of the operator
// assertions in P24 involve one.
//
// `next start` and not `next dev`, because the shipped policy and the development policy differ by
// `'unsafe-eval'` and the assertion is about what ships.

import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { existsSync } from "node:fs";
import { join } from "node:path";

/** freePort asks the OS for a port nobody is using, so concurrent test files cannot collide. */
async function freePort() {
  const probe = createServer();
  probe.listen(0, "127.0.0.1");
  await once(probe, "listening");
  const { port } = probe.address();
  await new Promise((resolve) => probe.close(resolve));
  return port;
}

/**
 * startAdminConsole boots `next start` on a free port and resolves once it answers its health route.
 *
 * A missing build fails LOUD and names the fix. Without that, `next start` boots against a half-written
 * manifest and every assertion below fails with a message about a policy — sending the reader to
 * `middleware.ts`, where there is nothing to find.
 */
export async function startAdminConsole(extraEnv = {}) {
  if (!existsSync(join(process.cwd(), ".next", "BUILD_ID"))) {
    throw new Error(
      [
        "no production build at .next/BUILD_ID",
        "",
        "These assertions read a real response header, which needs `next start`, which needs a build:",
        "  1. run `npm run build` in web/admin-console",
        "  2. make sure no `next dev` is running against this directory — dev and start share one .next",
      ].join("\n"),
    );
  }

  const port = await freePort();
  /*
   * 🔴 `detached: true`, and the close below kills the process GROUP.
   *
   * This job hung in CI for eleven minutes with all 116 tests PASSED and the step never finishing —
   * which is the worst way for a suite to fail, because the log says everything worked.
   *
   * `npx next start` is a WRAPPER. It spawns the real `next` server as a grandchild, so
   * `child.kill()` reaps the wrapper and leaves the server running with its stdio pipes attached to
   * this process — and Node will not exit while a pipe it is reading from is open. Locally the runner
   * happened to exit anyway; on a CI runner it did not, and "happened to" is not a property.
   *
   * `detached` puts the wrapper and everything it spawns in their own process group, so one signal to
   * `-pid` reaps all of it. The pipes are destroyed as well, because a killed process's pipe can still
   * hold the loop open until the descriptor closes.
   */
  const child = spawn("npx", ["next", "start", "--port", String(port)], {
    cwd: process.cwd(),
    detached: true,
    env: {
      ...process.env,
      NODE_ENV: "production",
      ADMIN_API_BASE: "http://127.0.0.1:1",
      ADMIN_PLATFORM_CREDENTIAL: "harness-admin-credential-do-not-ship",
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
      stop(child);
      throw new Error(`the operator console did not start within 30s:\n${logs.join("")}`);
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
      // Wait for the process to be REAPED rather than for a fixed delay. A timeout that is long enough
      // on a developer machine is the thing that was not long enough on a CI runner.
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
 * The process GROUP, not the process: `npx` is a wrapper around the real `next` server, and signalling
 * only the wrapper leaves the server running with its stdio attached to this process — which is exactly
 * how a suite whose every test passed hangs forever.
 */
function stop(child) {
  try {
    // Negative pid = the whole group. `detached: true` above is what makes the group exist.
    process.kill(-child.pid, "SIGKILL");
  } catch {
    // Already gone, or the group never formed. Fall through to the direct kill.
    try {
      child.kill("SIGKILL");
    } catch {
      // nothing left to kill
    }
  }
  // Node will not exit while it is still reading a pipe, even from a dead process.
  child.stdout?.destroy();
  child.stderr?.destroy();
}
