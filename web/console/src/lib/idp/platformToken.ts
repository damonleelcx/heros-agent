import "server-only";

/**
 * platformToken.ts is the `platform` identity seam: the console verifies a presented credential by
 * ASKING THE PLATFORM whose it is, instead of keeping a second copy of the answer.
 *
 * # The gap this closes
 *
 * `heros login` authenticates a platform tenant token, `heros link` transmits with it, and the command
 * ends by printing `view it at https://<platform>/app/runs/<run-id>`. That URL then refused the very
 * credential the user had just authenticated with, because the console's `configured` seam checks a
 * SEPARATE secret (`CONSOLE_TENANT_ASSERTIONS`) that no `heros` command mints, prints or derives. The
 * product's own success message pointed at a door the user had no key to, and the only way in was for an
 * operator to hand-edit an env var and deliver the string out of band.
 *
 * # Why this is not a privilege escalation
 *
 * The token being presented ALREADY authorizes the whole tenant API surface on the platform — that is
 * what `auth.Middleware` grants it, and it is what the CLI uses it for. A console session is strictly
 * LESS power than the token that bought it: the session is server-side, revocable, TTL-bounded, and
 * scoped to one tenant's read surfaces, whereas the token is the API itself. So this removes a secret
 * rather than adding a door. It is the reason `configured` stays available unchanged for a deployment
 * that genuinely wants console access and API access to be different grants.
 *
 * # Fail closed, and say nothing about the value
 *
 * Every failure — transport, timeout, non-200, empty identity, malformed body — is one refusal. The
 * platform is the authority on whether a token is good; a console that cannot REACH the platform does
 * not know, and "I do not know" must not resolve to "yes". The cause is logged server-side by the
 * caller; the value never is.
 */

/** The platform origin the BFF calls. Same variable the rest of the BFF uses; server-side only. */
const PLATFORM_API_BASE = process.env.PLATFORM_API_BASE ?? "http://127.0.0.1:4321";

/**
 * WHOAMI_PATH is the platform's token-identity endpoint.
 *
 * 🔴 It is the SAME path `heros login` validates against (`internal/runlink/allowlist.go`), on purpose:
 * "the CLI accepted this token" and "the console accepts this token" must not be two different
 * questions, or the gap this file closes reopens with an extra step.
 */
const WHOAMI_PATH = "/api/v1/whoami";

/** Bounded like every other upstream call — a hung platform must refuse, never hang a sign-in. */
const TIMEOUT_MS = Number(process.env.CONSOLE_UPSTREAM_TIMEOUT_MS ?? 10_000);

export type PlatformTokenOutcome =
  | { ok: true; tenantId: string }
  | { ok: false; cause: string };

/**
 * reachable probes the platform's identity endpoint for the health surface.
 *
 * 🔴 It sends NO credential and treats an answer — including `401` — as reachable, because the question
 * is "does the authority respond?", not "is some token good?". The discriminator is that something
 * ANSWERED: a `401` proves the platform is up and enforcing, which is exactly the healthy state.
 *
 * Without this, `identityHealth` reported `reachable: true` for this seam unconditionally, which is a
 * signal that cannot fail — and for `platform` the platform's reachability IS whether anyone can sign
 * in, so it is the one component whose health must not be assumed.
 */
export async function reachable(): Promise<{ reachable: boolean; detail?: string }> {
  const timer = new AbortController();
  const timeout = setTimeout(() => timer.abort(), TIMEOUT_MS);
  try {
    const response = await fetch(`${PLATFORM_API_BASE}${WHOAMI_PATH}`, {
      method: "GET",
      headers: { accept: "application/json" },
      cache: "no-store",
      signal: timer.signal,
    });
    return { reachable: true, detail: `platform answered ${response.status}` };
  } catch (cause) {
    return {
      reachable: false,
      detail:
        cause instanceof Error && cause.name === "AbortError"
          ? `no answer within ${TIMEOUT_MS} ms`
          : "the platform could not be reached",
    };
  } finally {
    clearTimeout(timeout);
  }
}

/**
 * verifyPlatformToken resolves a presented platform token to the tenant the platform attributes it to.
 *
 * The token is sent as a bearer credential in a header and is never placed in the URL, the body, a log
 * line, or the returned value.
 */
export async function verifyPlatformToken(token: string): Promise<PlatformTokenOutcome> {
  const value = token.trim();
  if (!value) return { ok: false, cause: "empty credential" };

  // A credential with a newline or a space in it cannot be a header value; rejecting it here keeps a
  // malformed paste from becoming a header-injection question further down.
  if (/[\r\n\0]/.test(value) || /\s/.test(value)) {
    return { ok: false, cause: "credential contains whitespace or control characters" };
  }

  const timer = new AbortController();
  const timeout = setTimeout(() => timer.abort(), TIMEOUT_MS);
  try {
    const response = await fetch(`${PLATFORM_API_BASE}${WHOAMI_PATH}`, {
      method: "GET",
      headers: {
        // The USER's credential, presented exactly as the CLI presents it. Not the BFF's own — this
        // call is asking "whose is this?", so answering it with the console's credential would return
        // the console's identity for every input and authenticate everybody as the same tenant.
        authorization: `Bearer ${value}`,
        accept: "application/json",
      },
      cache: "no-store",
      signal: timer.signal,
    });

    if (response.status === 401 || response.status === 403) {
      return { ok: false, cause: "the platform rejected this credential" };
    }
    if (!response.ok) {
      // A 5xx is the platform being unwell, not the credential being bad. Both refuse, because a
      // sign-in cannot proceed either way, but the operator-visible cause distinguishes them.
      return { ok: false, cause: `the platform answered ${response.status}` };
    }

    const body: unknown = await response.json().catch(() => null);
    const identity =
      body && typeof body === "object" && typeof (body as { identity?: unknown }).identity === "string"
        ? (body as { identity: string }).identity.trim()
        : "";
    if (!identity) {
      // The platform accepted the token and named nobody. Refuse: a session with an empty tenant id
      // is a session scoped to everything, and `scope.ts` derives every upstream call from this value.
      return { ok: false, cause: "the platform accepted the credential but returned no identity" };
    }
    return { ok: true, tenantId: identity };
  } catch (cause) {
    const reason = cause instanceof Error && cause.name === "AbortError"
      ? `the platform did not respond within ${TIMEOUT_MS} ms`
      : "the platform could not be reached";
    return { ok: false, cause: reason };
  } finally {
    clearTimeout(timeout);
  }
}
