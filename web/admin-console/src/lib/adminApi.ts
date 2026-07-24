import "server-only";

/**
 * adminApi.ts is the backend-for-frontend's ONLY door to the platform (FR20).
 *
 * # Why `server-only` is the first line of the file
 *
 * This module reads the platform credential. If a client component ever imported it — directly or
 * through a barrel file three hops away — the credential would be inlined into a bundle the browser
 * downloads, and the failure would be invisible in review because the page would still work. The
 * `server-only` import makes that a BUILD ERROR instead of a leak. The bundle scan in
 * scripts/scan-bundle.mjs is the second, independent check, because a written rule alone has a
 * demonstrated failure rate (FR20).
 *
 * # What the browser gets instead
 *
 * An `HttpOnly`, `SameSite=Strict`, `Secure` cookie holding the ADMIN SESSION token — bound to an
 * admin principal, revocable server-side, and unreadable by page script. The platform credential
 * stays here.
 */

/** ADMIN_API_BASE is the origin of the Go admin API. Server-side configuration, never public. */
const ADMIN_API_BASE = process.env.ADMIN_API_BASE ?? "http://127.0.0.1:4311";

/**
 * PLATFORM_CREDENTIAL is the BFF's credential for the admin API. It is read at request time from the
 * server environment (a secrets manager injects it) and is never returned, logged or serialized.
 */
function platformCredential(): string {
  const value = process.env.ADMIN_PLATFORM_CREDENTIAL;
  if (!value) {
    // Fail loudly at the first request rather than serving an unauthenticated console: a BFF with no
    // credential cannot reach the platform at all, and saying so beats a page of empty states.
    throw new Error(
      "admin BFF is not configured: ADMIN_PLATFORM_CREDENTIAL is unset — the console cannot reach the platform",
    );
  }
  return value;
}

/** ADMIN_SESSION_COOKIE is the admin session cookie name. */
export const ADMIN_SESSION_COOKIE = "heros_admin_session";

/**
 * Kinds an admin API error can take. They drive the console's render states (FR26, FR36).
 *
 * `degraded` and `unknown` are deliberately different answers to different questions. A READ that
 * cannot reach the platform is degraded: nothing changed, the view is incomplete, try again. A
 * COMMAND whose response is lost is `unknown`: the platform write-ahead-audits before it effects, so
 * the action may well have taken place, and telling the operator "failed" would invite them to do it
 * twice. Collapsing the two is the mistake this enum exists to prevent.
 */
export type AdminErrorKind = "auth" | "denied" | "friction" | "degraded" | "request" | "unknown";

/** AdminApiError carries the server's classification through to the component that renders it. */
export class AdminApiError extends Error {
  readonly kind: AdminErrorKind;
  readonly status: number;
  readonly heldBy: string[];

  constructor(kind: AdminErrorKind, status: number, message: string, heldBy: string[] = []) {
    super(message);
    this.name = "AdminApiError";
    this.kind = kind;
    this.status = status;
    this.heldBy = heldBy;
  }
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  /** sessionToken authorizes the request as a specific admin principal. */
  sessionToken?: string;
  /** impersonationId marks the request as taken under an impersonation session. */
  impersonationId?: string;
};

/**
 * adminFetch performs one call to the admin API with both credentials attached.
 *
 * `cache: "no-store"` on every call: an operator console that served a cached tenant view during an
 * incident would show a state that is no longer true, and "the page was stale" is not a defensible
 * reason to have suspended the wrong tenant.
 */
export async function adminFetch<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    "content-type": "application/json",
    "X-Admin-Platform-Credential": platformCredential(),
  };
  if (options.sessionToken) headers["X-Admin-Session"] = options.sessionToken;
  if (options.impersonationId) headers["X-Admin-Impersonation"] = options.impersonationId;

  const method = options.method ?? "GET";
  let response: Response;
  try {
    response = await fetch(`${ADMIN_API_BASE}${path}`, {
      method,
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      cache: "no-store",
    });
  } catch (cause) {
    // A transport failure is never an empty result: "we could not reach the platform" and "there is
    // nothing to show" are different facts (FR26).
    //
    // WHICH failure it is depends on what was being attempted. On a read, nothing changed — degraded.
    // On a COMMAND, the request may have arrived and been audited and applied before the connection
    // dropped, so the only honest answer is that the outcome is UNKNOWN, and the remedy is the audit
    // log rather than a retry (FR36). This is the one place that distinction can still be made:
    // further up the stack, all that is left is an exception.
    throw new AdminApiError(
      method === "GET" ? "degraded" : "unknown",
      0,
      `the admin API is unreachable: ${String(cause)}`,
    );
  }

  const text = await response.text();
  const parsed: unknown = text ? safeParse(text) : null;

  if (!response.ok) {
    const body = (parsed ?? {}) as { kind?: AdminErrorKind; detail?: string; error?: string; held_by?: string[] };
    throw new AdminApiError(
      body.kind ?? "degraded",
      response.status,
      body.detail ?? body.error ?? `admin API returned ${response.status}`,
      body.held_by ?? [],
    );
  }
  return parsed as T;
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * exchangeAssertion performs the SSO + MFA login against the admin identity provider and returns the
 * session token the BFF puts in its HttpOnly cookie.
 */
export async function exchangeAssertion(assertion: unknown): Promise<{
  session_token: string;
  admin_id: string;
  expires_at: string;
  mfa_factor: string;
}> {
  return adminFetch("/admin/api/session", { method: "POST", body: { assertion } });
}

/** revokeSession ends an admin session server-side, so the next request is denied with no grace. */
export async function revokeSession(sessionToken: string): Promise<void> {
  await adminFetch("/admin/api/session", { method: "DELETE", sessionToken });
}
