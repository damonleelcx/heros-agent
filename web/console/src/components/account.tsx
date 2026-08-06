"use client";

import { useState } from "react";
import { PASSWORD_COPY } from "@/content/passwordAccount";

/**
 * The two controls on `/app/settings/account`.
 *
 * # Why these are client components when the unauthenticated password forms are not
 *
 * The forms at `/signin`, `/create-account` and `/reset-password` are native posts with no client
 * JavaScript, for the three reasons the session route's header gives — chief among them that sign-in must
 * work before hydration, because it is the one surface a user cannot get past.
 *
 * Neither of these is that surface. The reader is already signed in, already past every gate, and what
 * they need here is the thing a full-page post cannot give: the result of a change reported in place,
 * beside the field they changed, without losing the rest of the page. 🔴 The password still never lands in
 * component state longer than the submit — it is read from the form, sent, and the field is cleared.
 */

async function post(path: string, body: unknown): Promise<{ ok: boolean; error: string }> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  let data: { error?: string } = {};
  try {
    data = (await res.json()) as typeof data;
  } catch {
    // A body-less refusal is still a refusal; the status decides.
  }
  return { ok: res.ok, error: data.error ?? "" };
}

export function ResendConfirmation({ email }: { email: string }) {
  const [state, setState] = useState<"idle" | "busy" | "sent">("idle");
  return (
    <div className="flex items-center gap-3">
      <button
        type="button"
        disabled={state !== "idle"}
        onClick={async () => {
          setState("busy");
          await post("/api/console/account/resend-confirmation", { email });
          // 🔴 Reports "sent" whatever happened, matching the platform's neutral answer. This surface is
          // authenticated, so the enumeration argument does not apply here — but the platform gives one
          // answer and a console that rendered two would be claiming to know something it was not told.
          setState("sent");
        }}
        className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-40"
      >
        {PASSWORD_COPY.unverified.resend}
      </button>
      {state === "sent" ? (
        <span className="text-xs text-muted-foreground">{PASSWORD_COPY.unverified.resent}</span>
      ) : null}
    </div>
  );
}

export function ChangePasswordForm() {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  return (
    <form
      className="flex max-w-sm flex-col gap-3"
      onSubmit={async (event) => {
        event.preventDefault();
        const form = event.currentTarget;
        const data = new FormData(form);
        setBusy(true);
        setMessage(null);
        const outcome = await post("/api/console/account/password", {
          current_password: String(data.get("current_password") ?? ""),
          new_password: String(data.get("new_password") ?? ""),
        });
        setBusy(false);
        // 🔴 Cleared on BOTH paths. A refused change that leaves the old password sitting in a field is a
        // password on screen behind whoever walks past, and in the browser's form-restore cache.
        form.reset();
        setMessage(
          outcome.ok
            ? { tone: "ok", text: "Password changed. Every other session has been signed out." }
            : { tone: "bad", text: outcome.error || "That change was refused." },
        );
      }}
    >
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        Current password
        <input
          name="current_password"
          type="password"
          autoComplete="current-password"
          required
          className="rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground"
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        {PASSWORD_COPY.reset.passwordLabel}
        <input
          name="new_password"
          type="password"
          autoComplete="new-password"
          minLength={12}
          required
          className="rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground"
        />
        {/* The rule, before submission — the same discipline the sign-up form follows. */}
        <span className="text-xs text-muted-foreground">{PASSWORD_COPY.signUp.passwordHelp}</span>
      </label>
      <button
        type="submit"
        disabled={busy}
        className="rounded-md border border-border px-4 py-2 text-sm disabled:opacity-40"
      >
        Change password
      </button>
      {message ? (
        <p className={message.tone === "ok" ? "text-xs text-muted-foreground" : "text-xs text-[var(--danger)]"}>
          {message.text}
        </p>
      ) : null}
    </form>
  );
}
