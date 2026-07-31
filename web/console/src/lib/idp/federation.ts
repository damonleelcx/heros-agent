import "server-only";

/**
 * federation.ts is THE federation contract (P22 task 1.2), and the only place it exists.
 *
 * # Why one module and not two agreeing ones
 *
 * OIDC and SAML are two encodings of the same three questions: who signed this, what did they say
 * about the subject, and how long is that true for. Written twice, the second copy drifts — and the
 * drift is invisible, because each half passes its own tests while the pair no longer means one thing.
 * A federation whose OIDC verifier accepts a five-minute-old assertion and whose SAML verifier accepts
 * a two-minute-old one does not have a freshness bound; it has two, and an attacker uses the looser.
 *
 * So the verifiers in `oidc.ts` and `saml.ts` DERIVE from this file. Neither defines a bound, a claim
 * name, an issuer rule or a refusal string of its own.
 *
 * # What deliberately is not here
 *
 * Nothing that reads the network, no crypto, no Next imports. This module is the contract; the
 * encodings are elsewhere. That split is what lets the tenant-resolution rule — the one NFR9 rests on
 * — be read end to end on one screen.
 */

// ── Bounds ──────────────────────────────────────────────────────────────────────────────────────

/**
 * MAX_ASSERTION_AGE_SECONDS bounds how old an assertion may be when it reaches the callback.
 *
 * 120 seconds, deliberately the same number as `adminidentity.MaxAssertionAge` on the operator side.
 * The exchange between the IdP's redirect and our callback is a network round trip, not a workflow,
 * and two spellings of "fresh" in one platform is how one of them stops being enforced.
 */
export const MAX_ASSERTION_AGE_SECONDS = 120;

/**
 * CLOCK_SKEW_SECONDS is the ONLY tolerance, applied symmetrically to both ends of every window.
 *
 * A federated IdP's clock is not ours and never will be. Sixty seconds makes a slightly-fast IdP
 * usable without making a stale assertion fresh — and being explicit about it means nobody later
 * "fixes" an intermittent failure by widening a bound in one verifier.
 */
export const CLOCK_SKEW_SECONDS = 60;

/**
 * MAX_FLOW_AGE_SECONDS bounds how long a begun sign-in may take to come back.
 *
 * It is the lifetime of the server-side `state`/PKCE record. Ten minutes covers a user who has to
 * complete an MFA challenge at their own IdP; anything older is refused and its record dropped, so the
 * flow store cannot become an unbounded pile of live CSRF tokens.
 */
export const MAX_FLOW_AGE_SECONDS = 600;

/**
 * REFUSAL is the single generic reason every identity failure returns (Decision 9).
 *
 * One string for "unknown issuer", "bad signature", "stale assertion", "replayed state" and "no
 * mapping". Distinguishing them builds a probing oracle that tells an attacker which half they got
 * wrong and helps no real user. The CAUSE is recorded server-side as a security event, where an
 * operator can read it and an attacker cannot.
 */
export const REFUSAL = "that sign-in was not accepted";

// ── The claims that cross the seam ──────────────────────────────────────────────────────────────

/**
 * FederatedClaims is everything P22 reads out of an assertion. Four fields, and that is the contract.
 *
 * Groups, roles, display names, `amr`, and every custom attribute an IdP might send are NOT read. A
 * claim we read is a claim a customer's IdP administrator can change to move authority inside our
 * platform, and P22 has exactly one authority decision to make: which tenant. Reading more would hand
 * that decision to a directory we do not control.
 */
export type FederatedClaims = {
  /** The OIDC `iss` or the SAML `entityID`. Selects the registration; never trusted for anything else. */
  issuer: string;
  /** The OIDC `sub` or the SAML `NameID`. The stable identity handle. */
  subject: string;
  /**
   * The verified email, or undefined.
   *
   * Undefined when the IdP did not say it verified it (`email_verified` absent or false). An
   * unverified email is a self-asserted string, and mapping a tenant off one is exactly the
   * cross-tenant hole NFR9 exists to close.
   */
  email?: string;
  /** `email`'s domain, lowercased. Derived here so two verifiers cannot derive it two ways. */
  emailDomain?: string;
  /**
   * The assertion's own identifier — OIDC `jti` (or a digest when the IdP omits it) / SAML
   * `AssertionID`. The key of the one-time guard.
   */
  assertionId: string;
  /** Seconds since the epoch. Absent bounds are a refusal, never a default (see `checkFreshness`). */
  issuedAt?: number;
  notBefore?: number;
  expiresAt?: number;
};

/**
 * emailClaim normalises a verified email into `{ email, emailDomain }`, or into nothing.
 *
 * `verified` is a required argument rather than an optional one: a caller that has no verification
 * signal has to pass `false` and see itself do it. An optional parameter defaulting to true is how an
 * unverified claim becomes a tenant.
 */
