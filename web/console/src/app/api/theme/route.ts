import type { NextRequest } from "next/server";
import { redirectTo, samePath } from "@/lib/redirect";
import { isTheme, THEME_COOKIE, THEME_COOKIE_OPTIONS } from "@/lib/theme";

/**
 * POST /api/theme — persist the reader's theme choice (R17 / FR37, task 5b.5).
 *
 * # Why this is a form POST and not a client toggle
 *
 * FR37 requires the first paint to already be in the chosen theme, and the console's CSP has no
 * `unsafe-inline`, so the usual blocking inline script in `<head>` is not available and should not be
 * bought with a CSP relaxation. A round trip that sets a cookie and re-renders is correct on the first
 * byte, needs no script at all, and keeps working with JavaScript disabled.
 *
 * The cost is a navigation per theme change. That is an acceptable price for a control a reader
 * touches roughly once.
 *
 * # Why it redirects to where the reader was, and why not via `Referer`
 *
 * The form carries a `back` field holding the current path, put there by `ThemeControl` from the
 * `x-pathname` header the middleware sets.
 *
 * The obvious implementation reads `Referer` — and it is wrong here, which a browser demonstrated:
 * this console sends `Referrer-Policy: no-referrer`, so `Referer` is never present, and every theme
 * change silently returned the reader to `/` instead of the page they were reading. The endpoint
 * responded 303 with a correct `Set-Cookie` the whole time.
 *
 * `samePath` still runs over the value, because a field in a form is client-supplied no matter where
 * the server originally put it. The redirect is relative for the reason `redirect.ts` documents at
 * length: an absolute `Location` reconstructed from `request.url` is refused by this console's own
 * `form-action 'self'` policy the moment the host the server believes in differs from the one in the
 * address bar.
 */
export async function POST(request: NextRequest): Promise<Response> {
  const form = await request.formData();
  const choice = form.get("theme");
  const requested = form.get("back");

  // An unknown value is ignored rather than stored. A cookie is a thing every later request reads;
  // writing an unvalidated string into one is how a display preference becomes an injection surface.
  const back = samePath(typeof requested === "string" ? requested : null, "/");
  if (typeof choice !== "string" || !isTheme(choice)) {
    return redirectTo(back);
  }

  const response = redirectTo(back);
  response.cookies.set(THEME_COOKIE, choice, THEME_COOKIE_OPTIONS);
  return response;
}
