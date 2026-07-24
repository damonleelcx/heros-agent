"use client";

import { useActionState } from "react";
import type { ReactNode } from "react";
import type { ActionResult } from "@/lib/actions";
import { ROLE_LABELS, type Role } from "@/lib/roles";
import { timestamp } from "@/lib/format";

/**
 * actionForm.tsx renders a dangerous-action confirmation with friction proportional to blast radius
 * (FR24), and the truthful outcome that follows it (FR36).
 *
 * The friction is driven by the SERVER's classification (`typedTarget`, `global`), passed in as props
 * from the capability's friction metadata — the console never decides on its own which actions need a
 * typed target, because a second opinion about blast radius is a second opinion that can be wrong.
 *
 *   - reversible per-tenant  → a required reason + a confirm checkbox
 *   - irreversible / elevate → the above PLUS typing the target identifier
 *   - global scope           → the above, rendered visually distinct and higher-friction
 *
 * # What the craft work was NOT allowed to touch (FR37)
 *
 * Every field below opens EMPTY. Nothing is pre-filled from context, from history, or from the last
 * thing the operator did — a reason suggested by the console is not a reason the operator gave, and a
 * pre-filled typed target is not a confirmation. The command palette can navigate here; it cannot
 * submit this form. The step count from intent to destructive effect is exactly what it was before the
 * console was made faster to read.
 *
 * # The four outcomes
 *
 * in-flight · succeeded · failed · outcome unknown. The fourth is not a nicety: this platform commits
 * the audit entry BEFORE the effect and fails closed, so a lost response genuinely means "it may have
 * happened". Rendering that as failure invites a double-suspend; rendering it as success asserts
 * something nobody confirmed.
 */
export function ActionForm({
  title,
  hint,
  action,
  submitLabel,
  danger,
  global,
  typedTarget,
  targetIdentifier,
  targetLabel,
  actionName,
  children,
}: {
  title: string;
  hint?: string;
  action: (state: ActionResult | null, fd: FormData) => Promise<ActionResult>;
  submitLabel: string;
  danger?: boolean;
  global?: boolean;
  typedTarget?: boolean;
  targetIdentifier?: string;
  /** The subject this control acts on, for the receipt and the unknown-outcome verification link. */
  targetLabel?: string;
  /** The capability name, for the audit-log link on an unknown outcome. */
  actionName?: string;
  children?: ReactNode;
}) {
  const [state, formAction, pending] = useActionState(action, null);
  const cls = global ? "action action--global" : danger ? "action action--danger" : "action";
  const formId = title.replace(/[^a-zA-Z0-9]+/g, "-").toLowerCase();

  return (
    <form className={cls} action={formAction}>
      <fieldset disabled={pending}>
        <legend>{title}</legend>
        {hint ? <p className="hint">{hint}</p> : null}
        {global ? (
          <p className="state state--degraded" role="note">
            <strong>Fleet-wide.</strong> This affects <em>every</em> tenant, not one. It is deliberately
            higher-friction than the per-tenant control so the two can never be confused.
          </p>
        ) : null}

        {children}

        <label htmlFor={`${formId}-reason`}>Reason (recorded in the audit log)</label>
        <textarea
          id={`${formId}-reason`}
          name="reason"
          rows={2}
          required
          placeholder="Why are you doing this? A ticket reference and a sentence."
        />

        {typedTarget ? (
          <>
            <label htmlFor={`${formId}-typed`}>
              Type the target to confirm: <code>{targetIdentifier}</code>
            </label>
            <p className="hint">
              This action cannot be undone. Typing the target is a second confirmation, not a
              formality.
            </p>
            <input id={`${formId}-typed`} name="typed_target" type="text" autoComplete="off" required />
          </>
        ) : null}

        <div className="checkbox-row">
          <input id={`${formId}-confirm`} name="confirmed" type="checkbox" required />
          <label htmlFor={`${formId}-confirm`}>I confirm this action</label>
        </div>

        <button type="submit" className={global || danger ? "danger" : "primary"} disabled={pending}>
          {pending ? "Working…" : submitLabel}
        </button>
      </fieldset>

      {/* In flight is its own rendering: the command has left, and nothing is claimed about it yet. */}
      {pending ? (
        <div className="state state--loading" role="status" aria-live="polite">
          <p className="state__title">In flight</p>
          <p className="state__body">
            Issued to the platform. Nothing is shown as done until the platform — and its write-ahead
            audit — confirm it.
          </p>
        </div>
      ) : state ? (
        <ActionOutcome state={state} actionName={actionName} targetLabel={targetLabel} />
      ) : null}
    </form>
  );
}

