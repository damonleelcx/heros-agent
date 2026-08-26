import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Turning a question into a bounded PLAN, from the console's server side (P35 §6.1).
 *
 * # 🔴 This route spends nothing, and that is its whole purpose
 *
 * FR1/FR2: a plan is the artifact a person can DECLINE. Before the run it is a decision; after the run
 * the same information is a receipt, and only one of those is useful. So planning is a separate call
 * from running, and this is the one that is free.
 *
 * # 🚫 What this route can never carry
 *
 * A budget, a candidate cap, a list of axes, an origin, or a tenant. Every one of them would let a
 * browser widen its own run:
 *
 *   budget / cap   the plan's bounds come from the tenant's ENTITLEMENT. A person typing a sentence is
 *                  the one least able to price it, and a bound a request can set is not a bound.
 *   axes           read from the question. A body field would let a client silently re-scope a run the
 *                  person described differently.
 *   origin         read from the TRANSPORT by the platform, because the origin selects the delivery
 *                  MODE — console runs use the hosted Git App — and an origin a caller could set is an
 *                  origin a caller sets to `console` to reach a write credential.
 *   tenant         from the scoped token this call presents.
 *
 * The platform refuses an unratified key outright; this route simply never sends one.
 *
 * # `acknowledge` — the one flag, and why it is on THIS route
 *
 * Above the disclosure threshold the run does not begin until the plan is acknowledged. The flag is on
 * the PLAN call because acknowledging is agreeing to a plan you have SEEN — a client that sets it
 * without rendering the plan first has acknowledged something it never showed anybody, and the
 * acknowledgement records what was projected so that is visible afterwards rather than invisible.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    question?: string;
    acknowledge?: boolean;
  };

  const question = (body.question ?? "").trim();
  if (!question) {
    return Response.json({ error: "ask a question — for example, fix what you can prove" }, { status: 400 });
  }

  return forward(context, context.paths.improvementPlan(), {
    method: "POST",
    body: {
      question,
      // Strict equality, not truthiness. `"false"`, `1` and `"on"` are all truthy in JavaScript, and
      // an acknowledgement is the thing standing between a sentence and a bill.
      acknowledge: body.acknowledge === true,
    },
  });
}
