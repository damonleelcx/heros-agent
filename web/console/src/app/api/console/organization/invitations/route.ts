import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Creating an invitation.
 *
 * The seat check happens on the platform, at invite time AND at acceptance — the first is the good error
 * message (the owner, who can act, sees it) and the second is the one that is actually true when the
 * membership is created. Its refusal names both numbers, and the console renders them rather than
 * summarising.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => ({}));
  return forward(context, context.paths.invitations(), { method: "POST", body });
}
