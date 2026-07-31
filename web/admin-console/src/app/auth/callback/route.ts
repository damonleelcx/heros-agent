import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import {
  consumeFlow, constantTimeEqual, beginPending,
  FLOW_COOKIE, FLOW_COOKIE_OPTIONS, PENDING_COOKIE, PENDING_COOKIE_OPTIONS,
} from "@/lib/idpFlow";
import { callbackURL, isFederated } from "@/lib/idpConfig";

/**
 * `/auth/callback` completes the operator's federated sign-in.
 *
 * # The order of checks IS the security property
 *
 *  1. The flow cookie and the returned `state` must refer to the SAME record, and the record is
 *     consumed — deleted — first. So a callback that fails leaves no live `state` behind, and
 *     "single-use" means single USE rather than single SUCCESS.
 *  2. The code goes to the PLATFORM, with the PKCE verifier this process held while the browser was
 *     away. The platform holds the client secret and does the exchange; this console never sees
 *     either the secret or the ID token.
 *  3. The platform verifies the ID token, requires a PLATFORM-VERIFIED second factor, and only then
 *     issues an admin session.
 *
 * # Why this route does NOT complete the sign-in
 *
 * It hands off to the factor step instead. The platform requires a PLATFORM-VERIFIED second factor
 * before it issues an operator session (NFR8), and a factor presented by a browser that has not yet
 * proved whose it is is a factor presented by anybody — so the order is: prove identity at the IdP,
 * come back HERE, then present the factor.
 *
 * What crosses the gap is an authorization code, held server-side (`beginPending`) for less time than
 * the IdP will honour it. The browser gets an opaque handle. The code is not yet worth anything on its
 * own: redeeming it needs the client secret, which lives in the platform, and the platform will not
 * issue a session without the factor.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  if (!isFederated()) return refuse("not_federated");

  const url = new URL(request.url);
  const jar = await cookies();
  const flow = consumeFlow(jar.get(FLOW_COOKIE)?.value);
  const state = url.searchParams.get("state") ?? "";

  if (!flow || !constantTimeEqual(state, flow.state)) {
    // Covers all of: no cookie, an expired flow, a `state` from another browser, and a replayed
    // callback whose flow was already consumed. One refusal for the lot — telling them apart is a
    // probing oracle and helps no real operator.
    return refuse("rejected");
  }
  // An IdP-reported error arrives as a parameter. It is NOT reflected back: the value is
  // attacker-controllable text that would otherwise render on our sign-in page.
  if (url.searchParams.get("error")) return refuse("rejected");

  const code = url.searchParams.get("code") ?? "";
  if (!code) return refuse("rejected");

  // Identity is proved; authority is not. Hand off to the factor step.
  const pending = beginPending({ code, codeVerifier: flow.codeVerifier, nonce: flow.nonce });
  const response = relative("/signin?step=factor");
  response.cookies.set(PENDING_COOKIE, pending, PENDING_COOKIE_OPTIONS);
  // The flow is spent. Clearing it stops a stale cookie from making the NEXT sign-in look replayed.
  response.cookies.set(FLOW_COOKIE, "", { ...FLOW_COOKIE_OPTIONS, maxAge: 0 });
  return response;
}

function refuse(reason: string): NextResponse {
  const response = relative(`/signin?reason=${reason}`);
  response.cookies.set(FLOW_COOKIE, "", { ...FLOW_COOKIE_OPTIONS, maxAge: 0 });
  return response;
}

/** relative keeps the response same-origin by construction rather than by arithmetic. */
function relative(path: string): NextResponse {
  return new NextResponse(null, { status: 303, headers: { location: path, "cache-control": "no-store" } });
}
