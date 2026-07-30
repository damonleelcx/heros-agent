import "server-only";

import type { InstallView } from "@/lib/types.generated";
import { platformFetch } from "@/lib/platformApi";

/**
 * fetchInstall reads the distribution contract from the platform.
 *
 * 🚫 There is no local fallback table. A console carrying its own copy of which channels work would be the
 * second source of truth the whole contract exists to prevent, and it would drift in the predictable direction
 * — the local copy is always the optimistic one, because nobody re-checks a table that already looks right.
 * When the platform cannot be read, the page says so rather than rendering a plausible install command nobody
 * verified.
 *
 * 🔴 The tenant is passed because every platform call carries the session's scope, NOT because the answer varies
 * by tenant — it does not, and the API takes no tenant at all. Which platforms are built and which channels
 * exist are properties of the release; the assertion that no entitlement can move a row lives where it belongs,
 * on the server (`handleP20Install` takes no plan, role or tenant).
 */
export async function fetchInstall(tenantId: string): Promise<InstallView | null> {
  const outcome = await platformFetch<InstallView>("/api/p20/install", { tenantId });
  return outcome.ok ? outcome.data : null;
}
