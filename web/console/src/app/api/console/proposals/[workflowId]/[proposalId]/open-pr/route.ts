import { withSession, isResponse } from "@/lib/bff";
import { platformFetch } from "@/lib/platformApi";
import { redirectTo } from "@/lib/redirect";

/**
 * Open a pull request for a verified proposal (P5.5).
 *
 * # Why the console does not decide whether this is allowed
 *
 * It forwards, and the platform refuses. `handleP55OpenPR` will not act unless the proposal's verdict
 * passed AND the workflow is at Assisted automation, so "unverified never ships" cannot be bypassed by
 * calling the endpoint directly — including by this console. The card's `can_open_pr` is a rendering
 * hint, not a gate: putting the gate in the browser would put it somewhere a browser can remove it.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request, { params }: { params: Promise<{ workflowId: string; proposalId: string }> }) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const { workflowId, proposalId } = await params;

  const outcome = await platformFetch<{ url?: string }>(context.paths.openPR(workflowId, proposalId), {
    tenantId: context.session.tenantId,
    method: "POST",
  });

  const back = `/app/workflows/${encodeURIComponent(workflowId)}/proposals/${encodeURIComponent(proposalId)}`;
  if (!outcome.ok) {
    // The platform's own refusal, carried back as a reason rather than a generic failure. Its message
    // says WHY — an unverified verdict, an automation level that does not permit it — and that is the
    // whole content of the answer.
    return redirectTo(`${back}?pr_error=${encodeURIComponent(outcome.error)}`);
  }
  return redirectTo(`${back}?pr_opened=${encodeURIComponent(outcome.data?.url ?? "1")}`);
}
