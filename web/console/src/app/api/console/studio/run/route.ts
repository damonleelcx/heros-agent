import { withSession, isResponse, forward } from "@/lib/bff";
/** P10 matrix — test-run a cell (output + cost + latency + tokens; exploratory, no ranking). */
export const dynamic = "force-dynamic";
export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.studioRun(), { method: "POST", body });
}
