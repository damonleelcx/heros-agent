import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Starting a local-mode pairing (P32 §4, design D5).
 *
 * # 🚫 There is no file picker, and this route is why there cannot be one
 *
 * All this does is ask the platform for a CODE. Nothing about the customer's filesystem is named here,
 * because nothing about it reaches the browser: the tree is read by an agent already running on the
 * machine that holds it. A browser affordance that read a folder and posted it would be Mode 1 wearing
 * Mode 3's clothes — the control would say "select a local repo" and the customer would reasonably
 * believe nothing left their machine.
 *
 * # The availability refusal arrives as a 409, and that is not an error
 *
 * The platform refuses to issue a code on a deployment the pinned bridge cannot reach. That is FR15:
 * the limit is stated at step zero rather than at the end of the flow. The sentence is forwarded as
 * written and the console renders it as a stated boundary, not as a fault.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as { workflow_id?: string };
  const workflowId = (body.workflow_id ?? "").trim();
  if (!workflowId) {
    return Response.json({ error: "name the workflow this machine will read for" }, { status: 400 });
  }
  return forward(context, "/api/v1/local-pairings", { method: "POST", body: { workflow_id: workflowId } });
}
