import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Approve inside the conversation (P31 FR8, design.md D4).
 *
 * 🚫 **No gate here.** Not an entitlement check, not an automation-level check, not an attribution
 * decision. A "yes" typed in a chat window is not a new authorization primitive, and a second gate is a
 * second place for the three checks that stand between a proposal and a customer's repository to be
 * wrong. The person and the tenant come from the platform's own view of this credential; nothing in
 * this body names either, and the platform refuses a body that tries to.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.conversationApproval(), { method: "POST", body });
}
