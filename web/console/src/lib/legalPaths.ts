/**
 * legalPaths.ts holds the consent surface's platform paths.
 *
 * # 🔴 Why this is not in `scope.ts`, where every other platform path lives
 *
 * `scope.ts` is TENANT SCOPING: every path there closes over the session's tenant so no caller can
 * supply a different one. The consent path has no tenant in it at all — the platform reads the tenant
 * and the principal from the authenticated session on its own side — so putting it there would have
 * been filing a path in the module that exists to do the one thing this path does not need.
 *
 * That is not the only reason, and the other one is stronger. `scope.ts` is pinned byte-for-byte by
 * `tests/sso-identity.test.mjs` as one of the four files above the ADR-008 seam: the session store, the
 * cookie, scope derivation and the fail-closed middleware. The fence's own message says a change there
 * "is an ADR-008 conversation, not a hash update".
 *
 * P23 adding a route is not an ADR-008 conversation. It is a new module — and the fence catching the
 * first attempt is the fence working, not an obstacle routed around.
 */

/**
 * legalAcceptances is the consent endpoint.
 *
 * No parameter, deliberately. There is nothing here a caller could widen, because there is nothing here
 * to widen — which is a stronger guarantee than a correctly-scoped parameter.
 */
export function legalAcceptances(): string {
  return "/api/v1/legal/acceptances";
}
