import "server-only";
import { headers } from "next/headers";
import { resolveSurface } from "../../../design-system/surface-map.ts";
import type { ReportingConfig } from "../../../design-system/error-report.ts";

/**
 * reporting.ts assembles the operator console's browser-reporter configuration.
 *
 * # 🔴 Why `granted` is true here without a banner, and why that is not a shortcut
 *
 * The operator console presents NO consent banner. It is a staff surface governed by the internal
 * acceptable-use notice, not a visitor surface, and the person in front of it is an employee acting in
 * that capacity — asking them to consent to their employer's error diagnostics would be consent
 * theatre, and a banner on an incident console is a control an operator learns to dismiss without
 * reading, which is worse than no banner at all.
 *
 * The exception is legitimate only because of what the payload contains: an error event here is
 * constructed from a thirteen-field allowlist, carries no message body unless it is a member of a
 * closed enum, carries no breadcrumb collection at all, and names the surface by id rather than by URL.
 * There is no personal data in it by construction. **Session replay and analytics remain refused on
 * this console structurally** — not gated on a grant that this file could flip, but absent from the
 * operator class's permitted categories in `web/design-system/third-party-policy.ts`.
 *
 * That exception is STATED in the acceptable-use notice (P24 task 4.9) rather than inferred from the
 * absence of a banner, because a reader who finds no banner cannot tell a decision from an omission.
 */
export async function reportingConfig(): Promise<ReportingConfig> {
  const h = await headers();
  const pathname = h.get("x-pathname") ?? "/";
  return {
    dsn: process.env.HEROS_ERROR_REPORTING_DSN ?? "",
    release: process.env.HEROS_VERSION ?? "unknown",
    edition: process.env.HEROS_EDITION ?? "unknown",
    surface: resolveSurface("operator", pathname),
    traceId: h.get("x-trace-id") ?? undefined,
    granted: true,
  };
}
