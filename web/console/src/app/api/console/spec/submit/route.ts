import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * P2's **Submit & run** (P2-6…11).
 *
 * The HTTP status is forwarded unchanged because the console's copy depends on it: the *nothing was
 * persisted — no spec, no transform, no run* message appears on **400 only**, and is deliberately
 * withheld on 500/503 where persistence is unknown (P2-9). A BFF that normalised every failure to
 * 500 would make that conditional unimplementable, and the message would either disappear or start
 * lying about a case where nobody knows what was written.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.submitSpec(), { method: "POST", body });
}
