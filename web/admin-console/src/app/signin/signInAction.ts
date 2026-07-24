"use server";

import { adminFetch } from "@/lib/adminApi";
import { signIn, type ActionResult } from "@/lib/actions";

/**
 * signInAction begins the SSO + MFA exchange.
 *
 * In a test-mode deployment the BFF asks the backend's test IdP to mint a fixture assertion for the
 * chosen subject and factor, then hands it to the same `signIn` action production uses. The assertion
 * is signed by the admin IdP's keys and verified by the real verifier — including the MFA evidence —
 * so this is not an MFA bypass, it is the production login path with a test-mode issuer.
 *
 * In a production deployment this action is unused: the operator returns from the IdP redirect with a
 * real assertion and `signIn` is called directly.
 */
export async function beginSignIn(_prev: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const subject = String(fd.get("subject") ?? "").trim();
  const factor = String(fd.get("factor") ?? "webauthn");
  if (!subject) return { ok: false, kind: "request", message: "Enter the admin principal's SSO subject." };
  try {
    // The BFF asks the backend for a test-mode assertion. This endpoint exists only when the admin IdP
    // is in test mode; a production backend returns 404 and the operator uses the IdP redirect instead.
    const assertion = await adminFetch<unknown>("/admin/api/testmode/assert", {
      method: "POST",
      body: { subject, factor },
    });
    return signIn(assertion);
  } catch (error) {
    return { ok: false, kind: "degraded", message: `could not begin sign-in: ${String(error)}` };
  }
}
