import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Removing a member. The only DELETE in the console's BFF, and it is here rather than in a server action
 * because the confirmation dialog needs the platform's own refusal codes to render the right sentence.
 *
 * The user id is a SUBJECT, not an authority: the platform scopes it to the caller's organization and
 * answers 404 for anybody else's, which is deliberately indistinguishable from "no such member".
 */
export const dynamic = "force-dynamic";

export async function DELETE(request: Request, { params }: { params: Promise<{ userId: string }> }) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const { userId } = await params;
  return forward(context, context.paths.member(userId), { method: "DELETE" });
}
