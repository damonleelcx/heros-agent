/**
 * format.ts is the console's ONE locale swap point (R4, FR23, task 5.5).
 *
 * # Why a single function, in an English-only product
 *
 * `Intl` follows the browser's locale unless told otherwise. On a Chinese-locale browser,
 * `new Intl.RelativeTimeFormat().format(0, "second")` renders as a Chinese word — **next to an English
 * label**, so the product ships a mixed-language string inside one sentence, on a machine nobody on
 * the team owns. The defect is invisible to every build, type-check and unit test.
 *
 * So the locale is pinned here, once, and `scripts/scan-strings.mjs` fails the build on any `Intl`
 * constructed anywhere else. This is the whole of P9's i18n work, and it is the seam a future i18n
 * phase changes.
 *
 * # Why the formatters are module-level constants
 *
 * `Intl.NumberFormat` construction is expensive relative to `.format()`, and a leaderboard formats a
 * few thousand cells. Constructing per call is the kind of thing that only shows up as a janky scroll
 * on the largest board a customer has.
 *
 * # 🔴 What this file must never grow
 *
 * A function that ROUNDS before comparing, ranks, or derives a statistic. Formatting decides how a
 * number is drawn; it must never decide what the number is (FR14). `score(0.7149)` may render
 * `0.715`; nothing anywhere may then compare the rendered string to decide an ordering — the server
 * already decided, and a second decision here is a second source of truth for a statistical claim.
 */

/** LOCALE is the pinned locale. Deliberately not `navigator.language`, and deliberately not a const enum. */
export const LOCALE = "en-US";

const INTEGER = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 0 });
const SCORE = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 3, maximumFractionDigits: 3 });
const USD_5 = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 5, maximumFractionDigits: 5 });
const USD_2 = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const PERCENT_0 = new Intl.NumberFormat(LOCALE, { style: "percent", maximumFractionDigits: 0 });
const DATETIME = new Intl.DateTimeFormat(LOCALE, {
  dateStyle: "medium",
  timeStyle: "medium",
  timeZone: "UTC",
});

/**
 * NULL is the one placeholder for an absent value, used everywhere (P2-21, P25-4).
 *
 * One character, one meaning: the server did not give us this. It is deliberately NOT used for a
 * value of zero, which is a measurement — rendering `0` as `—` is how "we measured nothing" and "we
 * measured nothing happening" become the same sentence.
 */
export const NULL_VALUE = "—";

function absent(value: number | null | undefined): value is null | undefined {
  return value === null || value === undefined || Number.isNaN(value);
}

/** integer renders a count. */
export function integer(value: number | null | undefined): string {
  return absent(value) ? NULL_VALUE : INTEGER.format(value);
}

/** score renders a composite or normalized score at three decimals — one precision per view. */
export function score(value: number | null | undefined): string {
  return absent(value) ? NULL_VALUE : SCORE.format(value);
}

/** usd5 renders a per-call cost, which is small enough that two decimals is all zeroes (P25-4). */
export function usd5(value: number | null | undefined): string {
  return absent(value) ? NULL_VALUE : `$${USD_5.format(value)}`;
}

/** usd2 renders an aggregate cost. */
export function usd2(value: number | null | undefined): string {
  return absent(value) ? NULL_VALUE : `$${USD_2.format(value)}`;
}

/** percent renders a fraction in [0,1] as a percentage. It does NOT accept an already-scaled number. */
export function percent(fraction: number | null | undefined): string {
  return absent(fraction) ? NULL_VALUE : PERCENT_0.format(fraction);
}

/** ms renders a latency at zero decimals (P25-4). */
export function ms(value: number | null | undefined): string {
  return absent(value) ? NULL_VALUE : `${INTEGER.format(value)} ms`;
}

/**
 * instant renders a timestamp in UTC.
 *
 * UTC rather than the viewer's zone, deliberately: two engineers comparing a run in a call must read
 * the same string, and a screenshot in a pull request must mean the same thing to its reviewer. The
 * zone is stated in the output so it is never ambiguous.
 */
export function instant(value: string | number | Date | null | undefined): string {
  if (value === null || value === undefined) return NULL_VALUE;
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return NULL_VALUE;
  return `${DATETIME.format(date)} UTC`;
}

/**
 * shortHash truncates a hash for display while the full value stays available as a title.
 *
 * Twelve characters, matching `variantspec.Display` and `Row.ConfigHashShort` on the server — the
 * console must not invent a second truncation length, or two views of the same hash stop looking like
 * the same hash.
 */
export function shortHash(value: string | null | undefined, length = 12): string {
  if (!value) return NULL_VALUE;
  return value.length <= length ? value : value.slice(0, length);
}

/**
 * plural picks a form. English-only, and that is the point of R4's swap point: when i18n arrives this
 * is one of the two functions that changes.
 */
export function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many;
}
