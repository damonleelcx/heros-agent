"use client";

import { useState } from "react";
import { AlertTriangle, ShieldCheck, Trash2 } from "lucide-react";
import type {
  CloneCause,
  CloneRecordView,
  ConnectionView,
  ForgeDescription,
  LocalModeAvailability,
  SourceForge,
} from "@/lib/types.generated";
import { Banner, Chip } from "@/components/primitives";
import { instant, shortHash } from "@/lib/format";

/**
 * connections.tsx is P32 §6 — the repository-intake controls.
 *
 * # Why the consent screen is a STEP and not a paragraph beside the button
 *
 * FR10: the customer is shown what the grant permits, that it is usable when they are not present,
 * and how to revoke it — and *"authorization cannot be completed without that disclosure having been
 * displayed."* A paragraph beside a button satisfies the letter and not the requirement: it is
 * scrolled past, and nothing about the flow records that it was ever on screen.
 *
 * So the flow is two screens. The first states the grant and has one control, `I understand — continue`.
 * The second collects the authorization. The browser sends `consent_shown` only after the first was
 * rendered, the BFF refuses without it, and the PLATFORM refuses without it — three layers, because a
 * check that lives only in the browser is a check a second client does not have.
 *
 * # 🔴 The unattended-use line is not a bullet
 *
 * §8.2: *"state the boundary out loud in the consent screen: a connection is usable when you are not
 * present."* That is the one thing about this grant a customer is least likely to have expected, and
 * putting it fourth in a list is how it gets skipped. It has its own block, its own weight and the
 * caution palette — which is a WARNING about a property, not a hazard about an action.
 *
 * # Why the four causes are four components' worth of copy
 *
 * FR11 and §6.4. A rotated token and a renamed default branch are different people's problems on
 * different days, and each message names WHOSE problem it is and what closes it. The mapping is
 * exhaustive over the generated `CloneCause` union, so a fifth cause added in Go is a TypeScript error
 * here rather than a blank card in a list of reads.
 */

// ── the four failure messages (§6.4) ─────────────────────────────────────────

/**
 * CAUSE_COPY is the whole of FR11 on this side.
 *
 * `Record<CloneCause, …>` rather than a lookup with a fallback: the fallback is what turns a fifth
 * cause into a blank card, and a blank card in a list of reads is indistinguishable from a read that
 * succeeded.
 */
const CAUSE_COPY: Record<CloneCause, { title: string; whose: string; next: string }> = {
  credential_rejected: {
    title: "The forge refused our credential",
    whose: "Yours to fix, on the forge.",
    next: "The grant was revoked, expired, or its permissions were changed on your side. Re-authorize the connection, or rotate the token and reconnect.",
  },
  repository_not_found: {
    title: "The repository could not be found",
    whose: "Yours to fix, on the forge.",
    next: "It was renamed, deleted, or made private in a way the grant no longer covers. Every forge answers the same way for “does not exist” and “you may not see it”, so we cannot tell you which — check the repository, then reconnect.",
  },
  revision_not_found: {
    title: "That revision is not in the repository",
    whose: "Yours to fix, in the repository.",
    next: "The commit was rebased away, or the branch it was on was deleted. This is not a credential problem — the repository was reachable. Ask for a revision that still exists.",
  },
  network: {
    title: "We could not reach the forge",
    whose: "Ours to fix.",
    next: "Nothing about your repository or your grant is wrong. The read will be retried; if this persists, it is a fault on our side and the per-forge figures on our health endpoint will show it.",
  },
};

/** CloneFailure renders one failure as its own message. */
export function CloneFailure({ cause, at }: { cause: CloneCause; at?: string }) {
  const copy = CAUSE_COPY[cause];
  return (
    <Banner tone="warn" title={copy.title}>
      <p>
        <strong>{copy.whose}</strong> {copy.next}
      </p>
      {at ? <p className="hint">Last attempted {at}.</p> : null}
    </Banner>
  );
}

