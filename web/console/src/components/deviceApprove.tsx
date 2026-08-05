"use client";

import { useState } from "react";

/**
 * The terminal-approval control (P27 task 13.1).
 *
 * # Why approve and deny are two buttons rather than one and an X
 *
 * Denying is a real outcome with a real effect: it ends the terminal's wait immediately instead of making
 * somebody watch a spinner until the code expires. It is also the button a person presses when they did
 * NOT start this login — which is the case that matters most, because a code somebody else is trying to
 * get approved is the only attack this flow has, and the response to it should be one press.
 */
export function ApproveDevice() {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<{ kind: "approved" | "denied" | "error"; text: string } | null>(null);

  async function decide(approve: boolean) {
    setBusy(true);
    setOutcome(null);
    const res = await fetch("/api/console/organization/device", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ user_code: code, approve }),
    });
    let data: { error?: string; device_label?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    if (!res.ok) {
      // The platform's sentence, shown as written. It deliberately does not say whether the code was
      // denied, expired or never existed — the difference helps only somebody guessing codes — so this
      // must not try to improve on it.
      setOutcome({ kind: "error", text: data.error ?? "that code is not waiting for approval" });
      return;
    }
    setOutcome(
      approve
        ? { kind: "approved", text: `Approved${data.device_label ? ` — ${data.device_label}` : ""}. Your terminal is signed in.` }
        : { kind: "denied", text: "Denied. That terminal was not signed in." },
    );
    setCode("");
  }

  return (
    <div className="flex flex-col gap-4">
      <label className="flex flex-col gap-2 text-sm">
        <span className="font-medium">Code from your terminal</span>
        <input
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="ABCD-EFGH"
          autoComplete="off"
          spellCheck={false}
          /* Uppercase for readability only. The platform normalizes case, spaces and hyphens, so a code
             retyped as `abcd efgh` is the same code — the separator is presentational. */
          className="rounded-md border px-3 py-2 font-mono uppercase tracking-widest"
          aria-describedby="device-code-help"
        />
      </label>
      <p id="device-code-help" className="text-sm opacity-80">
        Approving signs that terminal in as you, in this organization. Removing you from the organization
        ends it, at its next request.
      </p>
      <div className="flex gap-3">
        <button
          type="button"
          disabled={busy || code.trim() === ""}
          onClick={() => void decide(true)}
          className="rounded-md px-4 py-2 font-medium disabled:opacity-50"
        >
          {busy ? "Working…" : "Approve"}
        </button>
        <button
          type="button"
          disabled={busy || code.trim() === ""}
          onClick={() => void decide(false)}
          className="rounded-md border px-4 py-2 disabled:opacity-50"
        >
          I did not start this
        </button>
      </div>
      {outcome ? (
        <p role="status" className={outcome.kind === "error" ? "text-sm font-medium" : "text-sm"}>
          {outcome.text}
        </p>
      ) : null}
    </div>
  );
}
