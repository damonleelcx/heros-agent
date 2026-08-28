import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Save an axis change against the reader's own node (P37 §5.4).
 *
 * 🔴 A 200 here is not evidence of a write, and the surface does not treat it as one: it renders the
 * `config_hash` the response carries, and `tests/p37-save.test.mjs` proves that hash is the one the
 * registry row and the variant row produce. That four-layer treatment is the repository's standing
 * answer to `INSERT OR IGNORE` returning success over a row that was never written.
 *
 * The change arrives back stamped `unverified` and stays that way until the harness has run. There is no
 * parameter on this route that changes that — a submission cannot assert its own quality.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.authoringSubmit(), { method: "POST", body });
}
