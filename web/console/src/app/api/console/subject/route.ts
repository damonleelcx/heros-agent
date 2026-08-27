import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { requireSession } from "@/lib/session";
import { SUBJECT_COOKIE, SUBJECT_COOKIE_OPTIONS, encodeSubject } from "@/lib/axisSubject";
import { WORKING_SURFACES } from "@/lib/routes";

/**
 * POST /api/console/subject — remember which node the axis surfaces are bound to (P37 FR2, D-37.4).
 *
 * # 🔴 This route makes NO platform call
 *
 * It writes a browser cookie carrying two identifiers and nothing else. The node it names is validated
 * on the NEXT render, by the resolver, against the live node list — `enumeration.ts`'s discard rule:
 * *"a remembered subject the enumeration does not contain is DISCARDED rather than rendered."* Validating
 * here as well would be a second copy of that rule, and the second copy is the one that goes stale.
 *
 * So a cookie naming a node that does not exist is not a security problem and not a data problem: it is
 * discarded silently and the reader falls back to the resolver's own order, which is where a first-time
 * reader starts.
 *
 * # Why the redirect target is allow-listed
 *
 * `return_to` comes from a form field, and a redirect whose destination is attacker-supplied is an open
 * redirect. It is matched against `WORKING_SURFACES` — the console's own closed list of routes — rather
 * than pattern-checked, because "starts with /app" is the check that eventually admits `//evil.example`.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  // Fail closed. A subject is per-person UI state and a signed-out browser has no person.
  await requireSession();

  const form = await request.formData();
  const workflowId = String(form.get("workflow_id") ?? "").trim();
  const nodeId = String(form.get("node_id") ?? "").trim();
  const requested = String(form.get("return_to") ?? "").trim();

  const destination = WORKING_SURFACES.includes(requested) ? requested : "/app";

  if (workflowId && nodeId) {
    (await cookies()).set(SUBJECT_COOKIE, encodeSubject({ workflow_id: workflowId, node_id: nodeId }), {
      ...SUBJECT_COOKIE_OPTIONS,
    });
  }

  // 303, so the browser follows with a GET. A 307 would repeat the POST on refresh.
  return NextResponse.redirect(new URL(destination, request.url), 303);
}
