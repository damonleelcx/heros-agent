import "server-only";
import { CONFIG, type IdentityProviderKind } from "./idp/config";
import { REFUSAL, resolveTenant, type FederatedClaims } from "./idp/federation";
import { spendAssertion } from "./idp/flow";
import { verifyIdToken, reachable as oidcReachable, IdpUnreachableError } from "./idp/oidc";
import { verifyPlatformToken, reachable as platformReachable } from "./idp/platformToken";
import { verifyPassword, reachable as passwordReachable, type PasswordOutcome } from "./idp/password";
import { platformApiBase, platformFetch } from "./platformApi";
import { verifySamlResponse, reachableMetadata } from "./idp/saml";
import { SecretUnavailableError, describeSecrets } from "./idp/secrets";
import { sessionStore } from "./sessionStore";
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
 * # The provider kinds are ONE seam
 *
 * `oidc` and `saml` are the federated mechanisms (Decision 2). `configured` is the deployment-injected
 * assertion → tenant map that shipped before P22 and still serves an open-core deployment that
 * federates against nothing. `platform` takes the token `heros login` stores. `password` (P28) is an
 * email address and a password. `dev` is local-only. All of them answer the same question and none of
 * them is visible above this file.
 *
 * # 🔴 `password` adds a SECOND ENTRY POINT, and that is the only shape change to the seam
 *
 * `verify(assertion) → {tenantId}` is unchanged and every existing caller compiles untouched. What P28
 * adds beside it is `verifyPasswordCredentials(email, password)`, because a two-field credential cannot
 * be carried by a one-string assertion without inventing an encoding — and an encoding is a wire contract
 * nobody asked for, on the one input where getting it wrong is a sign-in that silently accepts the wrong
 * split. Everything ABOVE the seam still sees one abstract authenticated principal.
 */

export type TenantPrincipal = {
  tenantId: string;
  /**
   * userId is the PERSON, where the platform can name one.
   *
   * 🔴 ADR-008 recorded that the session holds a tenant and not a user "because the platform cannot
   * currently prove one". P22 made that false — a verified assertion yields `sub@issuer` — and P27 is
   * where the field appears. It is optional because the `configured` and `dev` seams resolve an
   * organization and no person, and inventing one for them would be worse than the gap.
   */
  userId?: string;
};

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

/**
 * IS_BUILD is true while `next build` collects page data.
 *
 * 🔴 The configuration guards below must not run then, and the reason is not convenience. At build time
 * there is no deployment: no assertion map, no session-store choice, no identity provider — Next imports
 * every route module to collect its data, and a guard that asserts a DEPLOYMENT's configuration would fail
 * the build of an image that is perfectly capable of running correctly once configured.
 *
 * It cost two broken builds to find, both times with the guard itself being right. The check belongs at
 * startup and at request time, where a configuration exists to be wrong.
 */
const IS_BUILD = process.env.NEXT_PHASE === "phase-production-build";

/*
 * 🔴 A `configured` deployment with no map authenticates NOBODY, and used to do so silently.
 *
 * The guard lived in `deploy/docker-compose.console.yml` as `${CONSOLE_TENANT_ASSERTIONS:?}` — which is
 * the right check on the wrong substrate twice over: it does not exist on Kubernetes, and it fires for
 * every kind including the ones that need no map at all. P28 is where the second half bites, because a
 * `password` deployment has no assertions to declare and would have been forced to invent an empty one.
 *
 * So the check moves here, where it can be kind-aware and where it holds on every substrate. Refusing to
 * BOOT rather than at sign-in, for the reason the `dev` guard below gives: a process that starts and then
 * rejects every login looks like a broken deployment, and one that will not start says exactly what is
 * wrong, once, to the person doing the deploy.
 */
if (!IS_BUILD && PROVIDER === "configured" && Object.keys(configuredTenants()).length === 0) {
  throw new Error(
    "CONSOLE_TENANT_IDENTITY=configured needs CONSOLE_TENANT_ASSERTIONS — an empty map authenticates nobody, " +
      "and a console that starts and refuses every sign-in is indistinguishable from a broken deployment",
  );
}

