import { NextResponse, type NextRequest } from "next/server";
import { SESSION_COOKIE } from "@/lib/cookies";

/**
 * middleware.ts does two jobs, and both are prefix-level on purpose.
 *
 * # 1 · Fail closed by ROUTE PREFIX (FR2)
 *
 * Everything under `/app` renders tenant data and everything under `/api/console` returns it. A
 * request to either without a session cookie is redirected to sign-in (pages) or refused (data
 * routes) HERE, before any page code runs.
 *
 * Each page also calls `requireSession()`, and that is not redundant. This check can only see whether
 * a cookie is PRESENT — middleware runs on the edge runtime and has no access to the server-side
 * session store — so it catches the "never signed in" case cheaply and cannot catch an expired or
 * revoked session. `requireSession()` catches those, by reading the store on every request, which is
 * what makes revocation effective at the next request with no grace period. Two checks, two different
 * questions; neither is the other's backup.
 *
 * The prefix rule is the load-bearing half: a fail-closed rule enforced page by page fails the first
 * time somebody adds a page.
 *
 * # 2 · A per-request, nonce-based Content-Security-Policy
 *
 * Next's App Router streams a small inline bootstrap to hydrate the page. Relaxing to
 * `'unsafe-inline'` would allow ANY injected inline script, defeating the point — so each response
 * carries a fresh nonce and only scripts stamped with it execute. `'strict-dynamic'` lets the nonced
 * bootstrap load the app's own chunks without listing each one.
 *
 * `default-src 'self'` is also what makes the public home page's no-third-party-origin rule (FR35)
 * enforceable rather than aspirational: a hosted font or an analytics tag does not render, it is
 * REFUSED, and the refusal is visible in the console's own error log.
 *
 * Development adds `'unsafe-eval'` only, because Next's React Refresh evaluates a string to install
 * the hot-reload runtime. Under the production CSP that throws, the bootstrap dies before hydration,
 * and the console renders as static HTML — every control inert, `next build` green, and only a real
 * browser interaction reveals it. Since this console's acceptance rule IS a real browser (R11), that
 * failure mode would make acceptance unrunnable. The shipped CSP is unchanged, and the asymmetry is
 * asserted by a test rather than left to this comment.
 */
const DEV = process.env.NODE_ENV !== "production";

/** GATED are the prefixes that require a session. Everything else is public by construction. */
const GATED = ["/app", "/api/console", "/api/stream"];

function isGated(pathname: string): boolean {
  return GATED.some((prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`));
}

export function middleware(request: NextRequest) {
  const { pathname, search } = request.nextUrl;

  if (isGated(pathname) && !request.cookies.get(SESSION_COOKIE)) {
    if (pathname.startsWith("/api/")) {
      // A data route refuses rather than redirecting: a fetch that follows a redirect to an HTML
      // sign-in page and then fails to parse it produces a mystifying error at the call site, where
      // the honest answer — "you are not signed in" — is available right here.
      return NextResponse.json(
        { error: "no session", reason: "no_session" },
        { status: 401, headers: { "cache-control": "no-store" } },
      );
    }
    const signin = new URL("/signin", request.url);
    signin.searchParams.set("reason", "no_session");
    // `next` preserves where they were going, so signing in lands on the subject they asked for
    // rather than on a generic home — an identifier the system already has must not be re-typed.
    signin.searchParams.set("next", pathname + search);
    return NextResponse.redirect(signin);
  }

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

  /*
   * `x-pathname` is the current path, made readable by server components.
   *
   * It exists because of a defect found by clicking the theme control in a real browser: the theme
   * form posted, the cookie was set, and the reader was returned to `/` instead of the page they were
   * on. The route handler had been redirecting to `Referer` — and this console sets
   * `Referrer-Policy: no-referrer` a few lines above, so that header is never sent. Every server-side
   * check passed; the behaviour was only wrong on screen.
   *
   * The path travels as a request header rather than being reconstructed from `request.url`, for the
   * same reason `lib/redirect.ts` refuses to reconstruct an origin: the value the server can compute
   * and the value in the address bar are not reliably the same one.
   */
  requestHeaders.set("x-pathname", pathname + search);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set("content-security-policy", csp);
  return response;
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
