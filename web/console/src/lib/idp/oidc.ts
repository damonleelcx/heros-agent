import "server-only";
import { CONFIG } from "./config";
import { identitySecret, SECRET_OIDC_CLIENT_SECRET } from "./secrets";
import { verifyCompactJws, jwsIdentifier, constantTimeEqual, type Jwk } from "./jwt";
import { emailClaim, checkFreshness, type FederatedClaims } from "./federation";
import { pkceChallenge } from "./flow";

/**
 * oidc.ts is the OIDC Authorization Code + PKCE mechanism (P22 Decision 2, task 2.1).
 *
 * It derives every bound, claim name and refusal from `federation.ts`, and every signature decision
 * from `jwt.ts`. What is left here is the PROTOCOL: discovery, the authorization request, the code
 * exchange, and the ID-token checks that are specific to OIDC rather than to federation in general.
 *
 * # Why the implicit flow is not an option, not even a configurable one
 *
 * The implicit flow delivers a token in a URL fragment, which puts it in the address bar, in browser
 * history, and in reach of any script on the page. Authorization Code + PKCE keeps the token on the
 * server side of the BFF, which is what makes the two properties above the seam possible at all: the
 * assertion can be DROPPED (NFR2) because it never left this process, and the browser can hold only an
 * opaque session cookie (the no-key rule) because it never held anything else.
 *
 * # Fail closed, and what "closed" means for a cache
 *
 * Discovery and JWKS are cached for a short TTL because fetching them per sign-in would make every
 * login wait on two extra round trips. A refresh that FAILS does not serve the stale copy: it returns
 * the error. Serving stale metadata during an IdP outage would let a sign-in proceed against a key set
 * the IdP can no longer vouch for — the "cached-credential login" Decision 8 refuses by name. The
 * token exchange hits the IdP on every sign-in regardless, so an unreachable IdP issues no session
 * whatever the cache holds.
 */

/** OIDC_TIMEOUT_MS bounds every call to the IdP. An IdP that hangs must not hang the sign-in surface. */
const OIDC_TIMEOUT_MS = Number(process.env.CONSOLE_IDP_TIMEOUT_MS ?? 5000);

/** METADATA_TTL_MS is how long discovery and JWKS are reused. Short: a rotated key must be picked up. */
const METADATA_TTL_MS = 5 * 60 * 1000;

/**
 * idpFetch is the ONE egress path to an identity provider (lane: direct outbound, no proxy).
 *
 * Declared in one place rather than at each call site so that "which client talks to the IdP, with
 * what timeout, following which redirects" is a single answer. `redirect: "error"` matters: an IdP
 * endpoint that 302s somewhere else is either a misconfiguration or an attempt to walk this process
 * onto a different host, and following it silently is how an SSRF starts.
 */
async function idpFetch(url: string, init: RequestInit = {}): Promise<Response> {
  const signal = AbortSignal.timeout(OIDC_TIMEOUT_MS);
  return fetch(url, { ...init, signal, redirect: "error", cache: "no-store" });
}

type Discovery = { issuer: string; authorization_endpoint: string; token_endpoint: string; jwks_uri: string };

type Cached<T> = { at: number; value: T };
const DISCOVERY_CACHE = Symbol.for("heros.console.idp.discovery");
const JWKS_CACHE = Symbol.for("heros.console.idp.jwks");
type MetaGlobal = typeof globalThis & { [DISCOVERY_CACHE]?: Cached<Discovery>; [JWKS_CACHE]?: Cached<Jwk[]> };

/** IdpUnreachableError is the fail-closed signal Decision 8 requires. No session is issued on it. */
export class IdpUnreachableError extends Error {
  constructor(detail: string) {
    super(`identity provider unreachable: ${detail}`);
    this.name = "IdpUnreachableError";
  }
}

/**
 * discover fetches and validates the issuer's discovery document.
 *
 * The `issuer` field is compared against the configured issuer, and a mismatch is a refusal. That
 * check is the whole reason discovery is safe to follow at all: without it, a compromised or
 * mis-typed discovery URL could point this console's authorization and token endpoints at an
 * attacker's host, and every subsequent check would pass against the attacker's own keys.
 */
