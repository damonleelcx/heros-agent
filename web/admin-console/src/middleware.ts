import { NextResponse, type NextRequest } from "next/server";
import { buildContentSecurityPolicy } from "../../design-system/csp.ts";


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
 *
 * # Why the header is built from data (P24)
 *
 * Every route of this console is an OPERATOR route, and P24 installs three third-party products into a
 * system that had committed to running none. The operator console's answer is the narrowest one
 * available: it takes the error reporter and refuses both the analytics tag and the session recorder,
 * structurally — its screen renders cross-tenant aggregates, tenant names, active impersonation state
 * and audit rows, and a recording of that is a copy of the material this platform exists to keep
 * inside a boundary.
 *
 * That refusal is not a line in this file. It is `SURFACE_CLASS_RULES.operator` in
 * `web/design-system/third-party-policy.ts`, which both consoles read, and which does not permit those
 * categories on this class at all. A `https://` literal in this file fails the build, so the refusal
 * cannot be undone here — only in the table, where the diff says what it is doing.
 */
const DEV = process.env.NODE_ENV !== "production";

export function middleware(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString("base64");
  const csp = buildContentSecurityPolicy({
    consoleId: "operator",
    pathname: request.nextUrl.pathname,
    nonce,
    dev: DEV,
    /*
     * 🔴 `error_diagnostics` is granted here BY POLICY, not by a banner, and the difference is stated
     * rather than left to be inferred from a missing control.
     *
     * This is a staff console governed by the internal acceptable-use notice. The person in front of it
     * is an employee acting in that capacity, and asking them to consent to their employer's error
     * diagnostics would be consent theatre — a banner on an incident console is a control an operator
     * learns to dismiss without reading, which is worse than none.
     *
     * The exception is one category wide and it stays that way structurally: `product_analytics` and
     * `session_replay` are ABSENT from the operator class's permitted categories in
     * `third-party-policy.ts`, so listing them here would change nothing. Try it and the header is the
     * same.
     */
    granted: ["essential", "error_diagnostics"],
  });

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-nonce", nonce);
  requestHeaders.set("content-security-policy", csp);
  // `x-pathname` is the current path, made readable by server components (P24 task 3.3). The layout
  // resolves it to an id from the CLOSED SURFACE ENUM and passes that to the browser reporter, so the
  // browser never derives a surface from `location.href`. The path is read here rather than
  // reconstructed from `request.url` for the same reason the customer console reads it here: the value
  // the server can compute and the value in the address bar are not reliably the same one.
  requestHeaders.set("x-pathname", request.nextUrl.pathname);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set("content-security-policy", csp);
  return response;
}

export const config = {
  // Run on every route except Next's static assets, which are hashed and served with their own
  // long-cache headers.
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
