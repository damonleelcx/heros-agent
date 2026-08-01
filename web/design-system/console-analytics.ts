/**
 * console-analytics.ts is the SERVER-SIDE analytics path for the two consoles.
 *
 * # 🔴 Why there is no browser tag under `/app/**` or on the operator console
 *
 * A URL under `/app` carries variant, run, node and tenant identifiers. A browser tag reports the URL —
 * that is what a browser tag is — so instrumenting those surfaces in the browser would put a tenant's
 * work into a third party's logs, and every mitigation is a denylist over paths that gain segments
 * every phase.
 *
 * The BFF already exists to keep something out of the browser: the platform credential. Keeping the
 * tenant's URL out of a third party's logs is the same argument on the same component. So the event is
 * emitted HERE, from the server, and the surface is an id from a closed enum.
 *
 * The rejected alternative is worth naming because it is the tidy one: proxy the browser tag through
 * our own origin so the policy stays literally `'self'`. Refused on honesty grounds — it would make a
 * third-party flow look first-party, and the policy's whole value is that it is a readable statement of
 * where data goes.
 *
 * # What an event may contain
 *
 * The six fields in `ANALYTICS_ALLOWLIST`, constructed one at a time. Not serialised-and-stripped: a
 * field added to some internal representation is ABSENT by default, which is the only direction of that
 * asymmetry a boundary can afford.
 *
 * # Absent by default
 *
 * The relay needs an API secret, and there is exactly one variable. Without it nothing is sent, nothing
 * is logged and nothing is retried — which is the state on every substrate except the platform's own
 * hosted deployment.
 */

import { isAnalyticsEvent, PLAN_NAMES, type AnalyticsEvent } from "./analytics-events.ts";
import { isSurfaceId, type SurfaceId } from "./third-party-policy.ts";

/** ConsoleEventInput is what a call site supplies. Every field is a closed value or a build fact. */
export type ConsoleEventInput = {
  event: AnalyticsEvent;
  surfaceId: SurfaceId;
  planName?: string;
  edition: string;
  release: string;
  /** Seconds since the epoch. Second granularity: a millisecond timestamp is a weak identifier. */
  occurredAt: number;
};

/** ConsoleEvent is the constructed wire shape — exactly the allowlist, nothing else. */
export type ConsoleEvent = {
  "event.name": string;
  surface_id: string;
  plan_name: string;
  edition: string;
  release: string;
  occurred_at: number;
};

/**
 * buildConsoleEvent constructs the event field by field, and drops anything that is not a closed value.
 *
 * Every drop is a REPLACEMENT with a truthful marker rather than a silent omission: an unknown surface
 * becomes `"unknown"`, an unrecognised plan becomes `""`. A field that vanished when its value was
 * rejected would make "every allowlist entry is populated" true only for the values that happened to be
 * valid.
 */
export function buildConsoleEvent(input: ConsoleEventInput): ConsoleEvent | null {
  if (!isAnalyticsEvent(input.event)) return null;
  return {
    "event.name": input.event,
    // Never a path and never a query string — an id from the closed enum or nothing.
    surface_id: isSurfaceId(input.surfaceId) ? input.surfaceId : "unknown",
    // The plan NAME only, from a set of four. Never a price and never a value: a price in an analytics
    // backend is a business number held by a third party.
    plan_name: (PLAN_NAMES as readonly string[]).includes(input.planName ?? "") ? (input.planName as string) : "",
    edition: input.edition,
    release: input.release,
    // Truncated to the second, deliberately. A millisecond timestamp joined across two events is a
    // weak identifier for the session that produced them.
    occurred_at: Math.floor(input.occurredAt),
  };
}

/** RelayConfig is everything the relay needs. Absent values mean absent, silently. */
export type RelayConfig = {
  measurementId: string;
  apiSecret: string;
  /** Injected so a test can capture the exact bytes. */
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
};

/** RelayState mirrors the error reporter's three states, for the same reason: a bool cannot say `absent`. */
export type RelayState = "absent" | "configured";

export function relayState(config: RelayConfig): RelayState {
  return config.measurementId && config.apiSecret ? "configured" : "absent";
}

/**
 * relayConsoleEvent transmits ONE event from the server process.
 *
 * Fail-static, exactly like the error reporter: it never throws, never retries, never blocks a served
 * response, and its failure is invisible to the reader. A console page that 500s because an analytics
 * backend was slow would be the integration taking the product down, which is the one thing "no
 * integration is a startup dependency" is written to prevent.
 *
 * # Why the payload is assembled here rather than by a client library
 *
 * The Measurement Protocol body is four fields. A library would bring its own event shape, its own
 * defaults and its own idea of what a "user" is — and `client_id` is required by the protocol, so the
 * value used is a CONSTANT rather than anything derived from a request. That is deliberate: a
 * per-request client id would be a user identifier invented to satisfy a schema.
 */
export async function relayConsoleEvent(config: RelayConfig, event: ConsoleEvent): Promise<void> {
  if (relayState(config) !== "configured") return;
  const send = config.fetchImpl ?? fetch;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), config.timeoutMs ?? 2000);
  try {
    await send(
      `https://www.google-analytics.com/mp/collect?measurement_id=${encodeURIComponent(config.measurementId)}` +
        `&api_secret=${encodeURIComponent(config.apiSecret)}`,
      {
        method: "POST",
        signal: controller.signal,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          // 🔴 A CONSTANT, not a per-request value. The protocol requires the field; nothing about this
          // deployment requires it to identify anybody, and a per-request id invented to satisfy a
          // schema is a user identifier with a different name.
          client_id: "heros-console-server",
          non_personalized_ads: true,
          events: [
            {
              name: event["event.name"],
              params: {
                surface_id: event.surface_id,
                plan_name: event.plan_name,
                edition: event.edition,
                release: event.release,
                occurred_at: event.occurred_at,
              },
            },
          ],
        }),
      },
    );
  } catch {
    // Fail-static. There is nothing a caller can do about a failed analytics transmit, and a served
    // request must never learn that one happened.
  } finally {
    clearTimeout(timer);
  }
}
