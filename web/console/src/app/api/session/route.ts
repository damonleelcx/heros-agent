import { NextResponse } from "next/server";
import { verifyTenantAssertion } from "@/lib/identity";
import { issueSession, readSessionToken, revokeSession, sessionTtlSeconds } from "@/lib/session";
import { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "@/lib/cookies";
import { redirectTo, samePath } from "@/lib/redirect";
import { logSession } from "@/lib/telemetry";

/**
 * The session lifecycle: POST signs in, DELETE signs out.
 *
 * # Why this is a route handler and the sign-in form posts to it natively
 *
 * The sign-in page is a plain `<form method="post" action="/api/session">` with **no client
 * JavaScript at all**. Three things follow, and each is worth more than the interactivity given up:
 *
 * 1. **Sign-in works before hydration, and without it.** The one surface a user cannot get past is the
 *    worst place to depend on a script having loaded — and this console's CSP is strict enough that a
 *    bootstrap failure is a real scenario, not a hypothetical.
 * 2. **No client component ever handles the credential.** There is no component holding it in state,
 *    no re-render carrying it, and nothing for a future refactor to accidentally persist.
 * 3. **It is exercisable end to end** by anything that can post a form — which is what makes the
 *    security assertions in `tests/security.test.mjs` a real test of the real path rather than a call
 *    to a function the page may or may not use.
 *
 * # What crosses which boundary (FR1, ADR-008)
 *
 * The assertion arrives once, in a POST body, over the console's own origin. It is verified, exchanged
 * for a session, and **dropped** — never stored in the session record, never written to a cookie,
 * never logged, never carried upstream. The browser keeps an opaque token in an `HttpOnly` cookie it
 * cannot read. The platform credential is not involved in this exchange at all: it is the BFF's own,
 * and it is used only for upstream calls.
 *
 * ⚠️ This said `SameSite=Strict`, and the cookie is `SameSite=Lax` — it has been since `strict` was
 * found to break the return from Stripe Checkout (the customer's card was charged and the product
 * answered with a sign-in page). `lib/cookies.ts` carries that whole argument beside the flag. The
 * sentence is corrected rather than deleted because a security-critical docstring overstating a flag is
 * worse than one that omits it: an auditor reading `Strict` here concludes a CSRF property this app
 * does not have, and stops looking for the thing that actually provides it — which is that no mutating
 * route in this app is a GET, enforced by `tests/session-cookie.test.mjs`.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const form = await request.formData().catch(() => null);
  const assertion = String(form?.get("assertion") ?? "");
  const next = samePath(form ? String(form.get("next") ?? "") || null : null);

  const outcome = await verifyTenantAssertion(assertion);
  if (!outcome.ok) {
    logSession({ action: "denied", reason: "assertion_rejected" });
    // Back to the form with a reason code, not the value that failed. The message is deliberately the
    // same for an empty assertion and an unknown one: distinguishing them tells an attacker which half
    // they got wrong, and helps a real user not at all — they know whether they pasted something.
    const back = next === "/app" ? "/signin?reason=rejected" : `/signin?reason=rejected&next=${encodeURIComponent(next)}`;
    return redirectTo(back);
  }

  const { token } = issueSession(outcome.principal);
  // Two things this line gets right that the obvious version does not, both found by clicking the
  // button in a real browser rather than by any check that passes on a build (R11):
  //
  //   1. The cookie is set ON THE RESPONSE. `Response.redirect()` is immutable, so Next cannot attach
  //      a `Set-Cookie` to it and the browser receives a redirect with no session.
  //   2. The destination is RELATIVE (see lib/redirect.ts). An absolute URL built from `request.url`
  //      resolves to the server's own notion of its host, which differs from the one the user typed —
  //      and the console's own `form-action 'self'` policy then refuses the navigation.
  const response = redirectTo(next);
  response.cookies.set(SESSION_COOKIE, token, { ...SESSION_COOKIE_OPTIONS, maxAge: sessionTtlSeconds() });
  return response;
}

/**
 * Sign-out: revoke server-side, then clear the cookie — in that order (FR3).
 *
 * The order is the requirement. Clearing the cookie alone would leave a live session that anybody
 * holding the token could still use; revoking first means the token is dead before the browser is told
 * to forget it, so a copy taken in between is worth nothing. Revocation is effective at the **next**
 * request because `resolveSession` reads the store on every read — there is no cached "was valid a
 * moment ago" path, and a grace period is precisely how long a compromised session outlives its
 * revocation.
 */
export async function DELETE() {
  const token = await readSessionToken();
  revokeSession(token);
  const response = NextResponse.json({ ok: true }, { headers: { "cache-control": "no-store" } });
  response.cookies.set(SESSION_COOKIE, "", { ...SESSION_COOKIE_OPTIONS, maxAge: 0 });
  return response;
}
