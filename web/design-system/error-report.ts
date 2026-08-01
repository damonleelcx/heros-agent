/**
 * error-report.ts is the BROWSER half of the P24 error-reporting boundary.
 *
 * It is the same discipline as `internal/erroreport`, in the same shape, for the same reason: the event
 * is BUILT field by field from the allowlist below, and anything not named here does not exist on the
 * wire. A field added to an internal error representation is absent from a transmitted event by
 * default, which is the only direction of that asymmetry a boundary carrying customer material can
 * afford.
 *
 * # 🔴 Why this is ~200 lines of our own code and not a vendor SDK
 *
 * Three reasons, and the first two are the same ones the Go side gives:
 *
 *  1. **A default browser SDK event is close to a worst case here.** It carries `event.message`, a
 *     `request.url` (which under `/app` is a variant/run/node/tenant identifier), and a BREADCRUMB
 *     ARRAY containing every fetch URL, every navigation, every console line and the text of every
 *     element the user clicked. The requirement is that breadcrumbs are ABSENT rather than filtered —
 *     and the only way to be sure a collection is absent is for no code to collect it.
 *  2. **The guarantee has to survive a version bump.** "The SDK sends only what we configured, at the
 *     version we tested" is a denylist wearing a configuration file.
 *  3. **The transfer budget.** A browser error SDK is ~100 KB on the wire; this is a few hundred bytes
 *     of first-party code inside a bundle that is already measured. On the public surface, where the
 *     per-origin budgets live, that difference is most of the budget.
 *
 * # Where it runs, and where it does not
 *
 * All three web surfaces — the public prefix, the tenant prefix and every operator route. Error
 * reporting is the ONE third-party origin permitted on a tenant or operator prefix, and it is permitted
 * precisely because this file makes the payload carry no content by construction. Session replay and
 * analytics are refused on those surfaces and are not configurable back on.
 *
 * # How it runs under the CSP
 *
 * As first-party bundled code, reached through `'strict-dynamic'` from the per-request nonced
 * bootstrap. There is no `<script src>` to a third-party host and no inline script: `script-src` gains
 * NO host on any prefix, and a script that did not arrive from the nonced root does not execute at all.
 * The only thing the policy is widened by is the reporting ORIGIN under `connect-src`, from the
 * checked-in allowlist.
 */

import { ALLOWED_ORIGINS, isSurfaceId, type SurfaceId } from "./third-party-policy.ts";

/**
 * BROWSER_ERROR_CODES mirrors the browser half of the central `errorcode` enum in Go.
 *
 * Four classes rather than one, because they have four different causes and three of them are invisible
 * to a green build: a chunk that failed to load, a hydration mismatch and a CSP that refused a script
 * all render a page that looks correct and does nothing. That failure mode — *renders correctly, does
 * nothing* — is the one this whole integration exists for.
 *
 * A Go-side test asserts this list is a subset of `internal/errorcode`'s, so the two cannot drift into
 * two vocabularies for one incident.
 */
export const BROWSER_ERROR_CODES = [
  "BROWSER_UNHANDLED_ERROR",
  "BROWSER_UNHANDLED_REJECTION",
  "BROWSER_CHUNK_LOAD_FAILED",
  "BROWSER_HYDRATION_FAILED",
  "UNKNOWN",
] as const;

export type BrowserErrorCode = (typeof BROWSER_ERROR_CODES)[number];

/**
 * ALLOWLIST is the complete set of wire keys a browser event may carry — the same thirteen as the Go
 * side, so one review answers for both.
 */
export const ALLOWLIST = [
  "error.type",
  "error.code",
  "level",
  "frames.function",
  "frames.package",
  "frames.file",
  "frames.line",
  "frames.in_app",
  "trace_id",
  "release",
  "edition",
  "surface",
  "runtime",
] as const;

/** ReportingConfig is everything the reporter needs. Nothing is read from ambient state. */
export type ReportingConfig = {
  /** The ingest DSN. Empty or absent means the reporter is ABSENT — no listeners, no transmit. */
  dsn: string;
  release: string;
  edition: string;
  /** The surface, from the closed enum. Never `location.href`. */
  surface: SurfaceId;
  /** The trace id of the page's own server render, so a browser failure joins the request that served it. */
  traceId?: string;
  /** True when `error_diagnostics` has been granted. False keeps the reporter absent. */
  granted: boolean;
};

type Frame = {
  function: string;
  package: string;
  file: string;
  line: number;
  in_app: boolean;
};

type BuiltEvent = {
  "error.type": string;
  "error.code": BrowserErrorCode;
  level: "error";
  frames: Frame[];
  trace_id: string;
  release: string;
  edition: string;
  surface: string;
  runtime: string;
};

