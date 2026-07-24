import { NextResponse, type NextRequest } from "next/server";

/**
 * middleware.ts sets a per-request, nonce-based Content-Security-Policy.
 *
 * # Why a nonce rather than 'unsafe-inline'
 *
 * Next.js App Router streams a small inline bootstrap script to hydrate the page. The console's CSP
 * must still be strict — an operator surface is exactly where a cross-site-scripting payload must have
 * nowhere to run — so instead of relaxing to `'unsafe-inline'` (which would allow ANY injected inline
 * script, defeating the point), each response carries a fresh random nonce and only scripts stamped
 * with that nonce execute. Next reads the nonce from the CSP header and applies it to its own bootstrap
 * automatically, so a strict CSP and a working page are not in tension.
 *
 * `'strict-dynamic'` lets the nonced bootstrap load the app's own chunks without listing each one,
 * while still refusing any script that was not reached from a trusted, nonced root.
 *
 * # Why development carries one extra source, and production does not
 *
 * Next's dev server ships React Refresh, which evaluates a string to install the hot-reload runtime.
 * Under the production CSP that throws, the bootstrap dies before hydration, and the console renders
 * as static HTML: every form is inert and every button does nothing. That failure mode is worse than
 * it sounds on this surface, because it is invisible — the page looks correct, `next build` passes,
 * and only a real browser interaction reveals that nothing is wired. The acceptance rule this console
 * is held to (rendered-browser evidence, FR37/12.15) is unrunnable in that state.
 *
 * So `'unsafe-eval'` is added ONLY when NODE_ENV is not production. The shipped bundle's CSP is
 * unchanged, and the asymmetry is asserted by a test rather than left to this comment.
 */
const DEV = process.env.NODE_ENV !== "production";

export function middleware(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString("base64");
  const scriptSrc = [`'self'`, `'nonce-${nonce}'`, `'strict-dynamic'`, DEV ? `'unsafe-eval'` : ""]
    .filter(Boolean)
    .join(" ");
  const csp = [
    `default-src 'self'`,
    `script-src ${scriptSrc}`,
    `style-src 'self' 'unsafe-inline'`,
    `img-src 'self' data:`,
    `connect-src 'self'`,
    `font-src 'self'`,
    `object-src 'none'`,
    `base-uri 'self'`,
    `form-action 'self'`,
    `frame-ancestors 'none'`,
  ].join("; ");

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-nonce", nonce);
  requestHeaders.set("content-security-policy", csp);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set("content-security-policy", csp);
  return response;
}

export const config = {
  // Run on every route except Next's static assets, which are hashed and served with their own
  // long-cache headers.
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
