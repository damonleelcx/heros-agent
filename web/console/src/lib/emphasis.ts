/**
 * emphasis.ts is 🔴 the confidence reservation, expressed as code (R14, FR29, task 5.11).
 *
 * # The rule
 *
 * The emphasis a **settled result** earns — accent colour, elevation above peers, entrance animation,
 * display-weight type — may not be applied to a value the server QUALIFIED. A qualified value gets the
 * `qualified` treatment and renders its qualifier beside it.
 *
 * # Why this is a correctness property and not a style one
 *
 * P4 went to considerable trouble to make a tie a tie. Overlapping confidence intervals are **not an
 * ordering**, so `p4board.html` renders a tied rank muted and non-bold, puts disqualified variants in
 * their own section titled *excluded from the ranked order, not ranked last*, and flags an
 * uncalibrated judge wherever its metric appears. Every one of those is a **statistical** decision
 * expressed visually.
 *
 * A craft pass that accents the top row, or animates the leader in, has silently overturned that
 * decision — in CSS, where no test in the eval harness can see it. This module is what makes the
 * overturn a failing test instead.
 *
 * # Why it is a function and not a convention
 *
 * `className={row.tie ? "qualified" : "confident"}` written at each of a dozen call sites is a rule
 * with twelve chances to be got wrong, and the twelfth is written under deadline. Here there is one
 * decision, one place, and a test that asserts no component writes the confident class by hand.
 */

/**
 * QUALIFIERS is the closed set of things the server can say that withdraw confidence.
 *
 * Each one is a field the platform actually returns, not a category invented here:
 *
 *   tie             `Row.flags` — this row's interval overlaps another's; the rank is not evidence
 *   provisional     `Row.provisional` — fewer seeds than the seed floor; the interval is wide on purpose
 *   disqualified    `Row.flags` — a gate refused it; it is excluded from the order, not ranked last
 *   low-confidence  `Row.flags` / `coverage.low_confidence` — the eval set cannot support the claim
 *   weak-labeled    `Row.flags` — scored against weak references
 *   uncalibrated    `judge.calibrated === false` — an opinion with no measured agreement behind it
 *   withheld        P5.5 `Card` — a proposal that failed its gate; never rendered as a recommendation
 *   candidate       `ViewLabel.candidate` — structure suggests the pattern; runtime traces have not confirmed it
 *   unverified      P5.5 — no verified delta exists yet
 *   gated           P7 entitlement — the tenant's plan does not include this; not broken, not available
 */
export const QUALIFIERS = [
  "tie",
  "provisional",
  "disqualified",
  "low-confidence",
  "weak-labeled",
  "uncalibrated",
  "withheld",
  "candidate",
  "unverified",
  "gated",
] as const;

export type Qualifier = (typeof QUALIFIERS)[number];

/** isQualifier reports whether a server-supplied flag withdraws confidence. */
export function isQualifier(flag: string): flag is Qualifier {
  return (QUALIFIERS as readonly string[]).includes(flag);
}

/**
 * emphasis decides how a value is drawn, from the qualifiers the SERVER attached to it.
 *
 * Note the direction: it reads flags, it never derives them. Deciding here that a score "looks tied"
 * would be exactly the client-side statistic FR14 forbids — the server already decided, and a second
 * decision is a second source of truth.
 */
export function emphasis(flags: readonly string[] | undefined | null): "confident" | "qualified" {
  if (!flags || flags.length === 0) return "confident";
  return flags.some(isQualifier) ? "qualified" : "confident";
}

/** qualifiersOf returns just the confidence-withdrawing flags, in the order the server gave them. */
export function qualifiersOf(flags: readonly string[] | undefined | null): Qualifier[] {
  return (flags ?? []).filter(isQualifier);
}

/**
 * QUALIFIER_COPY is what each qualifier MEANS, in one clause, rendered beside the value.
 *
 * Beside it, not in a tooltip. A qualifier reachable only by hover is a qualifier absent from a
 * screenshot, a printout, a screen reader's linear pass, and a phone — which is to say absent from
 * most of the ways this screen is actually read (FR31).
 */
/**
 * QUALIFIER_LABEL is the one- or two-word badge a qualifier wears beside its value.
 *
 * It exists alongside `QUALIFIER_COPY` rather than replacing it, and both render. The badge is what a
 * reader scanning a table of forty rows actually sees; the sentence is what tells them what it MEANS,
 * and dropping it would leave a coloured word that looks like a category rather than a withdrawal of
 * confidence. A test asserts the sentence renders beside the value rather than only in a tooltip.
 */
export const QUALIFIER_LABEL: Record<Qualifier, string> = {
  tie: "tied",
  provisional: "provisional",
  disqualified: "disqualified",
  "low-confidence": "low confidence",
  "weak-labeled": "weak-labeled",
  uncalibrated: "uncalibrated",
  withheld: "withheld",
  candidate: "candidate",
  unverified: "unverified",
  gated: "gated",
};

export const QUALIFIER_COPY: Record<Qualifier, string> = {
  tie: "tied — its interval overlaps another's, so this rank is an ordering, not evidence",
  provisional: "provisional — computed from fewer seeds than the seed floor, so the interval is wide",
  disqualified: "disqualified — a gate refused it; excluded from the ranked order, not ranked last",
  "low-confidence": "low confidence — the eval set cannot support a claim this strong",
  "weak-labeled": "weak-labeled — scored against weak references rather than gold ones",
  uncalibrated: "uncalibrated judge — no measured agreement stands behind this number",
  withheld: "withheld — it did not pass its verification gate",
  candidate: "candidate — structure shows the shape; runtime traces have not confirmed it",
  unverified: "unverified — no verified delta exists for this yet",
  gated: "not included in this plan",
};