/** causeLabel is the short form, for a table cell. */
export function causeLabel(cause: CloneCause): string {
  return CAUSE_COPY[cause].title;
}

// ── the consent screen (§6.2) ────────────────────────────────────────────────

/**
 * ConsentScreen states what the grant permits, per forge, before anything is authorized.
 *
 * Every sentence about the grant comes from `ForgeDescription` — the platform's own forge adapter —
 * rather than from a string here. A sentence maintained in the console cannot be checked against the
 * code that builds the grant, and the three forges genuinely differ (an App on one, a project token on
 * another), so a generic sentence would be true of none of them.
 */
function ConsentScreen({
  forge,
  repository,
  retentionHours,
  onContinue,
  onCancel,
}: {
  forge: ForgeDescription;
  repository: string;
  retentionHours: number;
  onContinue: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="conn__grant" data-testid="consent-screen">
      <p className="conn__grant-title">
        <ShieldCheck className="mr-2 inline size-4 align-[-0.15em]" aria-hidden="true" />
        What you are about to permit
      </p>
      <ul className="conn__grant-list list-disc">
        <li>
          We will be able to <strong>read</strong> {repository ? <code className="mono">{repository}</code> : "one repository"} and
          nothing else. The grant is {forge.grant_label}, carrying {forge.permission}.
        </li>
        <li>
          It <strong>cannot write</strong>. There is no scope on it that can push a ref, open a pull
          request, or change a setting.
        </li>
        <li>
          A tree we read is deleted after <strong>{retentionHours} hours</strong>. Revoking deletes it
          immediately, along with the grant.
        </li>
        <li>
          Every read is recorded — the revision, the reason, and whether a person asked for it. You can
          read that record here at any time.
        </li>
      </ul>

      {/* 🔴 §8.2 — the boundary, out loud. Its own block because it is the thing a customer is least
          likely to have expected, and a fourth bullet is a thing people skip. */}
      <p className="conn__unattended">
        <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <span>
          This connection is usable when you are not present. Scheduled and autonomous work will read
          this repository without asking again — that is what a connection is for, and it is the reason
          every read is recorded.
        </span>
      </p>

      <p className="hint">
        To revoke it on {forge.forge}: {forge.revoke_hint}. Revoking here deletes our grant, our copy of
        the credential, and every tree we derived from it.
      </p>

      <div className="conn__actions">
        <button type="button" className="conn__submit" onClick={onContinue}>
          I understand — continue
        </button>
        <button type="button" className="conn__cancel" onClick={onCancel}>
          Not now
        </button>
      </div>
    </div>
  );
}

// ── connect (§6.2) ───────────────────────────────────────────────────────────

type ConnectOutcome = { kind: "ok"; text: string } | { kind: "error"; text: string } | null;

/**
 * ConnectRepository is the three-step flow: NAME, disclose, authorize.
 *
 * # 🔴 Why naming comes before the disclosure, which was not the first design
 *
 * The first version disclosed first and collected everything after, and rendered-browser acceptance
 * caught what that produces: the consent screen described a **Bitbucket** grant — the first forge in
 * the list — to a customer who had not chosen a forge yet. FR10 requires the customer be shown what
 * *the* grant permits, and the three forges genuinely differ (an App on one, a project token on two).
 * A disclosure about a grant they then did not create is worse than no disclosure, because they would
 * remember having read one.
 *
 * So the forge and the repository are named FIRST, and the consent screen is about that exact grant.
 *
 * # Why the authorization fields are typed rather than fetched
 *
 * In production these arrive from the forge's own authorization redirect. This form is what a
 * deployment without that redirect wired uses, and — more usefully — it is what makes the BREADTH
 * REFUSAL visible: `covers` is what the forge says the grant reaches, and it is a separate field from
 * the repository being named precisely so the two can disagree. A form that derived `covers` from
 * `repository` would make ADR-013's Option B refusal untestable from this surface.
 */
