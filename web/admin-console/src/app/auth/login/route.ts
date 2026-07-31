import { NextResponse } from "next/server";
import { adminFetch } from "@/lib/adminApi";
import { beginFlow, pkceChallenge, FLOW_COOKIE, FLOW_COOKIE_OPTIONS } from "@/lib/idpFlow";
import { callbackURL, isFederated } from "@/lib/idpConfig";

/**
 * `/auth/login` begins the operator's federated sign-in.
 *
 * # Why a GET
 *
 * The response is a redirect to the IdP — a different origin — and a POSTed sign-in would be subject
 * to `form-action`, which browsers apply to a form submission's redirect chain. The customer console
 * records the same reasoning at length. The cookie this sets is not a credential: it carries an opaque
 * flow id and authorises nothing, and the `state`, nonce and PKCE verifier stay server-side.
 *
 * # What it does NOT do
 *
 * It does not build the authorization URL itself. The platform does, from the issuer's own discovery
 * document — so this process never needs to know the IdP's endpoints, and an operator changing IdP
 * changes one deployment's configuration rather than two.
 */
export const dynamic = "force-dynamic";

export async function GET() {
  if (!isFederated()) {
    // A test-mode deployment signs in through the fixture control on the sign-in page. Redirecting
    // there is the honest answer — and it is a REDIRECT, never a rendered shell.
    return relativeRedirect("/signin?reason=not_federated");
  }

  const { id, flow } = beginFlow();
  let authorizationURL: string;
  try {
    const started = await adminFetch<{ authorization_url: string }>("/admin/api/idp/start", {
      method: "POST",
      body: {
        state: flow.state,
        nonce: flow.nonce,
        code_challenge: pkceChallenge(flow.codeVerifier),
        redirect_uri: callbackURL(),
      },
    });
    authorizationURL = started.authorization_url;
  } catch {
    // Fail closed at the FIRST hop. A console that cheerfully redirects to an IdP it cannot reach
    // drops the operator on a broken page on somebody else's domain, with no way to tell whose fault
    // it is — and during an incident that is the worst possible moment for that ambiguity.
    return relativeRedirect("/signin?reason=idp_unreachable");
  }

  // The ONE off-origin redirect in this console. The destination is not client input and is not
  // reconstructed from a header: it is the authorization endpoint the IdP's own discovery document
  // declared, and the platform refuses that document unless its `issuer` equals the configured one.
  const response = new NextResponse(null, {
    status: 303,
    headers: { location: authorizationURL, "cache-control": "no-store" },
  });
  response.cookies.set(FLOW_COOKIE, id, FLOW_COOKIE_OPTIONS);
  return response;
}

function relativeRedirect(path: string): NextResponse {
  // Relative, so the response is same-origin by construction rather than by arithmetic. The customer
  // console's `lib/redirect.ts` records why reconstructing an origin from `request.url` fails in a
  // real browser while passing every server-side check.
  return new NextResponse(null, { status: 303, headers: { location: path, "cache-control": "no-store" } });
}
