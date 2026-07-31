import "server-only";
import { randomBytes, createHash } from "node:crypto";
import { MAX_FLOW_AGE_SECONDS, MAX_ASSERTION_AGE_SECONDS } from "./federation";

/**
 * flow.ts holds the two server-side records a redirect flow needs to be safe: the in-flight sign-in,
 * and the assertions already spent (P22 Decision 9, tasks 5.1 / 8.4).
 *
 * # Why a `state` in a cookie is not the same as a `state` in a URL
 *
 * The `state` parameter travels through the IdP and comes back in the address bar, which means it ends
 * up in browser history, in a `Referer`, in an IdP's access log, and in whatever the user pastes into
 * a support ticket. On its own it therefore proves nothing about WHO is completing the flow.
 *
 * What makes it a CSRF defense is the second half: the flow's identifier is written to an `HttpOnly`
 * cookie at `/auth/login`, and the callback requires BOTH — the `state` from the URL and the flow id
 * from the cookie, referring to the same record. An attacker who can make a victim's browser visit a
 * crafted callback has the URL half and not the cookie half; an attacker who captured the URL from a
 * log has neither the victim's cookie jar nor the PKCE verifier. Neither half alone is a session.
 *
 * # Why the PKCE verifier and the nonce never leave this process
 *
 * The verifier proves that the party redeeming the authorization code is the party that began the
 * flow; the nonce binds the returned ID token to this request. Both are held HERE, keyed by the flow
 * id, so the browser carries an opaque handle and never the secrets themselves. The alternative —
 * packing them into a signed cookie — puts the material in the browser to save a map, which is the
 * trade `session.ts` already declined for the session itself.
 *
 * # Why the stores hang off `globalThis`
 *
 * Same defect, same fix, same one-line summary as `session.ts`: Next's dev server compiles route
 * handlers into separate module graphs, so a plain module-level `Map` gives `/auth/login` a different
 * store from `/auth/callback` — a build that is green and a product where every sign-in fails CSRF.
 *
 * The operational consequence is the same too, and is stated rather than discovered: one process means
 * a console restart abandons in-flight sign-ins (the user retries and succeeds), and a horizontally
 * scaled console needs a shared store or sticky routing for the ten seconds a flow is alive.
 */

/** Flow is one in-flight sign-in. It holds no identity — nobody has proved anything yet. */
export type Flow = {
  /** The value that travels through the IdP in the URL. */
  state: string;
  /** The PKCE code verifier. Never leaves this process. */
  codeVerifier: string;
  /** The nonce that must appear in the returned ID token. Never leaves this process. */
  nonce: string;
  /** Where to land after a successful exchange. Same-origin path, validated before it is stored. */
  next: string;
  /** The exact allowlisted redirect URI this flow was begun with, replayed at the token endpoint. */
  redirectUri: string;
  createdAt: number;
};

const FLOW_STORE = Symbol.for("heros.console.idp.flows");
const SPENT_STORE = Symbol.for("heros.console.idp.spent");

type FlowGlobal = typeof globalThis & {
  [FLOW_STORE]?: Map<string, Flow>;
  [SPENT_STORE]?: Map<string, number>;
};

function flows(): Map<string, Flow> {
  const scope = globalThis as FlowGlobal;
  if (!scope[FLOW_STORE]) scope[FLOW_STORE] = new Map<string, Flow>();
  return scope[FLOW_STORE];
}

function spent(): Map<string, number> {
  const scope = globalThis as FlowGlobal;
  if (!scope[SPENT_STORE]) scope[SPENT_STORE] = new Map<string, number>();
  return scope[SPENT_STORE];
}

/**
 * The flow cookie's NAME and FLAGS live in `cookies.ts`, with the session cookie's.
 *
 * Not because they belong together conceptually — they do not — but because credential cookie flags
 * are set in exactly one place in this console, and `tests/security.test.mjs` fails if a second place
 * appears. Re-exported here so a call site that already has the flow module does not need a second
 * import, exactly as `session.ts` re-exports the session cookie's.
 */
export { SIGNIN_FLOW_COOKIE, SIGNIN_FLOW_COOKIE_OPTIONS } from "../cookies";

/** beginFlow mints a flow and returns its id (for the cookie) and the record. */
export function beginFlow(input: { next: string; redirectUri: string }): { id: string; flow: Flow } {
  // 32 bytes of CSPRNG each. `state` and the flow id are DIFFERENT values on purpose: if they were the
  // same, the URL half would leak the cookie half and the browser binding would be decorative.
  const id = randomBytes(32).toString("base64url");
  const flow: Flow = {
    state: randomBytes(32).toString("base64url"),
    codeVerifier: randomBytes(32).toString("base64url"),
    nonce: randomBytes(32).toString("base64url"),
    next: input.next,
    redirectUri: input.redirectUri,
    createdAt: Date.now(),
  };
  sweepFlows();
  flows().set(id, flow);
  return { id, flow };
}

/**
 * consumeFlow reads a flow ONCE and deletes it, whatever happens next.
 *
 * Deleted before the assertion is verified, not after: a callback that fails verification must not
 * leave a live `state` behind for the next attempt, or "single-use" would mean "single SUCCESSFUL
 * use" and a replayed callback would get as many tries as it liked.
 */
export function consumeFlow(id: string | null | undefined): Flow | null {
  if (!id) return null;
  const store = flows();
  const flow = store.get(id);
  store.delete(id);
  if (!flow) return null;
  if (Date.now() - flow.createdAt > MAX_FLOW_AGE_SECONDS * 1000) return null;
  return flow;
}

function sweepFlows(): void {
  // Swept on write rather than on a timer: the store is only touched here and at the callback, so an
  // abandoned flow costs one map entry until the next sign-in begins, and a timer is one more thing to
  // get wrong in a process that may be scaled to zero.
  const cutoff = Date.now() - MAX_FLOW_AGE_SECONDS * 1000;
  for (const [id, flow] of flows()) if (flow.createdAt < cutoff) flows().delete(id);
}

/** pkceChallenge is the S256 challenge for a verifier. Plain `code_challenge_method` is never offered. */
export function pkceChallenge(verifier: string): string {
  return createHash("sha256").update(verifier).digest("base64url");
}

/**
 * spendAssertion records an assertion id and reports whether it was NEW.
 *
 * This is the one-time half of the replay guard; freshness (`federation.checkFreshness`) is the other.
 * Freshness alone leaves a 120-second window in which a captured assertion is a valid credential, and
 * 120 seconds is plenty. Together they mean a captured assertion is worth one use — the one the
 * legitimate user already made.
 *
 * Entries are kept for the freshness window plus the skew and then dropped: an assertion too old to
 * pass `checkFreshness` cannot be replayed anyway, so remembering it forever would be an unbounded set
 * that buys nothing.
 */
export function spendAssertion(assertionId: string): boolean {
  const store = spent();
  const now = Date.now();
  for (const [id, at] of store) if (now - at > (MAX_ASSERTION_AGE_SECONDS + 60) * 1000) store.delete(id);
  if (store.has(assertionId)) return false;
  store.set(assertionId, now);
  return true;
}

/** __resetIdentityFlowsForTest clears both stores. Named so its presence outside a test is obvious. */
export function __resetIdentityFlowsForTest(): void {
  flows().clear();
  spent().clear();
}
