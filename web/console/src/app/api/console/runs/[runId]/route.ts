import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * The run record, polled by the P2 watch toggle (P2-23…26).
 *
 * It exists as a route because the watch is a client behavior: the toggle re-reads at 1 s until the
 * RUN RECORD's status is terminal (P2-24) — never until a node-derived condition — and a server
 * re-render per second would be a page reload per second.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ runId: string }> }) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const { runId } = await params;
  return forward(context, context.paths.run(runId));
}
