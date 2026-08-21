import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Submit a question (P31 task 2.2).
 *
 * 🔴 The platform answers **202**, not 200, and this forwards that unchanged. The distinction is the
 * product: the turn runs on a goroutine detached from this request, so a 200 would tell the client the
 * work is done when it has only been accepted. The client's next act is to open the stream, and 202 is
 * what says so.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.conversationTurn(), { method: "POST", body });
}
