import { withSession, isResponse, forward } from "@/lib/bff";
/** P10 matrix — the current in-force bindings for a workflow (one per node). */
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  const wf = new URL(request.url).searchParams.get("workflow") ?? "";
  return forward(context, context.paths.studioWorkflowBindings(wf));
}
