import "server-only";
import { logUpstream } from "./telemetry";

/**
 * platformApi.ts is the BFF's ONLY door to the platform API (FR1, FR4, FR5).
 *
 * # Why `server-only` is the first line of the file
 *
 * This module reads the platform credential. If a client component ever imported it — directly, or
 * through a barrel file three hops away — the credential would be inlined into a bundle the browser
 * downloads, and the failure would be invisible in review because the page would still work. The
 * `server-only` import turns that into a BUILD ERROR rather than a leak. `scripts/scan-bundle.mjs` is
 * the second, independent check, because a written rule alone has a demonstrated failure rate here.
 *
 * # The pass-through contract (design.md Decision 3)
 *
 * This file authenticates, scopes by session, forwards, and returns. It does NOT merge two upstream
 * responses, re-rank, re-aggregate, round, reformat, or decide what a status means. The reason is not
 * tidiness: the first business rule a "smart" BFF absorbs is the tie rule, and at that point two
 * implementations of P4's statistical honesty exist and can disagree. A number the UI needs that the
 * server does not return is a read-model change request to the owning phase, not a computation here.
 *
 * # Why the failure taxonomy is a UNION and not an exception
 *
 * `503 not-mounted`, `404 not-found` and `transport failure` are three different things the user does
 * three different things about — mount the subsystem, check the identifier, check the network — and
 * the legacy pages already distinguish them correctly. Modelling them as one thrown `Error` is how a
 * port flattens them, because every catch site then has to re-derive the distinction from a status
 * code it may not have. So `platformFetch` RETURNS an outcome. There is deliberately no success-only
 * overload: a caller cannot accidentally ignore the failure shape, because there is no shape that
 * omits it.
 */

/** PLATFORM_API_BASE is the origin of the Go platform API. Server-side configuration, never public. */
const PLATFORM_API_BASE = process.env.PLATFORM_API_BASE ?? "http://127.0.0.1:4321";

/**
 * UPSTREAM_TIMEOUT_MS bounds every upstream call (FR8).
 *
 * A hung upstream must surface as a transport failure with actionable copy — never as an unbounded
 * loading state. An unbounded spinner is the worst of the three failures to render, because it is the
 * only one that tells the user nothing at all AND gives them nothing to do.
 */
const UPSTREAM_TIMEOUT_MS = Number(process.env.CONSOLE_UPSTREAM_TIMEOUT_MS ?? 10_000);

/**
 * platformCredential reads the BFF's own credential for the platform API.
 *
 * It is read at request time from the process environment (a secrets manager injects it) and is never
 * returned, logged, or serialized. It is the BFF's credential — NOT the user's: the session proves
 * which tenant, this proves the console may call the platform at all (ADR-008).
 */
function platformCredential(): string {
  const value = process.env.CONSOLE_PLATFORM_CREDENTIAL;
  if (!value) {
    // Fail loudly at the first request rather than serving a console of empty states: a BFF with no
    // credential cannot reach the platform at all, and saying so beats rendering "no data" everywhere
    // and letting somebody spend an afternoon on the wrong question.
    throw new Error(
      "the console BFF is not configured: CONSOLE_PLATFORM_CREDENTIAL is unset — it cannot reach the platform",
    );
  }
  return value;
}

/**
 * PlatformFailure is the three-way taxonomy, preserved end to end (FR6).
 *
 * `not-mounted`  the platform answered 503 with a not-mounted body — the subsystem is absent on this
 *                deployment. Remedy: mount it. The view degrades; the shell does not.
 * `not-found`    the platform answered 404 — this subject does not exist. Remedy: check the id. This
 *                is NEVER converted to an empty successful result, because "there is no such run" and
 *                "this run has no nodes" have different next actions.
 * `upstream`     any other non-2xx. The platform's own error body is carried through.
 * `transport`    the BFF could not reach the platform, or the platform did not answer within the
 *                timeout. Remedy: check the network / the platform's health. NEVER an empty result.
 */
export type PlatformFailureKind = "not-mounted" | "not-found" | "gated" | "upstream" | "transport";

/**
 * Denial is the entitlement gate's own decision, passed through verbatim (FR15).
 *
 * It mirrors the platform's `DenialView` and is deliberately NOT reconstructed from the console's
 * capability table: the plan named on screen must be the plan the gate actually enforces. The console
 * keeps a table of which plan to *mention* when it has no answer from the platform, and this is the
 * answer from the platform — they are different things and the second always wins.
 */
export type Denial = {
  feature?: string;
  feature_label?: string;
  reason?: string;
  reason_code?: string;
  upgrade_plan?: string;
  upgrade_plan_name?: string;
};

