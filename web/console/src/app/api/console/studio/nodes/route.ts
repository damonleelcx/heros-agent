import { withSession, isResponse, forward } from "@/lib/bff";
/** P10 matrix — a workflow's nodes (columns). ?workflow= is a subject the platform scopes. */
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  const wf = new URL(request.url).searchParams.get("workflow") ?? "";
  return forward(context, context.paths.studioNodes(wf));
}
