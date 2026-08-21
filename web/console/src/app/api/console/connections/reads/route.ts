import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * The read LEDGER for one connection (P32 FR9).
 *
 * # Why this is a route and not part of the list
 *
 * FR9 requires the record be *"readable by the customer"*, and the list already carries the two facts
 * a reader scans for (last success, last failure). The full ledger is what somebody opens when they
 * have a QUESTION — "what did it read while I was on leave" — and loading every connection's ledger on
 * every page view to serve a question most readers never ask is a page that gets slower for everyone.
 *
 * `connection_id` is a SUBJECT, not an authority. The platform scopes it to the tenant the presented
 * token names and answers 404 for one that is not theirs — deliberately indistinguishable from "does
 * not exist", so the id cannot be used to probe which connections another tenant holds.
 */
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const id = (new URL(request.url).searchParams.get("connection_id") ?? "").trim();
  if (!id) {
    return Response.json({ error: "name the connection whose reads to show" }, { status: 400 });
  }
  return forward(context, `/api/v1/repo-connection-reads?connection_id=${encodeURIComponent(id)}`);
}
