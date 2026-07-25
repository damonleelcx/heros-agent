import { withSession, isResponse, forward } from "@/lib/bff";
/** P10 matrix — bind a node to a cell's (model, prompt) via bound mode; marked unverified. */
export const dynamic = "force-dynamic";
export async function POST(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.studioBind(), { method: "POST", body });
}
