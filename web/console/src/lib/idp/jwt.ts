import "server-only";
import { createHash, createPublicKey, verify as cryptoVerify, constants, timingSafeEqual } from "node:crypto";

/**
 * jwt.ts verifies a compact JWS against a JWKS, and does nothing else.
 *
 * # Why this is written rather than imported
 *
 * The console ships four runtime dependencies and a build-time credential fence that reads the bundle
 * it produces (`scripts/scan-bundle.mjs`). Every dependency added to a browser-facing BFF is code
 * inside the credential boundary that nobody in this repository has read. For the ~150 lines below —
 * a header parse, an algorithm allowlist, a key lookup and one `crypto.verify` call — that trade is
 * not worth making, and Node's own `crypto` already implements every hard part.
 *
 * # The three mistakes a hand-written verifier makes, and where each is closed
 *
 * 1. **`alg: "none"`, or an algorithm the token chooses.** The token is not allowed to pick: `ALLOWED`
 *    is an allowlist, and `none` is not in it. This is the single most-exploited JWT defect.
 * 2. **HMAC confusion** — an attacker signs with the RSA public key as an HMAC secret and a verifier
 *    that dispatches on `alg` accepts it. No `HS*` algorithm exists here at all, so the confusion has
 *    no landing site.
 * 3. **Trusting the header's `kid` to select any key.** The key comes from the JWKS the ISSUER's
 *    discovery document named; `kid` only chooses among those. A JWKS with no matching `kid` and one
 *    key is allowed to fall through to that key (IdPs do rotate without publishing `kid`), but a JWKS
 *    with several keys and no match is a refusal, not a loop that tries them all.
 */

/**
 * ALLOWED maps a JWS algorithm to how Node verifies it. An allowlist, so a token cannot name its way
 * out — and deliberately asymmetric-only, so an HMAC algorithm has nowhere to land.
 */
const ALLOWED: Record<string, { hash: string; options?: Record<string, unknown>; kty: string }> = {
  RS256: { hash: "sha256", kty: "RSA" },
  RS384: { hash: "sha384", kty: "RSA" },
  RS512: { hash: "sha512", kty: "RSA" },
  PS256: { hash: "sha256", kty: "RSA", options: { padding: constants.RSA_PKCS1_PSS_PADDING, saltLength: constants.RSA_PSS_SALTLEN_DIGEST } },
  PS384: { hash: "sha384", kty: "RSA", options: { padding: constants.RSA_PKCS1_PSS_PADDING, saltLength: constants.RSA_PSS_SALTLEN_DIGEST } },
  PS512: { hash: "sha512", kty: "RSA", options: { padding: constants.RSA_PKCS1_PSS_PADDING, saltLength: constants.RSA_PSS_SALTLEN_DIGEST } },
  // `ieee-p1363` is JWS's raw R‖S signature encoding. Without it Node expects DER and every valid
  // ECDSA token fails verification — a failure that looks exactly like a wrong key.
  ES256: { hash: "sha256", kty: "EC", options: { dsaEncoding: "ieee-p1363" } },
  ES384: { hash: "sha384", kty: "EC", options: { dsaEncoding: "ieee-p1363" } },
  ES512: { hash: "sha512", kty: "EC", options: { dsaEncoding: "ieee-p1363" } },
};

/** JsonWebKey is the subset of a JWKS entry this module reads. */
export type Jwk = { kty?: string; kid?: string; use?: string; alg?: string; n?: string; e?: string; crv?: string; x?: string; y?: string };

export type JwsOutcome<T> = { ok: true; header: Record<string, unknown>; payload: T } | { ok: false; cause: string };

function decodeSegment(segment: string): Buffer {
  return Buffer.from(segment, "base64url");
}

function parseJson(buf: Buffer): unknown {
  try {
    return JSON.parse(buf.toString("utf8"));
  } catch {
    return null;
  }
}