/**
 * parseDsn splits an ingest DSN and REFUSES an origin the shared allowlist does not carry.
 *
 * 🔴 This is the load-bearing half of "the policy tells the truth about where data goes". The
 * Content-Security-Policy names the reporting origin because the artefact carries it; if a deployment
 * could set a DSN pointing somewhere else, the policy and the destination would disagree and the policy
 * would be the one that is wrong. So the DSN's origin is checked against the same table the header is
 * built from, and a mismatch turns the reporter off rather than transmitting to an origin the browser
 * would refuse anyway — refusing loudly here beats a console full of blocked requests nobody reads.
 */
export function parseDsn(dsn: string): { endpoint: string; key: string } | null {
  let url: URL;
  try {
    url = new URL(dsn);
  } catch {
    return null;
  }
  if (!url.username || !url.pathname || url.pathname === "/") return null;
  const permitted = ALLOWED_ORIGINS.some(
    (o) => o.origin === url.origin && o.category === "error_diagnostics",
  );
  if (!permitted) return null;
  return {
    endpoint: `${url.origin}/api/${url.pathname.replace(/^\//, "")}/envelope/`,
    key: url.username,
  };
}

/**
 * pageOrigin is the origin the page was served from, or "" outside a browser.
 *
 * Read through a guard rather than as a bare `location.origin`, because this module is also imported by
 * the test suite and by the acceptance harness, where `location` does not exist. An earlier version
 * dereferenced it unguarded: every `new URL(file, location.origin)` threw, the catch kept the RAW file
 * reference, and a frame carried a full URL with its query string intact — while the browser path,
 * where `location` does exist, was fine. A defect that only appears where nothing looks at it.
 */
function pageOrigin(): string {
  return typeof location === "undefined" ? "" : location.origin;
}

/** isInApp identifies a frame as platform code: our chunks are served from our own origin. */
function isInApp(file: string): boolean {
  if (!file) return false;
  const origin = pageOrigin();
  if (!origin) return file.startsWith("/");
  try {
    return new URL(file, origin).origin === origin;
  } catch {
    return false;
  }
}

/**
 * parseFrames reduces a stack string to allowlisted frames.
 *
 * A file path is reduced to its PATHNAME — never the full URL — because a chunk URL on a tenant route
 * is same-origin and harmless, while any query or fragment on it is not. The line number survives; the
 * column does not, because it is not on the allowlist and adds nothing a line does not.
 */
export function parseFrames(stack: string | undefined): Frame[] {
  if (!stack) return [];
  const out: Frame[] = [];
  for (const raw of stack.split("\n").slice(0, 40)) {
    const line = raw.trim();
    if (!line || line.startsWith("Error")) continue;
    // `at fn (https://host/_next/static/chunks/x.js:12:34)` and the bare `at https://…:12:34` form.
    const match =
      /at\s+(.+?)\s+\((.+?):(\d+):(\d+)\)/.exec(line) ?? /at\s+(.*?)(https?:[^\s)]+):(\d+):(\d+)/.exec(line);
    if (!match) continue;
    const fn = (match[1] || "").trim() || "<anonymous>";
    const file = match[2];
    let pathname = file;
    try {
      pathname = new URL(file, pageOrigin() || "http://localhost").pathname;
    } catch {
      // A non-URL file reference (a native frame). Keep it as-is; it names no host.
    }
    out.push({
      function: fn,
      // The chunk directory stands in for a package: it is the closest thing a browser stack has to a
      // subsystem, and it is our own build's output rather than anything a user supplied.
      package: pathname.split("/").slice(0, -1).join("/"),
      file: pathname.split("/").slice(-1)[0] ?? "",
      line: Number(match[3]) || 0,
      in_app: isInApp(file),
    });
  }
  return out;
}

/**
 * classify maps a browser failure to a code from the closed enum.
 *
 * 🔴 It reads the message ONLY to CLASSIFY, and the message never leaves this function. That is the
 * one place a message body is touched anywhere in the boundary, and it is worth naming: a chunk-load
 * failure and an ordinary type error are the same `Error` class and are told apart by nothing else.
 * The output is a member of a closed enum; the input is discarded.
 */
export function classify(kind: "error" | "rejection", message: string): BrowserErrorCode {
  const m = message.toLowerCase();
  if (m.includes("loading chunk") || m.includes("failed to fetch dynamically imported module") ||
      m.includes("importing a module script failed")) {
    return "BROWSER_CHUNK_LOAD_FAILED";
  }
  if (m.includes("hydration") || m.includes("hydrating") || m.includes("did not match")) {
    return "BROWSER_HYDRATION_FAILED";
  }
  return kind === "rejection" ? "BROWSER_UNHANDLED_REJECTION" : "BROWSER_UNHANDLED_ERROR";
}

