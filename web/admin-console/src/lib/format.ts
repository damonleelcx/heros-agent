/**
 * format.ts is the console's SINGLE locale swap point.
 *
 * Every `Intl.*` construction in this application happens here, pinned to `en-US`. The rule it
 * enforces is not stylistic: `Intl.NumberFormat()` with no locale argument follows the BROWSER's
 * locale, so the same audit timestamp reads differently on two operators' screens and a decimal
 * separator can silently change meaning mid-incident. UI strings are English; the locale is pinned;
 * and when the product is localized, this file is the one place that changes.
 */

/** LOCALE is the pinned formatting locale. */
export const LOCALE = "en-US";

const numberFormatter = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 4 });
const integerFormatter = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 0 });
const dateTimeFormatter = new Intl.DateTimeFormat(LOCALE, {
  year: "numeric",
  month: "short",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  timeZone: "UTC",
  timeZoneName: "short",
});

/**
 * quantity formats a measured quantity from the platform.
 *
 * It formats a number the SERVER produced. The console performs no arithmetic on platform values and
 * carries no plan price or numeric limit of its own (FR28) — there is nothing here to compute with.
 */
export function quantity(value: number): string {
  return numberFormatter.format(value);
}

/** count formats a whole-number count. */
export function count(value: number): string {
  return integerFormatter.format(value);
}

/** timestamp formats an RFC 3339 instant in UTC, so two operators read the same string. */
export function timestamp(iso: string | undefined | null): string {
  if (!iso) return "—";
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return iso;
  return dateTimeFormatter.format(parsed);
}

const clockFormatter = new Intl.DateTimeFormat(LOCALE, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
  timeZone: "UTC",
});

/**
 * clock renders the wall-clock time the chrome band shows, in UTC.
 *
 * It exists because every figure on this console states the instant it is current as of, and an
 * as-of time is only readable against a NOW the operator can see. Both are UTC and both come from
 * here, so "as of 14:09:31" and the clock beside it can be compared without a timezone in between.
 */
export function clock(at: Date): string {
  return clockFormatter.format(at);
}

/** minutes renders a remaining-seconds value as whole minutes for the impersonation banner. */
export function minutes(seconds: number): string {
  return integerFormatter.format(Math.max(0, Math.floor(seconds / 60)));
}
