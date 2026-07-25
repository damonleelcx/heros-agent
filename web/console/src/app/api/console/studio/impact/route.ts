import { withSession, isResponse, forward } from "@/lib/bff";

/** P10 studio — impact analysis for a proposed body, BEFORE publish (task 4.4). */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.promptImpact(), { method: "POST", body });
}