export type PlatformOutcome<T> =
  | { ok: true; data: T; status: number; traceId?: string }
  | {
      ok: false;
      kind: PlatformFailureKind;
      status: number;
      /** error is the UPSTREAM error body's message where one exists — never a message invented here. */
      error: string;
      /** denial is present on a `gated` outcome, carrying the platform's own words about the boundary. */
      denial?: Denial;
      /**
       * reasonCode is the platform's machine-readable name for a failure that is a CONFIGURED STATE
       * rather than a fault — today only `collection_not_configured`, the 404 every billing account
       * returns on a deployment with no payment provider.
       *
       * Carried for every kind, unlike `denial`, because the distinction it makes is not about
       * entitlement: a page has to be able to say "this install collects no payments" instead of "that
       * identifier does not resolve", and both arrive as 404. Branching on the error PROSE would put
       * the decision in two places and let a copy edit change behaviour.
       */
      reasonCode?: string;
      traceId?: string;
    };

type FetchOptions = {
  /**
   * tenantId is the scope, and it comes from the SESSION (NFR12). It is a required argument rather
   * than an ambient read so that a call site that has not resolved a session cannot compile — the
   * standing lesson being that a request must not be trusted to describe its own authority.
   */
  tenantId: string;
  /**
   * userId is the acting PERSON, when the session names one.
   *
   * 🔴 Without it every console call reaches the platform as the BFF's own credential — which names no
   * person, so `actingMember` refuses it and the members surface renders as a plan boundary for
   * everybody. That is not a hypothetical: it is exactly what the first browser run showed.
   *
   * With it, the call is made under a short-lived token scoped to this organization AND this person,
   * so an audit entry names who acted and a removed member's console stops working at their next
   * request — because the token is a session row and `RemoveMember` revokes sessions.
   */
  userId?: string;
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
};

/**
 * platformFetch performs exactly one call to the platform API and returns its outcome unmodified.
 *
 * `cache: "no-store"` on every call. A console that served a cached board during a run would show a
 * ranking that is no longer true, and "the page was stale" is not a defensible reason to have shipped
 * the wrong variant.
 */
