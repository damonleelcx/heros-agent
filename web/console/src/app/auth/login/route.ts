import { NextResponse } from "next/server";
import { CONFIG, canonicalCallback } from "@/lib/idp/config";
import { beginFlow, SIGNIN_FLOW_COOKIE, SIGNIN_FLOW_COOKIE_OPTIONS } from "@/lib/idp/flow";
import { MAX_FLOW_AGE_SECONDS } from "@/lib/idp/federation";
import { authorizationUrl, ensureReachable, IdpUnreachableError } from "@/lib/idp/oidc";
import { authnRequestUrl, ensureMetadata } from "@/lib/idp/saml";
import { SecretUnavailableError } from "@/lib/idp/secrets";
import { samePath, redirectTo } from "@/lib/redirect";
import { logIdentity } from "@/lib/telemetry";

/**
 * `/auth/login` begins a federated sign-in (P22 task 3.1).
 *
 * # Why this one is a GET when nothing else that sets a cookie is
 *
 * `tests/session-cookie.test.mjs` fails the build if an API route mutates on GET, because
 * `SameSite=Lax` sends cookies on cross-site top-level navigations and a mutating GET is then a
 * one-link CSRF. That rule is right and this route is a deliberate, reasoned exception rather than an
 * oversight, so the reasoning is here rather than in a commit message:
 *
 *  1. **A POST cannot work.** The response is a redirect to the IdP — a different origin — and this
 *     console serves `form-action 'self'`. Firefox applies `form-action` to the redirect chain of a
 *     form submission, so a POSTed sign-in would be blocked by our own CSP in a real browser while
 *     passing every server-side check. Changing the CSP is not available either: `middleware.ts` is
 *     above the seam and P22 does not touch it (ADR-008 Rule 3).
 *  2. **The cookie it sets is not a credential.** It carries an opaque flow id and authorises
 *     nothing. The PKCE verifier, the nonce and the `state` stay server-side.
 *  3. **A cross-site-triggered flow cannot be completed by the party that triggered it.** The
 *     victim's browser gets the cookie; the attacker's browser gets neither it nor the IdP session.
 *     The worst outcome is that a victim's own in-flight sign-in is replaced and they retry — which
 *     is why the flow record is single-use and ten minutes long rather than durable.
 *
 * # What the browser is trusted with, and what it is not
 *
 * `next` — where to land afterwards — is client input, and it is normalised to a same-origin path by
 * `samePath` BEFORE being stored. It is stored server-side on the flow rather than round-tripped
 * through the IdP, so the value that decides the final navigation is one this process wrote.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  if (CONFIG.kind !== "oidc" && CONFIG.kind !== "saml") {
    // An unfederated deployment has no IdP to send anyone to. Redirecting to the credential form is
    // the honest answer — and it is a REDIRECT, never a rendered shell (the shell rule, task 3.2).
    return redirectTo("/signin?reason=no_session");
  }

  const url = new URL(request.url);
  const next = samePath(url.searchParams.get("next"));
  const redirectUri = canonicalCallback();
  const { id, flow } = beginFlow({ next, redirectUri });

  let authorize: string;
  try {
    // Reachability is confirmed BEFORE the redirect, against the live IdP rather than against a warm
    // metadata cache. Without this the flow begins happily against an IdP that died four minutes ago
    // and the user is dropped on a dead host — see `oidc.ensureReachable` for the whole argument.
    if (CONFIG.kind === "oidc") await ensureReachable();
    else await ensureMetadata();
    authorize = CONFIG.kind === "oidc" ? await authorizationUrl(flow) : await authnRequestUrl(flow);
  } catch (err) {
    // Fail closed at the FIRST hop, not at the callback (Decision 8). A console that cheerfully
    // redirects to an IdP it cannot reach hands the user a broken page on somebody else's domain and
    // no way to tell whose fault it is.
    if (err instanceof IdpUnreachableError) {
      logIdentity({ event: "idp_unreachable", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: err.message });
    } else if (err instanceof SecretUnavailableError) {
      logIdentity({ event: "secret_unavailable", provider: CONFIG.kind, cause: err.message });
    } else {
      logIdentity({ event: "idp_unreachable", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: err instanceof Error ? err.message : "could not begin sign-in" });
    }
    return redirectTo("/signin?reason=idp_unreachable");
  }

  // The ONE off-origin redirect in this console, and the only place `redirectTo` is deliberately not
  // used. The destination is not client input and not reconstructed from a header: it is the
  // authorization endpoint the IdP's own discovery document (OIDC) or metadata (SAML) declared, and
  // both are refused unless the document's `issuer`/`entityID` equals the configured one. So the
  // target is pinned to the trust anchor an operator configured, not to anything a request said.
  const response = new NextResponse(null, {
    status: 303,
    headers: { location: authorize, "cache-control": "no-store" },
  });
  response.cookies.set(SIGNIN_FLOW_COOKIE, id, { ...SIGNIN_FLOW_COOKIE_OPTIONS, maxAge: MAX_FLOW_AGE_SECONDS });
  return response;
}
