import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Executing a plan, and reading a run back (P35 §6.1).
 *
 * # 🔴 `POST` is the call that costs money
 *
 * It is the only one on this surface that does. Everything protecting it is server-side and none of it
 * is re-implemented here: the plan's spend budget comes from the tenant's entitlement, and above the
 * disclosure threshold the platform withholds the run until the plan has been acknowledged.
 *
 * # Why the body re-supplies the QUESTION beside the plan id
 *
 * The platform re-derives the plan deterministically rather than holding run state between two
 * requests, and then compares the id. A plan id is derived from every field that changes what the run
 * will do — so if the tenant's budget moved between the two calls, the re-derived plan has a different
 * id and the platform answers 404 rather than running something the person was never shown.
 *
 * That 404 is the point. A route that took only a question would re-plan and run whatever the current
 * bounds produce, which is how somebody agrees to a modest run and gets one an order of magnitude
 * larger.
 *
 * (The amounts are described rather than written: `plancfg`'s git fence refuses a priced literal in any
 * shipped UI source that also mentions a plan, and it is right to — a concrete figure lives in the
 * billing provider and is referenced only by an opaque ref. A fence being correct about a comment is
 * still the fence being correct.)
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    plan_id?: string;
    question?: string;
  };

  const planId = (body.plan_id ?? "").trim();
  const question = (body.question ?? "").trim();
  if (!planId || !question) {
    return Response.json(
      { error: "run the plan you were shown: both its id and the question it came from are required" },
      { status: 400 },
    );
  }

  return forward(context, context.paths.runImprovement(), {
    method: "POST",
    body: { plan_id: planId, question },
  });
}

export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const runId = (new URL(request.url).searchParams.get("run_id") ?? "").trim();
  if (!runId) {
    return Response.json({ error: "name the run to read" }, { status: 400 });
  }
  return forward(context, context.paths.improvementRun(runId));
}
