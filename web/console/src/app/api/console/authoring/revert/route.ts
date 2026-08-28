import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Back out an authored change (P13 §11, kept by P37's editor kit).
 *
 * Reverting re-derives the immutable parent rather than applying the inverse of the edits, so the result
 * is byte-identical to the configuration the reader had — not merely equivalent to it. Applying an
 * inverse edit is how an undo quietly becomes a third configuration.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.authoringRevert(), { method: "POST", body });
}