export function emailClaim(raw: string | undefined, verified: boolean): { email?: string; emailDomain?: string } {
  if (!verified) return {};
  const value = (raw ?? "").trim().toLowerCase();
  const at = value.lastIndexOf("@");
  // A single `@` with something on both sides. Not an RFC 5322 validator — this is a mapping key, and
  // the only thing that matters is that the domain half is unambiguous.
  if (at <= 0 || at === value.length - 1 || value.indexOf("@") !== at) return {};
  return { email: value, emailDomain: value.slice(at + 1) };
}

// ── Freshness ───────────────────────────────────────────────────────────────────────────────────

export type FreshnessOutcome = { ok: true } | { ok: false; cause: string };

/**
 * checkFreshness applies the one set of bounds to either encoding.
 *
 * A missing bound is a REFUSAL, not a default (🔴 `no-lazy-defaults`). Defaulting `expiresAt` when the
 * IdP omitted it means inventing a lifetime on the IdP's behalf — and the lifetime we would invent is
 * the one an attacker would ask for.
 */
export function checkFreshness(claims: FederatedClaims, nowMs: number): FreshnessOutcome {
  const now = Math.floor(nowMs / 1000);
  if (claims.expiresAt === undefined) return { ok: false, cause: "assertion carries no expiry" };
  if (now > claims.expiresAt + CLOCK_SKEW_SECONDS) return { ok: false, cause: "assertion expired" };
  if (claims.notBefore !== undefined && now < claims.notBefore - CLOCK_SKEW_SECONDS) {
    return { ok: false, cause: "assertion not yet valid" };
  }
  if (claims.issuedAt !== undefined) {
    if (now < claims.issuedAt - CLOCK_SKEW_SECONDS) return { ok: false, cause: "assertion issued in the future" };
    if (now - claims.issuedAt > MAX_ASSERTION_AGE_SECONDS + CLOCK_SKEW_SECONDS) {
      return { ok: false, cause: "assertion older than the freshness window" };
    }
  }
  return { ok: true };
}

// ── The trusted issuer set and the tenant map ───────────────────────────────────────────────────

/** IssuerRegistration is one federated IdP, and the tenant it — and only it — resolves to. */
export type IssuerRegistration = {
  /** The exact `iss` / `entityID`. */
  issuer: string;
  /** The one tenant this registration resolves to. */
  tenant: string;
  /**
   * The domains THIS registration has proven it owns.
   *
   * The nesting is the whole of NFR9. A flat `domain → tenant` table would let any federated IdP claim
   * any tenant's domain by minting one `email` claim; hanging the domain off the issuer entry means
   * "IdP A asserts an acme.com address" resolves to tenant B only when A is B's registered issuer.
   */
  verifiedDomains: string[];
  /** Domains this registration may just-in-time provision into its tenant. Empty by default. */
  jitAllow: string[];
};

export type MappingStrategy = "domain" | "per-issuer" | "jit";

export type TenantMap = {
  strategy: MappingStrategy;
  /** Keyed by issuer. A `Map`, so a crafted issuer like `__proto__` cannot reach an object prototype. */
  issuers: Map<string, IssuerRegistration>;
};

const STRATEGIES: readonly MappingStrategy[] = ["domain", "per-issuer", "jit"];

/**
 * parseTenantMap validates the injected mapping document, or throws.
 *
 * # Why this throws at load rather than refusing at sign-in
 *
 * ADR-004 fail-static, and the same argument `CONSOLE_TENANT_ASSERTIONS` already makes: a console that
 * boots and then refuses every login looks like a broken product to everyone who sees it, while a
 * console that refuses to boot says exactly what is wrong, once, to the person doing the deploy. The
 * worse case is the third one — a console that boots and maps LOOSELY because a malformed entry was
 * skipped — which is why every field below is required rather than tolerated.
 *
 * It is loud about the FACT and silent about the VALUE: the map names tenants and issuers, and an
 * error message that echoed it would put the deployment's federation topology in a log aggregator.
 */