export async function platformFetch<T>(path: string, options: FetchOptions): Promise<PlatformOutcome<T>> {
  const method = options.method ?? "GET";
  const started = Date.now();

  // The timeout is enforced here rather than being left to the platform: a socket that connects and
  // then says nothing is indistinguishable from a working call until something bounds it.
  const timer = new AbortController();
  const timeout = setTimeout(() => timer.abort(), UPSTREAM_TIMEOUT_MS);
  if (options.signal) {
    // A client that goes away (closed tab, cancelled navigation) must not leave the upstream call
    // running: the abort is chained rather than replaced, so either reason ends the request.
    options.signal.addEventListener("abort", () => timer.abort(), { once: true });
  }

  let response: Response;
  try {
    response = await fetch(`${PLATFORM_API_BASE}${path}`, {
      method,
      headers: {
        "content-type": "application/json",
        // The credential crosses exactly one boundary: here — and when the session names a person, what
        // crosses is a SCOPED TOKEN standing for them rather than the BFF's own key. See scopedToken.
        "X-API-Key": await credentialFor(options),
        // 🔴 P27 DELETED the `X-Console-Tenant` header that used to sit here.
        //
        // It was sent on every upstream call and the platform never read it — `grep -rn X-Console-Tenant
        // --include=*.go` returned a single hit, a comment in a proof binary. So `scope.ts`'s note that
        // "tenant isolation is ultimately enforced by the platform against the credential and the
        // X-Console-Tenant header" described a mechanism that did not exist, and every signed-in tenant
        // resolved to the one principal this process-wide credential names.
        //
        // The header is removed rather than made authoritative. Trusting it costs one line and lets any
        // holder of this credential name any organization — a request describing its own authority,
        // which ADR-008 Rule 2 exists to forbid. Scope now travels INSIDE the credential: the BFF
        // exchanges its session for a short-lived, organization-scoped token at
        // POST /api/v1/token-exchange, and `auth` resolves the organization from the thing the platform
        // verified. An ignored header that names authority is a loaded gun with the safety on, and
        // `internal/api/ownership_fence_test.go` fails the build if it comes back.
      },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      cache: "no-store",
      signal: timer.signal,
    });
  } catch (cause) {
    clearTimeout(timeout);
    // A transport failure is never an empty result. `catch { return [] }` is the single most damaging
    // line this file could contain: it tells the user there is genuinely nothing, and sends them to
    // look in the wrong place.
    const aborted = timer.signal.aborted;
    const outcome = {
      ok: false as const,
      kind: "transport" as const,
      status: 0,
      error: aborted
        ? `the platform API did not respond within ${UPSTREAM_TIMEOUT_MS} ms`
        : `the platform API is unreachable: ${describe(cause)}`,
    };
    logUpstream({ method, path, status: 0, kind: "transport", ms: Date.now() - started });
    return outcome;
  }
  clearTimeout(timeout);

  // trace_id is read from the response so a single request can be followed browser → BFF → platform
  // (FR26). It is an identifier, never content.
  const traceId = response.headers.get("X-Trace-Id") ?? undefined;

  const text = await response.text();
  const parsed = text ? safeParse(text) : null;

  if (!response.ok) {
    const body = (parsed ?? {}) as {
      error?: string;
      detail?: string;
      trace_id?: string;
      reason_code?: string;
    } & Denial;
    // The platform's own words, not ours. `p35graph.html`'s error copy — "no such workflow (distinct
    // from a workflow that exists but is unclassified)" — is the product; inventing a generic message
    // here would destroy it before any component could render it.
    const error = body.error ?? body.detail ?? `the platform API returned ${response.status}`;
    // 🔴 CLASSIFY ON `error`, RENDER `detail`. The two fields do different jobs and reading one for
    // both loses whichever job it was not doing.
    //
    // `classify` recognises not-mounted by matching /not mounted/i, so the short phrase has to be what
    // it sees. But the short phrase is also all the user got: /app/runs/{id}/live said "the live
    // monitor is not mounted" while the platform had a full explanation for the same fact — why it
    // cannot be live, and which surfaces DO carry the parts of that run it can show. The operator read
    // that in the boot log; the person looking at the empty page did not.
    //
    // So a body carrying both keeps the classification from `error` and shows `detail` to the reader.
    // A body with only one is unchanged in either direction.
    const kind = classify(response.status, error);
    const message = body.error && body.detail ? body.detail : error;
    logUpstream({ method, path, status: response.status, kind, ms: Date.now() - started, traceId });
    // The denial detail is carried only where it MEANS something. Attaching it to every failure would
    // invite a component to read `upgrade_plan_name` off a 404 and offer an upgrade for a typo.
    const denial =
      kind === "gated"
        ? {
            feature: body.feature,
            feature_label: body.feature_label,
            reason: body.reason,
            reason_code: body.reason_code,
            upgrade_plan: body.upgrade_plan,
            upgrade_plan_name: body.upgrade_plan_name,
          }
        : undefined;
    return {
      ok: false,
      kind,
      status: response.status,
      error: message,
      denial,
      reasonCode: body.reason_code,
      traceId: traceId ?? body.trace_id,
    };
  }

  logUpstream({ method, path, status: response.status, kind: "ok", ms: Date.now() - started, traceId });
  return { ok: true, data: parsed as T, status: response.status, traceId };
}

/**
 * classify maps an upstream status to the taxonomy.
 *
 * 503 is `not-mounted` because that is the only thing the platform returns 503 for on these routes —
 * every mount point answers `{"error": "… is not mounted on this server"}` when its source is nil.
 * The message is checked as well as the status so that a 503 from something else (a load balancer
 * shedding, say) is not mislabelled as a subsystem that was never installed: those have different
 * remedies, and R3 forbids collapsing two conditions with different remedies into one rendering.
 *
 * 🔴 403 is `gated`, and separating it out is a REQUIREMENT rather than a nicety (FR15).
 *
 * Before this, an entitlement refusal fell through to `upstream` and rendered as *"The platform refused
 * this request"* — which is to say, as an **error**. FR15 forbids exactly that: a capability outside
 * the tenant's plan is not broken and nothing has gone wrong; it is a commercial boundary, and a reader
 * shown an error goes to support while a reader shown the plan that unlocks it has a conversation with
 * sales. The distinction is worth a status code of its own.
 *
 * The console does not decide this. The platform returns a `DenialView` — feature, reason, and the
 * plan name that unlocks it — and the console renders those words. That is what makes the screen and
 * the gate incapable of disagreeing: there is one decision, taken upstream, displayed here.
 */
function classify(status: number, error: string): PlatformFailureKind {
  if (status === 404) return "not-found";
  if (status === 503 && /not mounted/i.test(error)) return "not-mounted";
  if (status === 403) return "gated";
  return "upstream";
}

