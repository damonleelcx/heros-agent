import "server-only";

/**
 * identity.ts is the tenant-identity SEAM, and deliberately nothing more (ADR-008).
 *
 * # Why this file is so small, and must stay so
 *
 * P7 has not named the tenant identity mechanism — hosted auth, OIDC against the customer's own IdP,
 * or both — and PRD §14 Q1 says explicitly that P9 must not pre-empt it. Choosing one here would be
 * picking a published contract with the customer's IT organization as a side effect of building a UI,
 * and the cost of being wrong is a migration for every existing tenant.
 *
 * So the whole contract is one function: `verify(assertion) → { tenantId }`. Everything above it —
 * the session, the cookie, the fail-closed routing, the scope derivation, the entitlement read — is
 * written against an abstract authenticated tenant principal and knows nothing about how the
 * assertion was proved. When P7 decides, this file changes and nothing above it does.
 *
 * # Three rules that bind every implementation, present and future
 *
 * 1. **The assertion is never persisted.** It is verified, exchanged for a session, and dropped. Not
 *    stored in the session record, not written to a cookie, not logged, not carried upstream.
 * 2. **The tenant is authoritative and server-side.** Nothing the client sends can widen it.
 * 3. **A development provider must not be able to run in production.** The guard below is the
 *    load-bearing part of the `dev` implementation, not a nicety.
 */

export type TenantPrincipal = { tenantId: string };

export type VerifyOutcome =
  | { ok: true; principal: TenantPrincipal }
  | { ok: false; reason: string };

/**
 * The provider in force. `configured` in any real deployment; `dev` for local work only.
 *
 * Read once at module load rather than per request: a deployment does not change its identity
 * provider between two requests, and reading it per request would make the production guard below
 * depend on the timing of the first sign-in.
 */
const PROVIDER = (process.env.CONSOLE_TENANT_IDENTITY ?? "configured").toLowerCase();
const IS_PRODUCTION = process.env.NODE_ENV === "production";

if (PROVIDER === "dev" && IS_PRODUCTION) {
  // Refuse to boot rather than refuse at sign-in. A process that starts and then rejects every login
  // looks like a broken deployment; a process that will not start says exactly what is wrong, once,
  // to the person doing the deploy.
  throw new Error(
    "CONSOLE_TENANT_IDENTITY=dev is a development-only identity provider and must not run in production",
  );
}

/**
 * configuredTenants parses the deployment's assertion → tenant map.
 *
 * This is the same shape the platform's own `auth.Registry` already uses to bind a credential to a
 * tenant; the console reads that existing model rather than inventing a second one. The map lives in
 * the environment (a secrets manager injects it), never in the repository.
 *
 * Format: `CONSOLE_TENANT_ASSERTIONS={"<assertion>":"<tenant_id>", …}`
 */
function configuredTenants(): Record<string, string> {
  const raw = process.env.CONSOLE_TENANT_ASSERTIONS;
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, string>;
    }
  } catch {
    // Deliberately silent about the VALUE and loud about the FACT: the map is credential material, so
    // a parse error must not print it, and a console whose identity map is unparseable must not
    // quietly authenticate nobody while looking healthy.
    throw new Error("CONSOLE_TENANT_ASSERTIONS is set but is not a JSON object of assertion → tenant");
  }
  throw new Error("CONSOLE_TENANT_ASSERTIONS is set but is not a JSON object of assertion → tenant");
}

/**
 * verifyTenantAssertion resolves an assertion to a tenant principal, or refuses.
 *
 * The refusal reason is deliberately generic and identical for "no such assertion" and "empty
 * assertion": distinguishing them tells an attacker which of the two they got wrong, and neither
 * distinction helps a real user, who knows whether they pasted something.
 */
export async function verifyTenantAssertion(assertion: string): Promise<VerifyOutcome> {
  const value = assertion.trim();
  if (!value) return { ok: false, reason: "that credential was not accepted" };

  if (PROVIDER === "dev") {
    // The development provider treats the assertion AS the tenant id, so a local console is runnable
    // with no platform credential in a browser and no identity infrastructure at all (README §13.3).
    // It is unreachable in production by the guard above.
    return { ok: true, principal: { tenantId: value } };
  }

  const map = configuredTenants();
  const tenantId = map[value];
  if (!tenantId) return { ok: false, reason: "that credential was not accepted" };
  return { ok: true, principal: { tenantId } };
}

/** identityProvider names the provider in force, for the health surface. Never a credential. */
export function identityProvider(): string {
  return PROVIDER;
}
