import { withSession, isResponse, forward } from "@/lib/bff";
/** P10 matrix — the loaded workflows the matrix can open. */
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  return forward(context, context.paths.studioWorkflows());
}
