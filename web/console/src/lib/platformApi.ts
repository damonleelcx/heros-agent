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
      traceId?: string;
    };

type FetchOptions = {
  /**
   * tenantId is the scope, and it comes from the SESSION (NFR12). It is a required argument rather
   * than an ambient read so that a call site that has not resolved a session cannot compile — the
   * standing lesson being that a request must not be trusted to describe its own authority.
   */
  tenantId: string;
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
        // The credential crosses exactly one boundary: here.
        "X-API-Key": platformCredential(),
        // The tenant is stated explicitly on every upstream call so the platform's own logs can
        // attribute it. It is derived server-side from the session and cannot be influenced by the
        // client — see scope.ts.
        "X-Console-Tenant": options.tenantId,
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
    const body = (parsed ?? {}) as { error?: string; detail?: string; trace_id?: string } & Denial;
    // The platform's own words, not ours. `p35graph.html`'s error copy — "no such workflow (distinct
    // from a workflow that exists but is unclassified)" — is the product; inventing a generic message
    // here would destroy it before any component could render it.
    const error = body.error ?? body.detail ?? `the platform API returned ${response.status}`;
    const kind = classify(response.status, error);
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
    return { ok: false, kind, status: response.status, error, denial, traceId: traceId ?? body.trace_id };
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
        "X-Console-Tenant": options.tenantId,
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