export function ConnectRepository({
  forges,
  retentionHours,
}: {
  forges: ForgeDescription[];
  retentionHours: number;
}) {
  const [step, setStep] = useState<"closed" | "name" | "consent" | "authorize">("closed");
  const [forgeName, setForgeName] = useState<SourceForge>(forges[0]?.forge ?? "github");
  const [workflowId, setWorkflowId] = useState("");
  const [repository, setRepository] = useState("");
  const [subPath, setSubPath] = useState("");
  const [token, setToken] = useState("");
  const [covers, setCovers] = useState("");
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<ConnectOutcome>(null);

  const forge = forges.find((f) => f.forge === forgeName) ?? forges[0];
  if (!forge) {
    return (
      <Banner tone="warn" title="No forge is available">
        This deployment reports no code host it can connect to. Push a source bundle instead.
      </Banner>
    );
  }

  async function submit() {
    setBusy(true);
    setOutcome(null);
    const res = await fetch("/api/console/connections", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        workflow_id: workflowId.trim(),
        repository: repository.trim(),
        sub_path: subPath.trim(),
        forge: forgeName,
        grant_kind: forge.grant_kind,
        token,
        // Split on comma and newline, so a value pasted from a forge's own listing works.
        covers: covers
          .split(/[\n,]/)
          .map((c) => c.trim())
          .filter(Boolean),
        account_wide: false,
        // 🔴 Set only because ConsentScreen was rendered and its control pressed. This is the browser's
        // half of FR10; the BFF and the platform both refuse without it.
        consent_shown: true,
      }),
    });
    let data: { error?: string; repository?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    if (!res.ok) {
      // The platform's sentence, as written. It names WHICH repositories a too-broad grant covers, and
      // rewriting it here would lose the one detail the customer needs to narrow it.
      setOutcome({ kind: "error", text: data.error ?? "the connection was refused" });
      return;
    }
    setOutcome({ kind: "ok", text: `Connected ${data.repository ?? repository}. Nothing has been read yet.` });
    setStep("closed");
    setToken("");
  }

  if (step === "closed") {
    return (
      <div className="conn__actions">
        <button type="button" className="conn__submit" onClick={() => setStep("name")}>
          Connect a repository
        </button>
        {outcome ? (
          <span className={outcome.kind === "ok" ? "hint" : "text-sm text-foreground"}>{outcome.text}</span>
        ) : null}
      </div>
    );
  }

  // Step 1 — WHICH repository, on WHICH forge. Named before anything is disclosed, so the disclosure
  // that follows is about the grant the customer is actually about to create.
  if (step === "name") {
    const named = repository.trim().includes("/") && workflowId.trim() !== "";
    return (
      <div className="conn__form" data-testid="name-form">
        <label className="conn__label">
          Forge
          <select
            className="conn__select"
            value={forgeName}
            onChange={(e) => setForgeName(e.target.value as SourceForge)}
          >
            {forges.map((f) => (
              <option key={f.forge} value={f.forge}>
                {f.forge} — {f.grant_kind.replace(/_/g, " ")}
              </option>
            ))}
          </select>
        </label>
        <label className="conn__label">
          Workflow id
          <input className="conn__input" value={workflowId} onChange={(e) => setWorkflowId(e.target.value)} />
        </label>
        <label className="conn__label">
          Repository (owner/name)
          <input
            className="conn__input"
            value={repository}
            onChange={(e) => setRepository(e.target.value)}
            placeholder="acme/api"
          />
        </label>
        <label className="conn__label">
          Sub-path — optional, for a monorepo
          <input className="conn__input" value={subPath} onChange={(e) => setSubPath(e.target.value)} />
        </label>
        <p className="hint">
          A connection covers exactly one repository. A sub-path bounds what we actually read inside it;
          the grant itself stays repository-scoped, because no forge issues a narrower one.
        </p>
        <div className="conn__actions">
          {/* Disabled until both are named, so the next screen cannot describe a grant nobody has
              specified — which is the defect rendered-browser acceptance found in the first design. */}
          <button
            type="button"
            className="conn__submit"
            onClick={() => setStep("consent")}
            disabled={!named}
          >
            Continue
          </button>
          <button type="button" className="conn__cancel" onClick={() => setStep("closed")}>
            Cancel
          </button>
        </div>
      </div>
    );
  }

  // Step 2 — the disclosure, about THAT forge and THAT repository.
  if (step === "consent") {
    return (
      <ConsentScreen
        forge={forge}
        repository={repository}
        retentionHours={retentionHours}
        onContinue={() => setStep("authorize")}
        onCancel={() => setStep("name")}
      />
    );
  }

  // Step 3 — the authorization itself. The identity fields are NOT repeated here: they were named in
  // step 1 and disclosed in step 2, and a form that let them be edited after the disclosure would let
  // the grant differ from the one the customer read about.
  return (
    <div className="conn__form" data-testid="connect-form">
      <p className="text-sm text-muted-foreground">
        Authorizing <code className="mono">{repository}</code> on {forge.forge}
        {subPath ? (
          <>
            , rooted at <code className="mono">{subPath}</code>
          </>
        ) : null}
        .
      </p>
      <label className="conn__label">
        Repositories this grant covers, as {forge.forge} reports them
        <input
          className="conn__input"
          value={covers}
          onChange={(e) => setCovers(e.target.value)}
          placeholder="acme/api"
        />
      </label>
      <label className="conn__label">
        The credential {forge.forge} issued
        <input
          className="conn__input"
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          autoComplete="off"
        />
      </label>
      <p className="hint">
        The credential is stored in this deployment&apos;s secret store and is never returned by any
        page, log line or record. A grant covering anything other than the repository you named is
        refused here.
      </p>
      <div className="conn__actions">
        <button type="button" className="conn__submit" onClick={submit} disabled={busy || !token}>
          {busy ? "Connecting…" : "Authorize this connection"}
        </button>
        {/* Back to the DISCLOSURE, not to the start. Somebody who wants to re-read what they are
            permitting should not have to re-type the repository to see it. */}
        <button type="button" className="conn__cancel" onClick={() => setStep("consent")}>
          Back
        </button>
      </div>
      {outcome ? <Banner tone={outcome.kind === "ok" ? "info" : "warn"} title={outcome.text} /> : null}
    </div>
  );
}

