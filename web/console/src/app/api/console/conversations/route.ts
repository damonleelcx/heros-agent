import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Open a conversation (P31 task 2.1).
 *
 * A pass-through. The workflow id travels in the body and the SCOPE does not: the platform derives the
 * tenant and the person from the credential this call presents, so there is nothing here for a client
 * to widen. A cross-tenant workflow and a nonexistent one both come back as the same 404, and this
 * route forwards that unchanged — normalising it would be the one place the console could accidentally
 * reintroduce the enumeration oracle the platform removed.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body: unknown = await request.json().catch(() => null);
  return forward(context, context.paths.createConversation(), { method: "POST", body });
}
