import { type NextRequest } from "next/server";
import { redirectTo } from "@/lib/redirect";

/**
 * `/p4/board?workflow=` -> the canonical board route (FR11).
 *
 * 🔴 With NO parameter this goes to SELECTION. The legacy page does
 * `params.get('workflow') || 'wf-demo'`, so a user who opens it bare is shown a fully rendered,
 * confident board for a workflow that is not theirs. That default is deliberately not ported (P4-0):
 * an empty state tells the truth, a wrong default asserts a falsehood with the full authority of a
 * populated UI.
 */
export const dynamic = "force-dynamic";

export function GET(request: NextRequest) {
  const workflow = request.nextUrl.searchParams.get("workflow");
  if (workflow) {
    return redirectTo(`/app/workflows/${encodeURIComponent(workflow)}/board`, 308);
  }
  return redirectTo("/app/workflows", 308);
}
