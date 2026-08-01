import "server-only";
import { headers } from "next/headers";
import { resolveSurface } from "../../../design-system/surface-map.ts";
import {
  buildConsoleEvent,
  relayConsoleEvent,
  relayState,
} from "../../../design-system/console-analytics.ts";

/**
 * consoleAnalytics.ts emits `surface_viewed` from the SERVER half of this console's BFF.
 *
 * # What the call site can and cannot supply
 *
 * Nothing. It takes no arguments: the surface is resolved from `x-pathname` through the closed enum,
 * and everything else is a build fact. That is the point — a function that accepted a surface string
 * would be a function a call site could pass a path to, and the whole reason console analytics is
 * server-side is that a path under `/app` carries variant, run, node and tenant identifiers.
 *
 * # Fire and forget, deliberately
 *
 * The promise is not awaited by any render path. An analytics backend must never be able to slow a
 * console page, and there is nothing a reader could do with the failure if it were surfaced.
 */
export function recordSurfaceViewed(): void {
  const config = {
    measurementId: process.env.HEROS_GA4_MEASUREMENT_ID ?? "",
    apiSecret: process.env.HEROS_GA4_API_SECRET ?? "",
  };
  // Absent by default, and absent SILENTLY: no warning, no log line. On every substrate except the
  // platform's own hosted deployment this is the correct and expected state.
  if (relayState(config) !== "configured") return;

  void (async () => {
    const h = await headers();
    const event = buildConsoleEvent({
      event: "surface_viewed",
      surfaceId: resolveSurface("customer", h.get("x-pathname") ?? "/"),
      edition: process.env.HEROS_EDITION ?? "unknown",
      release: process.env.HEROS_VERSION ?? "unknown",
      occurredAt: Date.now() / 1000,
    });
    if (event) await relayConsoleEvent(config, event);
  })();
}
