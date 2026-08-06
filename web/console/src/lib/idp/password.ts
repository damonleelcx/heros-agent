import "server-only";
import { platformFetchPublic } from "../platformApi";
import { logIdentity } from "../telemetry";

/**
 * password.ts is the `password` identity seam: an email address and a password, verified BY THE PLATFORM.
 *
 * # 🔴 The console holds no password logic and no stored hash
 *
 * Everything here forwards. There is no argon2 in this process, no comparison, no lockout counter and no
 * user table — the platform owns all of it, exactly as `platformToken.ts` asks the platform whose token it
 * is rather than keeping a second copy of the answer. Two places that can decide whether a password is
 * correct is two places a revocation has to reach, and the second one is always the one somebody forgets.
 *
 * # What crosses which boundary
 *
 * The password arrives once, in a POST body, on the console's own origin. It is forwarded once, to the
 * platform, and dropped. It is never stored in the session record, never written to a cookie, never logged,
 * and never put in a URL — the three rules `identity.ts` binds every seam implementation to, restated here
 * because this is the only seam where the secret is something a human chose and could reuse elsewhere.
 *
 * 🔴 Note what that means for the code below: the plaintext reaches these functions as an argument, is
 * used once, and is never returned or handed to `logIdentity` — which has no parameter that could carry it.
 *
 * # Why the refusal prose comes from the platform
 *
 * The platform decides what a sign-in refusal says, including the one distinguishable case (a locked
 * account, whose message carries the remaining duration only the server knows). Re-writing it here would
 * put the copy in two places and let this console's wording drift from the CLI's for the same event.
 */

export type PasswordPrincipal = {
  tenantId: string;
  userId?: string;
  email: string;
  emailVerified: boolean;
  organizationName?: string;
};

export type PasswordOutcome =
  | { ok: true; principal: PasswordPrincipal }
  | { ok: false; reason: string; reasonCode: string };

/** The platform's machine-readable refusals this seam branches on. */
export const REASON_BAD_CREDENTIALS = "bad_credentials";
export const REASON_LOCKED = "account_locked";
export const REASON_WEAK_PASSWORD = "weak_password";
export const REASON_LINK_UNUSABLE = "link_unusable";
export const REASON_SELF_SERVE_DISABLED = "self_serve_disabled";

/**
 * GENERIC_REFUSAL is what a caller shows when the platform gave no prose of its own.
 *
 * It is the same sentence for an unknown address and a wrong password, matching the platform — which is
 * the point: a console that helpfully distinguished them would rebuild the account-enumeration oracle the
 * platform closes, one layer up, where nobody would think to look for it.
 */
export const GENERIC_REFUSAL = "That email and password did not match. Check them, or reset your password.";

type SignInBody = {
  tenant_id?: string;
  organization_name?: string;
  user_id?: string;
  email?: string;
  email_verified?: boolean;
  error?: string;
  reason_code?: string;
};

/**
 * verifyPassword authenticates an address and a password and returns the principal.
 *
 * 🚫 It sends NO `device_label`, so the platform mints no credential. The console does not want one: it
 * issues its own server-side session from the principal, exactly as every other seam does, and a long-lived
 * API credential minted on every page-load sign-in would appear in the customer's credentials list once per
 * browser and be revocable only by hand.
 */
export async function verifyPassword(email: string, plaintext: string): Promise<PasswordOutcome> {
  const address = email.trim().toLowerCase();
  if (!address || !plaintext) {
    // Refused without a round trip, and with the SAME message the platform would give — an empty field
    // must not be distinguishable from a wrong one, or the form itself becomes the oracle.
    return { ok: false, reason: GENERIC_REFUSAL, reasonCode: REASON_BAD_CREDENTIALS };
  }
  const outcome = await platformFetchPublic<SignInBody>("/api/v1/auth/password/signin", {
    method: "POST",
    body: { email: address, password: plaintext },
  });
  if (!outcome.ok) {
    // The CAUSE is logged server-side and never the value. `logIdentity` has no parameter a credential
    // would fit in, which is what makes that a property of the code rather than of this comment.
    logIdentity({ event: "assertion_refused", provider: "password", cause: outcome.reasonCode ?? outcome.error });
    return {
      ok: false,
      reason: outcome.error || GENERIC_REFUSAL,
      reasonCode: outcome.reasonCode ?? REASON_BAD_CREDENTIALS,
    };
  }
  const { tenant_id: tenantId, user_id: userId, email: verifiedEmail, email_verified: emailVerified } = outcome.data;
  if (!tenantId) {
    logIdentity({ event: "assertion_refused", provider: "password", cause: "the platform returned no organization" });
    return { ok: false, reason: GENERIC_REFUSAL, reasonCode: REASON_BAD_CREDENTIALS };
  }
  logIdentity({ event: "sign_in", provider: "password", tenantId });
  return {
    ok: true,
    principal: {
      tenantId,
      userId: userId || undefined,
      email: verifiedEmail ?? address,
      emailVerified: Boolean(emailVerified),
      organizationName: outcome.data.organization_name,
    },
  };
}

/**
 * signUpWithPassword creates the organization, the person, the owner membership, the free account and the
 * password — all of them or none, on the platform.
 *
 * 🔴 It returns the SAME shape for a duplicate address as for a new one, because the platform does. That is
 * not this function being lax: registration must not answer "does this address have an account here", and
 * the information goes to the address instead. See the platform's `handlePasswordSignUp`.
 */
