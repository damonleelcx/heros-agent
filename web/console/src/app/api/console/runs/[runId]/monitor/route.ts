import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * The live monitor's snapshot — the POLLING FALLBACK's endpoint (P25-9).
 *
 * This route is why the fallback still works behind a proxy that breaks SSE: it is an ordinary JSON
 * GET with nothing streaming about it. Removing it because "we have SSE" is exactly the resilience
 * loss the port is organised to prevent (design.md Decision 4).
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ runId: string }> }) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  const { runId } = await params;
  return forward(context, context.paths.monitor(runId));
}