/** buildEvent constructs the wire object. It is the ONLY place an event is created. */
export function buildEvent(
  config: ReportingConfig,
  kind: "error" | "rejection",
  error: unknown,
): BuiltEvent {
  const err = error instanceof Error ? error : undefined;
  const message = err?.message ?? (typeof error === "string" ? error : "");
  return {
    // A CLASS name, never a value. `error.constructor.name` is the browser's equivalent of `%T`.
    "error.type": err?.name || (error === null ? "null" : typeof error),
    "error.code": classify(kind, message),
    level: "error",
    frames: parseFrames(err?.stack),
    trace_id: config.traceId ?? "",
    release: config.release,
    edition: config.edition,
    // From the closed enum. NEVER `location.href`: a URL under /app carries variant, run, node and
    // tenant identifiers, and there is no redaction of it that survives the next route being added.
    surface: isSurfaceId(config.surface) ? config.surface : "unknown",
    runtime: "browser",
  };
}

/**
 * encodeEnvelope renders the exact bytes.
 *
 * There is no breadcrumb array, no `request`, no `contexts`, no `user`, no `extra` and no `modules` —
 * not because they are filtered, but because this function does not write them and nothing else
 * serialises an event.
 */
export function encodeEnvelope(event: BuiltEvent, eventId: string, sentAt: string): string {
  const payload = JSON.stringify({
    event_id: eventId,
    platform: "javascript",
    level: event.level,
    release: event.release,
    tags: {
      trace_id: event.trace_id,
      "error.code": event["error.code"],
      edition: event.edition,
      surface: event.surface,
      runtime: event.runtime,
    },
    exception: {
      values: [
        {
          type: event["error.type"],
          // The ONLY message-shaped field, and it is a member of a closed enum.
          value: event["error.code"],
          stacktrace: {
            frames: event.frames.map((f) => ({
              function: f.function,
              module: f.package,
              filename: f.file,
              lineno: f.line,
              in_app: f.in_app,
            })),
          },
        },
      ],
    },
  });
  const header = JSON.stringify({ event_id: eventId, sent_at: sentAt });
  const item = JSON.stringify({ type: "event", content_type: "application/json", length: payload.length });
  return `${header}\n${item}\n${payload}\n`;
}

/**
 * eventId derives the protocol's required identifier from the event's own content.
 *
 * Derived rather than randomly minted, for the reason the design gives: adding an incident system is the
 * classic moment a second correlation identity appears, after which two systems hold half an incident
 * each with no join key. `trace_id` stays the only handle anyone uses; this is a protocol artefact.
 */
function eventId(event: BuiltEvent, sentAt: string): string {
  const material = [event.trace_id, event["error.code"], event["error.type"], event.surface, sentAt,
    event.frames.map((f) => `${f.package}/${f.file}:${f.line}`).join(";")].join(" ");
  // FNV-1a, twice with different offsets, to fill 32 hex characters without pulling in a hash library
  // or making this an async function — `crypto.subtle.digest` is a promise, and an error handler that
  // awaits is an error handler that loses events during a page unload.
  let a = 0x811c9dc5;
  let b = 0x01000193;
  for (let i = 0; i < material.length; i += 1) {
    a = Math.imul(a ^ material.charCodeAt(i), 16777619) >>> 0;
    b = Math.imul(b ^ material.charCodeAt(material.length - 1 - i), 16777619) >>> 0;
  }
  // `>>> 0` on EVERY value, not only on the accumulators. `a ^ b` is a signed 32-bit result in
  // JavaScript, and a negative one renders as "-1a2b3c4d" — which produced a 31-character event id
  // with a hyphen in it, caught by the acceptance run reading a real transmitted body rather than by
  // any unit test, because nothing before that had looked at the bytes.
  const half = (n: number) => (n >>> 0).toString(16).padStart(8, "0");
  return (half(a) + half(b) + half(a ^ b) + half(a + b)).slice(0, 32);
}

/** Rate limits, mirroring the Go side's numbers and their basis. */
const PER_ISSUE_LIMIT = 5;
const TRANSMIT_BUDGET = 60;
const RATE_INTERVAL_MS = 60_000;

