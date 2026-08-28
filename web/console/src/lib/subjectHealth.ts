import "server-only";
import { SUBJECT_STATES, type SubjectState } from "./axisSubject";

/**
 * subjectHealth.ts counts what the subject resolver ANSWERED, per process, readable over HTTP (P37 §7.1).
 *
 * # Why a counter and not a log line
 *
 * `health-signal-surface` (enforced): *"any pipeline health, connectivity or self-check signal cannot
 * live only in a log — it must be exposed at an endpoint something can read."* A rate that exists only
 * in logs is a rate somebody has to go and derive during the incident it would have warned them about.
 *
 * # 🔴 The one number that matters, and why it is not an error rate
 *
 * `ambiguous` is the cost side of design D1. The whole premise is that the reader is asked WHICH NODE at
 * most once, and not at all in the common case — so a rising `ambiguous` share is the signal that the
 * resolution order is not working for real repositories, and it is invisible in every error metric
 * because nothing failed. An operator watching only failures would see a healthy surface asking every
 * reader a question P37 exists to remove.
 *
 * `not_connected` is the second: it is the customer's own boundary, not a fault, and a deployment where
 * it dominates is a deployment where P32's connection flow is not landing — which is a product signal
 * rather than an outage.
 *
 * # 🔴 What these counters are NOT, stated so nobody reads them as more
 *
 *   · **Per process.** They reset on every rollout and are not summed across replicas. An operator
 *     comparing today's absolute count with yesterday's is comparing two different processes.
 *   · **Not a store.** D-37.4: this phase adds no table. These are integers in memory, and losing them
 *     costs a convenience.
 *   · **Not a business metric.** They count RESOLUTIONS, not readers: one person opening four axis
 *     surfaces increments `resolved` four times. The ratio between the states is the readable signal;
 *     the magnitudes are not.
 *
 * Saying that here rather than leaving it to be discovered is the same discipline `scan-prose.mjs`
 * applies to its own blind spot. A signal whose limits are unstated is one that will be trusted past
 * them.
 */

/** counts is the whole state. One integer per member of the closed set, and no other key can exist. */
const counts: Record<SubjectState, number> = Object.fromEntries(
  SUBJECT_STATES.map((state) => [state, 0]),
) as Record<SubjectState, number>;

/**
 * recordSubjectOutcome counts one resolution.
 *
 * It cannot fail in a way that matters, and nothing above it checks: an uncounted resolution costs a
 * data point, and a health counter that could break a page render would be a worse problem than the one
 * it exists to report.
 */
export function recordSubjectOutcome(state: SubjectState): void {
  if (counts[state] === undefined) return;
  counts[state] += 1;
}

/**
 * subjectResolverHealth is what `/api/health` publishes.
 *
 * Every member of the closed set is present, including the ones at zero. 🔴 An omitted key and a zero
 * are different facts — "this deployment has never refused a reader for want of a connection" and "this
 * build does not report that state" — and only one of them is good news.
 */
export function subjectResolverHealth(): {
  states: Record<SubjectState, number>;
  total: number;
  /** scope names what these numbers are, so a reader cannot take them for a fleet-wide total. */
  scope: string;
} {
  const total = SUBJECT_STATES.reduce((sum, state) => sum + counts[state], 0);
  return {
    states: { ...counts },
    total,
    scope: "this console process since it started; not summed across replicas and reset by every rollout",
  };
}
