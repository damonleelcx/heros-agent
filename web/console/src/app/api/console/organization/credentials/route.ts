import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Creating an API key.
 *
 * 🔴 This is the ONE console route whose response carries a secret, and it carries it because that is
 * the only moment the value exists. It is forwarded verbatim to the browser, held in component state,
 * and written nowhere — no cookie, no storage, no log. `forward` does not transform bodies, which is
 * what keeps that true without a special case here.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => ({}));
  return forward(context, context.paths.credentials(), { method: "POST", body });
}
