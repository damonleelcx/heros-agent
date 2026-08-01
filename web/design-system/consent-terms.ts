/**
 * consent-terms.ts is the TERM DICTIONARY for the four consent categories.
 *
 * # Why the words live in one file
 *
 * Three surfaces name these categories: the banner a visitor answers, the privacy document they can
 * read before answering, and the operator console's oversight view. If those three use three different
 * words for the same thing, a visitor cannot check that what they agreed to is what is described — and
 * "we told them" becomes technically true and practically false.
 *
 * The repository already has a rule for this: user documentation, user interface and CLI share ONE term
 * dictionary, and a mismatch is corrected rather than tolerated. This is that dictionary for P24.
 *
 * # Why the summaries are this short, and this concrete
 *
 * Each says what is COLLECTED and who RECEIVES it, in a sentence. Not "to improve your experience" —
 * that is a purpose, and a purpose is not a disclosure. A reader deciding whether to agree needs to
 * know what leaves their browser, and the honest version of that fits in one line.
 *
 * # Not translated, deliberately
 *
 * The console is English-only (`format.ts` pins `en-US` and a scan fails on a non-Latin script), so
 * these are strings rather than message ids. When a second locale arrives, this file is the ONE place
 * a translator is given — which is the whole point of it existing before there is a second locale.
 */

import type { ConsentCategory } from "./third-party-policy.ts";

export type CategoryTerm = {
  /** The term, used verbatim on every surface. */
  term: string;
  /** What is collected and who receives it. One sentence, concrete. */
  summary: string;
  /** Whether a visitor may decline it. `essential` may not, and says why. */
  optional: boolean;
};

export const CATEGORY_TERMS: Record<ConsentCategory, CategoryTerm> = {
  essential: {
    term: "Essential",
    summary:
      "Keeps you signed in, remembers your theme, and stores the choices you make here. Nothing on " +
      "this list leaves this site, and the product does not work without it.",
    optional: false,
  },
  product_analytics: {
    term: "Usage analytics",
    summary:
      "Counts which pages of this public site are read and which steps of sign-up and installation " +
      "are reached. Sent to Google Analytics. Never used on a signed-in page.",
    optional: true,
  },
  session_replay: {
    term: "Session recording",
    summary:
      "Records how a page of this public site is scrolled and clicked, with every text input masked. " +
      "Sent to Microsoft Clarity. Refused outright on signed-in pages and on the operator console.",
    optional: true,
  },
  error_diagnostics: {
    term: "Error diagnostics",
    summary:
      "Reports the type, the code and the stack of a failure so it can be fixed. Sent to Sentry. " +
      "Carries no message text, no page address and no content — the payload is built from a " +
      "thirteen-field list, and everything else is absent rather than removed.",
    optional: true,
  },
};

/** ORDER is the order the categories are presented in, everywhere. Essential first, then as declared. */
export const CATEGORY_ORDER: readonly ConsentCategory[] = [
  "essential",
  "product_analytics",
  "session_replay",
  "error_diagnostics",
];

/** termOf is the accessor every surface uses, so nobody re-types a term. */
export function termOf(category: ConsentCategory): string {
  return CATEGORY_TERMS[category].term;
}
