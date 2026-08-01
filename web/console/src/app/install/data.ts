import "server-only";

import type { InstallView } from "@/lib/types.generated";
import { platformFetchPublic } from "@/lib/platformApi";

/**
 * fetchInstall reads the distribution contract from the platform, WITHOUT a session.
 *
 * 🔴 The absence of a session is the requirement, not a shortcut. This page tells a reader how to obtain the
 * CLI and states that the CLI is free with no account — and it sat behind `requireSession()` until someone
 * pointed out the obvious: the people who need it are exactly the people who have no account yet. A sign-in
 * wall in front of "here is how to install the free thing" is a contradiction the page itself refutes two
 * paragraphs later.
 *
 * That it CAN be session-less is a property of the endpoint, not a favour: `/api/v1/install` takes no tenant,
 * no plan and no role, because which platforms are built and which channels exist are facts about the RELEASE.
 * The server asserts that by signature, so this cannot quietly become tenant-varying data served without a
 * session.
 *
 * 🚫 There is still no local fallback table. A console carrying its own copy of which channels work would be
 * the second source of truth the whole contract exists to prevent, and it would drift in the predictable
 * direction — the local copy is always the optimistic one. When the platform cannot be read, the page says so
 * rather than rendering a plausible install command nobody verified.
 */
export async function fetchInstall(): Promise<InstallView | null> {
  const outcome = await platformFetchPublic<InstallView>("/api/v1/install");
  return outcome.ok ? outcome.data : null;
}
