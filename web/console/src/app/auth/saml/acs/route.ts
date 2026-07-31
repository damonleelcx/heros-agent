import { cookies } from "next/headers";
import { CONFIG, canonicalCallback, allowedRedirect } from "@/lib/idp/config";
import { consumeFlow, SIGNIN_FLOW_COOKIE, SIGNIN_FLOW_COOKIE_OPTIONS } from "@/lib/idp/flow";
import { requestId } from "@/lib/idp/saml";
import { constantTimeEqual } from "@/lib/idp/jwt";
import { verifyTenantAssertion, type RefusalClass } from "@/lib/identity";
import { issueSession, sessionTtlSeconds } from "@/lib/session";
import { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "@/lib/cookies";
import { redirectTo, samePath } from "@/lib/redirect";
import { logIdentity } from "@/lib/telemetry";

/**
 * `/auth/saml/acs` is the SAML Assertion Consumer Service (P22 tasks 3.1, 5.2).
 *
 * # Why the ACS is a POST and the OIDC callback is a GET
 *
 * Not a style choice: the SAML HTTP-POST binding delivers the response as a form field, so this route
 * receives a POST because the protocol says so. That happens to be the safer shape, and it is why the
 * OIDC callback's GET needed the justification it carries.
 *
 * # The allowlist is checked twice, and the two checks are different questions
 *
 * Here: is the URL this deployment publishes as its ACS on the allowlist at all — a configuration
 * check, so a misconfigured deployment fails at the first response rather than accepting one at a URL
 * nobody meant to expose. Inside `verifySamlResponse`: does the assertion's own `Destination` and
 * `SubjectConfirmationData/@Recipient` name an allowlisted ACS — an assertion check, which is what
 * stops a response minted for another SP from being replayed at ours.
 *
 * # What binds this response to this browser
 *
 * `RelayState` carries the flow's `state` and must equal the record the `HttpOnly` flow cookie points
 * at, and the assertion's `InResponseTo` must equal the `ID` of the AuthnRequest that flow signed. An
 * unsolicited assertion — however validly signed by the registered IdP — matches neither and issues
 * no session.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  if (CONFIG.kind !== "saml") return refuse("credential");

  const acsUrl = canonicalCallback();
  if (!allowedRedirect(acsUrl, CONFIG.acsAllowlist)) {
    logIdentity({ event: "redirect_refused", provider: CONFIG.kind, cause: "the configured ACS is not on its own allowlist" });
    return refuse("credential");
  }

  const form = await request.formData().catch(() => null);
  const samlResponse = String(form?.get("SAMLResponse") ?? "");
  const relayState = String(form?.get("RelayState") ?? "");

  const jar = await cookies();
  const flow = consumeFlow(jar.get(SIGNIN_FLOW_COOKIE)?.value);
  if (!flow || !constantTimeEqual(relayState, flow.state)) {
    logIdentity({ event: "replay_refused", provider: CONFIG.kind, cause: "ACS RelayState did not match a live browser-bound flow" });
    return refuse("credential");
  }
  if (!samlResponse) {
    logIdentity({ event: "assertion_refused", provider: CONFIG.kind, issuer: CONFIG.issuer, cause: "ACS carried no SAMLResponse" });
    return refuse("credential");
  }

  const outcome = await verifyTenantAssertion(samlResponse, {
    kind: "saml",
    acsUrl,
    inResponseTo: requestId(flow.state),
  });
  if (!outcome.ok) return refuse(outcome.refusal, flow.next);

  const { token } = issueSession(outcome.principal);
  const response = redirectTo(samePath(flow.next));
  response.cookies.set(SESSION_COOKIE, token, { ...SESSION_COOKIE_OPTIONS, maxAge: sessionTtlSeconds() });
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
