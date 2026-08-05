import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * 🔴 The preview the removal dialog cannot open without.
 *
 * It is a separate route from the removal itself precisely so the two cannot be collapsed: a
 * confirmation that fetched nothing would have to compute what removal covers, and the console does not
 * know which keys are personal. Guessing from a label is how the wrong key gets revoked — and how a
 * machine key nobody mentioned keeps deploying after somebody has left.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ userId: string }> }) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const { userId } = await params;
  return forward(context, context.paths.memberRemovalPreview(userId));
}
