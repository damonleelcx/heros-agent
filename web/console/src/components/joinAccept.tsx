"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { refusalCopy } from "@/lib/organizationCopy";

/**
 * The accept control.
 *
 * A POST behind a press, never an effect on render: a GET that changes state is a link a mail scanner
 * follows, and an invitation spent by a link-preview bot is an invitation nobody can use.
 */
export function AcceptInvitation({ invitationId }: { invitationId: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<{ title: string; body: string } | null>(null);

  async function accept() {
    setBusy(true);
    setRefusal(null);
    const res = await fetch("/api/console/organization/join", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ invitation_id: invitationId }),
    });
    let data: { reason_code?: string; error?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    if (!res.ok) {
      // Branch on the CODE. `invitation_identity_mismatch` and `invitation_expired` need different
      // words and different next actions, and the sentence is what we show rather than what we decide on.
      setRefusal(refusalCopy(data.reason_code, data.error ?? ""));
      return;
    }
    router.push("/app");
  }

  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        onClick={() => void accept()}
        disabled={busy}
        className="self-start rounded-md border border-border px-4 py-2 text-sm disabled:opacity-40"
      >
        Join this organization
      </button>
      {refusal ? (
        <p className="text-sm text-[var(--danger)]">
          <strong>{refusal.title}</strong> {refusal.body}
        </p>
      ) : null}
    </div>
  );
}
