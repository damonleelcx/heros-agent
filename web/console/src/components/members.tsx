"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { REMOVAL_COPY, CREDENTIAL_COPY, refusalCopy } from "@/lib/organizationCopy";

/**
 * members.tsx is the only interactive part of the members surface.
 *
 * # What the client is allowed to do here, and what it is not
 *
 * It calls four BFF routes and re-renders. It derives NOTHING: no seat count, no state word, no
 * eligibility. Every number and every state label on that page came from the platform, and the reason is
 * the console's own standing rule — a client-side recomputation is a second source of truth for a claim
 * the server already made.
 *
 * # 🔴 The removal dialog exists for its SECOND list
 *
 * Removing somebody ends their sessions and their personal keys, and leaves the organization's machine
 * keys running. A confirmation that showed only the first half would have somebody attest to an
 * offboarding that is wrong — and a CI key the departing engineer created keeps deploying, discovered
 * later by a build that breaks or, worse, one that does not.
 *
 * So the dialog fetches the platform's own PREVIEW before it will confirm anything, and renders both
 * lists. It is not computed here: the console does not know which keys are personal, and guessing from
 * a name is how the wrong key gets revoked.
 */

type Preview = {
  user_id: string;
  email: string;
  last_owner: boolean;
  sessions_to_revoke: number;
  credentials_revoked: { credential_id: string; label: string; kind: string }[];
  credentials_retained: { credential_id: string; label: string; kind: string }[];
};

type Refusal = { title: string; body: string } | null;

