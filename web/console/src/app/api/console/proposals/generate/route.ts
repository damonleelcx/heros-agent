import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Run one proposal-generation pass (P30 §1.11).
 *
 * # Why this returns JSON rather than redirecting
 *
 * The open-PR route beside it redirects, because opening a pull request navigates a reader somewhere.
 * A generation pass does not: it answers a question about the page the reader is already on. The whole
 * point of the trigger is that the state and its sentence change WITHOUT a reload — a full navigation
 * would lose scroll position and the selected tab, and it would make "the pass ran and found nothing"
 * indistinguishable from "the page reloaded" for the reader watching it.
 *
 * # The workflow id is in the BODY, and it is checked here
 *
 * `forward` sends it to `/api/v1/proposal-generations`, whose whole shape exists so the route can be
 * published `Exact`. The tenant is NOT in the body and cannot be: the platform takes it from the
 * authenticated principal, and this handler takes it from the session. A tenant a browser could name
 * would be a scope-widening parameter.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;

  let workflowId = "";
  try {
    const body = (await request.json()) as { workflow_id?: unknown };
    workflowId = typeof body.workflow_id === "string" ? body.workflow_id : "";
  } catch {
    workflowId = "";
  }
  if (!workflowId) {
    return Response.json(
      { error: "a generation pass must name a workflow", kind: "not-found" },
      { status: 400, headers: { "cache-control": "no-store" } },
    );
  }

  return forward(context, context.paths.generateProposals(), {
    method: "POST",
    body: { workflow_id: workflowId },
  });
}