// ── revoke (§6.3) ────────────────────────────────────────────────────────────

/**
 * RevokeConnection is the hazard control.
 *
 * # Why it arms rather than confirming in a dialog
 *
 * The confirmation has to STATE what will be deleted — the grant, our copy of the credential, and every
 * tree we derived from it — and a `window.confirm()` cannot carry that, cannot be styled in the hazard
 * palette, and cannot be read by anything that checks the console's copy. The armed panel can.
 *
 * # 🔴 The receipt is a NUMBER
 *
 * The platform returns how many derived trees the cascade deleted, and it is shown. A confirmation
 * that repeats "derived trees are deleted" after the fact is the same claim twice; a count is evidence
 * — and it is the one number that would be zero if the cascade's second half were missing, which is
 * exactly the failure design D3 says is invisible from the inside.
 */
export function RevokeConnection({ connection }: { connection: ConnectionView }) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  async function revoke() {
    setBusy(true);
    const res = await fetch("/api/console/connections/revoke", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ connection_id: connection.connection_id }),
    });
    let data: { error?: string; snapshots_deleted?: number } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    setArmed(false);
    if (!res.ok) {
      // 🔴 The platform's sentence verbatim. A partially-completed cascade is retryable and its message
      // says so; replacing it with "revocation failed" would leave a customer who asked us to stop
      // holding their source unable to tell how far it got.
      setResult({ ok: false, text: data.error ?? "the revocation could not be completed" });
      return;
    }
    const n = data.snapshots_deleted ?? 0;
    setResult({
      ok: true,
      text: `Revoked. The grant and our copy of the credential are deleted, along with ${n} derived ${
        n === 1 ? "tree" : "trees"
      }. A read of this workflow now reports no source.`,
    });
  }

  if (result) {
    return <Banner tone={result.ok ? "info" : "warn"} title={result.text} />;
  }

  if (!armed) {
    return (
      // `self-start` because the card is a flex COLUMN and a stretched destructive control reads as a
      // banner. A full-width red bar is the shape of an alert, not of a button somebody has to choose
      // to press — and this is the one control on the page that must look like a deliberate act.
      <button type="button" className="conn__revoke self-start" onClick={() => setArmed(true)}>
        <Trash2 className="size-4" aria-hidden="true" />
        Revoke
      </button>
    );
  }

  return (
    <div className="conn__confirm" data-testid="revoke-confirm">
      <p className="text-sm text-foreground">
        Revoking <code className="mono">{connection.repository}</code> deletes three things and cannot be
        undone:
      </p>
      <ul className="conn__grant-list list-disc text-sm text-muted-foreground">
        <li>the grant, so we can no longer read the repository at all;</li>
        <li>our copy of the credential;</li>
        <li>
          <strong>every tree we derived from it</strong> — a read of this workflow will report no source
          rather than answering from what we already held.
        </li>
      </ul>
      <p className="hint">
        This does not revoke it on {connection.forge}. To do that as well: {connection.revoke_hint}.
      </p>
      <div className="conn__actions">
        <button type="button" className="conn__revoke" onClick={revoke} disabled={busy}>
          <Trash2 className="size-4" aria-hidden="true" />
          {busy ? "Revoking…" : "Revoke and delete the derived trees"}
        </button>
        <button type="button" className="conn__cancel" onClick={() => setArmed(false)}>
          Keep the connection
        </button>
      </div>
    </div>
  );
}

