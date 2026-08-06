import { NextResponse } from "next/server";
import { requireSession } from "@/lib/session";
import { resendConfirmation } from "@/lib/idp/password";

/**
 * Asking for another confirmation link, from the console's server side.
 *
 * # Why this requires a session even though the platform's endpoint does not
 *
 * The platform's `/resend` is public and answers identically for every address, because it has to serve
 * somebody who is not signed in. This route is the console's own, reached only from the account page, so
 * requiring a session costs nothing and removes one unauthenticated surface from this origin.
 *
 * # 🔴 It reports success regardless
 *
 * Matching the platform, which gives one answer. A console that rendered two would be claiming to know
 * something it was not told — and the address in the body is the reader's own, so there is nothing here
 * for a second answer to be useful about.
 */
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  await requireSession();
  const body = (await request.json().catch(() => ({}))) as { email?: string };
  await resendConfirmation(body.email ?? "");
  return NextResponse.json({ ok: true }, { headers: { "cache-control": "no-store" } });
}