export async function discover(): Promise<Discovery> {
  const scope = globalThis as MetaGlobal;
  const cached = scope[DISCOVERY_CACHE];
  if (cached && Date.now() - cached.at < METADATA_TTL_MS) return cached.value;

  const base = CONFIG.issuer.replace(/\/+$/, "");
  let res: Response;
  try {
    res = await idpFetch(`${base}/.well-known/openid-configuration`);
  } catch (err) {
    throw new IdpUnreachableError(err instanceof Error ? err.message : "discovery failed");
  }
  if (!res.ok) throw new IdpUnreachableError(`discovery returned ${res.status}`);
  const doc = (await res.json().catch(() => null)) as Partial<Discovery> | null;
  if (!doc || typeof doc !== "object") throw new IdpUnreachableError("discovery document is not JSON");
  for (const field of ["issuer", "authorization_endpoint", "token_endpoint", "jwks_uri"] as const) {
    if (typeof doc[field] !== "string" || !doc[field]) throw new IdpUnreachableError(`discovery document has no ${field}`);
  }
  if (doc.issuer !== CONFIG.issuer) {
    throw new IdpUnreachableError("discovery document declares a different issuer than the configured one");
  }
  const value = doc as Discovery;
  scope[DISCOVERY_CACHE] = { at: Date.now(), value };
  return value;
}

/** jwks fetches the issuer's signing keys. Same fail-closed rule: a failed refresh is not a stale serve. */
export async function jwks(): Promise<Jwk[]> {
  const scope = globalThis as MetaGlobal;
  const cached = scope[JWKS_CACHE];
  if (cached && Date.now() - cached.at < METADATA_TTL_MS) return cached.value;

  const meta = await discover();
  let res: Response;
  try {
    res = await idpFetch(meta.jwks_uri);
  } catch (err) {
    throw new IdpUnreachableError(err instanceof Error ? err.message : "JWKS fetch failed");
  }
  if (!res.ok) throw new IdpUnreachableError(`JWKS returned ${res.status}`);
  const doc = (await res.json().catch(() => null)) as { keys?: Jwk[] } | null;
  if (!doc || !Array.isArray(doc.keys) || doc.keys.length === 0) throw new IdpUnreachableError("JWKS holds no keys");
  scope[JWKS_CACHE] = { at: Date.now(), value: doc.keys };
  return doc.keys;
}

/**
 * authorizationUrl builds the authorization request for a begun flow.
 *
 * `response_type=code` and nothing else — there is no branch that can produce `id_token` or `token`,
 * so the implicit flow is not a configuration mistake this deployment can make.
 */
export async function authorizationUrl(flow: { state: string; nonce: string; codeVerifier: string; redirectUri: string }): Promise<string> {
  const meta = await discover();
  const url = new URL(meta.authorization_endpoint);
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", CONFIG.clientId);
  url.searchParams.set("redirect_uri", flow.redirectUri);
  url.searchParams.set("scope", "openid email");
  url.searchParams.set("state", flow.state);
  url.searchParams.set("nonce", flow.nonce);
  url.searchParams.set("code_challenge", pkceChallenge(flow.codeVerifier));
  // S256 only. `plain` sends the verifier itself, which is not a proof of anything.
  url.searchParams.set("code_challenge_method", "S256");
  return url.toString();
}

/**
 * exchangeCode redeems an authorization code for an ID token.
 *
 * The client secret is resolved through the `Secrets` seam at the MOMENT OF USE and is never held in a
 * module-level constant: a value fetched once at boot survives a rotation, and a value in a constant
 * is a value a stack trace can print.
 *
 * The returned ID token is a raw credential. Every caller of this function drops it after
 * `verifyIdToken` — it is never returned above the seam.
 */
export async function exchangeCode(input: { code: string; codeVerifier: string; redirectUri: string }): Promise<string> {
  const meta = await discover();
  const secret = await identitySecret(SECRET_OIDC_CLIENT_SECRET);
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code: input.code,
    redirect_uri: input.redirectUri,
    client_id: CONFIG.clientId,
    client_secret: secret,
    code_verifier: input.codeVerifier,
  });
  let res: Response;
  try {
    res = await idpFetch(meta.token_endpoint, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded", accept: "application/json" },
      body,
    });
  } catch (err) {
    throw new IdpUnreachableError(err instanceof Error ? err.message : "token exchange failed");
  }
  // A non-2xx is NOT unreachability — a reused or forged code lands here, and calling that "the IdP is
  // down" would page an operator for an attacker's failed attempt. It is an ordinary refusal.
  if (!res.ok) throw new Error("token exchange refused");
  const doc = (await res.json().catch(() => null)) as { id_token?: string } | null;
  const idToken = doc?.id_token;
  if (typeof idToken !== "string" || !idToken) throw new Error("token response carried no ID token");
  return idToken;
}

