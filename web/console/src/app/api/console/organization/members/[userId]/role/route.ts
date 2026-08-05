import { withSession, isResponse, forward } from "@/lib/bff";

/** Changing a member's role. The platform refuses a change that would leave no owner, by name. */
export const dynamic = "force-dynamic";

export async function POST(request: Request, { params }: { params: Promise<{ userId: string }> }) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const { userId } = await params;
  const body: unknown = await request.json().catch(() => ({}));
  return forward(context, context.paths.memberRole(userId), { method: "POST", body });
}
