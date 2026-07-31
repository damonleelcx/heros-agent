import "server-only";
import { randomBytes, createHash, timingSafeEqual } from "node:crypto";

/**
 * idpFlow.ts holds the operator console's in-flight sign-in — the browser half of the OIDC flow.
 *
 * # Why the BFF holds this and the platform holds the client secret
 *
 * The split is deliberate and it is the same one `internal/adminidentity/oidcflow.go` argues for from
 * the other side. `state`, the PKCE verifier and the nonce are per-BROWSER, so they belong with the
 * process that set the cookie — this one. The client secret mints operator sessions, so it belongs
 * where every other operator credential already lives: the platform's `Secrets` seam, resolved at the
 * moment of the exchange.
 *
 * The consequence worth stating: this BFF holds exactly ONE credential, the platform credential that
 * proves a request came through it. Adding the IdP's client secret here would give the operator
 * console a second credential from a second source, and `/readyz` would report one source while the
 * thing that actually mints sessions came from somewhere else.
 *
 * # Why the browser gets an opaque id and not the material
 *
 * The `state` in the URL travels through the IdP and lands in browser history, a `Referer`, and the
 * IdP's access log. On its own it proves nothing about WHO is completing the flow. What makes it a
 * CSRF defence is the second half: the flow id in an `HttpOnly` cookie. An attacker who can make a
 * victim's browser hit a crafted callback has the URL half and not the cookie half; an attacker who
 * read the URL out of a log has neither. Neither half alone is a session.
 */

/** MAX_FLOW_AGE_SECONDS bounds how long a begun sign-in may take to come back. */
export const MAX_FLOW_AGE_SECONDS = 600;

/** FLOW_COOKIE carries the opaque flow id. */
export const FLOW_COOKIE = "heros_admin_signin";

/**
 * FLOW_COOKIE_OPTIONS — `sameSite: "lax"`, and that differs from the admin SESSION cookie's `strict`
 * on purpose.
 *
 * The IdP returns the browser to `/auth/callback` as a cross-site top-level GET navigation, and
 * `strict` means exactly "do not send this cookie on one". A `strict` flow cookie produces a sign-in
 * that fails its own CSRF check every single time. The session cookie stays `strict` because nothing
 * ever navigates to the console from another origin.
 */
export const FLOW_COOKIE_OPTIONS = {
  httpOnly: true,
  sameSite: "lax",
  secure: process.env.NODE_ENV === "production",
  path: "/auth",
  maxAge: MAX_FLOW_AGE_SECONDS,
} as const;

export type Flow = { state: string; nonce: string; codeVerifier: string; createdAt: number };

/**
 * The store hangs off `globalThis` for the reason the customer console records at length: Next's dev
 * server compiles route handlers into separate module graphs, so a plain module-level `Map` gives
 * `/auth/login` a different store from `/auth/callback` — a build that is green and a product where
 * every sign-in fails CSRF.
 */
const FLOW_STORE = Symbol.for("heros.admin.idp.flows");
type FlowGlobal = typeof globalThis & { [FLOW_STORE]?: Map<string, Flow> };

function flows(): Map<string, Flow> {
  const scope = globalThis as FlowGlobal;
  if (!scope[FLOW_STORE]) scope[FLOW_STORE] = new Map<string, Flow>();
  return scope[FLOW_STORE];
}

/** beginFlow mints a flow and returns its id (for the cookie) and the record. */
export function beginFlow(): { id: string; flow: Flow } {
  // The id and the `state` are DIFFERENT 32-byte values. If they were the same, the URL half would
  // leak the cookie half and the browser binding would be decorative.
  const id = randomBytes(32).toString("base64url");
  const flow: Flow = {
    state: randomBytes(32).toString("base64url"),
    nonce: randomBytes(32).toString("base64url"),
    codeVerifier: randomBytes(32).toString("base64url"),
    createdAt: Date.now(),
  };
  const cutoff = Date.now() - MAX_FLOW_AGE_SECONDS * 1000;
  for (const [key, value] of flows()) if (value.createdAt < cutoff) flows().delete(key);
  flows().set(id, flow);
  return { id, flow };
}

/**
 * consumeFlow reads a flow ONCE and deletes it, whatever happens next.
 *
 * Deleted before the code is exchanged, not after: a callback that fails must not leave a live `state`
 * behind, or "single-use" would mean "single SUCCESSFUL use" and a replayed callback would get as many
 * attempts as it liked.
 */
