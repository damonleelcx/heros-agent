import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * ONE decision on ONE proposal (P35 §6.1, design D4).
 *
 * # 🚫 There is no plural form, and there will not be one
 *
 * Design D4 predicts the request and refuses it in advance: *a bundle approval is one click that means
 * several things, and the person will read the first item and accept the rest.* It is the most
 * predictable and most dangerous convenience in this phase.
 *
 * So this route takes `proposal_id`, singular. The platform refuses `proposal_ids` outright — a
 * `DisallowUnknownFields` decoder rather than a filter — and this route never sends one. A client that
 * wants to approve three proposals makes three calls, and the person sees three cards.
 *
 * # `decision` is a discriminator, not two routes
 *
 * Approving and declining are the same act on the same resource with opposite values. Two endpoints
 * would be two places the hash-binding check could diverge, at which point the safe answer is whichever
 * one somebody remembered to write it in.
 *
 * # 🔴 What the response carries, and why it is the whole run
 *
 * Approving does more than record consent: it applies the change, re-measures it, and — when the
 * re-measurement disagrees — WITHDRAWS it before delivery. A response carrying only "approved" would
 * let this console render a green tick over a change that was withdrawn three lines later. The
 * sequence approved → applied → withdrawn is a sequence the UI has to be able to tell, or it reads as
 * a failure rather than as the system working.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    run_id?: string;
    proposal_id?: string;
    decision?: string;
  };

  const runId = (body.run_id ?? "").trim();
  const proposalId = (body.proposal_id ?? "").trim();
  const decision = (body.decision ?? "").trim();

  if (!runId || !proposalId) {
    return Response.json({ error: "name the run and the proposal being decided" }, { status: 400 });
  }
  // The closed set, checked here as well as in Go. A third value would reach the platform and be
  // refused — this makes a browser bug visible as a browser bug.
  if (decision !== "approve" && decision !== "decline") {
    return Response.json({ error: "a decision is approve or decline" }, { status: 400 });
  }

  return forward(context, context.paths.improvementDecision(), {
    method: "POST",
    body: { run_id: runId, proposal_id: proposalId, decision },
  });
}
