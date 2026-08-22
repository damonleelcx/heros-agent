import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Running an assessment, from the console's server side (P33 §6).
 *
 * # 🔴 `reinfer` is forwarded, and the DEFAULT is the control that matters
 *
 * An ordinary run is idempotent and free on a pinned revision: the platform returns the stored report
 * and makes no provider call. A re-inference ignores the pin and SPENDS the platform's own provider
 * money (PRD §14 A2 — an inference is our spend, not the customer's).
 *
 * So the flag defaults to false HERE as well as in Go, and the duplication is the point: the platform's
 * default is what makes the property true of the system, and this one makes a browser bug visible as a
 * browser bug rather than as a surprise on a spend report. A `reinfer` that arrives as anything other
 * than the boolean `true` is a false.
 *
 * # 🚫 What this route can never carry
 *
 * A tenant. It comes from the scoped token this call presents — a tenant a request can name is a tenant
 * a request can change, and this request spends money and writes a durable report.
 *
 * A list of axes. The platform refuses an unratified key, and the key a future client would add here is
 * exactly that: a request to assess a subset, which would produce a report with fewer than nine
 * findings and defeat the requirement that all nine are always present.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    workflow_id?: string;
    reinfer?: boolean;
  };

  const workflowId = (body.workflow_id ?? "").trim();
  if (!workflowId) {
    return Response.json({ error: "name the workflow to assess" }, { status: 400 });
  }

  return forward(context, "/api/v1/assessments", {
    method: "POST",
    body: {
      workflow_id: workflowId,
      // Strict equality, not truthiness. `"false"`, `1` and `"on"` are all truthy in JavaScript and
      // all of them would spend money a reader did not ask to spend.
      reinfer: body.reinfer === true,
    },
  });
}
