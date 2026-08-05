"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { SIGNUP_COPY, refusalCopy } from "@/lib/organizationCopy";

/**
 * The organization-creation form.
 *
 * One field. Everything else — who you are, which identity provider vouched for you — comes from the
 * verified assertion on the server side, and there is no input here that could carry it.
 */
export function SignUpForm() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<{ title: string; body: string } | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setRefusal(null);
    const res = await fetch("/api/console/organization/signup", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ name }),
    });
    let data: { reason_code?: string; error?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    if (!res.ok) {
      // Branch on the CODE. The prose is what we show; a copy edit must not change behaviour.
      setRefusal(refusalCopy(data.reason_code, data.error ?? ""));
      return;
    }
    router.push("/app");
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <label htmlFor="org-name" className="text-xs text-muted-foreground">
          {SIGNUP_COPY.nameLabel}
        </label>
        <input
          id="org-name"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={SIGNUP_COPY.namePlaceholder}
          className="rounded-md border border-border bg-background px-3 py-2 text-sm"
        />
        <p className="text-xs text-muted-foreground">{SIGNUP_COPY.nameHelp}</p>
      </div>
      <button
        type="submit"
        disabled={busy}
        className="rounded-md border border-border px-4 py-2 text-sm disabled:opacity-40"
      >
        {SIGNUP_COPY.submit}
      </button>
      {refusal ? (
        <p className="text-xs text-[var(--danger)]">
          <strong>{refusal.title}</strong> {refusal.body}
        </p>
      ) : null}
    </form>
  );
}
