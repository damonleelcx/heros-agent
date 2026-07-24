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
 * # Why there are nine and not four
 *
 * Every kind here is a DIFFERENT QUESTION with a DIFFERENT REMEDY, and the whole point of the enum is
 * that the console can never answer one of them with another's copy:
 *
 *   auth         the session is gone                    → sign in again
 *   denied       your role does not hold this           → ask someone who does (named)
 *   friction     a required step was missing            → nothing happened; complete it
 *   request      the platform rejected the input        → nothing happened; fix the values
 *   not_found    the identifier does not resolve        → check what you pasted
 *   not_mounted  this deployment does not carry it      → nothing is wrong
 *   gated        outside this plan                      → an entitlement, ✋ NOT an error
 *   degraded     the platform could not be reached      → check the network, retry the READ
 *   unusable     it answered something we cannot read   → a version mismatch, and retrying will not fix it
 *   unknown      a COMMAND's response was lost          → the audit log, never a retry
 *
 * The collapses this prevents are all ones the console previously made. A 404 arrived as `degraded`,
 * so pasting a wrong tenant id told the operator the platform was broken. A 2xx whose body did not
 * parse was cast to the expected type and rendered as a page of blank fields — an unreadable answer
 * presented as a readable one, which is the worst of the nine to get wrong because it looks like data.
 */
export type AdminErrorKind =
  | "auth"
  | "denied"
  | "friction"
  | "request"
  | "not_found"
  | "not_mounted"
  | "gated"
  | "degraded"
  | "unusable"
  | "unknown";

/**
 * STATUS_KIND maps the HTTP statuses that carry their own meaning.
 *
 * The platform sends an explicit `kind` on most errors and that always wins; this is the fallback for
 * the statuses where the status IS the classification, and where defaulting to `degraded` would
 * mislabel an ordinary answer as a broken platform.
 */
const STATUS_KIND: Record<number, AdminErrorKind> = {
  401: "auth",
  402: "gated",
  403: "denied",
  404: "not_found",
  409: "friction",
  422: "request",
  501: "not_mounted",
};

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
  // A body that failed to PARSE and a body that parsed to `null` are different things, and only the
  // first is a version mismatch. The sentinel keeps them apart; `parsed === null` alone would not.
  const unreadable = parsed === UNPARSEABLE;

  if (!response.ok) {
    const body = (unreadable ? {} : (parsed ?? {})) as {
      kind?: AdminErrorKind;
      detail?: string;
      error?: string;
      held_by?: string[];
    };
    throw new AdminApiError(
      // The platform's own classification wins; the status is the fallback; `degraded` is the last
      // resort rather than the default, because "the platform is broken" is the most alarming of the
      // nine answers and the one least often actually true.
      body.kind ?? STATUS_KIND[response.status] ?? "degraded",
      response.status,
      body.detail ?? body.error ?? `admin API returned ${response.status}`,
      body.held_by ?? [],
    );
  }

  /*
   * 🔴 A successful response whose body cannot be read is UNUSABLE, not empty.
   *
   * This was silently the opposite. `safeParse` returned `null`, `null` was cast to `T`, and the page
   * rendered every field as `undefined` — a version mismatch presented to an operator as a tenant with
   * no plan, a queue with no jobs, a chain with no entries. That is the most dangerous failure on this
   * surface because it does not look like a failure; it looks like data, and it is read as data.
   *
   * An empty BODY is still legitimate (a 204 on logout), which is why the check is on unparseable
   * content rather than on absence.
   */
  if (unreadable) {
    throw new AdminApiError(
      "unusable",
      response.status,
      `the platform answered ${path} with ${text.length} bytes the console could not read — ` +
        "this is a version mismatch, not an empty result",
    );
  }
  return parsed as T;
}

/** UNPARSEABLE marks "this body is not JSON", which `null` cannot: `null` is valid JSON. */
const UNPARSEABLE = Symbol("unparseable");

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return UNPARSEABLE;
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
