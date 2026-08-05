import "server-only";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { randomUUID, randomBytes } from "node:crypto";
import { SESSION_COOKIE } from "./cookies";
import { logSession } from "./telemetry";
import { sessionStore } from "./sessionStore";
import type { TenantPrincipal } from "./identity";

/**
 * session.ts is the console's fail-closed entry check (FR2) and its revocation boundary (FR3).
 *
 * # Why every tenant page calls requireSession() before rendering anything
 *
 * The requirement is that an unauthenticated request REDIRECTS rather than rendering a shell that
 * then fails each of its requests. The current behavior is the opposite — the four legacy pages load
 * and then 401 on every fetch — and it teaches the user that the product is broken rather than that
 * they are signed out. So the session check happens before the first byte of a page, and its failure
 * is a redirect.
 *
 * The check is by ROUTE PREFIX as well as per page (see middleware.ts): a fail-closed rule enforced
 * page by page fails the first time somebody adds a page.
 *
 * # What the session holds
 *
 * `{ id, tenantId, userId?, issuedAt, expiresAt, revokedAt }`.
 *
 * It does NOT hold the assertion that produced it (identity.ts rule 1). It DOES now hold the person,
 * where there is one — and the sentence that used to be here is worth keeping beside the change:
 *
 *   > "it does NOT hold a user — P9 has no concept of a user, because the platform cannot currently
 *   > prove one."
 *
 * That was true when ADR-008 was written. **P22 made it false**: a verified assertion yields
 * `sub@issuer`, which is exactly the proof whose absence was the reason, and the deferral was never
 * revisited. P27 promotes it. `userId` is ABSENT — never `""` — when the principal is not a person,
 * because a placeholder would put a name on an action nobody took.
 *
 * # Where the store lives, and why that changed
 *
 * Sessions used to live in a process-local map. That was honest for the one-container deployment
 * ADR-006 describes, and it had two stated consequences: a console restart ended every session, and a
 * horizontally-scaled console needed a shared store. P19's Kubernetes overlay declares `replicas: 2`,
 * under which a user signs in against one pod and is signed out by the next request that lands on the
 * other — intermittently, which is the worst failure mode to diagnose.
 *
 * So the STORE moved and nothing else did. Two implementations behind one seam:
 *
 *   * `memory` — the original map. Still the default, still honest, still what a deployment with no
 *     platform session backing runs on.
 *   * `platform` — a row in the platform's `console_session` table, written through three routes the
 *     browser cannot reach.
 *
 * 🔴 The platform never sees a console session token. This module mints it, hashes it, and sends only
 * the HASH; there is no field on any of those requests a plaintext could arrive in. And a console
 * session is not an API credential: the platform stores it with `purpose = 'console'` and its `auth`
 * layer refuses that purpose, so a stolen cookie reaches the console and stops there.
 *
 * What did NOT change, and is asserted rather than assumed: the TTL, the cookie flags, revocation at
 * the next request with no grace period, and the fail-closed middleware.
 */

