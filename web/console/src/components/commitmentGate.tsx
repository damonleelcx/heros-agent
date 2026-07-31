"use client";

import Link from "next/link";
import { useState } from "react";
import type { PendingRow } from "@/components/legalAcceptance";

/**
 * CommitmentGate is the acceptance step at a commitment — first sign-in, checkout, plan change
 * (tasks 10.2, 10.4).
 *
 * # 🔴 NO OPTIMISTIC CHECKMARK, EVER
 *
 * This is the rule the whole component is shaped around. The button does not render success until the
 * platform has answered that the row was COMMITTED. On failure it returns to rest with a plain sentence —
 * *the acceptance was not recorded; nothing has been agreed* — and a retry.
 *
 * An optimistic checkmark here is the worst failure available to this phase: a customer told their
 * acceptance was recorded, a commitment allowed to proceed on the strength of it, and nothing in the
 * database. The direction of the error is what matters, and this direction is survivable.
 *
 * # 🔴 It gates the COMMITMENT, not the console
 *
 * This component is rendered at a commitment moment only. It is not a route guard and not a layout
 * wrapper — a consent modal keyed to a deployment blocks every customer simultaneously, on release day,
 * turning a legal update into an outage. Reading the console, an in-flight run, and the documents
 * themselves are never blocked (asserted by `tests/legal.test.mjs`).
 *
 * # What the reader can do without accepting
 *
 * Read the document. The link is present, it opens the exact version being asked about, and nothing is
 * recorded by following it. A gate that hides the text it is asking you to agree to is not a gate, it is
 * a dark pattern.
 */

type Outcome =
  | { state: "rest" }
  | { state: "submitting" }
  | { state: "recorded"; version: string }
  | { state: "failed"; message: string };

export function CommitmentGate({
  pending,
  method,
  onAccepted,
}: {
  pending: PendingRow[];
  /** Which commitment moment this is. Recorded on the row so an audit can say where consent was given. */
  method: "signin" | "checkout" | "plan_change";
  /** Called once every outstanding document has been recorded. The commitment proceeds from here. */
  onAccepted?: () => void;
}) {
  const [outcome, setOutcome] = useState<Outcome>({ state: "rest" });
  const [done, setDone] = useState<string[]>([]);

  const outstanding = pending.filter((row) => !done.includes(`${row.document_kind}@${row.document_version}`));
  if (outstanding.length === 0) return null;
  const next = outstanding[0];

  async function accept() {
    setOutcome({ state: "submitting" });
    try {
      const res = await fetch("/api/console/legal/acceptances", {
        method: "POST",
        headers: { "content-type": "application/json" },
        // Exactly the three fields plus the method. The BFF forwards them unchanged; the tenant and the
        // principal come from the session on the server side and are not sent from here.
        body: JSON.stringify({
          document_kind: next.document_kind,
          document_version: next.document_version,
          content_hash: next.content_hash,
          method,
        }),
      });

      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string; reason?: string } | null;
        setOutcome({
          state: "failed",
          message:
            body?.reason === "content_hash_mismatch"
              ? "This document changed while you were reading it. Reload the page and read the current version — nothing has been agreed."
              : "The acceptance was not recorded; nothing has been agreed.",
        });
        return;
      }

      // Only here, after a committed row, is anything rendered as accepted.
      const accepted = `${next.document_kind}@${next.document_version}`;
      const remaining = outstanding.length - 1;
      setDone((prior) => [...prior, accepted]);
      setOutcome({ state: "recorded", version: next.document_version });
      if (remaining === 0) onAccepted?.();
    } catch {
      setOutcome({
        state: "failed",
        message: "The acceptance was not recorded; nothing has been agreed.",
      });
    }
  }

  return (
    <div className="rounded-xl border border-border bg-card p-4" data-commitment-gate="true">
      <p className="stat__label">Before you continue</p>
      <p className="mt-2 text-sm text-foreground">
        The <strong className="font-semibold">{label(next.document_kind)}</strong>, version{" "}
        {next.document_version}, takes effect on {next.effective_date}. Accepting records the version and
        a hash of the exact text you are shown.
      </p>

      <p className="mt-3">
        <Link className="prose-link text-sm" href={next.route}>
          Read it
        </Link>
        <span className="caption"> — opens the exact version above. Reading records nothing.</span>
      </p>

      <div className="mt-4 flex flex-wrap items-center gap-3">
        <button
          className="button button--primary px-4 py-2 text-sm"
          type="button"
          onClick={accept}
          disabled={outcome.state === "submitting"}
        >
          {outcome.state === "submitting" ? "Recording…" : "Accept"}
        </button>

        {outcome.state === "failed" ? (
          /*
           * 🔴 The button is back at rest and the sentence says what did NOT happen. There is no
           * checkmark, no partial state, and no "we will try again in the background" — the customer
           * knows exactly where they stand, which is: nowhere, and they may retry.
           */
          <p className="text-sm text-bad" role="alert">
            {outcome.message} <span className="text-muted-foreground">You can try again.</span>
          </p>
        ) : null}

        {outcome.state === "recorded" ? (
          <p className="text-sm text-ok" role="status">
            Recorded — version {outcome.version}.
            {outstanding.length > 1 ? " One more document to go." : ""}
          </p>
        ) : null}
      </div>

      <p className="hint mt-3 max-w-none">
        This step is here because you are making a commitment. It does not affect anything else: the
        console keeps working, a run already in progress is untouched, and you can close this and come
        back.
      </p>
    </div>
  );
}

function label(kind: string): string {
  return { terms: "Terms of Service", privacy: "Privacy Notice" }[kind] ?? kind;
}
