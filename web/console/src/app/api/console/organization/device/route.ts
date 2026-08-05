import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Approving (or denying) a terminal login, from the console's server side — P27 task 13.1.
 *
 * # 🔴 What crosses, and what cannot
 *
 * The user code the person typed, the organization they picked, and their decision. The APPROVER comes
 * from the scoped token this call presents — a credential the platform issued and verifies — so there is
 * no field here an identity could arrive in, exactly as there is none on `join`.
 *
 * That is what makes the platform's check meaningful: it verifies an ACTIVE membership for the person the
 * token names, in the organization the body names. A request that could assert its own approver would
 * make that check a formality.
 *
 * # Why the organization is in the body and the person is not
 *
 * A person may hold memberships in several organizations, so "which one is this terminal for" has no
 * server-side default that is not a guess — they pick. The platform then refuses a pick they hold no
 * active membership in, which is what stops somebody removed this morning from approving a terminal into
 * the organization they just left.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    user_code?: string;
    tenant_id?: string;
    approve?: boolean;
  };
  const userCode = (body.user_code ?? "").trim();
  if (!userCode) {
    return Response.json({ error: "enter the code shown in your terminal" }, { status: 400 });
  }
  return forward(context, "/api/v1/device/approve", {
    method: "POST",
    // 🔴 An OBJECT, not a string. `forward` takes `body: unknown` and serializes it itself, so
    // pre-stringifying here sends a JSON string where the platform expects a document — and the platform
    // answered exactly that: `cannot unmarshal string into Go value of type struct{...}`.
    //
    // Every type in the chain was satisfied: `unknown` accepts a string happily, and the error surfaced
    // only as text inside a 400 the browser rendered. Found by pressing Approve.
    body: {
      user_code: userCode,
      // Absent means "the organization this session is scoped to", which the platform resolves from the
      // token rather than defaulting to here. A default chosen in the browser is a default nobody checked.
      tenant_id: (body.tenant_id ?? "").trim() || undefined,
      // Explicit, never inferred from the field's absence: denying is a real outcome that ends somebody's
      // wait immediately, and it must not be reachable by omitting a key.
      approve: body.approve === true,
    },
  });
}
