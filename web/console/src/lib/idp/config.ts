import "server-only";
import { parseTenantMap, type TenantMap } from "./federation";

/**
 * config.ts binds the identity configuration at RUNTIME, never at build time (P22 task 3.4, ADR-004).
 *
 * # Why this matters more here than anywhere else in the console
 *
 * One image has to federate against Okta for one customer, Entra for another and a self-hosted Keycloak
 * for a third. If the issuer, the client id, the redirect allowlist or the mapping strategy were baked
 * into the bundle, "onboard a customer" would mean "cut a release", and the image an operator verified
 * would not be the image they run. Everything below is therefore read from the ENVIRONMENT of the
 * running process, and nothing identity-shaped is a compile-time constant.
 *
 * # Fail static, at load
 *
 * A malformed allowlist or an unknown strategy throws HERE, at module load, exactly as
 * `CONSOLE_TENANT_ASSERTIONS` does today. The failure mode being avoided is not "boots and refuses
 * every login" — that one is at least honest — it is "boots and federates LOOSELY because a bad entry
 * was skipped".
 */

/**
 * IdentityProviderKind is the seam's implementation in force.
 *
 * `platform` verifies the presented credential against the platform's own `/api/v1/whoami` — the same
 * token `heros login` authenticates and `heros link` transmits with. It exists because the console
 * previously demanded a SECOND secret that no CLI command could produce, which made the dashboard URL
 * `heros link` prints unopenable by the person who ran it. See `idp/platformToken.ts`.
 */
export type IdentityProviderKind = "oidc" | "saml" | "configured" | "platform" | "dev";

const KINDS: readonly IdentityProviderKind[] = ["oidc", "saml", "configured", "platform", "dev"];

function requireEnv(name: string, kind: IdentityProviderKind): string {
  const value = (process.env[name] ?? "").trim();
  if (!value) {
    throw new Error(`${name} is required when CONSOLE_TENANT_IDENTITY=${kind} — an unset value would federate with nobody`);
  }
  return value;
}

/**
 * urlAllowlist parses an absolute-URL allowlist, or throws.
 *
 * Three refusals, each closing a specific hole:
 *   - a non-absolute entry, because a relative "allowlist" compares nothing;
 *   - a `*` anywhere, because a wildcard redirect target is an open redirect by construction
 *     (Decision 9) and the point of the list is that it is exhaustive;
 *   - a URL carrying a query or fragment, because the comparison is by ORIGIN+PATH and an entry with
 *     a query would silently never match, leaving an operator staring at a correct-looking config.
 */
function urlAllowlist(name: string, kind: IdentityProviderKind): string[] {
  const raw = requireEnv(name, kind);
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error(`${name} must be a JSON array of absolute URLs`);
  }
  if (!Array.isArray(parsed) || parsed.length === 0) {
    throw new Error(`${name} must be a non-empty JSON array of absolute URLs`);
  }
  return parsed.map((entry) => {
    const value = String(entry ?? "").trim();
    if (value.includes("*")) throw new Error(`${name} contains a wildcard — a wildcard redirect target is an open redirect`);
    let url: URL;
    try {
      url = new URL(value);
    } catch {
      throw new Error(`${name} contains an entry that is not an absolute URL`);
    }
    if (url.search || url.hash) throw new Error(`${name} entries carry no query or fragment`);
    // Normalised to origin+pathname so the comparison at the callback is a string equality on the
    // same shape the browser will present, rather than a URL-object comparison that differs on a
    // trailing slash.
    return url.origin + url.pathname;
  });
}

/** IdentityConfig is the whole runtime identity surface. Frozen once, at load. */
export type IdentityConfig = {
  kind: IdentityProviderKind;
  /** The tenant map. Null for `configured`/`dev`, which map by assertion string rather than by claim. */
  tenantMap: TenantMap | null;
  /** OIDC: the issuer, used as the discovery base and compared against `iss`. */
  issuer: string;
  /** OIDC: the public client identifier. Not a secret — the client SECRET never leaves `secrets.ts`. */
  clientId: string;
  /** OIDC: the exact redirect URIs this deployment may be sent back to. */
  redirectAllowlist: string[];
  /** SAML: this SP's entityID, enforced as the assertion's audience restriction. */
  spEntityId: string;
  /** SAML: the exact ACS URLs a response may be accepted at. */
  acsAllowlist: string[];
  /** SAML: where the IdP's metadata (signing certificates) is fetched from. */
  metadataUrl: string;
};

