import "server-only";
import type { PendingRow } from "@/components/legalAcceptance";

/**
 * consentGate.ts decides WHETHER the commitment gate is shown, and to whom (task 10.2).
 *
 * # 🔴 Behind a flag, and new principals first
 *
 * The rollout order is the whole point. A gate switched on for everybody at once asks every existing
 * customer to accept, simultaneously, on the day it deploys — and if anything about it is wrong, it is
 * wrong for all of them at the moment they are least able to route around it.
 *
 * So:
 *
 *   off              (default) nothing is gated. The notice on /app/account still names what is
 *                    outstanding, because informing is not gating.
 *   new-principals   only a principal with NO acceptance at all is asked. An existing customer with a
 *                    superseded acceptance is not interrupted yet.
 *   all              every principal with an outstanding document is asked at a commitment.
 *
 * `new-principals` is the rung that makes this safe to turn on: the population is people who are
 * signing up right now, the gate is part of their first experience rather than an interruption to
 * work in progress, and the blast radius on day one is "new sign-ups" rather than "everyone".
 *
 * # What the flag does NOT control
 *
 * It does not control whether acceptances are recorded — the endpoint works regardless — and it does not
 * control reading. Turning it off removes an ask; it never removes a record, and it never adds access
 * somebody did not have.
 */

/** GateMode is the rollout rung. */
export type GateMode = "off" | "new-principals" | "all";

/**
 * gateMode reads the flag.
 *
 * Default OFF. An unrecognised value is also off, and deliberately so: a typo in a deployment variable
 * must not switch a customer-facing interruption on. The failure direction for a rollout flag is "did
 * less than you meant", never "did more".
 */
export function gateMode(): GateMode {
  switch ((process.env.CONSOLE_CONSENT_GATE ?? "").trim().toLowerCase()) {
    case "all":
      return "all";
    case "new-principals":
      return "new-principals";
    default:
      return "off";
  }
}

/**
 * shouldGate decides whether this principal is asked at a commitment moment.
 *
 * `hasAnyAcceptance` distinguishes a brand-new principal from an existing one whose acceptance has been
 * superseded. That distinction is the `new-principals` rung, and it is computed from the record rather
 * than from an account-creation date — which the platform does not expose and which would be a second,
 * driftable answer to the same question.
 */
export function shouldGate({
  mode,
  pending,
  hasAnyAcceptance,
}: {
  mode: GateMode;
  pending: PendingRow[];
  hasAnyAcceptance: boolean;
}): boolean {
  if (pending.length === 0) return false;
  switch (mode) {
    case "off":
      return false;
    case "new-principals":
      return !hasAnyAcceptance;
    case "all":
      return true;
  }
}