function ActionOutcome({
  state,
  actionName,
  targetLabel,
}: {
  state: ActionResult;
  actionName?: string;
  targetLabel?: string;
}) {
  if (state.ok) {
    return <Receipt state={state} />;
  }

  // Outcome unknown — neither success nor failure, with the one remedy that can settle it.
  if (state.kind === "unknown") {
    const query = actionName ? `?action=${encodeURIComponent(actionName)}` : "";
    return (
      <div className="state state--unknown" role="alert">
        <p className="state__title">Outcome unknown — do not retry yet</p>
        <p className="state__body">
          The console lost contact with the platform while issuing this command
          {targetLabel ? ` against ${targetLabel}` : ""}. The platform writes its audit entry{" "}
          <em>before</em> the effect, so it may have taken place. Check the record before issuing it
          again.
        </p>
        <p className="state__body mono">{state.message}</p>
        <p className="state__body">
          <a href={`/audit${query}`}>Open the audit log for this action</a>
        </p>
      </div>
    );
  }

  if (state.kind === "denied") {
    const holders = (state.heldBy ?? []).map((r) => ROLE_LABELS[r as Role] ?? r);
    return (
      <div className="state state--denied" role="alert">
        <p className="state__title">Denied — nothing happened</p>
        <p className="state__body">
          {state.message}
          {holders.length > 0 ? ` This capability is held by ${holders.join(" or ")}.` : ""}
        </p>
      </div>
    );
  }

  if (state.kind === "friction") {
    return (
      <div className="state state--denied" role="alert">
        <p className="state__title">One more step — nothing happened</p>
        <p className="state__body">{state.message}</p>
      </div>
    );
  }

  return (
    <div className="state state--degraded" role="alert">
      <p className="state__title">
        {state.kind === "request" ? "Check the details — nothing happened" : "Platform unreachable"}
      </p>
      <p className="state__body">{state.message}</p>
    </div>
  );
}

/**
 * Receipt states what happened, to whom, under which reason, and the audit entry it wrote (FR36).
 *
 * The audit sequence is a LINK. "What did I just do, and where is the record?" is asked after the
 * moment has passed, and an answer that requires the operator to go and search the log by hand is an
 * answer they will stop asking for. The reversing action is named, or its absence is stated in words —
 * a missing undo button is not a statement.
 */
function Receipt({ state }: { state: Extract<ActionResult, { ok: true }> }) {
  const { receipt, command } = state;
  return (
    <div className="receipt" role="status" aria-live="polite">
      <p className="receipt__title">Done — confirmed by the platform.</p>
      <dl className="receipt__grid">
        {command ? (
          <>
            <dt>Action</dt>
            <dd className="mono">{command.action}</dd>
            <dt>Target</dt>
            <dd className="mono">{command.target}</dd>
            <dt>Reason</dt>
            <dd>{command.reason}</dd>
          </>
        ) : null}
        {receipt ? (
          <>
            <dt>When</dt>
            {/* Through the single locale swap point, like every other instant the console renders —
                a receipt is exactly where two operators comparing notes must read the same string. */}
            <dd>{timestamp(receipt.at)}</dd>
            <dt>Actor</dt>
            <dd className="mono">{receipt.actor_admin_id}</dd>
            <dt>Result</dt>
            <dd className="mono">{receipt.result}</dd>
            <dt>Audit</dt>
            <dd>
              <a href={`/audit?seq=${receipt.outcome_seq}`}>
                entry {receipt.outcome_seq} (write-ahead {receipt.write_ahead_seq})
              </a>{" "}
              <span className="mono">{receipt.entry_hash.slice(0, 16)}…</span>
            </dd>
          </>
        ) : null}
      </dl>
      <p className="receipt__undo">
        {command?.undo ? (
          <>
            <strong>To reverse:</strong> {command.undo}.
          </>
        ) : (
          <>
            <strong>There is no undo for this action.</strong> It is corrected by a further recorded
            action, never by editing what is already on the record.
          </>
        )}
      </p>
      {state.message ? <p className="receipt__undo">{state.message}</p> : null}
    </div>
  );
}
