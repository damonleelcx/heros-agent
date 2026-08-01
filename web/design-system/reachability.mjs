// reachability.mjs answers one question both consoles' bundle scans need and neither can answer by
// looking at a filename: **which client chunks does a browser download on a tenant or operator route.**
//
// # Why the question is not "is this runtime in the bundle"
//
// P24 permits an error-reporting runtime on every surface and refuses an analytics tag and a session
// recorder on tenant and operator surfaces (design D2, D3). A whole-bundle scan cannot express that:
// it would either fail the build for the permitted runtime or pass it for the refused one. The rule is
// about REACHABILITY, so the scan has to be too.
//
// # Why the build's own manifest and not a guess
//
// `app-build-manifest.json` is Next's own statement of which chunks each route entry loads, layouts
// included. Inferring it from chunk names is guesswork about a build tool's naming, and the failure is
// silent in the dangerous direction: a recorder that lands in a shared chunk is exactly the case a
// name-based heuristic misses, and exactly the case that matters.
//
// A consequence worth stating, because it is the point rather than a side effect: anything imported by
// the ROOT LAYOUT is reachable from every route, so a tag added there is tenant-reachable and fails the
// build. That is the accident this fence exists for — not a deliberate `/app/studio` import, which
// nobody would write, but a `<Script>` dropped into the layout that serves the whole application.

/**
 * normalizeRoute strips Next's route-group segments, which are organisational and carry no URL.
 *
 * `/(reading)/docs/page` and `/(public)/page` are `/docs` and `/` to a browser; a prefix test against
 * the raw key would classify both as neither public nor tenant and quietly protect nothing.
 */
export function normalizeRoute(key) {
  return (
    key
      .replace(/\/\([^)]*\)/g, "")
      .replace(/\/(page|route|layout|not-found|error|loading|template)$/, "") || "/"
  );
}

/** TENANT_PREFIXES are the customer console's non-public prefixes, in the artefact's own terms. */
const TENANT_PREFIXES = ["/app", "/api"];

/**
 * guardedChunks returns the chunks a browser downloads on a guarded route.
 *
 * "Guarded" is per console: on the customer console it is the tenant prefixes, because that console
 * deliberately serves a public surface too; on the operator console it is every route, because every
 * operator route renders cross-tenant material.
 *
 * @param {{pages: Record<string, string[]>}} manifest  Next's app-build-manifest.json
 * @param {"customer"|"operator"} consoleId
 * @returns {Set<string>} chunk paths, relative to `.next/`, e.g. `static/chunks/…js`
 */
export function guardedChunks(manifest, consoleId) {
  const out = new Set();
  for (const [key, chunks] of Object.entries(manifest.pages ?? {})) {
    const route = normalizeRoute(key);
    const guarded =
      consoleId === "operator" ||
      TENANT_PREFIXES.some((p) => route === p || route.startsWith(`${p}/`));
    if (!guarded) continue;
    for (const chunk of chunks) out.add(chunk);
  }
  return out;
}

/**
 * publicOnlyChunks returns the chunks reachable ONLY from an unguarded route.
 *
 * It exists so the scan can say so in its passing message. A fence that reports "nothing found" cannot
 * be told apart from a fence that looked at nothing, and this phase installs runtimes that are
 * SUPPOSED to appear in the public partition — so the partition has to be visible in the output.
 */
export function publicOnlyChunks(manifest, consoleId) {
  const guarded = guardedChunks(manifest, consoleId);
  const out = new Set();
  for (const chunks of Object.values(manifest.pages ?? {})) {
    for (const chunk of chunks) if (!guarded.has(chunk)) out.add(chunk);
  }
  return out;
}
