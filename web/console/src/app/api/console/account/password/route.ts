import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Changing your own password, from the console's server side.
 *
 * # 🔴 There is no field here that says WHOSE password
 *
 * The person comes from the SCOPED TOKEN this call presents — a credential the platform issued and
 * verifies — exactly as it does on `join` and `device`. A route that forwarded a client-supplied user id
 * would be a password-change form anybody could point at somebody else's account, and the current-password
 * check would be a formality rather than a proof.
 *
 * # What crosses
 *
 * The two passwords, once, in a POST body, over the console's own origin. They are forwarded and dropped:
 * not stored, not logged, not put in a URL, not carried into any second call. There is no code path here
 * that retains either, which is why the whole handler is six lines.
 *
 * The platform's refusals are forwarded unchanged. `bad_credentials` on this route means "that is not your
 * current password" — a different sentence from the sign-in refusal, and the platform is the one that
 * makes the distinction, because it is the only party that knows which check failed.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    current_password?: string;
    new_password?: string;
  };
  if (!body.current_password || !body.new_password) {
    return Response.json({ error: "enter your current password and a new one" }, { status: 400 });
  }
  return forward(context, "/api/v1/auth/password/change", {
    method: "POST",
    body: { current_password: body.current_password, new_password: body.new_password },
  });
}
