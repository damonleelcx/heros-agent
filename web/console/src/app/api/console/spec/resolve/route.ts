import { withSession, isResponse, forward } from "@/lib/bff";

/** P2's **Validate only** — resolve a Variant Spec's refs without submitting anything (P2-5). */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;
  // The body is forwarded as received. It is a Variant Spec — customer content — so it is never
  // logged, never inspected for business rules, and never "normalised" on the way through.
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.resolveSpec(), { method: "POST", body });
}
