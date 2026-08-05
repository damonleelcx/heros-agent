import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * P10 studio — preview (task 4.6). Renders a prompt version with sample bindings and returns the exact
 * string a run would send. The result carries no score, rank, or judgement — it is exploratory. The
 * platform renders it with the SAME renderer a run uses, so the preview is byte-identical, not a
 * client-side approximation.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.studioPreview(), { method: "POST", body });
}
