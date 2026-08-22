import "server-only";

import { requireSession } from "@/lib/session";
import { scoped } from "@/lib/scope";
import { platformFetch, type PlatformOutcome } from "@/lib/platformApi";
import type { AssessmentView } from "@/lib/types.generated";

/**
 * data.ts reads a workflow's latest assessment, and runs one.
 *
 * 🚫 There is no local fallback and no cached copy. A console that carried its own last-known report
 * would show a reader claims about a revision the platform may no longer hold, with no way to tell —
 * and the whole product of this surface is knowing what is and is not established.
 */

/** fetchAssessment reads the newest assessment of one workflow. */
export async function fetchAssessment(workflowId: string): Promise<PlatformOutcome<AssessmentView>> {
  const session = await requireSession();
  const paths = scoped(session);
  return platformFetch<AssessmentView>(paths.assessments(workflowId), {
    tenantId: session.tenantId,
    userId: session.userId,
  });
}

/**
 * runAssessment starts one.
 *
 * 🔴 `reinfer` defaults to false, and the default is load-bearing. An ordinary run is idempotent and
 * free on a pinned revision — the platform returns the stored report and makes no provider call — so a
 * page may call it. A re-inference ignores the pin and SPENDS the platform's provider money, which is
 * why the control that sets this flag is an explicit button with its own copy, never a page load.
 */
export async function runAssessment(
  workflowId: string,
  reinfer = false,
): Promise<PlatformOutcome<AssessmentView>> {
  const session = await requireSession();
  const paths = scoped(session);
  return platformFetch<AssessmentView>(paths.runAssessment(), {
    tenantId: session.tenantId,
    userId: session.userId,
    method: "POST",
    body: { workflow_id: workflowId, reinfer },
  });
}
