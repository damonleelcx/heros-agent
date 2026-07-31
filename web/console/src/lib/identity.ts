import "server-only";
import { CONFIG, type IdentityProviderKind } from "./idp/config";
import { REFUSAL, resolveTenant, type FederatedClaims } from "./idp/federation";
import { spendAssertion } from "./idp/flow";
import { verifyIdToken, reachable as oidcReachable, IdpUnreachableError } from "./idp/oidc";
import { verifySamlResponse, reachableMetadata } from "./idp/saml";
import { SecretUnavailableError, describeSecrets } from "./idp/secrets";
import { logIdentity } from "./telemetry";

/**
 * identity.ts is the tenant-identity SEAM, and deliberately nothing more (ADR-008).
 *
 * # Why this file is so small, and must stay so
 *
 * The whole contract is one function: `verify(assertion) → { tenantId }`. Everything above it — the
 * session, the cookie, the fail-closed routing, the scope derivation, the entitlement read — is
 * written against an abstract authenticated tenant principal and knows nothing about how the
 * assertion was proved.
 *
 * ADR-008 deferred the mechanism on purpose, because choosing OIDC/SAML is a published contract with
 * the customer's IT organization whose cost, if wrong, is a migration for every tenant. **P22 is that
 * later.** What changed is this file and the `/auth/*` routes around it. What did NOT change is
 * anything above the seam: `session.ts`, `cookies.ts`, `middleware.ts` and `scope.ts` are byte-for-byte
 * what they were, and that is asserted by `tests/sso-identity.test.mjs`, not assumed.
 *
 * # Three rules that bind every implementation, present and future
 *
 * 1. **The assertion is never persisted.** It is verified, exchanged for a session, and dropped. Not
 *    stored in the session record, not written to a cookie, not logged, not carried upstream. Note
 *    what that means for the code below: the ID token and the SAML response reach this module as
 *    arguments, are read once, and are never returned, stored, or handed to `logIdentity` — which has
 *    no parameter that could carry them.
 * 2. **The tenant is authoritative and server-side.** Nothing the client sends can widen it. The only
 *    thing that produces a `tenantId` is `resolveTenant`, and its only inputs are the injected mapping
 *    and claims from a verified assertion.
 * 3. **A development provider must not be able to run in production.** The guard below is the
 *    load-bearing part of the `dev` implementation, not a nicety.
 *
 * # The four provider kinds are ONE seam
 *
 * `oidc` and `saml` are the federated mechanisms (Decision 2). `configured` is the deployment-injected
 * assertion → tenant map that shipped before P22 and still serves an open-core deployment that
 * federates against nothing. `dev` is local-only. All four answer the same question and none of them
 * is visible above this file.
 */

export type TenantPrincipal = { tenantId: string };

/**
 * RefusalClass is the COARSE reason a sign-in did not happen. Exactly three values, and the split is
 * deliberate rather than convenient.
 *
 * - `credential` — every verification failure: unknown issuer, bad signature, wrong audience, stale or
 *   replayed assertion, missing binding. One class for all of them, because distinguishing them is the
 *   probing oracle Decision 9 refuses. The user is told "that sign-in was not accepted" and nothing
 *   more.
 * - `not_provisioned` — the assertion VERIFIED and then matched no mapping rule. Distinguishable
 *   because it is not an oracle: reaching it at all requires a signature from the registered IdP, and
 *   an attacker who can mint one is past the point where this distinction helps them. Meanwhile it is
 *   the single most useful thing the system can tell a real person, who is otherwise left retrying a
 *   sign-in that will never work.
 * - `idp_unreachable` — infrastructure, not identity. Nobody's credential was rejected; the IdP could
 *   not be reached, or an identity secret could not be sourced. Saying "sign-in was not accepted"
 *   here would send a user to their IT department over our outage.
 *
 * The class never names the mechanism, the secret's logical name, the provider kind, or the issuer —
 * that is `logIdentity`'s job, server-side (task 9.1).
 */
export type RefusalClass = "credential" | "not_provisioned" | "idp_unreachable";

export type VerifyOutcome =
  | { ok: true; principal: TenantPrincipal }
  | { ok: false; reason: string; refusal: RefusalClass };

