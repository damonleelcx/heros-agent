import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * The preflight for an axis change the reader is composing (P13 §11, bound to the reader's own node by
 * P37 FR5).
 *
 * # Why the status is forwarded unchanged
 *
 * The three verdicts are a 200 — `admissible`, `refused` and `not_yet_measurable` are all ANSWERS, and
 * a refusal mapped to a 4xx would make the surface render an error state for a verdict the product
 * deliberately produces. What a non-200 means here is that the preflight did not happen, which is a
 * different sentence with a different next action.
 *
 * # 🔴 The browser derives nothing (NFR7.3)
 *
 * The resulting `config_hash` and the diff against the parent variant are computed server-side and
 * rendered as received. This route exists so an editor is a place to COMPOSE a value, never a place to
 * COMPUTE one — which is what `/app/memory`'s local `hashFor()` had become.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.authoringPreflight(), { method: "POST", body });
}
