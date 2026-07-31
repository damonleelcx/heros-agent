"use server";

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { adminFetch, ADMIN_SESSION_COOKIE } from "@/lib/adminApi";
import { SESSION_COOKIE_OPTIONS } from "@/lib/session";
import { consumePending, PENDING_COOKIE, PENDING_COOKIE_OPTIONS } from "@/lib/idpFlow";
import { callbackURL } from "@/lib/idpConfig";
import type { ActionResult } from "@/lib/actions";

/**
 * completeSignIn presents the second factor and finishes the operator's sign-in.
 *
 * # The order, and why the pending record dies first
 *
 * The pending authentication is consumed BEFORE the platform is called, so a wrong code does not leave
 * the authorization code live for a second attempt. The operator restarts the sign-in — one redirect —
 * and the window in which a captured pending handle is worth trying against is closed.
 *
 * # Why the challenge is fetched here rather than supplied
 *
 * `POST /admin/api/mfa/challenge` mints it server-side and remembers minting it; this action only
 * carries the id. A challenge the CLIENT chooses proves that the client can choose a challenge, which
 * is what made the first cut of this flow's replay guard decorative.
 */
export async function completeSignIn(_prev: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const jar = await cookies();
  const pending = consumePending(jar.get(PENDING_COOKIE)?.value);
  jar.set(PENDING_COOKIE, "", { ...PENDING_COOKIE_OPTIONS, maxAge: 0 });
  if (!pending) {
    return {
      ok: false,
      kind: "request",
      message: "That sign-in expired before the factor was presented. Start again.",
    };
  }

  const totp = String(fd.get("totp") ?? "").trim();
  const credentialID = String(fd.get("credential_id") ?? "").trim();
  if (!totp && !credentialID) {
    return { ok: false, kind: "request", message: "Present your second factor to continue." };
  }

  const body: Record<string, unknown> = {
    code: pending.code,
    code_verifier: pending.codeVerifier,
    nonce: pending.nonce,
    redirect_uri: callbackURL(),
  };
  if (totp) {
    body.factor = { totp };
  } else {
    body.challenge_id = String(fd.get("challenge_id") ?? "");
    body.factor = {
      webauthn: {
        credential_id: credentialID,
        authenticator_data: String(fd.get("authenticator_data") ?? ""),
        client_data_json: String(fd.get("client_data_json") ?? ""),
        signature: String(fd.get("signature") ?? ""),
      },
    };
  }

  let sessionToken: string;
  try {
    const result = await adminFetch<{ session_token: string }>("/admin/api/session", { method: "POST", body });
    sessionToken = result.session_token;
  } catch {
    // One message for every failure — a stale code, a wrong factor, an unenrolled principal, a
    // disabled one. The operator sees one thing; the platform records which, as a security event.
    return {
      ok: false,
      kind: "denied",
      message: "That sign-in was not accepted. Start again, and check that your factor is enrolled.",
    };
  }
  jar.set(ADMIN_SESSION_COOKIE, sessionToken, SESSION_COOKIE_OPTIONS);
  redirect("/tenants");
}

/** mfaChallenge mints a single-use challenge for a WebAuthn attempt. */
export async function mfaChallenge(): Promise<{ challenge_id: string; challenge: string } | null> {
  try {
    return await adminFetch<{ challenge_id: string; challenge: string }>("/admin/api/mfa/challenge", {
      method: "POST",
      body: {},
    });
  } catch {
    return null;
  }
}