// ── local mode (§4, §6.5) ────────────────────────────────────────────────────

/**
 * PairLocalRepository is Mode 3's console half.
 *
 * # 🚫 There is no file picker, and the absence is the design
 *
 * A control that read a folder and posted it would say "select a local repo" and produce Mode 1's
 * data-handling outcome. That is a consent failure, not a shortcut (design D5). What this does instead
 * is hand out a code for a terminal that is ALREADY on the machine holding the repository.
 *
 * # FR15 — the limit is stated BEFORE the flow, never after it
 *
 * When `availability.available` is false the button is not rendered at all and the reason is shown in
 * its place. A disabled button with a tooltip would still be a flow the reader starts.
 */
export function PairLocalRepository({
  availability,
  command,
}: {
  availability: LocalModeAvailability;
  command: string;
}) {
  const [workflowId, setWorkflowId] = useState("");
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (!availability.available) {
    return (
      <Banner tone="info" title="Reading a repository in place is not available on this deployment">
        <p>{availability.why}</p>
        <p className="hint">
          It works against {(availability.deployments ?? []).join(", ") || "no deployment this build knows of"}.
        </p>
      </Banner>
    );
  }

  async function start() {
    setBusy(true);
    setError(null);
    const res = await fetch("/api/console/pairings", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ workflow_id: workflowId.trim() }),
    });
    let data: { error?: string; user_code?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    if (!res.ok) {
      setError(data.error ?? "a pairing code could not be issued");
      return;
    }
    setCode(data.user_code ?? null);
  }

  return (
    <div className="conn__form">
      <p className="text-sm text-muted-foreground">
        The repository is read <strong>on your machine</strong> and its contents are never transmitted.
        We show a code; you type it into a terminal that is already there.
      </p>
      <label className="conn__label">
        Workflow id
        <input className="conn__input" value={workflowId} onChange={(e) => setWorkflowId(e.target.value)} />
      </label>
      {code ? (
        <>
          <p className="conn__code" data-testid="pairing-code">
            {code}
          </p>
          <p className="hint">
            Run this on the machine holding the repository, within ten minutes:
          </p>
          <p className="mono text-sm text-foreground">{command.replace("<the code above>", code)}</p>
        </>
      ) : (
        <div className="conn__actions">
          <button type="button" className="conn__submit" onClick={start} disabled={busy}>
            {busy ? "Starting…" : "Show me a pairing code"}
          </button>
        </div>
      )}
      {error ? <Banner tone="warn" title={error} /> : null}
    </div>
  );
}

