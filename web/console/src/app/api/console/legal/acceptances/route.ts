import { NextResponse } from "next/server";
import { requireSession } from "@/lib/session";
import { platformFetch } from "@/lib/platformApi";
import { legalAcceptances } from "@/lib/legalPaths";

/**
 * The BFF leg for recording an acceptance (task 10.2).
 *
 * # Why the browser does not talk to the platform directly
 *
 * The same reason as every other console route: the platform credential is held server-side and never
 * reaches a browser chunk. This handler adds the credential and the tenant scope; the browser sends only
 * the three fields plus the method.
 *
 * # 🔴 It forwards the platform's status UNCHANGED
 *
 * A 409 (the document changed under the reader), a 503 (the manifest could not be read) and a 500 mean
 * different things to the person at the keyboard, and the gate renders different sentences for them.
 * Collapsing them into a generic 500 here would make "reload and read the current version" indistinguishable
 * from "try again later" — which is the difference between a reader who can act and one who cannot.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const session = await requireSession();

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "the request body must be JSON" }, { status: 400 });
  }

  const outcome = await platformFetch<unknown>(legalAcceptances(), {
    tenantId: session.tenantId,
    method: "POST",
    body,
  });

  if (!outcome.ok) {
    return NextResponse.json(
      {
        // The sentence the gate renders when it has nothing better. It states what did NOT happen.
        error: "the acceptance was not recorded; nothing has been agreed",
        reason: outcome.kind,
      },
      { status: outcome.status && outcome.status >= 400 ? outcome.status : 502 },
    );
  }
  return NextResponse.json(outcome.data, { status: 200 });
}
