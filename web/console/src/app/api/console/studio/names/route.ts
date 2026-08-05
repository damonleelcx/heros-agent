import { withSession, isResponse, forward } from "@/lib/bff";

/** P10 studio — list this tenant's prompt names (task 4.1). Scoped server-side by the session. */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  return forward(context, context.paths.promptNames());
}
