import { cookies } from "next/headers";
import {
  CONSENT_COOKIE,
  CONSENT_POLICY_VERSION,
  DEFAULT_STATE,
  decode,
  encode,
  hasAnswered,
  isGranted,
  type ConsentState,
  type Decision,
} from "../../../design-system/consent.ts";
import { NON_ESSENTIAL_CATEGORIES, type ConsentCategory } from "../../../design-system/third-party-policy.ts";

/**
 * consentPrefs.ts is the console's server-side view of the visitor's analytics consent.
 *
 * # 🔴 This is NOT `consentGate.ts`, and the two must never be merged
 *
 * `src/lib/consentGate.ts` is P23's LEGAL ACCEPTANCE gate: which statutory documents a signed-in
 * principal owes, recorded in an append-only ledger keyed to an immutable document hash, surviving
 * identity erasure because a record of what somebody agreed to is evidence.
 *
 * This is the opposite in every respect — a revocable, per-browser, per-visitor preference with no
 * principal and no tenant, which somebody may change twice in a minute and which must disappear when
 * they clear their browser. Putting an analytics preference in that ledger would mean a cookie choice
 * outliving a deletion request, which is the one outcome a consent mechanism must never produce.
 *
 * The names are close enough to be confused, so both files say so.
 *
 * # Why the cookie is not `httpOnly`
 *
 * Exactly the argument `theme.ts` makes about the theme cookie, and it is asserted by
 * `tests/security.test.mjs`: the session cookie is `httpOnly` because it is a CREDENTIAL, and the flags
 * that claim credential protection live in one file. This carries no identity and authorises nothing.
 * Marking it `httpOnly` would imply it is sensitive, and a flag that means nothing stops meaning
 * anything.
 */

export { CONSENT_COOKIE, CONSENT_POLICY_VERSION, hasAnswered, isGranted };
export type { ConsentState, Decision };

/**
 * CONSENT_COOKIE_OPTIONS — deliberately NOT `httpOnly`, and `lax` for the same reason the theme is.
 *
 * A one-year lifetime rather than a session one: a visitor who declined must not be asked again next
 * week, and "a refusal is stored as a refusal" is worth nothing if the refusal expires when the tab
 * closes.
 */
export const CONSENT_COOKIE_OPTIONS = {
  httpOnly: false,
  sameSite: "lax",
  secure: process.env.NODE_ENV === "production",
  path: "/",
  maxAge: 60 * 60 * 24 * 365,
} as const;

/** readConsent returns the visitor's state, defaulting to nothing-granted. */
export async function readConsent(): Promise<ConsentState> {
  const store = await cookies();
  return decode(store.get(CONSENT_COOKIE)?.value);
}

/**
 * stateFromForm builds a state from the banner's submitted fields.
 *
 * Three shapes of submission, and every one of them is an EXPLICIT USER ACTION — which is the whole of
 * what may move a decision. Nothing here can be reached by a navigation, a timer or a scroll.
 *
 *   accept-all   every optional category granted
 *   decline-all  every optional category DENIED — stored as a denial, not left un-asked
 *   save         each category takes the value of its own checkbox; an absent checkbox is a DENIAL
 *
 * The third is the one that has to be got right. An unchecked box submits nothing at all, so "absent"
 * has to mean denied — reading it as "unchanged" would let a visitor un-tick a box, press save, and be
 * told nothing changed.
 */
export function stateFromForm(action: string, form: FormData): ConsentState | null {
  const decisions = { ...DEFAULT_STATE.decisions };
  if (action === "accept-all") {
    for (const category of NON_ESSENTIAL_CATEGORIES) decisions[category] = "granted";
  } else if (action === "decline-all") {
    for (const category of NON_ESSENTIAL_CATEGORIES) decisions[category] = "denied";
  } else if (action === "save") {
    for (const category of NON_ESSENTIAL_CATEGORIES) {
      decisions[category] = form.get(category) === "granted" ? "granted" : "denied";
    }
  } else {
    // An unrecognised action stores nothing. A cookie every later request reads must not be writable
    // by a value nobody validated.
    return null;
  }
  return { policyVersion: CONSENT_POLICY_VERSION, decisions };
}

/** serialize renders a state for the cookie. */
export function serialize(state: ConsentState): string {
  return encode(state);
}

/** categories is the presentation order, re-exported so a component does not re-declare it. */
export const OPTIONAL_CATEGORIES: readonly ConsentCategory[] = NON_ESSENTIAL_CATEGORIES;
