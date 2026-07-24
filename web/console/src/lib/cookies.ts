/**
 * cookies.ts holds the cookie NAMES and FLAGS, and deliberately nothing else.
 *
 * # Why this is a separate module from session.ts
 *
 * `middleware.ts` runs in the edge runtime and needs the cookie name to fail closed by route prefix.
 * `session.ts` reads `node:crypto` to mint session tokens, which the edge runtime cannot load — so a
 * middleware that imported the session module would fail the build. Splitting the constants out is
 * the honest fix: the edge check needs a NAME, not a session store.
 *
 * It also keeps a useful property: this file has no imports at all, so nothing can arrive in the edge
 * bundle by following an import from here.
 */

/**
 * SESSION_COOKIE is the customer session cookie.
 *
 * Named so it could not be mistaken for the operator console's `heros_admin_session`. The two
 * consoles are on different origins with different cookie jars, so confusion is already structurally
 * impossible — this is belt and braces, and it makes a log line or a devtools panel unambiguous about
 * which surface a session belongs to.
 */
export const SESSION_COOKIE = "heros_console_session";

/**
 * SESSION_COOKIE_OPTIONS is the only place the cookie's flags are set.
 *
 * `httpOnly` so page script cannot read it — the property the whole credential boundary rests on.
 * `sameSite: strict` so it is not sent on a cross-site navigation. `secure` in production only,
 * because a local development console is served over plain HTTP and a `Secure` cookie would simply
 * never be set — a sign-in that silently does nothing is a worse failure than the one it prevents on
 * a loopback address.
 */
export const SESSION_COOKIE_OPTIONS = {
  httpOnly: true,
  sameSite: "strict",
  secure: process.env.NODE_ENV === "production",
  path: "/",
} as const;
