import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Accepting an invitation, from the console's server side.
 *
 * # 🔴 It carries no identity, and that is the point
 *
 * The acting person comes from the SCOPED TOKEN this call presents — a credential the platform issued
 * and verifies — not from anything in the body. The invitation's address is matched against the address
 * recorded on that person at sign-in, which came from a verified assertion.
 *
 * So there is no field here an address could arrive in. "The address in the request is never the address
 * that matters" is not a rule anybody has to remember; it is the shape of the request.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as { invitation_id?: string };
  const id = (body.invitation_id ?? "").trim();
  if (!id) {
    return Response.json({ error: "an acceptance needs an invitation" }, { status: 400 });
  }
  return forward(context, `${context.paths.invitation(id)}/accept`, { method: "POST" });
}
