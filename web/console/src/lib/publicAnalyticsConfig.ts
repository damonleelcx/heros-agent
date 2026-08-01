import "server-only";
import { headers } from "next/headers";
import { resolveSurface } from "../../../design-system/surface-map.ts";
import { isGranted, readConsent } from "@/lib/consentPrefs";
import type { PublicAnalyticsConfig } from "../../../design-system/public-analytics.ts";

/**
 * publicAnalyticsConfig assembles the public surface's tag configuration on the SERVER.
 *
 * # Absent by default, on one variable each
 *
 * `HEROS_GA4_MEASUREMENT_ID` and `HEROS_CLARITY_PROJECT_ID`. Empty means the tag is not loaded at all —
 * no listener, no request, no marker — and empty is the value on every substrate except the platform's
 * own hosted deployment. There is no fallback, no file and no discovered default, which is what makes
 * the air-gapped package's zero-external-origin assertion a property of the build rather than of a
 * deployment being careful.
 *
 * Both ids are PUBLIC identifiers: neither authorises a read, and both are designed to be in a browser.
 * What is not public is nothing — there is no analytics credential in this product.
 */
export async function publicAnalyticsConfig(): Promise<PublicAnalyticsConfig> {
  const h = await headers();
  const consent = await readConsent();
  const pathname = h.get("x-pathname") ?? "/";
  return {
    measurementId: process.env.HEROS_GA4_MEASUREMENT_ID ?? "",
    clarityProjectId: process.env.HEROS_CLARITY_PROJECT_ID ?? "",
    surface: resolveSurface("customer", pathname),
    release: process.env.HEROS_VERSION ?? "unknown",
    edition: process.env.HEROS_EDITION ?? "unknown",
    granted: {
      productAnalytics: isGranted(consent, "product_analytics"),
      sessionReplay: isGranted(consent, "session_replay"),
    },
  };
}