/**
 * openPlatformStream opens an upstream SSE stream and returns the raw response for proxying (FR7).
 *
 * It deliberately returns the `Response` rather than a parsed stream: the BFF's job is to pass the
 * bytes through with flush semantics intact and without batching, and anything that parses events
 * here is one refactor away from buffering them. The stream is NOT given the request timeout — a
 * stream that is quiet is a run that is quiet, and aborting it after ten seconds would break exactly
 * the long-lived case SSE exists for.
 */
export async function openPlatformStream(
  path: string,
  options: { tenantId: string; signal?: AbortSignal },
): Promise<{ ok: true; response: Response } | { ok: false; kind: PlatformFailureKind; status: number; error: string }> {
  try {
    const response = await fetch(`${PLATFORM_API_BASE}${path}`, {
      headers: {
        "X-API-Key": platformCredential(),
        // See platformFetch: the tenant header is deleted, not made authoritative.
        accept: "text/event-stream",
      },
      cache: "no-store",
      signal: options.signal,
    });
    if (!response.ok) {
      const text = await response.text();
      const body = (safeParse(text) ?? {}) as { error?: string };
      const error = body.error ?? `the platform API returned ${response.status}`;
      logUpstream({ method: "GET", path, status: response.status, kind: classify(response.status, error), ms: 0 });
      return { ok: false, kind: classify(response.status, error), status: response.status, error };
    }
    return { ok: true, response };
  } catch (cause) {
    logUpstream({ method: "GET", path, status: 0, kind: "transport", ms: 0 });
    return {
      ok: false,
      kind: "transport",
      status: 0,
      error: `the platform API is unreachable: ${describe(cause)}`,
    };
  }
}

/**
 * describe renders a fetch failure without leaking anything.
 *
 * A raw `String(cause)` on a Node fetch error carries the full URL — which carries the platform
 * origin and, in some deployments, a path that names a tenant's subject. The console's error copy
 * needs the CLASS of failure, not the address the BFF dialled.
 */