/**
 * AssertionBinding is what an assertion must be bound TO for the verification to mean anything.
 *
 * # Why the seam takes a second, optional argument
 *
 * `verify(assertion) → { tenantId }` is unchanged: the input is still one assertion and the output is
 * still one tenant. What the redirect flows add is the *binding* — the `nonce` that ties an ID token
 * to the browser that began this flow, the `InResponseTo` and ACS that tie a SAML response to it. That
 * material cannot live in the assertion (the IdP writes the assertion) and must not live in a module
 * global (two concurrent sign-ins would share it), so it arrives alongside.
 *
 * It is optional because `configured` and `dev` have no redirect flow and therefore no binding — and
 * because an optional parameter keeps the pre-P22 call in `api/session/route.ts` compiling unchanged,
 * which is itself part of "nothing above the seam moved".
 */
export type AssertionBinding =
  | { kind: "oidc"; nonce: string }
  | { kind: "saml"; acsUrl: string; inResponseTo: string };

const PROVIDER: IdentityProviderKind = CONFIG.kind;
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
 * The refusal reason is deliberately generic and identical for every cause: distinguishing "no such
 * assertion" from "bad signature" from "unmapped identity" tells an attacker which of them they got
 * wrong, and none of the distinctions helps a real user. The CAUSE goes to `logIdentity`, server-side,
 * where an operator can read it.
 */
export async function verifyTenantAssertion(assertion: string, binding?: AssertionBinding): Promise<VerifyOutcome> {
  const value = assertion.trim();
  if (!value) return refuse("credential");

  switch (PROVIDER) {
    case "dev":
      // The development provider treats the assertion AS the tenant id, so a local console is runnable
      // with no platform credential in a browser and no identity infrastructure at all (README §13.3).
      // It is unreachable in production by the guard above.
      return { ok: true, principal: { tenantId: value } };

    case "configured": {
      const map = configuredTenants();
      const tenantId = map[value];
      if (!tenantId) {
        logIdentity({ event: "assertion_refused", provider: PROVIDER, cause: "assertion is not in the configured map" });
        return refuse("credential");
      }
      return { ok: true, principal: { tenantId } };
    }

    case "oidc": {
      // A federated deployment has no credential form. An assertion arriving without a binding did not
      // come through `/auth/callback`, and honouring it would be a second, unbound sign-in path.
      if (binding?.kind !== "oidc") {
        logIdentity({ event: "assertion_refused", provider: PROVIDER, cause: "assertion presented outside the OIDC callback" });
        return refuse("credential");
      }
      try {
        const verified = await verifyIdToken(value, binding.nonce);
        if (!verified.ok) {
          logIdentity({ event: "assertion_refused", provider: PROVIDER, issuer: CONFIG.issuer, cause: verified.cause });
          return refuse("credential");
        }
        return mapToTenant(verified.claims);
      } catch (err) {
        return failClosed(err);
      }
    }

    case "saml": {
      if (binding?.kind !== "saml") {
        logIdentity({ event: "assertion_refused", provider: PROVIDER, cause: "assertion presented outside the SAML ACS" });
        return refuse("credential");
      }
      try {
        const verified = await verifySamlResponse(value, {
          acsUrl: binding.acsUrl,
          expectedInResponseTo: binding.inResponseTo,
        });
        if (!verified.ok) {
          logIdentity({ event: "assertion_refused", provider: PROVIDER, issuer: CONFIG.issuer, cause: verified.cause });
          return refuse("credential");
        }
        return mapToTenant(verified.claims);
      } catch (err) {
        return failClosed(err);
      }
    }
  }
}

/**
 * mapToTenant is the one place a `tenantId` comes into existence, and the one-time replay guard.
 *
 * The order is the security property: the assertion is SPENT before it is mapped, so a response that
 * fails mapping still cannot be retried, and two concurrent replays cannot both win a race to the
 * mapping step.
 */