export async function signUpWithPassword(
  name: string,
  email: string,
  plaintext: string,
): Promise<PasswordOutcome> {
  const address = email.trim().toLowerCase();
  const outcome = await platformFetchPublic<SignInBody>("/api/v1/auth/password/signup", {
    method: "POST",
    body: { name: name.trim(), email: address, password: plaintext },
  });
  if (!outcome.ok) {
    logIdentity({ event: "assertion_refused", provider: "password", cause: outcome.reasonCode ?? outcome.error });
    return {
      ok: false,
      reason: outcome.error || "That account could not be created.",
      reasonCode: outcome.reasonCode ?? "",
    };
  }
  const { tenant_id: tenantId, user_id: userId } = outcome.data;
  if (!tenantId) {
    // The duplicate-address path: the platform answered 201 and deliberately named no organization, so
    // there is nothing to sign in as. The caller shows the neutral acknowledgement.
    return { ok: false, reason: "", reasonCode: "neutral_ack" };
  }
  return {
    ok: true,
    principal: {
      tenantId,
      userId: userId || undefined,
      email: outcome.data.email ?? address,
      emailVerified: Boolean(outcome.data.email_verified),
      organizationName: outcome.data.organization_name,
    },
  };
}

/**
 * requestPasswordReset always resolves, and always the same way.
 *
 * Even a transport failure returns success to the caller. 🔴 That is deliberate and it is the ONE place in
 * this codebase where an error is deliberately not surfaced: the whole property of this endpoint is that its
 * answer carries no information, and an outage that made a known address answer differently from an unknown
 * one would leak exactly what the design closes. The failure is logged, where an operator sees it.
 */
export async function requestPasswordReset(email: string): Promise<void> {
  const outcome = await platformFetchPublic<{ ok: boolean }>("/api/v1/auth/password/forgot", {
    method: "POST",
    body: { email: email.trim().toLowerCase() },
  });
  if (!outcome.ok) {
    logIdentity({ event: "assertion_refused", provider: "password", cause: `forgot-password upstream: ${outcome.error}` });
  }
}

/** resendConfirmation has the same shape, and the same reason for it, as requestPasswordReset. */
export async function resendConfirmation(email: string): Promise<void> {
  const outcome = await platformFetchPublic<{ ok: boolean }>("/api/v1/auth/password/resend", {
    method: "POST",
    body: { email: email.trim().toLowerCase() },
  });
  if (!outcome.ok) {
    logIdentity({ event: "assertion_refused", provider: "password", cause: `resend upstream: ${outcome.error}` });
  }
}

export type ResetOutcome =
  | {
      ok: true;
      email: string;
      sessionsRevoked: number;
      credentialsRevoked: number;
      /** What the reset did NOT revoke, by name. See the platform handler for why hiding this is worse. */
      machineCredentials: { credential_id: string; label: string }[];
    }
  | { ok: false; reason: string; reasonCode: string };

/** completePasswordReset spends the link and sets the new password. */
export async function completePasswordReset(token: string, plaintext: string): Promise<ResetOutcome> {
  const outcome = await platformFetchPublic<{
    email?: string;
    sessions_revoked?: number;
    credentials_revoked?: number;
    machine_credentials_untouched?: { credential_id: string; label: string }[];
  }>("/api/v1/auth/password/reset", { method: "POST", body: { token, password: plaintext } });
  if (!outcome.ok) {
    return {
      ok: false,
      reason: outcome.error || "This link is no longer usable.",
      reasonCode: outcome.reasonCode ?? REASON_LINK_UNUSABLE,
    };
  }
  return {
    ok: true,
    email: outcome.data.email ?? "",
    sessionsRevoked: outcome.data.sessions_revoked ?? 0,
    credentialsRevoked: outcome.data.credentials_revoked ?? 0,
    machineCredentials: outcome.data.machine_credentials_untouched ?? [],
  };
}

/** confirmEmail spends a confirmation link. */
export async function confirmEmail(token: string): Promise<{ ok: boolean; email: string; reason: string }> {
  const outcome = await platformFetchPublic<{ email?: string }>("/api/v1/auth/password/verify", {
    method: "POST",
    body: { token },
  });
  if (!outcome.ok) {
    return { ok: false, email: "", reason: outcome.error || "This link is no longer usable." };
  }
  return { ok: true, email: outcome.data.email ?? "", reason: "" };
}

/**
 * reachable probes the platform for the readiness surface.
 *
 * 🔴 It sends NO credential and treats ANY answer — including a refusal — as reachable, exactly as
 * `platformToken.reachable` does and for the same reason: the question is "does the authority respond?",
 * not "is some credential good?". A seam that reported `reachable: true` unconditionally would be a signal
 * that cannot fail, and for this seam the platform's reachability IS whether anybody can sign in at all.
 */
export async function reachable(): Promise<{ reachable: boolean; detail?: string }> {
  const outcome = await platformFetchPublic<unknown>("/api/v1/auth/password/forgot", {
    method: "POST",
    body: { email: "" },
  });
  if (outcome.ok) return { reachable: true };
  if (outcome.status > 0) {
    // Something answered. That is the healthy state for a probe that presents nothing.
    return { reachable: true };
  }
  return { reachable: false, detail: outcome.error || "the platform did not answer" };
}