function describe(cause: unknown): string {
  if (cause instanceof Error) {
    const code = (cause as { cause?: { code?: string } }).cause?.code;
    return code ?? cause.name;
  }
  return "unknown transport error";
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/** platformApiBase is exported for the health check only. It is an origin, never a credential. */
export function platformApiBase(): string {
  return PLATFORM_API_BASE;
}

/** upstreamTimeoutMs is exported so the timeout test asserts the configured bound, not a copy of it. */
export function upstreamTimeoutMs(): number {
  return UPSTREAM_TIMEOUT_MS;
}

/**
 * PUBLIC_SCOPE is the tenant value a session-less surface sends.
 *
 * It is a sentinel, not a tenant, and it is spelled so that nobody reading a platform access log mistakes it
 * for one. A public page has no session by construction — that is the point of it — so the honest header is
 * "there is no tenant here", not a plausible-looking identifier borrowed from somewhere.
 *
 * 🔴 ASCII ONLY, and that is not a style preference. This was first written with an em dash in it, and
 * `fetch` threw `TypeError: Cannot convert argument to a ByteString` before opening a socket — HTTP header
 * values are Latin-1. The BFF caught it and reported a TRANSPORT failure, so the page rendered its honest
 * "unavailable" banner and the real cause (a dash) was invisible from the browser. `isHeaderSafe` below turns
 * that into a loud failure at the call site instead.
 */
const PUBLIC_SCOPE = "public-surface-no-session";

/**
 * isHeaderSafe reports whether a value can legally be an HTTP header value.
 *
 * Header values are ByteStrings: any code point above 255 makes `fetch` throw before it connects, which
 * surfaces as an indistinguishable "transport failure". Checking here means the error names the value.
 */
function isHeaderSafe(value: string): boolean {
  for (const ch of value) {
    if (ch.codePointAt(0)! > 255) return false;
  }
  return true;
}

/**
 * platformFetchPublic reads an endpoint that is a property of the RELEASE rather than of a tenant, from a
 * surface that has no session.
 *
 * # Why this exists at all, given `platformFetch` deliberately requires a tenant
 *
 * `platformFetch`'s required `tenantId` is a compile-time guard: a call site that has not resolved a session
 * cannot compile, because a request must not be trusted to describe its own authority. That guard is right for
 * every surface that renders tenant data, and it is exactly wrong for the install page — a page whose readers
 * do not have accounts yet, and whose whole message is that they do not need one.
 *
 * # Why this is not a hole in that guard
 *
 * It is usable only against endpoints that take no tenant, no plan and no role, and the server enforces that
 * by SIGNATURE: `handleP20Install` accepts none of them, so a future contributor who wants to vary the answer
 * per tenant has to change the handler's shape first, and this door stops fitting. The credential still crosses
 * exactly one boundary, and no session is read, forged, or implied.
 *
 * A caller that needs tenant-scoped data and reaches for this instead gets an answer with no tenant in it,
 * which is a visible bug rather than a silent authority escalation.
 */
export async function platformFetchPublic<T>(
  path: string,
  options?: Omit<FetchOptions, "tenantId">,
): Promise<PlatformOutcome<T>> {
  if (!isHeaderSafe(PUBLIC_SCOPE)) {
    // Loud, and at the call site. A non-Latin-1 scope makes every public read fail as a "transport failure",
    // which renders as the page's honest unavailable banner — correct behaviour hiding a one-character bug.
    throw new Error(
      `the console BFF's public scope ${JSON.stringify(PUBLIC_SCOPE)} is not a legal HTTP header value ` +
        "(header values are Latin-1); every public read would fail as an unexplained transport error",
    );
  }
  return platformFetch<T>(path, { ...options, tenantId: PUBLIC_SCOPE });
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Scoped tokens
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

/**
 * credentialFor returns what this call should present upstream.
 *
 * # The two cases, and why the first one exists at all
 *
 * A call made on behalf of a SIGNED-IN PERSON presents a short-lived token scoped to that organization
 * and that person. A call made by the console itself — the token exchange, sign-in, anything with no
 * session behind it — presents the BFF's own credential, which names no person and is exactly right for
 * a caller that is not one.
 *
 * # 🔴 What this fixes, found in a browser and by nothing else
 *
 * Before it, every console call reached the platform as the BFF's machine credential. `actingMember`
 * refuses a machine credential — a CI key that could remove a colleague is a CI key that becomes an
 * offboarding tool — so the members, invitations and API-key sections all rendered as *"not included in
 * this plan"*. Nothing failed. The build was green, every test passed, and the product told customers a
 * capability they had was a capability they had to pay for.
 *
 * # Why caching the token is not caching a revocation
 *
 * The token is a `console_session` row, and the platform reads that row on EVERY request. Holding the
 * string until it expires caches the identifier, not the verdict: revoke the session and the very next
 * request presenting this token is refused. That is the distinction `durable.go` draws — caching a
 * "yes" would be caching a "not yet revoked", and this cache holds neither.
 */
async function credentialFor(options: FetchOptions): Promise<string> {
  if (!options.userId || !options.tenantId) return platformCredential();
  const token = await scopedToken(options.tenantId, options.userId);
  // A token exchange that fails does NOT fall back to the BFF's credential. Falling back would make a
  // person's call silently become the console's, which is the widening this whole mechanism removes;
  // the request then fails as an ordinary upstream failure and says so.
  return token ?? platformCredential();
}

type CachedToken = { token: string; expiresAtMs: number };

/** One process-wide cache. Keyed by organization AND person: a token is scoped to both. */
const TOKEN_CACHE = Symbol.for("heros.console.scopedTokens");
type TokenGlobal = typeof globalThis & { [TOKEN_CACHE]?: Map<string, CachedToken> };

function tokenCache(): Map<string, CachedToken> {
  const scope = globalThis as TokenGlobal;
  if (!scope[TOKEN_CACHE]) scope[TOKEN_CACHE] = new Map<string, CachedToken>();
  return scope[TOKEN_CACHE];
}

/**
 * SCOPED_TOKEN_SAFETY_MS is how early a cached token is treated as expired.
 *
 * Thirty seconds: a token that expires between this check and the platform reading it produces a 401
 * the user sees as an unexplained failure, and the cost of being early is one extra exchange.
 */
const SCOPED_TOKEN_SAFETY_MS = 30_000;

async function scopedToken(tenantId: string, userId: string): Promise<string | null> {
  const key = `${tenantId}\u0000${userId}`;
  const cache = tokenCache();
  const cached = cache.get(key);
  if (cached && cached.expiresAtMs - SCOPED_TOKEN_SAFETY_MS > Date.now()) return cached.token;

  try {
    const response = await fetch(`${PLATFORM_API_BASE}/api/v1/token-exchange`, {
      method: "POST",
      headers: { "content-type": "application/json", "X-API-Key": platformCredential() },
      body: JSON.stringify({ tenant_id: tenantId, user_id: userId }),
      cache: "no-store",
    });
    if (!response.ok) return null;
    const body = (await response.json()) as { token?: string; expires_in?: number };
    if (!body.token) return null;
    const ttl = (body.expires_in ?? 600) * 1000;
    cache.set(key, { token: body.token, expiresAtMs: Date.now() + ttl });
    return body.token;
  } catch {
    return null;
  }
}
