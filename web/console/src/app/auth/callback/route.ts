import { cookies } from "next/headers";
import { CONFIG } from "@/lib/idp/config";
import { consumeFlow, SIGNIN_FLOW_COOKIE, SIGNIN_FLOW_COOKIE_OPTIONS } from "@/lib/idp/flow";
import { exchangeCode, IdpUnreachableError } from "@/lib/idp/oidc";
import { SecretUnavailableError } from "@/lib/idp/secrets";
import { constantTimeEqual } from "@/lib/idp/jwt";
import { verifyTenantAssertion, type RefusalClass } from "@/lib/identity";
import { issueSession, sessionTtlSeconds } from "@/lib/session";
import { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "@/lib/cookies";
import { redirectTo, samePath } from "@/lib/redirect";
import { logIdentity } from "@/lib/telemetry";

/**
 * `/auth/callback` completes an OIDC sign-in (P22 tasks 3.1, 3.2, 5.1).
 *
 * # The order of checks IS the security property
 *
 * 1. **The flow cookie and the `state` must agree.** The cookie is the browser half, the `state` is
 *    the URL half, and the flow is consumed — deleted — before anything else happens. Consuming
 *    first means a callback that fails verification leaves no live `state` behind, so "single-use"
 *    means single USE and not single SUCCESS.
 * 2. **Then the code is exchanged**, with the PKCE verifier and the exact `redirect_uri` the flow
 *    began with. An intercepted code is worthless without the verifier this process never sent.
 * 3. **Then the ID token is verified** against the issuer's JWKS, bound to the flow's `nonce`, and
 *    mapped to a tenant — all inside the seam, which is the only thing that can produce a `tenantId`.
 * 4. **Then, and only then, a session is issued** by the same `issueSession` that shipped before P22,
 *    holding the same five fields. The ID token is not passed to it, not stored, and not logged; it
 *    goes out of scope at the end of this function and that is the whole of its lifetime (NFR2).
 *
 * # Every failure is a REDIRECT, never a rendered shell (task 3.2)
 *
 * The user gets a sign-in page with a `reason` they can act on — sign in / session ended / your IdP is
 * unreachable / you are not provisioned for this tenant — and never a broken console that 401s each
 * of its own fetches. The reason is one of three coarse classes (`identity.ts` `RefusalClass`); the
 * cause is a server-side event.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  if (CONFIG.kind !== "oidc") {
    // Not an oracle: which mechanism a deployment federates with is visible from its sign-in page.
    // Refusing here stops the OIDC callback from being a second entry point in a SAML deployment.
    return refuse("credential");
  }

  const url = new URL(request.url);
  const jar = await cookies();
  const flow = consumeFlow(jar.get(SIGNIN_FLOW_COOKIE)?.value);

  const state = url.searchParams.get("state") ?? "";
  if (!flow || !constantTimeEqual(state, flow.state)) {
    // Covers all of: no cookie, expired flow, a `state` from another browser, a replayed callback
    // whose flow was already consumed, and a forged callback with no `state` at all. One refusal for
    // the lot, because telling them apart is a probing oracle.
    logIdentity({ event: "replay_refused", provider: CONFIG.kind, cause: "callback state did not match a live browser-bound flow" });
    return refuse("credential");
  }

  // An IdP-reported error (`access_denied`, `login_required`, …) arrives here as a parameter. It is
  // NOT reflected back to the user: the value is attacker-controllable text on a page we render.
  const error = url.searchParams.get("error");
  if (error) {
    logIdentity({ event: "assertion_refused", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: "the identity provider returned an error response" });
    return refuse("credential");
  }

  const code = url.searchParams.get("code") ?? "";
  if (!code) {
    logIdentity({ event: "assertion_refused", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: "callback carried no authorization code" });
    return refuse("credential");
  }

  let idToken: string;
  try {
    idToken = await exchangeCode({ code, codeVerifier: flow.codeVerifier, redirectUri: flow.redirectUri });
  } catch (err) {
    if (err instanceof IdpUnreachableError) {
      logIdentity({ event: "idp_unreachable", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: err.message });
      return refuse("idp_unreachable");
    }
    if (err instanceof SecretUnavailableError) {
      logIdentity({ event: "secret_unavailable", provider: CONFIG.kind, cause: err.message });
      return refuse("idp_unreachable");
    }
    // A reused or forged code lands here. An ordinary refusal, not an outage.
    logIdentity({ event: "assertion_refused", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: "authorization code was not redeemable" });
    return refuse("credential");
  }

  const outcome = await verifyTenantAssertion(idToken, { kind: "oidc", nonce: flow.nonce });
  if (!outcome.ok) return refuse(outcome.refusal, flow.next);

  const { token } = issueSession(outcome.principal);
  // Relative destination, cookie set ON the response. Both are the lessons `lib/redirect.ts` and
  // `api/session/route.ts` already record — an immutable redirect cannot carry a `Set-Cookie`, and an
  // absolute URL built from `request.url` leaves this origin and is refused by our own CSP.
  const response = redirectTo(samePath(flow.next));
  response.cookies.set(SESSION_COOKIE, token, { ...SESSION_COOKIE_OPTIONS, maxAge: sessionTtlSeconds() });
  // The flow is spent. Clearing it stops a stale cookie from making the NEXT sign-in look replayed.
  response.cookies.set(SIGNIN_FLOW_COOKIE, "", { ...SIGNIN_FLOW_COOKIE_OPTIONS, maxAge: 0 });
  return response;
}

/** refuse redirects to sign-in with a reason, and always clears the spent flow cookie. */
function refuse(reason: RefusalClass, next?: string) {
  const target = next && next !== "/app" ? `/signin?reason=${reason}&next=${encodeURIComponent(next)}` : `/signin?reason=${reason}`;
  const response = redirectTo(target);
  response.cookies.set(SIGNIN_FLOW_COOKIE, "", { ...SIGNIN_FLOW_COOKIE_OPTIONS, maxAge: 0 });
  return response;
}