// The cookie name and flags live in `cookies.ts`, which has no imports, because `middleware.ts` runs
// in the edge runtime and needs the NAME without pulling `node:crypto` in behind it. Re-exported here
// so a call site that already has the session module does not need a second import.
export { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "./cookies";

/**
 * SESSION_TTL_SECONDS bounds a session's lifetime (FR3, NFR2).
 *
 * Eight hours: long enough that a working day does not require re-authentication, short enough that a
 * session left open on an unattended machine does not outlive the reason it was created. It is
 * configurable because a customer's own policy may be stricter, and never longer than the default by
 * accident — a deployment that wants longer has to say so.
 */
const SESSION_TTL_SECONDS = Number(process.env.CONSOLE_SESSION_TTL_SECONDS ?? 8 * 60 * 60);

export type Session = {
  id: string;
  tenantId: string;
  /**
   * userId is the acting person, when the principal is one.
   *
   * 🔴 ABSENT, never `""`, for a machine principal. `undefined` and `""` would both be falsy and the
   * code would work either way — which is the argument for choosing deliberately: an empty string in
   * an audit field reads as a person whose id we failed to record, and absence reads as what it is.
   */
  userId?: string;
  issuedAt: number;
  expiresAt: number;
  revokedAt?: number;
  /**
   * visited is the subjects this session has opened — a CONSOLE-LOCAL convenience, never a platform
   * statistic (see subjects.ts). It lives on the session so it is per-session, unshared, server-side,
   * and gone when the session is.
   */
  visited?: import("./subjects").Subject[];
};

/*
 * The store now lives in `sessionStore.ts`, behind a seam with two implementations. The token is still
 * the key and it is still NOT the session id — the id is for logs, the token is the bearer, and
 * separating them means a log line naming a session cannot be replayed as that session.
 */

/**
 * issueSession mints a session for a verified tenant principal and returns the browser's token.
 *
 * Async since P27, because the store may be durable. Nothing else about it moved: the same TTL, the
 * same 32 bytes of CSPRNG, the same telemetry line.
 */
export async function issueSession(principal: TenantPrincipal): Promise<{ token: string; session: Session }> {
  const now = Date.now();
  const session: Session = {
    id: randomUUID(),
    tenantId: principal.tenantId,
    issuedAt: now,
    expiresAt: now + SESSION_TTL_SECONDS * 1000,
  };
  // 🔴 Set only when the principal names a person. `undefined` and `""` are both falsy and the code
  // would work either way — which is why the choice is made here rather than left to fall out: an empty
  // string in an audit field reads as a person whose id we failed to record.
  if (principal.userId) session.userId = principal.userId;

  // 32 bytes of CSPRNG. The token is the only thing standing between a guess and a tenant's data, and
  // it is never derived from the tenant id — a token you can construct from a subject you know is not
  // a token.
  const token = randomBytes(32).toString("base64url");
  await sessionStore.create(token, session);
  logSession({ action: "issued", sessionId: session.id, tenantId: session.tenantId });
  return { token, session };
}

/**
 * resolveSession reads a token and returns the live session, or null.
 *
 * Expiry and revocation are checked HERE, on every read, which is what makes revocation effective at
 * the next request with no grace period. There is deliberately no cached "was valid a moment ago"
 * path: a grace period is a window in which a revoked session still works, and the length of that
 * window is exactly how long a compromised session outlives its revocation.
 */
export async function resolveSession(token: string | null | undefined): Promise<Session | null> {
  if (!token) return null;
  const session = await sessionStore.resolve(token);
  if (!session) return null;
  if (session.revokedAt !== undefined) {
    logSession({ action: "denied", sessionId: session.id, reason: "revoked" });
    return null;
  }
  if (Date.now() >= session.expiresAt) {
    logSession({ action: "denied", sessionId: session.id, reason: "expired" });
    return null;
  }
  return session;
}

/** revokeSession ends a session server-side. The next request presenting it is denied. */
export async function revokeSession(token: string | null | undefined): Promise<void> {
  if (!token) return;
  const session = await sessionStore.resolve(token);
  if (!session) return;
  await sessionStore.revoke(token);
  logSession({ action: "revoked", sessionId: session.id, tenantId: session.tenantId });
  // The record is kept (rather than deleted) with `revokedAt` set, so `resolveSession` can log the
  // denial as a REVOCATION rather than as an unknown token. "Someone presented a session we revoked"
  // and "someone presented a token we have never seen" are different security events.
}

/** readSessionToken returns the opaque token from the HttpOnly cookie, or null. */
export async function readSessionToken(): Promise<string | null> {
  const jar = await cookies();
  return jar.get(SESSION_COOKIE)?.value ?? null;
}

/**
 * requireSession resolves the acting tenant, or redirects to sign-in.
 *
 * The redirect carries a `reason` so the sign-in page can say "your session ended" rather than
 * "sign in" — which are different messages to a user who was, a moment ago, signed in.
 */
export async function requireSession(): Promise<Session> {
  const token = await readSessionToken();
  const session = await resolveSession(token);
  if (!session) {
    redirect(token ? "/signin?reason=session_ended" : "/signin?reason=no_session");
  }
  return session;
}

/**
 * sessionFromRequest resolves a session for a route handler, which has no `cookies()` scope of its
 * own semantics but does have the request. Route handlers must fail closed too: a BFF data route that
 * served without a session would make the page-level check decorative.
 */
export async function sessionFromRequest(request: Request): Promise<Session | null> {
  const header = request.headers.get("cookie") ?? "";
  const match = header
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${SESSION_COOKIE}=`));
  if (!match) return null;
  return await resolveSession(decodeURIComponent(match.slice(SESSION_COOKIE.length + 1)));
}

/** sessionTtlSeconds is exported so the cookie and the test read the same bound. */
export function sessionTtlSeconds(): number {
  return SESSION_TTL_SECONDS;
}

/**
 * __resetSessionsForTest clears the store. Exported for tests only, and named so that its presence in
 * a non-test call site is obvious in review.
 */
export function __resetSessionsForTest(): void {
  sessionStore.clear();
}