/**
 * verifyCompactJws checks a `header.payload.signature` token against a JWKS.
 *
 * It returns the parsed payload and does NOT interpret it: `iss`, `aud`, `nonce` and the time bounds
 * are the federation contract's business (`federation.ts`), not the signature layer's. Keeping the two
 * apart is what stops a "just check the audience here too" from quietly becoming the only audience
 * check, in a file the contract's tests do not read.
 */
export function verifyCompactJws<T = Record<string, unknown>>(token: string, keys: Jwk[]): JwsOutcome<T> {
  const parts = token.split(".");
  if (parts.length !== 3) return { ok: false, cause: "not a compact JWS" };
  const [headerB64, payloadB64, signatureB64] = parts;

  const header = parseJson(decodeSegment(headerB64));
  if (!header || typeof header !== "object") return { ok: false, cause: "unreadable JWS header" };
  const head = header as Record<string, unknown>;

  const alg = String(head.alg ?? "");
  const spec = ALLOWED[alg];
  // `alg: "none"` and every HMAC algorithm land here, because they are simply absent from the map.
  if (!spec) return { ok: false, cause: "unsupported signing algorithm" };

  const kid = head.kid === undefined ? undefined : String(head.kid);
  const candidates = keys.filter((k) => (k.kty ?? "") === spec.kty && (k.use ?? "sig") === "sig");
  const matched = kid ? candidates.filter((k) => k.kid === kid) : candidates;
  // One key and no `kid` match is a rotation an IdP performed without republishing `kid`s — usable.
  // Several keys and no match is a refusal: trying them all turns a key set into an oracle.
  const usable = matched.length > 0 ? matched : candidates.length === 1 ? candidates : [];
  if (usable.length === 0) return { ok: false, cause: "no signing key matches the token" };

  const signed = Buffer.from(`${headerB64}.${payloadB64}`, "ascii");
  const signature = decodeSegment(signatureB64);
  let verified = false;
  for (const jwk of usable) {
    let key;
    try {
      key = createPublicKey({ key: jwk as Record<string, unknown>, format: "jwk" });
    } catch {
      continue; // a malformed key in the set is skipped, never a reason to accept the token
    }
    try {
      if (cryptoVerify(spec.hash, signed, { key, ...(spec.options ?? {}) }, signature)) {
        verified = true;
        break;
      }
    } catch {
      continue;
    }
  }
  if (!verified) return { ok: false, cause: "signature did not verify" };

  const payload = parseJson(decodeSegment(payloadB64));
  if (!payload || typeof payload !== "object") return { ok: false, cause: "unreadable JWS payload" };
  return { ok: true, header: head, payload: payload as T };
}

/**
 * jwsIdentifier returns a stable identifier for a token — its `jti` when present, otherwise a digest.
 *
 * The one-time replay guard needs a key. Many IdPs omit `jti` from an ID token, and "no `jti` means no
 * replay check" would silently disable the guard for exactly those deployments. A SHA-256 of the
 * signature is a correct substitute: two distinct assertions cannot share one, and it reveals nothing
 * a holder of the token does not already have.
 */
export function jwsIdentifier(token: string, payload: Record<string, unknown>): string {
  const jti = typeof payload.jti === "string" ? payload.jti.trim() : "";
  if (jti) return jti;
  return createHash("sha256").update(token.split(".")[2] ?? token).digest("base64url");
}

/**
 * constantTimeEqual compares two short secrets without leaking their divergence point.
 *
 * Used for `state` and `nonce`. Both are 256-bit CSPRNG values, so a timing oracle is a stretch — but
 * `===` on a credential is the kind of line that gets copied to a place where it matters, and the
 * length guard also stops `timingSafeEqual` from throwing on a crafted short value.
 */
export function constantTimeEqual(a: string, b: string): boolean {
  const left = Buffer.from(a, "utf8");
  const right = Buffer.from(b, "utf8");
  if (left.length !== right.length || left.length === 0) return false;
  return timingSafeEqual(left, right);
}
