/**
 * analytics-events.ts is the CENTRAL EVENT ENUM — the complete set of things this product will ever
 * tell an analytics backend happened.
 *
 * # Why a closed enum, and why the build fails on an ad-hoc name
 *
 * An event name is the one field an analytics call site can invent, and every analytics implementation
 * in the world drifts the same way: `signup_start`, `signup-started`, `startSignup`, all three live,
 * none of them comparable, and the funnel that was supposed to answer a question answers three
 * questions badly.
 *
 * That is the ordinary cost. The one that matters here is different: **an invented name is a free-text
 * field on the far side of a boundary.** `install_step_${channel}` is one plausible line of code and an
 * exfiltration path — the same shape as `fmt.Errorf("failed to resolve prompt %q", p)` on the error
 * side, and refused for the same reason. A closed enum is what makes "no free text reaches the
 * analytics backend" checkable by reading one file.
 *
 * `scripts/scan-events.mjs` fails the build on any `track(...)` call whose argument is not a member.
 *
 * # What an event may carry, and what it may not
 *
 * The complete parameter set is in `ANALYTICS_ALLOWLIST` below: an event name, a surface id from the
 * closed surface enum, a plan NAME, the edition, the release, and a timestamp. No tenant id, no
 * principal id, no run / variant / node id, no path, no query, no referrer beyond first-party, and no
 * free text of any kind.
 */

/**
 * PUBLIC_FUNNEL_EVENTS are the events the PUBLIC surface may report.
 *
 * Four groups, matching the four questions the public surface exists to answer: was the page read, was
 * the argument read, did somebody try to install, did somebody try to sign up. Nothing here counts a
 * signed-in action — console usage is emitted server-side, with its own list.
 */
export const PUBLIC_FUNNEL_EVENTS = [
  // Was the page read at all
  "page_viewed",
  // Was the argument read — a section scrolled into view, named by the section's id
  "section_reached",
  // The install page's own steps
  "install_page_viewed",
  "install_channel_selected",
  "install_command_copied",
  "install_verification_read",
  // The sign-up path
  "signup_started",
  "signup_plan_selected",
  "signup_completed",
] as const;

/**
 * CONSOLE_EVENTS are the events emitted SERVER-SIDE from the two BFF halves (wave 24f).
 *
 * One event, deliberately. "Which of the eleven surfaces shipped by P13–P18 are actually used" is the
 * question that motivated console analytics, and a surface view answers it. Every additional event is a
 * new decision about what leaves the boundary, and none has been made yet.
 */
export const CONSOLE_EVENTS = ["surface_viewed"] as const;

export const ANALYTICS_EVENTS = [...PUBLIC_FUNNEL_EVENTS, ...CONSOLE_EVENTS] as const;

export type AnalyticsEvent = (typeof ANALYTICS_EVENTS)[number];

/** isAnalyticsEvent is the runtime half of the closed set, for a value crossing a boundary. */
export function isAnalyticsEvent(name: string): name is AnalyticsEvent {
  return (ANALYTICS_EVENTS as readonly string[]).includes(name);
}

/**
 * SECTIONS is the closed set of section ids `section_reached` may name.
 *
 * Without it, "named by the section's id" would be a free-text parameter wearing a closed event name —
 * which is the leak the enum exists to close, one field to the right.
 */
export const SECTIONS = ["how", "plans", "evidence", "install", "boundary"] as const;
export type SectionId = (typeof SECTIONS)[number];

/**
 * ANALYTICS_ALLOWLIST is design D5's second table: the complete set of parameters an analytics event
 * may carry.
 *
 * Same discipline as the error-event allowlist and for the same reason: an event is CONSTRUCTED from
 * this list, not serialised and stripped. A field added to some internal representation is absent by
 * default.
 */
export const ANALYTICS_ALLOWLIST = [
  { name: "event.name", why: "From the enum above. An ad-hoc name fails the build." },
  {
    name: "surface_id",
    why:
      "From the closed surface enum — never a URL, which under /app carries variant, run, node and " +
      "tenant identifiers.",
  },
  {
    name: "plan_name",
    why:
      "The plan NAME only (Free / Team / Business / Enterprise). Never a price and never a value: a " +
      "price in an analytics backend is a business number held by a third party.",
  },
  { name: "edition", why: "Deployment shape, from a closed set. Never a customer name, never a hostname." },
  { name: "release", why: "Build identifier, so a funnel change can be attributed to a deploy." },
  { name: "occurred_at", why: "Timestamp at second granularity. Not milliseconds: a millisecond timestamp is a weak identifier." },
] as const;

/** ANALYTICS_ALLOWLIST_KEYS is the flattened set the assertions walk. */
export const ANALYTICS_ALLOWLIST_KEYS: readonly string[] = ANALYTICS_ALLOWLIST.map((f) => f.name);

/**
 * PENDING_CALL_SITES names every enum member that has NO call site yet, and why.
 *
 * # Why this table exists rather than a shorter enum
 *
 * The both-directions discipline, applied to events: an enum member nothing emits is a permission
 * nobody asked for, exactly like a stale allowlist entry. A test requires every member to be either
 * CALLED somewhere or listed here with a reason — so "we declared it and never wired it" is a visible
 * fact rather than a silence.
 *
 * The reasons below are all the same shape and worth reading once: the control the event would report
 * does not exist on the shipped surface. Declaring the name now and wiring it when the control ships is
 * the right order — it means the funnel's vocabulary is decided once, by this file, rather than by
 * whoever adds the control.
 */
export const PENDING_CALL_SITES: Record<string, string> = {
  install_channel_selected:
    "The install page renders a channel table, not a selector. There is nothing to select, so there is " +
    "nothing to report; the event is declared so the name is settled before the control exists.",
  install_command_copied:
    "The install page renders its commands as text for a reader to copy with the browser's own copy. A " +
    "copy BUTTON would be a client component on a surface whose whole property is that it needs no " +
    "session and no script — that trade has not been made.",
  install_verification_read:
    "Verification is a section of the install page, not a step with a boundary. It becomes one when the " +
    "page gains the disclosure P20 sketched.",
  signup_plan_selected:
    "Plan selection happens on Stripe's own page (P21), on an origin this product does not instrument " +
    "and must not.",
  signup_completed:
    "Completion is observed SERVER-side from the Stripe webhook, where it is a fact rather than a " +
    "browser's claim. Reporting it from the browser would be a second, weaker source for a number that " +
    "already has an authoritative one.",
};

/**
 * PLAN_NAMES is the closed set `plan_name` may take.
 *
 * A plan name is the ONE product fact permitted in an analytics event, and it is permitted because it
 * is a label from a set of four rather than anything derived from a customer.
 */
export const PLAN_NAMES = ["Free", "Team", "Business", "Enterprise"] as const;
