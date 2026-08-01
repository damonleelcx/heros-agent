import "server-only";
import { headers } from "next/headers";
import { resolveSurface } from "../../../design-system/surface-map.ts";
import { isGranted, readConsent } from "@/lib/consentPrefs";
import type { ReportingConfig } from "../../../design-system/error-report.ts";

/**
 * reporting.ts assembles the browser reporter's configuration on the SERVER.
 *
 * # Why the server assembles it
 *
 * Three of the four values are server facts and the fourth is a decision the browser must not be
 * trusted with:
 *
 *   - the DSN comes from the deployment's environment, and is ABSENT on every substrate except the
 *     platform's own hosted deployment;
 *   - the release and edition are properties of the build and the topology;
 *   - the SURFACE is resolved from `x-pathname` through the closed enum, so the browser never has to
 *     derive it from `location.href` — which is the whole point, because a URL under `/app` carries
 *     variant, run, node and tenant identifiers;
 *   - and the consent decision is read from a first-party cookie the server can see, so an ungranted
 *     category produces a page with no listener attached rather than a page that attaches one and then
 *     decides not to send.
 *
 * # The DSN is public, and saying so matters
 *
 * An ingest DSN carries a PUBLIC key: it authorises writing an event, nothing else, and it is designed
 * to be in a browser. It is not in `scan-bundle.mjs`'s credential list for that reason, and flagging it
 * would teach people to switch the scan off. What is NOT public — and never reaches this process — is
 * the release-scoped auth token that uploads source maps.
 */
export async function reportingConfig(): Promise<ReportingConfig> {
  const h = await headers();
  const consent = await readConsent();
  const pathname = h.get("x-pathname") ?? "/";
  return {
    dsn: process.env.HEROS_ERROR_REPORTING_DSN ?? "",
    release: process.env.HEROS_VERSION ?? "unknown",
    edition: process.env.HEROS_EDITION ?? "unknown",
    surface: resolveSurface("customer", pathname),
    // The identity the platform already minted for this request, so a browser failure joins the server
    // request that served the page rather than starting a second incident.
    traceId: h.get("x-trace-id") ?? undefined,
    granted: isGranted(consent, "error_diagnostics"),
  };
}