export function parseTenantMap(raw: string | undefined): TenantMap | null {
  if (!raw || !raw.trim()) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("CONSOLE_IDP_TENANT_MAP is set but is not valid JSON");
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("CONSOLE_IDP_TENANT_MAP is set but is not a JSON object");
  }
  const doc = parsed as Record<string, unknown>;
  const strategy = String(doc.strategy ?? "");
  if (!STRATEGIES.includes(strategy as MappingStrategy)) {
    throw new Error(`CONSOLE_IDP_TENANT_MAP.strategy must be one of ${STRATEGIES.join(", ")}`);
  }
  const issuersDoc = doc.issuers;
  if (!issuersDoc || typeof issuersDoc !== "object" || Array.isArray(issuersDoc)) {
    throw new Error("CONSOLE_IDP_TENANT_MAP.issuers must be a JSON object of issuer → registration");
  }
  const issuers = new Map<string, IssuerRegistration>();
  for (const [key, value] of Object.entries(issuersDoc as Record<string, unknown>)) {
    const issuer = key.trim();
    if (!issuer) throw new Error("CONSOLE_IDP_TENANT_MAP has an empty issuer key");
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      throw new Error("CONSOLE_IDP_TENANT_MAP has an issuer whose registration is not an object");
    }
    const entry = value as Record<string, unknown>;
    const tenant = String(entry.tenant ?? "").trim();
    if (!tenant) throw new Error("CONSOLE_IDP_TENANT_MAP has an issuer registration with no tenant");
    issuers.set(issuer, {
      issuer,
      tenant,
      verifiedDomains: domainList(entry.verified_domains, "verified_domains"),
      jitAllow: domainList(entry.jit_allow, "jit_allow"),
    });
  }
  if (issuers.size === 0) throw new Error("CONSOLE_IDP_TENANT_MAP.issuers is empty — no IdP would be trusted");
  return { strategy: strategy as MappingStrategy, issuers };
}

function domainList(value: unknown, field: string): string[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(`CONSOLE_IDP_TENANT_MAP.${field} must be an array of domains`);
  return value.map((d) => {
    const domain = String(d ?? "").trim().toLowerCase();
    if (!domain) throw new Error(`CONSOLE_IDP_TENANT_MAP.${field} contains an empty domain`);
    return domain;
  });
}

/**
 * trustedIssuer returns the registration for an issuer, or null.
 *
 * Exact string match after trimming. No wildcard, no suffix rule, no "any issuer whose signature
 * validates": a signature proves that SOMEONE signed, and the issuer set is what turns that into
 * someone we federate with. `okta.com.attacker.example` is a suffix match away from being trusted in
 * any implementation that reaches for `endsWith`.
 */
export function trustedIssuer(map: TenantMap, issuer: string): IssuerRegistration | null {
  return map.issuers.get(issuer.trim()) ?? null;
}

// ── Tenant resolution — the NFR9 surface ────────────────────────────────────────────────────────

export type TenantResolution =
  | { ok: true; tenantId: string; provisioned: boolean }
  | { ok: false; cause: string };

/**
 * resolveTenant maps verified claims to exactly one tenant, or refuses.
 *
 * Read the order, because it is the security property:
 *
 *   1. The issuer must be REGISTERED. An unregistered issuer never reaches a domain comparison, so a
 *      self-asserted `email` from a foreign IdP has nothing to match against.
 *   2. Only then is the email domain compared, and only against THIS registration's own
 *      `verifiedDomains`. Tenant B's domain is unreachable from tenant A's IdP by construction —
 *      there is no code path that searches other registrations.
 *   3. JIT is a per-registration allow rule, never a fallback. An identity matching nothing is a
 *      REFUSAL and a security event, not a signup.
 *
 * The three strategies differ only in step 2/3; step 1 is unconditional, which is why "cross-tenant
 * resolution" is not a case this function has to handle — it is a case it cannot express.
 */
export function resolveTenant(map: TenantMap, claims: FederatedClaims): TenantResolution {
  const registration = trustedIssuer(map, claims.issuer);
  if (!registration) return { ok: false, cause: "issuer is not registered" };

  switch (map.strategy) {
    case "per-issuer":
      // The registration IS the mapping. Nothing about the subject can change which tenant this is.
      return { ok: true, tenantId: registration.tenant, provisioned: false };

    case "domain": {
      if (!claims.emailDomain) return { ok: false, cause: "no verified email domain in the assertion" };
      if (!registration.verifiedDomains.includes(claims.emailDomain)) {
        return { ok: false, cause: "email domain is not a verified domain of the registered issuer" };
      }
      return { ok: true, tenantId: registration.tenant, provisioned: false };
    }

    case "jit": {
      if (!claims.emailDomain) return { ok: false, cause: "no verified email domain in the assertion" };
      if (registration.verifiedDomains.includes(claims.emailDomain)) {
        return { ok: true, tenantId: registration.tenant, provisioned: false };
      }
      if (registration.jitAllow.includes(claims.emailDomain)) {
        // Provisioned, and SAID so: the caller records a distinct security event for a first sight of
        // an identity, because "a new person appeared in tenant X" is a thing an operator reads.
        return { ok: true, tenantId: registration.tenant, provisioned: true };
      }
      return { ok: false, cause: "email domain is on no verified or JIT-allowed list for the registered issuer" };
    }
  }
}
