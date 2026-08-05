import { withSession, isResponse, forward } from "@/lib/bff";
import type { PlanChangeView } from "@/lib/types.generated";

/**
 * P21 task 6.2 — subscribe / upgrade / downgrade BY PLAN NAME.
 *
 * One route for all three, because they are one operation: move this tenant to the plan with this
 * name. Which of the three it is depends on where the tenant is now, and that is a fact the platform
 * holds — a console that decided "this is a downgrade" would be a console holding an opinion about
 * rank, and the first time the plan catalogue changed it would be a wrong one.
 *
 * 🔴 The tenant is the SESSION's, from `scope.ts`. A customer id in the body would be a scope-widening
 * attempt, and there is no parameter here to supply one.
 *
 * The entitlement flips at the plan-change event the platform records; the money proration is the
 * payment provider's. Neither system guesses the other's fact.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;

  const body = (await request.json().catch(() => null)) as { plan_name?: unknown } | null;
  const planName = typeof body?.plan_name === "string" ? body.plan_name : "";
  if (planName === "") {
    return Response.json(
      { error: "a plan change needs the name of the plan to move to", kind: "bad_request" },
      { status: 400, headers: { "cache-control": "no-store" } },
    );
  }

  return forward<PlanChangeView>(context, context.paths.changePlan(), {
    method: "POST",
    body: { plan_name: planName },
  });
}
