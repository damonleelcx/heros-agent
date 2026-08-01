import "server-only";

import type { AxisCoverageView } from "@/lib/types.generated";
import { platformFetch } from "@/lib/platformApi";

/**
 * fetchCoverage reads the total coverage table from the platform.
 *
 * 🚫 There is no local fallback table. A console that carried its own copy would be the second coverage
 * source the contract exists to prevent, and it would drift in the usual direction — the copy is always
 * the optimistic one. When the platform cannot be read, the page says so rather than rendering a
 * plausible table nobody verified.
 *
 * 🔴 The tenant is passed because every platform call carries the session's scope, and NOT because
 * coverage varies by tenant — it does not, and the API takes no tenant at all. Sending it here keeps one
 * call convention; the assertion that coverage is plan-invariant lives where it belongs, on the server
 * (`handleCoverage` takes no plan, role, or tenant).
 */
export async function fetchCoverage(tenantId: string): Promise<AxisCoverageView | null> {
  const outcome = await platformFetch<AxisCoverageView>("/api/v1/coverage", { tenantId });
  return outcome.ok ? outcome.data : null;
}
