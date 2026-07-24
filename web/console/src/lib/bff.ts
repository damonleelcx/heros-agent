import "server-only";
import { sessionFromRequest } from "./session";
import { scoped, type Scoped } from "./scope";
import { platformFetch, type PlatformOutcome } from "./platformApi";

/**
 * bff.ts is the shared body of every console data route (FR2, FR5, FR6).
 *
 * # Why the route set is closed rather than a catch-all proxy
 *
 * The obvious shape for a BFF is `/api/console/[...path]` forwarding whatever the browser asks for.
 * It is also an **open proxy holding a platform credential**: anything the platform serves becomes
 * reachable by a signed-in browser constructing a path, including routes the console never intended
 * to expose. So the routes are enumerated — one file per thing the client genuinely needs to fetch
 * *dynamically* — and the upstream path is built by `scope.ts` from the session. Everything else is
 * rendered on the server, where no proxy is involved at all.
 *
 * What "dynamically" means here is narrow, and worth stating so the set does not grow by habit: the
 * live monitor's stream and its polling fallback, the run poll behind the P2 watch toggle, and the
 * two P2 spec actions. Four things. A view that merely displays data renders it server-side.
 *
 * # How the taxonomy survives the second hop
 *
 * The browser is two hops from the platform, and each hop can flatten a failure. `respond` keeps the
 * three classes distinguishable: not-found stays 404, not-mounted stays 503, and a transport failure
 * becomes **502 with `kind: "transport"`** — a status the platform itself never returns on these
 * routes, so the client can always tell "the platform said no" from "we could not ask it".
 *
 * The upstream error body is carried through verbatim. `p35graph.html`'s copy — *"this is a transport
 * failure, not an empty classification"* — is the product; a generic message manufactured here would
 * destroy it before any component saw it.
 */

export type RouteContext = { session: NonNullable<ReturnType<typeof sessionFromRequest>>; paths: Scoped };

/**
 * withSession resolves the session for a route handler or refuses.
 *
 * The middleware has already refused a request with no cookie at all; this catches the cases it
 * structurally cannot see — expired and revoked — because only the server-side store knows those.
 */
export function withSession(request: Request): RouteContext | Response {
  const session = sessionFromRequest(request);
  if (!session) {
    return Response.json(
      { error: "your session has ended — sign in again", kind: "unauthenticated" },
      { status: 401, headers: { "cache-control": "no-store" } },
    );
  }
  return { session, paths: scoped(session) };
}

/** isResponse narrows `withSession`'s union at a call site without an `instanceof` at every route. */
export function isResponse(value: RouteContext | Response): value is Response {
  return value instanceof Response;
}

/**
 * respond turns a platform outcome into an HTTP response, preserving the taxonomy.
 *
 * Note what it never does: it never returns `200 []` for a 404. "There is no such run" and "this run
 * has no nodes" are different facts with different next actions, and collapsing them is the single
 * most common way a port destroys information the user needs.
 */
export function respond<T>(outcome: PlatformOutcome<T>): Response {
  const headers: Record<string, string> = { "cache-control": "no-store" };
  if (outcome.traceId) headers["x-trace-id"] = outcome.traceId;

  if (outcome.ok) {
    return Response.json(outcome.data, { status: 200, headers });
  }
  const status = outcome.kind === "transport" ? 502 : outcome.status;
  return Response.json({ error: outcome.error, kind: outcome.kind }, { status, headers });
}

/** forward is the whole body of a read route: scope, fetch, respond. Nothing in between. */
export async function forward<T>(context: RouteContext, path: string, init?: { method?: string; body?: unknown }) {
  const outcome = await platformFetch<T>(path, {
    tenantId: context.session.tenantId,
    method: init?.method,
    body: init?.body,
  });
  return respond(outcome);
}
