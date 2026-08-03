// platform-routes.test.mjs pins the console's upstream paths to the routes the platform registers.
//
// # The failure this exists to stop
//
// `scope.ts` is the only place the console names a platform route. The platform names the same routes
// in `internal/api/*.go`. Nothing connected the two, and they drifted the moment the paths moved from
// `/api/pNN/…` to `/api/v1/…`: the prefixes were rewritten and three LEAF segments were carried over
// unchanged, while the platform had renamed those leaves after what the route does —
//
//	console asked  /api/v1/workflows/{id}/graph      platform served  …/pattern-graph
//	console asked  /api/v1/workflows/{id}/board      platform served  …/eval-board
//	console asked  /api/v1/workflows/{id}/surface    platform served  …/proposals
//
// All three answered 404. That is worse than a blank page, because `classify()` maps 404 to
// `not-found`, so the Graph, Board and Proposals views of a workflow that EXISTS rendered "No such
// workflow — the identifier does not resolve" — the exact collapse `platformApi.ts` says R3 forbids,
// since the honest answer (503, "this subsystem is not mounted on this deployment") has a different
// remedy. A typecheck cannot see this: both sides are strings, and both sides compiled.
//
// # Why it also pins the telemetry map
//
// `telemetry.ts` reduces a concrete path to a route template, and an unmatched path logs as
// `/unknown` — which that file's own comment calls visibly wrong. It was invisible anyway, because
// `/unknown` is a valid string that never throws: after the same move, EVERY matcher was dead and
// every upstream call logged as `/unknown`. So the second assertion here is that a path the console
// can emit is a path telemetry can name.
//
// # The watched failure
//
// The last test feeds the three historical paths back in and requires this fence to reject them. A
// fence nobody has seen fail is a fence nobody knows is connected.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const CONSOLE_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const REPO_ROOT = join(CONSOLE_ROOT, "..", "..");
const API_DIR = join(REPO_ROOT, "internal", "api");

/**
 * serverRoutes reads every route the platform mux registers.
 *
 * `.Mux.HandleFunc` and not `.mux.HandleFunc`: the operator console's `AdminAPI` owns a separate,
 * lower-cased mux on its own listener, and its routes are not reachable from this console.
 */
async function serverRoutes() {
  const routes = new Set();
  for (const name of await readdir(API_DIR)) {
    if (!name.endsWith(".go") || name.endsWith("_test.go")) continue;
    const src = await readFile(join(API_DIR, name), "utf8");
    for (const [, path] of src.matchAll(/\.Mux\.HandleFunc\("[A-Z]+ (\/[^"]*)"/g)) routes.add(path);
  }
  return [...routes];
}

/**
 * consolePaths reads every upstream path `scope.ts` can emit.
 *
 * Read as TEXT rather than by calling the module: the point is to catch a literal nobody executed,
 * and a path only reached from an unexercised branch is exactly the one that rots.
 */
async function consolePaths() {
  const src = await readFile(join(CONSOLE_ROOT, "src", "lib", "scope.ts"), "utf8");
  // Comments are dropped first. They discuss paths — including the wrong ones this fence exists to
  // catch — and a scan that reads prose as code reports a defect that is a sentence about the defect.
  const code = src
    .split("\n")
    .filter((line) => !/^\s*(\/\/|\*|\/\*)/.test(line))
    .join("\n");
  return [...code.matchAll(/`(\/api\/v1\/[^`]*)`/g)].map(([, p]) => p);
}

/**
 * pathTemplates reads PATH_TEMPLATES out of telemetry.ts as source.
 *
 * Not imported: telemetry.ts pulls in `server-only`, which resolves under Next and nowhere else. The
 * rest of this suite reads source text for the same class of reason, and it has the better property
 * anyway — the pattern that is PINNED is the one committed, not one a bundler rewrote.
 */
async function pathTemplates() {
  const src = await readFile(join(CONSOLE_ROOT, "src", "lib", "telemetry.ts"), "utf8");
  const block = src.slice(src.indexOf("const PATH_TEMPLATES"), src.indexOf("/** template reduces"));
  return [...block.matchAll(/\[\/(.+?)\/[gimsuy]*,\s*"([^"]+)"\]/g)].map(([, pattern, name]) => ({
    re: new RegExp(pattern),
    name,
  }));
}

/** templateFor mirrors telemetry.ts's own `template()`: first match wins, else `/unknown`. */
function templateFor(path, templates) {
  const bare = path.split("?")[0];
  return templates.find((t) => t.re.test(bare))?.name ?? "/unknown";
}

/** segments splits a path into comparable parts, treating `${…}` and `{…}` as one wildcard each. */
function segments(path) {
  return path
    .split("?")[0]
    .replace(/\$\{[^}]*\}/g, "{}")
    .replace(/\{[^}]*\}/g, "{}")
    .split("/")
    .filter(Boolean);
}

/** matches is true when a console path and a server route denote the same route. */
function matches(consolePath, serverRoute) {
  const a = segments(consolePath);
  const b = segments(serverRoute);
  if (a.length !== b.length) return false;
  return a.every((seg, i) => seg === b[i] || (seg === "{}" && b[i] === "{}"));
}

/** unregistered returns the console paths no server route answers. */
function unregistered(paths, routes) {
  return paths.filter((p) => !routes.some((r) => matches(p, r)));
}

test("every path the console asks for is a route the platform registers", async () => {
  const routes = await serverRoutes();
  const paths = await consolePaths();

  assert.ok(routes.length > 20, `only ${routes.length} platform routes were found — the scan is broken, not the code`);
  assert.ok(paths.length > 20, `only ${paths.length} console paths were found — the scan is broken, not the code`);

  const missing = unregistered(paths, routes);
  assert.deepEqual(
    missing,
    [],
    "scope.ts names a route the platform does not serve. It will 404, and a 404 classifies as " +
      "not-found — so the console will report a subject that EXISTS as one whose identifier does not " +
      "resolve. Fix the leaf in scope.ts to the name internal/api registers:\n  " +
      missing.join("\n  "),
  );
});

test("every path the console asks for has a telemetry template — none logs as /unknown", async () => {
  const templates = await pathTemplates();
  assert.ok(templates.length > 10, `only ${templates.length} templates parsed — the scan is broken, not the code`);

  const concrete = (await consolePaths()).map((p) =>
    p.replace(/\$\{[^}]*\}/g, "x").replace(/\?.*$/, ""),
  );
  const unnamed = concrete.filter((p) => templateFor(p, templates) === "/unknown");
  assert.deepEqual(
    unnamed,
    [],
    "these paths log as /unknown, so their latency and error rates are unqueryable. Add a matcher to " +
      "PATH_TEMPLATES in telemetry.ts:\n  " + unnamed.join("\n  "),
  );
});

test("🔴 the fence rejects the three paths that were actually wrong", async () => {
  const routes = await serverRoutes();
  const historical = [
    "/api/v1/workflows/${encode(workflowId)}/graph",
    "/api/v1/workflows/${encode(workflowId)}/board",
    "/api/v1/workflows/${encode(workflowId)}/surface",
  ];
  assert.deepEqual(
    unregistered(historical, routes),
    historical,
    "the fence accepted paths that a running platform answers with 404 — it is not connected",
  );
  // And the corrected forms pass, so the fence is discriminating rather than merely strict.
  assert.deepEqual(
    unregistered(
      [
        "/api/v1/workflows/${encode(workflowId)}/pattern-graph",
        "/api/v1/workflows/${encode(workflowId)}/eval-board",
        "/api/v1/workflows/${encode(workflowId)}/proposals",
      ],
      routes,
    ),
    [],
  );
});
