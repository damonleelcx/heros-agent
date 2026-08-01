"use client";

/**
 * publicAnalytics.tsx installs the public-surface tags. It renders NOTHING.
 *
 * The same React seam as `errorReporting.tsx`, and everything that decides WHAT loads lives in
 * `web/design-system/public-analytics.ts`. What is worth reading here is the guard on the other side of
 * the boundary: this component is mounted from the PUBLIC layout only, and the module it calls refuses
 * a surface id that is not `public.*` — so the refusal survives somebody mounting it in the wrong place.
 *
 * The third mechanism is the policy itself: `product_analytics` and `session_replay` are absent from
 * the tenant and operator surface classes, so those prefixes name neither origin and a browser would
 * refuse the request even if both other guards failed. Three independent mechanisms, because one is a
 * checklist.
 */

import { useEffect } from "react";
import { installPublicAnalytics, observeFunnel, track, type PublicAnalyticsConfig } from "../../../design-system/public-analytics.ts";
import { SECTIONS } from "../../../design-system/analytics-events.ts";

export function PublicAnalytics(config: PublicAnalyticsConfig) {
  const { measurementId, clarityProjectId, surface, release, edition } = config;
  const productAnalytics = config.granted.productAnalytics;
  const sessionReplay = config.granted.sessionReplay;
  useEffect(() => {
    let stop: (() => void) | undefined;
    installPublicAnalytics(
      { measurementId, clarityProjectId, surface, release, edition, granted: { productAnalytics, sessionReplay } },
      () => {
        // Runs only after the tag is actually installed. Reporting before that point is a silent no-op —
        // which is exactly what an earlier version did, and what the acceptance run caught by measuring
        // zero bytes to the measurement endpoint on a page whose tag had plainly loaded.
        if (surface === "public.install") track("install_page_viewed");
        if (surface === "public.signin") track("signup_started");
        stop = observeFunnel(SECTIONS);
      },
    );
    return () => stop?.();
  }, [measurementId, clarityProjectId, surface, release, edition, productAnalytics, sessionReplay]);
  return null;
}
