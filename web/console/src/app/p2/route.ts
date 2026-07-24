import { type NextRequest } from "next/server";
import { redirectTo } from "@/lib/redirect";

/**
 * `/p2?run=` / `?cfg=`+`?rev=` -> the canonical route (FR11, R9).
 *
 * # Why these compatibility routes exist at all
 *
 * R8 removes hand-typed parameters as an ENTRY MECHANISM. It must not remove **shareability**, which
 * is a real and used capability — `p2.html` deliberately supports `?run=` and `?cfg=`+`?rev=` auto-load
 * precisely so a link can point at evidence. Fixing the input problem by making views unaddressable
 * would trade one defect for a worse one.
 *
 * So every legacy form resolves, permanently, to the canonical route for the same subject. A link
 * pasted into a pull request two months ago still opens the thing it was pasted to show.
 *
 * 308 rather than 302: the mapping is permanent and the method must be preserved.
 */
export const dynamic = "force-dynamic";

export function GET(request: NextRequest) {
  const q = request.nextUrl.searchParams;
  const run = q.get("run");
  if (run) return redirectTo(`/app/runs/${encodeURIComponent(run)}`, 308);

  const cfg = q.get("cfg");
  const rev = q.get("rev");
  if (cfg && rev) {
    return redirectTo(`/app/transforms/${encodeURIComponent(cfg)}/${encodeURIComponent(rev)}`, 308);
  }
  // One half of a two-part key is not a subject. It goes to the picker, which says so — a different
  // message from "not found".
  if (cfg || rev) {
    const query = new URLSearchParams();
    if (cfg) query.set("config_hash", cfg);
    if (rev) query.set("source_revision", rev);
    return redirectTo(`/app/transforms?${query}`, 308);
  }
  // No parameters: the authoring panel is the only one of P2's three that has no subject until you
  // submit.
  return redirectTo("/app/configure", 308);
}
