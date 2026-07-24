import type { ReactNode } from "react";
import type { Role } from "@/lib/roles";
import { ROLE_LABELS } from "@/lib/roles";

/**
 * states.tsx renders the NINE distinct answers a view can give (FR26, FR36).
 *
 * # Nine questions, nine remedies
 *
 *   Loading       on its way                              → wait
 *   Empty         genuinely nothing yet                   → nothing is wrong
 *   Not mounted   this deployment does not carry it       → nothing is wrong
 *   Gated         outside this plan                       → an entitlement · ✋ NOT an error
 *   Not found     the identifier does not resolve         → check what you pasted
 *   Denied        your role does not hold this            → ask whoever does (named)
 *   Degraded      the platform could not be reached       → check the network, retry
 *   Unusable      it answered something we cannot read    → a version mismatch; retrying will not help
 *   Unknown       a command's response was lost           → the audit log, never a retry
 *
 * They are separate components rather than one with a variant prop because the failure they exist to
 * prevent is exactly the collapse of two into one: a denial rendered as emptiness ("there is no such
 * data"), a transport failure rendered as absence ("the fleet is idle"), a mistyped identifier
 * rendered as a broken platform, an unreadable response rendered as blank data, or an unconfirmed
 * command rendered as either success or failure.
 *
 * # 🔴 They are distinguishable BEFORE the copy is read
 *
 * That is the requirement, and it was previously unmet: the states differed only by border and tint,
 * so telling them apart meant reading two sentences — which is exactly what an operator mid-incident
 * does not do. Each now carries a **mark**: a distinct glyph, set in mono at a size that reads across
 * a room. The mark is `aria-hidden` and carries no information of its own; the title states the answer
 * in words, so a reader who cannot see the glyph loses nothing.
 *
 * Tint groups them into four families — *wait*, *nothing is wrong*, *you can act*, *something is
 * wrong* — and the mark separates the members within each family. Neither carries it alone.
 *
 * # Why "denied", "not found" and "gated" do not use the hazard palette
 *
 * None of them is an alarm. A denial is a routing problem with an answer; a bad identifier is a typo;
 * a gate is an entitlement. Painting any of them red would spend the colour the kill switch needs and
 * tell the operator something untrue about what just happened (FR31).
 */

import type { ReactNode as StateChildren } from "react";

/**
 * StateBlock is the one shape every state is built from, so a new state cannot arrive with its own
 * layout, its own spacing or its own idea of where the remedy goes.
 */
function StateBlock({
  variant,
  mark,
  title,
  role,
  live,
  children,
}: {
  variant: string;
  /** A glyph, for telling the states apart at a glance. Never the only carrier of meaning. */
  mark: string;
  title: string;
  role?: "status" | "alert" | "note";
  live?: "polite" | "assertive";
  children: StateChildren;
}) {
  return (
    <div className={`state state--${variant}`} role={role} aria-live={live}>
      <p className="state__title">
        <span className="state__mark" aria-hidden="true">
          {mark}
        </span>
        {title}
      </p>
      {children}
    </div>
  );
}

export function LoadingState({ what }: { what: string }) {
  return (
    <StateBlock variant="loading" mark="···" title="Loading" role="status" live="polite">
      <p className="state__body">Fetching {what} from the platform.</p>
    </StateBlock>
  );
}

export function EmptyState({ what, hint }: { what: string; hint?: string }) {
  return (
    <StateBlock variant="empty" mark="∅" title="Nothing to show">
      <p className="state__body">
        The platform returned no {what}. This is a real, current answer — not a failure to load.
        {hint ? ` ${hint}` : ""}
      </p>
    </StateBlock>
  );
}

/**
 * NotMountedState says the capability is not installed in THIS deployment.
 *
 * It is not an error and not an emptiness: the surface exists, the operator's role may well grant it,
 * and there is simply nothing behind it here. Rendering that as "nothing to show" invites an operator
 * to wait for data that is never coming; rendering it as degraded sends them to look for an outage
 * that is not happening.
 */
