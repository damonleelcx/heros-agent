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

/**
 * SIGNIN_FLOW_COOKIE carries the id of an in-flight federated sign-in (P22 task 3.1).
 *
 * # Why it is here rather than next to the flow store that uses it
 *
 * It was written next to the flow store first, and `tests/security.test.mjs` failed — correctly. The
 * invariant that file enforces is that **credential cookie flags are set in exactly one place**, so
 * there is one thing to audit and one thing to get wrong. A second `httpOnly: true` three directories
 * away is precisely the drift that invariant exists to catch, and the honest fix is to obey it rather
 * than to widen the assertion.
 *
 * # What it holds, and what it deliberately does not
 *
 * An opaque flow id. Not the PKCE verifier, not the nonce, not the `state` — those stay server-side in
 * `lib/idp/flow.ts`, keyed by this id, for the same reason the session record stays server-side while
 * the browser holds only a token. What the browser has is a handle; what it can do with the handle is
 * exactly nothing except present it back.
 *
 * The flow id in this cookie is the BROWSER half of the CSRF defense. The `state` in the callback URL
 * is the other half, and neither alone completes a sign-in.
 */
export const SIGNIN_FLOW_COOKIE = "heros_console_signin";

/**
 * SIGNIN_FLOW_COOKIE_OPTIONS is the flow cookie's flags.
 *
 * `sameSite: "lax"` for the same reason the session cookie uses it, and the reason is load-bearing
 * here rather than incidental: the IdP returns the browser to `/auth/callback` as a **cross-site
 * top-level GET navigation**, and `strict` means exactly "do not send this cookie on one". A `strict`
 * flow cookie produces a sign-in that fails its own CSRF check every single time — the identity
 * version of the payment defect recorded above.
 *
 * `path: "/auth"` because the cookie is meaningless anywhere else; a credential cookie scoped to where
 * it is used is one fewer thing riding on every request in the product.
 *
 * No `maxAge` here, matching `SESSION_COOKIE_OPTIONS`: the lifetime is a bound owned by the module
 * that owns the record (`MAX_FLOW_AGE_SECONDS`), and the route applies it — so the bound has one
 * definition rather than a copy in a file that cannot import it.
 */
export const SIGNIN_FLOW_COOKIE_OPTIONS = {
  httpOnly: true,
  sameSite: "lax",
  secure: process.env.NODE_ENV === "production",
  path: "/auth",
} as const;
