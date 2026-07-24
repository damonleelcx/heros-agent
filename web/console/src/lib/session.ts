import "server-only";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { randomUUID, randomBytes } from "node:crypto";
import { SESSION_COOKIE } from "./cookies";
import { logSession } from "./telemetry";
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
 * # What the session holds, and what it deliberately does not
 *
 * `{ id, tenantId, issuedAt, expiresAt, revokedAt }`. It does NOT hold the assertion that produced it
 * (identity.ts rule 1), and it does NOT hold a user — P9 has no concept of a user, because the
 * platform cannot currently prove one. When P7 introduces users, per-user revocation and audit
 * attribution become possible; today a session can be revoked but cannot say WHOSE it was, and
 * inventing a value for that field would be worse than the gap (ADR-008).
 *
 * # Why the store is in-process, and what that means operationally
 *
 * Sessions live in a process-local map. That is honest for the deployment ADR-006 describes — one
 * console container per unit — and it has a stated consequence: a console restart ends every session,
 * and a horizontally-scaled console needs a shared store. Both are recorded rather than discovered.
 * What matters for the security property is unchanged either way: the session is SERVER-SIDE, the
 * browser holds only an opaque token it cannot read, and revocation is effective at the next request
 * because the next request reads the store.
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

/**
 * The store. Keyed by the opaque token the browser holds, which is NOT the session id — the id is for
 * logs, the token is the bearer. Separating them means a log line naming a session cannot be replayed
 * as that session.
 *
 * # Why it hangs off globalThis rather than being a plain module-level Map
 *
 * Found by rendering the console in a real browser, which is the whole argument for R11: a plain
 * `const store = new Map()` passes every test under `next start` and then, under `next dev`, signs
 * you in and immediately tells you your session ended. Next's development server compiles route
 * handlers and pages into SEPARATE module graphs, so the module is instantiated more than once and
 * the sign-in handler writes into a different Map from the one the page reads. The build is green,
 * the types check, the production suite passes, and the product is unusable — precisely the class of
 * defect a passing build cannot see.
 *
 * Anchoring to `globalThis` gives one store per PROCESS, which is what the design actually meant.
 *
 * The operational consequence is unchanged and still worth stating: one process means a console
 * restart ends every session, and a horizontally-scaled console needs a shared store. Both are
 * recorded rather than discovered. The security property is unaffected either way — the session is
 * server-side, the browser holds an opaque token it cannot read, and revocation takes effect at the
 * next request because the next request reads the store.
 */
const SESSION_STORE = Symbol.for("heros.console.sessions");

type SessionGlobal = typeof globalThis & { [SESSION_STORE]?: Map<string, Session> };

const store: Map<string, Session> = ((): Map<string, Session> => {
  const scope = globalThis as SessionGlobal;
  if (!scope[SESSION_STORE]) scope[SESSION_STORE] = new Map<string, Session>();
  return scope[SESSION_STORE];
})();

/** issueSession mints a session for a verified tenant principal and returns the browser's token. */
export function issueSession(principal: TenantPrincipal): { token: string; session: Session } {
  const now = Date.now();
  const session: Session = {
    id: randomUUID(),
    tenantId: principal.tenantId,
    issuedAt: now,
    expiresAt: now + SESSION_TTL_SECONDS * 1000,
  };
  // 32 bytes of CSPRNG. The token is the only thing standing between a guess and a tenant's data, and
  // it is never derived from the tenant id — a token you can construct from a subject you know is not
  // a token.
  const token = randomBytes(32).toString("base64url");
  store.set(token, session);
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
export function resolveSession(token: string | null | undefined): Session | null {
  if (!token) return null;
  const session = store.get(token);
  if (!session) return null;
  if (session.revokedAt !== undefined) {
    logSession({ action: "denied", sessionId: session.id, reason: "revoked" });
    return null;
  }
  if (Date.now() >= session.expiresAt) {
    logSession({ action: "denied", sessionId: session.id, reason: "expired" });
    // Dropped on read rather than swept on a timer: the store is only consulted here, so a session
    // nobody reads costs a map entry and nothing else, and a sweeper is one more thing to get wrong.
    store.delete(token);
    return null;
  }
  return session;
}

/** revokeSession ends a session server-side. The next request presenting it is denied. */
export function revokeSession(token: string | null | undefined): void {
  if (!token) return;
  const session = store.get(token);
  if (!session) return;
  session.revokedAt = Date.now();
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
  const session = resolveSession(token);
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
export function sessionFromRequest(request: Request): Session | null {
  const header = request.headers.get("cookie") ?? "";
  const match = header
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${SESSION_COOKIE}=`));
  if (!match) return null;
  return resolveSession(decodeURIComponent(match.slice(SESSION_COOKIE.length + 1)));
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
  store.clear();
}