export function consumeFlow(id: string | null | undefined): Flow | null {
  if (!id) return null;
  const store = flows();
  const flow = store.get(id);
  store.delete(id);
  if (!flow || Date.now() - flow.createdAt > MAX_FLOW_AGE_SECONDS * 1000) return null;
  return flow;
}

/** pkceChallenge is the S256 challenge. `plain` is never offered — it sends the verifier itself. */
export function pkceChallenge(verifier: string): string {
  return createHash("sha256").update(verifier).digest("base64url");
}

/** constantTimeEqual compares `state` without leaking its divergence point. */
export function constantTimeEqual(a: string, b: string): boolean {
  const left = Buffer.from(a, "utf8");
  const right = Buffer.from(b, "utf8");
  if (left.length !== right.length || left.length === 0) return false;
  return timingSafeEqual(left, right);
}

// ── The pending authentication, between the IdP's return and the second factor ───────────────────

/**
 * PendingAuth is a sign-in that has come back from the IdP and has NOT yet presented a factor.
 *
 * # Why this exists rather than collecting the factor before the redirect
 *
 * A factor presented by a browser that has not yet proved whose it is, is a factor presented by
 * anybody. So the order is: prove identity at the IdP, come back, THEN present the factor. That means
 * something has to hold the authorization code across the gap, and this is it.
 *
 * # Why it holds a code and not a session
 *
 * The code is not yet worth anything: redeeming it needs the client secret, which lives in the
 * platform, and the platform will not issue a session without the factor. So the worst an attacker who
 * steals this record achieves is a redemption that then fails the MFA gate — which is the same wall
 * they hit without it.
 *
 * # The bound that matters
 *
 * An authorization code is short-lived at every IdP (Okta expires one in about a minute) and single
 * use. `PENDING_TTL_SECONDS` is deliberately shorter than that: a pending record that outlives the
 * code it holds turns "your code expired" into a confusing failure two screens later, so this expires
 * first and says so.
 */
export const PENDING_TTL_SECONDS = 45;

/** PENDING_COOKIE names the pending authentication. Opaque, `HttpOnly`, single-use. */
export const PENDING_COOKIE = "heros_admin_pending";

export const PENDING_COOKIE_OPTIONS = {
  httpOnly: true,
  // `strict` here, unlike the flow cookie: nothing navigates to the factor step from another origin.
  // The cross-site leg of this sign-in is over by the time this cookie exists.
  sameSite: "strict",
  secure: process.env.NODE_ENV === "production",
  path: "/",
  maxAge: PENDING_TTL_SECONDS,
} as const;

export type PendingAuth = { code: string; codeVerifier: string; nonce: string; createdAt: number };

const PENDING_STORE = Symbol.for("heros.admin.idp.pending");
type PendingGlobal = typeof globalThis & { [PENDING_STORE]?: Map<string, PendingAuth> };

function pendings(): Map<string, PendingAuth> {
  const scope = globalThis as PendingGlobal;
  if (!scope[PENDING_STORE]) scope[PENDING_STORE] = new Map<string, PendingAuth>();
  return scope[PENDING_STORE];
}

/** beginPending records a returned-but-unfactored sign-in and returns its opaque id. */
export function beginPending(auth: Omit<PendingAuth, "createdAt">): string {
  const id = randomBytes(32).toString("base64url");
  const cutoff = Date.now() - PENDING_TTL_SECONDS * 1000;
  for (const [key, value] of pendings()) if (value.createdAt < cutoff) pendings().delete(key);
  pendings().set(id, { ...auth, createdAt: Date.now() });
  return id;
}

/**
 * consumePending reads a pending authentication ONCE and deletes it.
 *
 * Deleted before the factor is checked, so a wrong code does not leave the authorization code live for
 * a second attempt. The operator restarts the sign-in, which costs one redirect and closes the window
 * in which a captured pending id is worth trying against.
 */
export function consumePending(id: string | null | undefined): PendingAuth | null {
  if (!id) return null;
  const store = pendings();
  const auth = store.get(id);
  store.delete(id);
  if (!auth || Date.now() - auth.createdAt > PENDING_TTL_SECONDS * 1000) return null;
  return auth;
}
