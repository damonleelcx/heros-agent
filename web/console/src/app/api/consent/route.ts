import type { NextRequest } from "next/server";
import { redirectTo, samePath } from "@/lib/redirect";
import { CONSENT_COOKIE, CONSENT_COOKIE_OPTIONS, serialize, stateFromForm } from "@/lib/consentPrefs";

/**
 * POST /api/consent — record the visitor's analytics-consent decision (P24 tasks 4.1, 4.2, 4.5, 4.6).
 *
 * # Why a form POST and not a client toggle
 *
 * The same argument `POST /api/theme` makes, and one more that is specific to consent.
 *
 * The shared part: this console's policy has no `unsafe-inline`, so the usual client-side consent
 * widget would either need a CSP relaxation or a hydration round trip — and the header this decision
 * CHANGES is computed in middleware, per request, from this very cookie. A choice that only exists in
 * the browser could not narrow the policy that governs the browser.
 *
 * The specific part: a consent control that needs JavaScript is a consent control that fails open for
 * anybody whose JavaScript did not run. With a form, declining works with scripting disabled, on a
 * failed hydration, and behind a proxy that stripped the bundle — which are exactly the conditions
 * under which a visitor is most likely to be somebody who cares.
 *
 * # 🔴 What can move a decision, and what cannot
 *
 * Only an explicit submission of this form, or a MATERIAL policy version (handled at decode time, in
 * `consent.ts`). There is no GET handler, no query parameter that grants, and no timer: a decision
 * cannot be moved by a navigation, by scrolling, or by waiting. That is asserted rather than described
 * — `tests/consent.test.mjs` walks three navigations and a new session after a refusal.
 *
 * # Why the redirect is relative and validated
 *
 * `back` is client-supplied no matter where the server originally put it, so `samePath` runs over it;
 * and the redirect is relative because an absolute `Location` reconstructed from `request.url` is
 * refused by this console's own `form-action 'self'` the moment the host the server believes in differs
 * from the one in the address bar. Both are `redirect.ts`'s existing rules, reused rather than
 * re-derived.
 */
export async function POST(request: NextRequest): Promise<Response> {
  const form = await request.formData();
  const action = form.get("action");
  const requested = form.get("back");
  const back = samePath(typeof requested === "string" ? requested : null, "/");

  const state = stateFromForm(typeof action === "string" ? action : "", form);
  if (!state) {
    // An unrecognised action stores nothing and changes nothing. It is not an error the visitor needs
    // to see: they are returned to the page they were on, with the decision they already had.
    return redirectTo(back);
  }

  const response = redirectTo(back);
  response.cookies.set(CONSENT_COOKIE, serialize(state), CONSENT_COOKIE_OPTIONS);
  return response;
}