/*
 * 🔴 The `password` seam requires a DURABLE session store, and refuses to boot without one.
 *
 * The sign-in page tells every reader: *"Sessions are server-side records, read on every request. When one
 * is revoked — by signing out, by a password reset, or by an owner removing you — the very next request is
 * denied, with no grace period."*
 *
 * On `CONSOLE_SESSION_STORE=memory` — the DEFAULT — that sentence is false in its most important clause. A
 * memory session is a `Map` in this process. A password reset runs on the PLATFORM: it revokes the
 * platform's sessions and every personal credential, and it cannot reach a map inside the console. So the
 * browser that was signed in stays signed in until the cookie expires — and the single commonest reason to
 * reset a password is that somebody else has the device it is signed in on.
 *
 * The other two clauses survive on memory (sign-out is this process revoking its own record), which is
 * exactly what makes the gap dangerous: two thirds of the sentence keep working, so nothing looks broken.
 *
 * Three ways out were possible. Weakening the copy would ship a product whose password reset does not do
 * what a reader reasonably expects. Making the copy conditional would put a security claim in two versions
 * and let a deployment pick the weak one silently. Refusing the combination makes the claim true wherever
 * it is rendered, which is the only one of the three that is a property rather than a description.
 *
 * ⚠️ WHEN this fires, stated precisely, because "refuses to boot" is easy to write and hard to earn.
 *
 * Next lazy-loads route modules, so a module-scope throw is not literally a startup failure. In practice it
 * is the next thing to it: `/api/health` imports this file to report the identity provider, so the guard
 * runs on the FIRST READINESS PROBE, the probe never returns 200, and the console never becomes ready. A
 * deployment on a bad combination does not serve — it fails its health check, which is what an orchestrator
 * and an operator both act on.
 *
 * That is the observed behaviour, not the intended one: `tests/password-identity.test.mjs` asserts it by
 * starting a console on the bad combination and requiring that it never comes up. If the health route ever
 * stops reading identity, this degrades to "the first sign-in fails" — still fail-closed, no longer
 * self-announcing — and that test is what would notice.
 *
 * A real boot hook was tried — `src/instrumentation.ts` importing this module — and it breaks the build:
 * Next compiles instrumentation for the edge runtime too, and this module reaches Node builtins through the
 * crypto and secrets seams. A broken build is worse than a guard that announces itself through the health
 * check, so the hook was removed and this paragraph exists instead of a comment that is not true.
 */
if (!IS_BUILD && PROVIDER === "password" && sessionStore.kind === "memory") {
  throw new Error(
    "CONSOLE_TENANT_IDENTITY=password requires CONSOLE_SESSION_STORE=platform. With the in-memory store a " +
      "password reset cannot revoke this console's own session cookie — it lives in this process, and the " +
      "reset happens on the platform — so a browser signed in on a lost or stolen device stays signed in " +
      "until the cookie expires, while the sign-in page promises the opposite.",
  );
}

