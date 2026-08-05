import { NextResponse } from "next/server";
import { requireSession } from "@/lib/session";
import { platformFetch } from "@/lib/platformApi";

/**
 * Creating an organization, from the console's server side.
 *
 * # 🔴 The identity is the SESSION's, never the request body's
 *
 * The browser sends one field: a name. The issuer, the subject and the email come from the session this
 * console issued after it verified an assertion — which is the only reason the platform is willing to
 * take them at all. A route that forwarded a client-supplied identity would be a signup form anybody
 * could use to create an organization owned by somebody else.
 *
 * The platform's refusal codes are forwarded UNCHANGED: `self_serve_disabled` is a deployment posture
 * with a next action ("ask whoever runs this install"), and flattening it into a 500 would replace the
 * one useful thing the server said.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const session = await requireSession();
  let body: { name?: string } = {};
  try {
    body = (await request.json()) as typeof body;
  } catch {
    return NextResponse.json({ error: "the request body must be JSON" }, { status: 400 });
  }

  const outcome = await platformFetch<unknown>("/api/v1/organizations", {
    tenantId: session.tenantId,
    method: "POST",
    body: {
      name: body.name ?? "",
      // From the session. There is no path by which the browser could set these.
      issuer: session.tenantId,
      subject: session.userId ?? session.id,
      email: "",
    },
  });
  if (!outcome.ok) {
    return NextResponse.json(
      { error: outcome.error, reason_code: outcome.reasonCode ?? "", kind: outcome.kind },
      { status: outcome.status && outcome.status >= 400 ? outcome.status : 502 },
    );
  }
  return NextResponse.json(outcome.data, { status: 201 });
}
