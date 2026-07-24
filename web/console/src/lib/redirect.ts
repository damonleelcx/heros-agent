import { NextResponse } from "next/server";

/**
 * redirect.ts issues SAME-ORIGIN redirects, structurally.
 *
 * # The defect this exists for, found by clicking a button in a real browser
 *
 * The obvious way to redirect from a route handler is
 * `NextResponse.redirect(new URL(next, request.url))`. It is what the documentation shows, it type
 * checks, it returns a correct-looking `303` with a correct-looking `Set-Cookie`, and every test
 * against it passes.
 *
 * In a browser it silently fails. `request.url` inside Next is normalised to the server's own notion
 * of its host — `http://localhost:4320` — while the person is on `http://127.0.0.1:4320`. Those are
 * **different origins**, so the console's own `form-action 'self'` policy refuses the navigation and
 * Chrome aborts the request. The user clicks Continue and nothing happens. No error, no log line, no
 * failing check: a signed-in session was issued on the server and the browser was never allowed to go
 * anywhere with it.
 *
 * Anything that reconstructs the host — from `request.url`, from `Host`, from a configured base — is
 * one deployment detail away from the same bug (a proxy, a container hostname, an IPv6 literal).
 *
 * # The fix
 *
 * A **relative** `Location`. RFC 7231 permits it and every browser resolves it against the current
 * URL, so the response is same-origin by construction rather than by arithmetic. As a side effect the
 * open-redirect surface disappears entirely: there is no origin to get wrong, because there is no
 * origin.
 */

/**
 * samePath rejects anything that is not a same-origin absolute path.
 *
 * A leading `//` is a protocol-relative URL to another host, which a bare `startsWith("/")` accepts —
 * so it is refused explicitly. Belt and braces on top of the relative `Location`: a caller should not
 * be able to express an off-origin destination even in principle.
 */
export function samePath(path: string | null | undefined, fallback = "/app"): string {
  if (!path || !path.startsWith("/") || path.startsWith("//")) return fallback;
  return path;
}

/**
 * redirectTo returns a redirect to a path on this origin.
 *
 * 303 by default: after a POST, a 303 tells the browser to follow with a GET, which is what a form
 * submission should produce. A 302 leaves the method up to the browser, and a 307 would repeat the
 * POST against the destination.
 */
export function redirectTo(path: string, status: 303 | 307 | 308 = 303): NextResponse {
  return new NextResponse(null, {
    status,
    headers: { location: samePath(path), "cache-control": "no-store" },
  });
}