async function post(path: string, body?: unknown, method = "POST"): Promise<{ ok: boolean; data: unknown }> {
  const res = await fetch(path, {
    method,
    headers: body ? { "content-type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    // A body-less response is legitimate for a DELETE. The status decides.
  }
  return { ok: res.ok, data };
}

function refusalFrom(data: unknown): Refusal {
  const body = (data ?? {}) as { reason_code?: string; error?: string };
  // Branch on the CODE, never on the prose. The sentence is what we SHOW; the code is what we decide on.
  return refusalCopy(body.reason_code, body.error ?? "");
}

export function MemberActions({
  userId,
  email,
  role,
  lastOwner,
  canPromoteToOwner,
  canRemove,
}: {
  userId: string;
  email: string;
  role: string;
  lastOwner: boolean;
  /** An admin may not make somebody an owner. The option is ABSENT rather than disabled: a disabled
   *  option in a select is a thing people try to click, and there is nothing to explain per-option. */
  canPromoteToOwner: boolean;
  /** An admin may not remove an owner. The button is absent, not refused after the press. */
  canRemove: boolean;
}) {
  const router = useRouter();
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>(null);

  async function openPreview() {
    setBusy(true);
    setRefusal(null);
    const res = await post(`/api/console/organization/members/${encodeURIComponent(userId)}/removal-preview`, undefined, "GET");
    setBusy(false);
    if (!res.ok) {
      setRefusal(refusalFrom(res.data));
      return;
    }
    setPreview(res.data as Preview);
  }

  async function confirmRemoval() {
    setBusy(true);
    const res = await post(`/api/console/organization/members/${encodeURIComponent(userId)}`, undefined, "DELETE");
    setBusy(false);
    if (!res.ok) {
      setRefusal(refusalFrom(res.data));
      return;
    }
    setPreview(null);
    router.refresh();
  }

  async function changeRole(next: string) {
    setBusy(true);
    setRefusal(null);
    const res = await post(`/api/console/organization/members/${encodeURIComponent(userId)}/role`, { role: next });
    setBusy(false);
    if (!res.ok) {
      setRefusal(refusalFrom(res.data));
      return;
    }
    router.refresh();
  }

  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex items-center gap-2">
        <label className="sr-only" htmlFor={`role-${userId}`}>
          Role for {email}
        </label>
        <select
          id={`role-${userId}`}
          className="rounded-md border border-border bg-background px-2 py-1 text-xs"
          defaultValue={role}
          disabled={busy}
          onChange={(e) => void changeRole(e.target.value)}
        >
          {canPromoteToOwner ? <option value="owner">owner</option> : null}
          <option value="admin">admin</option>
          <option value="member">member</option>
        </select>
        {canRemove ? (
          <button
            type="button"
            className="rounded-md border border-border px-2 py-1 text-xs disabled:opacity-40"
            // 🔴 DISABLED here, not absent — and the difference is deliberate. "You may not remove
            // anybody" is a role boundary and the control is absent for it. "This person cannot be
            // removed because they are the only owner" is a STATE of this row, with a remedy the viewer
            // can act on, so the control stays and carries the reason.
            disabled={busy || lastOwner}
            title={lastOwner ? "This organization would be left with nobody who can administer it" : undefined}
            onClick={() => void openPreview()}
          >
            Remove…
          </button>
        ) : null}
      </div>

      {refusal ? (
        <p className="max-w-xs text-right text-xs text-[var(--danger)]">
          <strong>{refusal.title}</strong> {refusal.body}
        </p>
      ) : null}

      {preview ? (
        <div
          role="dialog"
          aria-label={REMOVAL_COPY.title}
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
        >
          <div className="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded-xl border border-border bg-background p-5 text-left">
            <h2 className="font-display text-lg">{REMOVAL_COPY.title}</h2>
            <p className="mt-1 text-sm text-muted-foreground">{preview.email}</p>

            <h3 className="mt-4 text-sm font-medium">{REMOVAL_COPY.willEnd}</h3>
            <p className="text-xs text-muted-foreground">{REMOVAL_COPY.willEndHelp}</p>
            <ul className="mt-2 list-disc pl-5 text-sm">
              <li>
                {preview.sessions_to_revoke} browser session
                {preview.sessions_to_revoke === 1 ? "" : "s"}
              </li>
              {preview.credentials_revoked.map((c) => (
                <li key={c.credential_id}>
                  {c.label} <span className="text-xs text-muted-foreground">({CREDENTIAL_COPY.personalLabel})</span>
                </li>
              ))}
            </ul>

            {/*
              🔴 THE SECOND LIST. This is the reason the dialog exists at all, and it is rendered even
              when empty — "no organization keys are affected" is an answer, and its absence would read
              as the question not having been asked.
            */}
            <h3 className="mt-4 text-sm font-medium text-[var(--warn)]">{REMOVAL_COPY.willRemain}</h3>
            <p className="text-xs text-muted-foreground">{REMOVAL_COPY.willRemainHelp}</p>
            {preview.credentials_retained.length === 0 ? (
              <p className="mt-2 text-sm">{REMOVAL_COPY.nothingToRemain}</p>
            ) : (
              <ul className="mt-2 list-disc pl-5 text-sm">
                {preview.credentials_retained.map((c) => (
                  <li key={c.credential_id}>
                    {c.label} <span className="text-xs text-muted-foreground">({CREDENTIAL_COPY.machineLabel})</span>
                  </li>
                ))}
              </ul>
            )}

            <div className="mt-5 flex justify-end gap-2">
              <button
                type="button"
                className="rounded-md border border-border px-3 py-1.5 text-sm"
                onClick={() => setPreview(null)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="rounded-md border border-[var(--danger)] px-3 py-1.5 text-sm text-[var(--danger)] disabled:opacity-40"
                disabled={busy || preview.last_owner}
                onClick={() => void confirmRemoval()}
              >
                {REMOVAL_COPY.confirm}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

export function InviteForm() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>(null);
  const [sent, setSent] = useState<string | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setRefusal(null);
    setSent(null);
    const res = await post("/api/console/organization/invitations", { email, role });
    setBusy(false);
    if (!res.ok) {
      setRefusal(refusalFrom(res.data));
      return;
    }
    setSent(email);
    setEmail("");
    router.refresh();
  }

  return (
    <form onSubmit={submit} className="flex flex-wrap items-end gap-2">
      <div className="flex flex-col gap-1">
        <label htmlFor="invite-email" className="text-xs text-muted-foreground">
          Work address
        </label>
        <input
          id="invite-email"
          type="email"
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
          placeholder="colleague@acme.com"
        />
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor="invite-role" className="text-xs text-muted-foreground">
          Role
        </label>
        <select
          id="invite-role"
          value={role}
          onChange={(e) => setRole(e.target.value)}
          className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
        >
          <option value="member">member</option>
          <option value="admin">admin</option>
          <option value="owner">owner</option>
        </select>
      </div>
      <button
        type="submit"
        disabled={busy}
        className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-40"
      >
        Send invitation
      </button>

      {refusal ? (
        <p className="w-full text-xs text-[var(--danger)]">
          <strong>{refusal.title}</strong> {refusal.body}
        </p>
      ) : null}
      {sent ? (
        <p className="w-full text-xs text-muted-foreground">
          Invitation created for {sent}. The link fills in their address; signing in with that account is
          what joins them.
        </p>
      ) : null}
    </form>
  );
}

export function CredentialActions() {
  const router = useRouter();
  const [label, setLabel] = useState("");
  const [kind, setKind] = useState("personal");
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>(null);
  const [secret, setSecret] = useState<string | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setRefusal(null);
    const res = await post("/api/console/organization/credentials", { label, kind });
    setBusy(false);
    if (!res.ok) {
      setRefusal(refusalFrom(res.data));
      return;
    }
    const body = res.data as { secret?: string };
    // 🔴 Held in component state only, and never sent anywhere. This is the one moment the value exists;
    // writing it to storage would make "shown once" false.
    setSecret(body.secret ?? null);
    setLabel("");
    router.refresh();
  }

  return (
    <div className="flex flex-col gap-2">
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2">
        <div className="flex flex-col gap-1">
          <label htmlFor="cred-label" className="text-xs text-muted-foreground">
            Label
          </label>
          <input
            id="cred-label"
            required
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
            placeholder="dana's laptop"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label htmlFor="cred-kind" className="text-xs text-muted-foreground">
            Kind
          </label>
          <select
            id="cred-kind"
            value={kind}
            onChange={(e) => setKind(e.target.value)}
            className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
          >
            {/* Asked for, never inferred: the answer decides whether removing this person revokes it. */}
            <option value="personal">personal — mine, revoked when I leave</option>
            <option value="machine">machine — the organization&apos;s, survives a departure</option>
          </select>
        </div>
        <button
          type="submit"
          disabled={busy}
          className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-40"
        >
          Create key
        </button>
      </form>

      {refusal ? (
        <p className="text-xs text-[var(--danger)]">
          <strong>{refusal.title}</strong> {refusal.body}
        </p>
      ) : null}

      {secret ? (
        <div className="rounded-md border border-[var(--warn)] p-3">
          <p className="text-xs text-muted-foreground">{CREDENTIAL_COPY.secretOnce}</p>
          <code className="mt-2 block break-all font-mono text-sm">{secret}</code>
          <button
            type="button"
            className="mt-2 rounded-md border border-border px-2 py-1 text-xs"
            onClick={() => setSecret(null)}
          >
            I have copied it
          </button>
        </div>
      ) : null}
    </div>
  );
}
