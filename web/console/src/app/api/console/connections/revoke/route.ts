import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Revoking a repository connection (P32 §6.3).
 *
 * # Why this is its own route rather than a DELETE on the one above
 *
 * The platform's revocation is a THREE-PART cascade — the derived trees, the credential, then the
 * grant — and it returns a receipt saying how many trees were deleted. A `DELETE` returning a body is
 * unusual enough that clients drop it, and the number is what makes the console's confirmation a
 * receipt instead of a repeated claim.
 *
 * # 🔴 A failure here is NOT "nothing happened"
 *
 * A partially-completed cascade leaves the connection unable to read anything and is retryable. The
 * platform's sentence says so; this route forwards it verbatim rather than replacing it with
 * "revocation failed", because a customer who asked us to stop holding their source must be able to
 * tell how far it got.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as { connection_id?: string };
  const id = (body.connection_id ?? "").trim();
  if (!id) {
    return Response.json({ error: "name the connection to revoke" }, { status: 400 });
  }
  return forward(context, "/api/v1/repo-connection-revocations", {
    method: "POST",
    body: { connection_id: id },
  });
}
