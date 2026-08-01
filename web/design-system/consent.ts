/**
 * consent.ts is the per-visitor consent record: what it is stored in, what it means, and what the
 * default is.
 *
 * # 🔴 Default DENIED, and a refusal is stored AS a refusal
 *
 * Every non-essential category starts at `denied`, and nothing loads and no non-essential storage is
 * written before an explicit grant. The three states are `not-asked | granted | denied`, and the
 * difference between the first and the third is the whole reason there are three: "we have not asked"
 * and "they said no" are different facts, and a system that cannot tell them apart re-asks somebody who
 * already refused. That is the behaviour a visitor experiences as being ignored.
 *
 * # Why a first-party cookie and NOT the P23 `consent-records` ledger
 *
 * 🔴 This comment exists so the next reader does not "fix" it.
 *
 * The P23 ledger is STATUTORY: append-only, keyed to an immutable document hash, and it survives
 * identity erasure because a record of what somebody agreed to is evidence, not preference. This is the
 * opposite lifecycle in every respect — a revocable, per-browser, per-visitor preference with no tenant
 * and no principal, which a visitor may change twice in a minute and which must disappear when they
 * clear their browser.
 *
 * Putting it in the ledger would mean a cookie choice outliving a deletion request, which is the one
 * outcome a consent mechanism must never produce. So: a first-party cookie, carrying the policy version
 * it was given against and one decision per category, and nothing else.
 *
 * # Why the policy version travels with the decision
 *
 * A grant is given against a specific statement of what will be collected. When that statement changes
 * MATERIALLY, the grant no longer covers what is now being asked for — so the version is stored beside
 * the decision, and a material publication resets every non-essential category to `not-asked`. A
 * non-material version (a typo, a clearer sentence) asks nobody, because re-prompting for a
 * clarification trains people to click accept without reading.
 */

import { CONSENT_CATEGORIES, NON_ESSENTIAL_CATEGORIES, type ConsentCategory } from "./third-party-policy.ts";

/** CONSENT_COOKIE is the first-party cookie name. One spelling, in one place. */
export const CONSENT_COOKIE = "heros_consent";

/**
 * CONSENT_POLICY_VERSION is the version of the statement a grant is given against.
 *
 * 🔴 It IS the sub-processor document's version, spelled `sub-processors@<version>`, and that is the
 * wiring task 7.2 asks for rather than a coincidence of two numbers.
 *
 * A grant is given against a statement of WHO RECEIVES DATA. `content/legal/en/sub-processors/` is that
 * statement, and it carries `material: true`. Deriving the consent version from the document's version
 * means publishing a new material version cannot fail to invalidate the grants it invalidates — and
 * `tests/consent.test.mjs` reads the checked-in document's front matter and fails if the two disagree,
 * so a publication that forgot the bump is a red build rather than a silent over-collection.
 *
 * It is bumped when what is collected, who receives it, or which surfaces it runs on changes — not when
 * the wording improves. A non-material change publishes a new document version and leaves this alone,
 * which asks nobody.
 */
export const CONSENT_POLICY_VERSION = "sub-processors@1.0.0";

/** Decision is the three-state answer per category. Never a boolean. */
export type Decision = "not-asked" | "granted" | "denied";

/** ConsentState is the whole record: the version it was given against, and a decision per category. */
export type ConsentState = {
  policyVersion: string;
  decisions: Record<ConsentCategory, Decision>;
};

/**
 * DEFAULT_STATE is what a visitor who has never answered has.
 *
 * `essential` is `granted` because it is not a choice — it covers the session cookie, the theme
 * preference and this record itself, none of which are optional for the product to work, and offering a
 * toggle that cannot be turned off is consent theatre. Everything else is `not-asked`, which BEHAVES as
 * denied (nothing loads) while remaining distinguishable from a refusal.
 */
export const DEFAULT_STATE: ConsentState = {
  policyVersion: CONSENT_POLICY_VERSION,
  decisions: {
    essential: "granted",
    product_analytics: "not-asked",
    session_replay: "not-asked",
    error_diagnostics: "not-asked",
  },
};

/**
 * decode parses a cookie value into a state, falling back to the default on anything unexpected.
 *
 * The fallback direction is the safe one: a malformed cookie becomes "nothing granted", never "assume
 * they agreed". A record whose policy version does not match the current MATERIAL version is reset to
 * `not-asked` for every non-essential category, which is the invalidation rule expressed at the only
 * place that can enforce it — the point where the record is read.
 */
export function decode(raw: string | undefined | null): ConsentState {
  if (!raw) return DEFAULT_STATE;
  let parsed: unknown;
  try {
    parsed = JSON.parse(decodeURIComponent(raw));
  } catch {
    return DEFAULT_STATE;
  }
  if (typeof parsed !== "object" || parsed === null) return DEFAULT_STATE;
  const record = parsed as { v?: unknown; d?: unknown };
  const version = typeof record.v === "string" ? record.v : "";
  const stored = typeof record.d === "object" && record.d !== null ? (record.d as Record<string, unknown>) : {};

  const decisions = { ...DEFAULT_STATE.decisions };
  const stale = version !== CONSENT_POLICY_VERSION;
  for (const category of CONSENT_CATEGORIES) {
    if (category === "essential") continue;
    if (stale) continue; // a material version reset — re-ask rather than carry the old answer forward
    const value = stored[category];
    if (value === "granted" || value === "denied" || value === "not-asked") {
      decisions[category] = value;
    }
  }
  return { policyVersion: stale ? CONSENT_POLICY_VERSION : version, decisions };
}

/** encode renders a state for the cookie. Compact keys: this value travels on every request. */
export function encode(state: ConsentState): string {
  const d: Record<string, Decision> = {};
  for (const category of NON_ESSENTIAL_CATEGORIES) d[category] = state.decisions[category];
  return encodeURIComponent(JSON.stringify({ v: state.policyVersion, d }));
}

/**
 * grantedCategories returns the categories that are currently granted.
 *
 * `essential` is always in the list. `not-asked` is NOT — it behaves as denied, which is what
 * "default-denied" means in the only place it can be enforced.
 */
export function grantedCategories(state: ConsentState): ConsentCategory[] {
  return CONSENT_CATEGORIES.filter((c) => state.decisions[c] === "granted");
}

/** isGranted is the single question every gated integration asks. */
export function isGranted(state: ConsentState, category: ConsentCategory): boolean {
  return state.decisions[category] === "granted";
}

/** hasAnswered reports whether the visitor has decided every non-essential category. */
export function hasAnswered(state: ConsentState): boolean {
  return NON_ESSENTIAL_CATEGORIES.every((c) => state.decisions[c] !== "not-asked");
}