if (!IS_BUILD && PROVIDER === "dev" && IS_PRODUCTION) {
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
      const userId = await resolveSeamPerson("configured", value, tenantId);
      return { ok: true, principal: userId ? { tenantId, userId } : { tenantId } };
    }

    case "password":
      // 🔴 A `password` deployment has no assertion form. Anything arriving here is a one-string credential
      // presented to a seam that takes two fields — a stale bookmark, a scripted POST, or a bug — and
      // honouring it would be a second, weaker way in beside `verifyPasswordCredentials`. Refused with the
      // same generic reason as everything else: whoever sent it learns nothing.
      logIdentity({ event: "assertion_refused", provider: PROVIDER, cause: "an assertion was presented to the password seam" });
      return refuse("credential");

    case "platform": {
      // The credential IS the platform token, so the platform is asked whose it is rather than a second
      // map being consulted. This is the seam that makes `heros login` and console sign-in one act
      // instead of two secrets — see idp/platformToken.ts for why it is not an escalation.
      const outcome = await verifyPlatformToken(value);
      if (!outcome.ok) {
        logIdentity({ event: "assertion_refused", provider: PROVIDER, cause: outcome.cause });
        return refuse("credential");
      }
      // 🔴 The platform ALREADY knows who this token belongs to — a personal credential names a person
      // and a machine credential names none — so this seam asks rather than inventing. That is also what
      // makes `heros login`'s token carry a person into the console.
      return {
        ok: true,
        principal: outcome.userId
          ? { tenantId: outcome.tenantId, userId: outcome.userId }
          : { tenantId: outcome.tenantId },
      };
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
        return await mapToTenant(verified.claims);
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
        return await mapToTenant(verified.claims);
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
async function mapToTenant(claims: FederatedClaims): Promise<VerifyOutcome> {
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

  /*
   * 🔴 The PERSON, resolved last and never allowed to fail the sign-in.
   *
   * ADR-008 recorded that the session holds a tenant and not a user "because the platform cannot
   * currently prove one". P22 made that false. This is where the proof becomes an id: the platform
   * upserts the person against the verified `(issuer, subject)` pair and ensures they are a member of
   * the organization the mapping above already placed them in.
   *
   * It does not gate the sign-in. A platform that cannot answer leaves `userId` ABSENT — which is
   * exactly what a machine principal looks like, and exactly what this console did for every session
   * before P27 — rather than turning an identity outage into a refused sign-in for somebody whose
   * assertion verified. The consequence is stated rather than hidden: per-user attribution is missing
   * for that session, and the surfaces that need a member refuse with a reason.
   */
  const userId = await resolvePerson(claims, resolved.tenantId);
  return { ok: true, principal: userId ? { tenantId: resolved.tenantId, userId } : { tenantId: resolved.tenantId } };
}

/**
 * resolveSeamPerson resolves a person for the seams that prove an ORGANIZATION rather than an individual.
 *
 * `dev` and `configured` map an assertion to an organization and know no name behind it. That is honest,
 * and it left every member-scoped surface refusing their readers with "you are not a member" — which is
 * true and useless. So the assertion itself becomes the subject: one stable person per assertion, which
 * is precisely what those seams model.
 *
 * It never fails a sign-in. An unreachable platform leaves `userId` absent, which is the same state a
 * machine principal is in and the state every session was in before P27.
 */
async function resolveSeamPerson(seam: string, assertion: string, tenantId: string): Promise<string | undefined> {
  try {
    const outcome = await platformFetch<{ user_id?: string }>("/api/v1/users/resolve", {
      tenantId,
      method: "POST",
      body: {
        // The ISSUER is the seam, not a URL: there is no identity provider here, and pretending there
        // is one would put a fabricated issuer on a person's permanent identity.
        issuer: `console:${seam}`,
        subject: assertion,
        email: "",
        tenant_id: tenantId,
      },
    });
    return outcome.ok ? outcome.data.user_id : undefined;
  } catch {
    return undefined;
  }
}

/**
 * resolvePerson asks the platform for the internal id of a verified identity, and for a membership in
 * the organization the mapping resolved.
 *
 * Returns undefined on any failure, deliberately — see the call site.
 */
async function resolvePerson(claims: FederatedClaims, tenantId: string): Promise<string | undefined> {
  try {
    const outcome = await platformFetch<{ user_id?: string; member?: boolean }>("/api/v1/users/resolve", {
      tenantId,
      method: "POST",
      body: {
        issuer: claims.issuer,
        subject: claims.subject,
        email: claims.email ?? "",
        tenant_id: tenantId,
      },
    });
    if (!outcome.ok || !outcome.data.user_id) return undefined;
    return outcome.data.user_id;
  } catch {
    return undefined;
  }
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
  if (PROVIDER === "password") {
    // The platform verifies the password, so its reachability IS this seam's health — the same reasoning
    // `platform` uses below. A deployment whose platform is down cannot sign anybody in, and reporting a
    // hard-coded `true` would be a component that cannot fail.
    const probe = await passwordReachable();
    return { kind: PROVIDER, issuer: platformApiBase(), ...probe };
  }
  if (PROVIDER === "oidc") {
    const probe = await oidcReachable();
    return { kind: PROVIDER, issuer: CONFIG.issuer, ...probe };
  }
  if (PROVIDER === "saml") {
    const probe = await reachableMetadata();
    return { kind: PROVIDER, issuer: CONFIG.issuer, ...probe };
  }
  if (PROVIDER === "platform") {
    // The platform is this seam's authority, so its reachability is this seam's health. Reporting a
    // hard-coded `true` here would be a component that cannot fail — see platformToken.reachable.
    const probe = await platformReachable();
    return { kind: PROVIDER, issuer: platformApiBase(), ...probe };
  }
  return { kind: PROVIDER, issuer: "", reachable: true };
}

/**
 * verifyPasswordCredentials is the `password` seam's entry point.
 *
 * 🔴 It refuses on every OTHER kind rather than falling through to something weaker. A deployment
 * federating with Okta must not also accept a password, because two doors and one lock is how a
 * revocation that works on one path stops meaning anything — the same reasoning `verifyTenantAssertion`
 * uses to refuse an OIDC assertion presented outside the callback.
 */
export async function verifyPasswordCredentials(email: string, plaintext: string): Promise<PasswordOutcome> {
  if (PROVIDER !== "password") {
    logIdentity({ event: "assertion_refused", provider: PROVIDER, cause: "a password was presented to a seam that does not accept one" });
    return { ok: false, reason: REFUSAL, reasonCode: "" };
  }
  return verifyPassword(email, plaintext);
}

/** passwordSignInEnabled reports whether this deployment offers email-and-password sign-in. */
export function passwordSignInEnabled(): boolean {
  return PROVIDER === "password";
}

/** identitySecretsSource names where identity credentials come from, for the health surface. */
export function identitySecretsSource(): { kind: string; detail: string } {
  return describeSecrets();
}
