import { readSessionToken, revokeSession } from "@/lib/session";
import { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "@/lib/cookies";
import { redirectTo } from "@/lib/redirect";

/**
 * Sign-out as a form POST, so the shell's control works without client JavaScript.
 *
 * # Why sign-out is not a link
 *
 * A GET that ends a session can be triggered by a browser prefetch, an `<img>` tag on a page the user
 * happens to visit, a link checker, or an email scanner. Any of those signs the user out for reasons
 * they will never work out. The state change goes behind a POST, which none of those issue.
 *
 * The `DELETE` verb on `/api/session` remains, for callers that can send it. This route exists so the
 * HTML form path is equally available — the same reasoning as the sign-in form, on the other end of
 * the session's life.
 */
export const dynamic = "force-dynamic";

export async function POST() {
  // Revoke server-side FIRST, then clear the cookie. Clearing alone would leave a live session anybody
  // holding the token could still use; revoking first means the token is dead before the browser is
  // told to forget it.
  const token = await readSessionToken();
  revokeSession(token);
  // Set on the RESPONSE, and RELATIVE. See the note in ../route.ts: an immutable redirect cannot carry
  // a `Set-Cookie`, and an absolute one built from `request.url` can leave this origin.
  const response = redirectTo("/signin?reason=session_ended");
  response.cookies.set(SESSION_COOKIE, "", { ...SESSION_COOKIE_OPTIONS, maxAge: 0 });
  return response;
}
