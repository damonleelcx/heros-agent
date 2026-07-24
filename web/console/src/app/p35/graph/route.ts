import { type NextRequest } from "next/server";
import { redirectTo } from "@/lib/redirect";

/** `/p35/graph?workflow_id=` -> the canonical graph route (FR11). */
export const dynamic = "force-dynamic";

export function GET(request: NextRequest) {
  const workflowId = request.nextUrl.searchParams.get("workflow_id");
  if (workflowId) {
    return redirectTo(`/app/workflows/${encodeURIComponent(workflowId)}/graph`, 308);
  }
  return redirectTo("/app/workflows", 308);
}
