import type { ReactNode } from "react";
import type { Role } from "@/lib/roles";
import { ROLE_LABELS } from "@/lib/roles";

/**
 * states.tsx renders the distinct states a view can be in — the four the interface floor requires
 * (loading, empty, denied, degraded — FR26) plus the fifth a fail-closed platform actually produces
 * (outcome unknown — FR36).
 *
 * They are separate components rather than one with a variant prop because the failure these exist to
 * prevent is exactly the collapse of two of them into one: a permission denial rendered as an empty
 * result ("there is no such data"), a transport failure rendered as absence ("the fleet is idle"), or
 * an unconfirmed command rendered as either success or failure. Separate components with different
 * copy, different colour and different affordances make that collapse a deliberate edit rather than a
 * default.
 *
 * # Why "denied" does not use the hazard palette
 *
 * A denial is not an alarm. It is a routing problem with an answer — this role holds it, ask them.
 * Painting it red would spend the colour the kill switch needs and tell the operator something untrue
 * about the severity of what just happened (FR31).
 */

export function LoadingState({ what }: { what: string }) {
  return (
    <div className="state state--loading" role="status" aria-live="polite">
      <p className="state__title">Loading</p>
      <p className="state__body">Fetching {what} from the platform.</p>
    </div>
  );
}

export function EmptyState({ what, hint }: { what: string; hint?: string }) {
  return (
    <div className="state state--empty">
      <p className="state__title">Nothing to show</p>
      <p className="state__body">
        The platform returned no {what}. This is a real, current answer — not a failure to load.
        {hint ? ` ${hint}` : ""}
      </p>
    </div>
  );
}

/**
 * DeniedState names WHO holds the capability and how to ask for it. A bare refusal tells an operator
 * mid-incident nothing they can act on (FR22).
 */
export function DeniedState({
  capability,
  description,
  heldBy,
}: {
  capability: string;
  description?: string;
  heldBy: Role[] | string[];
}) {
  const holders = heldBy.map((r) => ROLE_LABELS[r as Role] ?? r);
  return (
    <div className="state state--denied" role="note">
      <p className="state__title">Your role does not grant this</p>
      <p className="state__body">
        {description ?? capability} requires a role you do not hold.{" "}
        {holders.length > 0 ? (
          <>
            It is held by <strong>{holders.join(" or ")}</strong>. Ask a Superadmin to grant it, or
            hand the action to someone who already holds it.
          </>
        ) : (
          <>No role currently grants it — raise it with the platform team.</>
        )}
      </p>
      <p className="state__body">
        <span className="mono">{capability}</span>
      </p>
    </div>
  );
}

/**
 * DegradedState says a subsystem could not be reached. Distinct from empty, and it never presents
 * partial data as complete.
 */
export function DegradedState({ what, detail }: { what: string; detail?: string }) {
  return (
    <div className="state state--degraded" role="alert">
      <p className="state__title">Degraded — this view is incomplete</p>
      <p className="state__body">
        The console could not reach {what}. What you see below, if anything, is partial and must not be
        read as the whole picture.
      </p>
      {detail ? <p className="state__body mono">{detail}</p> : null}
    </div>
  );
}

/**
 * UnknownState is the third outcome of a command (FR36) — not success, not failure.
 *
 * It exists because this platform write-ahead-audits before it effects and fails closed when it
 * cannot: when a command's response is lost, the honest answer is "we cannot tell whether that took
 * effect", and the remedy is specific — look for it in the audit log. Collapsing this into "failed"
 * invites the operator to retry an action that may already have happened; collapsing it into
 * "succeeded" asserts an effect nobody confirmed.
 */
export function UnknownState({
  what,
  detail,
  verifyHref,
}: {
  what: string;
  detail?: string;
  verifyHref?: string;
}) {
  return (
    <div className="state state--unknown" role="alert">
      <p className="state__title">Outcome unknown — do not retry yet</p>
      <p className="state__body">
        The console lost contact while {what}. It may or may not have taken effect: the platform writes
        its audit entry <em>before</em> the effect, so the audit log is the place that can answer.
      </p>
      {detail ? <p className="state__body mono">{detail}</p> : null}
      {verifyHref ? (
        <p className="state__body">
          <a href={verifyHref}>Check the audit log for this action</a> before issuing it again.
        </p>
      ) : null}
    </div>
  );
}

/** Pill is a status marker: a word and a colour, never a colour alone. */
export function Pill({
  tone,
  children,
}: {
  tone: "ok" | "warn" | "danger" | "neutral" | "accent";
  children: ReactNode;
}) {
  return <span className={`pill pill--${tone}`}>{children}</span>;
}
