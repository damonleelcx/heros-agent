import { NextResponse } from "next/server";
import { readSessionToken } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { loadOperatingPicture } from "@/lib/overview";
import type { AdminIdentity } from "@/lib/types";

/**
 * /api/overview is the BFF endpoint the operating picture refreshes against (FR34).
 *
 * # Why a route handler rather than a client fetch to the platform
 *
 * The same reason the whole console is a BFF: the platform credential is server-side only (FR20). The
 * browser polls THIS origin with its `HttpOnly` admin session; the credential never moves.
 *
 * # Why it resolves the identity on every poll
 *
 * Capabilities are read live, so a revoked role stops returning panels at the NEXT poll rather than at
 * the next login — the same no-grace rule the rest of the console follows (FR2). A poll with no valid
 * session returns 401 and the client stops polling and marks the picture stale; it does not silently
 * keep showing figures nobody is authorised for any more.
 */
export const dynamic = "force-dynamic";

export async function GET() {
  const sessionToken = await readSessionToken();
  if (!sessionToken) {
    return NextResponse.json({ error: "no session" }, { status: 401 });
  }
  try {
    const identity = await adminFetch<AdminIdentity>("/admin/api/me", { sessionToken });
    const picture = await loadOperatingPicture(sessionToken, identity.capabilities);
    return NextResponse.json(picture, {
      // A cached operating picture is a stale operating picture presented as current — the exact
      // failure FR34 exists to prevent.
      headers: { "cache-control": "no-store" },
    });
  } catch (error) {
    const status = error instanceof AdminApiError && error.kind === "auth" ? 401 : 503;
    return NextResponse.json(
      { error: error instanceof Error ? error.message : String(error) },
      { status },
    );
  }
}