function mapToTenant(claims: FederatedClaims): VerifyOutcome {
  if (!CONFIG.tenantMap) {
    // Unreachable in a federated deployment — `config.ts` refuses to boot without a map — and a
    // refusal rather than a throw so a future kind cannot turn a missing map into an exception page.
    logIdentity({ event: "assertion_refused", provider: PROVIDER, issuer: claims.issuer, cause: "no tenant mapping is configured" });
    return refuse("credential");
  }
  if (!spendAssertion(claims.assertionId)) {
    logIdentity({ event: "replay_refused", provider: PROVIDER, issuer: claims.issuer, cause: "assertion has already been used" });
    return refuse("credential");
  }
  const resolved = resolveTenant(CONFIG.tenantMap, claims);
  if (!resolved.ok) {
    // NFR9. An identity matching no rule is a SECURITY EVENT, not a signup: no tenant is created, no
    // session is issued, and the operator gets a line naming the issuer that presented it.
    logIdentity({ event: "unmapped_identity", provider: PROVIDER, issuer: claims.issuer, cause: resolved.cause });
    return refuse("not_provisioned");
  }
  if (resolved.provisioned) {
    logIdentity({ event: "jit_provisioned", provider: PROVIDER, issuer: claims.issuer, tenantId: resolved.tenantId });
  }
  logIdentity({ event: "sign_in", provider: PROVIDER, issuer: claims.issuer, tenantId: resolved.tenantId });
  return { ok: true, principal: { tenantId: resolved.tenantId } };
}

/**
 * failClosed turns an infrastructure failure into a refusal, loudly.
 *
 * An unreachable IdP or an unavailable secret issues NO session and falls back to nothing — not to a
 * cached credential, not to `configured`, not to a weaker mechanism (Decision 8, no silent fallback). The
 * distinction from an ordinary refusal exists only in the log: the user sees the same generic reason,
 * while the operator gets an event a monitor can page on.
 */
function failClosed(err: unknown): VerifyOutcome {
  if (err instanceof IdpUnreachableError) {
    logIdentity({ event: "idp_unreachable", provider: PROVIDER, issuer: CONFIG.issuer, cause: err.message });
    return refuse("idp_unreachable");
  }
  if (err instanceof SecretUnavailableError) {
    // An unavailable identity secret is an outage of OURS, not a rejected credential — and it is the
    // fail-closed path: no session, no weaker mechanism, no cached credential.
    logIdentity({ event: "secret_unavailable", provider: PROVIDER, cause: err.message });
    return refuse("idp_unreachable");
  }
  logIdentity({ event: "assertion_refused", provider: PROVIDER, issuer: CONFIG.issuer, cause: err instanceof Error ? err.message : "verification failed" });
  return refuse("credential");
}

/**
 * refuse is the ONE constructor of a negative outcome.
 *
 * Every refusal carries the same `reason` string; only the coarse class varies. Funnelling them
 * through one function is what stops a future call site from inventing a more helpful message and
 * building the oracle back.
 */
function refuse(refusal: RefusalClass): VerifyOutcome {
  return { ok: false, reason: REFUSAL, refusal };
}

/** identityProvider names the provider in force, for the health surface. Never a credential. */
export function identityProvider(): string {
  return PROVIDER;
}

/**
 * IdentityHealth is the `/readyz → identity_provider` shape (task 7.1). It names the DOOR — kind,
 * issuer, reachability — and never anything behind it: no client id, no secret, no allowlist.
 */
export type IdentityHealth = { kind: string; issuer: string; reachable: boolean; detail?: string };

/**
 * identityHealth probes the IdP for the readiness surface.
 *
 * `configured` and `dev` federate with nobody, so there is nothing to be unreachable: they report
 * `reachable: true` because the statement is "this console's identity mechanism is serviceable", and
 * for a static map it always is. Reporting `false` there would page an operator about a dependency
 * the deployment does not have.
 */
export async function identityHealth(): Promise<IdentityHealth> {
  if (PROVIDER === "oidc") {
    const probe = await oidcReachable();
    return { kind: PROVIDER, issuer: CONFIG.issuer, ...probe };
  }
  if (PROVIDER === "saml") {
    const probe = await reachableMetadata();
    return { kind: PROVIDER, issuer: CONFIG.issuer, ...probe };
  }
  return { kind: PROVIDER, issuer: "", reachable: true };
}

/** identitySecretsSource names where identity credentials come from, for the health surface. */
export function identitySecretsSource(): { kind: string; detail: string } {
  return describeSecrets();
}
