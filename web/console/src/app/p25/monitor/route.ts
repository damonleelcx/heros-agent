import { type NextRequest } from "next/server";
import { redirectTo } from "@/lib/redirect";

/** `/p25/monitor?run_id=` -> the canonical live-run route (FR11). */
export const dynamic = "force-dynamic";

export function GET(request: NextRequest) {
  const runId = request.nextUrl.searchParams.get("run_id");
  if (runId) {
    return redirectTo(`/app/runs/${encodeURIComponent(runId)}/live`, 308);
  }
  // The legacy page's answer here is *"No run_id in the URL. Append ?run_id=…"* — a syntax lesson
  // where a next action belongs. This goes to selection instead (R8).
  return redirectTo("/app/runs", 308);
}