// ── the read ledger (FR9) ────────────────────────────────────────────────────

/**
 * ReadLedger is the record FR9 requires be readable by the customer.
 *
 * # Why it loads on demand
 *
 * It is what somebody opens when they have a question — "what did it read while I was on leave" — and
 * loading every connection's ledger on every page view to serve a question most readers never ask
 * makes the page slower for everyone.
 *
 * # 🔴 A FAILED read is a row here, beside the successful ones
 *
 * A ledger of successes only cannot answer "when did it start failing", which is the question asked
 * immediately after a token is rotated — and that question arriving with no answer is what makes a
 * customer stop trusting the ledger for the successes as well.
 */
export function ReadLedger({ connectionId }: { connectionId: string }) {
  const [open, setOpen] = useState(false);
  const [records, setRecords] = useState<CloneRecordView[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function toggle() {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    if (records) return;
    const res = await fetch(`/api/console/connections/reads?connection_id=${encodeURIComponent(connectionId)}`);
    let data: { records?: CloneRecordView[]; error?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    if (!res.ok) {
      setError(data.error ?? "the read record could not be loaded");
      return;
    }
    setRecords(data.records ?? []);
  }

  return (
    <div className="flex flex-col gap-3">
      <button type="button" className="conn__cancel self-start" onClick={toggle} aria-expanded={open}>
        {open ? "Hide every read" : "Show every read of this repository"}
      </button>
      {open && error ? <Banner tone="warn" title={error} /> : null}
      {open && records && records.length === 0 ? (
        <p className="hint">This connection has never been used.</p>
      ) : null}
      {open && records && records.length > 0 ? (
        <table className="conn__ledger" data-testid="read-ledger">
          <caption className="visually-hidden">
            Every read of this repository — when, at which revision, who asked, and how it ended.
          </caption>
          <thead>
            <tr>
              <th scope="col">When</th>
              <th scope="col">Revision</th>
              <th scope="col">Who</th>
              <th scope="col">Outcome</th>
            </tr>
          </thead>
          <tbody>
            {records.map((r) => (
              <tr key={r.record_id}>
                {/* Through the ONE swap point (src/lib/format.ts), never a formatter built here —
                    see scan-strings.mjs. A page that builds its own is a page whose dates change when
                    somebody's browser language does. */}
                <td>{instant(r.at_ms)}</td>
                <td className="mono">{shortHash(r.revision)}</td>
                {/* The FR9 distinction, written out rather than abbreviated: the whole reason this
                    ledger exists is that the grant is usable when nobody is present. */}
                <td>
                  {r.actor === "person"
                    ? `a person${r.actor_id ? ` (${r.actor_id})` : ""}`
                    : "a scheduled process, with nobody present"}
                </td>
                <td>
                  {r.outcome === "succeeded" ? (
                    <Chip tone="ok">read</Chip>
                  ) : (
                    <CauseChip cause={r.outcome} />
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
    </div>
  );
}

// ── the mode chip ────────────────────────────────────────────────────────────

/**
 * ModeChip renders how a workflow's source arrives.
 *
 * 🚫 `bundle` is NOT rendered as a lesser state. No feature is gated on a connection (FR12), and a
 * chip that greyed the default mode out would be telling every Mode 1 customer they are missing
 * something they are not.
 */
export function ModeChip({ mode }: { mode: ConnectionView["mode"] }) {
  const label =
    mode === "connected" ? "connected repository" : mode === "local" ? "read on your machine" : "pushed bundle";
  return <Chip tone="info">{label}</Chip>;
}

/** CauseChip renders a failure cause in a table row. */
export function CauseChip({ cause }: { cause: CloneCause }) {
  return <Chip tone="warn">{causeLabel(cause)}</Chip>;
}