export type OidcOutcome = { ok: true; claims: FederatedClaims } | { ok: false; cause: string };

/**
 * verifyIdToken validates an ID token and returns the four contract claims.
 *
 * Order matters and is the security property: signature, then issuer, then audience, then nonce, then
 * the time bounds. Checking the nonce before the signature would let an unsigned token consume the
 * flow's one-time nonce; checking the audience after the bounds would spend clock on a token minted
 * for somebody else.
 */
export async function verifyIdToken(idToken: string, expectedNonce: string): Promise<OidcOutcome> {
  const keys = await jwks();
  const verified = verifyCompactJws<Record<string, unknown>>(idToken, keys);
  if (!verified.ok) return { ok: false, cause: verified.cause };
  const p = verified.payload;

  const issuer = typeof p.iss === "string" ? p.iss : "";
  if (issuer !== CONFIG.issuer) return { ok: false, cause: "ID token issuer is not the configured issuer" };

  // `aud` is a string or an array of strings. An array containing our client id is valid OIDC; an
  // array we are merely mentioned in with an `azp` naming somebody else is not, which is why `azp` is
  // checked when present rather than ignored.
  const aud = Array.isArray(p.aud) ? p.aud.map(String) : typeof p.aud === "string" ? [p.aud] : [];
  if (!aud.includes(CONFIG.clientId)) return { ok: false, cause: "ID token audience is not this client" };
  if (typeof p.azp === "string" && p.azp !== CONFIG.clientId) {
    return { ok: false, cause: "ID token authorized party is a different client" };
  }

  const nonce = typeof p.nonce === "string" ? p.nonce : "";
  if (!constantTimeEqual(nonce, expectedNonce)) return { ok: false, cause: "ID token nonce does not bind to this flow" };

  const subject = typeof p.sub === "string" ? p.sub.trim() : "";
  if (!subject) return { ok: false, cause: "ID token carries no subject" };

  const claims: FederatedClaims = {
    issuer,
    subject,
    ...emailClaim(typeof p.email === "string" ? p.email : undefined, p.email_verified === true),
    assertionId: jwsIdentifier(idToken, p),
    issuedAt: typeof p.iat === "number" ? p.iat : undefined,
    notBefore: typeof p.nbf === "number" ? p.nbf : undefined,
    expiresAt: typeof p.exp === "number" ? p.exp : undefined,
  };
  const fresh = checkFreshness(claims, Date.now());
  if (!fresh.ok) return { ok: false, cause: fresh.cause };
  return { ok: true, claims };
}

/**
 * ensureReachable confirms the IdP answers RIGHT NOW, and throws `IdpUnreachableError` if it does not.
 *
 * # The defect this exists for, found by the fail-closed test rather than by reading
 *
 * `authorizationUrl` reads discovery, and discovery is cached for five minutes. So an IdP that died
 * four minutes ago still yielded a perfectly-formed authorization URL, and `/auth/login` cheerfully
 * redirected the user onto a dead host. No session was issued — the token exchange could never
 * succeed — so the letter of "fail closed" held. What did not hold is the part that matters to the
 * person: they were dropped on somebody else's error page with no way to tell whose fault it was, and
 * our own sign-in surface never learned anything was wrong.
 *
 * The cache is cleared rather than consulted, because a readiness answer from a five-minute-old cache
 * is a report about the past. The refetch repopulates it, so the callback leg still runs warm and the
 * cost is one round trip at the START of a sign-in — which is rare, and is exactly the moment worth
 * spending it.
 */
export async function ensureReachable(): Promise<void> {
  const scope = globalThis as MetaGlobal;
  delete scope[DISCOVERY_CACHE];
  delete scope[JWKS_CACHE];
  await jwks();
}

/**
 * reachable probes the IdP for `/readyz` (task 7.1, Decision 8).
 *
 * It measures REACHABILITY — can discovery and the key set be fetched and validated — not traffic
 * freshness, and it does not depend on the traffic it gates. A console with no sign-ins all night is
 * not therefore unhealthy, and a console whose IdP died an hour ago is, which is the correct pair.
 *
 * The cache is deliberately bypassed for the probe's own fetch by asking for the key set: a readiness
 * signal that answers from a five-minute-old cache is reporting the past.
 */
export async function reachable(): Promise<{ reachable: boolean; detail?: string }> {
  try {
    await ensureReachable();
    return { reachable: true };
  } catch (err) {
    return { reachable: false, detail: err instanceof Error ? err.message : "unreachable" };
  }
}
