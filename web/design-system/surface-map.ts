/**
 * surface-map.ts turns a request path into an id from the closed surface enum.
 *
 * # 🔴 This function is the reason no event in this phase carries a URL
 *
 * A URL under `/app` carries variant, run, node and tenant identifiers. Every alternative to a closed
 * enum is a denylist over paths that gain segments every phase — and a denylist over a surface that
 * grows fails silently the first time somebody adds a page, which on this product means a tenant
 * identifier in a third party's logs.
 *
 * So the mapping is a TABLE, checked in, ordered longest-first, and it is total: a path nothing matches
 * resolves to the section's own landing id rather than to the path. There is no branch that returns
 * anything derived from the input string.
 *
 * # Why the table and not a set of `if`s at the call sites
 *
 * Two call sites need this — the customer console's layout and the operator console's — and a rule
 * enforced call site by call site fails the first time somebody adds a call site. It is also the only
 * form in which "here is the complete set of things a third party can be told about where a user was"
 * is a readable answer.
 */

import { type ConsoleId, type SurfaceId } from "./third-party-policy.ts";

/** A route pattern and the surface it resolves to. `prefix` matches the path or any child of it. */
type SurfaceRoute = { prefix: string; surface: SurfaceId };

/**
 * ROUTES is ordered LONGEST-PREFIX-FIRST within each console, and every list ends in a catch-all.
 *
 * A dynamic segment is deliberately NOT in the table: `/app/runs/anything` resolves to `tenant.run`
 * because the surface is "the run page", not "this run". That is the difference between an id and a URL.
 */
const ROUTES: Record<ConsoleId, readonly SurfaceRoute[]> = {
  customer: [
    { prefix: "/app/workflows", surface: "tenant.workflows" },
    { prefix: "/app/transforms", surface: "tenant.transforms" },
    { prefix: "/app/variants", surface: "tenant.variants" },
    { prefix: "/app/runs", surface: "tenant.runs" },
    { prefix: "/app/account", surface: "tenant.account" },
    { prefix: "/app/authoring", surface: "tenant.authoring" },
    { prefix: "/app/billing", surface: "tenant.billing" },
    { prefix: "/app/configure", surface: "tenant.configure" },
    { prefix: "/app/context", surface: "tenant.context" },
    { prefix: "/app/coverage", surface: "tenant.coverage" },
    { prefix: "/app/delivery", surface: "tenant.delivery" },
    { prefix: "/app/harness", surface: "tenant.harness" },
    { prefix: "/app/memory", surface: "tenant.memory" },
    { prefix: "/app/studio", surface: "tenant.studio" },
    { prefix: "/app/wiring", surface: "tenant.wiring" },
    { prefix: "/app", surface: "tenant.overview" },
    { prefix: "/install", surface: "public.install" },
    { prefix: "/docs", surface: "public.docs" },
    { prefix: "/legal", surface: "public.legal" },
    { prefix: "/signin", surface: "public.signin" },
    { prefix: "/preview", surface: "public.preview" },
    { prefix: "/", surface: "public.home" },
  ],
  operator: [
    { prefix: "/tenants", surface: "operator.tenants" },
    { prefix: "/audit", surface: "operator.audit" },
    { prefix: "/axes", surface: "operator.axes" },
    { prefix: "/billing", surface: "operator.billing" },
    { prefix: "/compliance", surface: "operator.compliance" },
    { prefix: "/crosstenant", surface: "operator.crosstenant" },
    { prefix: "/delivery", surface: "operator.delivery" },
    { prefix: "/fleet", surface: "operator.fleet" },
    { prefix: "/killswitch", surface: "operator.killswitch" },
    { prefix: "/oversight", surface: "operator.oversight" },
    { prefix: "/registry", surface: "operator.registry" },
    { prefix: "/releases", surface: "operator.releases" },
    { prefix: "/signin", surface: "operator.signin" },
    { prefix: "/", surface: "operator.overview" },
  ],
};

/**
 * resolveSurface returns the surface id governing `pathname`.
 *
 * The query string is discarded before matching, and never reaches the result — a query string is the
 * other half of what makes a URL an identifier.
 */
export function resolveSurface(consoleId: ConsoleId, pathname: string): SurfaceId {
  const path = (pathname || "/").split("?")[0].split("#")[0];
  for (const route of ROUTES[consoleId]) {
    if (route.prefix === "/") return route.surface;
    if (path === route.prefix || path.startsWith(`${route.prefix}/`)) return route.surface;
  }
  // Unreachable while every list ends in "/", and a hard failure rather than a permissive default if
  // somebody removes it: a surface with no id must not become a surface identified by its path.
  throw new Error(`no surface governs ${path} in the ${consoleId} console`);
}

/** surfaceRoutes exposes the table so a test can assert it covers every shipped route. */
export function surfaceRoutes(consoleId: ConsoleId): readonly SurfaceRoute[] {
  return ROUTES[consoleId];
}