export function NotMountedState({ what, detail }: { what: string; detail?: string }) {
  return (
    <StateBlock variant="not-mounted" mark="⊘" title="Not installed here">
      <p className="state__body">
        This deployment does not carry {what}. Nothing is wrong and there is nothing to retry — the
        capability is simply not part of this installation.
      </p>
      {detail ? <p className="state__body mono">{detail}</p> : null}
    </StateBlock>
  );
}

/**
 * NotFoundState says the identifier does not resolve.
 *
 * The remedy is the operator's own clipboard, which is why the identifier is echoed back in mono: a
 * mistyped or truncated id is only visible when you can see the characters you actually sent.
 */
export function NotFoundState({ what, identifier }: { what: string; identifier?: string }) {
  return (
    <StateBlock variant="not-found" mark="?" title="No such record" role="note">
      <p className="state__body">
        The platform has no {what} with that identifier. This is a current, authoritative answer — the
        record does not exist, rather than being unreachable. Check what you pasted.
      </p>
      {identifier ? (
        <p className="state__body">
          Looked for <span className="mono">{identifier}</span>
        </p>
      ) : null}
    </StateBlock>
  );
}

/**
 * GatedState says the subject's plan does not include this.
 *
 * ✋ It is emphatically NOT an error, and the copy says so: an entitlement boundary rendered in the
 * language of failure teaches operators that the console is unreliable, and teaches them to click
 * through the states that are real failures.
 */
export function GatedState({ what, planName }: { what: string; planName?: string }) {
  return (
    <StateBlock variant="gated" mark="▲" title="Outside this plan" role="note">
      <p className="state__body">
        {what} is not included{planName ? ` in the ${planName} plan` : " in this tenant's plan"}. This
        is an entitlement boundary, not a fault — nothing failed and nothing needs fixing.
      </p>
    </StateBlock>
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
    <StateBlock variant="denied" mark="⊗" title="Your role does not grant this" role="note">
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
    </StateBlock>
  );
}

/**
 * DegradedState says a subsystem could not be reached. Distinct from empty, and it never presents
 * partial data as complete.
 */
export function DegradedState({ what, detail }: { what: string; detail?: string }) {
  return (
    <StateBlock variant="degraded" mark="⚠" title="Degraded — this view is incomplete" role="alert">
      <p className="state__body">
        The console could not reach {what}. What you see below, if anything, is partial and must not be
        read as the whole picture.
      </p>
      {detail ? <p className="state__body mono">{detail}</p> : null}
    </StateBlock>
  );
}

/**
 * UnusableState says the platform answered, and the console could not read the answer.
 *
 * 🔴 This is the state whose ABSENCE was most dangerous. Without it, a response the console could not
 * parse was cast to the expected shape and rendered as a page of blank fields — a version mismatch
 * presented as data, and read as data. "The tenant has no plan" and "we cannot read the tenant" look
 * identical on screen and could not be further apart in what they ask the operator to do.
 *
 * It is deliberately separate from degraded: degraded means *try again*, and this means *retrying will
 * not help*. Somebody has to reconcile two versions.
 */
export function UnusableState({ what, detail }: { what: string; detail?: string }) {
  return (
    <StateBlock variant="unusable" mark="≠" title="Unreadable answer — do not trust this view" role="alert">
      <p className="state__body">
        The platform answered for {what}, and the console could not read what it sent. This is a
        version mismatch between the console and the platform, not an outage — retrying will produce
        the same result. Nothing below is safe to read as data.
      </p>
      {detail ? <p className="state__body mono">{detail}</p> : null}
    </StateBlock>
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
    <StateBlock variant="unknown" mark="⁇" title="Outcome unknown — do not retry yet" role="alert">
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
    </StateBlock>
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
