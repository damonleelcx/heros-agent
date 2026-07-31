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
 * `secure` in production only, because a local development console is served over plain HTTP and a
 * `Secure` cookie would simply never be set — a sign-in that silently does nothing is a worse failure
 * than the one it prevents on a loopback address.
 *
 * # Why `lax` and not `strict`
 *
 * 🔴 This was `strict`, and `strict` broke payment outright: a customer who completed Stripe Checkout
 * came back to a SIGN-IN PAGE. Their card had been charged and their subscription created, and the
 * product's response was to ask them who they were.
 *
 * The return from `checkout.stripe.com` is a cross-site top-level navigation, and `strict` means
 * exactly "do not send this cookie on one". So the browser was behaving correctly and so was the
 * console — the flag was simply incompatible with the architecture around it. P21's whole collection
 * design (Decision D2) REQUIRES leaving this origin, because the card must be entered on Stripe's own
 * page and never on ours. A cookie policy that assumes the user never leaves cannot be paired with a
 * payment flow whose first step is to send them away.
 *
 * `lax` gives up the narrowest possible thing: the cookie now rides along on cross-site top-level
 * navigations that use a SAFE method (a GET redirect back from the payment provider). It is still
 * withheld from cross-site POST, from iframes, and from subresource loads — which is where CSRF
 * actually lives, and which is why no mutating route in this app is a GET. That invariant is not left
 * to memory: `tests/session-cookie.test.mjs` fails if a GET handler ever appears in an API route.
 */
export const SESSION_COOKIE_OPTIONS = {
  httpOnly: true,
  sameSite: "lax",
  secure: process.env.NODE_ENV === "production",
  path: "/",
} as const;
