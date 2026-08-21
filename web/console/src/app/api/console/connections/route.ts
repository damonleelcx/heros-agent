import { withSession, isResponse, forward } from "@/lib/bff";

/**
 * Creating a repository connection, from the console's server side (P32 §6.2).
 *
 * # 🔴 The consent flag is FORWARDED, not asserted here
 *
 * FR10 says authorization cannot complete without the disclosure having been displayed. This route
 * carries the browser's answer; the PLATFORM refuses when it is false. That split is deliberate: a
 * check that lived only here would be a check a second client does not have, and the console is not
 * the only thing that can reach the platform.
 *
 * # 🚫 What this route can never do
 *
 * Read a credential back. The token crosses once, in this direction, and no console route returns it —
 * there is no field on any view type that could hold one.
 *
 * # Why the tenant is absent from the body
 *
 * It comes from the scoped token this call presents. A tenant a request can name is a tenant a request
 * can change, and this request creates a standing read grant.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const context = await withSession(request);
  if (isResponse(context)) return context;
  const body = (await request.json().catch(() => ({}))) as {
    workflow_id?: string;
    repository?: string;
    sub_path?: string;
    forge?: string;
    grant_kind?: string;
    external_id?: string;
    covers?: string[];
    account_wide?: boolean;
    scopes?: string[];
    token?: string;
    consent_shown?: boolean;
  };

  const repository = (body.repository ?? "").trim();
  if (!repository.includes("/")) {
    return Response.json(
      { error: "name the repository as owner/name — a connection covers exactly one" },
      { status: 400 },
    );
  }
  if (body.consent_shown !== true) {
    // Refused here TOO, and the duplication is the point: the platform's refusal is the one that makes
    // the requirement true of the system, and this one makes the browser's own bug visible as a
    // browser bug rather than as a platform error.
    return Response.json(
      { error: "the disclosure has not been displayed — authorization cannot complete" },
      { status: 400 },
    );
  }

  return forward(context, "/api/v1/repo-connections", {
    method: "POST",
    body: {
      workflow_id: (body.workflow_id ?? "").trim(),
      repository,
      // Absent rather than empty: an empty sub-path and no sub-path are the same thing (the whole
      // repository), and sending "" would store a value that means nothing.
      sub_path: (body.sub_path ?? "").trim() || undefined,
      forge: (body.forge ?? "").trim(),
      grant_kind: (body.grant_kind ?? "").trim(),
      external_id: (body.external_id ?? "").trim() || undefined,
      // 🔴 What the FORGE says the grant covers, forwarded unmodified. The platform's breadth refusal
      // is the comparison between this and `repository`, so a console that "helpfully" normalised it
      // to the requested repository would defeat the one check ADR-013 Option B rests on.
      covers: Array.isArray(body.covers) ? body.covers : [],
      account_wide: body.account_wide === true,
      scopes: Array.isArray(body.scopes) ? body.scopes : undefined,
      token: body.token ?? "",
      consent_shown: true,
    },
  });
}