function load(): IdentityConfig {
  const kindRaw = (process.env.CONSOLE_TENANT_IDENTITY ?? "configured").trim().toLowerCase();
  if (!KINDS.includes(kindRaw as IdentityProviderKind)) {
    throw new Error(`CONSOLE_TENANT_IDENTITY must be one of ${KINDS.join(", ")}`);
  }
  const kind = kindRaw as IdentityProviderKind;

  const base: IdentityConfig = {
    kind,
    tenantMap: null,
    issuer: "",
    clientId: "",
    redirectAllowlist: [],
    spEntityId: "",
    acsAllowlist: [],
    metadataUrl: "",
  };
  // `platform` needs no tenant map and no allowlist: the platform IS the map, and there is no redirect
  // flow to protect. Its one dependency (PLATFORM_API_BASE) is the BFF's own, already required for the
  // console to render anything at all — so there is nothing here that could be half-configured.
  if (kind === "configured" || kind === "platform" || kind === "dev") return base;

  // A federated deployment MUST carry a tenant map: without one there is no rule that resolves a
  // claim to a tenant, and the only honest behaviours are "refuse to boot" and "authenticate nobody".
  const tenantMap = parseTenantMap(process.env.CONSOLE_IDP_TENANT_MAP);
  if (!tenantMap) {
    throw new Error(`CONSOLE_IDP_TENANT_MAP is required when CONSOLE_TENANT_IDENTITY=${kind}`);
  }

  if (kind === "oidc") {
    return {
      ...base,
      tenantMap,
      issuer: requireEnv("CONSOLE_IDP_ISSUER", kind),
      clientId: requireEnv("CONSOLE_IDP_CLIENT_ID", kind),
      redirectAllowlist: urlAllowlist("CONSOLE_IDP_REDIRECT_ALLOWLIST", kind),
    };
  }
  return {
    ...base,
    tenantMap,
    issuer: requireEnv("CONSOLE_SAML_IDP_ENTITY_ID", kind),
    spEntityId: requireEnv("CONSOLE_SAML_SP_ENTITY_ID", kind),
    acsAllowlist: urlAllowlist("CONSOLE_SAML_ACS_ALLOWLIST", kind),
    metadataUrl: requireEnv("CONSOLE_SAML_IDP_METADATA_URL", kind),
  };
}

/**
 * CONFIG is read once at module load rather than per request.
 *
 * A deployment does not change its identity provider between two requests, and reading per request
 * would make the production guard in `identity.ts` depend on the timing of the first sign-in — the
 * same argument the pre-P22 seam already made for `PROVIDER`.
 */
export const CONFIG: IdentityConfig = load();

/**
 * canonicalCallback is the callback URL this deployment SENDS — the first allowlist entry.
 *
 * # Why it is derived from the allowlist rather than configured separately
 *
 * A second variable that has to agree with the list is a second variable that can disagree with it,
 * and the failure is silent: the flow begins with a `redirect_uri` the IdP accepts and the callback
 * then refuses its own response. Making the list's head canonical means "the URL we send" and "the
 * URLs we accept" cannot drift, and the remaining entries exist for the real case that needs several —
 * a deployment mid-hostname-migration, which must accept the old callback while sending the new one.
 *
 * It is deliberately NOT reconstructed from the request. `lib/redirect.ts` records why at length: the
 * host this process believes it has and the host in the user's address bar are not reliably the same
 * one, and an identity flow that guesses wrong fails in a browser while passing every test.
 */
export function canonicalCallback(): string {
  const list = CONFIG.kind === "saml" ? CONFIG.acsAllowlist : CONFIG.redirectAllowlist;
  if (list.length === 0) throw new Error("no callback is configured for this identity provider");
  return list[0];
}

/**
 * allowedRedirect returns the allowlisted entry equal to `candidate`, or null (task 5.2, Decision 9).
 *
 * Exact origin+path equality. Not `startsWith`, not a hostname check, not a same-origin test: every
 * one of those has been the shape of a real open redirect, and the reflected-target variant is the
 * one that turns a login flow into a token-exfiltration vector.
 */
export function allowedRedirect(candidate: string, allowlist: string[]): string | null {
  let url: URL;
  try {
    url = new URL(candidate);
  } catch {
    return null;
  }
  const normalised = url.origin + url.pathname;
  return allowlist.includes(normalised) ? normalised : null;
}