/**
 * makeSender builds the ONE send path, or null when the reporter is ABSENT.
 *
 * Absent means: no DSN, no `error_diagnostics` grant, or a DSN whose origin the shared allowlist does
 * not carry. It returns null rather than a no-op function so a caller cannot accidentally attach a
 * listener that will never do anything — an absent reporter attaches nothing at all, which is what
 * makes "declining leaves every function intact" cost nothing and leaves no code path to re-enable.
 *
 * One sender rather than two, because an unhandled exception and a failure an error boundary caught
 * must travel the same construction, the same rate limit and the same encoder. Two paths is two
 * chances for one of them to grow a field.
 */
function makeSender(config: ReportingConfig): ((kind: "error" | "rejection", error: unknown) => void) | null {
  if (typeof window === "undefined" || !config.granted) return null;
  const target = parseDsn(config.dsn);
  if (!target) return null;

  let windowStart = 0;
  let windowCount = 0;
  const perIssue = new Map<string, number>();

  const admit = (event: BuiltEvent): boolean => {
    const now = Date.now();
    if (now - windowStart >= RATE_INTERVAL_MS) {
      windowStart = now;
      windowCount = 0;
      perIssue.clear();
    }
    if (windowCount >= TRANSMIT_BUDGET) return false;
    const key = `${event["error.code"]}|${event["error.type"]}|${event.frames[0]?.function ?? ""}`;
    const seen = perIssue.get(key) ?? 0;
    if (seen >= PER_ISSUE_LIMIT) return false;
    perIssue.set(key, seen + 1);
    windowCount += 1;
    return true;
  };

  return (kind, error) => {
    try {
      const event = buildEvent(config, kind, error);
      if (!admit(event)) return;
      const sentAt = new Date().toISOString();
      // `keepalive` so an event raised during a navigation still leaves. No retry and no queue: a
      // browser reporter that retries can amplify an outage from every open tab at once.
      void fetch(target.endpoint, {
        method: "POST",
        keepalive: true,
        headers: {
          "Content-Type": "application/x-sentry-envelope",
          "X-Sentry-Auth": `Sentry sentry_version=7, sentry_key=${target.key}`,
        },
        body: encodeEnvelope(event, eventId(event, sentAt), sentAt),
      }).catch(() => {
        // Fail-static. A reporting failure must never surface to the user or throw inside a handler
        // that is already handling an error.
      });
    } catch {
      // Same rule one level up: constructing a report must not itself throw.
    }
  };
}

/**
 * installErrorReporting attaches the handlers and returns a teardown function.
 *
 * Two listeners cover unhandled errors and unhandled rejections. Chunk-load and hydration failures
 * arrive through whichever of those two the browser raises them on — and through
 * `reportHandledFailure` when React catches them first, which is the case that matters most, because a
 * caught chunk-load failure renders a fallback and looks like a working page.
 *
 * Withdrawal is what the teardown is for: revoking `error_diagnostics` removes the listeners on the
 * next navigation with no sign-out, and collection stops.
 */
export function installErrorReporting(config: ReportingConfig): () => void {
  const send = makeSender(config);
  if (!send) return () => {};

  const onError = (e: ErrorEvent) => send("error", e.error ?? e.message);
  const onRejection = (e: PromiseRejectionEvent) => send("rejection", e.reason);

  window.addEventListener("error", onError);
  window.addEventListener("unhandledrejection", onRejection);

  return () => {
    window.removeEventListener("error", onError);
    window.removeEventListener("unhandledrejection", onRejection);
    if (activeSender === send) activeSender = null;
  };
}

/**
 * activeSender is the send path the currently installed reporter uses.
 *
 * # Why a module-scoped value rather than a prop or a context
 *
 * A React ERROR BOUNDARY is a client component that Next renders in place of the subtree that failed.
 * It cannot call a server function to get the configuration, and threading a context through it means
 * the provider has to survive the failure that took the subtree down — which is exactly the case where
 * it may not have mounted yet. A chunk that fails to load fails BEFORE the tree it belongs to exists.
 *
 * So the configuration is captured once, when the root layout installs the reporter, and the boundary
 * reads it. `null` when nothing is installed, which is the correct behaviour for a declined grant: the
 * boundary still renders its degraded page, and nothing is transmitted.
 */
let activeSender: ((kind: "error" | "rejection", error: unknown) => void) | null = null;

/**
 * reportHandledFailure reports a failure a React error boundary caught.
 *
 * Chunk-load and hydration failures do not always reach `window.onerror`: React catches them and
 * renders a fallback, which is exactly the *renders correctly, does nothing* case this integration
 * exists for. An error boundary calls this so the failure is reported rather than swallowed by the
 * fallback that made the page look fine.
 */
export function reportHandledFailure(error: unknown): void {
  activeSender?.("error", error);
}
